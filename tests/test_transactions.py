"""Test suite for atomic writes, flock-based process locking, and TransactionContext with rollback."""

import os
import tempfile
from unittest.mock import patch

import pytest

from rv.transactions.atomic import AtomicWrite
from rv.transactions.context import TransactionContext
from rv.transactions.lock import LockAcquisitionError, ProcessLock


def test_process_lock() -> None:
    with tempfile.NamedTemporaryFile() as tmp:
        # Acquire lock first time
        with ProcessLock(lock_path=tmp.name, blocking=False) as lock1:
            assert lock1.lock_path == os.path.abspath(tmp.name)

            # Try to acquire lock second time in nested context, should raise LockAcquisitionError
            with pytest.raises(LockAcquisitionError) as excinfo:
                with ProcessLock(lock_path=tmp.name, blocking=False):
                    pass
            assert "Another revive process currently holds the lock" in str(excinfo.value)


def test_atomic_write() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        target = os.path.join(tmpdir, "subdir", "target.txt")
        content = "atomic data content"

        # Write
        AtomicWrite.write(target, content)

        assert os.path.exists(target)
        with open(target) as f:
            assert f.read() == content


def test_transaction_context_success() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        target_file = os.path.join(tmpdir, "test_file.txt")
        target_link = os.path.join(tmpdir, "test_link")

        # Initialize transaction
        tx = TransactionContext()

        # 1. Plan
        tx.plan_operation("copy", target_file, source_data=b"hello world", permissions="0644")
        tx.plan_operation("symlink", target_link, source_data=target_file)

        # 2. Validate
        tx.validate()

        # 3. Snapshot
        tx.snapshot()
        assert len(tx.entries) == 2
        assert tx.status == "pending"

        # 4. Execute
        tx.execute()
        assert tx.status == "executing"
        assert os.path.exists(target_file)
        assert os.path.islink(target_link)
        assert os.readlink(target_link) == target_file

        # 5. Verify
        tx.verify()
        assert tx.status == "verifying"

        # 6. Commit
        tx.commit()
        assert tx.status == "committed"

        # 7. Cleanup
        tx.cleanup()
        # Backup dir should be deleted
        assert os.path.exists(tx.backup_dir) is False


def test_transaction_context_rollback() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        # Pre-existing file
        pre_existing = os.path.join(tmpdir, "pre_existing.txt")
        with open(pre_existing, "w") as f:
            f.write("original content")
        os.chmod(pre_existing, 0o644)

        new_file = os.path.join(tmpdir, "new_file.txt")

        tx = TransactionContext()

        # Plan a successful overwrite and a failure operation
        tx.plan_operation("copy", pre_existing, source_data=b"overwritten content", permissions="0600")
        tx.plan_operation("copy", new_file, source_data=b"new file content", permissions="0644")
        # Plan a third operation that will intentionally fail (e.g. invalid operation type or missing parameters)
        tx.planned_operations.append(
            {"op_type": "invalid_op", "target": os.path.join(tmpdir, "fail.txt"), "source_data": None, "kwargs": {}}
        )

        tx.snapshot()

        # Executing should fail and trigger automatic rollback
        with pytest.raises(Exception):
            tx.execute()

        # State should be restored
        assert tx.status == "rolled_back"

        # 1. pre_existing.txt should have original content and original permissions restored
        assert os.path.exists(pre_existing)
        with open(pre_existing) as f:
            assert f.read() == "original content"
        mode = os.stat(pre_existing).st_mode & 0o7777
        assert mode == 0o644

        # 2. new_file.txt (which didn't exist before) should be deleted
        assert os.path.exists(new_file) is False


def test_snapshot_symlink_backup() -> None:
    """snapshot() saves SYMLINK:<target> text when the existing target is a symlink."""
    with tempfile.TemporaryDirectory() as tmpdir:
        real_file = os.path.join(tmpdir, "real.txt")
        with open(real_file, "w") as f:
            f.write("real content")

        link_path = os.path.join(tmpdir, "my_link")
        os.symlink(real_file, link_path)

        tx = TransactionContext()
        tx.plan_operation("copy", link_path, source_data=b"new content")

        tx.snapshot()

        assert len(tx.entries) == 1
        entry = tx.entries[0]
        assert entry.src_backup is not None
        with open(entry.src_backup) as f:
            content = f.read()
        assert content.startswith("SYMLINK:")
        assert real_file in content


def test_snapshot_directory_backup() -> None:
    """snapshot() copies a directory tree when the existing target is a directory."""
    with tempfile.TemporaryDirectory() as tmpdir:
        existing_dir = os.path.join(tmpdir, "existing_dir")
        os.makedirs(existing_dir)
        with open(os.path.join(existing_dir, "file.txt"), "w") as f:
            f.write("dir content")

        tx = TransactionContext()
        tx.plan_operation("copy", existing_dir, source_data=b"not used")

        tx.snapshot()

        assert len(tx.entries) == 1
        entry = tx.entries[0]
        assert entry.src_backup is not None
        assert os.path.isdir(entry.src_backup)
        assert os.path.exists(os.path.join(entry.src_backup, "file.txt"))


