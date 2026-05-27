"""Cargo package provider orchestration for Rust developer tools."""

import subprocess

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError

logger = AuditLogger.get_logger("rv.providers.cargo")


class CargoProvider(BaseProvider):
    """Orchestrates Rust binary installation via cargo install."""

    def __init__(self) -> None:
        super().__init__("cargo")

    def is_available(self) -> bool:
        """Checks if cargo is available on the system."""
        import shutil

        return shutil.which("cargo") is not None

    def is_installed(self, pkg: str) -> bool:
        """Checks if a cargo package is installed by querying installed binaries.

        Args:
            pkg: Cargo crate name to check.

        Returns:
            True if the package/binary is installed, False otherwise.
        """
        try:
            result = subprocess.run(
                ["cargo", "install", "--list"],
                capture_output=True,
                text=True,
                check=False,
            )
            if result.returncode != 0:
                return False
            # Output format: "pkg v1.2.3:\n    /path/to/binary"
            return any(line.startswith(f"{pkg} ") or line.startswith(f"{pkg}\n") for line in result.stdout.splitlines())
        except Exception as e:
            logger.debug(f"Failed to check cargo install status for '{pkg}': {e}")
            return False

    def install(self, packages: list[str], dry_run: bool = False, use_cache: bool = True) -> None:
        """Installs missing Rust tools via cargo install.

        Args:
            packages: List of crate names to install (e.g. ['ripgrep', 'fd-find']).
            dry_run: Whether to simulate installation without making changes.
            use_cache: If True (default), consult the PackageCache for idempotency.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("cargo is not available on this platform")

        missing = self.filter_missing(packages, use_cache=use_cache)

        if not missing:
            logger.info("All cargo packages are already installed.")
            return

        if dry_run:
            logger.info(f"[Dry Run] cargo packages to install: {', '.join(missing)}")
            return

        logger.info(f"Installing missing cargo packages: {', '.join(missing)}")
        cmd = ["cargo", "install"] + missing
        try:
            self.execute_with_retry(cmd)
            from rv.providers.base import PackageCache

            PackageCache.mark_installed(self.name, missing)
            logger.info("Cargo package installation completed successfully.")
        except Exception as e:
            raise ProviderError(f"Cargo installation failed: {e}") from e
