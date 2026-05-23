"""Test suite for the Typer CLI commands in rv.cli.main."""

import os
import tempfile
from unittest.mock import MagicMock, patch

import pytest
from typer.testing import CliRunner

from rv.cli.main import _get_repo_dir, app
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
        assert os.path.exists(os.path.join(temp_repo, "AGENTS.md"))
        assert os.path.exists(os.path.join(temp_repo, ".agents", "skills", "rv", "SKILL.md"))

        # Verify gitignore has proper ignoring for AI agents, IDEs, and local state/secrets
        gitignore_path = os.path.join(temp_repo, ".gitignore")
        assert os.path.exists(gitignore_path)
        with open(gitignore_path, encoding="utf-8") as f:
            content = f.read()
            assert ".claude/" in content
            assert ".cline/" in content
            assert ".vscode/" in content
            assert ".idea/" in content
            assert "identity.txt" in content
            assert ".antigravitycli/" in content

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
                no_plugins=False,
                parallel=True,
                force_packages=False,
            )

        # 2. Success case with options
        with patch("rv.services.restore.RestoreService.restore") as mock_restore:
            result = runner.invoke(app, ["restore", "work", "--identity", "id_file", "--dry-run", "--non-interactive"])
            assert result.exit_code == 0
            mock_restore.assert_called_once_with(
                repo_dir=temp_repo,
                profile_name="work",
                identity_path="id_file",
                interactive=False,
                dry_run=True,
                no_plugins=False,
                parallel=True,
                force_packages=False,
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
                no_plugins=True,
                parallel=True,
                force_packages=False,
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
                    "details": "Matches manifest",
                }
            },
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
                    "details": "Content has changed",
                }
            },
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
                    "details": "Matches manifest",
                }
            },
        }
        with patch("rv.services.status.StatusService.get_status", return_value=report_sync):
            result = runner.invoke(app, ["diff", "-p", "base"])
            assert result.exit_code == 0
            assert "No file content modifications detected." in result.stdout

        # 2. Diffs present (Unified flag)
        report_drifted = {
            "drifted": True,
            "assets": {
                "test_zshrc": {
                    "type": AssetType.SYMLINK,
                    "target": "/home/user/.zshrc",
                    "status": "modified",
                    "details": "Content has changed",
                }
            },
        }
        with patch("rv.services.status.StatusService.get_status", return_value=report_drifted):
            with patch("rv.services.status.StatusService.get_diff", return_value="- old\n+ new") as mock_diff:
                result = runner.invoke(app, ["diff", "-p", "base", "--unified"])
                assert result.exit_code == 0
                assert "Drift Diff: test_zshrc" in result.stdout
                assert "- old" in result.stdout
                mock_diff.assert_called_once_with(temp_repo, "base", "test_zshrc", None)

            # 2b. Diffs present (Side-by-side default)
            with patch(
                "rv.services.status.StatusService.get_contents_for_diff", return_value=("old\nline1", "new\nline1")
            ):
                result = runner.invoke(app, ["diff", "-p", "base"])
                assert result.exit_code == 0
                assert "Expected" in result.stdout
                assert "Actual" in result.stdout

            # 2c. Diffs present with early-return error placeholder
            with patch(
                "rv.services.status.StatusService.get_contents_for_diff",
                return_value=("[Cannot decrypt source: identity file missing]", ""),
            ):
                result = runner.invoke(app, ["diff", "-p", "base"])
                assert result.exit_code == 0
                assert "Error rendering diff" in result.stdout

        # 3. Failed status in diff
        with patch("rv.services.status.StatusService.get_status", side_effect=Exception("Diff status failed")):
            result = runner.invoke(app, ["diff", "-p", "base"])
            assert result.exit_code == 1
            assert "Failed to get drift status" in result.stdout


