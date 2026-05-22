"""Plugin manifest models and discovery loader.
"""

import os

import yaml
from pydantic import BaseModel, Field


class PluginPermissions(BaseModel):
    """Permissions requested by the plugin."""
    network: bool = Field(default=False, description="Allow network socket connections")
    shell: bool = Field(default=False, description="Allow subprocess execution")
    allowed_paths: list[str] = Field(default_factory=list, description="Allowed directories for filesystem access")


class PluginManifest(BaseModel):
    """Plugin specification declared in plugin.yaml."""
    name: str = Field(..., description="Unique identifier for the plugin")
    version: str = Field(..., description="Plugin version string")
    entrypoint: str = Field(..., description="Path to the entry point script relative to plugin.yaml")
    permissions: PluginPermissions = Field(default_factory=PluginPermissions)
    hooks: list[str] = Field(default_factory=list, description="List of hooks to subscribe to")
    timeout: int = Field(default=30, description="Execution timeout in seconds")



class Plugin:
    """Represents a discovered and loaded plugin."""
    def __init__(self, directory: str, manifest: PluginManifest):
        self.directory = os.path.abspath(directory)
        self.manifest = manifest

    @property
    def entrypoint_path(self) -> str:
        """Resolves absolute path to plugin's entry point."""
        return os.path.abspath(os.path.join(self.directory, self.manifest.entrypoint))


class PluginLoader:
    """Utility to discover and parse plugin manifests."""

    @staticmethod
    def load_from_directory(plugin_dir: str) -> Plugin | None:
        """Parses plugin.yaml manifest in a given directory."""
        yaml_path = os.path.join(plugin_dir, "plugin.yaml")
        if not os.path.exists(yaml_path):
            return None
        try:
            with open(yaml_path, encoding="utf-8") as f:
                content = yaml.safe_load(f)
            if not isinstance(content, dict):
                return None
            manifest = PluginManifest.model_validate(content)
            return Plugin(plugin_dir, manifest)
        except Exception:
            return None

    @classmethod
    def discover_plugins(cls, repo_dir: str) -> list[Plugin]:
        """Scans workspace and system-wide paths for plugins, resolving duplicates by precedence."""
        plugins: list[Plugin] = []
        seen_names: set[str] = set()

        search_dirs = [
            os.path.join(repo_dir, "plugins"),
            os.path.expanduser("~/.config/rv/plugins"),
            os.path.join(os.path.dirname(__file__), "builtin")
        ]

        for s_dir in search_dirs:
            if not os.path.isdir(s_dir):
                continue
            try:
                for entry in os.listdir(s_dir):
                    full_path = os.path.join(s_dir, entry)
                    if os.path.isdir(full_path):
                        plugin = cls.load_from_directory(full_path)
                        if plugin:
                            if plugin.manifest.name in seen_names:
                                continue
                            seen_names.add(plugin.manifest.name)
                            plugins.append(plugin)
            except Exception:
                pass

        return plugins
