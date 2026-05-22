"""Snap package provider orchestration."""

import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.snap")


class SnapProvider(BaseProvider):
    """Orchestrates Snap packages."""

    def __init__(self) -> None:
        super().__init__("snap")

    def _is_installed(self, pkg: str) -> bool:
        """Checks if a snap package is installed via snap list."""
        try:
            # snap list <pkg> returns 0 if installed, 1 if not.
            res = subprocess.run(["snap", "list", pkg], capture_output=True, check=False)
            return res.returncode == 0
        except Exception:
            return False

    def install(self, packages: list[str], dry_run: bool = False) -> None:
        """Installs missing snap packages.

        Args:
            packages: List of snap package names.
            dry_run: Whether to simulate installation.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("snap CLI is not installed or not in system PATH")

        missing = []
        for pkg in packages:
            # Snap packages might be specified with flags (e.g. classic) like "classic:code" or "code --classic"
            # To be robust, let's allow "classic:code" prefix or similar.
            actual_pkg = pkg
            classic_flag = False
            if pkg.startswith("classic:"):
                actual_pkg = pkg.split(":", 1)[1]
                classic_flag = True

            if not self._is_installed(actual_pkg):
                missing.append((pkg, actual_pkg, classic_flag))

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
                logger.info(f"Successfully installed Snap: {pkg}")
            except Exception as e:
                raise ProviderError(f"Snap installation failed for {pkg}: {e}") from e
