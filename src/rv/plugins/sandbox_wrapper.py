"""Sandbox execution wrapper. Runs inside the sandboxed subprocess.

Implements defense-in-depth security:
  1. Import-time module blocking via __import__ allowlist
  2. Resource limits (RLIMIT_CPU, RLIMIT_AS) applied before ctypes is blocked
  3. Filesystem access gated to whitelisted directories
  4. Network and shell execution patched out per plugin permissions
  5. os._exit interception to prevent silent process termination
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
from typing import Any, Mapping, NoReturn, Sequence

# ---------------------------------------------------------------------------
# Step 0: Apply resource limits BEFORE blocking ctypes/resource
# ---------------------------------------------------------------------------
try:
    import resource  # POSIX only

    # Limit CPU time to 310 seconds (slightly above max plugin timeout of 300s)
    _cpu_limit = 310
    try:
        resource.setrlimit(resource.RLIMIT_CPU, (_cpu_limit, _cpu_limit))
    except (ValueError, OSError):
        pass  # Ignore if limits exceed hard limit

    # Limit virtual address space to 2 GiB to prevent memory bombs
    _mem_limit = 2 * 1024 * 1024 * 1024  # 2 GiB
    try:
        resource.setrlimit(resource.RLIMIT_AS, (_mem_limit, _mem_limit))
    except (ValueError, OSError):
        pass  # Best-effort — may fail in containers

except ImportError:
    pass  # Not POSIX (Windows), skip resource limits

# Store originals before any patching
_original_import = builtins.__import__
_original_open = builtins.open
_original_socket = socket.socket
_original_popen = subprocess.Popen

# ---------------------------------------------------------------------------
# Forbidden module allowlist enforcement
# ---------------------------------------------------------------------------
# These modules are known sandbox escape vectors and must be blocked.
_BLOCKED_MODULES: frozenset[str] = frozenset(
    [
        "ctypes",
        "ctypes.util",
        "ctypes._endian",
        "cffi",
        "cffi.api",
        "importlib",
        "importlib._bootstrap",
        "importlib._bootstrap_external",
        "importlib.util",
        "importlib.machinery",
        "imp",
        "gc",
        "pickle",
        "pickletools",
        "marshal",
        "ast",
        "code",
        "codeop",
        "compile",
        "pty",
        "termios",
        "tty",
        "readline",
        "rlcompleter",
    ]
)

_plugin_entrypoint_abs: str | None = None
_plugin_dir: str | None = None


def _get_importing_frame() -> Any:
    """Walks up stack to find first frame not in python import or sandbox machinery."""
    frame: Any = sys._getframe(1)
    while frame:
        filename = frame.f_code.co_filename
        if filename:
            # Skip python's import/runpy/sandbox machinery
            if (
                "importlib" not in filename
                and "runpy" not in filename
                and "<frozen" not in filename
                and "sandbox_wrapper" not in filename
            ):
                return frame
        frame = frame.f_back
    return None


def _is_called_from_plugin() -> bool:
    """Helper to detect if the calling execution frame belongs to the plugin module code."""
    if _plugin_entrypoint_abs is None or _plugin_dir is None:
        return False
    frame = _get_importing_frame()
    if frame:
        filename = frame.f_code.co_filename
        if filename:
            try:
                abs_filename = os.path.abspath(filename)
                if abs_filename == _plugin_entrypoint_abs or abs_filename.startswith(_plugin_dir + os.sep):
                    return True
            except Exception:
                pass
    return False


class _SandboxedSysModules(dict[str, Any]):
    """Custom sys.modules dictionary that blocks access to forbidden modules from plugin frames."""

    def __getitem__(self, item: Any) -> Any:
        if isinstance(item, str):
            top_level = item.split(".")[0]
            if (item in _BLOCKED_MODULES or top_level in _BLOCKED_MODULES) and _is_called_from_plugin():
                raise KeyError(f"Access to blocked module '{item}' is forbidden by the sandbox.")
        return super().__getitem__(item)

    def get(self, item: Any, default: Any = None) -> Any:
        if isinstance(item, str):
            top_level = item.split(".")[0]
            if (item in _BLOCKED_MODULES or top_level in _BLOCKED_MODULES) and _is_called_from_plugin():
                return default
        return super().get(item, default)


# Install custom sys.modules wrapper to block direct dict lookup evasion
sys.modules = _SandboxedSysModules(sys.modules)


def _sandboxed_import(
    name: str,
    globals: Mapping[str, object] | None = None,
    locals: Mapping[str, object] | None = None,
    fromlist: Sequence[str] | None = None,
    level: int = 0,
) -> Any:
    """Patched __import__ that blocks forbidden modules."""
    # Check exact name and top-level package name
    top_level = name.split(".")[0]
    if (name in _BLOCKED_MODULES or top_level in _BLOCKED_MODULES) and _is_called_from_plugin():
        raise ImportError(
            f"Import of '{name}' is blocked by the Revive plugin security sandbox. "
            "This module is a known sandbox escape vector."
        )
    return _original_import(name, globals, locals, fromlist, level)


def _install_import_hook() -> None:
    """Install the sandboxed __import__ into builtins."""
    builtins.__import__ = _sandboxed_import


# ---------------------------------------------------------------------------
# os._exit interception
# ---------------------------------------------------------------------------
_original_os_exit = os._exit


def _sandboxed_os_exit(status: int) -> NoReturn:
    """Intercept os._exit to log and use a clean sys.exit instead."""
    print(
        json.dumps({"status": "error", "message": f"Plugin attempted os._exit({status}), intercepted by sandbox"}),
        file=sys.stderr,
    )
    sys.exit(1)


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

    global _plugin_entrypoint_abs, _plugin_dir
    _plugin_entrypoint_abs = os.path.abspath(entrypoint_path)
    _plugin_dir = os.path.abspath(os.path.dirname(_plugin_entrypoint_abs))

    # Validate entrypoint is within its parent directory (no path escape)
    entrypoint_abs = _plugin_entrypoint_abs
    plugin_dir = _plugin_dir
    if not entrypoint_abs.startswith(plugin_dir + os.sep) and entrypoint_abs != plugin_dir:
        print(
            json.dumps({"status": "error", "message": f"Entrypoint path escapes plugin directory: {entrypoint_path}"}),
            file=sys.stderr,
        )
        sys.exit(1)

    # Export context environment variables
    os.environ["REVIVE_HOOK"] = hook_type
    os.environ["REVIVE_CONTEXT"] = context_raw

    # Build directory whitelist
    allowed_dirs: list[str] = [
        plugin_dir,
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

    # Install the import blocklist hook
    _install_import_hook()

    # Intercept os._exit
    os._exit = _sandboxed_os_exit

    # 1. Enforce shell execution restriction
    if not permissions.get("shell"):

        def sandboxed_popen(*args: Any, **kwargs: Any) -> Any:
            raise PermissionError("Process/shell execution is not allowed by plugin permissions")

        subprocess.Popen = sandboxed_popen  # type: ignore[misc,assignment]
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

        socket.socket = sandboxed_socket  # type: ignore[misc,assignment]

    # 3. Enforce filesystem access restriction
    def sandboxed_open(file: Any, mode: str = "r", *args: Any, **kwargs: Any) -> Any:
        if isinstance(file, (str, bytes)):
            filepath = os.fsdecode(file)
            if not is_path_allowed(filepath):
                raise PermissionError(f"Filesystem access to '{filepath}' is not allowed by plugin permissions")
        return _original_open(file, mode, *args, **kwargs)

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
        "remove",
        "unlink",
        "rename",
        "mkdir",
        "rmdir",
        "makedirs",
        "removedirs",
        "listdir",
        "scandir",
        "stat",
    ]:
        if hasattr(os, func_name):
            setattr(os, func_name, make_sandboxed_os_func(getattr(os, func_name)))

    # Execute the actual plugin entry point
    try:
        os.chdir(os.path.dirname(entrypoint_abs))
        runpy.run_path(entrypoint_abs, run_name="__main__")
    except SystemExit as e:
        # Allow clean sys.exit(0) from plugins
        if e.code != 0:
            print(
                json.dumps({"status": "error", "message": f"Plugin exited with non-zero code: {e.code}"}),
                file=sys.stderr,
            )
        sys.exit(e.code if isinstance(e.code, int) else 1)
    except Exception as e:
        print(json.dumps({"status": "error", "message": f"Plugin execution failed: {e}"}), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
