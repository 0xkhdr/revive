"""Unit and integration tests for the Revive Web GUI HTTP server."""

import json
import os
import socket
import threading
import time
import urllib.error
import urllib.request
from unittest.mock import MagicMock

import pytest

from rv.gui.server import start_gui_server
from rv.models.manifest import Manifest
from rv.services.workspace import WorkspaceService


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

    # Launch server in background thread with auth disabled for integration testing
    server_thread = threading.Thread(
        target=start_gui_server,
        kwargs={"host": host, "port": port, "open_browser": False, "auth_token": ""},
        daemon=True,
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


def test_api_keygen(gui_server):
    """Verify that posting to keygen returns a valid public and private Age keypair."""
    url = f"{gui_server}/api/action/keygen"
    req = urllib.request.Request(url, data=b"{}", headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        assert resp.status == 200
        data = json.loads(resp.read().decode("utf-8"))
        assert "public_key" in data
        assert "private_key" in data
        assert data["public_key"].startswith("age1")
        assert "AGE-SECRET-KEY-1" in data["private_key"]


def test_api_recovery_list(gui_server):
    """Verify that recovery listing works and returns a list of journals."""
    url = f"{gui_server}/api/action/recovery/list"
    req = urllib.request.Request(url, data=b"{}", headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req) as resp:
        assert resp.status == 200
        data = json.loads(resp.read().decode("utf-8"))
        assert "journals" in data


def test_api_token_authentication():
    """Verify WebGUIRequestHandler's _check_auth method works correctly for token validation."""
    import rv.gui.server as gui_server_module
    from rv.gui.server import WebGUIRequestHandler

    # Mock the handler and its attributes
    handler = MagicMock(spec=WebGUIRequestHandler)
    handler.path = "/api/workspace"
    handler.headers = {"X-Auth-Token": "test-secret-token"}

    # Case 1: Auth disabled (_AUTH_TOKEN is None)
    gui_server_module._AUTH_TOKEN = None
    assert WebGUIRequestHandler._check_auth(handler) is True

    # Case 2: Auth enabled, matching header token
    gui_server_module._AUTH_TOKEN = "test-secret-token"
    assert WebGUIRequestHandler._check_auth(handler) is True

    # Case 3: Auth enabled, mismatched header token
    handler.headers = {"X-Auth-Token": "wrong-token"}
    assert WebGUIRequestHandler._check_auth(handler) is False

    # Case 4: Auth enabled, missing header but matching query token
    handler.headers = {}
    handler.path = "/api/workspace?token=test-secret-token"
    assert WebGUIRequestHandler._check_auth(handler) is True

    # Case 5: Auth enabled, missing header and mismatched/missing query token
    handler.path = "/api/workspace?token=wrong-token"
    assert WebGUIRequestHandler._check_auth(handler) is False

    handler.path = "/api/workspace"
    assert WebGUIRequestHandler._check_auth(handler) is False

    # Clean up module state
    gui_server_module._AUTH_TOKEN = None


def test_api_authenticated_returns_401(gui_server: str) -> None:
    """API requests with a wrong auth token receive a 401 Unauthorized response."""
    import rv.gui.server as gui_server_module

    # Enable authentication with a known token
    gui_server_module._AUTH_TOKEN = "valid-secret-token"
    try:
        url = f"{gui_server}/api/workspace"
        req = urllib.request.Request(url, headers={"X-Auth-Token": "wrong-token"})
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            urllib.request.urlopen(req)
        assert exc_info.value.code == 401
    finally:
        gui_server_module._AUTH_TOKEN = None


def test_cors_header_loopback_restricted(gui_server: str) -> None:
    """API responses include Access-Control-Allow-Origin restricted to the bound host."""
    import rv.gui.server as gui_server_module

    url = f"{gui_server}/api/workspace"
    with urllib.request.urlopen(url) as resp:
        cors_origin = resp.headers.get("Access-Control-Allow-Origin", "")
        # Should NOT be wildcard (the default after T-002 fix)
        assert cors_origin != "*", "CORS origin should not be wildcard after GAP-002 fix"
        # Should be the loopback address the server is bound on
        assert "127.0.0.1" in cors_origin or cors_origin == gui_server_module._ALLOWED_ORIGIN


def test_api_options_preflight(gui_server: str) -> None:
    """OPTIONS pre-flight request returns 204 with correct CORS headers."""
    url = f"{gui_server}/api/workspace"
    req = urllib.request.Request(url, method="OPTIONS")
    with urllib.request.urlopen(req) as resp:
        assert resp.status == 204
        assert "Access-Control-Allow-Methods" in resp.headers


def test_api_unknown_endpoint_404(gui_server: str) -> None:
    """API requests to an unknown endpoint return 404."""
    url = f"{gui_server}/api/nonexistent"
    with pytest.raises(urllib.error.HTTPError) as exc_info:
        urllib.request.urlopen(url)
    assert exc_info.value.code == 404


def test_api_restore_mocked(gui_server: str) -> None:
    """POST /api/action/restore executes a restore and returns tx_id on success."""
    from unittest.mock import patch

    url = f"{gui_server}/api/action/restore"
    payload = json.dumps({"profile": "base", "dry_run": True}).encode("utf-8")
    req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})

    with patch("rv.services.restore.RestoreService.restore", return_value="mocked-tx-id"):
        with urllib.request.urlopen(req) as resp:
            assert resp.status == 200
            data = json.loads(resp.read().decode("utf-8"))
            assert data["success"] is True
            assert data["tx_id"] == "mocked-tx-id"


