"""Comprehensive test suite for StatusService drift analysis and diff calculation."""

import os
import shutil
import tempfile
from unittest.mock import MagicMock, patch

import pytest
import yaml

from rv.models.manifest import Asset, AssetType, Manifest, Profile, Secret
from rv.models.transaction import Lockfile, LockfileEntry
from rv.security.encryptor import AgeEncryptor
from rv.services.status import StatusService


@pytest.fixture
def temp_workspace() -> str:
    """Fixture to provide a clean temporary workspace."""
    tmpdir = tempfile.mkdtemp()
    os.makedirs(os.path.join(tmpdir, "assets"), exist_ok=True)
    os.makedirs(os.path.join(tmpdir, "secrets"), exist_ok=True)
    os.makedirs(os.path.join(tmpdir, "machine"), exist_ok=True)
    yield tmpdir
    shutil.rmtree(tmpdir)


def test_status_lockfile_malformed(temp_workspace: str) -> None:
    """Tests that a malformed lockfile does not crash StatusService and is handled gracefully."""
    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "bashrc_copy",
                "type": "copy",
                "source": "assets/bashrc_src",
                "target": os.path.join(temp_workspace, "system_bashrc"),
            }
        ],
        "profiles": {"base": {"assets": ["bashrc_copy"]}},
    }
    with open(os.path.join(temp_workspace, "manifest.yaml"), "w") as f:
        yaml.safe_dump(manifest_data, f)

    # Create source
    with open(os.path.join(temp_workspace, "assets", "bashrc_src"), "w") as f:
        f.write("content")

    # Create malformed lockfile
    with open(os.path.join(temp_workspace, "manifest.lock"), "w") as f:
        f.write("invalid json { {[")

    # Call status - should recover gracefully and report missing target
    report = StatusService.get_status(temp_workspace, "base")
    assert report["drifted"] is True
    assert report["assets"]["bashrc_copy"]["status"] == "missing"


def test_status_path_interpolation_failure(temp_workspace: str) -> None:
    """Tests status reporting on path interpolation failure (e.g. unresolved env var)."""
    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "bad_path",
                "type": "copy",
                "source": "assets/bashrc_src",
                "target": "${MISSING_ENV_VAR_WITHOUT_DEFAULT}",
            }
        ],
        "profiles": {"base": {"assets": ["bad_path"]}},
    }
    with open(os.path.join(temp_workspace, "manifest.yaml"), "w") as f:
        yaml.safe_dump(manifest_data, f)

    with open(os.path.join(temp_workspace, "assets", "bashrc_src"), "w") as f:
        f.write("content")

    # Should report error status for bad_path
    report = StatusService.get_status(temp_workspace, "base")
    assert report["drifted"] is True
    assert report["assets"]["bad_path"]["status"] == "error"
    assert "Failed path interpolation" in report["assets"]["bad_path"]["details"]


def test_status_symlink_drift_and_mismatch(temp_workspace: str) -> None:
    """Tests drift and mismatch logic for symlink type assets."""
    target_link = os.path.join(temp_workspace, "system_symlink")
    source_file = os.path.join(temp_workspace, "assets/bashrc_src")

    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "my_link",
                "type": "symlink",
                "source": "assets/bashrc_src",
                "target": target_link,
            }
        ],
        "profiles": {"base": {"assets": ["my_link"]}},
    }
    with open(os.path.join(temp_workspace, "manifest.yaml"), "w") as f:
        yaml.safe_dump(manifest_data, f)

    with open(source_file, "w") as f:
        f.write("content")

    # 1. Target is a standard file instead of a symlink
    with open(target_link, "w") as f:
        f.write("standard file")

    report1 = StatusService.get_status(temp_workspace, "base")
    assert report1["assets"]["my_link"]["status"] == "type_mismatch"
    assert "Expected a symlink" in report1["assets"]["my_link"]["details"]

    # 2. Target is a symlink but points to the wrong source
    os.unlink(target_link)
    os.symlink(os.path.join(temp_workspace, "wrong_target"), target_link)

    report2 = StatusService.get_status(temp_workspace, "base")
    assert report2["assets"]["my_link"]["status"] == "modified"
    assert "Symlink points to" in report2["assets"]["my_link"]["details"]

    # 3. Simulate readlink failure / exception
    with patch("os.readlink", side_effect=OSError("Readlink error")):
        report3 = StatusService.get_status(temp_workspace, "base")
        assert report3["assets"]["my_link"]["status"] == "error"
        assert "Failed to read symlink" in report3["assets"]["my_link"]["details"]


