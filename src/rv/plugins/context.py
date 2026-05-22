"""ReviveContext model representing the environment context provided to plugins.
"""

from pydantic import BaseModel, Field


class ReviveContext(BaseModel):
    """Context object passed to plugins/hooks containing current run metadata."""
    repo_dir: str = Field(..., description="Absolute path to the repository being restored")
    profile_name: str = Field(..., description="The deployment profile name")
    dry_run: bool = Field(..., description="Whether this is a dry-run execution")
    targets: list[str] = Field(default_factory=list, description="Target filesystem paths affected by this transaction")
    hook_type: str = Field(..., description="The name of the hook (e.g. pre-restore, post-restore)")
