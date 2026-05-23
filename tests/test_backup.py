"""Test suite for BackupService and bidirectional backup operations."""

import os
import shutil
import tempfile
from unittest.mock import MagicMock, patch

import pytest

from rv.models.manifest import Asset, AssetType, Manifest, Profile, Secret
from rv.security.encryptor import AgeEncryptor
from rv.services.backup import BackupService


@pytest.fixture
def temp_workspace():
    with tempfile.TemporaryDirectory() as tmpdir:
        repo_dir = os.path.join(tmpdir, "repo")
        system_dir = os.path.join(tmpdir, "system")
        os.makedirs(repo_dir)
        os.makedirs(os.path.join(repo_dir, "assets"))
        os.makedirs(os.path.join(repo_dir, "secrets"))
        os.makedirs(system_dir)

        # Write dummy identity file
        config_dir = os.path.join(tmpdir, "config")
        os.makedirs(config_dir)
        identity_file = os.path.join(config_dir, "identity.txt")

        yield repo_dir, system_dir, identity_file, tmpdir


def test_resolve_identity_path(temp_workspace):
    repo_dir, system_dir, identity_file, tmpdir = temp_workspace

    # 1. Custom path exists
    custom_path = os.path.join(tmpdir, "custom_key.txt")
    with open(custom_path, "w") as f:
        f.write("AGE-SECRET-KEY-1...")

    resolved = BackupService.resolve_identity(custom_path, True)
    assert resolved == os.path.abspath(custom_path)

    # 2. Custom path does not exist raises FileNotFoundError
    with pytest.raises(FileNotFoundError):
        BackupService.resolve_identity(os.path.join(tmpdir, "non_existent.txt"), True)

    # 3. Default path lookup
    with patch("os.path.expanduser", side_effect=lambda path: path.replace("~", tmpdir)):
        default_dir = os.path.join(tmpdir, ".config", "rv")
        keys_dir = os.path.join(default_dir, "keys")
        os.makedirs(keys_dir, exist_ok=True)

        identity_file = os.path.join(default_dir, "identity.txt")
        keys_identity_file = os.path.join(keys_dir, "identity.txt")
        identifier_file = os.path.join(default_dir, "identifier.txt")

        # 3.1. identity.txt exists (highest preference)
        with open(identity_file, "w") as f:
            f.write("AGE-SECRET-KEY-1...")
        assert BackupService.resolve_identity(None, True) == identity_file
        os.remove(identity_file)

        # 3.2. keys/identity.txt exists (middle preference)
        with open(keys_identity_file, "w") as f:
            f.write("AGE-SECRET-KEY-1...")
        assert BackupService.resolve_identity(None, True) == keys_identity_file
        os.remove(keys_identity_file)

        # 3.3. identifier.txt exists (lowest preference)
        with open(identifier_file, "w") as f:
            f.write("AGE-SECRET-KEY-1...")
        assert BackupService.resolve_identity(None, True) == identifier_file
        os.remove(identifier_file)

    # 4. Default path does not exist, but no encrypted profile exists
    with patch("os.path.expanduser", side_effect=lambda path: path.replace("~", os.path.join(tmpdir, "empty_home"))):
        resolved = BackupService.resolve_identity(None, False)
        assert resolved is None

    # 5. Default path does not exist, and secrets DO exist
    with patch("os.path.expanduser", side_effect=lambda path: path.replace("~", os.path.join(tmpdir, "empty_home"))):
        with pytest.raises(ValueError, match="Age identity file not found at default location"):
            BackupService.resolve_identity(None, True)


def test_backup_flow(temp_workspace):
    repo_dir, system_dir, identity_file, tmpdir = temp_workspace

    # Mock AgeEncryptor methods
    with (
        patch.object(AgeEncryptor, "get_public_key", return_value="age1_mock_pub"),
        patch.object(AgeEncryptor, "encrypt_file") as mock_encrypt,
    ):
        # Create manifest
        manifest_path = os.path.join(repo_dir, "manifest.yaml")
        manifest_content = f"""
version: 2
assets:
  - id: my_zshrc
    type: copy
    source: assets/zshrc
    target: {system_dir}/.zshrc
  - id: my_symlink
    type: symlink
    source: assets/config_file
    target: {system_dir}/config_file
  - id: my_template
    type: template
    source: assets/template_file
    target: {system_dir}/template_file
secrets:
  - id: my_secret
    source: secrets/my_secret.age
    target: {system_dir}/my_secret
profiles:
  base:
    assets: [my_zshrc, my_symlink, my_template]
    secrets: [my_secret]
"""
        with open(manifest_path, "w") as f:
            f.write(manifest_content)

        # Write dummy files to system_dir
        with open(os.path.join(system_dir, ".zshrc"), "w") as f:
            f.write("zshrc content")
        with open(os.path.join(system_dir, "config_file"), "w") as f:
            f.write("config file content")
        with open(os.path.join(system_dir, "my_secret"), "w") as f:
            f.write("secret content")

        # Create a mock identity file
        with open(identity_file, "w") as f:
            f.write("AGE-SECRET-KEY-1...")

        # Run backup service in dry-run
        backed_up = BackupService.backup(repo_dir, "base", identity_path=identity_file, dry_run=True)
        assert "my_zshrc" in backed_up
        assert "my_symlink" in backed_up
        assert "my_secret" in backed_up
        # Verify no files were actually written to repo
        assert not os.path.exists(os.path.join(repo_dir, "assets", "zshrc"))

        # Run active backup
        backed_up = BackupService.backup(repo_dir, "base", identity_path=identity_file, dry_run=False)
        assert "my_zshrc" in backed_up
        assert "my_symlink" in backed_up
        assert "my_secret" in backed_up

        # Check copy asset was copied
        zshrc_repo = os.path.join(repo_dir, "assets", "zshrc")
        assert os.path.exists(zshrc_repo)
        with open(zshrc_repo) as f:
            assert f.read() == "zshrc content"

        # Check symlink asset's actual file content was copied
        config_repo = os.path.join(repo_dir, "assets", "config_file")
        assert os.path.exists(config_repo)
        with open(config_repo) as f:
            assert f.read() == "config file content"

        # Check secret was encrypted
        mock_encrypt.assert_called_once()
        args, kwargs = mock_encrypt.call_args
        assert args[0] == os.path.join(system_dir, "my_secret")
        assert args[1] == os.path.join(repo_dir, "secrets", "my_secret.age")
        assert args[2] == ["age1_mock_pub"]


