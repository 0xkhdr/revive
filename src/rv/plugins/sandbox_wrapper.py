"""Sandbox execution wrapper. Runs inside the sandboxed subprocess.
"""

import base64
import builtins
import json
import os
import runpy
import socket
import subprocess
import sys
import tempfile
from typing import Any

# Store originals
original_open = builtins.open
original_socket = socket.socket
original_popen = subprocess.Popen


def main() -> None:
    """Prepares the sandbox environment and runs the plugin."""
    if len(sys.argv) < 5:
        print(json.dumps({"status": "error", "message": "Invalid arguments to sandbox wrapper"}), file=sys.stderr)
        sys.exit(1)

    entrypoint_path = sys.argv[1]
    permissions_raw = base64.b64decode(sys.argv[2]).decode("utf-8")
    context_raw = base64.b64decode(sys.argv[3]).decode("utf-8")
    hook_type = sys.argv[4]

    try:
        permissions = json.loads(permissions_raw)
        context = json.loads(context_raw)
    except Exception as e:
        print(json.dumps({"status": "error", "message": f"Malformed parameters: {e}"}), file=sys.stderr)
        sys.exit(1)

    # Export context environment variables
    os.environ["REVIVE_HOOK"] = hook_type
    os.environ["REVIVE_CONTEXT"] = context_raw

    # Build directory whitelist
    allowed_dirs = [
        os.path.abspath(os.path.expanduser(os.path.dirname(entrypoint_path))),
        os.path.abspath(os.path.expanduser(context["repo_dir"])),
        os.path.abspath(os.path.expanduser(tempfile.gettempdir())),
    ]
    for p in permissions.get("allowed_paths", []):
        if p:
            allowed_dirs.append(os.path.abspath(os.path.expanduser(p)))
    for t in context.get("targets", []):
        if t:
            allowed_dirs.append(os.path.abspath(os.path.expanduser(t)))
            allowed_dirs.append(os.path.abspath(os.path.expanduser(os.path.dirname(t))))

    def is_path_allowed(filepath: str) -> bool:
        """Checks if a resolved path falls within the whitelisted directories."""
        try:
            abs_path = os.path.abspath(os.path.expanduser(filepath))
        except Exception:
            return False
        for allowed in allowed_dirs:
            try:
                if abs_path == allowed or abs_path.startswith(allowed + os.sep):
                    return True
            except Exception:
                continue
        return False

    # 1. Enforce shell execution restriction
    if not permissions.get("shell"):
        def sandboxed_popen(*args: Any, **kwargs: Any) -> Any:
            raise PermissionError("Process/shell execution is not allowed by plugin permissions")

        subprocess.Popen = sandboxed_popen  # type: ignore[assignment, misc]
        subprocess.run = sandboxed_popen

        def sandboxed_system(*args: Any, **kwargs: Any) -> Any:
            raise PermissionError("Shell execution is not allowed by plugin permissions")

        os.system = sandboxed_system
        os.popen = sandboxed_system
        if hasattr(os, "spawnl"):
            os.spawnl = sandboxed_system
        if hasattr(os, "spawnle"):
            os.spawnle = sandboxed_system
        if hasattr(os, "spawnlp"):
            os.spawnlp = sandboxed_system
        if hasattr(os, "spawnlpe"):
            os.spawnlpe = sandboxed_system
        if hasattr(os, "spawnv"):
            os.spawnv = sandboxed_system
        if hasattr(os, "spawnve"):
            os.spawnve = sandboxed_system
        if hasattr(os, "spawnvp"):
            os.spawnvp = sandboxed_system
        if hasattr(os, "spawnvpe"):
            os.spawnvpe = sandboxed_system
        if hasattr(os, "posix_spawn"):
            os.posix_spawn = sandboxed_system
        if hasattr(os, "posix_spawnp"):
            os.posix_spawnp = sandboxed_system

    # 2. Enforce network access restriction
    if not permissions.get("network"):
        def sandboxed_socket(*args: Any, **kwargs: Any) -> Any:
            raise PermissionError("Network access is not allowed by plugin permissions")

        socket.socket = sandboxed_socket  # type: ignore[assignment, misc]

    # 3. Enforce filesystem access restriction
    def sandboxed_open(file: Any, mode: str = "r", *args: Any, **kwargs: Any) -> Any:
        if isinstance(file, (str, bytes)):
            filepath = os.fsdecode(file)
            if not is_path_allowed(filepath):
                raise PermissionError(f"Filesystem access to '{filepath}' is not allowed by plugin permissions")
        return original_open(file, mode, *args, **kwargs)

    builtins.open = sandboxed_open

    def make_sandboxed_os_func(orig_func: Any) -> Any:
        if orig_func is None:
            return None

        def wrapper(path: Any, *args: Any, **kwargs: Any) -> Any:
            if isinstance(path, (str, bytes)):
                filepath = os.fsdecode(path)
                if not is_path_allowed(filepath):
                    raise PermissionError(f"Filesystem access to '{filepath}' is not allowed by plugin permissions")
            return orig_func(path, *args, **kwargs)

        return wrapper

    for func_name in [
        "remove", "unlink", "rename", "mkdir", "rmdir", "makedirs",
        "removedirs", "listdir", "scandir", "stat"
    ]:
        if hasattr(os, func_name):
            setattr(os, func_name, make_sandboxed_os_func(getattr(os, func_name)))

    # Execute the actual plugin entry point
    try:
        os.chdir(os.path.dirname(os.path.abspath(entrypoint_path)))
        runpy.run_path(entrypoint_path, run_name="__main__")
    except Exception as e:
        print(json.dumps({"status": "error", "message": f"Plugin execution failed: {e}"}), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
