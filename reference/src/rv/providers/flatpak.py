"""Flatpak package provider orchestration."""

import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.flatpak")


class FlatpakProvider(BaseProvider):
    """Orchestrates Flatpak applications."""

    def __init__(self) -> None:
        super().__init__("flatpak")

    def is_installed(self, ref: str) -> bool:
        """Checks if a flatpak ref is already installed via flatpak info.

        Args:
            ref: Flatpak application ref to check.

        Returns:
            True if installed, False otherwise.
        """
        try:
            # flatpak info <ref> returns 0 if installed, 1 if not.
            res = subprocess.run(["flatpak", "info", ref], capture_output=True, check=False)
            return res.returncode == 0
        except Exception:
            return False

    def install(self, packages: list[str], dry_run: bool = False, use_cache: bool = True) -> None:
        """Installs missing flatpak packages.

        Args:
            packages: List of flatpak application refs.
            dry_run: Whether to simulate installation.
            use_cache: If True (default), consult the PackageCache for idempotency.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("flatpak CLI is not installed or not in system PATH")

        from rv.providers.base import PackageCache

        missing = []
        for ref in packages:
            if use_cache and PackageCache.is_installed(self.name, ref):
                logger.debug(f"[Cache] flatpak '{ref}' already in cache. Skipping.")
                continue
            if not self.is_installed(ref):
                missing.append(ref)
            else:
                PackageCache.mark_installed(self.name, [ref])

        if not missing:
            logger.info("All flatpak packages are already installed.")
            return

        if dry_run:
            logger.info(f"[Dry Run] flatpak packages would be installed: {', '.join(missing)}")
            return

        logger.info(f"Installing missing flatpak applications: {', '.join(missing)}")
        for ref in missing:
            try:
                self.execute_with_retry(["flatpak", "install", "-y", ref])
                PackageCache.mark_installed(self.name, [ref])
                logger.info(f"Successfully installed Flatpak: {ref}")
            except Exception as e:
                raise ProviderError(f"Flatpak installation failed for {ref}: {e}") from e
