"""Pydantic schemas for Manifest, Assets, Secrets, Packages, and Profiles.

Enforces strict v2 validation and domain-specific rules.
"""

from enum import StrEnum
from typing import Any

from pydantic import BaseModel, Field, field_validator, model_validator

# Supported manifest schema versions. Raise UnsupportedSchemaVersionError for anything else.
_SUPPORTED_SCHEMA_VERSIONS: frozenset[int] = frozenset({1, 2})


class UnsupportedSchemaVersionError(ValueError):
    """Raised when a manifest declares a schema version that this rv release does not support.

    Prevents silent data corruption when a future schema adds conflicting or renamed fields
    that an older Pydantic model would accept as valid garbage.
    """

    def __init__(self, version: object) -> None:
        supported = ", ".join(str(v) for v in sorted(_SUPPORTED_SCHEMA_VERSIONS))
        super().__init__(
            f"Unsupported manifest schema version: {version!r}. "
            f"This rv release supports versions: {supported}. "
            "Upgrade rv or downgrade your manifest to a supported version."
        )


class AssetType(StrEnum):
    """Supported asset orchestration types."""

    SYMLINK = "symlink"
    COPY = "copy"
    TEMPLATE = "template"
    SECRET = "secret"


class AssetHookCommand(BaseModel):
    """Inline shell-command hook definition for a per-asset hook step."""

    command: str = Field(..., description="Shell command to execute (passed as list via shlex.split)")


class AssetHookPlugin(BaseModel):
    """Plugin-reference hook definition for a per-asset hook step."""

    plugin: str = Field(..., description="Plugin name to invoke from the repository plugins/ directory")


class AssetHooks(BaseModel):
    """Pre- and post-mutation hook definitions for a single asset."""

    pre: list[AssetHookCommand | AssetHookPlugin] = Field(
        default_factory=list,
        description="Hooks to execute before the asset mutation is applied.",
    )
    post: list[AssetHookCommand | AssetHookPlugin] = Field(
        default_factory=list,
        description="Hooks to execute after the asset mutation is applied successfully.",
    )


class ConflictStrategy(StrEnum):
    """Strategies to resolve file conflicts during restore."""

    PROMPT = "prompt"
    OVERWRITE = "overwrite"
    SKIP = "skip"
    ABORT = "abort"


class Asset(BaseModel):
    """Asset definition representing a symlink, copied file, or template."""

    id: str = Field(..., description="Unique identifier for the asset")
    type: AssetType = Field(AssetType.SYMLINK, description="Orchestration type")
    source: str = Field(..., description="Source path relative to the manifest.yaml")
    target: str | list[str] = Field(..., description="Target system path. Supports ${VAR} env interpolation.")
    permissions: str | None = Field(None, description="Octal permission string (e.g., '0644')")
    owner: str | None = Field(None, description="Owner username (null defaults to current user)")
    conflict_strategy: ConflictStrategy = Field(ConflictStrategy.PROMPT, description="Conflict resolution strategy")
    encrypted: bool = Field(False, description="Whether the asset source is encrypted (always true for secret type)")
    template_vars: dict[str, Any] | None = Field(None, description="Key-value mapping for template interpolation")
    hooks: AssetHooks = Field(default_factory=AssetHooks, description="Per-asset pre/post mutation hooks")

    @model_validator(mode="after")
    def validate_encrypted_secret(self) -> "Asset":
        """Ensure secret type has encrypted=True and secret type properties."""
        if self.type == AssetType.SECRET:
            self.encrypted = True
        return self

    @field_validator("source")
    @classmethod
    def validate_source_path(cls, v: str) -> str:
        """Prevent path traversal in source path."""
        import os

        normalized = os.path.normpath(v)
        if normalized.startswith("..") or os.path.isabs(normalized):
            raise ValueError(
                f"Source path '{v}' must be relative to the repository and not contain path traversal ('..')"
            )
        return v

    @field_validator("permissions")
    @classmethod
    def validate_permissions(cls, v: str | None) -> str | None:
        """Validate octal permission string."""
        if v is None:
            return v
        try:
            int(v, 8)
        except ValueError:
            raise ValueError(f"Permissions must be a valid octal string, got '{v}'")
        if len(v) != 4 or not v.startswith("0"):
            raise ValueError(f"Permissions must be a 4-digit octal string starting with 0, got '{v}'")
        return v


