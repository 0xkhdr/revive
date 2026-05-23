"""RestoreService implementing the 14-step deterministic apply order."""

import hashlib
import logging
import os
import socket

import yaml
from pydantic import ValidationError

from rv.logging.audit import AuditLogger
from rv.models.manifest import Asset, Manifest, Secret
from rv.models.transaction import Lockfile, LockfileEntry
from rv.providers.apt import AptProvider
from rv.providers.brew import BrewProvider
from rv.providers.cargo import CargoProvider
from rv.providers.dnf import DnfProvider
from rv.providers.docker import DockerProvider
from rv.providers.flatpak import FlatpakProvider
from rv.providers.nix import NixProvider
from rv.providers.node import NodeProvider
from rv.providers.pacman import PacmanProvider
from rv.providers.pip import PipProvider
from rv.providers.snap import SnapProvider
from rv.security.scrubber import SecretScrubber
from rv.services.handlers import AssetHandler
from rv.transactions.atomic import AtomicWrite
from rv.transactions.context import TransactionContext
from rv.transactions.lock import ProcessLock
from rv.utils.interpolate import Interpolator
from rv.utils.path import PathHelper

logger = AuditLogger.get_logger("rv.services.restore")


class ManifestLoader:
    """Loads and validates the revive manifest file."""

    @staticmethod
    def load(manifest_path: str) -> Manifest:
        """Parses the YAML manifest file and returns a validated Pydantic model.

        Args:
            manifest_path: Absolute path to manifest.yaml file.

        Returns:
            Validated Manifest instance.
        """
        if not os.path.exists(manifest_path):
            raise FileNotFoundError(f"Manifest file not found at: {manifest_path}")

        try:
            with open(manifest_path, encoding="utf-8") as f:
                raw_data = yaml.safe_load(f)
        except Exception as e:
            raise ValueError(f"Failed to parse YAML manifest: {e}") from e

        if not isinstance(raw_data, dict):
            raise ValueError("Manifest content must be a dictionary")

        try:
            return Manifest.model_validate(raw_data)
        except ValidationError as e:
            raise ValueError(f"Manifest validation failed:\n{e}") from e


class ResolvedProfile:
    """Encapsulates the fully resolved state of a deployment profile."""

    def __init__(self) -> None:
        self.assets: dict[str, Asset] = {}
        self.secrets: dict[str, Secret] = {}
        self.packages: dict[str, list[str]] = {
            "brew": [],
            "apt": [],
            "flatpak": [],
            "snap": [],
            "pacman": [],
            "dnf": [],
            "nix": [],
            "cargo": [],
            "pip": [],
        }
        self.docker_images: list[str] = []
        self.node_config: dict[str, str | None] = {"version_file": None, "version": None}


