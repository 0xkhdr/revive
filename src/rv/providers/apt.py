"""Apt package provider orchestration for Debian/Ubuntu systems."""

import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.apt")


class AptProvider(BaseProvider):
    """Orchestrates Debian/Ubuntu packages via apt-get and dpkg."""

    def __init__(self) -> None:
        super().__init__("apt-get")

    def is_available(self) -> bool:
        """Checks if both apt-get and dpkg are available on the system."""
        import shutil

        return shutil.which("apt-get") is not None and shutil.which("dpkg") is not None

    def is_installed(self, pkg: str) -> bool:
        """Checks if a Debian/Ubuntu package is currently installed using dpkg.

        Args:
            pkg: Package name to check.

        Returns:
            True if installed, False otherwise.
        """
        try:
            result = subprocess.run(["dpkg", "-s", pkg], capture_output=True, text=True, check=False)
            return result.returncode == 0 and "Status: install ok installed" in result.stdout
        except Exception as e:
            logger.debug(f"Failed to check package status via dpkg for {pkg}: {e}")
            return False

    def _get_missing_packages(self, packages: list[str]) -> list[str]:
        """Queries dpkg to see which packages are not currently installed."""
        return [pkg for pkg in packages if not self.is_installed(pkg)]

    def install(self, packages: list[str], dry_run: bool = False, use_cache: bool = True) -> None:
        """Installs missing packages using apt-get.

        Args:
            packages: List of package names to check and install.
            dry_run: Whether to simulate installation.
            use_cache: If True (default), consult the PackageCache for idempotency.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("apt-get or dpkg is not available on this platform")

        logger.info("Checking package status via dpkg (with idempotency cache)...")
        missing = self.filter_missing(packages, use_cache=use_cache)

        if not missing:
            logger.info("All apt packages are already installed.")
            return

        if dry_run:
            logger.info(f"[Dry Run] apt packages to install: {', '.join(missing)}")
            return

        logger.info(f"Installing missing apt packages: {', '.join(missing)}")
        cmd = ["apt-get", "install", "-y"] + missing
        try:
            self.execute_with_retry(cmd)
            from rv.providers.base import PackageCache

            PackageCache.mark_installed(self.name, missing)
            logger.info("Apt package installation completed successfully.")
        except Exception as e:
            raise ProviderError(f"Apt installation failed: {e}") from e
