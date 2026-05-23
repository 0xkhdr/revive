"""Test suite for ManifestLoader, ProfileResolver, RestoreService, StatusService, and DoctorService."""

import os
import shutil
import socket
import tempfile
from collections.abc import Generator
from unittest.mock import MagicMock, patch

import pytest

from rv.models.manifest import Asset, AssetType, ConflictStrategy, Manifest, Profile, Secret
from rv.models.transaction import Lockfile
from rv.security.encryptor import AgeEncryptor
from rv.services.doctor import DoctorService
from rv.services.handlers import AssetHandler, AssetHandlerError
from rv.services.restore import ManifestLoader, ProfileResolver, ResolvedProfile, RestoreService
from rv.services.status import StatusService


@pytest.fixture
def temp_repo() -> Generator[str, None, None]:
    """Creates a temporary repository directory."""
    with tempfile.TemporaryDirectory() as tmpdir:
        os.makedirs(os.path.join(tmpdir, "assets"), exist_ok=True)
        os.makedirs(os.path.join(tmpdir, "secrets"), exist_ok=True)
        os.makedirs(os.path.join(tmpdir, "machine"), exist_ok=True)
        yield tmpdir


def test_manifest_loader_errors(temp_repo: str) -> None:
    # 1. Non-existent path raises FileNotFoundError
    with pytest.raises(FileNotFoundError):
        ManifestLoader.load(os.path.join(temp_repo, "non_existent.yaml"))

    # 2. Malformed YAML
    malformed_path = os.path.join(temp_repo, "malformed.yaml")
    with open(malformed_path, "w") as f:
        f.write("{invalid: yaml: malformed")
    with pytest.raises(ValueError, match="Failed to parse YAML manifest"):
        ManifestLoader.load(malformed_path)

    # 3. Non-dict YAML content
    invalid_content_path = os.path.join(temp_repo, "invalid.yaml")
    with open(invalid_content_path, "w") as f:
        f.write("- item1\n- item2\n")
    with pytest.raises(ValueError, match="Manifest content must be a dictionary"):
        ManifestLoader.load(invalid_content_path)

    # 4. Pydantic validation failure
    invalid_pydantic_path = os.path.join(temp_repo, "invalid_pydantic.yaml")
    with open(invalid_pydantic_path, "w") as f:
        f.write("assets:\n  - id: 1\n    type: invalid_type\n")
    with pytest.raises(ValueError, match="Manifest validation failed"):
        ManifestLoader.load(invalid_pydantic_path)


def test_profile_resolver_inheritance() -> None:
    # Build a complex manifest programmatically
    manifest = Manifest(
        version=2,
        assets=[
            Asset(id="zshrc", type=AssetType.SYMLINK, source="assets/zshrc", target="~/.zshrc"),
            Asset(id="bashrc", type=AssetType.COPY, source="assets/bashrc", target="~/.bashrc"),
            Asset(id="tmux_conf", type=AssetType.COPY, source="assets/tmux.conf", target="~/.tmux.conf"),
        ],
        secrets=[Secret(id="ssh_key", source="secrets/id_ed25519.age", target="~/.ssh/id_ed25519", permissions="0600")],
        profiles={
            "base": Profile(assets=["zshrc"], secrets=["ssh_key"], packages=["brew"]),
            "work": Profile(extends=["base"], assets=["bashrc"], packages=["apt"]),
            "custom": Profile(
                extends=["work"],
                assets=[Asset(id="zshrc", type=AssetType.COPY, source="assets/zshrc_custom", target="~/.zshrc")],
            ),
        },
    )

    # 1. Resolve base profile
    resolved_base = ProfileResolver.resolve(manifest, "base")
    assert "zshrc" in resolved_base.assets
    assert resolved_base.assets["zshrc"].type == AssetType.SYMLINK
    assert "ssh_key" in resolved_base.secrets
    assert resolved_base.packages["brew"] == []  # Not instantiated until Phase 3 integration checks

    # 2. Resolve work profile (extends base)
    resolved_work = ProfileResolver.resolve(manifest, "work")
    assert "zshrc" in resolved_work.assets
    assert "bashrc" in resolved_work.assets
    assert resolved_work.assets["bashrc"].type == AssetType.COPY

    # 3. Resolve custom profile (extends work, overrides zshrc with copy type)
    resolved_custom = ProfileResolver.resolve(manifest, "custom")
    assert "zshrc" in resolved_custom.assets
    assert resolved_custom.assets["zshrc"].type == AssetType.COPY
    assert resolved_custom.assets["zshrc"].source == "assets/zshrc_custom"


def test_profile_resolver_cyclic_detection() -> None:
    manifest = Manifest(
        version=2, profiles={"A": Profile(extends=["B"]), "B": Profile(extends=["C"]), "C": Profile(extends=["A"])}
    )

    with pytest.raises(ValueError, match="Cyclic profile inheritance detected: A -> B -> C -> A"):
        ProfileResolver.resolve(manifest, "A")