class ProfileResolver:
    """Resolves profile inheritance hierarchies with cycle detection."""

    @classmethod
    def resolve(cls, manifest: Manifest, profile_name: str) -> ResolvedProfile:
        """Resolves the inheritance hierarchy of a profile or multiple comma-separated profiles.

        Args:
            manifest: Root Manifest configuration.
            profile_name: Name of the target profile (can be comma-separated).

        Returns:
            Fully resolved profile configuration.
        """
        profile_names = [p.strip() for p in profile_name.split(",") if p.strip()]
        if not profile_names:
            raise ValueError("No profile names provided")

        if len(profile_names) == 1:
            name = profile_names[0]
            if name not in manifest.profiles:
                raise ValueError(f"Profile '{name}' is not defined in manifest profiles")

            resolved = ResolvedProfile()
            cls._resolve_recursive(manifest, name, resolved, [])
            return resolved

        # Multiple profiles: resolve each and merge
        merged = ResolvedProfile()
        for name in profile_names:
            resolved = cls.resolve(manifest, name)

            # Merge assets (last-write-wins)
            merged.assets.update(resolved.assets)

            # Merge secrets (last-write-wins)
            merged.secrets.update(resolved.secrets)

            # Merge packages
            for provider, pkgs in resolved.packages.items():
                for p in pkgs:
                    if p not in merged.packages[provider]:
                        merged.packages[provider].append(p)

            # Merge docker images
            for img in resolved.docker_images:
                if img not in merged.docker_images:
                    merged.docker_images.append(img)

            # Merge node config
            if resolved.node_config["version_file"]:
                merged.node_config["version_file"] = resolved.node_config["version_file"]
            if resolved.node_config["version"]:
                merged.node_config["version"] = resolved.node_config["version"]

        return merged

    @classmethod
    def _resolve_recursive(
        cls, manifest: Manifest, profile_name: str, resolved: ResolvedProfile, visited: list[str]
    ) -> None:
        """Recursively resolves the extends chain."""
        if profile_name in visited:
            # Reconstruct loop path for the error message
            visited.append(profile_name)
            chain = " -> ".join(visited)
            raise ValueError(f"Cyclic profile inheritance detected: {chain}")

        visited.append(profile_name)
        profile = manifest.profiles[profile_name]

        # 1. Resolve extends profiles first (base profiles resolved first)
        for base_profile_name in profile.extends:
            cls._resolve_recursive(manifest, base_profile_name, resolved, visited.copy())

        # 2. Merge local assets
        global_assets = {a.id: a for a in manifest.assets}
        for item in profile.assets:
            if isinstance(item, str):
                if item not in global_assets:
                    raise ValueError(
                        f"Asset ID '{item}' referenced in profile '{profile_name}' does not exist in the global pool"
                    )
                asset = global_assets[item]
            else:
                asset = item

            # Last-write-wins merge
            resolved.assets[asset.id] = asset

        # 3. Merge local secrets
        global_secrets = {s.id: s for s in manifest.secrets}
        for s_item in profile.secrets:
            if isinstance(s_item, str):
                if s_item not in global_secrets:
                    raise ValueError(
                        f"Secret ID '{s_item}' referenced in profile '{profile_name}' does not exist in the global pool"
                    )
                secret = global_secrets[s_item]
            else:
                secret = s_item

            # Last-write-wins merge
            resolved.secrets[secret.id] = secret

        # 4. Merge package categories
        for pkg_provider in profile.packages:
            if pkg_provider == "brew":
                resolved.packages["brew"].extend(manifest.packages.brew)
            elif pkg_provider == "apt":
                resolved.packages["apt"].extend(manifest.packages.apt)
            elif pkg_provider == "flatpak":
                resolved.packages["flatpak"].extend(manifest.packages.flatpak)
            elif pkg_provider == "snap":
                resolved.packages["snap"].extend(manifest.packages.snap)
            elif pkg_provider == "pacman":
                resolved.packages["pacman"].extend(manifest.packages.pacman)
            elif pkg_provider == "dnf":
                resolved.packages["dnf"].extend(manifest.packages.dnf)
            elif pkg_provider == "nix":
                resolved.packages["nix"].extend(manifest.packages.nix)
            elif pkg_provider == "cargo":
                resolved.packages["cargo"].extend(manifest.packages.cargo)
            elif pkg_provider == "pip":
                resolved.packages["pip"].extend(manifest.packages.pip)
            elif pkg_provider == "docker":
                resolved.docker_images.extend(manifest.packages.docker.images)
            elif pkg_provider == "node":
                if manifest.packages.node.version_file:
                    resolved.node_config["version_file"] = manifest.packages.node.version_file
                if manifest.packages.node.version:
                    resolved.node_config["version"] = manifest.packages.node.version


