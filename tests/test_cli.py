"""Test suite for the Typer CLI commands in rv.cli.main.
"""

import os
import tempfile
import pytest
from unittest.mock import patch, MagicMock
from typer.testing import CliRunner

from rv.cli.main import app, _get_repo_dir
from rv.models.manifest import AssetType

runner = CliRunner()


@pytest.fixture
def temp_repo() -> str:
    """Creates a temporary directory to act as the current working directory."""
    with tempfile.TemporaryDirectory() as tmpdir:
        yield tmpdir


def test_cli_init(temp_repo: str) -> None:
    """Tests 'rv init' scaffolds directories and manifest."""
    with patch("os.getcwd", return_value=temp_repo):
        result = runner.invoke(app, ["init"])
        assert result.exit_code == 0
        assert "Success!" in result.stdout
        assert os.path.exists(os.path.join(temp_repo, "manifest.yaml"))
        assert os.path.exists(os.path.join(temp_repo, "assets", "example_zshrc"))

        # Running again should fail
        result_again = runner.invoke(app, ["init"])
        assert result_again.exit_code == 1
        assert "Error:" in result_again.stdout


def test_cli_restore(temp_repo: str) -> None:
    """Tests 'rv restore' invokes RestoreService.restore."""
    with patch("os.getcwd", return_value=temp_repo):
        # 1. Success case
        with patch("rv.services.restore.RestoreService.restore") as mock_restore:
            result = runner.invoke(app, ["restore", "base"])
            assert result.exit_code == 0
            mock_restore.assert_called_once_with(
                repo_dir=temp_repo,
                profile_name="base",
                identity_path=None,
                interactive=True,
                dry_run=False,
                no_plugins=False
            )

        # 2. Success case with options
        with patch("rv.services.restore.RestoreService.restore") as mock_restore:
            result = runner.invoke(app, [
                "restore", "work",
                "--identity", "id_file",
                "--dry-run",
                "--non-interactive"
            ])
            assert result.exit_code == 0
            mock_restore.assert_called_once_with(
                repo_dir=temp_repo,
                profile_name="work",
                identity_path="id_file",
                interactive=False,
                dry_run=True,
                no_plugins=False
            )

        # 3. Success case with no-plugins option
        with patch("rv.services.restore.RestoreService.restore") as mock_restore:
            result = runner.invoke(app, ["restore", "base", "--no-plugins"])
            assert result.exit_code == 0
            mock_restore.assert_called_once_with(
                repo_dir=temp_repo,
                profile_name="base",
                identity_path=None,
                interactive=True,
                dry_run=False,
                no_plugins=True
            )

        # 4. Failure case
        with patch("rv.services.restore.RestoreService.restore", side_effect=Exception("Restore crashed")):
            result = runner.invoke(app, ["restore", "base"])
            assert result.exit_code == 2
            assert "Transaction Failed:" in result.stdout


def test_cli_status(temp_repo: str) -> None:
    """Tests 'rv status' output and drift detection."""
    with patch("os.getcwd", return_value=temp_repo):
        # 1. In-sync case
        report_sync = {
            "drifted": False,
            "assets": {
                "test_zshrc": {
                    "type": AssetType.SYMLINK,
                    "target": "/home/user/.zshrc",
                    "status": "in_sync",
                    "details": "Matches manifest"
                }
            }
        }
        with patch("rv.services.status.StatusService.get_status", return_value=report_sync) as mock_status:
            result = runner.invoke(app, ["status", "-p", "base"])
            assert result.exit_code == 0
            assert "In Sync" in result.stdout
            assert "test_zshrc" in result.stdout
            mock_status.assert_called_once_with(temp_repo, "base", None)

        # 2. Drifted case
        report_drifted = {
            "drifted": True,
            "assets": {
                "test_zshrc": {
                    "type": AssetType.SYMLINK,
                    "target": "/home/user/.zshrc",
                    "status": "modified",
                    "details": "Content has changed"
                }
            }
        }
        with patch("rv.services.status.StatusService.get_status", return_value=report_drifted) as mock_status:
            result = runner.invoke(app, ["status", "-p", "base"])
            # The CLI is configured to return code 0 on drift (as shown in main.py:195, wait, exit code is 0 but prints warning)
            assert result.exit_code == 0
            assert "Warning:" in result.stdout
            assert "test_zshrc" in result.stdout

        # 3. Error case
        with patch("rv.services.status.StatusService.get_status", side_effect=Exception("Status failed")):
            result = runner.invoke(app, ["status", "-p", "base"])
            assert result.exit_code == 1
            assert "Status check failed:" in result.stdout


def test_cli_diff(temp_repo: str) -> None:
    """Tests 'rv diff' command."""
    with patch("os.getcwd", return_value=temp_repo):
        # 1. No diffs
        report_sync = {
            "drifted": False,
            "assets": {
                "test_zshrc": {
                    "type": AssetType.SYMLINK,
                    "target": "/home/user/.zshrc",
                    "status": "in_sync",
                    "details": "Matches manifest"
                }
            }
        }
        with patch("rv.services.status.StatusService.get_status", return_value=report_sync):
            result = runner.invoke(app, ["diff", "-p", "base"])
            assert result.exit_code == 0
            assert "No file content modifications detected." in result.stdout

        # 2. Diffs present
        report_drifted = {
            "drifted": True,
            "assets": {
                "test_zshrc": {
                    "type": AssetType.SYMLINK,
                    "target": "/home/user/.zshrc",
                    "status": "modified",
                    "details": "Content has changed"
                }
            }
        }
        with patch("rv.services.status.StatusService.get_status", return_value=report_drifted):
            with patch("rv.services.status.StatusService.get_diff", return_value="- old\n+ new") as mock_diff:
                result = runner.invoke(app, ["diff", "-p", "base"])
                assert result.exit_code == 0
                assert "Drift Diff: test_zshrc" in result.stdout
                assert "- old" in result.stdout
                mock_diff.assert_called_once_with(temp_repo, "base", "test_zshrc", None)

        # 3. Failed status in diff
        with patch("rv.services.status.StatusService.get_status", side_effect=Exception("Diff status failed")):
            result = runner.invoke(app, ["diff", "-p", "base"])
            assert result.exit_code == 1
            assert "Failed to get drift status" in result.stdout


