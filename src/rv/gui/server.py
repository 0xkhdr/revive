"""Lightweight, robust Python HTTP server for Revive Web GUI.

Uses only Python standard library to serve static assets and REST API endpoints.
Supports token-based authentication for all API endpoints.
"""

import http.server
import json
import logging
import os
import secrets
import shutil
import sys
import urllib.parse
import webbrowser
from datetime import datetime
from io import StringIO
from socketserver import TCPServer
from typing import Any

from rv.models.manifest import Asset, AssetType, ConflictStrategy, Manifest, Secret
from rv.security.encryptor import AgeEncryptor
from rv.services.doctor import DoctorService
from rv.services.recovery import RecoveryService
from rv.services.restore import ManifestLoader, ProfileResolver, RestoreService
from rv.services.status import StatusService
from rv.services.workspace import WorkspaceService

logger = logging.getLogger("rv.gui.server")

# Module-level auth token — stored in memory only, never persisted to disk.
# Set by start_gui_server() before the server starts.
_AUTH_TOKEN: str | None = None

# Module-level CORS origin — restricted to the loopback address the server is bound on.
# Set by start_gui_server() to prevent cross-origin requests from malicious web pages.
# Only set to "*" when explicitly requested via --cors-wildcard.
_ALLOWED_ORIGIN: str = "http://127.0.0.1:8080"


