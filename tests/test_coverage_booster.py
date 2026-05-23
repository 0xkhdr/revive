"""Coverage booster test suite for revive core components."""

import os
import shutil
import subprocess
import tempfile
from collections.abc import Generator
from unittest.mock import MagicMock, patch

import jinja2
import pytest

from rv.models.manifest import Asset, AssetType, ConflictStrategy, Manifest, Profile, Secret
from rv.models.transaction import Lockfile, LockfileEntry
from rv.services.handlers import AssetHandler, AssetHandlerError
from rv.services.restore import ProfileResolver, RestoreService
from rv.services.status import StatusService
from rv.transactions.atomic import AtomicWrite
from rv.transactions.lock import LockAcquisitionError, ProcessLock
from rv.utils.path import PathHelper


@pytest.fixture
def temp_repo() -> Generator[str, None, None]:
    """Creates a temporary repository directory."""
    with tempfile.TemporaryDirectory() as tmpdir:
        os.makedirs(os.path.join(tmpdir, "assets"), exist_ok=True)
        os.makedirs(os.path.join(tmpdir, "secrets"), exist_ok=True)
        os.makedirs(os.path.join(tmpdir, "machine"), exist_ok=True)
        yield tmpdir


def test_path_helper_is_cross_device_missing_paths() -> None:
    """Tests PathHelper.is_cross_device with paths that do not exist, triggering parent traversal."""
    with patch("os.path.exists", return_value=False):
        res = PathHelper.is_cross_device("/tmp/nonexistent1/sub1", "/tmp/nonexistent2/sub2")
        assert res is False


def test_path_helper_is_cross_device_exception() -> None:
    """Tests PathHelper.is_cross_device fallback when stat raises exception."""
    with patch("os.stat", side_effect=OSError("Permission denied")):
        res = PathHelper.is_cross_device("/tmp/1", "/tmp/2")
        assert res is False


def test_path_helper_detect_symlink_loop_relative() -> None:
    """Tests PathHelper.detect_symlink_loop with a relative symlink target."""
    temp_dir = tempfile.mkdtemp()
    try:
        sym1 = os.path.join(temp_dir, "sym1")
        sym2 = os.path.join(temp_dir, "sym2")

        os.symlink("sym2", sym1)
        os.symlink("sym1", sym2)

        assert PathHelper.detect_symlink_loop(sym1) is True
    finally:
        shutil.rmtree(temp_dir)


def test_path_helper_detect_symlink_loop_exception() -> None:
    """Tests PathHelper.detect_symlink_loop when os.readlink raises exception."""
    temp_dir = tempfile.mkdtemp()
    try:
        sym = os.path.join(temp_dir, "sym")
        os.symlink("nonexistent", sym)
        with patch("os.readlink", side_effect=OSError("Broken link")):
            assert PathHelper.detect_symlink_loop(sym) is False
    finally:
        shutil.rmtree(temp_dir)


def test_path_helper_is_safe_subpath() -> None:
    """Tests safe subpath traversal checker."""
    assert PathHelper.is_safe_subpath("/var/www", "/var/www/html") is True
    assert PathHelper.is_safe_subpath("/var/www", "/var/outside") is False
    assert PathHelper.is_safe_subpath("/var/www", "/var/www/../outside") is False


def test_atomic_write_error_unlink_fails() -> None:
    """Tests AtomicWrite error path cleanup when os.unlink fails."""
    temp_dir = tempfile.mkdtemp()
    try:
        target = os.path.join(temp_dir, "test.txt")
        with (
            patch("os.fsync", side_effect=OSError("Sync failed")),
            patch("os.unlink", side_effect=OSError("Unlink failed")),
        ):
            with pytest.raises(RuntimeError, match="Atomic write to .* failed"):
                AtomicWrite.write(target, "content")
    finally:
        shutil.rmtree(temp_dir)


def test_process_lock_default_path() -> None:
    """Tests ProcessLock initialization with default path expanded user directory."""
    lock = ProcessLock()
    assert lock.lock_path == os.path.abspath(os.path.expanduser("~/.config/rv/rv.lock"))


def test_process_lock_unexpected_exception() -> None:
    """Tests ProcessLock __enter__ raising unexpected exceptions."""
    temp_dir = tempfile.mkdtemp()
    try:
        lock_path = os.path.join(temp_dir, "lock")
        lock = ProcessLock(lock_path)
        with patch("fcntl.flock", side_effect=RuntimeError("Unexpected fcntl error")):
            with pytest.raises(RuntimeError, match="Unexpected error while acquiring lock"):
                with lock:
                    pass
    finally:
        shutil.rmtree(temp_dir)


