"""BackupService to synchronize system files back into the repository."""

import logging
import os
import shutil
import socket

from rv.logging.audit import AuditLogger
from rv.models.manifest import Asset, AssetType, Secret
from rv.security.encryptor import AgeEncryptor
from rv.services.restore import ManifestLoader, ProfileResolver
from rv.utils.interpolate import Interpolator
from rv.utils.path import PathHelper

logger = AuditLogger.get_logger("rv.services.backup")


class BackupService:
    """Manages the bidirectional synchronization of files and secrets from system to repository."""

    @classmethod
    def resolve_identity(cls, identity_path: str | None, profile_has_encrypted: bool) -> str | None:
        """Resolves the age identity file path, using the default if unspecified.

        If secrets are present and no identity is found, raises a clear instructive error.
        """
        default_paths = [
            os.path.expanduser("~/.config/rv/identity.txt"),
            os.path.expanduser("~/.config/rv/keys/identity.txt"),
            os.path.expanduser("~/.config/rv/identifier.txt"),
        ]

        if identity_path:
            abs_path = os.path.abspath(identity_path)
            if not os.path.exists(abs_path):
                raise FileNotFoundError(f"Age identity file not found at: {identity_path}")
            return abs_path

        # Try default locations in order
        for path in default_paths:
            if os.path.exists(path):
                return path

        # If we need it but don't have it, instruct the user
        if profile_has_encrypted:
            raise ValueError(
                "Age identity file not found at default location '~/.config/rv/identity.txt'.\n"
                "To manage secrets, please do one of the following:\n"
                "  1. Create the identity file at '~/.config/rv/identity.txt' with your age private key.\n"
                "  2. Provide a custom identity file path using the '--identity' / '-i' option."
            )

        return None

    @classmethod
    def backup(
        cls,
        repo_dir: str,
        profile_name: str,
        identity_path: str | None = None,
        dry_run: bool = False,
    ) -> list[str]:
        """Synchronizes system files back into the repository based on the profile definition.

        Args:
            repo_dir: Canonical path to the source repository.
            profile_name: Deployment profile name.
            identity_path: Optional path to the age identity file.
            dry_run: If True, previews operations without modifying the repository.

        Returns:
            A list of successfully backed up asset IDs.
        """
        repo_dir = os.path.abspath(repo_dir)
        manifest_path = os.path.join(repo_dir, "manifest.yaml")

        logger.info(f"Loading manifest from {manifest_path}...")
        manifest = ManifestLoader.load(manifest_path)

        logger.info(f"Resolving profile '{profile_name}'...")
        resolved = ProfileResolver.resolve(manifest, profile_name)

        # Merge machine overrides to correctly map machine-specific paths
        if manifest.machine_overrides.enabled:
            hostname = socket.gethostname()
            override_rel_path = manifest.machine_overrides.path.format(hostname=hostname)
            override_path = os.path.join(repo_dir, override_rel_path)

            if os.path.exists(override_path):
                import yaml

                from rv.models.manifest import Asset, Secret

                logger.info(f"Merging machine overrides from {override_path}...")
                try:
                    with open(override_path, encoding="utf-8") as f:
                        override_data = yaml.safe_load(f)
                except Exception as e:
                    raise ValueError(f"Failed to parse override YAML at {override_path}: {e}") from e

                if override_data and isinstance(override_data, dict):
                    if "assets" in override_data:
                        for asset_dict in override_data["assets"]:
                            asset = Asset.model_validate(asset_dict)
                            resolved.assets[asset.id] = asset

                    if "secrets" in override_data:
                        for secret_dict in override_data["secrets"]:
                            secret = Secret.model_validate(secret_dict)
                            resolved.secrets[secret.id] = secret

        # Check if we have encrypted assets/secrets
        has_encrypted = any(a.encrypted for a in resolved.assets.values()) or len(resolved.secrets) > 0
        resolved_identity = cls.resolve_identity(identity_path, has_encrypted)

        if resolved_identity:
            logger.info(f"Using age identity file: {resolved_identity}")

        backed_up_ids: list[str] = []

        # Process Assets
        for asset in resolved.assets.values():
            if asset.type == AssetType.TEMPLATE:
                logger.warning(
                    f"Skipping template asset '{asset.id}' - templates cannot be backed up directly from system."
                )
                continue

            cls._backup_item(asset, repo_dir, resolved_identity, dry_run)
            backed_up_ids.append(asset.id)

        # Process Secrets
        for secret in resolved.secrets.values():
            cls._backup_item(secret, repo_dir, resolved_identity, dry_run)
            backed_up_ids.append(secret.id)

        return backed_up_ids

    @classmethod
    def _backup_item(
        cls,
        item: Asset | Secret,
        repo_dir: str,
        identity_path: str | None,
        dry_run: bool,
    ) -> None:
        """Backs up a single asset or secret from system to repository."""
        targets = [item.target] if isinstance(item.target, str) else item.target
        abs_source = os.path.join(repo_dir, item.source)

        for target_expr in targets:
            interpolated_target = Interpolator.interpolate(target_expr)
            abs_target = PathHelper.canonicalize(interpolated_target)

            if not os.path.exists(abs_target) and not os.path.islink(abs_target):
                logger.warning(f"Target '{abs_target}' does not exist on system, skipping.")
                continue

            # Determine where to write the backup file in the repository
            if isinstance(item.target, list) or os.path.isdir(abs_source):
                resolved_source = os.path.join(abs_source, os.path.basename(abs_target))
            else:
                resolved_source = abs_source

            # Skip if target is already a symlink pointing to the repo resolved source
            if os.path.islink(abs_target):
                try:
                    link_target = os.readlink(abs_target)
                    if os.path.abspath(link_target) == os.path.abspath(resolved_source):
                        logger.info(f"Asset '{item.id}' is already in sync (system symlink points to repo).")
                        continue
                    # Follow symlink to get actual target file contents
                    abs_target = os.path.realpath(abs_target)
                except Exception:
                    pass

            if dry_run:
                action = "encrypt" if item.encrypted else "copy"
                logger.info(f"[Dry Run] Would {action} system path '{abs_target}' to repo path '{resolved_source}'")
                continue

            # Active execution
            os.makedirs(os.path.dirname(resolved_source), exist_ok=True)

            if item.encrypted:
                if not identity_path:
                    raise ValueError(f"Identity key required to encrypt secret/asset: {item.id}")

                logger.info(f"Encrypting '{abs_target}' to repo '{resolved_source}'...")
                # Derive recipient public key from private key
                recipient = AgeEncryptor.get_public_key(identity_path)
                AgeEncryptor.encrypt_file(abs_target, resolved_source, [recipient])
            else:
                logger.info(f"Copying '{abs_target}' to repo '{resolved_source}'...")
                if os.path.isdir(abs_target):
                    if os.path.exists(resolved_source):
                        if os.path.isdir(resolved_source):
                            shutil.rmtree(resolved_source)
                        else:
                            os.remove(resolved_source)
                    shutil.copytree(abs_target, resolved_source, symlinks=True)
                else:
                    if os.path.exists(resolved_source) and os.path.isdir(resolved_source):
                        shutil.rmtree(resolved_source)
                    shutil.copy2(abs_target, resolved_source, follow_symlinks=True)
