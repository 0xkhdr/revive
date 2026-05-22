import os
import shutil
import tempfile
import pytest
from unittest.mock import MagicMock

from rv.models.manifest import Asset, AssetType, ConflictStrategy
from rv.models.transaction import Lockfile, LockfileEntry
from rv.services.handlers import AssetHandler
from rv.services.status import StatusService
from rv.services.restore import RestoreService
from rv.transactions.context import TransactionContext


@pytest.fixture
def temp_repo():
    """Sets up a temporary repository directory."""
    with tempfile.TemporaryDirectory() as tmpdir:
        # Create some files inside the repo
        assets_dir = os.path.join(tmpdir, "assets")
        os.makedirs(assets_dir, exist_ok=True)

        # 1. Simple source file
        with open(os.path.join(assets_dir, "config.txt"), "w") as f:
            f.write("standard config content")

        # 2. Directory source (representing a project)
        project_dir = os.path.join(assets_dir, "my-project")
        os.makedirs(project_dir, exist_ok=True)

        # Subfolder inside directory source
        compose_dir = os.path.join(project_dir, "compose")
        os.makedirs(compose_dir, exist_ok=True)
        with open(os.path.join(compose_dir, "docker-compose.yml"), "w") as f:
            f.write("version: '3'")

        # File inside directory source
        with open(os.path.join(project_dir, "README.md"), "w") as f:
            f.write("# My Project")

        yield tmpdir


def test_asset_handler_copy_multiple_targets(temp_repo):
    """Verifies copying a single file to multiple target locations."""
    with tempfile.TemporaryDirectory() as sys_dir:
        target1 = os.path.join(sys_dir, "dest1.txt")
        target2 = os.path.join(sys_dir, "dest2.txt")

        asset = Asset(
            id="multi_dest",
            type=AssetType.COPY,
            source="assets/config.txt",
            target=[target1, target2],
            conflict_strategy=ConflictStrategy.OVERWRITE,
        )

        tx_context = TransactionContext()
        planned = AssetHandler.handle(asset, temp_repo, tx_context)
        assert planned is True

        # Verify both copy operations are planned
        assert len(tx_context.planned_operations) == 2
        assert tx_context.planned_operations[0]["op_type"] == "copy"
        assert tx_context.planned_operations[0]["target"] == os.path.abspath(target1)
        assert tx_context.planned_operations[1]["op_type"] == "copy"
        assert tx_context.planned_operations[1]["target"] == os.path.abspath(target2)

        # Execute the transaction
        tx_context.validate()
        tx_context.snapshot()
        tx_context.execute()
        tx_context.verify()
        tx_context.commit()
        tx_context.cleanup()

        # Verify files are successfully created and contain correct contents
        assert os.path.exists(target1)
        assert os.path.exists(target2)
        with open(target1) as f:
            assert f.read() == "standard config content"
        with open(target2) as f:
            assert f.read() == "standard config content"


def test_asset_handler_copy_directory_recursive(temp_repo):
    """Verifies copying an entire directory recursively."""
    with tempfile.TemporaryDirectory() as sys_dir:
        target_dir = os.path.join(sys_dir, "deployed-project")

        asset = Asset(
            id="dir_copy",
            type=AssetType.COPY,
            source="assets/my-project",
            target=target_dir,
            conflict_strategy=ConflictStrategy.OVERWRITE,
        )

        tx_context = TransactionContext()
        planned = AssetHandler.handle(asset, temp_repo, tx_context)
        assert planned is True

        # Execute
        tx_context.validate()
        tx_context.snapshot()
        tx_context.execute()
        tx_context.verify()
        tx_context.commit()
        tx_context.cleanup()

        # Verify recursive structure is successfully copied
        assert os.path.isdir(target_dir)
        assert os.path.isfile(os.path.join(target_dir, "README.md"))
        assert os.path.isdir(os.path.join(target_dir, "compose"))
        assert os.path.isfile(os.path.join(target_dir, "compose", "docker-compose.yml"))

        with open(os.path.join(target_dir, "compose", "docker-compose.yml")) as f:
            assert f.read() == "version: '3'"