def test_cli_doctor(temp_repo: str) -> None:
    """Tests 'rv doctor' command."""
    with patch("os.getcwd", return_value=temp_repo):
        # 1. Healthy case
        report_healthy = {"healthy": True, "checks_run": 5, "tools": {"age": True, "git": True}, "issues": []}
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
            "issues": [{"severity": "critical", "category": "Sanity", "message": "Missing age tool"}],
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
        result = runner.invoke(app, ["secret", "encrypt", "plain.txt", "-o", "cipher.age", "-r", "age1pubkey"])
        assert result.exit_code == 0
        assert "Successfully encrypted secret" in result.stdout
        mock_encrypt.assert_called_once_with("plain.txt", "cipher.age", ["age1pubkey"])

    # 2. encrypt error
    with patch("rv.security.encryptor.AgeEncryptor.encrypt_file", side_effect=Exception("Encrypt fail")):
        result = runner.invoke(app, ["secret", "encrypt", "plain.txt", "-o", "cipher.age", "-r", "age1pubkey"])
        assert result.exit_code == 1
        assert "Encryption failed:" in result.stdout

    # 3. decrypt
    with patch("rv.security.encryptor.AgeEncryptor.decrypt_file") as mock_decrypt:
        result = runner.invoke(app, ["secret", "decrypt", "cipher.age", "-o", "plain.txt", "-i", "identity.txt"])
        assert result.exit_code == 0
        assert "Successfully decrypted secret" in result.stdout
        mock_decrypt.assert_called_once_with("cipher.age", "plain.txt", "identity.txt")

    # 4. decrypt error
    with patch("rv.security.encryptor.AgeEncryptor.decrypt_file", side_effect=Exception("Decrypt fail")):
        result = runner.invoke(app, ["secret", "decrypt", "cipher.age", "-o", "plain.txt", "-i", "identity.txt"])
        assert result.exit_code == 1
        assert "Decryption failed:" in result.stdout

    # 5. rotate
    with (
        patch("rv.security.encryptor.AgeEncryptor.decrypt_file") as mock_decrypt,
        patch("rv.security.encryptor.AgeEncryptor.encrypt_file") as mock_encrypt,
    ):
        result = runner.invoke(app, ["secret", "rotate", "cipher.age", "-i", "identity.txt", "-nr", "age1newpub"])
        assert result.exit_code == 0
        assert "rotated to new recipients" in result.stdout
        mock_decrypt.assert_called_once()
        mock_encrypt.assert_called_once()

    # 6. rotate failure
    with patch("rv.security.encryptor.AgeEncryptor.decrypt_file", side_effect=Exception("Rotate decrypt fail")):
        result = runner.invoke(app, ["secret", "rotate", "cipher.age", "-i", "identity.txt", "-nr", "age1newpub"])
        assert result.exit_code == 1
        assert "Rotation failed:" in result.stdout


def test_cli_verbose_headless(temp_repo: str) -> None:
    """Tests --verbose and --headless setup flag callbacks."""
    with patch("rv.logging.audit.AuditLogger.setup") as mock_setup, patch("os.getcwd", return_value=temp_repo):
        result = runner.invoke(app, ["--verbose", "--headless", "init"])
        assert result.exit_code == 0
        mock_setup.assert_called_once_with(verbose=True, headless=True)


def test_cli_secret_keygen(temp_repo: str) -> None:
    """Tests 'rv secret keygen' command."""
    # 1. Successful key generation in stdout
    with patch(
        "rv.security.encryptor.AgeEncryptor.generate_keypair", return_value=("age1pubkey", "AGE-SECRET-KEY-1PRIVKEY")
    ):
        result = runner.invoke(app, ["secret", "keygen"])
        assert result.exit_code == 0
        assert "AGE-SECRET-KEY-1PRIVKEY" in result.stdout
        assert "age1pubkey" in result.stdout

    # 2. Successful key generation with file output
    out_key_path = os.path.join(temp_repo, "keys", "my_identity.key")
    with (
        patch(
            "rv.security.encryptor.AgeEncryptor.generate_keypair",
            return_value=("age1pubkey", "AGE-SECRET-KEY-1PRIVKEY"),
        ),
        patch("os.chmod") as mock_chmod,
    ):
        result = runner.invoke(app, ["secret", "keygen", "-o", out_key_path])
        assert result.exit_code == 0
        assert "Private identity key saved to:" in result.stdout
        assert "age1pubkey" in result.stdout
        assert os.path.exists(out_key_path)
        with open(out_key_path, encoding="utf-8") as f:
            content = f.read()
            assert "# public key: age1pubkey" in content
            assert "AGE-SECRET-KEY-1PRIVKEY" in content
        mock_chmod.assert_called_once_with(out_key_path, 0o600)

    # 3. Handle key generation exception
    with patch("rv.security.encryptor.AgeEncryptor.generate_keypair", side_effect=RuntimeError("No keygen available")):
        result = runner.invoke(app, ["secret", "keygen"])
        assert result.exit_code == 1
        assert "Key generation failed:" in result.stdout


