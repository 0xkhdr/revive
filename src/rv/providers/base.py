"""Base package provider interface and utility methods.
"""

import subprocess
import time
from typing import Any

from rv.logging.audit import AuditLogger

logger = AuditLogger.get_logger("rv.providers.base")


class ProviderError(Exception):
    """Raised when package orchestration fails."""
    pass


class BaseProvider:
    """Abstract base class for package orchestrators."""

    def __init__(self, name: str) -> None:
        self.name = name

    def is_available(self) -> bool:
        """Check if the package manager command exists in the system PATH.

        Returns:
            True if available, False otherwise.
        """
        import shutil
        return shutil.which(self.name) is not None

    def execute_with_retry(
        self,
        cmd: list[str],
        retries: int = 3,
        backoff_factor: float = 2.0,
        **kwargs: Any
    ) -> subprocess.CompletedProcess[str]:
        """Executes a command with exponential backoff on failure.

        Args:
            cmd: Command arguments list.
            retries: Number of allowed retries.
            backoff_factor: Backoff multiplier (e.g. 2.0 means 2s, 4s, 8s).

        Returns:
            CompletedProcess result.
        """
        last_error: Exception | None = None
        current_delay = 2.0

        for attempt in range(1, retries + 1):
            try:
                logger.debug(f"Executing cmd (attempt {attempt}/{retries}): {' '.join(cmd)}")
                result = subprocess.run(
                    cmd,
                    check=True,
                    capture_output=True,
                    text=True,
                    **kwargs
                )
                return result
            except (subprocess.CalledProcessError, FileNotFoundError, PermissionError) as e:
                last_error = e
                # Check if it was a CalledProcessError to get stderr
                stderr = e.stderr if isinstance(e, subprocess.CalledProcessError) else str(e)
                logger.warning(
                    f"Command failed (attempt {attempt}/{retries}) for provider '{self.name}': {stderr}"
                )
                if attempt < retries:
                    logger.info(f"Retrying in {current_delay}s...")
                    time.sleep(current_delay)
                    current_delay *= backoff_factor

        raise ProviderError(
            f"Failed to execute command after {retries} attempts: {last_error}"
        ) from last_error

    def install(self, packages: list[str], dry_run: bool = False) -> None:
        """Installs the given packages using the native tool.

        Args:
            packages: List of packages/references to install.
            dry_run: If True, previews the install without execution.
        """
        raise NotImplementedError("Subclasses must implement the install method")
