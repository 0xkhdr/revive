"""DNF package provider orchestration for Fedora / RHEL / CentOS Stream systems."""

import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.dnf")


class DnfProvider(BaseProvider):
    """Orchestrates Fedora/RHEL packages via dnf."""

    def __init__(self) -> None:
        super().__init__("dnf")

    def is_available(self) -> bool:
        """Checks if dnf is available on the system."""
        import shutil

        return shutil.which("dnf") is not None

    def is_installed(self, pkg: str) -> bool:
        """Checks if a package is currently installed via rpm -q.

        Args:
            pkg: Package name to check.

        Returns:
            True if installed, False otherwise.
        """
        try:
            result = subprocess.run(
                ["rpm", "-q", pkg],
                capture_output=True,
                text=True,
                check=False,
            )
            return result.returncode == 0
        except Exception as e:
            logger.debug(f"Failed to check rpm package status for '{pkg}': {e}")
            return False

    def install(self, packages: list[str], dry_run: bool = False, use_cache: bool = True) -> None:
        """Installs missing packages using dnf install -y.

        Args:
            packages: List of package names to check and install.
            dry_run: Whether to simulate installation without making changes.
            use_cache: If True (default), consult the PackageCache for idempotency.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("dnf is not available on this platform")

        missing = self.filter_missing(packages, use_cache=use_cache)

        if not missing:
            logger.info("All dnf packages are already installed.")
            return

        if dry_run:
            logger.info(f"[Dry Run] dnf packages to install: {', '.join(missing)}")
            return

        logger.info(f"Installing missing dnf packages: {', '.join(missing)}")
        cmd = ["dnf", "install", "-y"] + missing
        try:
            self.execute_with_retry(cmd)
            from rv.providers.base import PackageCache

            PackageCache.mark_installed(self.name, missing)
            logger.info("DNF package installation completed successfully.")
        except Exception as e:
            raise ProviderError(f"DNF installation failed: {e}") from e
