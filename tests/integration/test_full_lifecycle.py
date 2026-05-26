"""Full end-to-end integration tests for the Revive lifecycle.

These tests run inside Docker containers (Ubuntu, Alpine, Arch) and exercise:
  - Repository init
  - Asset copy, symlink, template restore
  - Secret encryption/decryption end-to-end
  - Rollback on failure
  - Backup → restore roundtrip
  - Workspace registration and sync
  - Package provider availability detection (no actual installs in CI to keep fast)

Prerequisites:
  - The `age` binary must be available in PATH.
  - Python 3.11+ with the rv package installed.
"""

import hashlib
import os
import shutil
import subprocess
import tempfile
from collections.abc import Generator
from pathlib import Path

import pytest
import yaml

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _age_available() -> bool:
    """Returns True if the `age` and `age-keygen` binaries are discoverable."""
    return shutil.which("age") is not None and shutil.which("age-keygen") is not None


def _generate_age_keypair(tmp_dir: str) -> tuple[str, str]:
    """Generates an age keypair and returns (public_key, identity_file_path)."""
    identity_path = os.path.join(tmp_dir, "identity.txt")
    result = subprocess.run(
        ["age-keygen", "-o", identity_path],
        capture_output=True,
        text=True,
        check=True,
    )
    # Public key is printed to stderr by age-keygen
    public_key = ""
    for line in result.stderr.splitlines():
        if line.startswith("Public key:"):
            public_key = line.split(":", 1)[1].strip()
            break
    if not public_key:
        # Parse from the identity file comment
        with open(identity_path) as f:
            for line in f:
                if line.startswith("# public key:"):
                    public_key = line.split(":", 1)[1].strip()
                    break
    assert public_key, "Failed to extract public key from age-keygen output"
    os.chmod(identity_path, 0o600)
    return public_key, identity_path


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture()
def repo_dir() -> Generator[str, None, None]:
    """Creates a temporary revive repository with a basic manifest."""
    tmp = tempfile.mkdtemp(prefix="rv_integration_")
    try:
        yield tmp
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


@pytest.fixture()
def system_dir() -> Generator[str, None, None]:
    """Creates a temporary directory simulating the target system paths."""
    tmp = tempfile.mkdtemp(prefix="rv_system_")
    try:
        yield tmp
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


# ---------------------------------------------------------------------------
# Test: Manifest loading and profile resolution
# ---------------------------------------------------------------------------