class Secret(BaseModel):
    """Secret definition decrypted via age before placement."""

    id: str = Field(..., description="Unique identifier for the secret")
    type: AssetType = Field(AssetType.SECRET, description="Orchestration type (fixed to secret)")
    source: str = Field(..., description="Encrypted source file path (typically ends in .age)")
    target: str | list[str] = Field(..., description="Target system path. Supports ${VAR} env interpolation.")
    permissions: str = Field("0600", description="Strict permissions enforced on secrets")
    owner: str | None = Field(None, description="Owner username (null defaults to current user)")
    encrypted: bool = Field(True, description="Always true for secrets")

    @model_validator(mode="after")
    def validate_secret_type(self) -> "Secret":
        """Enforce secret specific fields."""
        self.type = AssetType.SECRET
        self.encrypted = True
        # Enforce strict secret permissions if none provided
        if not self.permissions:
            self.permissions = "0600"
        return self

    @field_validator("source")
    @classmethod
    def validate_source_path(cls, v: str) -> str:
        """Prevent path traversal in source path."""
        import os

        normalized = os.path.normpath(v)
        if normalized.startswith("..") or os.path.isabs(normalized):
            raise ValueError(
                f"Secret source path '{v}' must be relative to the repository and not contain path traversal"
            )
        return v

    @field_validator("permissions")
    @classmethod
    def validate_permissions(cls, v: str) -> str:
        """Validate strict octal permissions for secrets (typically 0600 or 0700)."""
        try:
            val = int(v, 8)
        except ValueError:
            raise ValueError(f"Permissions must be a valid octal string, got '{v}'")
        if len(v) != 4 or not v.startswith("0"):
            raise ValueError(f"Permissions must be a 4-digit octal string starting with 0, got '{v}'")
        # Ensure secret permissions are secure (no world-readable/writable permissions)
        # 0700 or 0600 are standard. Let's make sure it is at least group/world restrictive.
        if (val & 0o077) != 0:
            raise ValueError(f"Secrets must have secure permissions restricting group and world access, got '{v}'")
        return v


class DockerConfig(BaseModel):
    """Docker environment provisioning."""

    images: list[str] = Field(default_factory=list)


class NodeConfig(BaseModel):
    """Node/Nvm environment provisioning."""

    version_file: str | None = Field(default=None, description="Path to .nvmrc or similar version file")
    version: str | None = Field(default=None, description="Explicit target Node.js version")


class Packages(BaseModel):
    """System and language level packages."""

    brew: list[str] = Field(default_factory=list)
    apt: list[str] = Field(default_factory=list)
    flatpak: list[str] = Field(default_factory=list)
    snap: list[str] = Field(default_factory=list)
    pacman: list[str] = Field(default_factory=list, description="Arch Linux packages via pacman")
    dnf: list[str] = Field(default_factory=list, description="Fedora/RHEL packages via dnf")
    nix: list[str] = Field(default_factory=list, description="Nix packages via nix-env (nixpkgs.<pkg>)")
    cargo: list[str] = Field(default_factory=list, description="Rust tools via cargo install")
    pip: list[str] = Field(default_factory=list, description="Python tools via pip install --user")
    docker: DockerConfig = Field(default_factory=lambda: DockerConfig())
    node: NodeConfig = Field(default_factory=lambda: NodeConfig())


class Profile(BaseModel):
    """Profile configuration linking assets, secrets, and packages."""

    extends: list[str] = Field(default_factory=list, description="Base profiles extended by this profile")
    assets: list[str | Asset] = Field(default_factory=list, description="Assets to restore (by ID or inline)")
    secrets: list[str | Secret] = Field(default_factory=list, description="Secrets to restore (by ID or inline)")
    packages: list[str] = Field(default_factory=list, description="Top-level package groups referenced by this profile")


class BackupRetentionConfig(BaseModel):
    """Controls automatic cleanup of old transaction backup snapshots."""

    max_count: int = Field(default=10, ge=1, description="Keep at most N backup snapshots (FIFO eviction)")
    max_age_days: int = Field(default=30, ge=1, description="Delete backup snapshots older than N days")


class MachineOverridesConfig(BaseModel):
    """Machine override configuration."""

    enabled: bool = Field(default=True, description="Enable machine overrides")
    path: str = Field(
        default="machine/{hostname}.yaml", description="Path pattern for host-specific override manifests"
    )


class Manifest(BaseModel):
    """Root configuration manifest representing the complete repository state."""

    version: int = Field(2, description="Manifest schema version")
    assets: list[Asset] = Field(default_factory=list, description="Global pool of assets")
    secrets: list[Secret] = Field(default_factory=list, description="Global pool of secrets")
    packages: Packages = Field(default_factory=lambda: Packages(), description="Global package definitions")
    profiles: dict[str, Profile] = Field(default_factory=dict, description="Named deployment profiles")
    machine_overrides: MachineOverridesConfig = Field(default_factory=lambda: MachineOverridesConfig())
    backup_retention: BackupRetentionConfig = Field(
        default_factory=lambda: BackupRetentionConfig(),
        description="Automatic backup snapshot pruning configuration.",
    )

    @field_validator("version")
    @classmethod
    def validate_schema_version(cls, v: int) -> int:
        """Reject manifests that declare an unsupported schema version.

        This guard runs *inside* Pydantic model_validate(), which is called
        only after ManifestLoader.load() performs the raw-version pre-check.
        The double-guard ensures correctness even when Manifest is constructed
        directly in tests or other code paths.
        """
        if v not in _SUPPORTED_SCHEMA_VERSIONS:
            raise UnsupportedSchemaVersionError(v)
        return v
