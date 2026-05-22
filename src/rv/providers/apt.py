"""Apt package provider orchestration for Debian/Ubuntu systems.
"""

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

    def _get_missing_packages(self, packages: list[str]) -> list[str]:
        """Queries dpkg to see which packages are not currently installed."""
        missing = []
        for pkg in packages:
            try:
                # dpkg -s <pkg> returns 0 if installed, 1 if not.
                result = subprocess.run(
                    ["dpkg", "-s", pkg],
                    capture_output=True,
                    text=True,
                    check=False
                )
                if result.returncode != 0 or "Status: install ok installed" not in result.stdout:
                    missing.append(pkg)
            except Exception as e:
                # If dpkg command fails or is blocked, treat package as missing
                logger.debug(f"Failed to check package status via dpkg for {pkg}: {e}")
                missing.append(pkg)
        return missing

    def install(self, packages: list[str], dry_run: bool = False) -> None:
        """Installs missing packages using apt-get.

        Args:
            packages: List of package names to check and install.
            dry_run: Whether to simulate installation.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("apt-get or dpkg is not available on this platform")

        logger.info("Checking package status via dpkg...")
        missing = self._get_missing_packages(packages)

        if not missing:
            logger.info("All apt packages are already installed.")
            return

        if dry_run:
            logger.info(f"[Dry Run] apt packages to install: {', '.join(missing)}")
            return

        logger.info(f"Installing missing apt packages: {', '.join(missing)}")
        # apt-get install -y <pkg_list>
        # Note: requires root permissions, which is up to the caller's environment setup.
        cmd = ["apt-get", "install", "-y"] + missing
        try:
            self.execute_with_retry(cmd)
            logger.info("Apt package installation completed successfully.")
        except Exception as e:
            raise ProviderError(f"Apt installation failed: {e}") from e
