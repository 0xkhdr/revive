"""Platform detection utilities for system capabilities and OS profiles."""

import os
import shutil
import sys


class Platform:
    """Detects and caches OS, distribution, and tool command locations."""

    _cached_os: str | None = None
    _cached_distro: str | None = None
    _cached_tools: dict[str, str | None] = {}

    @classmethod
    def get_os(cls) -> str:
        """Returns the current operating system (linux, darwin, freebsd, win32)."""
        if cls._cached_os is None:
            cls._cached_os = sys.platform
        return cls._cached_os

    @classmethod
    def is_linux(cls) -> bool:
        """True if running on Linux."""
        return cls.get_os().startswith("linux")

    @classmethod
    def is_macos(cls) -> bool:
        """True if running on macOS (Darwin)."""
        return cls.get_os() == "darwin"

    @classmethod
    def get_distro(cls) -> str:
        """Returns the Linux distribution name if applicable, or empty string."""
        if cls._cached_distro is not None:
            return cls._cached_distro

        if not cls.is_linux():
            cls._cached_distro = ""
            return cls._cached_distro

        # Read /etc/os-release to get the distribution ID
        try:
            if os.path.exists("/etc/os-release"):
                with open("/etc/os-release") as f:
                    for line in f:
                        if line.startswith("ID="):
                            cls._cached_distro = line.strip().split("=")[1].strip('"').lower()
                            return cls._cached_distro
        except OSError:
            pass

        cls._cached_distro = "unknown"
        return cls._cached_distro

    @classmethod
    def find_tool(cls, tool_name: str) -> str | None:
        """Finds the absolute path of a command tool in system PATH."""
        if tool_name in cls._cached_tools:
            return cls._cached_tools[tool_name]

        path = shutil.which(tool_name)
        cls._cached_tools[tool_name] = path
        return path

    @classmethod
    def has_tool(cls, tool_name: str) -> bool:
        """Checks if a tool is available in system PATH."""
        return cls.find_tool(tool_name) is not None

    @classmethod
    def get_available_package_managers(cls) -> dict[str, bool]:
        """Checks which package managers are natively available on this system."""
        return {
            "brew": cls.has_tool("brew"),
            "apt": cls.has_tool("apt-get"),
            "flatpak": cls.has_tool("flatpak"),
            "snap": cls.has_tool("snap"),
            "docker": cls.has_tool("docker"),
            "node": cls.has_tool("node") or cls.has_tool("npm"),
        }
