"""Base package provider interface and utility methods."""

import json
import os
import subprocess
import time
from abc import abstractmethod
from typing import Any

from rv.logging.audit import AuditLogger

logger = AuditLogger.get_logger("rv.providers.base")

# TTL for the package idempotency cache in seconds (24 hours)
_CACHE_TTL_SECONDS: int = 86400


class ProviderError(Exception):
    """Raised when package orchestration fails."""

    pass


class PackageCache:
    """Thread-safe, file-backed package installation state cache.

    Cache file format: ~/.config/rv/package-cache.json
    Structure:
        {
          "<provider_name>": {
            "installed": ["pkg1", "pkg2"],
            "last_updated": 1716000000.0
          }
        }

    The cache expires per-provider after `_CACHE_TTL_SECONDS`.
    Use `invalidate(provider_name)` to force a refresh on the next check.
    """

    _CACHE_PATH: str = os.path.expanduser("~/.config/rv/package-cache.json")

    @classmethod
    def _load(cls) -> dict[str, Any]:
        """Loads the cache JSON from disk. Returns empty dict on any error."""
        if not os.path.exists(cls._CACHE_PATH):
            return {}
        try:
            with open(cls._CACHE_PATH, encoding="utf-8") as f:
                data = json.load(f)
            if not isinstance(data, dict):
                return {}
            result: dict[str, Any] = data
            return result
        except Exception:
            return {}

    @classmethod
    def _save(cls, data: dict[str, Any]) -> None:
        """Persists the cache JSON to disk atomically."""
        os.makedirs(os.path.dirname(cls._CACHE_PATH), exist_ok=True)
        tmp_path = cls._CACHE_PATH + ".tmp"
        try:
            with open(tmp_path, "w", encoding="utf-8") as f:
                json.dump(data, f, indent=2)
            os.replace(tmp_path, cls._CACHE_PATH)
        except Exception as e:
            logger.warning(f"Failed to persist package cache: {e}")
            try:
                os.unlink(tmp_path)
            except OSError:
                pass

    @classmethod
    def is_installed(cls, provider_name: str, pkg: str) -> bool:
        """Returns True if the package is in the valid (non-expired) cache for the provider.

        Args:
            provider_name: Provider identifier (e.g. 'apt-get', 'brew').
            pkg: Package name to look up.

        Returns:
            True if pkg is cached as installed and cache has not expired.
        """
        data = cls._load()
        entry = data.get(provider_name)
        if not isinstance(entry, dict):
            return False

        last_updated = entry.get("last_updated", 0.0)
        if time.time() - last_updated > _CACHE_TTL_SECONDS:
            return False

        installed = entry.get("installed", [])
        return pkg in installed

    @classmethod
    def mark_installed(cls, provider_name: str, packages: list[str]) -> None:
        """Adds a list of packages to the provider's cache entry.

        Args:
            provider_name: Provider identifier.
            packages: Package names to mark as installed.
        """
        data = cls._load()
        if provider_name not in data or not isinstance(data[provider_name], dict):
            data[provider_name] = {"installed": [], "last_updated": time.time()}

        installed: list[str] = data[provider_name].get("installed", [])
        for pkg in packages:
            if pkg not in installed:
                installed.append(pkg)
        data[provider_name]["installed"] = installed
        data[provider_name]["last_updated"] = time.time()
        cls._save(data)

    @classmethod
    def invalidate(cls, provider_name: str) -> None:
        """Clears the cache entry for a specific provider.

        Args:
            provider_name: Provider identifier to invalidate.
        """
        data = cls._load()
        if provider_name in data:
            del data[provider_name]
            cls._save(data)
            logger.info(f"Package cache invalidated for provider: {provider_name}")

    @classmethod
    def invalidate_all(cls) -> None:
        """Clears the entire package cache across all providers."""
        try:
            if os.path.exists(cls._CACHE_PATH):
                os.unlink(cls._CACHE_PATH)
            logger.info("Full package cache invalidated.")
        except OSError as e:
            logger.warning(f"Failed to invalidate full package cache: {e}")


class BaseProvider:
    """Abstract base class for package orchestrators with idempotency cache support."""

    def __init__(self, name: str) -> None:
        self.name = name

    def is_available(self) -> bool:
        """Check if the package manager command exists in the system PATH.

        Returns:
            True if available, False otherwise.
        """
        import shutil

        return shutil.which(self.name) is not None

    @abstractmethod
    def is_installed(self, pkg: str) -> bool:
        """Check if a specific package is currently installed on the system.

        This method MUST be implemented by all providers for idempotency.

        Args:
            pkg: Package name to check.

        Returns:
            True if the package is installed, False otherwise.
        """
        ...

    def filter_missing(self, packages: list[str], use_cache: bool = True) -> list[str]:
        """Returns packages that are not yet installed, using the idempotency cache.

        Args:
            packages: List of package names to check.
            use_cache: If True, consult the PackageCache before calling is_installed().
                       Set to False (--force-packages) to bypass the cache.

        Returns:
            Filtered list of packages that need to be installed.
        """
        missing = []
        for pkg in packages:
            if use_cache and PackageCache.is_installed(self.name, pkg):
                logger.debug(f"[Cache] '{pkg}' already in cache for provider '{self.name}'. Skipping.")
                continue
            if self.is_installed(pkg):
                # Update cache for future runs
                PackageCache.mark_installed(self.name, [pkg])
            else:
                missing.append(pkg)
        return missing

    def execute_with_retry(
        self, cmd: list[str], retries: int = 3, backoff_factor: float = 2.0, **kwargs: Any
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
                result = subprocess.run(cmd, check=True, capture_output=True, text=True, **kwargs)
                return result
            except (subprocess.CalledProcessError, FileNotFoundError, PermissionError) as e:
                last_error = e
                # Check if it was a CalledProcessError to get stderr
                stderr = e.stderr if isinstance(e, subprocess.CalledProcessError) else str(e)
                logger.warning(f"Command failed (attempt {attempt}/{retries}) for provider '{self.name}': {stderr}")
                if attempt < retries:
                    logger.info(f"Retrying in {current_delay}s...")
                    time.sleep(current_delay)
                    current_delay *= backoff_factor

        raise ProviderError(f"Failed to execute command after {retries} attempts: {last_error}") from last_error

    def install(self, packages: list[str], dry_run: bool = False) -> None:
        """Installs the given packages using the native tool.

        Args:
            packages: List of packages/references to install.
            dry_run: If True, previews the install without execution.
        """
        raise NotImplementedError("Subclasses must implement the install method")