def test_profile_resolver_missing_references() -> None:
    manifest = Manifest(version=2, profiles={"base": Profile(assets=["missing_asset_id"])})

    with pytest.raises(ValueError, match="Asset ID 'missing_asset_id' referenced.*does not exist"):
        ProfileResolver.resolve(manifest, "base")


def test_asset_handler_conflict_strategies(temp_repo: str) -> None:
    # Create target conflict file
    with tempfile.TemporaryDirectory() as system_dir:
        target_file = os.path.join(system_dir, "conflict.txt")
        with open(target_file, "w") as f:
            f.write("existing content")

        source_file = os.path.join(temp_repo, "assets", "source.txt")
        with open(source_file, "w") as f:
            f.write("new content")

        # 1. Skip Strategy
        asset_skip = Asset(
            id="test_skip",
            type=AssetType.COPY,
            source="assets/source.txt",
            target=target_file,
            conflict_strategy=ConflictStrategy.SKIP,
        )
        tx_context = MagicMock()
        planned = AssetHandler.handle(asset_skip, temp_repo, tx_context)
        assert planned is False
        tx_context.plan_operation.assert_not_called()

        # 2. Abort Strategy
        asset_abort = Asset(
            id="test_abort",
            type=AssetType.COPY,
            source="assets/source.txt",
            target=target_file,
            conflict_strategy=ConflictStrategy.ABORT,
        )
        with pytest.raises(AssetHandlerError, match="[Cc]onflict strategy is set to 'abort'"):
            AssetHandler.handle(asset_abort, temp_repo, tx_context)

        # 3. Prompt Strategy (non-interactive raises abort)
        asset_prompt = Asset(
            id="test_prompt",
            type=AssetType.COPY,
            source="assets/source.txt",
            target=target_file,
            conflict_strategy=ConflictStrategy.PROMPT,
        )
        with pytest.raises(AssetHandlerError, match="running in non-interactive environment"):
            AssetHandler.handle(asset_prompt, temp_repo, tx_context, interactive=False)


def test_asset_handler_template(temp_repo: str) -> None:
    with tempfile.TemporaryDirectory() as system_dir:
        target_file = os.path.join(system_dir, "config.conf")
        source_template = os.path.join(temp_repo, "assets", "config.j2")

        with open(source_template, "w") as f:
            f.write("db_host = {{ DB_HOST }}\nuser = {{ USERNAME }}")

        asset = Asset(
            id="test_template",
            type=AssetType.TEMPLATE,
            source="assets/config.j2",
            target=target_file,
            template_vars={"DB_HOST": "127.0.0.1", "USERNAME": "test_user"},
        )

        tx_context = MagicMock()
        AssetHandler.handle(asset, temp_repo, tx_context)

        # Verify it plans a copy operation with rendered content
        called_args = tx_context.plan_operation.call_args_list
        assert len(called_args) == 1
        op_type, target = called_args[0][0]
        source_data = called_args[0][1]["source_data"]
        assert op_type == "copy"
        assert target == target_file
        assert source_data == b"db_host = 127.0.0.1\nuser = test_user"


def test_restore_service_end_to_end(temp_repo: str) -> None:
    # 1. Write manifest.yaml
    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "bashrc_copy",
                "type": "copy",
                "source": "assets/bashrc_src",
                "target": os.path.join(temp_repo, "system_bashrc"),
                "permissions": "0644",
                "conflict_strategy": "overwrite",
            },
            {
                "id": "bashrc_link",
                "type": "symlink",
                "source": "assets/bashrc_src",
                "target": os.path.join(temp_repo, "system_bashrc_link"),
                "conflict_strategy": "overwrite",
            },
        ],
        "profiles": {"base": {"assets": ["bashrc_copy", "bashrc_link"]}},
    }

    with open(os.path.join(temp_repo, "manifest.yaml"), "w") as f:
        import yaml

        yaml.safe_dump(manifest_data, f)

    # Create source file
    with open(os.path.join(temp_repo, "assets", "bashrc_src"), "w") as f:
        f.write("# system config file\nexport TEST_VAR=1")

    # Run restore
    tx_id = RestoreService.restore(repo_dir=temp_repo, profile_name="base", interactive=False, dry_run=False)

    assert tx_id is not None

    # 2. Verify targets are placed correctly
    copy_target = os.path.join(temp_repo, "system_bashrc")
    link_target = os.path.join(temp_repo, "system_bashrc_link")

    assert os.path.exists(copy_target)
    assert os.path.islink(link_target)
    assert os.readlink(link_target) == os.path.join(temp_repo, "assets", "bashrc_src")

    # 3. Verify manifest.lock is written and matches
    lockfile_path = os.path.join(temp_repo, "manifest.lock")
    assert os.path.exists(lockfile_path)

    with open(lockfile_path) as f:
        lockfile_data = Lockfile.model_validate_json(f.read())
        assert "bashrc_copy" in lockfile_data.entries
        assert "bashrc_link" in lockfile_data.entries
        assert lockfile_data.entries["bashrc_copy"].target_path == copy_target