def test_process_lock_exit_exception() -> None:
    """Tests ProcessLock __exit__ handling fcntl release errors gracefully."""
    temp_dir = tempfile.mkdtemp()
    try:
        lock_path = os.path.join(temp_dir, "lock")
        lock = ProcessLock(lock_path)
        with lock:
            with patch("fcntl.flock", side_effect=RuntimeError("Unlock failed")):
                lock.__exit__(None, None, None)
    finally:
        shutil.rmtree(temp_dir)


def mock_exists_source_only(path: str) -> bool:
    """Mock os.path.exists that returns True for sources, but False for targets/locks to avoid conflict logic."""
    if "tgt" in path or "target" in path or "lock" in path:
        return False
    return True


def test_asset_handler_unsupported_type() -> None:
    """Tests AssetHandler raises ValueError for unsupported asset types."""
    asset = Asset(id="bad_type", type=AssetType.COPY, source="src", target="tgt")
    asset.type = "unsupported_type"  # type: ignore
    tx_context = MagicMock()
    with patch("os.path.exists", side_effect=mock_exists_source_only):
        with pytest.raises(ValueError, match="Unsupported asset type"):
            AssetHandler.handle(asset, "/tmp", tx_context)


def test_asset_handler_symlink_loop() -> None:
    """Tests AssetHandler raises AssetHandlerError when a symlink loop is detected."""
    asset = Asset(id="loop_link", type=AssetType.SYMLINK, source="src", target="tgt")
    tx_context = MagicMock()
    with (
        patch("os.path.exists", side_effect=mock_exists_source_only),
        patch("rv.utils.path.PathHelper.detect_symlink_loop", return_value=True),
    ):
        with pytest.raises(AssetHandlerError, match="Symlink loop detected for source"):
            AssetHandler.handle(asset, "/tmp", tx_context)


def test_asset_handler_copy_encrypted_missing_identity() -> None:
    """Tests AssetHandler raises AssetHandlerError when identity_path is missing for encrypted copy."""
    asset = Asset(id="enc_copy", type=AssetType.COPY, source="src.age", target="tgt", encrypted=True)
    tx_context = MagicMock()
    with pytest.raises(AssetHandlerError, match="Identity key required to decrypt encrypted asset"):
        AssetHandler.handle(asset, "/tmp", tx_context, identity_path=None)


def test_asset_handler_copy_decryption_fails() -> None:
    """Tests AssetHandler raises AssetHandlerError when AgeEncryptor decryption fails."""
    asset = Asset(id="enc_copy", type=AssetType.COPY, source="src.age", target="tgt", encrypted=True)
    tx_context = MagicMock()
    with (
        patch("os.path.exists", side_effect=mock_exists_source_only),
        patch("rv.security.encryptor.AgeEncryptor.decrypt_file", side_effect=RuntimeError("Decryption failed")),
    ):
        with pytest.raises(AssetHandlerError, match="Failed to decrypt asset enc_copy"):
            AssetHandler.handle(asset, "/tmp", tx_context, identity_path="/tmp/id")


def test_asset_handler_template_read_fails() -> None:
    """Tests AssetHandler template reading failing."""
    asset = Asset(id="tpl", type=AssetType.TEMPLATE, source="src.j2", target="tgt")
    tx_context = MagicMock()
    with (
        patch("os.path.exists", side_effect=mock_exists_source_only),
        patch("builtins.open", side_effect=OSError("Read failed")),
    ):
        with pytest.raises(AssetHandlerError, match="Failed to read template source"):
            AssetHandler.handle(asset, "/tmp", tx_context)


def mock_open_content(content: str):
    """Helper to mock open context manager returning content."""
    mock = MagicMock()
    mock.__enter__.return_value.read.return_value = content
    return mock


def test_asset_handler_template_render_fails() -> None:
    """Tests AssetHandler template rendering failing."""
    asset = Asset(id="tpl", type=AssetType.TEMPLATE, source="src.j2", target="tgt")
    tx_context = MagicMock()
    with (
        patch("os.path.exists", side_effect=mock_exists_source_only),
        patch("builtins.open", mock_open_content("Hello {{ MISSING_VAR }}")),
    ):
        with pytest.raises(AssetHandlerError, match="Template rendering failed for tpl"):
            AssetHandler.handle(asset, "/tmp", tx_context)