def test_status_file_instead_of_symlink_mismatch(temp_workspace: str) -> None:
    """Tests mismatch logic when we expect a file but find a symlink."""
    target_file = os.path.join(temp_workspace, "system_file")
    source_file = os.path.join(temp_workspace, "assets/bashrc_src")

    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "my_file",
                "type": "copy",
                "source": "assets/bashrc_src",
                "target": target_file,
            }
        ],
        "profiles": {"base": {"assets": ["my_file"]}},
    }
    with open(os.path.join(temp_workspace, "manifest.yaml"), "w") as f:
        yaml.safe_dump(manifest_data, f)

    with open(source_file, "w") as f:
        f.write("content")

    # Create target as a symlink
    os.symlink(source_file, target_file)

    report = StatusService.get_status(temp_workspace, "base")
    assert report["assets"]["my_file"]["status"] == "type_mismatch"
    assert "Expected a file, but found a symlink" in report["assets"]["my_file"]["details"]


def test_status_permissions_drift_and_default(temp_workspace: str) -> None:
    """Tests default permissions assignment and permissions drift detection."""
    target_file = os.path.join(temp_workspace, "system_file")
    source_file = os.path.join(temp_workspace, "assets/bashrc_src")

    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "my_file",
                "type": "copy",
                "source": "assets/bashrc_src",
                "target": target_file,
                # permissions is omitted, should default to "0644"
            }
        ],
        "profiles": {"base": {"assets": ["my_file"]}},
    }
    with open(os.path.join(temp_workspace, "manifest.yaml"), "w") as f:
        yaml.safe_dump(manifest_data, f)

    with open(source_file, "w") as f:
        f.write("content")

    # Write target file with wrong permissions (0755)
    with open(target_file, "w") as f:
        f.write("content")
    os.chmod(target_file, 0o755)

    report = StatusService.get_status(temp_workspace, "base")
    assert report["assets"]["my_file"]["status"] == "permissions_drifted"
    assert "Permissions mismatch" in report["assets"]["my_file"]["details"]


def test_status_template_drift_and_exception(temp_workspace: str) -> None:
    """Tests drift and rendering exceptions for Template type assets."""
    target_file = os.path.join(temp_workspace, "system_file")
    source_template = os.path.join(temp_workspace, "assets/bashrc.j2")

    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "my_tpl",
                "type": "template",
                "source": "assets/bashrc.j2",
                "target": target_file,
                "template_vars": {"MY_VAR": "templated_value"},
            }
        ],
        "profiles": {"base": {"assets": ["my_tpl"]}},
    }
    with open(os.path.join(temp_workspace, "manifest.yaml"), "w") as f:
        yaml.safe_dump(manifest_data, f)

    # 1. Perfectly in sync
    with open(source_template, "w") as f:
        f.write("Value: {{ MY_VAR }}")

    with open(target_file, "w") as f:
        f.write("Value: templated_value")
    os.chmod(target_file, 0o644)

    report1 = StatusService.get_status(temp_workspace, "base")
    assert report1["assets"]["my_tpl"]["status"] == "in_sync"

    # 2. Template content drift
    with open(target_file, "w") as f:
        f.write("Value: different_value")

    report2 = StatusService.get_status(temp_workspace, "base")
    assert report2["assets"]["my_tpl"]["status"] == "modified"

    # 3. Simulate template rendering exception (Jinja2 StrictUndefined error)
    with open(source_template, "w") as f:
        f.write("Value: {{ MISSING_VAR }}")

    report3 = StatusService.get_status(temp_workspace, "base")
    # Rendering failure returns True for drift -> status: modified
    assert report3["assets"]["my_tpl"]["status"] == "modified"


