"""Test suite for package providers (Base, Brew, Apt, Flatpak, Snap, Docker, Node,
Cargo, Dnf, Nix, Pacman, Pip)."""

import os
import shutil
import subprocess
import tempfile
from unittest.mock import MagicMock, call, patch

import pytest

from rv.providers import (
    AptProvider,
    BaseProvider,
    BrewProvider,
    CargoProvider,
    DnfProvider,
    DockerProvider,
    FlatpakProvider,
    NixProvider,
    NodeProvider,
    PacmanProvider,
    PipProvider,
    ProviderError,
    SnapProvider,
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
        with open(temp_file) as f:
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
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.base.PackageCache.mark_installed"),
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
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.base.PackageCache.mark_installed"),
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
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.base.PackageCache.mark_installed"),
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


# =============================================================================
# CargoProvider
# =============================================================================


def test_cargo_is_available_true() -> None:
    """CargoProvider.is_available returns True when cargo is on PATH."""
    provider = CargoProvider()
    with patch("shutil.which", return_value="/usr/bin/cargo"):
        assert provider.is_available() is True


def test_cargo_is_available_false() -> None:
    """CargoProvider.is_available returns False when cargo is absent."""
    provider = CargoProvider()
    with patch("shutil.which", return_value=None):
        assert provider.is_available() is False


def test_cargo_is_installed_found() -> None:
    """CargoProvider.is_installed returns True when package appears in 'cargo install --list'."""
    provider = CargoProvider()
    mock_result = subprocess.CompletedProcess(
        ["cargo", "install", "--list"], 0, stdout="ripgrep v14.1.0:\n    /home/user/.cargo/bin/rg\n"
    )
    with patch("subprocess.run", return_value=mock_result):
        assert provider.is_installed("ripgrep") is True


def test_cargo_is_installed_not_found() -> None:
    """CargoProvider.is_installed returns False when package not in list."""
    provider = CargoProvider()
    mock_result = subprocess.CompletedProcess(["cargo", "install", "--list"], 0, stdout="other-crate v1.0.0:\n")
    with patch("subprocess.run", return_value=mock_result):
        assert provider.is_installed("ripgrep") is False


def test_cargo_is_installed_command_error() -> None:
    """CargoProvider.is_installed returns False when subprocess raises."""
    provider = CargoProvider()
    with patch("subprocess.run", side_effect=OSError("cargo not found")):
        assert provider.is_installed("ripgrep") is False


def test_cargo_is_installed_nonzero_returncode() -> None:
    """CargoProvider.is_installed returns False on non-zero returncode."""
    provider = CargoProvider()
    mock_result = subprocess.CompletedProcess(["cargo", "install", "--list"], 1, stdout="")
    with patch("subprocess.run", return_value=mock_result):
        assert provider.is_installed("ripgrep") is False


def test_cargo_install_empty_list() -> None:
    """CargoProvider.install is a no-op for empty package list."""
    provider = CargoProvider()
    provider.install([])  # Must not raise


def test_cargo_install_not_available() -> None:
    """CargoProvider.install raises ProviderError when cargo is not on PATH."""
    provider = CargoProvider()
    with patch("rv.providers.cargo.CargoProvider.is_available", return_value=False):
        with pytest.raises(ProviderError, match="cargo is not available"):
            provider.install(["ripgrep"])


def test_cargo_install_dry_run() -> None:
    """CargoProvider.install in dry_run mode logs without invoking subprocess."""
    provider = CargoProvider()
    with (
        patch("rv.providers.cargo.CargoProvider.is_available", return_value=True),
        patch("rv.providers.cargo.CargoProvider.execute_with_retry") as mock_exec,
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.cargo.CargoProvider.is_installed", return_value=False),
    ):
        provider.install(["ripgrep"], dry_run=True)
        mock_exec.assert_not_called()


def test_cargo_install_all_cached() -> None:
    """CargoProvider.install skips execution when all packages are in cache."""
    provider = CargoProvider()
    with (
        patch("rv.providers.cargo.CargoProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=True),
        patch("rv.providers.cargo.CargoProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["ripgrep"], use_cache=True)
        mock_exec.assert_not_called()


def test_cargo_install_success() -> None:
    """CargoProvider.install calls 'cargo install <pkgs>' and marks cache."""
    provider = CargoProvider()
    with (
        patch("rv.providers.cargo.CargoProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.cargo.CargoProvider.is_installed", return_value=False),
        patch("rv.providers.cargo.CargoProvider.execute_with_retry") as mock_exec,
        patch("rv.providers.base.PackageCache.mark_installed") as mock_mark,
    ):
        provider.install(["ripgrep", "fd-find"])
        mock_exec.assert_called_once_with(["cargo", "install", "ripgrep", "fd-find"])
        mock_mark.assert_called_once_with("cargo", ["ripgrep", "fd-find"])


def test_cargo_install_failure() -> None:
    """CargoProvider.install raises ProviderError when execute_with_retry fails."""
    provider = CargoProvider()
    with (
        patch("rv.providers.cargo.CargoProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.cargo.CargoProvider.is_installed", return_value=False),
        patch("rv.providers.cargo.CargoProvider.execute_with_retry", side_effect=ProviderError("build failed")),
    ):
        with pytest.raises(ProviderError, match="Cargo installation failed"):
            provider.install(["ripgrep"])


# =============================================================================
# DnfProvider
# =============================================================================


def test_dnf_is_installed_found() -> None:
    """DnfProvider.is_installed returns True when rpm -q succeeds."""
    provider = DnfProvider()
    mock_result = subprocess.CompletedProcess(["rpm", "-q", "git"], 0, stdout="git-2.43.0\n")
    with patch("subprocess.run", return_value=mock_result):
        assert provider.is_installed("git") is True


def test_dnf_is_installed_not_found() -> None:
    """DnfProvider.is_installed returns False when rpm -q fails."""
    provider = DnfProvider()
    mock_result = subprocess.CompletedProcess(["rpm", "-q", "git"], 1, stdout="package git is not installed\n")
    with patch("subprocess.run", return_value=mock_result):
        assert provider.is_installed("git") is False


def test_dnf_is_installed_exception() -> None:
    """DnfProvider.is_installed returns False when subprocess raises."""
    provider = DnfProvider()
    with patch("subprocess.run", side_effect=FileNotFoundError("rpm not found")):
        assert provider.is_installed("git") is False


def test_dnf_install_empty_list() -> None:
    """DnfProvider.install is a no-op for empty package list."""
    provider = DnfProvider()
    provider.install([])


def test_dnf_install_not_available() -> None:
    """DnfProvider.install raises ProviderError when dnf is absent."""
    provider = DnfProvider()
    with patch("rv.providers.dnf.DnfProvider.is_available", return_value=False):
        with pytest.raises(ProviderError, match="dnf is not available"):
            provider.install(["git"])


def test_dnf_install_dry_run() -> None:
    """DnfProvider.install dry-run does not invoke subprocess."""
    provider = DnfProvider()
    with (
        patch("rv.providers.dnf.DnfProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.dnf.DnfProvider.is_installed", return_value=False),
        patch("rv.providers.dnf.DnfProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["git"], dry_run=True)
        mock_exec.assert_not_called()


def test_dnf_install_all_cached() -> None:
    """DnfProvider.install skips when all packages are cached."""
    provider = DnfProvider()
    with (
        patch("rv.providers.dnf.DnfProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=True),
        patch("rv.providers.dnf.DnfProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["git"], use_cache=True)
        mock_exec.assert_not_called()


def test_dnf_install_success() -> None:
    """DnfProvider.install calls 'dnf install -y <pkgs>' and marks cache."""
    provider = DnfProvider()
    with (
        patch("rv.providers.dnf.DnfProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.dnf.DnfProvider.is_installed", return_value=False),
        patch("rv.providers.dnf.DnfProvider.execute_with_retry") as mock_exec,
        patch("rv.providers.base.PackageCache.mark_installed") as mock_mark,
    ):
        provider.install(["git", "curl"])
        mock_exec.assert_called_once_with(["dnf", "install", "-y", "git", "curl"])
        mock_mark.assert_called_once_with("dnf", ["git", "curl"])


def test_dnf_install_failure() -> None:
    """DnfProvider.install raises ProviderError on execute_with_retry failure."""
    provider = DnfProvider()
    with (
        patch("rv.providers.dnf.DnfProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.dnf.DnfProvider.is_installed", return_value=False),
        patch("rv.providers.dnf.DnfProvider.execute_with_retry", side_effect=ProviderError("network error")),
    ):
        with pytest.raises(ProviderError, match="DNF installation failed"):
            provider.install(["git"])


# =============================================================================
# NixProvider
# =============================================================================


def test_nix_is_installed_found() -> None:
    """NixProvider.is_installed returns True when nix-env -q includes package name."""
    provider = NixProvider()
    mock_result = subprocess.CompletedProcess(["nix-env", "-q", "ripgrep"], 0, stdout="ripgrep-14.1.0\n")
    with patch("subprocess.run", return_value=mock_result):
        assert provider.is_installed("ripgrep") is True


def test_nix_is_installed_not_found_name_absent() -> None:
    """NixProvider.is_installed returns False when package name not in stdout."""
    provider = NixProvider()
    mock_result = subprocess.CompletedProcess(["nix-env", "-q", "ripgrep"], 0, stdout="other-package-1.0\n")
    with patch("subprocess.run", return_value=mock_result):
        assert provider.is_installed("ripgrep") is False


def test_nix_is_installed_nonzero_returncode() -> None:
    """NixProvider.is_installed returns False on non-zero returncode."""
    provider = NixProvider()
    mock_result = subprocess.CompletedProcess(["nix-env", "-q", "ripgrep"], 1, stdout="")
    with patch("subprocess.run", return_value=mock_result):
        assert provider.is_installed("ripgrep") is False


def test_nix_is_installed_exception() -> None:
    """NixProvider.is_installed returns False when subprocess raises."""
    provider = NixProvider()
    with patch("subprocess.run", side_effect=OSError("nix-env not found")):
        assert provider.is_installed("ripgrep") is False


def test_nix_install_empty_list() -> None:
    """NixProvider.install is a no-op for empty list."""
    provider = NixProvider()
    provider.install([])


def test_nix_install_not_available() -> None:
    """NixProvider.install raises ProviderError when nix-env is absent."""
    provider = NixProvider()
    with patch("rv.providers.nix.NixProvider.is_available", return_value=False):
        with pytest.raises(ProviderError, match="nix-env is not available"):
            provider.install(["ripgrep"])


def test_nix_install_dry_run() -> None:
    """NixProvider.install dry-run does not invoke subprocess."""
    provider = NixProvider()
    with (
        patch("rv.providers.nix.NixProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.nix.NixProvider.is_installed", return_value=False),
        patch("rv.providers.nix.NixProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["ripgrep"], dry_run=True)
        mock_exec.assert_not_called()


def test_nix_install_all_cached() -> None:
    """NixProvider.install skips when packages are cached."""
    provider = NixProvider()
    with (
        patch("rv.providers.nix.NixProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=True),
        patch("rv.providers.nix.NixProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["ripgrep"], use_cache=True)
        mock_exec.assert_not_called()


def test_nix_install_success_per_package() -> None:
    """NixProvider.install calls 'nix-env -iA nixpkgs.<pkg>' for each package."""
    provider = NixProvider()
    with (
        patch("rv.providers.nix.NixProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.nix.NixProvider.is_installed", return_value=False),
        patch("rv.providers.nix.NixProvider.execute_with_retry") as mock_exec,
        patch("rv.providers.base.PackageCache.mark_installed"),
    ):
        provider.install(["ripgrep", "neovim"])
        assert mock_exec.call_count == 2
        mock_exec.assert_any_call(["nix-env", "-iA", "nixpkgs.ripgrep"])
        mock_exec.assert_any_call(["nix-env", "-iA", "nixpkgs.neovim"])


def test_nix_install_failure_per_package() -> None:
    """NixProvider.install raises ProviderError when a single package install fails."""
    provider = NixProvider()
    with (
        patch("rv.providers.nix.NixProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.nix.NixProvider.is_installed", return_value=False),
        patch("rv.providers.nix.NixProvider.execute_with_retry", side_effect=ProviderError("nix error")),
    ):
        with pytest.raises(ProviderError, match="Nix installation of 'ripgrep' failed"):
            provider.install(["ripgrep"])


# =============================================================================
# PacmanProvider
# =============================================================================


def test_pacman_is_installed_found() -> None:
    """PacmanProvider.is_installed returns True when pacman -Q succeeds."""
    provider = PacmanProvider()
    mock_result = subprocess.CompletedProcess(["pacman", "-Q", "git"], 0, stdout="git 2.43.0-1\n")
    with patch("subprocess.run", return_value=mock_result):
        assert provider.is_installed("git") is True


def test_pacman_is_installed_not_found() -> None:
    """PacmanProvider.is_installed returns False when pacman -Q fails."""
    provider = PacmanProvider()
    mock_result = subprocess.CompletedProcess(["pacman", "-Q", "git"], 1, stdout="")
    with patch("subprocess.run", return_value=mock_result):
        assert provider.is_installed("git") is False


def test_pacman_is_installed_exception() -> None:
    """PacmanProvider.is_installed returns False when subprocess raises."""
    provider = PacmanProvider()
    with patch("subprocess.run", side_effect=FileNotFoundError("pacman not found")):
        assert provider.is_installed("git") is False


def test_pacman_install_empty_list() -> None:
    """PacmanProvider.install is a no-op for empty list."""
    provider = PacmanProvider()
    provider.install([])


def test_pacman_install_not_available() -> None:
    """PacmanProvider.install raises ProviderError when pacman is absent."""
    provider = PacmanProvider()
    with patch("rv.providers.pacman.PacmanProvider.is_available", return_value=False):
        with pytest.raises(ProviderError, match="pacman is not available"):
            provider.install(["git"])


def test_pacman_install_dry_run() -> None:
    """PacmanProvider.install dry-run does not invoke subprocess."""
    provider = PacmanProvider()
    with (
        patch("rv.providers.pacman.PacmanProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.pacman.PacmanProvider.is_installed", return_value=False),
        patch("rv.providers.pacman.PacmanProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["git"], dry_run=True)
        mock_exec.assert_not_called()


def test_pacman_install_all_cached() -> None:
    """PacmanProvider.install skips when all packages cached."""
    provider = PacmanProvider()
    with (
        patch("rv.providers.pacman.PacmanProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=True),
        patch("rv.providers.pacman.PacmanProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["git"], use_cache=True)
        mock_exec.assert_not_called()


def test_pacman_install_success() -> None:
    """PacmanProvider.install calls 'pacman -S --noconfirm <pkgs>' and marks cache."""
    provider = PacmanProvider()
    with (
        patch("rv.providers.pacman.PacmanProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.pacman.PacmanProvider.is_installed", return_value=False),
        patch("rv.providers.pacman.PacmanProvider.execute_with_retry") as mock_exec,
        patch("rv.providers.base.PackageCache.mark_installed") as mock_mark,
    ):
        provider.install(["git", "base-devel"])
        mock_exec.assert_called_once_with(["pacman", "-S", "--noconfirm", "git", "base-devel"])
        mock_mark.assert_called_once_with("pacman", ["git", "base-devel"])


def test_pacman_install_failure() -> None:
    """PacmanProvider.install raises ProviderError on execute_with_retry failure."""
    provider = PacmanProvider()
    with (
        patch("rv.providers.pacman.PacmanProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.pacman.PacmanProvider.is_installed", return_value=False),
        patch("rv.providers.pacman.PacmanProvider.execute_with_retry", side_effect=ProviderError("lock conflict")),
    ):
        with pytest.raises(ProviderError, match="Pacman installation failed"):
            provider.install(["git"])


# =============================================================================
# PipProvider
# =============================================================================


def test_pip_get_pip_cmd_pip3() -> None:
    """PipProvider._get_pip_cmd returns 'pip3' when pip3 is on PATH."""
    provider = PipProvider()
    with patch("shutil.which", side_effect=lambda x: "/usr/bin/pip3" if x == "pip3" else None):
        assert provider._get_pip_cmd() == "pip3"


def test_pip_get_pip_cmd_pip_fallback() -> None:
    """PipProvider._get_pip_cmd returns 'pip' when pip3 is absent."""
    provider = PipProvider()
    with patch("shutil.which", return_value=None):
        assert provider._get_pip_cmd() == "pip"


def test_pip_is_available_via_pip3() -> None:
    """PipProvider.is_available returns True when pip3 is on PATH."""
    provider = PipProvider()
    with patch("shutil.which", side_effect=lambda x: "/usr/bin/pip3" if x == "pip3" else None):
        assert provider.is_available() is True


def test_pip_is_available_via_pip() -> None:
    """PipProvider.is_available returns True when pip (not pip3) is on PATH."""
    provider = PipProvider()
    with patch("shutil.which", side_effect=lambda x: "/usr/bin/pip" if x == "pip" else None):
        assert provider.is_available() is True


def test_pip_is_available_false() -> None:
    """PipProvider.is_available returns False when neither pip nor pip3 is available."""
    provider = PipProvider()
    with patch("shutil.which", return_value=None):
        assert provider.is_available() is False


def test_pip_is_installed_found() -> None:
    """PipProvider.is_installed returns True when pip show returns 0."""
    provider = PipProvider()
    mock_result = subprocess.CompletedProcess(["pip3", "show", "requests"], 0, stdout="Name: requests\n")
    with (
        patch("shutil.which", return_value="/usr/bin/pip3"),
        patch("subprocess.run", return_value=mock_result),
    ):
        assert provider.is_installed("requests") is True


def test_pip_is_installed_not_found() -> None:
    """PipProvider.is_installed returns False when pip show returns non-zero."""
    provider = PipProvider()
    mock_result = subprocess.CompletedProcess(["pip3", "show", "requests"], 1, stdout="")
    with (
        patch("shutil.which", return_value="/usr/bin/pip3"),
        patch("subprocess.run", return_value=mock_result),
    ):
        assert provider.is_installed("requests") is False


def test_pip_is_installed_exception() -> None:
    """PipProvider.is_installed returns False when subprocess raises."""
    provider = PipProvider()
    with (
        patch("shutil.which", return_value="/usr/bin/pip3"),
        patch("subprocess.run", side_effect=OSError("pip show failed")),
    ):
        assert provider.is_installed("requests") is False


def test_pip_install_empty_list() -> None:
    """PipProvider.install is a no-op for empty list."""
    provider = PipProvider()
    provider.install([])


def test_pip_install_not_available() -> None:
    """PipProvider.install raises ProviderError when pip is absent."""
    provider = PipProvider()
    with patch("rv.providers.pip.PipProvider.is_available", return_value=False):
        with pytest.raises(ProviderError, match="pip is not available"):
            provider.install(["requests"])


def test_pip_install_dry_run() -> None:
    """PipProvider.install dry-run does not invoke subprocess."""
    provider = PipProvider()
    with (
        patch("rv.providers.pip.PipProvider.is_available", return_value=True),
        patch("rv.providers.pip.PipProvider._get_pip_cmd", return_value="pip3"),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.pip.PipProvider.is_installed", return_value=False),
        patch("rv.providers.pip.PipProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["requests"], dry_run=True)
        mock_exec.assert_not_called()


def test_pip_install_all_cached() -> None:
    """PipProvider.install skips when all packages are cached."""
    provider = PipProvider()
    with (
        patch("rv.providers.pip.PipProvider.is_available", return_value=True),
        patch("rv.providers.base.PackageCache.is_installed", return_value=True),
        patch("rv.providers.pip.PipProvider.execute_with_retry") as mock_exec,
    ):
        provider.install(["requests"], use_cache=True)
        mock_exec.assert_not_called()


def test_pip_install_success() -> None:
    """PipProvider.install calls 'pip3 install --user <pkgs>' and marks cache."""
    provider = PipProvider()
    with (
        patch("rv.providers.pip.PipProvider.is_available", return_value=True),
        patch("rv.providers.pip.PipProvider._get_pip_cmd", return_value="pip3"),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.pip.PipProvider.is_installed", return_value=False),
        patch("rv.providers.pip.PipProvider.execute_with_retry") as mock_exec,
        patch("rv.providers.base.PackageCache.mark_installed") as mock_mark,
    ):
        provider.install(["requests", "rich"])
        mock_exec.assert_called_once_with(["pip3", "install", "--user", "requests", "rich"])
        mock_mark.assert_called_once_with("pip", ["requests", "rich"])


def test_pip_install_failure() -> None:
    """PipProvider.install raises ProviderError on execute_with_retry failure."""
    provider = PipProvider()
    with (
        patch("rv.providers.pip.PipProvider.is_available", return_value=True),
        patch("rv.providers.pip.PipProvider._get_pip_cmd", return_value="pip3"),
        patch("rv.providers.base.PackageCache.is_installed", return_value=False),
        patch("rv.providers.pip.PipProvider.is_installed", return_value=False),
        patch("rv.providers.pip.PipProvider.execute_with_retry", side_effect=ProviderError("resolution failed")),
    ):
        with pytest.raises(ProviderError, match="Pip installation failed"):
            provider.install(["requests"])
