"""Test suite for package providers (Base, Brew, Apt, Flatpak, Snap, Docker, Node)."""

import os
import shutil
import tempfile
import subprocess
import pytest
from unittest.mock import patch, MagicMock

from rv.providers import (
    BaseProvider,
    ProviderError,
    BrewProvider,
    AptProvider,
    FlatpakProvider,
    SnapProvider,
    DockerProvider,
    NodeProvider,
)


def test_base_provider_available() -> None:
    """Tests BaseProvider availability checks."""
    provider = BaseProvider("nonexistent_cmd")
    assert not provider.is_available()

    with patch("shutil.which", return_value="/usr/bin/git"):
        git_provider = BaseProvider("git")
        assert git_provider.is_available()


def test_base_provider_retry_success() -> None:
    """Tests BaseProvider execute_with_retry under success scenario."""
    provider = BaseProvider("test")
    mock_run = MagicMock(return_value=subprocess.CompletedProcess(["ls"], 0, stdout="success"))

    with patch("subprocess.run", mock_run):
        res = provider.execute_with_retry(["ls"])
        assert res.returncode == 0
        assert res.stdout == "success"
        mock_run.assert_called_once()


def test_base_provider_retry_fails_and_retries() -> None:
    """Tests BaseProvider retry mechanism on CalledProcessError."""
    provider = BaseProvider("test")

    # 3 failures, should raise ProviderError after 3 tries
    mock_run = MagicMock(side_effect=subprocess.CalledProcessError(1, "cmd", stderr="error message"))

    with patch("subprocess.run", mock_run), patch("time.sleep") as mock_sleep:
        with pytest.raises(ProviderError) as excinfo:
            provider.execute_with_retry(["bad_cmd"], retries=3)

        assert "Failed to execute command after 3 attempts" in str(excinfo.value)
        assert mock_run.call_count == 3
        assert mock_sleep.call_count == 2


def test_brew_provider() -> None:
    """Tests BrewProvider Brewfile bundling."""
    provider = BrewProvider()

    # 1. Empty package list
    provider.install([])

    # 2. Not available check
    with patch("rv.providers.brew.BrewProvider.is_available", return_value=False):
        with pytest.raises(ProviderError, match="Homebrew .* is not installed"):
            provider.install(["git"])

    # 3. Dry run
    with patch("rv.providers.brew.BrewProvider.is_available", return_value=True):
        provider.install(["git", "cask:visual-studio-code", "tap:homebrew/cask"], dry_run=True)

    # 4. Successful execution
    with (
        patch("rv.providers.brew.BrewProvider.is_available", return_value=True),
        patch("rv.providers.brew.BrewProvider.execute_with_retry") as mock_exec,
        patch("rv.security.tempfile.SecureTempFile.file") as mock_temp,
    ):
        # Mock temp file context manager
        temp_dir = tempfile.mkdtemp()
        temp_file = os.path.join(temp_dir, "Brewfile")
        with open(temp_file, "w") as f:
            f.write("")
        mock_temp.return_value.__enter__.return_value = temp_file

        provider.install(["git", "cask:code"])
        mock_exec.assert_called_once_with(["brew", "bundle", "--file", temp_file])

        # Verify Brewfile contents
        with open(temp_file, "r") as f:
            content = f.read()
            assert 'brew "git"' in content
            assert 'cask "code"' in content

        shutil.rmtree(temp_dir)