def test_restore_service_dry_run(temp_repo: str) -> None:
    # Write basic manifest
    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "dry_copy",
                "type": "copy",
                "source": "assets/dry_src",
                "target": os.path.join(temp_repo, "system_dry"),
                "permissions": "0644",
            }
        ],
        "profiles": {"base": {"assets": ["dry_copy"]}},
    }
    with open(os.path.join(temp_repo, "manifest.yaml"), "w") as f:
        import yaml

        yaml.safe_dump(manifest_data, f)

    with open(os.path.join(temp_repo, "assets", "dry_src"), "w") as f:
        f.write("dry test content")

    # Run restore with dry_run=True
    RestoreService.restore(repo_dir=temp_repo, profile_name="base", interactive=False, dry_run=True)

    # Check target does NOT exist
    assert not os.path.exists(os.path.join(temp_repo, "system_dry"))


def test_status_and_diff_services(temp_repo: str) -> None:
    target_path = os.path.join(temp_repo, "system_bashrc")
    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "bashrc_copy",
                "type": "copy",
                "source": "assets/bashrc_src",
                "target": target_path,
                "permissions": "0644",
            }
        ],
        "profiles": {"base": {"assets": ["bashrc_copy"]}},
    }
    with open(os.path.join(temp_repo, "manifest.yaml"), "w") as f:
        import yaml

        yaml.safe_dump(manifest_data, f)

    # Create source
    with open(os.path.join(temp_repo, "assets", "bashrc_src"), "w") as f:
        f.write("line 1\nline 2\n")

    # 1. Status with missing target
    status_report = StatusService.get_status(temp_repo, "base")
    assert status_report["drifted"] is True
    assert status_report["assets"]["bashrc_copy"]["status"] == "missing"

    # Write target file (perfect sync)
    with open(target_path, "w") as f:
        f.write("line 1\nline 2\n")

    # Set permissions exactly (simulate chmod)
    os.chmod(target_path, 0o644)

    # 2. Perfect sync status
    status_report2 = StatusService.get_status(temp_repo, "base")
    assert status_report2["assets"]["bashrc_copy"]["status"] == "in_sync"

    # Modify target file (content drift)
    with open(target_path, "w") as f:
        f.write("line 1\nline 2 modified\n")

    # 3. Modified drift status
    status_report3 = StatusService.get_status(temp_repo, "base")
    assert status_report3["assets"]["bashrc_copy"]["status"] == "modified"

    # Check diff output
    diff_output = StatusService.get_diff(temp_repo, "base", "bashrc_copy")
    assert diff_output is not None
    assert "-line 2" in diff_output
    assert "+line 2 modified" in diff_output


def test_doctor_service(temp_repo: str) -> None:
    # 1. Dr. checks on uninitialized repo
    uninit_dir = tempfile.mkdtemp()
    uninit_report = DoctorService.check_health(uninit_dir)
    assert uninit_report["healthy"] is False
    assert any(i["category"] == "manifest" for i in uninit_report["issues"])
    shutil.rmtree(uninit_dir)

    # 2. Healthy initialized checks
    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "bashrc_copy",
                "type": "copy",
                "source": "assets/bashrc_src",
                "target": os.path.join(temp_repo, "system_bashrc"),
                "permissions": "0644",
            }
        ],
        "profiles": {"base": {"assets": ["bashrc_copy"]}},
    }
    with open(os.path.join(temp_repo, "manifest.yaml"), "w") as f:
        import yaml

        yaml.safe_dump(manifest_data, f)

    # Source exists
    with open(os.path.join(temp_repo, "assets", "bashrc_src"), "w") as f:
        f.write("test content")

    report = DoctorService.check_health(temp_repo, "base")
    assert report["healthy"] is True
    assert len(report["issues"]) == 0


def test_profile_resolver_multiple_profiles() -> None:
    from rv.models.manifest import Packages

    manifest = Manifest(
        version=2,
        packages=Packages(brew=["ripgrep"], apt=["curl"]),
        assets=[
            Asset(id="zshrc", type=AssetType.SYMLINK, source="assets/zshrc", target="~/.zshrc"),
            Asset(id="bashrc", type=AssetType.COPY, source="assets/bashrc", target="~/.bashrc"),
        ],
        secrets=[
            Secret(id="ssh_key", source="secrets/id_ed25519.age", target="~/.ssh/id_ed25519", permissions="0600"),
            Secret(id="vpn_key", source="secrets/vpn.age", target="~/.vpn/key", permissions="0600"),
        ],
        profiles={
            "base": Profile(assets=["zshrc"], secrets=["ssh_key"], packages=["brew"]),
            "work": Profile(assets=["bashrc"], secrets=["vpn_key"], packages=["apt"]),
        },
    )

    resolved = ProfileResolver.resolve(manifest, "base,work")
    assert "zshrc" in resolved.assets
    assert "bashrc" in resolved.assets
    assert "ssh_key" in resolved.secrets
    assert "vpn_key" in resolved.secrets
    assert resolved.packages["brew"] == ["ripgrep"]
    assert resolved.packages["apt"] == ["curl"]

    # Test error handling of invalid profile in list
    with pytest.raises(ValueError, match="Profile 'invalid' is not defined"):
        ProfileResolver.resolve(manifest, "base,invalid")
