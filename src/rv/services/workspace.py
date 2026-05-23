"""Service for managing Revive workspaces."""

import os
from datetime import datetime
from pathlib import Path

import yaml

from rv.models.workspace import Workspace, WorkspaceConfig


class WorkspaceService:
    """Handles discovery, registration, and management of workspaces."""

    CONFIG_PATH = os.path.expanduser("~/.config/rv/workspaces.yaml")

    @classmethod
    def _ensure_config_dir(cls) -> None:
        """Ensures the configuration directory exists."""
        os.makedirs(os.path.dirname(cls.CONFIG_PATH), exist_ok=True)

    @classmethod
    def load_config(cls) -> WorkspaceConfig:
        """Loads the workspace configuration from disk."""
        if not os.path.exists(cls.CONFIG_PATH):
            return WorkspaceConfig(default_workspace=None)

        try:
            with open(cls.CONFIG_PATH, encoding="utf-8") as f:
                data = yaml.safe_load(f)
                if not data:
                    return WorkspaceConfig(default_workspace=None)
                return WorkspaceConfig(**data)
        except Exception:
            return WorkspaceConfig(default_workspace=None)

    @classmethod
    def save_config(cls, config: WorkspaceConfig) -> None:
        """Saves the workspace configuration to disk."""
        cls._ensure_config_dir()
        with open(cls.CONFIG_PATH, "w", encoding="utf-8") as f:
            yaml.dump(config.model_dump(mode="json"), f)

    @classmethod
    def register_workspace(cls, path: str, name: str | None = None) -> Workspace:
        """Registers a new workspace at the given path."""
        abs_path = os.path.abspath(path)
        if not name:
            name = os.path.basename(abs_path)

        config = cls.load_config()

        # Check if already exists
        for ws in config.workspaces:
            if ws.path == abs_path:
                ws.last_accessed = datetime.now()
                cls.save_config(config)
                return ws

        # Add new
        new_ws = Workspace(name=name, path=abs_path, last_accessed=datetime.now())
        config.workspaces.append(new_ws)
        cls.save_config(config)
        return new_ws

    @classmethod
    def list_workspaces(cls) -> list[Workspace]:
        """Returns a list of all registered workspaces."""
        config = cls.load_config()
        return config.workspaces

    @classmethod
    def get_current_workspace(cls) -> Workspace | None:
        """Detects if the current directory or its parents is a registered workspace."""
        current_path = os.getcwd()
        config = cls.load_config()

        # Sort workspaces by path length descending to match the most specific one first
        sorted_workspaces = sorted(config.workspaces, key=lambda x: len(x.path), reverse=True)

        for ws in sorted_workspaces:
            if current_path.startswith(ws.path):
                return ws
        return None

    @classmethod
    def remove_workspace(cls, name: str) -> bool:
        """Removes a workspace by name."""
        config = cls.load_config()
        initial_count = len(config.workspaces)
        config.workspaces = [ws for ws in config.workspaces if ws.name != name]

        if len(config.workspaces) < initial_count:
            cls.save_config(config)
            return True
        return False

    @classmethod
    def update_workspace(cls, original_path: str, new_name: str | None = None, new_path: str | None = None) -> Workspace | None:
        """Updates a workspace's name and/or path."""
        config = cls.load_config()
        for ws in config.workspaces:
            if ws.path == original_path:
                if new_name:
                    ws.name = new_name
                if new_path:
                    abs_new_path = os.path.abspath(os.path.expanduser(new_path))
                    ws.path = abs_new_path
                ws.last_accessed = datetime.now()
                cls.save_config(config)
                return ws
        return None

    @classmethod
    def remove_workspaces(cls, paths: list[str]) -> int:
        """Removes workspaces by their paths."""
        config = cls.load_config()
        initial_count = len(config.workspaces)
        config.workspaces = [ws for ws in config.workspaces if ws.path not in paths]

        removed = initial_count - len(config.workspaces)
        if removed > 0:
            cls.save_config(config)
        return removed
