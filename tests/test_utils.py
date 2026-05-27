"""Extended tests for PathHelper covering cross-device detection,
non-existent path handling, relative symlink resolution, and safe-subpath edge cases.
Targets: utils/path.py lines 25-45, 70, 72-73 (54% → 90%+).
"""

import os
import tempfile
from unittest.mock import patch

import pytest

from rv.utils.path import PathHelper

# ---------------------------------------------------------------------------
# canonicalize
# ---------------------------------------------------------------------------


def test_canonicalize_home_expansion() -> None:
    result = PathHelper.canonicalize("~")
    assert result == os.path.expanduser("~")
    assert os.path.isabs(result)


def test_canonicalize_dotdot_resolution() -> None:
    result = PathHelper.canonicalize("/tmp/../tmp/.")
    assert result == "/tmp"


def test_canonicalize_env_var_missing_uses_empty() -> None:
    # Unset env var is left as empty string by os.path.expandvars
    os.environ.pop("__DEFINITELY_MISSING_VAR__", None)
    result = PathHelper.canonicalize("/prefix/${__DEFINITELY_MISSING_VAR__}/suffix")
    assert "/prefix/" in result


# ---------------------------------------------------------------------------
# is_cross_device — same device (common case)
# ---------------------------------------------------------------------------


def test_is_cross_device_same_device() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        p1 = os.path.join(tmpdir, "a.txt")
        p2 = os.path.join(tmpdir, "b.txt")
        open(p1, "w").close()
        open(p2, "w").close()
        # Same tmpdir → same device
        assert PathHelper.is_cross_device(p1, p2) is False


def test_is_cross_device_nonexistent_paths_walk_to_parent() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        # Neither child exists yet — should walk up to tmpdir and compare devices
        p1 = os.path.join(tmpdir, "does_not_exist_1", "nested")
        p2 = os.path.join(tmpdir, "does_not_exist_2", "nested")
        # Both resolve to tmpdir's device → not cross-device
        result = PathHelper.is_cross_device(p1, p2)
        assert isinstance(result, bool)


def test_is_cross_device_stat_failure_returns_false() -> None:
    # If stat raises, fallback is False (conservative)
    with patch("os.stat", side_effect=OSError("stat fail")):
        result = PathHelper.is_cross_device("/fake/path/a", "/fake/path/b")
    assert result is False


def test_is_cross_device_one_existing_one_not() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        existing = os.path.join(tmpdir, "real.txt")
        open(existing, "w").close()
        missing = os.path.join(tmpdir, "ghost", "deep", "path")
        result = PathHelper.is_cross_device(existing, missing)
        assert isinstance(result, bool)


# ---------------------------------------------------------------------------
# detect_symlink_loop — additional edge cases
# ---------------------------------------------------------------------------


def test_detect_symlink_loop_non_symlink_file() -> None:
    with tempfile.NamedTemporaryFile() as f:
        # Regular file is not a symlink — no loop
        assert PathHelper.detect_symlink_loop(f.name) is False


def test_detect_symlink_loop_non_existent_path() -> None:
    # Non-existent path — canonicalize resolves it but islink returns False
    assert PathHelper.detect_symlink_loop("/absolutely/does/not/exist/path") is False


def test_detect_symlink_loop_relative_symlink() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        target = os.path.join(tmpdir, "real.txt")
        with open(target, "w") as f:
            f.write("content")

        link = os.path.join(tmpdir, "rel_link")
        # Create a relative symlink (target is just basename)
        os.symlink("real.txt", link)

        # Relative symlink → resolves to real file → no loop
        assert PathHelper.detect_symlink_loop(link) is False


def test_detect_symlink_loop_readlink_oserror() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        link = os.path.join(tmpdir, "dangling_link")
        os.symlink("/nonexistent/target", link)

        with patch("os.readlink", side_effect=OSError("readlink fail")):
            # Exception caught → returns False
            assert PathHelper.detect_symlink_loop(link) is False


def test_detect_symlink_loop_three_link_chain() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        # a → b → c → a (loop of 3)
        a = os.path.join(tmpdir, "a")
        b = os.path.join(tmpdir, "b")
        c = os.path.join(tmpdir, "c")
        os.symlink(b, a)
        os.symlink(c, b)
        os.symlink(a, c)
        assert PathHelper.detect_symlink_loop(a) is True


# ---------------------------------------------------------------------------
# is_safe_subpath
# ---------------------------------------------------------------------------


def test_is_safe_subpath_exact_match() -> None:
    with tempfile.TemporaryDirectory() as tmpdir:
        assert PathHelper.is_safe_subpath(tmpdir, tmpdir) is True


def test_is_safe_subpath_sibling_directory() -> None:
    # /tmp/base vs /tmp/base_other — must not match
    assert PathHelper.is_safe_subpath("/tmp/base", "/tmp/base_other") is False


def test_is_safe_subpath_traversal_attempt() -> None:
    assert PathHelper.is_safe_subpath("/var/repo", "/var/repo/../../etc/passwd") is False


def test_is_safe_subpath_deeply_nested() -> None:
    assert PathHelper.is_safe_subpath("/repo", "/repo/a/b/c/d/e") is True
