"""DoctorService for environment diagnostics, linting, and health checking."""

import os
from typing import Any

from rv.models.transaction import Lockfile
from rv.services.restore import ManifestLoader, ProfileResolver
from rv.utils.interpolate import Interpolator
from rv.utils.path import PathHelper
from rv.utils.platform import Platform


class DoctorService:
    """Diagnoses the health of the revive repository configuration and system integration."""

    @classmethod
    def check_health(cls, repo_dir: str, profile_name: str | None = None) -> dict[str, Any]:
        """Runs health checks on the repository and active system profile.

        Args:
            repo_dir: Absolute path to the source repository.
            profile_name: Optional profile name to check specifically.

        Returns:
            A diagnostic dictionary report with categories.
        """
        repo_dir = os.path.abspath(repo_dir)
        manifest_path = os.path.join(repo_dir, "manifest.yaml")
        lockfile_path = os.path.join(repo_dir, "manifest.lock")

        issues: list[dict[str, str]] = []
        checks_run = 0

        # 1. Manifest Checks
        checks_run += 1
        manifest = None
        if not os.path.exists(manifest_path):
            issues.append(
                {
                    "category": "manifest",
                    "severity": "critical",
                    "message": f"manifest.yaml not found at {manifest_path}. Repository must be initialized.",
                }
            )
        else:
            try:
                manifest = ManifestLoader.load(manifest_path)
            except Exception as e:
                issues.append(
                    {
                        "category": "manifest",
                        "severity": "critical",
                        "message": f"Failed to load or validate manifest.yaml: {e}",
                    }
                )

        # 2. Lockfile Checks
        checks_run += 1
        if os.path.exists(lockfile_path):
            try:
                with open(lockfile_path, encoding="utf-8") as f:
                    Lockfile.model_validate_json(f.read())
            except Exception as e:
                issues.append(
                    {
                        "category": "lockfile",
                        "severity": "warning",
                        "message": f"manifest.lock is corrupt or invalid: {e}",
                    }
                )

        # 3. Environment capabilities checks
        checks_run += 1
        system_tools = [
            "age",
            "age-keygen",
            "brew",
            "apt",
            "flatpak",
            "snap",
            "pacman",
            "dnf",
            "nix-env",
            "cargo",
            "pip",
            "docker",
            "node",
            "git",
        ]
        tools_status: dict[str, bool] = {}
        for tool in system_tools:
            available = Platform.has_tool(tool)
            tools_status[tool] = available
            if tool in ("age", "age-keygen") and not available:
                # Pyrage might be available natively, so check that too
                from rv.security.encryptor import AgeEncryptor

                if not AgeEncryptor.is_pyrage_available():
                    issues.append(
                        {
                            "category": "system",
                            "severity": "warning",
                            "message": f"Neither pyrage python library nor '{tool}' CLI executable is available. Encryption/decryption will fail.",
                        }
                    )

        # 4. Profile Specific Checks
        if manifest and profile_name:
            checks_run += 1
            if profile_name not in manifest.profiles:
                issues.append(
                    {
                        "category": "profile",
                        "severity": "critical",
                        "message": f"Profile '{profile_name}' does not exist in manifest",
                    }
                )
            else:
                try:
                    resolved = ProfileResolver.resolve(manifest, profile_name)

                    # Verify each asset source file exists in repo
                    for asset in resolved.assets.values():
                        abs_source = os.path.join(repo_dir, asset.source)
                        if not os.path.exists(abs_source) and not asset.encrypted:
                            issues.append(
                                {
                                    "category": "asset_source",
                                    "severity": "error",
                                    "message": f"Source file missing for asset '{asset.id}': {abs_source}",
                                }
                            )

                        # Verify path loop checks
                        targets = [asset.target] if isinstance(asset.target, str) else asset.target
                        for target in targets:
                            try:
                                abs_target = PathHelper.canonicalize(Interpolator.interpolate(target))
                                if PathHelper.detect_symlink_loop(abs_target):
                                    issues.append(
                                        {
                                            "category": "asset_target",
                                            "severity": "error",
                                            "message": f"Target path '{abs_target}' for asset '{asset.id}' forms a cyclic symlink loop",
                                        }
                                    )
                            except Exception as e:
                                issues.append(
                                    {
                                        "category": "asset_target",
                                        "severity": "error",
                                        "message": f"Failed path interpolation/verification for asset '{asset.id}': {e}",
                                    }
                                )

                    # Verify each secret source exists in repo
                    for secret in resolved.secrets.values():
                        abs_source = os.path.join(repo_dir, secret.source)
                        if not os.path.exists(abs_source):
                            issues.append(
                                {
                                    "category": "secret_source",
                                    "severity": "error",
                                    "message": f"Source file missing for secret '{secret.id}': {abs_source}",
                                }
                            )

                except Exception as e:
                    issues.append(
                        {
                            "category": "profile_resolution",
                            "severity": "critical",
                            "message": f"Failed to resolve profile '{profile_name}': {e}",
                        }
                    )

        healthy = not any(i["severity"] == "critical" for i in issues)

        # 5. Package cache state
        checks_run += 1
        import time

        from rv.providers.base import _CACHE_TTL_SECONDS, PackageCache

        cache_data = PackageCache._load()
        cache_info: dict[str, Any] = {}
        now = time.time()
        for provider_name, entry in cache_data.items():
            if not isinstance(entry, dict):
                continue
            last_updated = entry.get("last_updated", 0.0)
            installed = entry.get("installed", [])
            age_secs = now - last_updated
            expired = age_secs > _CACHE_TTL_SECONDS
            cache_info[provider_name] = {
                "installed_count": len(installed),
                "age_seconds": round(age_secs, 1),
                "expired": expired,
            }

        return {
            "healthy": healthy,
            "issues": issues,
            "checks_run": checks_run,
            "tools": tools_status,
            "package_cache": cache_info,
        }