class RestoreService:
    """Orchestrates the 14-step deterministic restore apply engine."""

    @staticmethod
    def calculate_sha256(path: str) -> str:
        """Calculates SHA-256 of the file or directory at the given path."""
        if not os.path.exists(path):
            return ""
        if os.path.isdir(path):
            hasher = hashlib.sha256()
            for root, dirs, files in os.walk(path):
                dirs.sort()
                for file in sorted(files):
                    file_path = os.path.join(root, file)
                    rel_path = os.path.relpath(file_path, path)
                    hasher.update(rel_path.encode("utf-8"))
                    try:
                        with open(file_path, "rb") as f:
                            for chunk in iter(lambda: f.read(4096), b""):
                                hasher.update(chunk)
                    except Exception:
                        pass
            return hasher.hexdigest()

        hasher = hashlib.sha256()
        with open(path, "rb") as f:
            for chunk in iter(lambda: f.read(4096), b""):
                hasher.update(chunk)
        return hasher.hexdigest()

    @classmethod
    def restore(
        cls,
        repo_dir: str,
        profile_name: str,
        identity_path: str | None = None,
        interactive: bool = False,
        dry_run: bool = False,
        no_plugins: bool = False,
    ) -> str:
        """Runs the entire restore lifecycle under flock process protection.

        Args:
            repo_dir: Canonical path to the source repository.
            profile_name: Deployment profile name (e.g., 'base', 'work').
            identity_path: Optional path to age identity file.
            interactive: Whether to prompt on conflicts.
            dry_run: If True, plans and validates without modifying filesystem.

        Returns:
            The transaction ID of the executed context.
        """
        # Ensure we canonicalize the repository path
        repo_dir = os.path.abspath(repo_dir)
        manifest_path = os.path.join(repo_dir, "manifest.yaml")

        # Step 0: Acquire process lock (flock-based serialization)
        lock_path = os.path.expanduser("~/.config/rv/rv.lock")

        logger.info(f"Acquiring revive process lock at {lock_path}...")
        with ProcessLock(lock_path, blocking=False):
            # Step 1: Manifest Validation
            logger.info("Step 1/14: Loading and validating manifest.yaml...")
            manifest = ManifestLoader.load(manifest_path)

            # Step 2: Profile Resolution
            logger.info(f"Step 2/14: Resolving profile inheritance for '{profile_name}'...")
            resolved = ProfileResolver.resolve(manifest, profile_name)

            # Step 3: Machine Override Merge
            logger.info("Step 3/14: Merging host-specific machine overrides...")
            if manifest.machine_overrides.enabled:
                hostname = socket.gethostname()
                override_rel_path = manifest.machine_overrides.path.format(hostname=hostname)
                override_path = os.path.join(repo_dir, override_rel_path)

                if os.path.exists(override_path):
                    logger.info(f"Applying machine overrides from {override_path}...")
                    try:
                        with open(override_path, encoding="utf-8") as f:
                            override_data = yaml.safe_load(f)
                    except Exception as e:
                        raise ValueError(f"Failed to parse override YAML at {override_path}: {e}") from e

                    if override_data and isinstance(override_data, dict):
                        # Merge assets overrides
                        if "assets" in override_data:
                            for asset_dict in override_data["assets"]:
                                asset = Asset.model_validate(asset_dict)
                                resolved.assets[asset.id] = asset

                        # Merge secrets overrides
                        if "secrets" in override_data:
                            for secret_dict in override_data["secrets"]:
                                secret = Secret.model_validate(secret_dict)
                                resolved.secrets[secret.id] = secret

                        # Merge packages
                        if "packages" in override_data:
                            pkg_overrides = override_data["packages"]
                            for k in ["brew", "apt", "flatpak", "snap", "pacman", "dnf", "nix", "cargo", "pip"]:
                                if k in pkg_overrides and isinstance(pkg_overrides[k], list):
                                    resolved.packages[k].extend(pkg_overrides[k])
                            if "docker" in pkg_overrides and "images" in pkg_overrides["docker"]:
                                resolved.docker_images.extend(pkg_overrides["docker"]["images"])
                            if "node" in pkg_overrides:
                                if "version_file" in pkg_overrides["node"]:
                                    resolved.node_config["version_file"] = pkg_overrides["node"]["version_file"]
                                if "version" in pkg_overrides["node"]:
                                    resolved.node_config["version"] = pkg_overrides["node"]["version"]
                else:
                    logger.debug(f"No host override found at {override_path}. Skipping.")

            # Resolve identity path automatically if needed
            has_encrypted = any(a.encrypted for a in resolved.assets.values()) or len(resolved.secrets) > 0
            from rv.services.backup import BackupService

            identity_path = BackupService.resolve_identity(identity_path, has_encrypted)

            # Register dynamic secrets to logging scrubber if identity is present
            if identity_path and os.path.exists(identity_path):
                try:
                    with open(identity_path, encoding="utf-8") as f:
                        priv_key = f.read().strip()
                        if priv_key.startswith("AGE-SECRET-KEY-"):
                            SecretScrubber.register_secret(priv_key)
                except OSError as e:
                    logger.debug(f"Failed to read identity file for scrubber: {e}")

            # Step 4 & 5: Dependency Validation & Secret Decryption (within planning/handling)
            logger.info("Step 4/14 & 5/14: Validating dependencies and handling decryption...")
            tx_context = TransactionContext()

            # Process assets and secrets into the transaction plan
            skipped_assets = []
            for asset in resolved.assets.values():
                try:
                    success = AssetHandler.handle(asset, repo_dir, tx_context, identity_path, interactive)
                    if not success:
                        skipped_assets.append(asset.id)
                except Exception as e:
                    raise RuntimeError(f"Failed to plan asset '{asset.id}': {e}") from e

            for secret in resolved.secrets.values():
                try:
                    success = AssetHandler.handle(secret, repo_dir, tx_context, identity_path, interactive)
                    if not success:
                        skipped_assets.append(secret.id)
                except Exception as e:
                    raise RuntimeError(f"Failed to plan secret '{secret.id}': {e}") from e

            if skipped_assets:
                logger.info(f"Skipped assets due to conflict strategy: {', '.join(skipped_assets)}")

            # Run Pre-Restore Hooks
            cls._run_hooks(
                repo_dir=repo_dir,
                profile_name=profile_name,
                hook_type="pre-restore",
                tx_context=tx_context,
                dry_run=dry_run,
                no_plugins=no_plugins,
            )

            # Dry-run early exit before mutation
            if dry_run:
                logger.info("[Dry Run] Dry-run mode active. Verification of plan complete. Skipping all mutations.")
                return tx_context.tx_id

            # Step 6: Backup Snapshot (Create rollback journal backups)
            logger.info("Step 6/14: Creating rollback journal backup snapshot...")
            tx_context.validate()
            tx_context.snapshot()

            # Step 7, 8, 9: Symlinks, Copy, Permissions executed atomically
            logger.info("Steps 7/14 - 9/14: Executing atomic system modifications...")
            tx_context.execute()

            # Step 10: Package Orchestration
            logger.info("Step 10/14: Orchestrating package installations...")
            try:
                if resolved.packages["brew"]:
                    BrewProvider().install(resolved.packages["brew"], dry_run=dry_run)
                if resolved.packages["apt"]:
                    AptProvider().install(resolved.packages["apt"], dry_run=dry_run)
                if resolved.packages["flatpak"]:
                    FlatpakProvider().install(resolved.packages["flatpak"], dry_run=dry_run)
                if resolved.packages["snap"]:
                    SnapProvider().install(resolved.packages["snap"], dry_run=dry_run)
                if resolved.packages["pacman"]:
                    PacmanProvider().install(resolved.packages["pacman"], dry_run=dry_run)
                if resolved.packages["dnf"]:
                    DnfProvider().install(resolved.packages["dnf"], dry_run=dry_run)
                if resolved.packages["nix"]:
                    NixProvider().install(resolved.packages["nix"], dry_run=dry_run)
                if resolved.packages["cargo"]:
                    CargoProvider().install(resolved.packages["cargo"], dry_run=dry_run)
                if resolved.packages["pip"]:
                    PipProvider().install(resolved.packages["pip"], dry_run=dry_run)
                if resolved.docker_images:
                    DockerProvider().install(resolved.docker_images, dry_run=dry_run)
                if resolved.node_config["version"] or resolved.node_config["version_file"]:
                    NodeProvider().install_node(
                        repo_dir=repo_dir,
                        version=resolved.node_config["version"],
                        version_file=resolved.node_config["version_file"],
                        dry_run=dry_run,
                    )

                # Step 11: Plugin Hooks
                logger.info("Step 11/14: Running post-apply plugin hooks...")
                cls._run_hooks(
                    repo_dir=repo_dir,
                    profile_name=profile_name,
                    hook_type="post-restore",
                    tx_context=tx_context,
                    dry_run=dry_run,
                    no_plugins=no_plugins,
                )

                # Step 12: Post-Apply Verification
                logger.info("Step 12/14: Verifying system mutations post-apply...")
                tx_context.verify()
            except Exception as e:
                logger.error(f"Post-execution step failed, rolling back transaction: {e}")
                tx_context.rollback()
                raise RuntimeError(f"Restore failed during post-execution/package steps: {e}") from e

            # Step 13: Update manifest.lock SHA-256 map
            logger.info("Step 13/14: Updating manifest.lock sync states...")
            lockfile_path = os.path.join(repo_dir, "manifest.lock")

            # Load existing lockfile if it exists
            lockfile = Lockfile()
            if os.path.exists(lockfile_path):
                try:
                    with open(lockfile_path, encoding="utf-8") as f:
                        lock_dict = yaml.safe_load(f)
                    if isinstance(lock_dict, dict):
                        lockfile = Lockfile.model_validate(lock_dict)
                except Exception as e:
                    logger.warning(f"Failed to read existing lockfile {lockfile_path}, creating new: {e}")

            # Update entries for resolved assets
            for asset in resolved.assets.values():
                if asset.id in skipped_assets:
                    continue
                abs_source = os.path.join(repo_dir, asset.source)

                targets = [asset.target] if isinstance(asset.target, str) else asset.target
                resolved_targets = []
                permissions_list = []
                mtime_list = []

                for t in targets:
                    abs_target = PathHelper.canonicalize(Interpolator.interpolate(t))
                    if os.path.exists(abs_target):
                        resolved_targets.append(abs_target)
                        mtime = os.stat(abs_target).st_mtime
                        perms = asset.permissions or oct(os.stat(abs_target).st_mode & 0o7777)
                        if not perms.startswith("0"):
                            perms = "0" + perms[2:] if perms.startswith("o") else perms
                        permissions_list.append(perms)
                        mtime_list.append(mtime)

                if resolved_targets:
                    source_sha = cls.calculate_sha256(abs_source)
                    lockfile.entries[asset.id] = LockfileEntry(
                        sha256_of_source=source_sha,
                        target_path=resolved_targets[0] if isinstance(asset.target, str) else resolved_targets,
                        permissions=permissions_list[0] if isinstance(asset.target, str) else permissions_list,
                        mtime=mtime_list[0] if isinstance(asset.target, str) else mtime_list,
                    )

            # Update entries for resolved secrets
            for secret in resolved.secrets.values():
                if secret.id in skipped_assets:
                    continue
                abs_source = os.path.join(repo_dir, secret.source)

                targets = [secret.target] if isinstance(secret.target, str) else secret.target
                resolved_targets = []
                permissions_list = []
                mtime_list = []

                for t in targets:
                    abs_target = PathHelper.canonicalize(Interpolator.interpolate(t))
                    if os.path.exists(abs_target):
                        resolved_targets.append(abs_target)
                        mtime = os.stat(abs_target).st_mtime
                        perms = secret.permissions or "0600"
                        permissions_list.append(perms)
                        mtime_list.append(mtime)

                if resolved_targets:
                    source_sha = cls.calculate_sha256(abs_source)
                    lockfile.entries[secret.id] = LockfileEntry(
                        sha256_of_source=source_sha,
                        target_path=resolved_targets[0] if isinstance(secret.target, str) else resolved_targets,
                        permissions=permissions_list[0] if isinstance(secret.target, str) else permissions_list,
                        mtime=mtime_list[0] if isinstance(secret.target, str) else mtime_list,
                    )

            # Write lockfile atomically
            # Carry over any rendered template checksums collected by handlers
            lockfile.rendered_checksums.update(tx_context.rendered_checksums)
            AtomicWrite.write(lockfile_path, lockfile.model_dump_json(indent=2).encode("utf-8"))

            # Step 14: Structured Audit Log Commit
            logger.info("Step 14/14: Committing transaction and writing audit logs...")
            tx_context.commit()

            AuditLogger.log_audit(
                f"Sync restore of profile '{profile_name}' completed successfully.",
                level=logging.INFO,
                tx_id=tx_context.tx_id,
                profile=profile_name,
                op="restore",
            )

            # Finalize cleanup
            tx_context.cleanup()

            logger.info(f"Restore transaction {tx_context.tx_id} committed successfully!")
            return tx_context.tx_id

    @classmethod
    def _run_hooks(
        cls,
        repo_dir: str,
        profile_name: str,
        hook_type: str,
        tx_context: TransactionContext,
        dry_run: bool = False,
        no_plugins: bool = False,
    ) -> None:
        """Discovers and executes sandboxed hooks for the specified hook stage."""
        if no_plugins:
            logger.info(f"Skipping {hook_type} hooks due to --no-plugins flag.")
            return

        from rv.plugins.context import ReviveContext
        from rv.plugins.loader import PluginLoader
        from rv.plugins.sandbox import SandboxRunner

        try:
            plugins = PluginLoader.discover_plugins(repo_dir)
        except Exception as e:
            logger.warning(f"Failed to discover plugins: {e}")
            return

        matching_plugins = [p for p in plugins if hook_type in p.manifest.hooks]

        if not matching_plugins:
            logger.debug(f"No plugins found for hook: {hook_type}")
            return

        logger.info(f"Running {hook_type} plugin hooks ({len(matching_plugins)} registered)...")
        targets = [os.path.abspath(op["target"]) for op in tx_context.planned_operations]

        context = ReviveContext(
            repo_dir=repo_dir, profile_name=profile_name, dry_run=dry_run, targets=targets, hook_type=hook_type
        )

        for plugin in matching_plugins:
            logger.info(f"Executing hook '{hook_type}' in plugin '{plugin.manifest.name}'...")
            try:
                res = SandboxRunner.run_plugin(plugin, context)
                logger.info(f"Plugin '{plugin.manifest.name}' succeeded: {res.get('message', 'No message')}")
            except Exception as e:
                logger.error(f"Plugin '{plugin.manifest.name}' failed during hook '{hook_type}': {e}")
                raise
