"""Test suite for platform, path, and env interpolation utilities.
"""

import os
import tempfile
import pytest
from rv.utils.platform import Platform
from rv.utils.path import PathHelper
from rv.utils.interpolate import Interpolator


def test_platform_detection() -> None:
    # Verify platform methods run without errors
    current_os = Platform.get_os()
    assert isinstance(current_os, str)
    assert len(current_os) > 0

    is_linux = Platform.is_linux()
    is_macos = Platform.is_macos()
    # Cannot be both linux and macos
    assert not (is_linux and is_macos)

    distro = Platform.get_distro()
    assert isinstance(distro, str)

    # Check finding common system tool (like ls)
    ls_path = Platform.find_tool("ls")
    if ls_path:
        assert os.path.exists(ls_path)
        assert Platform.has_tool("ls") is True


def test_path_helper_canonicalize() -> None:
    path = PathHelper.canonicalize("/tmp/../tmp/file.txt")
    assert path == "/tmp/file.txt"

    # Env var expansion in path
    os.environ["__TEST_RV_PATH__"] = "my_dir"
    path_with_env = PathHelper.canonicalize("/tmp/${__TEST_RV_PATH__}/file.txt")
    assert path_with_env == "/tmp/my_dir/file.txt"
    del os.environ["__TEST_RV_PATH__"]


def test_path_helper_subpath_safety() -> None:
    base = "/var/www/html"
    assert PathHelper.is_safe_subpath(base, "/var/www/html/rai/up") is True
    assert PathHelper.is_safe_subpath(base, "/var/www/html") is True
    assert PathHelper.is_safe_subpath(base, "/var/www") is False
    assert PathHelper.is_safe_subpath(base, "/etc/passwd") is False


def test_path_helper_symlink_loop_detection() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        # Create loop: link1 -> link2 -> link1
        link1 = os.path.join(tmpdir, "link1")
        link2 = os.path.join(tmpdir, "link2")

        os.symlink(link2, link1)
        os.symlink(link1, link2)

        # Detect loop should return True
        assert PathHelper.detect_symlink_loop(link1) is True
        assert PathHelper.detect_symlink_loop(link2) is True

        # Non-looping symlink
        target_file = os.path.join(tmpdir, "target.txt")
        with open(target_file, "w") as f:
            f.write("hello")
        
        safe_link = os.path.join(tmpdir, "safe_link")
        os.symlink(target_file, safe_link)

        assert PathHelper.detect_symlink_loop(safe_link) is False


def test_interpolator() -> None:
    env = {
        "USER": "test_user",
        "HOME": "/home/test_user",
    }

    # Standard variable
    res = Interpolator.interpolate("Welcome ${USER}!", env_override=env)
    assert res == "Welcome test_user!"

    # Variable with default value (when var is present)
    res = Interpolator.interpolate("Path: ${HOME:-/default/path}", env_override=env)
    assert res == "Path: /home/test_user"

    # Variable with default value (when var is absent)
    res = Interpolator.interpolate("Port: ${PORT:-8080}", env_override=env)
    assert res == "Port: 8080"

    # Missing variable, no default -> raises ValueError
    with pytest.raises(ValueError) as excinfo:
        Interpolator.interpolate("Secret: ${SECRET_TOKEN}", env_override=env)
    assert "Environment variable 'SECRET_TOKEN' is required but not set" in str(excinfo.value)
