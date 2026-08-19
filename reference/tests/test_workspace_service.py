"""Test suite for WorkspaceService — targets services/workspace.py ≥90% coverage (A-005)."""

import os
import tempfile
from collections.abc import Generator
from datetime import datetime
from unittest.mock import patch

import pytest
import yaml

from rv.models.workspace import Workspace, WorkspaceConfig
from rv.services.workspace import WorkspaceService


@pytest.fixture
def isolated_config(tmp_path: object) -> Generator[str, None, None]:
    """Patches WorkspaceService.CONFIG_PATH to an isolated temp path for each test."""
    config_path = str(tmp_path) + "/workspaces.yaml"  # type: ignore[operator]
    with patch.object(WorkspaceService, "CONFIG_PATH", config_path):
        yield config_path


# =============================================================================
# load_config
# =============================================================================


def test_load_config_file_missing(isolated_config: str) -> None:
    """Returns default WorkspaceConfig when config file does not exist."""
    config = WorkspaceService.load_config()
    assert config.workspaces == []
    assert config.default_workspace is None


def test_load_config_empty_yaml(isolated_config: str) -> None:
    """Returns default WorkspaceConfig when YAML file is empty (null parse)."""
    with open(isolated_config, "w") as f:
        f.write("")
    config = WorkspaceService.load_config()
    assert config.workspaces == []


def test_load_config_invalid_yaml(isolated_config: str) -> None:
    """Returns default WorkspaceConfig on YAML parse exception."""
    with open(isolated_config, "w") as f:
        f.write(": : : invalid yaml :::")
    config = WorkspaceService.load_config()
    assert config.workspaces == []


def test_load_config_bad_structure(isolated_config: str) -> None:
    """Returns default WorkspaceConfig when YAML parses to non-dict structure."""
    with open(isolated_config, "w") as f:
        f.write("- item1\n- item2\n")
    # WorkspaceConfig(**data) will fail — exception caught → default returned
    config = WorkspaceService.load_config()
    assert config.workspaces == []


# =============================================================================
# save_config + roundtrip
# =============================================================================


def test_save_and_load_roundtrip(isolated_config: str) -> None:
    """Saving a config and loading it returns the same data."""
    ws = Workspace(name="myrepo", path="/home/user/repos/myrepo", last_accessed=datetime.now())
    config = WorkspaceConfig(workspaces=[ws], default_workspace="myrepo")

    WorkspaceService.save_config(config)
    assert os.path.exists(isolated_config)

    loaded = WorkspaceService.load_config()
    assert len(loaded.workspaces) == 1
    assert loaded.workspaces[0].name == "myrepo"
    assert loaded.workspaces[0].path == "/home/user/repos/myrepo"
    assert loaded.default_workspace == "myrepo"


# =============================================================================
# register_workspace
# =============================================================================


def test_register_workspace_new(isolated_config: str, tmp_path: object) -> None:
    """Registering a new path creates a workspace entry."""
    path = str(tmp_path)  # type: ignore[arg-type]
    ws = WorkspaceService.register_workspace(path, name="test-ws")

    assert ws.name == "test-ws"
    assert ws.path == os.path.abspath(path)

    loaded = WorkspaceService.load_config()
    assert len(loaded.workspaces) == 1
    assert loaded.workspaces[0].name == "test-ws"


def test_register_workspace_uses_basename_if_no_name(isolated_config: str, tmp_path: object) -> None:
    """Registering without an explicit name uses directory basename."""
    path = str(tmp_path)  # type: ignore[arg-type]
    ws = WorkspaceService.register_workspace(path)
    assert ws.name == os.path.basename(os.path.abspath(path))


def test_register_workspace_duplicate_updates_last_accessed(isolated_config: str, tmp_path: object) -> None:
    """Re-registering the same path updates last_accessed and does not duplicate."""
    path = str(tmp_path)  # type: ignore[arg-type]
    WorkspaceService.register_workspace(path, name="first")

    import time

    time.sleep(0.01)  # Ensure timestamp difference
    ws2 = WorkspaceService.register_workspace(path, name="second")

    loaded = WorkspaceService.load_config()
    # Path match wins — no duplicate added
    assert len(loaded.workspaces) == 1
    # last_accessed was updated
    assert ws2.last_accessed >= loaded.workspaces[0].last_accessed


# =============================================================================
# list_workspaces
# =============================================================================


def test_list_workspaces_empty(isolated_config: str) -> None:
    """list_workspaces returns empty list when no workspaces are registered."""
    result = WorkspaceService.list_workspaces()
    assert result == []


def test_list_workspaces_multiple(isolated_config: str, tmp_path: object) -> None:
    """list_workspaces returns all registered workspaces."""
    base = str(tmp_path)  # type: ignore[arg-type]
    path_a = os.path.join(base, "a")
    path_b = os.path.join(base, "b")
    os.makedirs(path_a, exist_ok=True)
    os.makedirs(path_b, exist_ok=True)

    WorkspaceService.register_workspace(path_a, name="ws-a")
    WorkspaceService.register_workspace(path_b, name="ws-b")

    result = WorkspaceService.list_workspaces()
    names = [ws.name for ws in result]
    assert "ws-a" in names
    assert "ws-b" in names


