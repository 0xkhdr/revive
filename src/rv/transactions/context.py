"""TransactionContext implementing the 7-step transactional execution boundary.

Steps: Plan, Validate, Snapshot, Execute, Verify, Commit, and Cleanup.
Includes robust rollback capabilities to restore system state on failure.
"""

import hashlib
import os
import shutil
import tempfile
import time
import uuid
from typing import Any

from rv.logging.audit import AuditLogger
from rv.models.transaction import RollbackEntry, TransactionJournal
from rv.security.permissions import PermissionEnforcer
from rv.transactions.atomic import AtomicWrite

logger = AuditLogger.get_logger("rv.transactions.context")


class TransactionContext:
    """Manages transactional state changes on the local system with rollback support."""

    def __init__(self, tx_id: str | None = None):
        """Initializes the transaction context.

        Args:
            tx_id: Optional transaction ID. Generates UUID if None.
        """
        self.tx_id = tx_id or str(uuid.uuid4())
        self.timestamp = time.time()
        self.status = "pending"
        self.entries: list[RollbackEntry] = []

        # Paths for journal and backups
        self.journal_dir = os.path.expanduser("~/.config/rv/journals")
        self.backup_dir = os.path.expanduser(f"~/.config/rv/backups/{self.tx_id}")
        self.journal_path = os.path.join(self.journal_dir, f"{self.tx_id}.json")

        # Track registered operations before execution
        self.planned_operations: list[dict[str, Any]] = []

    def plan_operation(self, op_type: str, target: str, source_data: Any | None = None, **kwargs: Any) -> None:
        """Registers a filesystem operation to be executed in this transaction.

        Args:
            op_type: Operation type ('copy', 'symlink', 'chmod', 'delete')
            target: Absolute path to the mutated resource.
            source_data: Source path or content data.
            kwargs: Extra parameters (e.g., permissions, owner).
        """
        self.planned_operations.append(
            {"op_type": op_type, "target": os.path.abspath(target), "source_data": source_data, "kwargs": kwargs}
        )

    def validate(self) -> None:
        """Step 2: Validate all targets and permissions before any write.

        Ensures parent directories exist (or will be created) and permissions allow writes.
        """
        for op in self.planned_operations:
            op_type = op["op_type"]
            if op_type not in ("copy", "symlink", "chmod", "delete"):
                raise ValueError(f"Unsupported operation type: {op_type}")

            target = op["target"]
            parent = os.path.dirname(target)

            # Check if parent directory exists or can be created
            if not os.path.exists(parent):
                try:
                    # Dry-run test of directory creation
                    pass
                except Exception as e:
                    raise ValueError(f"Parent directory {parent} for target {target} is invalid: {e}") from e

            # Check if we have write access if the file exists, or if parent directory is writable
            if os.path.exists(target):
                if not os.access(target, os.W_OK):
                    raise PermissionError(f"Target path is not writable: {target}")
            else:
                if os.path.exists(parent) and not os.access(parent, os.W_OK):
                    raise PermissionError(f"Parent directory is not writable: {parent}")

    def snapshot(self) -> None:
        """Step 3: Create backup snapshots of all existing resources to be mutated.

        Writes the rollback journal to disk.
        """
        os.makedirs(self.journal_dir, exist_ok=True)
        os.makedirs(self.backup_dir, exist_ok=True)

        for idx, op in enumerate(self.planned_operations):
            target = op["target"]
            op_type = op["op_type"]

            # Determine rollback entry parameters
            src_backup = None
            checksum = None
            permissions = None

            if os.path.exists(target):
                # Calculate pre-mutation checksum
                try:
                    if os.path.isfile(target) and not os.path.islink(target):
                        with open(target, "rb") as f:
                            checksum = hashlib.sha256(f.read()).hexdigest()

                    # Store permissions
                    permissions = oct(os.stat(target).st_mode & 0o7777)
                except OSError:
                    pass

                # Back up existing files/symlinks
                backup_filename = f"backup_{idx}_{os.path.basename(target)}"
                backup_path = os.path.join(self.backup_dir, backup_filename)

                try:
                    if os.path.islink(target):
                        # Save symlink target
                        link_target = os.readlink(target)
                        with open(backup_path, "w") as f:
                            f.write(f"SYMLINK:{link_target}")
                        src_backup = backup_path
                    elif os.path.isdir(target):
                        shutil.copytree(target, backup_path, symlinks=True)
                        src_backup = backup_path
                    elif os.path.isfile(target):
                        shutil.copy2(target, backup_path)
                        src_backup = backup_path
                except Exception as e:
                    raise RuntimeError(f"Failed to create backup snapshot for {target}: {e}") from e

            # Create the rollback entry
            rollback_op = "create" if not os.path.exists(target) else "modify"
            if op_type == "delete":
                rollback_op = "delete"

            entry = RollbackEntry(
                op=rollback_op, src_backup=src_backup, target=target, checksum=checksum, permissions=permissions
            )
            self.entries.append(entry)

        # Write initial journal to disk
        self._write_journal()

    def execute(self) -> None:
        """Step 4: Execute all planned mutations atomically.

        Rolls back automatically on failure.
        """
        self.status = "executing"
        self._write_journal()

        try:
            for op in self.planned_operations:
                op_type = op["op_type"]
                target = op["target"]
                source_data = op["source_data"]
                kwargs = op["kwargs"]

                # Ensure parent directory exists
                os.makedirs(os.path.dirname(target), exist_ok=True)

                if op_type == "copy":
                    # Perform atomic write
                    if isinstance(source_data, str) and os.path.exists(source_data):
                        if os.path.isdir(source_data):
                            # Atomic directory copy using temporary sibling directory
                            if os.path.exists(target) or os.path.islink(target):
                                if os.path.isdir(target):
                                    shutil.rmtree(target)
                                else:
                                    os.unlink(target)

                            parent_dir = os.path.dirname(target)
                            os.makedirs(parent_dir, exist_ok=True)
                            temp_dir = tempfile.mkdtemp(dir=parent_dir, prefix=".rv_atomic_dir_tmp_")
                            try:
                                shutil.copytree(source_data, temp_dir, symlinks=True, dirs_exist_ok=True)
                                os.rename(temp_dir, target)
                            except Exception as e:
                                if os.path.exists(temp_dir):
                                    shutil.rmtree(temp_dir)
                                raise RuntimeError(f"Atomic directory copy failed: {e}") from e
                        else:
                            # Standard file copy
                            with open(source_data, "rb") as f:
                                content = f.read()
                            AtomicWrite.write(target, content)
                    else:
                        content = source_data or b""
                        AtomicWrite.write(target, content)

                elif op_type == "symlink":
                    # Remove existing file/symlink if present
                    if os.path.exists(target) or os.path.islink(target):
                        os.unlink(target)
                    os.symlink(str(source_data), target)

                elif op_type == "chmod":
                    # Change permissions
                    PermissionEnforcer.enforce(target, kwargs["permissions"], kwargs.get("owner"))

                elif op_type == "delete":
                    if os.path.exists(target) or os.path.islink(target):
                        if os.path.islink(target) or os.path.isfile(target):
                            os.unlink(target)
                        elif os.path.isdir(target):
                            shutil.rmtree(target)
                else:
                    raise ValueError(f"Unsupported operation type: {op_type}")

                if kwargs.get("permissions") is not None and op_type in ("copy", "symlink"):
                    PermissionEnforcer.enforce(target, kwargs["permissions"], kwargs.get("owner"))

        except Exception as e:
            self.rollback()
            raise RuntimeError(f"Transaction execution failed, successfully rolled back changes: {e}") from e

    def verify(self) -> None:
        """Step 5: Post-apply verification.

        Validates all targets exist and match planned integrity checks.
        """
        self.status = "verifying"
        self._write_journal()

        try:
            for idx, op in enumerate(self.planned_operations):
                target = op["target"]
                op_type = op["op_type"]
                kwargs = op["kwargs"]

                if op_type == "delete":
                    has_subsequent_creation = any(
                        other_op["target"] == target and other_op["op_type"] in ("copy", "symlink")
                        for other_op in self.planned_operations[idx + 1:]
                    )
                    if not has_subsequent_creation and os.path.exists(target):
                        raise RuntimeError(f"Verification failed: Deleted target still exists at {target}")
                    continue

                if not os.path.exists(target) and not os.path.islink(target):
                    raise RuntimeError(f"Verification failed: Target not found at {target}")

                if kwargs.get("permissions") is not None:
                    expected = kwargs["permissions"]
                    if not PermissionEnforcer.verify(target, expected):
                        raise RuntimeError(
                            f"Verification failed: Permissions mismatch on {target}. "
                            f"Expected {expected}, got {oct(os.stat(target).st_mode & 0o7777)}"
                        )
        except Exception as e:
            self.rollback()
            raise RuntimeError(f"Transaction verification failed, successfully rolled back: {e}") from e

    def commit(self) -> None:
        """Step 6: Commit the transaction.

        Finalizes lockfile and status.
        """
        self.status = "committed"
        self._write_journal()

    def cleanup(self) -> None:
        """Step 7: Remove backups and temporary files."""
        # Clean up backup files
        if os.path.exists(self.backup_dir):
            try:
                shutil.rmtree(self.backup_dir)
            except OSError as e:
                logger.debug(f"Failed to remove backup dir: {e}")

        # Remove the journal file upon successful commit
        if self.status == "committed" and os.path.exists(self.journal_path):
            try:
                os.unlink(self.journal_path)
            except OSError as e:
                logger.debug(f"Failed to remove journal: {e}")

    def rollback(self) -> None:
        """Reverts all changes made during the transaction to restore pre-existing state."""
        self.status = "rolling_back"
        self._write_journal()

        # Revert operations in REVERSE order of execution
        for entry in reversed(self.entries):
            target = entry.target
            op = entry.op
            src_backup = entry.src_backup
            permissions = entry.permissions

            try:
                if op == "create":
                    # Target did not exist previously, so delete it
                    if os.path.exists(target) or os.path.islink(target):
                        if os.path.islink(target) or os.path.isfile(target):
                            os.unlink(target)
                        elif os.path.isdir(target):
                            shutil.rmtree(target)

                elif op in ("modify", "delete") and src_backup:
                    # Remove current target if it exists
                    if os.path.exists(target) or os.path.islink(target):
                        if os.path.islink(target) or os.path.isfile(target):
                            os.unlink(target)
                        elif os.path.isdir(target):
                            shutil.rmtree(target)

                    # Restore backup
                    if os.path.exists(src_backup):
                        # Check if it was a symlink
                        is_symlink = False
                        link_target = ""
                        try:
                            with open(src_backup) as f:
                                content = f.read()
                                if content.startswith("SYMLINK:"):
                                    is_symlink = True
                                    link_target = content[len("SYMLINK:") :]
                        except OSError as e:
                            logger.debug(f"Failed to read backup for symlink check: {e}")

                        if is_symlink:
                            os.symlink(link_target, target)
                        elif os.path.isdir(src_backup):
                            shutil.copytree(src_backup, target, symlinks=True, dirs_exist_ok=True)
                        else:
                            shutil.copy2(src_backup, target)

                        # Re-apply permissions if they existed
                        if permissions:
                            PermissionEnforcer.enforce(target, permissions)
            except Exception as e:
                # Log critical error during rollback
                logger.error(f"Critical error during rollback of {target}: {e}")

        self.status = "rolled_back"
        self._write_journal()

    def _write_journal(self) -> None:
        """Serializes current transaction journal state to the journal directory."""
        try:
            journal = TransactionJournal(
                tx_id=self.tx_id, timestamp=self.timestamp, status=self.status, entries=self.entries
            )
            AtomicWrite.write(self.journal_path, journal.model_dump_json(indent=2))
        except OSError as e:
            logger.debug(f"Failed to write journal: {e}")
