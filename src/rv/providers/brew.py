"""Brew package provider orchestration."""

from rv.logging.audit import AuditLogger
from rv.providers.base import BaseProvider, ProviderError
from rv.security.tempfile import SecureTempFile

logger = AuditLogger.get_logger("rv.providers.brew")


class BrewProvider(BaseProvider):
    """Orchestrates Homebrew packages using Brewfile bundle."""

    def __init__(self) -> None:
        super().__init__("brew")

    def is_installed(self, pkg: str) -> bool:
        """Checks if a brew formula/cask is installed.

        Args:
            pkg: Formula or cask name (prefix with 'cask:' for casks).

        Returns:
            True if installed, False otherwise.
        """
        import subprocess

        # Normalize the package name (strip 'cask:' or 'tap:' prefixes for checking)
        actual_pkg = pkg
        if pkg.startswith("cask:"):
            actual_pkg = pkg.split(":", 1)[1]
            try:
                result = subprocess.run(
                    ["brew", "list", "--cask", actual_pkg], capture_output=True, text=True, check=False
                )
                return result.returncode == 0
            except Exception:
                return False
        elif pkg.startswith("tap:"):
            # Taps are always considered "installed" for idempotency check purposes
            return False

        try:
            result = subprocess.run(["brew", "list", actual_pkg], capture_output=True, text=True, check=False)
            return result.returncode == 0
        except Exception:
            return False

    def install(self, packages: list[str], dry_run: bool = False) -> None:
        """Installs Homebrew packages using a temporary Brewfile.

        Args:
            packages: List of formula/cask/tap strings.
            dry_run: Whether to simulate installation.
        """
        if not packages:
            return

        if not dry_run and not self.is_available():
            raise ProviderError("Homebrew ('brew') is not installed or not in system PATH")

        # Compile Brewfile contents
        brewfile_lines = []
        for pkg in packages:
            # Handle taps vs casks vs normal formulae
            # Simplistic detection: if it starts with homebrew/cask/ or contains /cask/ it is a cask
            # If it has a slash but isn't a tap, e.g. user/repo/pkg, treat it as a formula, but could tap first.
            # Standard Brewfile format:
            # tap "user/repo"
            # brew "formula"
            # cask "caskname"
            if pkg.startswith("cask:"):
                cask_name = pkg.split(":", 1)[1]
                brewfile_lines.append(f'cask "{cask_name}"')
            elif pkg.startswith("tap:"):
                tap_name = pkg.split(":", 1)[1]
                brewfile_lines.append(f'tap "{tap_name}"')
            else:
                brewfile_lines.append(f'brew "{pkg}"')

        brewfile_content = "\n".join(brewfile_lines) + "\n"

        if dry_run:
            logger.info(f"[Dry Run] Would create Brewfile with contents:\n{brewfile_content}")
            return

        # Write to secure temp file and execute
        with SecureTempFile.file() as tmp_path:
            with open(tmp_path, "w", encoding="utf-8") as f:
                f.write(brewfile_content)

            logger.info("Installing packages via brew bundle...")
            try:
                # brew bundle install --file=<tempfile>
                self.execute_with_retry(["brew", "bundle", "--file", tmp_path])
                logger.info("Homebrew bundle restoration completed successfully.")
            except Exception as e:
                raise ProviderError(f"Homebrew installation failed: {e}") from e
