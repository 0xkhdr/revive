"""Nix package provider orchestration for NixOS and nix-on-other-distro setups."""

import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.nix")


class NixProvider(BaseProvider):
    """Orchestrates Nix packages via nix-env.

    Supports both NixOS and nix-on-other-distro (Nix installed on Ubuntu/macOS etc.).
    Uses nix-env for broad compatibility. Packages are specified as nixpkgs attribute names
    (e.g. 'ripgrep', 'neovim') and installed via `nix-env -iA nixpkgs.<pkg>`.
    """

    def __init__(self) -> None:
        super().__init__("nix-env")

    def is_available(self) -> bool:
        """Checks if nix-env is available on the system."""
        import shutil

        return shutil.which("nix-env") is not None

    def is_installed(self, pkg: str) -> bool:
        """Checks if a nix package is installed in the current nix-env profile.

        Args:
            pkg: Nix package attribute name (e.g. 'ripgrep', 'neovim').

        Returns:
            True if the package is installed, False otherwise.
        """
        try:
            result = subprocess.run(
                ["nix-env", "-q", pkg],
                capture_output=True,
                text=True,
                check=False,
            )
            return result.returncode == 0 and pkg in result.stdout
        except Exception as e:
            logger.debug(f"Failed to check nix-env package status for '{pkg}': {e}")
            return False

    def install(self, packages: list[str], dry_run: bool = False, use_cache: bool = True) -> None:
        """Installs missing nix packages via nix-env -iA nixpkgs.<pkg>.

        Args:
            packages: List of nixpkgs attribute names (e.g. ['ripgrep', 'neovim']).
            dry_run: Whether to simulate installation without making changes.
            use_cache: If True (default), consult the PackageCache for idempotency.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("nix-env is not available on this platform")

        missing = self.filter_missing(packages, use_cache=use_cache)

        if not missing:
            logger.info("All nix packages are already installed.")
            return

        if dry_run:
            logger.info(f"[Dry Run] nix packages to install: {', '.join(missing)}")
            return

        logger.info(f"Installing missing nix packages: {', '.join(missing)}")
        from rv.providers.base import PackageCache

        for pkg in missing:
            cmd = ["nix-env", "-iA", f"nixpkgs.{pkg}"]
            try:
                self.execute_with_retry(cmd)
                PackageCache.mark_installed(self.name, [pkg])
                logger.info(f"Nix package '{pkg}' installed successfully.")
            except Exception as e:
                raise ProviderError(f"Nix installation of '{pkg}' failed: {e}") from e