def test_status_encrypted_copy_and_secret_drift(temp_workspace: str) -> None:
    """Tests drift calculations for encrypted copies and secrets."""
    target_file = os.path.join(temp_workspace, "system_file")
    source_enc = os.path.join(temp_workspace, "assets/secret.age")
    identity_path = os.path.join(temp_workspace, "identity.txt")

    manifest_data = {
        "version": 2,
        "secrets": [
            {
                "id": "my_sec",
                "source": "assets/secret.age",
                "target": target_file,
            }
        ],
        "profiles": {"base": {"secrets": ["my_sec"]}},
    }
    with open(os.path.join(temp_workspace, "manifest.yaml"), "w") as f:
        yaml.safe_dump(manifest_data, f)

    # Create files
    with open(source_enc, "w") as f:
        f.write("encrypted payload")
    with open(target_file, "w") as f:
        f.write("decrypted payload")
    os.chmod(target_file, 0o600)
    with open(identity_path, "w") as f:
        f.write("identity key")

    # 1. Perfectly in sync (decrypt_file decrypts to matching target hash)
    def mock_decrypt(src, dst, key):
        with open(dst, "w") as f:
            f.write("decrypted payload")

    with patch("rv.security.encryptor.AgeEncryptor.decrypt_file", side_effect=mock_decrypt):
        report1 = StatusService.get_status(temp_workspace, "base", identity_path=identity_path)
        assert report1["assets"]["my_sec"]["status"] == "in_sync"

    # 2. Content drifted
    def mock_decrypt_drifted(src, dst, key):
        with open(dst, "w") as f:
            f.write("different decrypted payload")

    with patch("rv.security.encryptor.AgeEncryptor.decrypt_file", side_effect=mock_decrypt_drifted):
        report2 = StatusService.get_status(temp_workspace, "base", identity_path=identity_path)
        assert report2["assets"]["my_sec"]["status"] == "modified"

    # 3. Decryption exception fallback (compares mtime with lockfile entry)
    lock_entry = LockfileEntry(
        sha256_of_source="123",
        target_path=target_file,
        permissions="0600",
        mtime=os.stat(target_file).st_mtime,
    )
    lockfile = Lockfile(entries={"my_sec": lock_entry})
    lockfile_path = os.path.join(temp_workspace, "manifest.lock")
    with open(lockfile_path, "w") as f:
        f.write(lockfile.model_dump_json())

    # Decryption fails, but mtime matches -> in_sync
    with patch("rv.security.encryptor.AgeEncryptor.decrypt_file", side_effect=RuntimeError("Decryption failed")):
        report3 = StatusService.get_status(temp_workspace, "base", identity_path=identity_path)
        assert report3["assets"]["my_sec"]["status"] == "in_sync"


