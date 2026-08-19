"""Test suite for manifest and transaction models."""

import pytest
from pydantic import ValidationError

from rv.models.manifest import Asset, AssetType, ConflictStrategy, Manifest, Secret


def test_asset_validation() -> None:
    # Valid asset
    asset = Asset(
        id="test_zshrc", type=AssetType.SYMLINK, source="assets/zsh/.zshrc", target="~/.zshrc", permissions="0644"
    )
    assert asset.id == "test_zshrc"
    assert asset.permissions == "0644"

    # Invalid permission string
    with pytest.raises(ValidationError) as excinfo:
        Asset(
            id="bad_permissions",
            source="assets/file",
            target="/target/file",
            permissions="644",  # Needs leading 0
        )
    assert "Permissions must be a 4-digit octal string starting with 0" in str(excinfo.value)

    # Path traversal attempt in source
    with pytest.raises(ValidationError) as excinfo:
        Asset(id="traversal", source="../outside/file", target="/target/file")
    assert "must be relative to the repository and not contain path traversal" in str(excinfo.value)


def test_secret_validation() -> None:
    # Valid secret
    secret = Secret(id="ssh_key", source="secrets/id_ed25519.age", target="~/.ssh/id_ed25519", permissions="0600")
    assert secret.encrypted is True
    assert secret.type == AssetType.SECRET

    # Invalid permissions for secret (must restrict group and world)
    with pytest.raises(ValidationError) as excinfo:
        Secret(
            id="insecure_secret",
            source="secrets/key.age",
            target="~/.ssh/key",
            permissions="0644",  # Insecure! Allows group/world reads.
        )
    assert "Secrets must have secure permissions restricting group and world access" in str(excinfo.value)


def test_manifest_validation() -> None:
    # Complete valid manifest dictionary
    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "zshrc",
                "type": "symlink",
                "source": "assets/zshrc",
                "target": "~/.zshrc",
                "permissions": "0644",
                "conflict_strategy": "overwrite",
            }
        ],
        "secrets": [
            {
                "id": "db_pass",
                "type": "secret",
                "source": "secrets/db_pass.age",
                "target": "~/.db_pass",
                "permissions": "0600",
            }
        ],
        "packages": {"brew": ["git", "ripgrep"], "apt": ["curl"], "node": {"version_file": ".nvmrc"}},
        "profiles": {"base": {"assets": ["zshrc"], "secrets": ["db_pass"], "packages": ["brew"]}},
    }

    manifest = Manifest.model_validate(manifest_data)
    assert manifest.version == 2
    assert len(manifest.assets) == 1
    assert manifest.packages.brew == ["git", "ripgrep"]
    assert "base" in manifest.profiles
    assert manifest.profiles["base"].assets == ["zshrc"]