def test_asset_handler_multi_target_subitem_resolution(temp_repo):
    """Verifies matching and copying individual sub-items of a directory to specific target paths."""
    with tempfile.TemporaryDirectory() as sys_dir:
        dest_compose = os.path.join(sys_dir, "compose")
        dest_readme = os.path.join(sys_dir, "README.md")

        asset = Asset(
            id="subitem_match",
            type=AssetType.COPY,
            source="assets/my-project",
            target=[dest_compose, dest_readme],
            conflict_strategy=ConflictStrategy.OVERWRITE,
        )

        tx_context = TransactionContext()
        planned = AssetHandler.handle(asset, temp_repo, tx_context)
        assert planned is True

        # Execute
        tx_context.validate()
        tx_context.snapshot()
        tx_context.execute()
        tx_context.verify()
        tx_context.commit()
        tx_context.cleanup()

        # Verify sub-items were mapped and copied correctly
        assert os.path.isdir(dest_compose)
        assert os.path.isfile(os.path.join(dest_compose, "docker-compose.yml"))
        assert os.path.isfile(dest_readme)

        with open(dest_readme) as f:
            assert f.read() == "# My Project"


def test_transaction_directory_rollback(temp_repo):
    """Verifies that copying a directory can be successfully rolled back if a subsequent step fails."""
    with tempfile.TemporaryDirectory() as sys_dir:
        target_dir = os.path.join(sys_dir, "deployed-project")

        asset = Asset(
            id="rollback_dir",
            type=AssetType.COPY,
            source="assets/my-project",
            target=target_dir,
            conflict_strategy=ConflictStrategy.OVERWRITE,
        )

        tx_context = TransactionContext()
        AssetHandler.handle(asset, temp_repo, tx_context)

        # Plan a second operation that will deliberately fail during execution
        tx_context.plan_operation("copy", "/invalid-root-dir/non-existent-file.txt", b"fails")

        # Execute should fail and trigger auto-rollback
        with pytest.raises(RuntimeError, match="Transaction execution failed"):
            tx_context.validate()
            tx_context.snapshot()
            tx_context.execute()

        # Verify target_dir was rolled back (does not exist)
        assert not os.path.exists(target_dir)


def test_status_drift_detection_multi_target(temp_repo):
    """Verifies drift detection for an asset with multiple targets."""
    with tempfile.TemporaryDirectory() as sys_dir:
        target1 = os.path.join(sys_dir, "dest1.txt")
        target2 = os.path.join(sys_dir, "dest2.txt")

        asset = Asset(
            id="drift_multi",
            type=AssetType.COPY,
            source="assets/config.txt",
            target=[target1, target2],
            conflict_strategy=ConflictStrategy.OVERWRITE,
            permissions="0644",
        )

        lockfile = Lockfile()

        # 1. Targets do not exist yet (status should be missing)
        status = StatusService._check_asset_drift(asset, temp_repo, lockfile)
        assert status["status"] == "missing"

        # Deploy targets
        tx_context = TransactionContext()
        AssetHandler.handle(asset, temp_repo, tx_context)
        tx_context.validate()
        tx_context.snapshot()
        tx_context.execute()
        tx_context.commit()
        tx_context.cleanup()

        # 2. Targets exist but not recorded in lockfile (status should still be in_sync because they match)
        status = StatusService._check_asset_drift(asset, temp_repo, lockfile)
        assert status["status"] == "in_sync"

        # 3. Modify target1 (should detect content drift)
        with open(target1, "w") as f:
            f.write("drifted content")

        status = StatusService._check_asset_drift(asset, temp_repo, lockfile)
        assert status["status"] == "modified"
        assert status["target"] == os.path.abspath(target1)