def test_backup_relative_and_broken_symlinks(temp_workspace):
    repo_dir, system_dir, identity_file, tmpdir = temp_workspace

    manifest_path = os.path.join(repo_dir, "manifest.yaml")
    manifest_content = f"""
version: 2
assets:
  - id: relative_symlink_asset
    type: symlink
    source: assets/config_file
    target: {system_dir}/config_file
  - id: broken_symlink_asset
    type: symlink
    source: assets/broken_link
    target: {system_dir}/broken_link
profiles:
  base:
    assets: [relative_symlink_asset, broken_symlink_asset]
"""
    with open(manifest_path, "w") as f:
        f.write(manifest_content)

    # 1. Create a relative symlink on system pointing to the expected repo source
    repo_source_path = os.path.join(repo_dir, "assets", "config_file")
    os.makedirs(os.path.dirname(repo_source_path), exist_ok=True)
    with open(repo_source_path, "w") as f:
        f.write("in repo content")

    rel_target = os.path.relpath(repo_source_path, system_dir)
    os.symlink(rel_target, os.path.join(system_dir, "config_file"))

    # 2. Create a broken symlink on system pointing to non-existent path
    os.symlink("non_existent_target_path", os.path.join(system_dir, "broken_link"))

    # Run backup. The relative symlink is already in sync, so it should be skipped without error.
    # The broken symlink target doesn't exist, so it should be skipped with a warning.
    # Neither should fail or raise FileNotFoundError.
    backed_up = BackupService.backup(repo_dir, "base", identity_path=None, dry_run=False)
    
    assert "relative_symlink_asset" in backed_up
    assert "broken_symlink_asset" in backed_up


def test_backup_multi_target_secrets(temp_workspace):
    repo_dir, system_dir, identity_file, tmpdir = temp_workspace

    with (
        patch.object(AgeEncryptor, "get_public_key", return_value="age1_mock_pub"),
        patch.object(AgeEncryptor, "encrypt_file") as mock_encrypt,
    ):
        manifest_path = os.path.join(repo_dir, "manifest.yaml")
        manifest_content = f"""
version: 2
secrets:
  - id: card_express_env
    source: secrets/card_express_env
    target:
      - {system_dir}/.env
      - {system_dir}/.env.deploy
    permissions: "0600"
profiles:
  base:
    secrets: [card_express_env]
"""
        with open(manifest_path, "w") as f:
            f.write(manifest_content)

        # Create both target files on system
        with open(os.path.join(system_dir, ".env"), "w") as f:
            f.write("env content")
        with open(os.path.join(system_dir, ".env.deploy"), "w") as f:
            f.write("deploy content")

        # Create a mock identity file
        with open(identity_file, "w") as f:
            f.write("AGE-SECRET-KEY-1...")

        # Run backup service
        backed_up = BackupService.backup(repo_dir, "base", identity_path=identity_file, dry_run=False)
        assert "card_express_env" in backed_up

        # Since it processes both targets, it should encrypt twice to the SAME source path
        # first with .env, second with .env.deploy (so both should be encrypted to secrets/card_express_env)
        assert mock_encrypt.call_count == 2
        
        # Verify first call
        first_call_args = mock_encrypt.call_args_list[0][0]
        assert first_call_args[0] == os.path.join(system_dir, ".env")
        assert first_call_args[1] == os.path.join(repo_dir, "secrets", "card_express_env")
        assert first_call_args[2] == ["age1_mock_pub"]

        # Verify second call
        second_call_args = mock_encrypt.call_args_list[1][0]
        assert second_call_args[0] == os.path.join(system_dir, ".env.deploy")
        assert second_call_args[1] == os.path.join(repo_dir, "secrets", "card_express_env")
        assert second_call_args[2] == ["age1_mock_pub"]