class TestManifestLifecycle:
    """Tests for manifest loading and profile resolution."""

    def test_manifest_load_valid(self, repo_dir: str) -> None:
        """Valid manifest.yaml loads and validates cleanly."""
        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "zshrc",
                    "type": "copy",
                    "source": "assets/zshrc",
                    "target": f"{repo_dir}/system/zshrc",
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {
                "base": {
                    "assets": ["zshrc"],
                    "packages": [],
                }
            },
        }
        manifest_path = os.path.join(repo_dir, "manifest.yaml")
        with open(manifest_path, "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.restore import ManifestLoader, ProfileResolver

        manifest = ManifestLoader.load(manifest_path)
        assert manifest.version == 2
        assert len(manifest.assets) == 1
        assert manifest.assets[0].id == "zshrc"

        resolved = ProfileResolver.resolve(manifest, "base")
        assert "zshrc" in resolved.assets

    def test_manifest_invalid_version_raises(self, repo_dir: str) -> None:
        """Manifest with unsupported schema version raises UnsupportedSchemaVersionError."""
        manifest_path = os.path.join(repo_dir, "manifest.yaml")
        with open(manifest_path, "w") as f:
            yaml.dump({"version": 99, "profiles": {}}, f)

        from rv.models.manifest import UnsupportedSchemaVersionError
        from rv.services.restore import ManifestLoader

        with pytest.raises(UnsupportedSchemaVersionError, match="Unsupported manifest schema version"):
            ManifestLoader.load(manifest_path)

    def test_cyclic_profile_inheritance_raises(self, repo_dir: str) -> None:
        """Cyclic profile inheritance is detected and raises ValueError."""
        manifest_content = {
            "version": 2,
            "assets": [],
            "secrets": [],
            "packages": {},
            "profiles": {
                "a": {"extends": ["b"], "assets": [], "packages": []},
                "b": {"extends": ["a"], "assets": [], "packages": []},
            },
        }
        manifest_path = os.path.join(repo_dir, "manifest.yaml")
        with open(manifest_path, "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.restore import ManifestLoader, ProfileResolver

        manifest = ManifestLoader.load(manifest_path)
        with pytest.raises(ValueError, match="Cyclic"):
            ProfileResolver.resolve(manifest, "a")


# ---------------------------------------------------------------------------
# Test: Asset copy restore
# ---------------------------------------------------------------------------


class TestAssetCopyRestore:
    """Integration tests for copy-type asset restoration."""

    def test_copy_asset_restored(self, repo_dir: str, system_dir: str) -> None:
        """Copy asset is written to target with correct content and permissions."""
        # Create source asset
        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir, exist_ok=True)
        source_content = b"# my zshrc\nexport PATH=$HOME/bin:$PATH\n"
        source_path = os.path.join(assets_dir, "zshrc")
        with open(source_path, "wb") as f:
            f.write(source_content)

        target_path = os.path.join(system_dir, "zshrc")

        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "zshrc",
                    "type": "copy",
                    "source": "assets/zshrc",
                    "target": target_path,
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": ["zshrc"], "packages": []}},
        }
        manifest_path = os.path.join(repo_dir, "manifest.yaml")
        with open(manifest_path, "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.restore import RestoreService

        tx_id = RestoreService.restore(
            repo_dir=repo_dir,
            profile_name="base",
            interactive=False,
            dry_run=False,
        )
        assert tx_id

        assert os.path.isfile(target_path)
        with open(target_path, "rb") as f:
            assert f.read() == source_content

        actual_perms = oct(os.stat(target_path).st_mode & 0o7777)
        assert actual_perms == "0o644"

    def test_copy_overwrite_existing(self, repo_dir: str, system_dir: str) -> None:
        """Existing target is overwritten when conflict_strategy is 'overwrite'."""
        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir)
        new_content = b"new content\n"
        with open(os.path.join(assets_dir, "cfg"), "wb") as f:
            f.write(new_content)

        target_path = os.path.join(system_dir, "cfg")
        with open(target_path, "w") as f:
            f.write("old content\n")

        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "cfg",
                    "type": "copy",
                    "source": "assets/cfg",
                    "target": target_path,
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": ["cfg"], "packages": []}},
        }
        with open(os.path.join(repo_dir, "manifest.yaml"), "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.restore import RestoreService

        RestoreService.restore(repo_dir=repo_dir, profile_name="base", interactive=False, dry_run=False)

        with open(target_path, "rb") as f:
            assert f.read() == new_content

    def test_dry_run_does_not_mutate(self, repo_dir: str, system_dir: str) -> None:
        """Dry-run mode returns a transaction ID but does not write the target."""
        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir)
        with open(os.path.join(assets_dir, "file"), "w") as f:
            f.write("content")

        target_path = os.path.join(system_dir, "file")

        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "file",
                    "type": "copy",
                    "source": "assets/file",
                    "target": target_path,
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": ["file"], "packages": []}},
        }
        with open(os.path.join(repo_dir, "manifest.yaml"), "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.restore import RestoreService

        tx_id = RestoreService.restore(repo_dir=repo_dir, profile_name="base", interactive=False, dry_run=True)
        assert tx_id
        assert not os.path.exists(target_path), "Dry run must not create target file"


# ---------------------------------------------------------------------------
# Test: Symlink restore
# ---------------------------------------------------------------------------


class TestSymlinkRestore:
    """Integration tests for symlink-type asset restoration."""

    def test_symlink_created(self, repo_dir: str, system_dir: str) -> None:
        """Symlink asset creates a symlink pointing to the source path."""
        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir)
        source_path = os.path.join(assets_dir, "vimrc")
        with open(source_path, "w") as f:
            f.write("set nu\n")

        target_path = os.path.join(system_dir, ".vimrc")

        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "vimrc",
                    "type": "symlink",
                    "source": "assets/vimrc",
                    "target": target_path,
                    "conflict_strategy": "overwrite",
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": ["vimrc"], "packages": []}},
        }
        with open(os.path.join(repo_dir, "manifest.yaml"), "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.restore import RestoreService

        RestoreService.restore(repo_dir=repo_dir, profile_name="base", interactive=False, dry_run=False)

        assert os.path.islink(target_path)
        assert os.readlink(target_path) == source_path


# ---------------------------------------------------------------------------
# Test: Template restore
# ---------------------------------------------------------------------------


class TestTemplateRestore:
    """Integration tests for Jinja2 template rendering and deployment."""

    def test_template_renders_builtin_vars(self, repo_dir: str, system_dir: str) -> None:
        """Built-in template variables (_hostname, _user, _platform) render correctly."""
        import getpass
        import platform
        import socket
        import sys

        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir)
        template_content = "host={{ _hostname }}\nuser={{ _user }}\nplatform={{ _platform }}\n"
        with open(os.path.join(assets_dir, "env.tmpl"), "w") as f:
            f.write(template_content)

        target_path = os.path.join(system_dir, "env.conf")
        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "env_conf",
                    "type": "template",
                    "source": "assets/env.tmpl",
                    "target": target_path,
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": ["env_conf"], "packages": []}},
        }
        with open(os.path.join(repo_dir, "manifest.yaml"), "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.restore import RestoreService

        RestoreService.restore(repo_dir=repo_dir, profile_name="base", interactive=False, dry_run=False)

        with open(target_path) as f:
            rendered = f.read()

        assert f"host={socket.gethostname()}" in rendered
        assert f"user={getpass.getuser()}" in rendered
        assert f"platform={sys.platform}" in rendered

    def test_template_user_vars_override_builtins(self, repo_dir: str, system_dir: str) -> None:
        """User-defined template_vars override built-in variables with same name."""
        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir)
        with open(os.path.join(assets_dir, "t.tmpl"), "w") as f:
            f.write("{{ _hostname }}")

        target_path = os.path.join(system_dir, "out")
        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "t",
                    "type": "template",
                    "source": "assets/t.tmpl",
                    "target": target_path,
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                    "template_vars": {"_hostname": "custom-host"},
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": ["t"], "packages": []}},
        }
        with open(os.path.join(repo_dir, "manifest.yaml"), "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.restore import RestoreService

        RestoreService.restore(repo_dir=repo_dir, profile_name="base", interactive=False, dry_run=False)

        with open(target_path) as f:
            assert f.read() == "custom-host"


# ---------------------------------------------------------------------------
# Test: Secret encryption / decryption end-to-end
# ---------------------------------------------------------------------------


@pytest.mark.skipif(not _age_available(), reason="age binary not available")
class TestSecretLifecycle:
    """End-to-end tests for secret encryption and decryption via age."""

    def test_secret_encrypt_decrypt_roundtrip(self, repo_dir: str, system_dir: str) -> None:
        """Secret encrypted with age key is correctly decrypted and deployed to target."""
        import subprocess as _sp

        from rv.security.encryptor import AgeEncryptor

        key_dir = tempfile.mkdtemp(prefix="rv_keys_")
        try:
            public_key, identity_path = _generate_age_keypair(key_dir)

            # Write plaintext secret
            secrets_dir = os.path.join(repo_dir, "secrets")
            os.makedirs(secrets_dir)
            plaintext = b"MY_SECRET_TOKEN=abc123xyz\n"
            plain_path = os.path.join(secrets_dir, "token.txt")
            encrypted_path = os.path.join(secrets_dir, "token.age")
            with open(plain_path, "wb") as f:
                f.write(plaintext)

            AgeEncryptor.encrypt_file(plain_path, encrypted_path, [public_key])
            os.unlink(plain_path)

            target_path = os.path.join(system_dir, ".token")
            manifest_content = {
                "version": 2,
                "assets": [],
                "secrets": [
                    {
                        "id": "token",
                        "source": "secrets/token.age",
                        "target": target_path,
                        "permissions": "0600",
                    }
                ],
                "packages": {},
                "profiles": {"base": {"assets": [], "secrets": ["token"], "packages": []}},
            }
            with open(os.path.join(repo_dir, "manifest.yaml"), "w") as f:
                yaml.dump(manifest_content, f)

            from rv.services.restore import RestoreService

            RestoreService.restore(
                repo_dir=repo_dir,
                profile_name="base",
                identity_path=identity_path,
                interactive=False,
                dry_run=False,
            )

            assert os.path.isfile(target_path)
            actual_perms = oct(os.stat(target_path).st_mode & 0o7777)
            assert actual_perms == "0o600", f"Secret permissions should be 0600, got {actual_perms}"
            with open(target_path, "rb") as f:
                assert f.read() == plaintext
        finally:
            shutil.rmtree(key_dir, ignore_errors=True)


# ---------------------------------------------------------------------------
# Test: Transaction rollback
# ---------------------------------------------------------------------------


class TestTransactionRollback:
    """Tests verifying that the system state is preserved on restore failure."""

    def test_rollback_restores_original_file(self, repo_dir: str, system_dir: str) -> None:
        """If a restore step fails, the original target file is restored."""
        from unittest.mock import patch

        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir)
        with open(os.path.join(assets_dir, "cfg"), "w") as f:
            f.write("new content")

        original_content = b"original content\n"
        target_path = os.path.join(system_dir, "cfg")
        with open(target_path, "wb") as f:
            f.write(original_content)

        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "cfg",
                    "type": "copy",
                    "source": "assets/cfg",
                    "target": target_path,
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": ["cfg"], "packages": []}},
        }
        with open(os.path.join(repo_dir, "manifest.yaml"), "w") as f:
            yaml.dump(manifest_content, f)

        from rv.transactions.context import TransactionContext

        # Patch verify() to force failure after execute()
        with patch.object(TransactionContext, "verify", side_effect=RuntimeError("injected failure")):
            from rv.services.restore import RestoreService

            with pytest.raises(RuntimeError):
                RestoreService.restore(
                    repo_dir=repo_dir,
                    profile_name="base",
                    interactive=False,
                    dry_run=False,
                )

        # Original content must be restored
        with open(target_path, "rb") as f:
            assert f.read() == original_content


