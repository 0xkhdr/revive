"""Pydantic schemas for Transaction Logs, Rollback Journals, and Lockfile.

These schemas track system state mutations for auditability and rollback safety.
"""

from pydantic import BaseModel, Field


class RollbackEntry(BaseModel):
    """A single filesystem operation recorded for potential rollback."""

    op: str = Field(..., description="Operation type: create, modify, delete, symlink, chmod")
    src_backup: str | None = Field(None, description="Path to the backup file of the pre-existing state")
    target: str = Field(..., description="Target system path that was mutated")
    checksum: str | None = Field(None, description="Pre-mutation SHA-256 checksum of the target file")
    permissions: str | None = Field(None, description="Pre-mutation permissions of the target file")


class TransactionJournal(BaseModel):
    """A log of all operations performed in a single transaction."""

    tx_id: str = Field(..., description="Unique transaction ID")
    timestamp: float = Field(..., description="Timestamp of transaction initiation")
    status: str = Field("pending", description="Status: pending, committed, aborted, rolled_back")
    entries: list[RollbackEntry] = Field(default_factory=list, description="Ordered rollback operations")


class LockfileEntry(BaseModel):
    """Verification entry for a successfully managed asset or secret."""

    sha256_of_source: str = Field(..., description="SHA-256 checksum of the source asset in the repository")
    target_path: str | list[str] = Field(..., description="Resolved system target path(s)")
    permissions: str | list[str] = Field(..., description="Enforced octal file permission string(s)")
    mtime: float | list[float] = Field(..., description="Modified time(s) of target(s) at successful sync")


class Lockfile(BaseModel):
    """Manifest lockfile structure maintaining deterministic state synchronization records."""

    entries: dict[str, LockfileEntry] = Field(default_factory=dict, description="Sync state keyed by asset/secret ID")