class WebGUIRequestHandler(http.server.BaseHTTPRequestHandler):
    """Custom request handler that serves both Web GUI static files and REST API endpoints."""

    def log_message(self, format: str, *args: Any) -> None:
        """Silence standard request logging to keep terminal output clean."""
        logger.debug(format % args)

    def _check_auth(self) -> bool:
        """Validates the authentication token from query parameter or header.

        Accepts token via:
        - Query parameter: ?token=<value>
        - HTTP header: X-Auth-Token: <value>

        Returns:
            True if the token is valid or auth is disabled (no token configured).
            False if auth is required and the token is missing or invalid.
        """
        global _AUTH_TOKEN
        if _AUTH_TOKEN is None:
            # Auth not configured — allow all requests
            return True

        # Check query parameter
        parsed_url = urllib.parse.urlparse(self.path)
        query_params = urllib.parse.parse_qs(parsed_url.query)
        query_token = query_params.get("token", [None])[0]
        if query_token and secrets.compare_digest(query_token, _AUTH_TOKEN):
            return True

        # Check header
        header_token = self.headers.get("X-Auth-Token", "")
        if header_token and secrets.compare_digest(header_token, _AUTH_TOKEN):
            return True

        return False

    def _send_response_json(self, data: Any, status: int = 200) -> None:
        """Helper to send a JSON response."""
        try:
            body = json.dumps(data)
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Access-Control-Allow-Origin", _ALLOWED_ORIGIN)
            self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
            self.send_header("Access-Control-Allow-Headers", "Content-Type, X-Auth-Token")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body.encode("utf-8"))
        except Exception as e:
            logger.error(f"Error sending JSON response: {e}")

    def do_OPTIONS(self) -> None:
        """Handle CORS pre-flight requests."""
        self.send_response(204)
        self.send_header("Access-Control-Allow-Origin", _ALLOWED_ORIGIN)
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Access-Control-Allow-Headers", "Content-Type, X-Auth-Token")
        self.end_headers()

    def do_GET(self) -> None:
        """Handle GET requests for static files and APIs."""
        parsed_url = urllib.parse.urlparse(self.path)
        path = parsed_url.path

        if path.startswith("/api/"):
            if not self._check_auth():
                self._send_response_json({"error": "Unauthorized: valid auth token required"}, 401)
                return
            self._handle_api_get(path, parsed_url.query)
        else:
            self._serve_static_file(path)

    def do_POST(self) -> None:
        """Handle POST requests for APIs."""
        parsed_url = urllib.parse.urlparse(self.path)
        path = parsed_url.path

        if path.startswith("/api/"):
            if not self._check_auth():
                self._send_response_json({"error": "Unauthorized: valid auth token required"}, 401)
                return
            content_length = int(self.headers.get("Content-Length", 0))
            post_data = self.rfile.read(content_length).decode("utf-8") if content_length > 0 else ""

            try:
                payload = json.loads(post_data) if post_data else {}
            except ValueError:
                self._send_response_json({"error": "Invalid JSON format"}, 400)
                return

            self._handle_api_post(path, payload)
        else:
            self.send_error(404, "Not Found")

    def do_PUT(self) -> None:
        """Handle PUT requests for APIs."""
        parsed_url = urllib.parse.urlparse(self.path)
        path = parsed_url.path

        if path.startswith("/api/"):
            if not self._check_auth():
                self._send_response_json({"error": "Unauthorized: valid auth token required"}, 401)
                return
            content_length = int(self.headers.get("Content-Length", 0))
            post_data = self.rfile.read(content_length).decode("utf-8") if content_length > 0 else ""

            try:
                payload = json.loads(post_data) if post_data else {}
            except ValueError:
                self._send_response_json({"error": "Invalid JSON format"}, 400)
                return

            self._handle_api_put(path, payload)
        else:
            self.send_error(404, "Not Found")

    def do_DELETE(self) -> None:
        """Handle DELETE requests for APIs."""
        parsed_url = urllib.parse.urlparse(self.path)
        path = parsed_url.path

        if path.startswith("/api/"):
            if not self._check_auth():
                self._send_response_json({"error": "Unauthorized: valid auth token required"}, 401)
                return
            content_length = int(self.headers.get("Content-Length", 0))
            post_data = self.rfile.read(content_length).decode("utf-8") if content_length > 0 else ""

            try:
                payload = json.loads(post_data) if post_data else {}
            except ValueError:
                self._send_response_json({"error": "Invalid JSON format"}, 400)
                return

            self._handle_api_delete(path, payload)
        else:
            self.send_error(404, "Not Found")

    def _serve_static_file(self, path: str) -> None:
        """Serve files from the local static directory safely."""
        # Sanitize path to prevent directory traversal
        clean_path = path.lstrip("/")
        if not clean_path or clean_path == "index.html":
            clean_path = "index.html"

        # Locate static folder relative to server.py
        static_dir = os.path.join(os.path.dirname(__file__), "static")
        target_file = os.path.abspath(os.path.join(static_dir, clean_path))

        # Enforce that the target file resides strictly within the static directory
        if not target_file.startswith(os.path.abspath(static_dir)):
            self.send_error(403, "Forbidden")
            return

        if not os.path.exists(target_file) or os.path.isdir(target_file):
            self.send_error(404, "Not Found")
            return

        # Determine Content-Type based on extension
        ext = os.path.splitext(target_file)[1].lower()
        content_types = {
            ".html": "text/html",
            ".css": "text/css",
            ".js": "application/javascript",
            ".json": "application/json",
            ".png": "image/png",
            ".jpg": "image/jpeg",
            ".svg": "image/svg+xml",
            ".ico": "image/x-icon",
        }
        content_type = content_types.get(ext, "application/octet-stream")

        try:
            with open(target_file, "rb") as f:
                content = f.read()

            self.send_response(200)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(content)))
            self.end_headers()
            self.wfile.write(content)
        except Exception as e:
            logger.error(f"Error serving static file {clean_path}: {e}")
            self.send_error(500, "Internal Server Error")

    def _handle_api_get(self, path: str, query: str) -> None:
        """Route and handle API GET requests."""
        if path == "/api/workspace":
            # Get current active workspace details
            active_ws = WorkspaceService.get_current_workspace()
            workspaces = WorkspaceService.list_workspaces()

            # Map workspaces to JSON serializable structures
            ws_list = [
                {
                    "name": ws.name,
                    "path": ws.path,
                    "last_accessed": ws.last_accessed.isoformat(),
                }
                for ws in workspaces
            ]

            res = {
                "active_path": os.getcwd(),
                "active_workspace": {
                    "name": active_ws.name,
                    "path": active_ws.path,
                }
                if active_ws
                else None,
                "registered_workspaces": ws_list,
            }
            self._send_response_json(res)

        elif path == "/api/manifest":
            active_ws = WorkspaceService.get_current_workspace()
            if not active_ws:
                self._send_response_json(
                    {"error": "No active workspace. Please register or select a workspace first."}, 400
                )
                return

            manifest_path = os.path.join(active_ws.path, "manifest.yaml")
            if not os.path.exists(manifest_path):
                self._send_response_json({"error": f"manifest.yaml not found in workspace path: {active_ws.path}"}, 404)
                return

            try:
                manifest = ManifestLoader.load(manifest_path)
                self._send_response_json(manifest.model_dump(mode="json"))
            except Exception as e:
                self._send_response_json({"error": f"Failed to load manifest: {e}"}, 500)

        else:
            self.send_error(404, "Not Found")

    def _handle_api_put(self, path: str, payload: dict[str, Any]) -> None:
        """Route and handle API PUT requests."""
        if path == "/api/workspace":
            original_path = payload.get("original_path")
            new_name = payload.get("name")
            new_path = payload.get("path")

            if not original_path:
                self._send_response_json({"error": "Original workspace path is required for updating"}, 400)
                return

            try:
                ws = WorkspaceService.update_workspace(original_path, new_name, new_path)
                if ws:
                    self._send_response_json({"success": True, "workspace": {"name": ws.name, "path": ws.path}})
                else:
                    self._send_response_json({"error": "Workspace not found"}, 404)
            except Exception as e:
                self._send_response_json({"error": f"Failed to update workspace: {e}"}, 500)
            return

        self.send_error(404, "Not Found")

    def _handle_api_delete(self, path: str, payload: dict[str, Any]) -> None:
        """Route and handle API DELETE requests."""
        if path == "/api/workspace":
            paths = payload.get("paths", [])
            if not paths or not isinstance(paths, list):
                self._send_response_json({"error": "A list of workspace paths is required"}, 400)
                return

            try:
                removed_count = WorkspaceService.remove_workspaces(paths)
                self._send_response_json({"success": True, "removed_count": removed_count})
            except Exception as e:
                self._send_response_json({"error": f"Failed to delete workspaces: {e}"}, 500)
            return

        self.send_error(404, "Not Found")

    def _handle_api_post(self, path: str, payload: dict[str, Any]) -> None:
        """Route and handle API POST requests."""
        active_ws = WorkspaceService.get_current_workspace()

        # Workspace operations do not necessarily require an active workspace context
        if path == "/api/workspace/register":
            folder_path = payload.get("path")
            name = payload.get("name")
            if not folder_path:
                self._send_response_json({"error": "Workspace path is required"}, 400)
                return

            abs_path = os.path.abspath(os.path.expanduser(folder_path))
            if not os.path.isdir(abs_path):
                self._send_response_json({"error": f"Path is not a valid directory: {folder_path}"}, 400)
                return

            try:
                ws = WorkspaceService.register_workspace(abs_path, name)
                # Switch current context directory to this workspace
                os.chdir(ws.path)
                self._send_response_json({"success": True, "workspace": {"name": ws.name, "path": ws.path}})
            except Exception as e:
                self._send_response_json({"error": f"Failed to register workspace: {e}"}, 500)
            return

        elif path == "/api/workspace/switch":
            name = payload.get("name")
            if not name:
                self._send_response_json({"error": "Workspace name is required"}, 400)
                return

            workspaces = WorkspaceService.list_workspaces()
            target_ws = next((ws for ws in workspaces if ws.name == name), None)
            if not target_ws:
                self._send_response_json({"error": f"Workspace not found with name: {name}"}, 404)
                return

            try:
                # Update current directory to target path
                os.chdir(target_ws.path)
                target_ws.last_accessed = (
                    datetime.fromtimestamp(os.path.getmtime(target_ws.path))
                    if os.path.exists(target_ws.path)
                    else target_ws.last_accessed
                )
                WorkspaceService.register_workspace(target_ws.path, target_ws.name)  # Updates last_accessed
                self._send_response_json(
                    {"success": True, "workspace": {"name": target_ws.name, "path": target_ws.path}}
                )
            except Exception as e:
                self._send_response_json({"error": f"Failed to switch to workspace: {e}"}, 500)
            return

        # Core operations require an active workspace context
        if not active_ws:
            self._send_response_json({"error": "Active workspace not selected"}, 400)
            return

        manifest_path = os.path.join(active_ws.path, "manifest.yaml")

        if path == "/api/manifest":
            try:
                # Validate using Pydantic models first
                validated = Manifest.model_validate(payload)

                # Write to manifest.yaml
                with open(manifest_path, "w", encoding="utf-8") as f:
                    data = validated.model_dump(mode="json", exclude_none=True)
                    import yaml

                    yaml.dump(data, f, sort_keys=False)

                self._send_response_json({"success": True})
            except Exception as e:
                self._send_response_json({"error": f"Validation/Save failed: {e}"}, 400)

        elif path == "/api/asset/import":
            src_raw = payload.get("source_path")
            is_secret = bool(payload.get("is_secret"))
            asset_id = payload.get("asset_id")
            target_path = payload.get("target_path")
            profile = payload.get("profile", "base")
            recipient = payload.get("recipient")

            if not src_raw:
                self._send_response_json({"error": "source_path is required"}, 400)
                return

            abs_src = os.path.abspath(os.path.expanduser(src_raw))
            if not os.path.isfile(abs_src):
                self._send_response_json({"error": f"Source file does not exist or is a directory: {src_raw}"}, 400)
                return

            if is_secret and not recipient:
                # Attempt to get recipient from environment
                recipient = os.environ.get("REVIVE_PUBKEY")
                if not recipient:
                    self._send_response_json({"error": "Recipient public key is required for secret encryption"}, 400)
                    return

            try:
                manifest = ManifestLoader.load(manifest_path)
                item_id = asset_id or os.path.basename(abs_src)
                target = target_path or f"~/.config/revive_imported/{item_id}"

                # Ensure ID is unique
                if any(a.id == item_id for a in manifest.assets) or any(s.id == item_id for s in manifest.secrets):
                    self._send_response_json({"error": f"Asset ID '{item_id}' already registered in manifest"}, 400)
                    return

                if profile not in manifest.profiles:
                    self._send_response_json({"error": f"Target profile '{profile}' does not exist in manifest"}, 400)
                    return

                if is_secret:
                    if recipient is None:
                        raise ValueError("recipient is required for secrets")
                    dest_rel = os.path.join("secrets", f"{item_id}.age")
                    dest_abs = os.path.join(active_ws.path, dest_rel)
                    os.makedirs(os.path.dirname(dest_abs), exist_ok=True)

                    AgeEncryptor.encrypt_file(abs_src, dest_abs, [recipient])
                    manifest.secrets.append(
                        Secret(
                            id=item_id,
                            type=AssetType.SECRET,
                            source=dest_rel,
                            target=target,
                            permissions="0600",
                            owner=None,
                            encrypted=True,
                        )
                    )
                    manifest.profiles[profile].secrets.append(item_id)
                else:
                    dest_rel = os.path.join("assets", item_id)
                    dest_abs = os.path.join(active_ws.path, dest_rel)
                    os.makedirs(os.path.dirname(dest_abs), exist_ok=True)

                    shutil.copy2(abs_src, dest_abs)
                    manifest.assets.append(
                        Asset(
                            id=item_id,
                            type=AssetType.COPY,
                            source=dest_rel,
                            target=target,
                            permissions=None,
                            owner=None,
                            conflict_strategy=ConflictStrategy.PROMPT,
                            encrypted=False,
                            template_vars=None,
                        )
                    )
                    manifest.profiles[profile].assets.append(item_id)

                # Persist manifest
                with open(manifest_path, "w", encoding="utf-8") as f:
                    data = manifest.model_dump(mode="json", exclude_none=True)
                    import yaml

                    yaml.dump(data, f, sort_keys=False)

                self._send_response_json({"success": True, "imported_id": item_id})
            except Exception as e:
                self._send_response_json({"error": f"Import execution failed: {e}"}, 500)

        elif path == "/api/action/status":
            profile = payload.get("profile", "base")
            identity = payload.get("identity")

            try:
                report = StatusService.get_status(active_ws.path, profile, identity)
                self._send_response_json(report)
            except Exception as e:
                self._send_response_json({"error": f"Status drift analysis failed: {e}"}, 500)

        elif path == "/api/action/diff":
            profile = payload.get("profile", "base")
            asset_id = payload.get("asset_id")
            identity = payload.get("identity")

            if not asset_id:
                self._send_response_json({"error": "asset_id is required"}, 400)
                return

            try:
                diff_text = StatusService.get_diff(active_ws.path, profile, asset_id, identity)
                lines = diff_text.splitlines() if diff_text else []
                self._send_response_json({"diff_lines": lines})
            except Exception as e:
                self._send_response_json({"error": f"Failed to compute diff: {e}"}, 500)

        elif path == "/api/action/doctor":
            profile = payload.get("profile")

            try:
                report = DoctorService.check_health(active_ws.path, profile)
                self._send_response_json(report)
            except Exception as e:
                self._send_response_json({"error": f"Diagnostics clinic run failed: {e}"}, 500)

        elif path == "/api/action/restore":
            profile = payload.get("profile", "base")
            identity = payload.get("identity")
            dry_run = bool(payload.get("dry_run"))

            # Capture all logging stream
            log_stream = StringIO()
            handler = logging.StreamHandler(log_stream)
            handler.setFormatter(logging.Formatter("[rv] %(levelname)s - %(message)s"))
            root_logger = logging.getLogger()
            root_logger.addHandler(handler)
            original_level = root_logger.level
            root_logger.setLevel(logging.INFO)

            tx_id = None
            error = None
            try:
                tx_id = RestoreService.restore(
                    repo_dir=active_ws.path,
                    profile_name=profile,
                    identity_path=identity,
                    interactive=False,
                    dry_run=dry_run,
                    no_plugins=False,
                )
            except Exception as e:
                error = str(e)
            finally:
                root_logger.removeHandler(handler)
                root_logger.setLevel(original_level)

            logs = log_stream.getvalue()

            if error:
                self._send_response_json(
                    {
                        "success": False,
                        "error": error,
                        "logs": logs,
                    },
                    500,
                )
            else:
                self._send_response_json(
                    {
                        "success": True,
                        "tx_id": tx_id,
                        "logs": logs,
                    }
                )

        elif path == "/api/action/keygen":
            try:
                public_key, private_key = AgeEncryptor.generate_keypair()
                self._send_response_json({"public_key": public_key, "private_key": private_key})
            except Exception as e:
                self._send_response_json({"error": f"Failed to generate Age keypair: {e}"}, 500)

        elif path == "/api/action/recovery/list":
            try:
                journals = RecoveryService.list_incomplete_journals()
                serialized = []
                for j in journals:
                    serialized.append(
                        {
                            "tx_id": j.tx_id,
                            "timestamp": j.timestamp,
                            "status": j.status,
                            "entries": [
                                {"op": entry.op, "target": entry.target, "src_backup": entry.src_backup}
                                for entry in j.entries
                            ],
                        }
                    )
                self._send_response_json({"journals": serialized})
            except Exception as e:
                self._send_response_json({"error": f"Failed to list incomplete journals: {e}"}, 500)

        elif path == "/api/action/recovery/rollback":
            tx_id = payload.get("tx_id")
            if not tx_id:
                self._send_response_json({"error": "tx_id is required"}, 400)
                return
            try:
                journals = RecoveryService.list_incomplete_journals()
                target_journal = next((j for j in journals if j.tx_id == tx_id), None)
                if not target_journal:
                    self._send_response_json({"error": f"Incomplete transaction {tx_id} not found"}, 404)
                    return

                RecoveryService.rollback_journal(target_journal)
                self._send_response_json({"success": True, "message": f"Successfully rolled back transaction {tx_id}"})
            except Exception as e:
                self._send_response_json({"error": f"Failed to roll back transaction: {e}"}, 500)

        elif path == "/api/action/recovery/discard":
            tx_id = payload.get("tx_id")
            if not tx_id:
                self._send_response_json({"error": "tx_id is required"}, 400)
                return
            try:
                journals = RecoveryService.list_incomplete_journals()
                target_journal = next((j for j in journals if j.tx_id == tx_id), None)
                if not target_journal:
                    self._send_response_json({"error": f"Incomplete transaction {tx_id} not found"}, 404)
                    return

                RecoveryService.discard_journal(target_journal)
                self._send_response_json({"success": True, "message": f"Successfully discarded journal for {tx_id}"})
            except Exception as e:
                self._send_response_json({"error": f"Failed to discard journal: {e}"}, 500)

        else:
            self.send_error(404, "Not Found")


