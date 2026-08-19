"""StatusService to compute drift between manifest.lock, resolved profile, and local filesystem."""

import difflib
import hashlib
import os
from typing import Any

from rv.logging.audit import AuditLogger
from rv.models.manifest import Asset, AssetType, Secret
from rv.models.transaction import Lockfile
from rv.security.encryptor import AgeEncryptor
from rv.security.tempfile import SecureTempFile
from rv.services.restore import ManifestLoader, ProfileResolver, RestoreService
from rv.utils.interpolate import Interpolator
from rv.utils.path import PathHelper

logger = AuditLogger.get_logger("rv.services.status")


class StatusService:
    """Computes drift between expected repo state and actual system state."""

    @classmethod
    def get_status(
        cls,
        repo_dir: str,
        profile_name: str,
        identity_path: str | None = None,
        manifest_path: str | None = None,
    ) -> dict[str, Any]:
        """Compares resolved profile assets with the current filesystem.

        Args:
            repo_dir: Absolute path to the source repository.
            profile_name: Deployment profile name.
            identity_path: Optional path to the age identity file.
            manifest_path: Optional path to a custom manifest file.

        Returns:
            A dictionary describing the drift status of all assets.
        """
        repo_dir = os.path.abspath(repo_dir)
        if manifest_path is None:
            manifest_path = os.path.join(repo_dir, "manifest.yaml")
        else:
            if not os.path.isabs(manifest_path):
                manifest_path = os.path.join(repo_dir, manifest_path)
        lockfile_path = os.path.splitext(manifest_path)[0] + ".lock"

        # 1. Load manifest and resolve profile
        manifest = ManifestLoader.load(manifest_path)
        resolved = ProfileResolver.resolve(manifest, profile_name)

        from rv.services.backup import BackupService

        identity_path = BackupService.resolve_identity(identity_path, profile_has_encrypted=False)

        # 2. Load lockfile if exists
        lockfile = Lockfile()
        if os.path.exists(lockfile_path):
            try:
                lockfile = Lockfile.model_validate_json(open(lockfile_path, encoding="utf-8").read())
            except Exception as e:
                logger.debug(f"Failed to read or parse lockfile: {e}")

        drifted = False
        assets_status: dict[str, dict[str, Any]] = {}

        # 3. Analyze each asset
        for asset in resolved.assets.values():
            status_info = cls._check_asset_drift(asset, repo_dir, lockfile, identity_path)
            assets_status[asset.id] = status_info
            if status_info["status"] != "in_sync":
                drifted = True

        # 4. Analyze each secret
        for secret in resolved.secrets.values():
            status_info = cls._check_asset_drift(secret, repo_dir, lockfile, identity_path)
            assets_status[secret.id] = status_info
            if status_info["status"] != "in_sync":
                drifted = True

        return {"drifted": drifted, "profile": profile_name, "assets": assets_status}

    @classmethod
    def _check_asset_drift(
        cls, asset: Asset | Secret, repo_dir: str, lockfile: Lockfile, identity_path: str | None = None
    ) -> dict[str, Any]:
        """Evaluates a single asset or secret for drift against current filesystem."""
        targets = [asset.target] if isinstance(asset.target, str) else asset.target
        lock_entry = lockfile.entries.get(asset.id)

        for target_expr in targets:
            try:
                abs_target = PathHelper.canonicalize(Interpolator.interpolate(target_expr))
            except Exception as e:
                return {
                    "type": asset.type,
                    "target": target_expr,
                    "status": "error",
                    "details": f"Failed path interpolation: {e}",
                }

            # Match source sub-item if directory
            abs_source = os.path.join(repo_dir, asset.source)
            if os.path.isdir(abs_source):
                basename = os.path.basename(abs_target)
                potential_source = os.path.join(abs_source, basename)
                if os.path.exists(potential_source):
                    abs_source = potential_source

            # Extract specific lock entry fields for this target
            lock_entry_for_target: Any = None
            if lock_entry:
                if isinstance(lock_entry.target_path, list):
                    try:
                        t_idx = lock_entry.target_path.index(abs_target)
                        perms = (
                            lock_entry.permissions[t_idx]
                            if isinstance(lock_entry.permissions, list)
                            else lock_entry.permissions
                        )
                        mt = lock_entry.mtime[t_idx] if isinstance(lock_entry.mtime, list) else lock_entry.mtime

                        class TargetLockEntry:
                            def __init__(self, target_path: str, permissions: str, mtime: float):
                                self.target_path = target_path
                                self.permissions = permissions
                                self.mtime = mtime

                        lock_entry_for_target = TargetLockEntry(abs_target, perms, mt)
                    except (ValueError, IndexError):
                        pass
                else:
                    lock_entry_for_target = lock_entry

            # 1. Check if target exists
            if not os.path.exists(abs_target) and not os.path.islink(abs_target):
                return {
                    "type": asset.type,
                    "target": abs_target,
                    "status": "missing",
                    "details": f"Target {abs_target} does not exist on filesystem",
                }

            # 2. Check type mismatch (e.g. symlink wanted, but standard file exists)
            if asset.type == AssetType.SYMLINK:
                if not os.path.islink(abs_target):
                    return {
                        "type": asset.type,
                        "target": abs_target,
                        "status": "type_mismatch",
                        "details": "Expected a symlink, but found a regular file/directory",
                    }

                # Check if symlink target points to correct source
                try:
                    link_target = os.readlink(abs_target)
                    # Resolve relative symlink relative to the symlink's directory
                    if not os.path.isabs(link_target):
                        abs_link_target = os.path.abspath(os.path.join(os.path.dirname(abs_target), link_target))
                    else:
                        abs_link_target = os.path.abspath(link_target)

                    if abs_link_target != os.path.abspath(abs_source):
                        return {
                            "type": asset.type,
                            "target": abs_target,
                            "status": "modified",
                            "details": f"Symlink points to '{link_target}', expected '{abs_source}'",
                        }
                except Exception as e:
                    return {
                        "type": asset.type,
                        "target": abs_target,
                        "status": "error",
                        "details": f"Failed to read symlink: {e}",
                    }
            else:
                # Expected a standard file/directory
                if os.path.islink(abs_target):
                    return {
                        "type": asset.type,
                        "target": abs_target,
                        "status": "type_mismatch",
                        "details": "Expected a file, but found a symlink",
                    }

                # Check permissions
                stat_mode = os.stat(abs_target).st_mode & 0o7777
                expected_perms = asset.permissions
                if not expected_perms:
                    expected_perms = "0600" if asset.type == AssetType.SECRET else "0644"

                if oct(stat_mode) != oct(int(expected_perms, 8)):
                    return {
                        "type": asset.type,
                        "target": abs_target,
                        "status": "permissions_drifted",
                        "details": f"Permissions mismatch: actual {oct(stat_mode)}, expected {expected_perms}",
                    }

                # Check content drift
                if asset.type == AssetType.COPY:
                    if asset.encrypted:
                        # Encrypted copy
                        content_changed = cls._check_encrypted_drift(
                            abs_source, abs_target, lock_entry_for_target, identity_path
                        )
                    else:
                        if os.path.isdir(abs_source) or os.path.isdir(abs_target):
                            content_changed = False
                            if lock_entry_for_target:
                                try:
                                    target_mtime = os.stat(abs_target).st_mtime
                                    content_changed = bool(abs(target_mtime - lock_entry_for_target.mtime) > 0.001)
                                except Exception:
                                    content_changed = True
                        else:
                            # Regular copy
                            content_changed = RestoreService.calculate_sha256(
                                abs_source
                            ) != RestoreService.calculate_sha256(abs_target)
                elif asset.type == AssetType.TEMPLATE:
                    # Compare rendered template
                    if not isinstance(asset, Asset):
                        raise TypeError("Expected an Asset instance for template type")
                    content_changed = cls._check_template_drift(asset, abs_source, abs_target)
                elif asset.type == AssetType.SECRET:
                    content_changed = cls._check_encrypted_drift(
                        abs_source, abs_target, lock_entry_for_target, identity_path
                    )
                else:
                    content_changed = False

                if content_changed:
                    return {
                        "type": asset.type,
                        "target": abs_target,
                        "status": "modified",
                        "details": "File content has drifted from repository source",
                    }

        # If we checked all targets and none had drift, they are in sync
        return {
            "type": asset.type,
            "target": asset.target,
            "status": "in_sync",
            "details": "Asset is in sync with repository state",
        }

    @classmethod
    def _check_encrypted_drift(
        cls, abs_source: str, abs_target: str, lock_entry: Any | None, identity_path: str | None
    ) -> bool:
        """Determines if decrypted/encrypted file content has drifted."""
        if identity_path and os.path.exists(identity_path):
            with SecureTempFile.file() as tmp_decrypted:
                try:
                    AgeEncryptor.decrypt_file(abs_source, tmp_decrypted, identity_path)
                    decrypted_sha = RestoreService.calculate_sha256(tmp_decrypted)
                    target_sha = RestoreService.calculate_sha256(abs_target)
                    return decrypted_sha != target_sha
                except Exception as e:
                    logger.debug(f"Decryption failed during status check, falling back to mtime: {e}")

        # Fallback if key missing or decryption fails: check mtime from lockfile entry
        if lock_entry:
            try:
                target_mtime = os.stat(abs_target).st_mtime
                # Allow a tiny tolerance for float representation
                return bool(abs(target_mtime - lock_entry.mtime) > 0.001)
            except Exception:
                return True
        return True

    @classmethod
    def _check_template_drift(cls, asset: Asset, abs_source: str, abs_target: str) -> bool:
        """Determines if a rendered template has drifted from actual system file."""
        import jinja2

        try:
            with open(abs_source, encoding="utf-8") as f:
                template_content = f.read()

            context = dict(os.environ)
            if asset.template_vars:
                context.update(asset.template_vars)

            template = jinja2.Template(template_content, undefined=jinja2.StrictUndefined)
            rendered = template.render(context)

            target_sha = RestoreService.calculate_sha256(abs_target)

            # Temporary hash calculation
            rendered_sha = hashlib.sha256(rendered.encode("utf-8")).hexdigest()
            return rendered_sha != target_sha
        except Exception:
            return True

    @classmethod
    def get_contents_for_diff(
        cls,
        repo_dir: str,
        profile_name: str,
        asset_id: str,
        identity_path: str | None = None,
        manifest_path: str | None = None,
    ) -> tuple[str, str] | None:
        """Retrieves the expected and actual contents for calculating a diff.

        Args:
            repo_dir: Absolute path to the source repository.
            profile_name: Deployment profile name.
            asset_id: The ID of the asset.
            identity_path: Optional path to the age identity file.
            manifest_path: Optional path to a custom manifest file.

        Returns:
            A tuple of (expected_text, actual_text), or None if no drift or binary file.
        """
        repo_dir = os.path.abspath(repo_dir)
        if manifest_path is None:
            manifest_path = os.path.join(repo_dir, "manifest.yaml")
        else:
            if not os.path.isabs(manifest_path):
                manifest_path = os.path.join(repo_dir, manifest_path)

        manifest = ManifestLoader.load(manifest_path)
        resolved = ProfileResolver.resolve(manifest, profile_name)

        from rv.services.backup import BackupService

        identity_path = BackupService.resolve_identity(identity_path, profile_has_encrypted=False)

        asset = resolved.assets.get(asset_id) or resolved.secrets.get(asset_id)
        if not asset:
            return None

        try:
            target_str = asset.target[0] if isinstance(asset.target, list) else asset.target
            abs_target = PathHelper.canonicalize(Interpolator.interpolate(target_str))
        except Exception:
            return None

        abs_source = os.path.join(repo_dir, asset.source)

        if not os.path.exists(abs_target) or os.path.islink(abs_target):
            return None

        expected_text = ""
        # 1. Read expected content
        if asset.type == AssetType.COPY:
            if asset.encrypted:
                if not identity_path or not os.path.exists(identity_path):
                    return "[Cannot decrypt source: identity file missing]", ""
                with SecureTempFile.file() as tmp_decrypted:
                    try:
                        AgeEncryptor.decrypt_file(abs_source, tmp_decrypted, identity_path)
                        with open(tmp_decrypted, encoding="utf-8", errors="replace") as f:
                            expected_text = f.read()
                    except Exception as e:
                        return f"[Decryption failed: {e}]", ""
            else:
                try:
                    with open(abs_source, encoding="utf-8", errors="replace") as f:
                        expected_text = f.read()
                except Exception:
                    return None
        elif asset.type == AssetType.TEMPLATE:
            import jinja2

            try:
                with open(abs_source, encoding="utf-8") as f:
                    template_content = f.read()
                context = dict(os.environ)
                if isinstance(asset, Asset) and asset.template_vars:
                    context.update(asset.template_vars)
                template = jinja2.Template(template_content, undefined=jinja2.StrictUndefined)
                expected_text = template.render(context)
            except Exception as e:
                return f"[Template rendering failed: {e}]", ""
        elif asset.type == AssetType.SECRET:
            if not identity_path or not os.path.exists(identity_path):
                return "[Cannot decrypt secret: identity file missing]", ""
            with SecureTempFile.file() as tmp_decrypted:
                try:
                    AgeEncryptor.decrypt_file(abs_source, tmp_decrypted, identity_path)
                    with open(tmp_decrypted, encoding="utf-8", errors="replace") as f:
                        expected_text = f.read()
                except Exception as e:
                    return f"[Decryption failed: {e}]", ""
        else:
            return None

        # 2. Read actual content
        try:
            with open(abs_target, encoding="utf-8", errors="replace") as f:
                actual_text = f.read()
        except Exception:
            return None

        return expected_text, actual_text

    @classmethod
    def get_diff(
        cls,
        repo_dir: str,
        profile_name: str,
        asset_id: str,
        identity_path: str | None = None,
        manifest_path: str | None = None,
    ) -> str | None:
        """Calculates a diff representation between expected source and system file.

        Args:
            repo_dir: Absolute path to the source repository.
            profile_name: Deployment profile name.
            asset_id: The ID of the asset to diff.
            identity_path: Optional path to the age identity file.
            manifest_path: Optional path to a custom manifest file.

        Returns:
            A string diff, or None if no drift or binary file.
        """
        contents = cls.get_contents_for_diff(
            repo_dir, profile_name, asset_id, identity_path, manifest_path=manifest_path
        )
        if not contents:
            return None

        expected_text, actual_text = contents

        # Handle early-return error string placeholders from get_contents_for_diff
        if not actual_text and (
            expected_text.startswith("[Cannot decrypt")
            or expected_text.startswith("[Decryption failed")
            or expected_text.startswith("[Template rendering failed")
        ):
            return expected_text

        repo_dir = os.path.abspath(repo_dir)
        if manifest_path is None:
            manifest_path = os.path.join(repo_dir, "manifest.yaml")
        else:
            if not os.path.isabs(manifest_path):
                manifest_path = os.path.join(repo_dir, manifest_path)
        manifest = ManifestLoader.load(manifest_path)
        resolved = ProfileResolver.resolve(manifest, profile_name)
        asset = resolved.assets.get(asset_id) or resolved.secrets.get(asset_id)
        if not asset:
            return None

        try:
            target_str = asset.target[0] if isinstance(asset.target, list) else asset.target
            abs_target = PathHelper.canonicalize(Interpolator.interpolate(target_str))
        except Exception:
            return None

        diff_lines = difflib.unified_diff(
            expected_text.splitlines(),
            actual_text.splitlines(),
            fromfile=f"repo://{asset.source}",
            tofile=abs_target,
            lineterm="",
        )

        return "\n".join(diff_lines)