def test_asset_handler_secret_decryption_fails() -> None:
    """Tests AssetHandler secret decryption failure path."""
    secret = Secret(id="sec", source="sec.age", target="tgt")
    tx_context = MagicMock()
    with (
        patch("os.path.exists", side_effect=mock_exists_source_only),
        patch("rv.security.encryptor.AgeEncryptor.decrypt_file", side_effect=RuntimeError("Decryption failed")),
    ):
        with pytest.raises(AssetHandlerError, match="Failed to decrypt secret sec"):
            AssetHandler.handle(secret, "/tmp", tx_context, identity_path="/tmp/id")


def test_status_service_encrypted_drift_mtime_exception() -> None:
    """Tests StatusService _check_encrypted_drift mtime fallback throws exception."""
    lock_entry = LockfileEntry(sha256_of_source="123", target_path="/tmp/tgt", permissions="0600", mtime=123.45)
    with patch("os.stat", side_effect=OSError("Perm denied")):
        res = StatusService._check_encrypted_drift("/tmp/src", "/tmp/tgt", lock_entry, None)
        assert res is True


def test_restore_service_machine_overrides(temp_repo: str) -> None:
    """Tests RestoreService applying host-specific machine overrides."""
    hostname = "my-test-host"
    override_rel = f"machine/{hostname}.yaml"
    override_abs = os.path.join(temp_repo, override_rel)

    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "bashrc_copy",
                "type": "copy",
                "source": "assets/bashrc_src",
                "target": os.path.join(temp_repo, "system_bashrc"),
            }
        ],
        "profiles": {"base": {"assets": ["bashrc_copy"]}},
        "machine_overrides": {"enabled": True, "path": "machine/{hostname}.yaml"},
    }
    with open(os.path.join(temp_repo, "manifest.yaml"), "w", encoding="utf-8") as f:
        import yaml

        yaml.safe_dump(manifest_data, f)

    with open(os.path.join(temp_repo, "assets", "bashrc_src"), "w", encoding="utf-8") as f:
        f.write("content")

    os.makedirs(os.path.dirname(override_abs), exist_ok=True)
    override_data = {
        "assets": [
            {
                "id": "bashrc_copy",
                "type": "copy",
                "source": "assets/bashrc_src",
                "target": os.path.join(temp_repo, "system_bashrc_overridden"),
            }
        ],
        "packages": {"brew": ["git"], "docker": {"images": ["alpine"]}},
    }
    with open(override_abs, "w", encoding="utf-8") as f:
        yaml.safe_dump(override_data, f)

    with (
        patch("socket.gethostname", return_value=hostname),
        patch("rv.providers.brew.BrewProvider.install") as mock_brew,
        patch("rv.providers.docker.DockerProvider.install") as mock_docker,
    ):
        tx_id = RestoreService.restore(temp_repo, "base", interactive=False)
        assert tx_id is not None

        assert os.path.exists(os.path.join(temp_repo, "system_bashrc_overridden"))
        assert not os.path.exists(os.path.join(temp_repo, "system_bashrc"))


def test_restore_service_rollback_on_package_failure(temp_repo: str) -> None:
    """Tests that RestoreService triggers tx_context.rollback() and raises RuntimeError when package installation fails."""
    manifest_data = {
        "version": 2,
        "packages": {"brew": ["git"]},
        "assets": [
            {
                "id": "bashrc_copy",
                "type": "copy",
                "source": "assets/bashrc_src",
                "target": os.path.join(temp_repo, "system_bashrc"),
            }
        ],
        "profiles": {"base": {"assets": ["bashrc_copy"], "packages": ["brew"]}},
    }
    with open(os.path.join(temp_repo, "manifest.yaml"), "w", encoding="utf-8") as f:
        import yaml

        yaml.safe_dump(manifest_data, f)

    with open(os.path.join(temp_repo, "assets", "bashrc_src"), "w", encoding="utf-8") as f:
        f.write("content")

    with patch("rv.providers.brew.BrewProvider.install", side_effect=RuntimeError("Brew install crashed")):
        with pytest.raises(RuntimeError, match="Restore failed during post-execution"):
            RestoreService.restore(temp_repo, "base", interactive=False)

        assert not os.path.exists(os.path.join(temp_repo, "system_bashrc"))