# ---------------------------------------------------------------------------
# Test: Backup → restore roundtrip
# ---------------------------------------------------------------------------


class TestBackupRestoreRoundtrip:
    """Tests verifying that BackupService captures live state correctly."""

    def test_backup_captures_copy_asset(self, repo_dir: str, system_dir: str) -> None:
        """BackupService copies the live system file back into the repository."""
        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir)

        # Original source in repo
        original_content = b"original from repo\n"
        with open(os.path.join(assets_dir, "cfg"), "wb") as f:
            f.write(original_content)

        target_path = os.path.join(system_dir, "cfg")

        # Deploy to system first
        from rv.transactions.atomic import AtomicWrite

        AtomicWrite.write(target_path, original_content)

        # Modify live system file
        modified_content = b"modified on system\n"
        with open(target_path, "wb") as f:
            f.write(modified_content)

        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "cfg",
                    "type": "copy",
                    "source": "assets/cfg",
                    "target": target_path,
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": ["cfg"], "packages": []}},
        }
        manifest_path = os.path.join(repo_dir, "manifest.yaml")
        with open(manifest_path, "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.backup import BackupService

        BackupService.backup(repo_dir=repo_dir, profile_name="base", dry_run=False)

        # Repo source should now reflect the modified system content
        with open(os.path.join(assets_dir, "cfg"), "rb") as f:
            assert f.read() == modified_content


# ---------------------------------------------------------------------------
# Test: Parallel vs sequential asset planning
# ---------------------------------------------------------------------------


class TestParallelPlanning:
    """Tests verifying parallel and sequential planning produce identical results."""

    def test_parallel_and_sequential_identical_targets(self, repo_dir: str, system_dir: str) -> None:
        """Parallel and sequential restore produce the same target files."""
        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir)

        asset_entries = []
        for i in range(5):
            fname = f"file{i}"
            with open(os.path.join(assets_dir, fname), "w") as f:
                f.write(f"content{i}\n")
            target = os.path.join(system_dir, fname)
            asset_entries.append(
                {
                    "id": fname,
                    "type": "copy",
                    "source": f"assets/{fname}",
                    "target": target,
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                }
            )

        manifest_content = {
            "version": 2,
            "assets": asset_entries,
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": [e["id"] for e in asset_entries], "packages": []}},
        }

        # Test sequential
        seq_dir = tempfile.mkdtemp(prefix="rv_seq_")
        try:
            # Rewrite targets to seq_dir
            for entry in manifest_content["assets"]:  # type: ignore[index]
                entry["target"] = entry["target"].replace(system_dir, seq_dir)  # type: ignore[index]
            seq_manifest = os.path.join(repo_dir, "manifest.yaml")
            with open(seq_manifest, "w") as f:
                yaml.dump(manifest_content, f)

            from rv.services.restore import RestoreService

            RestoreService.restore(
                repo_dir=repo_dir, profile_name="base", interactive=False, dry_run=False, parallel=False
            )

            seq_contents = {}
            for i in range(5):
                p = os.path.join(seq_dir, f"file{i}")
                with open(p) as f:
                    seq_contents[f"file{i}"] = f.read()
        finally:
            shutil.rmtree(seq_dir, ignore_errors=True)

        # Now parallel into par_dir
        par_dir = tempfile.mkdtemp(prefix="rv_par_")
        try:
            for entry in manifest_content["assets"]:  # type: ignore[index]
                entry["target"] = entry["target"].replace(seq_dir, par_dir)  # type: ignore[index]
            with open(seq_manifest, "w") as f:
                yaml.dump(manifest_content, f)

            RestoreService.restore(
                repo_dir=repo_dir, profile_name="base", interactive=False, dry_run=False, parallel=True
            )

            for i in range(5):
                p = os.path.join(par_dir, f"file{i}")
                with open(p) as f:
                    assert f.read() == seq_contents[f"file{i}"], f"Parallel result differs for file{i}"
        finally:
            shutil.rmtree(par_dir, ignore_errors=True)