# =============================================================================
# get_current_workspace
# =============================================================================


def test_get_current_workspace_match(isolated_config: str, tmp_path: object) -> None:
    """Returns the workspace when CWD is a registered path."""
    path = str(tmp_path)  # type: ignore[arg-type]
    WorkspaceService.register_workspace(path, name="active-ws")

    with patch("os.getcwd", return_value=path):
        result = WorkspaceService.get_current_workspace()

    assert result is not None
    assert result.name == "active-ws"


def test_get_current_workspace_no_match(isolated_config: str) -> None:
    """Returns None when CWD is not a registered path."""
    with patch("os.getcwd", return_value="/totally/unrelated/dir"):
        result = WorkspaceService.get_current_workspace()
    assert result is None


def test_get_current_workspace_most_specific_wins(isolated_config: str, tmp_path: object) -> None:
    """Returns the most specific (longest path) matching workspace."""
    base = str(tmp_path)  # type: ignore[arg-type]
    parent = base
    child = os.path.join(base, "nested", "deep")
    os.makedirs(child, exist_ok=True)

    WorkspaceService.register_workspace(parent, name="parent-ws")
    WorkspaceService.register_workspace(child, name="child-ws")

    # CWD is inside child → child-ws should win
    cwd = os.path.join(child, "subdir")
    with patch("os.getcwd", return_value=cwd):
        result = WorkspaceService.get_current_workspace()

    assert result is not None
    assert result.name == "child-ws"


# =============================================================================
# remove_workspace (by name)
# =============================================================================


def test_remove_workspace_exists(isolated_config: str, tmp_path: object) -> None:
    """remove_workspace returns True when the named workspace is removed."""
    path = str(tmp_path)  # type: ignore[arg-type]
    WorkspaceService.register_workspace(path, name="to-remove")

    result = WorkspaceService.remove_workspace("to-remove")

    assert result is True
    assert WorkspaceService.list_workspaces() == []


def test_remove_workspace_not_found(isolated_config: str) -> None:
    """remove_workspace returns False when workspace name doesn't exist."""
    result = WorkspaceService.remove_workspace("nonexistent")
    assert result is False


# =============================================================================
# update_workspace
# =============================================================================


def test_update_workspace_name(isolated_config: str, tmp_path: object) -> None:
    """update_workspace changes the name of the matching workspace."""
    path = str(tmp_path)  # type: ignore[arg-type]
    abs_path = os.path.abspath(path)
    WorkspaceService.register_workspace(path, name="old-name")

    updated = WorkspaceService.update_workspace(abs_path, new_name="new-name")

    assert updated is not None
    assert updated.name == "new-name"

    loaded = WorkspaceService.list_workspaces()
    assert loaded[0].name == "new-name"


def test_update_workspace_path(isolated_config: str, tmp_path: object) -> None:
    """update_workspace changes the path of the matching workspace."""
    base = str(tmp_path)  # type: ignore[arg-type]
    original_path = os.path.join(base, "original")
    new_path_raw = os.path.join(base, "relocated")
    os.makedirs(original_path, exist_ok=True)

    abs_orig = os.path.abspath(original_path)
    WorkspaceService.register_workspace(original_path, name="reloc-ws")

    updated = WorkspaceService.update_workspace(abs_orig, new_path=new_path_raw)

    assert updated is not None
    assert updated.path == os.path.abspath(new_path_raw)


def test_update_workspace_not_found(isolated_config: str) -> None:
    """update_workspace returns None when no workspace matches the original path."""
    result = WorkspaceService.update_workspace("/nonexistent/path", new_name="whatever")
    assert result is None


# =============================================================================
# remove_workspaces (by path list)
# =============================================================================


def test_remove_workspaces_by_path(isolated_config: str, tmp_path: object) -> None:
    """remove_workspaces removes all matching paths and returns the count removed."""
    base = str(tmp_path)  # type: ignore[arg-type]
    paths = [os.path.join(base, f"ws{i}") for i in range(3)]
    for p in paths:
        os.makedirs(p, exist_ok=True)
        WorkspaceService.register_workspace(p, name=os.path.basename(p))

    abs_paths = [os.path.abspath(p) for p in paths[:2]]
    removed = WorkspaceService.remove_workspaces(abs_paths)

    assert removed == 2
    remaining = WorkspaceService.list_workspaces()
    assert len(remaining) == 1
    assert remaining[0].path == os.path.abspath(paths[2])


def test_remove_workspaces_no_match(isolated_config: str) -> None:
    """remove_workspaces returns 0 when no paths match."""
    removed = WorkspaceService.remove_workspaces(["/no/match/here"])
    assert removed == 0