def test_diff_edge_cases(temp_workspace: str) -> None:
    """Tests all edge cases in get_diff including missing assets, failures, and file formats."""
    with patch(
        "os.path.expanduser", side_effect=lambda path: path.replace("~", os.path.join(temp_workspace, "empty_home"))
    ):
        target_file = os.path.join(temp_workspace, "system_file")
        source_file = os.path.join(temp_workspace, "assets/bashrc_src")
        identity_path = os.path.join(temp_workspace, "identity.txt")

        manifest_data = {
            "version": 2,
            "assets": [
                {
                    "id": "my_file",
                    "type": "copy",
                    "source": "assets/bashrc_src",
                    "target": target_file,
                },
                {
                    "id": "my_enc",
                    "type": "copy",
                    "source": "assets/bashrc_src.age",
                    "target": target_file,
                    "encrypted": True,
                },
                {
                    "id": "my_tpl",
                    "type": "template",
                    "source": "assets/bashrc_src.j2",
                    "target": target_file,
                },
                {
                    "id": "my_link",
                    "type": "symlink",
                    "source": "assets/bashrc_src",
                    "target": os.path.join(temp_workspace, "symlink_tgt"),
                },
            ],
            "secrets": [
                {
                    "id": "my_sec",
                    "source": "assets/secret.age",
                    "target": target_file,
                }
            ],
            "profiles": {
                "base": {
                    "assets": ["my_file", "my_enc", "my_tpl", "my_link"],
                    "secrets": ["my_sec"],
                }
            },
        }
        with open(os.path.join(temp_workspace, "manifest.yaml"), "w") as f:
            yaml.safe_dump(manifest_data, f)

        with open(identity_path, "w") as f:
            f.write("identity key")

        # 1. Non-existent asset
        assert StatusService.get_diff(temp_workspace, "base", "non_existent") is None

        # 2. Path interpolation failure
        with patch("rv.utils.path.PathHelper.canonicalize", side_effect=Exception("Path error")):
            assert StatusService.get_diff(temp_workspace, "base", "my_file") is None

        # 3. Target is missing or a symlink
        assert StatusService.get_diff(temp_workspace, "base", "my_link") is None

        # Write target file for other checks
        with open(target_file, "w") as f:
            f.write("actual text content")

        # 4. Decrypt encrypted copy without identity key
        assert "[Cannot decrypt source" in StatusService.get_diff(temp_workspace, "base", "my_enc", identity_path=None)

        # 5. Decrypt encrypted copy where decryption raises exception
        with patch("rv.security.encryptor.AgeEncryptor.decrypt_file", side_effect=RuntimeError("GPG error")):
            assert "[Decryption failed" in StatusService.get_diff(
                temp_workspace, "base", "my_enc", identity_path=identity_path
            )

        # 6. Decrypt secret without identity key
        assert "[Cannot decrypt secret" in StatusService.get_diff(temp_workspace, "base", "my_sec", identity_path=None)

        # 7. Decrypt secret where decryption raises exception
        with patch("rv.security.encryptor.AgeEncryptor.decrypt_file", side_effect=RuntimeError("Decryption error")):
            assert "[Decryption failed" in StatusService.get_diff(
                temp_workspace, "base", "my_sec", identity_path=identity_path
            )

        # 8. Template rendering failure inside get_diff
        with open(os.path.join(temp_workspace, "assets/bashrc_src.j2"), "w") as f:
            f.write("Hello {{ MISSING_VAR }}")

        assert "[Template rendering failed" in StatusService.get_diff(temp_workspace, "base", "my_tpl")

        # 9. Failed to read target file (simulate IOError on read)
        with open(source_file, "w") as f:
            f.write("repo source text")

        original_open = open

        def conditional_open(file, *args, **kwargs):
            if file == target_file:
                raise OSError("Read permission denied")
            return original_open(file, *args, **kwargs)

        with patch("builtins.open", side_effect=conditional_open):
            # Should raise IOError inside get_diff and return None
            assert StatusService.get_diff(temp_workspace, "base", "my_file") is None


def test_status_relative_symlink(temp_workspace: str) -> None:
    """Verifies that relative symlinks are correctly identified as in_sync during status checking."""
    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "rel_link",
                "type": "symlink",
                "source": "assets/config_file",
                "target": os.path.join(temp_workspace, "symlink_tgt"),
            }
        ],
        "profiles": {
            "base": {
                "assets": ["rel_link"],
            }
        },
    }
    with open(os.path.join(temp_workspace, "manifest.yaml"), "w") as f:
        yaml.safe_dump(manifest_data, f)

    # Create the source file in repo
    source_path = os.path.join(temp_workspace, "assets", "config_file")
    os.makedirs(os.path.dirname(source_path), exist_ok=True)
    with open(source_path, "w") as f:
        f.write("config file contents")

    # Create a relative symlink at the target pointing to the source
    target_path = os.path.join(temp_workspace, "symlink_tgt")
    rel_target = os.path.relpath(source_path, temp_workspace)
    os.symlink(rel_target, target_path)

    # Create the expected lockfile to simulate a restored status check
    from rv.models.transaction import Lockfile, LockfileEntry

    lockfile_path = os.path.join(temp_workspace, "manifest.lock")
    entry = LockfileEntry(
        sha256_of_source="123",
        target_path=target_path,
        permissions="0777",
        mtime=os.lstat(target_path).st_mtime,
    )
    lockfile = Lockfile(
        profile="base",
        entries={"rel_link": entry},
    )
    with open(lockfile_path, "w") as f:
        f.write(lockfile.model_dump_json())

    # Check status
    report = StatusService.get_status(temp_workspace, "base")
    assert report["assets"]["rel_link"]["status"] == "in_sync"