# ---------------------------------------------------------------------------
# Test: Per-asset hooks
# ---------------------------------------------------------------------------


class TestPerAssetHooks:
    """Tests verifying per-asset pre/post hook execution."""

    def test_pre_hook_command_executes(self, repo_dir: str, system_dir: str) -> None:
        """Pre-hook shell command runs before asset mutation."""
        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir)
        with open(os.path.join(assets_dir, "cfg"), "w") as f:
            f.write("content")

        hook_marker = os.path.join(system_dir, "pre_hook_ran")
        target_path = os.path.join(system_dir, "cfg")

        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "cfg",
                    "type": "copy",
                    "source": "assets/cfg",
                    "target": target_path,
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                    "hooks": {
                        "pre": [{"command": f"touch {hook_marker}"}],
                        "post": [],
                    },
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": ["cfg"], "packages": []}},
        }
        with open(os.path.join(repo_dir, "manifest.yaml"), "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.restore import RestoreService

        RestoreService.restore(repo_dir=repo_dir, profile_name="base", interactive=False, dry_run=False)

        assert os.path.exists(hook_marker), "Pre-hook command must have executed"

    def test_post_hook_command_executes(self, repo_dir: str, system_dir: str) -> None:
        """Post-hook shell command runs after asset mutation."""
        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir)
        with open(os.path.join(assets_dir, "cfg"), "w") as f:
            f.write("content")

        hook_marker = os.path.join(system_dir, "post_hook_ran")
        target_path = os.path.join(system_dir, "cfg")

        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "cfg",
                    "type": "copy",
                    "source": "assets/cfg",
                    "target": target_path,
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                    "hooks": {
                        "pre": [],
                        "post": [{"command": f"touch {hook_marker}"}],
                    },
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": ["cfg"], "packages": []}},
        }
        with open(os.path.join(repo_dir, "manifest.yaml"), "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.restore import RestoreService

        RestoreService.restore(repo_dir=repo_dir, profile_name="base", interactive=False, dry_run=False)

        assert os.path.exists(hook_marker), "Post-hook command must have executed"

    def test_failing_pre_hook_raises(self, repo_dir: str, system_dir: str) -> None:
        """A failing pre-hook command raises AssetHandlerError."""
        assets_dir = os.path.join(repo_dir, "assets")
        os.makedirs(assets_dir)
        with open(os.path.join(assets_dir, "cfg"), "w") as f:
            f.write("content")

        target_path = os.path.join(system_dir, "cfg")

        manifest_content = {
            "version": 2,
            "assets": [
                {
                    "id": "cfg",
                    "type": "copy",
                    "source": "assets/cfg",
                    "target": target_path,
                    "permissions": "0644",
                    "conflict_strategy": "overwrite",
                    "hooks": {
                        "pre": [{"command": "false"}],  # always exits 1
                        "post": [],
                    },
                }
            ],
            "secrets": [],
            "packages": {},
            "profiles": {"base": {"assets": ["cfg"], "packages": []}},
        }
        with open(os.path.join(repo_dir, "manifest.yaml"), "w") as f:
            yaml.dump(manifest_content, f)

        from rv.services.restore import RestoreService

        with pytest.raises(RuntimeError):
            RestoreService.restore(repo_dir=repo_dir, profile_name="base", interactive=False, dry_run=False)


# ---------------------------------------------------------------------------
# Test: Provider availability (smoke — no actual installs)
# ---------------------------------------------------------------------------


class TestProviderAvailability:
    """Smoke tests verifying that provider is_available() doesn't raise."""

    def test_apt_availability_check(self) -> None:
        from rv.providers.apt import AptProvider

        result = AptProvider().is_available()
        assert isinstance(result, bool)

    def test_brew_availability_check(self) -> None:
        from rv.providers.brew import BrewProvider

        result = BrewProvider().is_available()
        assert isinstance(result, bool)

    def test_pacman_availability_check(self) -> None:
        from rv.providers.pacman import PacmanProvider

        result = PacmanProvider().is_available()
        assert isinstance(result, bool)

    def test_dnf_availability_check(self) -> None:
        from rv.providers.dnf import DnfProvider

        result = DnfProvider().is_available()
        assert isinstance(result, bool)

    def test_nix_availability_check(self) -> None:
        from rv.providers.nix import NixProvider

        result = NixProvider().is_available()
        assert isinstance(result, bool)

    def test_cargo_availability_check(self) -> None:
        from rv.providers.cargo import CargoProvider

        result = CargoProvider().is_available()
        assert isinstance(result, bool)

    def test_pip_availability_check(self) -> None:
        from rv.providers.pip import PipProvider

        result = PipProvider().is_available()
        assert isinstance(result, bool)
