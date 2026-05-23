"""Snap package provider orchestration."""

import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.snap")


class SnapProvider(BaseProvider):
    """Orchestrates Snap packages."""

    def __init__(self) -> None:
        super().__init__("snap")

    def is_installed(self, pkg: str) -> bool:
        """Checks if a snap package is installed via snap list.

        Args:
            pkg: Snap package name to check.

        Returns:
            True if installed, False otherwise.
        """
        try:
            # snap list <pkg> returns 0 if installed, 1 if not.
            res = subprocess.run(["snap", "list", pkg], capture_output=True, check=False)
            return res.returncode == 0
        except Exception:
            return False

    def install(self, packages: list[str], dry_run: bool = False, use_cache: bool = True) -> None:
        """Installs missing snap packages.

        Args:
            packages: List of snap package names.
            dry_run: Whether to simulate installation.
            use_cache: If True (default), consult the PackageCache for idempotency.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("snap CLI is not installed or not in system PATH")

        from rv.providers.base import PackageCache

        missing = []
        for pkg in packages:
            actual_pkg = pkg
            classic_flag = False
            if pkg.startswith("classic:"):
                actual_pkg = pkg.split(":", 1)[1]
                classic_flag = True

            # Check cache first, then live query
            if use_cache and PackageCache.is_installed(self.name, actual_pkg):
                logger.debug(f"[Cache] snap '{actual_pkg}' already in cache. Skipping.")
                continue
            if not self.is_installed(actual_pkg):
                missing.append((pkg, actual_pkg, classic_flag))
            else:
                PackageCache.mark_installed(self.name, [actual_pkg])

        if not missing:
            logger.info("All snap packages are already installed.")
            return

        if dry_run:
            to_install_names = [item[0] for item in missing]
            logger.info(f"[Dry Run] snap packages would be installed: {', '.join(to_install_names)}")
            return

        logger.info("Installing missing snap packages...")
        for pkg, actual_pkg, classic_flag in missing:
            cmd = ["snap", "install", actual_pkg]
            if classic_flag:
                cmd.append("--classic")

            try:
                self.execute_with_retry(cmd)
                PackageCache.mark_installed(self.name, [actual_pkg])
                logger.info(f"Successfully installed Snap: {pkg}")
            except Exception as e:
                raise ProviderError(f"Snap installation failed for {pkg}: {e}") from e
