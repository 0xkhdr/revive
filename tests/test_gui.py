"""Unit and integration tests for the Revive Web GUI HTTP server."""

import os
import time
import socket
import threading
import json
import urllib.request
import urllib.error
import pytest

from rv.gui.server import start_gui_server
from rv.services.workspace import WorkspaceService
from rv.models.manifest import Manifest


def get_free_port() -> int:
    """Finds a free TCP port dynamically on the local interface."""
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


@pytest.fixture(scope="module")
def gui_server(tmp_path_factory):
    """Fixture that spins up the Web GUI server on a local port in a background thread."""
    # Scaffold a temporary config directory for workspaces to isolate tests
    temp_config_dir = tmp_path_factory.mktemp("rv_config")
    temp_workspace_dir = tmp_path_factory.mktemp("my_workspace")

    # Pre-register our test workspace
    manifest_content = """
version: 2
assets: []
secrets: []
packages:
  brew: []
profiles:
  base:
    assets: []
    secrets: []
    packages: []
"""
    with open(os.path.join(temp_workspace_dir, "manifest.yaml"), "w") as f:
        f.write(manifest_content)

    original_config_path = WorkspaceService.CONFIG_PATH
    WorkspaceService.CONFIG_PATH = os.path.join(temp_config_dir, "workspaces.yaml")

    # Register workspace and change working directory to it
    WorkspaceService.register_workspace(str(temp_workspace_dir), "test_ws")
    original_cwd = os.getcwd()
    os.chdir(str(temp_workspace_dir))

    port = get_free_port()
    host = "127.0.0.1"

    # Launch server in background thread
    server_thread = threading.Thread(
        target=start_gui_server, kwargs={"host": host, "port": port, "open_browser": False}, daemon=True
    )
    server_thread.start()

    # Allow port binding and server start-up latency
    time.sleep(0.4)

    yield f"http://{host}:{port}"

    # Tear down state
    os.chdir(original_cwd)
    WorkspaceService.CONFIG_PATH = original_config_path


def test_serve_static_index(gui_server):
    """Verify that the home page index.html is served successfully."""
    url = f"{gui_server}/"
    req = urllib.request.Request(url)
    with urllib.request.urlopen(req) as resp:
        assert resp.status == 200
        content = resp.read().decode("utf-8")
        assert "Revive" in content
        assert "dashboard-shell" in content


def test_path_traversal_safety(gui_server):
    """Enforce security rules preventing path traversals outside static directories."""
    # Attempting to go up levels to fetch secret configs should be rejected
    url = f"{gui_server}/../__init__.py"
    with pytest.raises(urllib.error.HTTPError) as exc_info:
        urllib.request.urlopen(url)
    assert exc_info.value.code in (403, 404)


def test_api_workspace_get(gui_server):
    """Verify GET /api/workspace returns registered and active workspaces."""
    url = f"{gui_server}/api/workspace"
    with urllib.request.urlopen(url) as resp:
        assert resp.status == 200
        data = json.loads(resp.read().decode("utf-8"))
        assert "active_workspace" in data
        assert data["active_workspace"]["name"] == "test_ws"
        assert len(data["registered_workspaces"]) >= 1


def test_api_manifest_get(gui_server):
    """Verify GET /api/manifest returns a valid manifest YAML mapping."""
    url = f"{gui_server}/api/manifest"
    with urllib.request.urlopen(url) as resp:
        assert resp.status == 200
        data = json.loads(resp.read().decode("utf-8"))
        assert "profiles" in data
        assert "base" in data["profiles"]
        assert "version" in data


def test_api_manifest_post_validation(gui_server):
    """Ensure posting invalid manifest data fails Pydantic schema validation."""
    url = f"{gui_server}/api/manifest"

    # profiles must be a dictionary, so passing a list is invalid schema
    bad_payload = json.dumps({"profiles": []}).encode("utf-8")
    req = urllib.request.Request(url, data=bad_payload, headers={"Content-Type": "application/json"})

    with pytest.raises(urllib.error.HTTPError) as exc_info:
        urllib.request.urlopen(req)
    assert exc_info.value.code == 400


def test_api_doctor_health(gui_server):
    """Verify running doctor diagnostics check over API works."""
    url = f"{gui_server}/api/action/doctor"
    payload = json.dumps({"profile": "base"}).encode("utf-8")
    req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})

    with urllib.request.urlopen(req) as resp:
        assert resp.status == 200
        data = json.loads(resp.read().decode("utf-8"))
        assert "checks_run" in data
        assert "issues" in data