def test_api_restore_failure_returns_500(gui_server: str) -> None:
    """POST /api/action/restore returns 500 when restore raises an exception."""
    from unittest.mock import patch

    url = f"{gui_server}/api/action/restore"
    payload = json.dumps({"profile": "base", "dry_run": False}).encode("utf-8")
    req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})

    with patch("rv.services.restore.RestoreService.restore", side_effect=RuntimeError("restore blew up")):
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            urllib.request.urlopen(req)
        assert exc_info.value.code == 500


def test_api_status_drift_check(gui_server: str) -> None:
    """POST /api/action/status returns a drift analysis report."""
    from unittest.mock import patch

    url = f"{gui_server}/api/action/status"
    payload = json.dumps({"profile": "base"}).encode("utf-8")
    req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})

    mock_report = {"status": "clean", "drifted_assets": [], "missing_assets": []}
    with patch("rv.services.status.StatusService.get_status", return_value=mock_report):
        with urllib.request.urlopen(req) as resp:
            assert resp.status == 200
            data = json.loads(resp.read().decode("utf-8"))
            assert "status" in data


def test_api_recovery_rollback_not_found(gui_server: str) -> None:
    """POST /api/action/recovery/rollback returns 404 when tx_id is not found."""
    from unittest.mock import patch

    url = f"{gui_server}/api/action/recovery/rollback"
    payload = json.dumps({"tx_id": "nonexistent-tx"}).encode("utf-8")
    req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})

    with patch("rv.services.recovery.RecoveryService.list_incomplete_journals", return_value=[]):
        with pytest.raises(urllib.error.HTTPError) as exc_info:
            urllib.request.urlopen(req)
        assert exc_info.value.code == 404


def test_api_recovery_discard_missing_tx_id(gui_server: str) -> None:
    """POST /api/action/recovery/discard without tx_id returns 400."""
    url = f"{gui_server}/api/action/recovery/discard"
    payload = json.dumps({}).encode("utf-8")
    req = urllib.request.Request(url, data=payload, headers={"Content-Type": "application/json"})

    with pytest.raises(urllib.error.HTTPError) as exc_info:
        urllib.request.urlopen(req)
    assert exc_info.value.code == 400


def test_start_gui_server_non_loopback_warning(capsys: pytest.CaptureFixture[str]) -> None:
    """start_gui_server prints a security warning when binding to a non-loopback address."""
    import sys
    from unittest.mock import MagicMock, patch

    mock_server = MagicMock()
    mock_server.serve_forever.side_effect = KeyboardInterrupt

    with patch("rv.gui.server.TCPServer", return_value=mock_server):
        from rv.gui.server import start_gui_server as _start

        try:
            _start(host="0.0.0.0", port=19999, open_browser=False, auth_token="")  # noqa: S104
        except SystemExit:
            pass
        except KeyboardInterrupt:
            pass

    captured = capsys.readouterr()
    assert "SECURITY WARNING" in captured.err or "SECURITY WARNING" in captured.out


def test_cors_wildcard_flag() -> None:
    """start_gui_server with cors_wildcard=True sets _ALLOWED_ORIGIN to '*'."""
    from unittest.mock import MagicMock, patch

    import rv.gui.server as gui_server_module

    mock_server = MagicMock()
    mock_server.serve_forever.side_effect = KeyboardInterrupt

    with patch("rv.gui.server.TCPServer", return_value=mock_server):
        try:
            from rv.gui.server import start_gui_server as _start

            _start(host="127.0.0.1", port=19998, open_browser=False, auth_token="", cors_wildcard=True)
        except (KeyboardInterrupt, SystemExit):
            pass

    assert gui_server_module._ALLOWED_ORIGIN == "*"
    # Reset for subsequent tests
    gui_server_module._ALLOWED_ORIGIN = "http://127.0.0.1:8080"