def test_cli_self_install(temp_repo: str) -> None:
    """Tests 'rv self-install' command."""
    custom_home = os.path.join(temp_repo, "user_home")
    os.makedirs(custom_home, exist_ok=True)
    target_bin_dir = os.path.join(custom_home, ".local", "bin")
    target_file = os.path.join(target_bin_dir, "rv")

    # 1. Success case (wrapper generated)
    with patch("os.path.expanduser", return_value=custom_home), patch("os.chmod") as mock_chmod:
        result = runner.invoke(app, ["self-install"])
        assert result.exit_code == 0
        assert "Successfully installed Revive CLI wrapper globally!" in result.stdout
        assert os.path.exists(target_file)
        with open(target_file, encoding="utf-8") as f:
            content = f.read()
            assert "# Revive CLI Autogenerated Wrapper" in content
            assert "exec" in content
        mock_chmod.assert_called_once_with(target_file, 0o755)

    # 2. Overwrite check without force flag
    with patch("os.path.expanduser", return_value=custom_home):
        result = runner.invoke(app, ["self-install"])
        assert result.exit_code == 0
        assert "Warning: An installation wrapper already exists" in result.stdout

    # 3. Overwrite check with force flag
    with patch("os.path.expanduser", return_value=custom_home), patch("os.chmod"):
        result = runner.invoke(app, ["self-install", "--force"])
        assert result.exit_code == 0
        assert "Successfully installed Revive CLI wrapper globally!" in result.stdout

    # 4. Error case
    with (
        patch("os.path.expanduser", return_value=custom_home),
        patch("os.chmod", side_effect=OSError("Permission denied")),
    ):
        result = runner.invoke(app, ["self-install", "--force"])
        assert result.exit_code == 1
        assert "Self-installation failed:" in result.stdout


def test_cli_restore_multiple_profiles(temp_repo: str) -> None:
    """Tests 'rv restore' with multiple profiles."""
    with patch("os.getcwd", return_value=temp_repo):
        with patch("rv.services.restore.RestoreService.restore") as mock_restore:
            result = runner.invoke(app, ["restore", "base", "work"])
            assert result.exit_code == 0
            mock_restore.assert_called_once_with(
                repo_dir=temp_repo,
                profile_name="base,work",
                identity_path=None,
                interactive=True,
                dry_run=False,
                no_plugins=False,
                parallel=True,
                force_packages=False,
            )


def test_cli_status_multiple_profiles(temp_repo: str) -> None:
    """Tests 'rv status' with multiple profiles."""
    with patch("os.getcwd", return_value=temp_repo):
        with patch("rv.services.status.StatusService.get_status") as mock_status:
            mock_status.return_value = {"drifted": False, "assets": {}}
            result = runner.invoke(app, ["status", "-p", "base", "-p", "work"])
            assert result.exit_code == 0
            mock_status.assert_called_once_with(temp_repo, "base,work", None)


def test_complete_profile_callback(temp_repo: str) -> None:
    """Tests complete_profile autocomplete helper."""
    # Write manifest.yaml with profiles
    manifest_data = {
        "version": 2,
        "assets": [],
        "profiles": {
            "base": {"assets": []},
            "work": {"assets": []},
            "home": {"assets": []},
        },
    }
    with open(os.path.join(temp_repo, "manifest.yaml"), "w") as f:
        import yaml

        yaml.safe_dump(manifest_data, f)

    from rv.cli.main import complete_profile

    with patch("os.getcwd", return_value=temp_repo):
        res = complete_profile(None, "w")
        assert res == ["work"]

        res_all = complete_profile(None, "")
        assert sorted(res_all) == sorted(["base", "work", "home"])
