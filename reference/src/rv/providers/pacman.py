"""Pacman package provider orchestration for Arch Linux / Manjaro systems."""

import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.pacman")


class PacmanProvider(BaseProvider):
    """Orchestrates Arch Linux packages via pacman."""

    def __init__(self) -> None:
        super().__init__("pacman")

    def is_available(self) -> bool:
        """Checks if pacman is available on the system."""
        import shutil

        return shutil.which("pacman") is not None

    def is_installed(self, pkg: str) -> bool:
        """Checks if a package is currently installed using pacman -Q.

        Args:
            pkg: Package name to check.

        Returns:
            True if the package is installed, False otherwise.
        """
        try:
            result = subprocess.run(
                ["pacman", "-Q", pkg],
                capture_output=True,
                text=True,
                check=False,
            )
            return result.returncode == 0
        except Exception as e:
            logger.debug(f"Failed to check pacman package status for '{pkg}': {e}")
            return False

    def install(self, packages: list[str], dry_run: bool = False, use_cache: bool = True) -> None:
        """Installs missing packages using pacman -S --noconfirm.

        Args:
            packages: List of package names to check and install.
            dry_run: Whether to simulate installation without making changes.
            use_cache: If True (default), consult the PackageCache for idempotency.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("pacman is not available on this platform")

        missing = self.filter_missing(packages, use_cache=use_cache)

        if not missing:
            logger.info("All pacman packages are already installed.")
            return

        if dry_run:
            logger.info(f"[Dry Run] pacman packages to install: {', '.join(missing)}")
            return

        logger.info(f"Installing missing pacman packages: {', '.join(missing)}")
        cmd = ["pacman", "-S", "--noconfirm"] + missing
        try:
            self.execute_with_retry(cmd)
            from rv.providers.base import PackageCache

            PackageCache.mark_installed(self.name, missing)
            logger.info("Pacman package installation completed successfully.")
        except Exception as e:
            raise ProviderError(f"Pacman installation failed: {e}") from e