def test_cli_doctor(temp_repo: str) -> None:
    """Tests 'rv doctor' command."""
    with patch("os.getcwd", return_value=temp_repo):
        # 1. Healthy case
        report_healthy = {
            "healthy": True,
            "checks_run": 5,
            "tools": {"age": True, "git": True},
            "issues": []
        }
        with patch("rv.services.doctor.DoctorService.check_health", return_value=report_healthy):
            result = runner.invoke(app, ["doctor"])
            assert result.exit_code == 0
            assert "HEALTHY" in result.stdout
            assert "No issues detected" in result.stdout

        # 2. Healthy json output
        with patch("rv.services.doctor.DoctorService.check_health", return_value=report_healthy):
            result = runner.invoke(app, ["doctor", "--json"])
            assert result.exit_code == 0
            assert '"healthy": true' in result.stdout

        # 3. Unhealthy case
        report_unhealthy = {
            "healthy": False,
            "checks_run": 5,
            "tools": {"age": False, "git": True},
            "issues": [{"severity": "critical", "category": "Sanity", "message": "Missing age tool"}]
        }
        with patch("rv.services.doctor.DoctorService.check_health", return_value=report_unhealthy):
            result = runner.invoke(app, ["doctor"])
            assert result.exit_code == 1
            assert "ISSUES FOUND" in result.stdout
            assert "[Critical]" in result.stdout


def test_cli_secret_commands() -> None:
    """Tests the cryptographic secret management commands under 'rv secret'."""
    # 1. encrypt
    with patch("rv.security.encryptor.AgeEncryptor.encrypt_file") as mock_encrypt:
        result = runner.invoke(app, [
            "secret", "encrypt", "plain.txt",
            "-o", "cipher.age",
            "-r", "age1pubkey"
        ])
        assert result.exit_code == 0
        assert "Successfully encrypted secret" in result.stdout
        mock_encrypt.assert_called_once_with("plain.txt", "cipher.age", ["age1pubkey"])

    # 2. encrypt error
    with patch("rv.security.encryptor.AgeEncryptor.encrypt_file", side_effect=Exception("Encrypt fail")):
        result = runner.invoke(app, [
            "secret", "encrypt", "plain.txt",
            "-o", "cipher.age",
            "-r", "age1pubkey"
        ])
        assert result.exit_code == 1
        assert "Encryption failed:" in result.stdout

    # 3. decrypt
    with patch("rv.security.encryptor.AgeEncryptor.decrypt_file") as mock_decrypt:
        result = runner.invoke(app, [
            "secret", "decrypt", "cipher.age",
            "-o", "plain.txt",
            "-i", "identity.txt"
        ])
        assert result.exit_code == 0
        assert "Successfully decrypted secret" in result.stdout
        mock_decrypt.assert_called_once_with("cipher.age", "plain.txt", "identity.txt")

    # 4. decrypt error
    with patch("rv.security.encryptor.AgeEncryptor.decrypt_file", side_effect=Exception("Decrypt fail")):
        result = runner.invoke(app, [
            "secret", "decrypt", "cipher.age",
            "-o", "plain.txt",
            "-i", "identity.txt"
        ])
        assert result.exit_code == 1
        assert "Decryption failed:" in result.stdout

    # 5. rotate
    with patch("rv.security.encryptor.AgeEncryptor.decrypt_file") as mock_decrypt, \
         patch("rv.security.encryptor.AgeEncryptor.encrypt_file") as mock_encrypt:
        result = runner.invoke(app, [
            "secret", "rotate", "cipher.age",
            "-i", "identity.txt",
            "-nr", "age1newpub"
        ])
        assert result.exit_code == 0
        assert "rotated to new recipients" in result.stdout
        mock_decrypt.assert_called_once()
        mock_encrypt.assert_called_once()

    # 6. rotate failure
    with patch("rv.security.encryptor.AgeEncryptor.decrypt_file", side_effect=Exception("Rotate decrypt fail")):
        result = runner.invoke(app, [
            "secret", "rotate", "cipher.age",
            "-i", "identity.txt",
            "-nr", "age1newpub"
        ])
        assert result.exit_code == 1
        assert "Rotation failed:" in result.stdout


def test_cli_verbose_headless(temp_repo: str) -> None:
    """Tests --verbose and --headless setup flag callbacks."""
    with patch("rv.logging.audit.AuditLogger.setup") as mock_setup, \
         patch("os.getcwd", return_value=temp_repo):
        result = runner.invoke(app, ["--verbose", "--headless", "init"])
        assert result.exit_code == 0
        mock_setup.assert_called_once_with(verbose=True, headless=True)