def test_apt_provider() -> None:
    """Tests AptProvider dpkg query and apt-get install."""
    provider = AptProvider()

    # 1. Empty list
    provider.install([])

    # 2. Not available check
    with patch("rv.providers.apt.AptProvider.is_available", return_value=False):
        with pytest.raises(ProviderError, match="apt-get or dpkg is not available"):
            provider.install(["curl"])

    # 3. Packages already installed
    mock_dpkg_ok = subprocess.CompletedProcess(["dpkg", "-s", "curl"], 0, stdout="Status: install ok installed")
    with (
        patch("rv.providers.apt.AptProvider.is_available", return_value=True),
        patch("subprocess.run", return_value=mock_dpkg_ok),
        patch("rv.providers.apt.AptProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["curl"])
        mock_exec.assert_not_called()

    # 4. Packages missing & installed successfully
    mock_dpkg_fail = subprocess.CompletedProcess(["dpkg", "-s", "git"], 1, stdout="not installed")
    with (
        patch("rv.providers.apt.AptProvider.is_available", return_value=True),
        patch("subprocess.run", return_value=mock_dpkg_fail),
        patch("rv.providers.apt.AptProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["git"])
        mock_exec.assert_called_once_with(["apt-get", "install", "-y", "git"])

    # 5. Dry run
    with (
        patch("rv.providers.apt.AptProvider.is_available", return_value=True),
        patch("subprocess.run", return_value=mock_dpkg_fail),
    ):
        provider.install(["git"], dry_run=True)


def test_flatpak_provider() -> None:
    """Tests FlatpakProvider info check and install."""
    provider = FlatpakProvider()

    # 1. Empty list
    provider.install([])

    # 2. Not available check
    with patch("rv.providers.flatpak.FlatpakProvider.is_available", return_value=False):
        with pytest.raises(ProviderError, match="flatpak CLI is not installed"):
            provider.install(["org.gimp.GIMP"])

    # 3. Already installed
    mock_info_ok = subprocess.CompletedProcess(["flatpak", "info", "ref"], 0)
    with (
        patch("rv.providers.flatpak.FlatpakProvider.is_available", return_value=True),
        patch("subprocess.run", return_value=mock_info_ok),
        patch("rv.providers.flatpak.FlatpakProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["org.gimp.GIMP"])
        mock_exec.assert_not_called()

    # 4. Missing install
    mock_info_fail = subprocess.CompletedProcess(["flatpak", "info", "ref"], 1)
    with (
        patch("rv.providers.flatpak.FlatpakProvider.is_available", return_value=True),
        patch("subprocess.run", return_value=mock_info_fail),
        patch("rv.providers.flatpak.FlatpakProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["org.gimp.GIMP"])
        mock_exec.assert_called_once_with(["flatpak", "install", "-y", "org.gimp.GIMP"])


def test_snap_provider() -> None:
    """Tests SnapProvider snap list and snap install classic checks."""
    provider = SnapProvider()

    # 1. Empty list
    provider.install([])

    # 2. Not available check
    with patch("rv.providers.snap.SnapProvider.is_available", return_value=False):
        with pytest.raises(ProviderError, match="snap CLI is not installed"):
            provider.install(["code"])

    # 3. Already installed
    mock_snap_ok = subprocess.CompletedProcess(["snap", "list", "code"], 0)
    with (
        patch("rv.providers.snap.SnapProvider.is_available", return_value=True),
        patch("subprocess.run", return_value=mock_snap_ok),
        patch("rv.providers.snap.SnapProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["code"])
        mock_exec.assert_not_called()

    # 4. Missing snap classic install
    mock_snap_fail = subprocess.CompletedProcess(["snap", "list", "code"], 1)
    with (
        patch("rv.providers.snap.SnapProvider.is_available", return_value=True),
        patch("subprocess.run", return_value=mock_snap_fail),
        patch("rv.providers.snap.SnapProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["classic:code"])
        mock_exec.assert_called_once_with(["snap", "install", "code", "--classic"])


def test_docker_provider() -> None:
    """Tests DockerProvider pull commands."""
    provider = DockerProvider()

    # 1. Empty list
    provider.install([])

    # 2. Not available check
    with patch("rv.providers.docker.DockerProvider.is_available", return_value=False):
        with pytest.raises(ProviderError, match="Docker CLI .* is not installed"):
            provider.install(["postgres:latest"])

    # 3. Already present
    mock_inspect_ok = subprocess.CompletedProcess(["docker", "image", "inspect", "postgres"], 0)
    with (
        patch("rv.providers.docker.DockerProvider.is_available", return_value=True),
        patch("subprocess.run", return_value=mock_inspect_ok),
        patch("rv.providers.docker.DockerProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["postgres:latest"])
        mock_exec.assert_not_called()

    # 4. Missing image pull
    mock_inspect_fail = subprocess.CompletedProcess(["docker", "image", "inspect", "postgres"], 1)
    with (
        patch("rv.providers.docker.DockerProvider.is_available", return_value=True),
        patch("subprocess.run", return_value=mock_inspect_fail),
        patch("rv.providers.docker.DockerProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["postgres:latest"])
        mock_exec.assert_called_once_with(["docker", "pull", "postgres:latest"])


def test_node_provider(tmp_path: str) -> None:
    """Tests NodeProvider version checking and installation."""
    provider = NodeProvider()

    # 1. No version target
    provider.install_node(str(tmp_path), None, None)

    # 2. Current version matches
    mock_node_version = subprocess.CompletedProcess(["node", "-v"], 0, stdout="v20.11.0\n")
    with (
        patch("shutil.which", return_value="/usr/bin/node"),
        patch("subprocess.run", return_value=mock_node_version) as mock_run,
    ):
        provider.install_node(str(tmp_path), "20.11.0", None)
        # Verify it didn't trigger any installation because it matches
        mock_run.assert_called_once_with(["node", "-v"], capture_output=True, text=True, check=True)

    # 3. Version mismatch but dry run
    with patch("shutil.which", return_value="/usr/bin/node"), patch("subprocess.run", return_value=mock_node_version):
        provider.install_node(str(tmp_path), "22.0.0", None, dry_run=True)

    # 4. Target version from file
    version_file = tmp_path / ".nvmrc"
    version_file.write_text("v18.15.0\n")
    with patch("shutil.which", return_value="/usr/bin/node"), patch("subprocess.run", return_value=mock_node_version):
        target = provider._resolve_target_version(str(tmp_path), None, ".nvmrc")
        assert target == "18.15.0"

    # 5. Mismatch, install via fnm
    with (
        patch("shutil.which") as mock_which,
        patch("subprocess.run", return_value=mock_node_version),
        patch("rv.providers.node.NodeProvider.execute_with_retry") as mock_exec,
    ):
        # Mock which to find fnm, but node matches a mismatch version 22.0.0
        mock_which.side_effect = lambda x: "/usr/bin/fnm" if x == "fnm" else ("/usr/bin/node" if x == "node" else None)

        provider.install_node(str(tmp_path), "22.0.0", None)
        mock_exec.assert_called_once_with(["fnm", "install", "22.0.0"])

    # 6. Mismatch, install via nvm.sh script fallback
    with patch("shutil.which") as mock_which, patch("subprocess.run") as mock_sub_run:
        # fnm is missing, node is available
        mock_which.side_effect = lambda x: "/usr/bin/node" if x == "node" else None

        # node -v returns v20.11.0
        mock_sub_run.side_effect = [
            subprocess.CompletedProcess(["node", "-v"], 0, stdout="v20.11.0\n"),  # current node -v
            subprocess.CompletedProcess(["bash"], 0),  # nvm install output
        ]

        with patch("os.path.exists", return_value=True):
            provider.install_node(str(tmp_path), "18.15.0", None)

            # Check the second subprocess call was nvm.sh script sourcing
            args, kwargs = mock_sub_run.call_args
            assert "nvm.sh" in args[0][2]
            assert "nvm install 18.15.0" in args[0][2]

    # 7. Mismatch, no fnm or nvm available raises ProviderError
    with (
        patch("shutil.which") as mock_which,
        patch("subprocess.run", return_value=mock_node_version),
        patch("os.path.exists", return_value=False),
    ):
        mock_which.side_effect = lambda x: "/usr/bin/node" if x == "node" else None

        with pytest.raises(ProviderError, match="Node.js version mismatch .* and no Node.js managers"):
            provider.install_node(str(tmp_path), "22.0.0", None)
