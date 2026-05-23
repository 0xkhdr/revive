"""Pip package provider orchestration for Python developer tools."""

import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.pip")


class PipProvider(BaseProvider):
    """Orchestrates Python package installation via pip install --user."""

    def __init__(self) -> None:
        super().__init__("pip")

    def is_available(self) -> bool:
        """Checks if pip is available on the system."""
        import shutil

        return shutil.which("pip") is not None or shutil.which("pip3") is not None

    def _get_pip_cmd(self) -> str:
        """Returns the available pip executable name."""
        import shutil

        if shutil.which("pip3"):
            return "pip3"
        return "pip"

    def is_installed(self, pkg: str) -> bool:
        """Checks if a Python package is installed via pip show.

        Args:
            pkg: PyPI package name to check.

        Returns:
            True if installed, False otherwise.
        """
        try:
            result = subprocess.run(
                [self._get_pip_cmd(), "show", pkg],
                capture_output=True,
                text=True,
                check=False,
            )
            return result.returncode == 0
        except Exception as e:
            logger.debug(f"Failed to check pip package status for '{pkg}': {e}")
            return False

    def install(self, packages: list[str], dry_run: bool = False) -> None:
        """Installs missing Python packages via pip install --user.

        Args:
            packages: List of PyPI package names to install.
            dry_run: Whether to simulate installation without making changes.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("pip is not available on this platform")

        # Filter out already-installed packages for idempotency
        missing = [pkg for pkg in packages if not self.is_installed(pkg)]

        if not missing:
            logger.info("All pip packages are already installed.")
            return

        if dry_run:
            logger.info(f"[Dry Run] pip packages to install: {', '.join(missing)}")
            return

        logger.info(f"Installing missing pip packages: {', '.join(missing)}")
        pip_cmd = self._get_pip_cmd()
        cmd = [pip_cmd, "install", "--user"] + missing
        try:
            self.execute_with_retry(cmd)
            logger.info("Pip package installation completed successfully.")
        except Exception as e:
            raise ProviderError(f"Pip installation failed: {e}") from e
