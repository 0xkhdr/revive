"""Pydantic models for Workspace management."""

from datetime import datetime

from pydantic import BaseModel, Field


class Workspace(BaseModel):
    """Represents an initialized Revive repository."""

    name: str = Field(..., description="Short name for the workspace")
    path: str = Field(..., description="Absolute path to the repository directory")
    last_accessed: datetime = Field(default_factory=datetime.now)


class WorkspaceConfig(BaseModel):
    """Global configuration for workspaces stored in ~/.config/rv/workspaces.yaml."""

    workspaces: list[Workspace] = Field(default_factory=list)
    default_workspace: str | None = Field(None, description="Name of the default workspace")
