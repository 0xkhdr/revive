"""Flatpak package provider orchestration.
"""

import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.flatpak")


class FlatpakProvider(BaseProvider):
    """Orchestrates Flatpak applications."""

    def __init__(self) -> None:
        super().__init__("flatpak")

    def _is_installed(self, ref: str) -> bool:
        """Checks if a flatpak ref is already installed via flatpak info."""
        try:
            # flatpak info <ref> returns 0 if installed, 1 if not.
            res = subprocess.run(
                ["flatpak", "info", ref],
                capture_output=True,
                check=False
            )
            return res.returncode == 0
        except Exception:
            return False

    def install(self, packages: list[str], dry_run: bool = False) -> None:
        """Installs missing flatpak packages.

        Args:
            packages: List of flatpak application refs.
            dry_run: Whether to simulate installation.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("flatpak CLI is not installed or not in system PATH")

        missing = []
        for ref in packages:
            if not self._is_installed(ref):
                missing.append(ref)

        if not missing:
            logger.info("All flatpak packages are already installed.")
            return

        if dry_run:
            logger.info(f"[Dry Run] flatpak packages would be installed: {', '.join(missing)}")
            return

        logger.info(f"Installing missing flatpak applications: {', '.join(missing)}")
        for ref in missing:
            # flatpak install -y <ref>
            # Flatpaks can sometimes be in user space or system space. Standard -y is system unless --user is specified.
            # We follow the plan's specification: flatpak install -y {ref}
            try:
                self.execute_with_retry(["flatpak", "install", "-y", ref])
                logger.info(f"Successfully installed Flatpak: {ref}")
            except Exception as e:
                raise ProviderError(f"Flatpak installation failed for {ref}: {e}") from e
