"""Test suite for atomic writes, flock-based process locking, and TransactionContext with rollback.
"""

import os
import tempfile
import pytest
from rv.transactions.lock import ProcessLock, LockAcquisitionError
from rv.transactions.atomic import AtomicWrite
from rv.transactions.context import TransactionContext


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
        with open(target, "r") as f:
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
        tx.planned_operations.append({
            "op_type": "invalid_op",
            "target": os.path.join(tmpdir, "fail.txt"),
            "source_data": None,
            "kwargs": {}
        })

        tx.snapshot()
        
        # Executing should fail and trigger automatic rollback
        with pytest.raises(Exception):
            tx.execute()

        # State should be restored
        assert tx.status == "rolled_back"
        
        # 1. pre_existing.txt should have original content and original permissions restored
        assert os.path.exists(pre_existing)
        with open(pre_existing, "r") as f:
            assert f.read() == "original content"
        mode = os.stat(pre_existing).st_mode & 0o7777
        assert mode == 0o644

        # 2. new_file.txt (which didn't exist before) should be deleted
        assert os.path.exists(new_file) is False