def test_rollback_restores_symlink() -> None:
    """rollback() recreates a symlink from SYMLINK: backup content."""
    with tempfile.TemporaryDirectory() as tmpdir:
        real_file = os.path.join(tmpdir, "real.txt")
        with open(real_file, "w") as f:
            f.write("real content")

        link_path = os.path.join(tmpdir, "my_link")
        os.symlink(real_file, link_path)

        tx = TransactionContext()
        tx.plan_operation("copy", link_path, source_data=b"new content")

        # Snapshot saves SYMLINK: backup
        tx.snapshot()

        # Execute overwrites the symlink with a regular file
        tx.execute()
        assert not os.path.islink(link_path)

        # Now manually rollback
        tx.status = "executing"  # reset so rollback is allowed
        tx.rollback()

        # The symlink should be restored
        assert os.path.islink(link_path)
        assert os.readlink(link_path) == real_file


def test_rollback_restores_directory() -> None:
    """rollback() restores a backed-up directory tree."""
    with tempfile.TemporaryDirectory() as tmpdir:
        target_dir = os.path.join(tmpdir, "target_dir")
        os.makedirs(target_dir)
        with open(os.path.join(target_dir, "original.txt"), "w") as f:
            f.write("original dir content")

        new_source_dir = os.path.join(tmpdir, "new_source_dir")
        os.makedirs(new_source_dir)
        with open(os.path.join(new_source_dir, "new.txt"), "w") as f:
            f.write("new content")

        tx = TransactionContext()
        tx.plan_operation("copy", target_dir, source_data=new_source_dir)

        tx.snapshot()
        tx.execute()

        # After execute, the dir should have the new content
        assert os.path.exists(os.path.join(target_dir, "new.txt"))
        assert not os.path.exists(os.path.join(target_dir, "original.txt"))

        # Rollback should restore the original directory
        tx.status = "executing"
        tx.rollback()

        assert os.path.exists(os.path.join(target_dir, "original.txt"))


def test_execute_atomic_directory_copy() -> None:
    """execute() copies a directory atomically via temp sibling directory rename."""
    with tempfile.TemporaryDirectory() as tmpdir:
        source_dir = os.path.join(tmpdir, "source_dir")
        os.makedirs(source_dir)
        with open(os.path.join(source_dir, "data.txt"), "w") as f:
            f.write("directory data")

        target_dir = os.path.join(tmpdir, "target_dir")

        tx = TransactionContext()
        tx.plan_operation("copy", target_dir, source_data=source_dir)

        tx.snapshot()
        tx.execute()

        assert os.path.isdir(target_dir)
        assert os.path.exists(os.path.join(target_dir, "data.txt"))


def test_write_journal_oserror_silenced() -> None:
    """_write_journal() silently logs debug message when journal write fails."""
    with tempfile.TemporaryDirectory() as tmpdir:
        tx = TransactionContext()
        tx.journal_path = os.path.join(tmpdir, "journal.json")

        # Patch AtomicWrite.write to raise OSError
        with patch("rv.transactions.context.AtomicWrite.write", side_effect=OSError("disk full")):
            # Should not raise — silences the error and logs debug
            tx._write_journal()


def test_cleanup_journal_not_committed() -> None:
    """cleanup() does not remove the journal if status is not 'committed'."""
    with tempfile.TemporaryDirectory() as tmpdir:
        tx = TransactionContext()
        tx.status = "rolling_back"
        tx.journal_path = os.path.join(tmpdir, "journal.json")

        # Create a fake journal file
        with open(tx.journal_path, "w") as f:
            f.write("{}")

        tx.backup_dir = os.path.join(tmpdir, "backups")

        tx.cleanup()

        # Journal should still exist since status is not committed
        assert os.path.exists(tx.journal_path)


def test_validate_failures() -> None:
    """validate() raises expected exceptions on invalid operation type or permission issues."""
    tx = TransactionContext()
    # 1. Unsupported operation type
    tx.planned_operations.append({"op_type": "unsupported", "target": "/some/path"})
    with pytest.raises(ValueError, match="Unsupported operation type"):
        tx.validate()

    # Reset
    tx.planned_operations = []

    # 2. Target not writable
    tx.plan_operation("copy", "/some/path", source_data=b"data")
    with patch("os.path.exists", return_value=True), patch("os.access", return_value=False):
        with pytest.raises(PermissionError, match="Target path is not writable"):
            tx.validate()

    # 3. Parent directory not writable
    with patch("os.path.exists", side_effect=lambda p: p != "/some/path"), patch("os.access", return_value=False):
        with pytest.raises(PermissionError, match="Parent directory is not writable"):
            tx.validate()