def start_gui_server(
    host: str = "127.0.0.1",
    port: int = 8080,
    open_browser: bool = True,
    auth_token: str | None = None,
    cors_wildcard: bool = False,
) -> None:
    """Instantiate and start the TCPServer serving the GUI.

    Args:
        host: Host address to bind to.
        port: TCP port to listen on.
        open_browser: Whether to open a browser tab on startup.
        auth_token: Optional authentication token for API access.
            If None, a 32-character random hex token is auto-generated and printed.
            If empty string (''), authentication is disabled entirely.
        cors_wildcard: If True, allow any origin via CORS (development only).
            When False (default), CORS is restricted to the loopback origin.
    """
    global _AUTH_TOKEN, _ALLOWED_ORIGIN

    loopback_hosts = {"127.0.0.1", "::1", "localhost"}
    if host not in loopback_hosts:
        print(
            f"\n[SECURITY WARNING] GUI server is binding to '{host}' (not loopback). "
            "The API will be accessible from the local network. "
            "Ensure your firewall rules are correct before proceeding.",
            file=sys.stderr,
        )

    # T-002: Set CORS origin to loopback-only unless --cors-wildcard is explicitly requested.
    if cors_wildcard:
        _ALLOWED_ORIGIN = "*"
        logger.warning("CORS wildcard enabled (--cors-wildcard). All origins are permitted.")
    else:
        scheme = "http"
        _ALLOWED_ORIGIN = f"{scheme}://{host}:{port}"

    # Configure authentication
    if auth_token == "":
        # Explicit empty string = disable auth
        _AUTH_TOKEN = None
        logger.warning("GUI auth is DISABLED. All API endpoints are unauthenticated.")
    elif auth_token is None:
        # Auto-generate a secure random token
        import secrets as _secrets

        _AUTH_TOKEN = _secrets.token_hex(32)
    else:
        _AUTH_TOKEN = auth_token

    server_address = (host, port)

    # Enable address reuse to prevent bind issues on fast restarts
    TCPServer.allow_reuse_address = True

    try:
        httpd = TCPServer(server_address, WebGUIRequestHandler)
    except OSError as e:
        if "Address already in use" in str(e):
            print(f"[rv] Port {port} is occupied. Scanning for next available port...", file=sys.stderr)
            start_gui_server(host=host, port=port + 1, open_browser=open_browser, auth_token=auth_token)
            return
        raise e

    url = f"http://{host}:{port}"
    print("\n=======================================================")
    print("  🌌 Revive Cosmic Web GUI Dashboard")
    print(f"  Serving at: {url}")
    if _AUTH_TOKEN:
        print(f"  Auth Token: {_AUTH_TOKEN}")
        print(f"  API access: {url}/api/status?token={_AUTH_TOKEN}")
    else:
        print("  Auth: DISABLED (all API endpoints public)")
    print("=======================================================\n")

    if open_browser:
        try:
            webbrowser.open(url)
        except Exception as e:
            logger.warning(f"Failed to launch browser automatically: {e}")

    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\n[rv] Web GUI server stopped.")
        httpd.server_close()