def test_snapshot_oserror_handled() -> None:
    """snapshot() handles OSError gracefully when gathering file metadata."""
    with tempfile.TemporaryDirectory() as tmpdir:
        target = os.path.join(tmpdir, "file.txt")
        with open(target, "w") as f:
            f.write("hello")

        tx = TransactionContext()
        tx.plan_operation("copy", target, source_data=b"data")
        tx.journal_dir = os.path.join(tmpdir, "journals")
        tx.backup_dir = os.path.join(tmpdir, "backups")

        # Raise OSError on stat
        with patch("os.stat", side_effect=OSError("Permission denied")):
            tx.snapshot()  # should not raise
            assert len(tx.entries) == 1
            assert tx.entries[0].permissions is None


def test_execute_failures() -> None:
    """execute() raises RuntimeError and handles exceptions during directory copy."""
    with tempfile.TemporaryDirectory() as tmpdir:
        source_dir = os.path.join(tmpdir, "source_dir")
        os.makedirs(source_dir)
        target_dir = os.path.join(tmpdir, "target_dir")

        tx = TransactionContext()
        tx.plan_operation("copy", target_dir, source_data=source_dir)

        # Force shutil.copytree to raise an exception
        with patch("shutil.copytree", side_effect=Exception("copy tree error")):
            with pytest.raises(RuntimeError, match="Atomic directory copy failed"):
                tx.execute()


def test_execute_symlink_replaces_existing() -> None:
    """execute() unlinks existing file/symlink before creating a new symlink."""
    with tempfile.TemporaryDirectory() as tmpdir:
        target = os.path.join(tmpdir, "link")
        with open(target, "w") as f:
            f.write("existing")

        tx = TransactionContext()
        tx.plan_operation("symlink", target, source_data="destination")
        tx.execute()

        assert os.path.islink(target)
        assert os.readlink(target) == "destination"


def test_verification_failures() -> None:
    """verify() rolls back and raises RuntimeError on verification mismatch."""
    with tempfile.TemporaryDirectory() as tmpdir:
        target = os.path.join(tmpdir, "file.txt")

        tx = TransactionContext()
        tx.plan_operation("copy", target, source_data=b"data", permissions="0600")
        tx.journal_dir = os.path.join(tmpdir, "journals")
        tx.backup_dir = os.path.join(tmpdir, "backups")

        tx.snapshot()
        # Do not run execute, so target doesn't exist
        with pytest.raises(RuntimeError, match="Verification failed: Target not found"):
            tx.verify()

        assert tx.status == "rolled_back"


def test_cleanup_oserrors_handled() -> None:
    """cleanup() silences OSErrors during backup and journal deletion."""
    with tempfile.TemporaryDirectory() as tmpdir:
        tx = TransactionContext()
        tx.status = "committed"
        tx.journal_path = os.path.join(tmpdir, "journal.json")
        tx.backup_dir = os.path.join(tmpdir, "backups")
        os.makedirs(tx.backup_dir)
        with open(tx.journal_path, "w") as f:
            f.write("{}")

        with (
            patch("shutil.rmtree", side_effect=OSError("rmtree error")),
            patch("os.unlink", side_effect=OSError("unlink error")),
        ):
            tx.cleanup()  # should not raise


def test_delete_verification() -> None:
    """verify() checks that deleted target is gone or raises RuntimeError if it still exists."""
    with tempfile.TemporaryDirectory() as tmpdir:
        target = os.path.join(tmpdir, "delete_me.txt")
        with open(target, "w") as f:
            f.write("content")

        tx = TransactionContext()
        tx.plan_operation("delete", target)
        tx.journal_dir = os.path.join(tmpdir, "journals")
        tx.backup_dir = os.path.join(tmpdir, "backups")

        tx.snapshot()
        # Verify without running execute (so file still exists)
        with pytest.raises(RuntimeError, match="Verification failed: Deleted target still exists"):
            tx.verify()

        assert tx.status == "rolled_back"


def test_delete_directory() -> None:
    """execute() successfully deletes an existing directory target."""
    with tempfile.TemporaryDirectory() as tmpdir:
        target_dir = os.path.join(tmpdir, "dir_to_delete")
        os.makedirs(target_dir)
        with open(os.path.join(target_dir, "file.txt"), "w") as f:
            f.write("data")

        tx = TransactionContext()
        tx.plan_operation("delete", target_dir)
        tx.execute()

        assert not os.path.exists(target_dir)


def test_chmod_operation() -> None:
    """execute() runs chmod successfully via PermissionEnforcer."""
    with tempfile.TemporaryDirectory() as tmpdir:
        target = os.path.join(tmpdir, "file.txt")
        with open(target, "w") as f:
            f.write("content")

        tx = TransactionContext()
        tx.plan_operation("chmod", target, permissions="0755")
        tx.execute()

        # Check permissions
        mode = oct(os.stat(target).st_mode & 0o777)
        assert mode == "0o755" or mode == "0755"
