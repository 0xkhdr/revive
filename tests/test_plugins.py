"""Comprehensive test suite for the revive plugin system and sandboxed hook execution."""

import os
import shutil
import sys
import tempfile
import time
from collections.abc import Generator
from unittest.mock import MagicMock, patch

import pytest
import yaml

from rv.plugins.context import ReviveContext
from rv.plugins.loader import Plugin, PluginLoader, PluginManifest, PluginPermissions
from rv.plugins.sandbox import SandboxRunner
from rv.services.restore import RestoreService
from rv.transactions.context import TransactionContext


@pytest.fixture
def temp_workspace() -> Generator[str, None, None]:
    """Creates a temporary workspace directory."""
    with tempfile.TemporaryDirectory() as tmpdir:
        yield tmpdir


def test_plugin_loader_parse_manifest(temp_workspace: str) -> None:
    """Tests loading and parsing a plugin.yaml manifest."""
    plugin_dir = os.path.join(temp_workspace, "test-plugin")
    os.makedirs(plugin_dir, exist_ok=True)

    manifest_data = {
        "name": "my-test-plugin",
        "version": "1.2.3",
        "entrypoint": "main.py",
        "permissions": {"network": True, "shell": False, "allowed_paths": ["/tmp/allowed"]},
        "hooks": ["pre-restore", "post-restore"],
        "timeout": 45,
    }

    yaml_path = os.path.join(plugin_dir, "plugin.yaml")
    with open(yaml_path, "w", encoding="utf-8") as f:
        yaml.safe_dump(manifest_data, f)

    plugin = PluginLoader.load_from_directory(plugin_dir)
    assert plugin is not None
    assert plugin.manifest.name == "my-test-plugin"
    assert plugin.manifest.version == "1.2.3"
    assert plugin.manifest.entrypoint == "main.py"
    assert plugin.entrypoint_path == os.path.abspath(os.path.join(plugin_dir, "main.py"))
    assert plugin.manifest.permissions.network is True
    assert plugin.manifest.permissions.shell is False
    assert plugin.manifest.permissions.allowed_paths == ["/tmp/allowed"]
    assert "pre-restore" in plugin.manifest.hooks
    assert plugin.manifest.timeout == 45


def test_plugin_loader_invalid_manifest(temp_workspace: str) -> None:
    """Tests loading a malformed or incomplete plugin manifest."""
    plugin_dir = os.path.join(temp_workspace, "bad-plugin")
    os.makedirs(plugin_dir, exist_ok=True)

    # Missing entrypoint
    manifest_data = {"name": "bad-plugin", "version": "1.0.0"}

    yaml_path = os.path.join(plugin_dir, "plugin.yaml")
    with open(yaml_path, "w", encoding="utf-8") as f:
        yaml.safe_dump(manifest_data, f)

    plugin = PluginLoader.load_from_directory(plugin_dir)
    assert plugin is None


def test_plugin_loader_discover_priority(temp_workspace: str) -> None:
    """Tests plugin loader precedence, duplicate resolution, and discovery."""
    repo_plugins = os.path.join(temp_workspace, "plugins")
    os.makedirs(repo_plugins, exist_ok=True)

    # 1. Create a repo plugin
    p1_dir = os.path.join(repo_plugins, "plugin-a")
    os.makedirs(p1_dir, exist_ok=True)
    with open(os.path.join(p1_dir, "plugin.yaml"), "w", encoding="utf-8") as f:
        yaml.safe_dump({"name": "plugin-a", "version": "1.0.0-repo", "entrypoint": "main.py"}, f)

    # 2. Discover plugins
    plugins = PluginLoader.discover_plugins(temp_workspace)
    # Should find plugin-a and the builtins
    names = [p.manifest.name for p in plugins]
    assert "plugin-a" in names
    assert "mcp-config" in names
    assert "claude-prompts" in names
    assert "python-skills" in names


def test_sandbox_runner_network_block(temp_workspace: str) -> None:
    """Tests that a plugin attempting to open a network socket gets blocked when network: false."""
    plugin_dir = os.path.join(temp_workspace, "net-plugin")
    os.makedirs(plugin_dir, exist_ok=True)

    with open(os.path.join(plugin_dir, "plugin.yaml"), "w", encoding="utf-8") as f:
        yaml.safe_dump(
            {"name": "net-plugin", "version": "1.0.0", "entrypoint": "main.py", "permissions": {"network": False}}, f
        )

    with open(os.path.join(plugin_dir, "main.py"), "w", encoding="utf-8") as f:
        f.write("""import socket
import json

try:
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.connect(("127.0.0.1", 80))
    print(json.dumps({"status": "error", "message": "Socket connection succeeded unexpectedly"}))
except PermissionError as e:
    print(json.dumps({"status": "success", "message": str(e)}))
""")

    plugin = PluginLoader.load_from_directory(plugin_dir)
    assert plugin is not None

    context = ReviveContext(
        repo_dir=temp_workspace, profile_name="base", dry_run=False, targets=[], hook_type="pre-restore"
    )

    res = SandboxRunner.run_plugin(plugin, context)
    assert res["status"] == "success"
    assert "Network access is not allowed" in res["message"]


def test_sandbox_runner_shell_block(temp_workspace: str) -> None:
    """Tests that a plugin attempting subprocess/shell executions gets blocked when shell: false."""
    plugin_dir = os.path.join(temp_workspace, "shell-plugin")
    os.makedirs(plugin_dir, exist_ok=True)

    with open(os.path.join(plugin_dir, "plugin.yaml"), "w", encoding="utf-8") as f:
        yaml.safe_dump(
            {"name": "shell-plugin", "version": "1.0.0", "entrypoint": "main.py", "permissions": {"shell": False}}, f
        )

    with open(os.path.join(plugin_dir, "main.py"), "w", encoding="utf-8") as f:
        f.write("""import subprocess
import os
import json

try:
    subprocess.run("echo hello", shell=True)
    print(json.dumps({"status": "error", "message": "Subprocess run succeeded unexpectedly"}))
except PermissionError as e:
    # Also verify os.system
    try:
        os.system("echo hello")
        print(json.dumps({"status": "error", "message": "os.system succeeded unexpectedly"}))
    except PermissionError as e2:
        print(json.dumps({"status": "success", "message": f"Shell blocked successfully: {e}"}))
""")

    plugin = PluginLoader.load_from_directory(plugin_dir)
    assert plugin is not None

    context = ReviveContext(
        repo_dir=temp_workspace, profile_name="base", dry_run=False, targets=[], hook_type="pre-restore"
    )

    res = SandboxRunner.run_plugin(plugin, context)
    assert res["status"] == "success"
    assert "blocked successfully" in res["message"]


def test_sandbox_runner_filesystem_block(temp_workspace: str) -> None:
    """Tests that filesystem access outside whitelisted paths gets blocked."""
    plugin_dir = os.path.join(temp_workspace, "fs-plugin")
    os.makedirs(plugin_dir, exist_ok=True)

    forbidden_file = "/etc/hosts"

    with open(os.path.join(plugin_dir, "plugin.yaml"), "w", encoding="utf-8") as f:
        yaml.safe_dump(
            {
                "name": "fs-plugin",
                "version": "1.0.0",
                "entrypoint": "main.py",
                "permissions": {
                    "allowed_paths": []  # Do not explicitly whitelist anything outside default CWD/repo
                },
            },
            f,
        )

    with open(os.path.join(plugin_dir, "main.py"), "w", encoding="utf-8") as f:
        f.write(f"""import json
import os

filepath = "{forbidden_file}"
try:
    with open(filepath, "r") as f:
        content = f.read()
    print(json.dumps({{"status": "error", "message": "Read forbidden file succeeded"}}))
except PermissionError as e:
    print(json.dumps({{"status": "success", "message": f"Filesystem blocked successfully: {{e}}"}}))
""")

    plugin = PluginLoader.load_from_directory(plugin_dir)
    assert plugin is not None

    context = ReviveContext(
        repo_dir=plugin_dir,  # Restrict repo path to plugin_dir itself so forbidden_file is outside repo
        profile_name="base",
        dry_run=False,
        targets=[],
        hook_type="pre-restore",
    )

    res = SandboxRunner.run_plugin(plugin, context)
    assert res["status"] == "success"
    assert "Filesystem access to" in res["message"]


def test_sandbox_runner_timeout(temp_workspace: str) -> None:
    """Tests that plugins executing in an infinite loop are terminated by timeout constraints."""
    plugin_dir = os.path.join(temp_workspace, "loop-plugin")
    os.makedirs(plugin_dir, exist_ok=True)

    with open(os.path.join(plugin_dir, "plugin.yaml"), "w", encoding="utf-8") as f:
        yaml.safe_dump(
            {
                "name": "loop-plugin",
                "version": "1.0.0",
                "entrypoint": "main.py",
                "timeout": 1,  # 1 second timeout
            },
            f,
        )

    with open(os.path.join(plugin_dir, "main.py"), "w", encoding="utf-8") as f:
        f.write("""import time
while True:
    time.sleep(0.1)
""")

    plugin = PluginLoader.load_from_directory(plugin_dir)
    assert plugin is not None

    context = ReviveContext(
        repo_dir=temp_workspace, profile_name="base", dry_run=False, targets=[], hook_type="pre-restore"
    )

    with pytest.raises(TimeoutError, match="timed out after 1 seconds"):
        SandboxRunner.run_plugin(plugin, context)


def test_restore_service_plugin_execution_abort(temp_workspace: str) -> None:
    """Tests that hook failure aborts the restore transaction."""
    # Scaffold repository
    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "file_a",
                "type": "copy",
                "source": "assets/file_a",
                "target": os.path.join(temp_workspace, "system_a"),
            }
        ],
        "profiles": {"base": {"assets": ["file_a"]}},
    }

    with open(os.path.join(temp_workspace, "manifest.yaml"), "w") as f:
        yaml.safe_dump(manifest_data, f)

    os.makedirs(os.path.join(temp_workspace, "assets"), exist_ok=True)
    with open(os.path.join(temp_workspace, "assets", "file_a"), "w") as f:
        f.write("repo asset")

    # Create a failing pre-restore hook
    plugins_dir = os.path.join(temp_workspace, "plugins")
    os.makedirs(plugins_dir, exist_ok=True)
    hook_dir = os.path.join(plugins_dir, "bad-hook")
    os.makedirs(hook_dir, exist_ok=True)

    with open(os.path.join(hook_dir, "plugin.yaml"), "w") as f:
        yaml.safe_dump({"name": "bad-hook", "version": "1.0.0", "entrypoint": "main.py", "hooks": ["pre-restore"]}, f)

    with open(os.path.join(hook_dir, "main.py"), "w") as f:
        f.write("""import sys
sys.exit(1) # Fail immediately
""")

    # Try restore, should raise RuntimeError and not write system_a
    with pytest.raises(RuntimeError, match="Plugin 'bad-hook' execution failed"):
        RestoreService.restore(repo_dir=temp_workspace, profile_name="base", interactive=False)

    assert not os.path.exists(os.path.join(temp_workspace, "system_a"))


def test_restore_service_no_plugins_escape_hatch(temp_workspace: str) -> None:
    """Tests that the --no-plugins flag skips plugin execution entirely."""
    manifest_data = {
        "version": 2,
        "assets": [
            {
                "id": "file_a",
                "type": "copy",
                "source": "assets/file_a",
                "target": os.path.join(temp_workspace, "system_a"),
            }
        ],
        "profiles": {"base": {"assets": ["file_a"]}},
    }

    with open(os.path.join(temp_workspace, "manifest.yaml"), "w") as f:
        yaml.safe_dump(manifest_data, f)

    os.makedirs(os.path.join(temp_workspace, "assets"), exist_ok=True)
    with open(os.path.join(temp_workspace, "assets", "file_a"), "w") as f:
        f.write("repo asset")

    # Create a failing pre-restore hook
    plugins_dir = os.path.join(temp_workspace, "plugins")
    os.makedirs(plugins_dir, exist_ok=True)
    hook_dir = os.path.join(plugins_dir, "bad-hook")
    os.makedirs(hook_dir, exist_ok=True)

    with open(os.path.join(hook_dir, "plugin.yaml"), "w") as f:
        yaml.safe_dump({"name": "bad-hook", "version": "1.0.0", "entrypoint": "main.py", "hooks": ["pre-restore"]}, f)

    with open(os.path.join(hook_dir, "main.py"), "w") as f:
        f.write("""import sys
sys.exit(1)
""")

    # Running with no_plugins=True should completely ignore bad-hook and succeed!
    tx_id = RestoreService.restore(repo_dir=temp_workspace, profile_name="base", interactive=False, no_plugins=True)

    assert tx_id is not None
    assert os.path.exists(os.path.join(temp_workspace, "system_a"))


def test_builtin_plugins_mcp_config(temp_workspace: str) -> None:
    """Tests that the built-in mcp-config plugin correctly copies config."""
    # Create mock context
    context = ReviveContext(
        repo_dir=temp_workspace, profile_name="base", dry_run=False, targets=[], hook_type="post-restore"
    )

    # 1. MCP config missing in repo -> exits successfully with skip msg
    plugins = PluginLoader.discover_plugins(temp_workspace)
    mcp_plugin = [p for p in plugins if p.manifest.name == "mcp-config"][0]

    res = SandboxRunner.run_plugin(mcp_plugin, context)
    assert res["status"] == "success"
    assert "skipping" in res["message"]

    # 2. MCP config exists in repo -> copies successfully
    with open(os.path.join(temp_workspace, "mcp-config.json"), "w") as f:
        json_data = {"mcpServers": {"test": {"command": "echo"}}}
        import json

        json.dump(json_data, f)

    # Mock user directory and platform
    with (
        patch("os.path.expanduser", return_value=temp_workspace),
        patch.dict(os.environ, {"HOME": temp_workspace}),
        patch("sys.platform", "linux"),
    ):
        # We modify the plugin's allowed paths temporarily to contain our temp_workspace
        # so the sandbox doesn't block writing there
        mcp_plugin.manifest.permissions.allowed_paths = [temp_workspace]

        # We mock target file creation path to point inside our temp directory
        target_dir = os.path.join(temp_workspace, ".config", "Claude")
        os.makedirs(target_dir, exist_ok=True)

        res = SandboxRunner.run_plugin(mcp_plugin, context)
        assert res["status"] == "success"

        copied_file = os.path.join(target_dir, "claude_desktop_config.json")
        assert os.path.exists(copied_file)
        with open(copied_file) as f:
            copied_data = json.load(f)
            assert copied_data["mcpServers"]["test"]["command"] == "echo"


def test_builtin_plugins_claude_prompts(temp_workspace: str) -> None:
    """Tests that the built-in claude-prompts plugin correctly copies prompts."""
    context = ReviveContext(
        repo_dir=temp_workspace, profile_name="base", dry_run=False, targets=[], hook_type="post-restore"
    )

    plugins = PluginLoader.discover_plugins(temp_workspace)
    prompts_plugin = [p for p in plugins if p.manifest.name == "claude-prompts"][0]

    # No prompts in repo
    res = SandboxRunner.run_plugin(prompts_plugin, context)
    assert res["status"] == "success"
    assert "skipping" in res["message"]

    # Create prompts in repo
    prompts_dir = os.path.join(temp_workspace, "claude-prompts")
    os.makedirs(prompts_dir, exist_ok=True)
    with open(os.path.join(prompts_dir, "sys.prompt"), "w") as f:
        f.write("system prompt")

    with (
        patch("os.path.expanduser", return_value=temp_workspace),
        patch.dict(os.environ, {"HOME": temp_workspace}),
        patch("sys.platform", "linux"),
    ):
        prompts_plugin.manifest.permissions.allowed_paths = [temp_workspace]

        target_dir = os.path.join(temp_workspace, ".config", "ClaudeCode")
        os.makedirs(target_dir, exist_ok=True)

        res = SandboxRunner.run_plugin(prompts_plugin, context)
        assert res["status"] == "success"
        assert os.path.exists(os.path.join(target_dir, "sys.prompt"))


def test_builtin_plugins_python_skills(temp_workspace: str) -> None:
    """Tests that the built-in python-skills plugin correctly copies skills."""
    context = ReviveContext(
        repo_dir=temp_workspace, profile_name="base", dry_run=False, targets=[], hook_type="post-restore"
    )

    plugins = PluginLoader.discover_plugins(temp_workspace)
    skills_plugin = [p for p in plugins if p.manifest.name == "python-skills"][0]

    # No skills in repo
    res = SandboxRunner.run_plugin(skills_plugin, context)
    assert res["status"] == "success"
    assert "skipping" in res["message"]

    # Create skills in repo
    skills_dir = os.path.join(temp_workspace, "skills")
    os.makedirs(skills_dir, exist_ok=True)
    with open(os.path.join(skills_dir, "skill_a.py"), "w") as f:
        f.write("class SkillA:")

    with patch("os.path.expanduser", return_value=temp_workspace), patch.dict(os.environ, {"HOME": temp_workspace}):
        skills_plugin.manifest.permissions.allowed_paths = [temp_workspace]

        target_dir = os.path.join(temp_workspace, ".config", "rv", "skills")
        os.makedirs(os.path.dirname(target_dir), exist_ok=True)

        res = SandboxRunner.run_plugin(skills_plugin, context)
        assert res["status"] == "success"
        assert os.path.exists(os.path.join(target_dir, "skill_a.py"))


def test_sandbox_runner_os_functions_block(temp_workspace: str) -> None:
    """Tests that a plugin attempting forbidden OS functions is successfully blocked."""
    plugin_dir = os.path.join(temp_workspace, "os-plugin")
    os.makedirs(plugin_dir, exist_ok=True)

    with open(os.path.join(plugin_dir, "plugin.yaml"), "w", encoding="utf-8") as f:
        yaml.safe_dump(
            {
                "name": "os-plugin",
                "version": "1.0.0",
                "entrypoint": "main.py",
                "permissions": {"allowed_paths": []},
            },
            f,
        )

    with open(os.path.join(plugin_dir, "main.py"), "w", encoding="utf-8") as f:
        f.write("""import os
import json

errors = []
forbidden = "/etc/forbidden_path_test_123"

funcs = [
    ("remove", lambda: os.remove(forbidden)),
    ("unlink", lambda: os.unlink(forbidden)),
    ("mkdir", lambda: os.mkdir(forbidden)),
    ("rmdir", lambda: os.rmdir(forbidden)),
    ("rename", lambda: os.rename(forbidden, "/tmp/another")),
    ("listdir", lambda: os.listdir(forbidden)),
]

for name, func in funcs:
    try:
        func()
        errors.append(f"{name} succeeded unexpectedly")
    except PermissionError:
        pass
    except Exception as e:
        errors.append(f"{name} raised unexpected exception: {e}")

if errors:
    print(json.dumps({"status": "error", "message": ", ".join(errors)}))
else:
    print(json.dumps({"status": "success", "message": "All forbidden OS functions blocked"}))
""")

    plugin = PluginLoader.load_from_directory(plugin_dir)
    assert plugin is not None

    context = ReviveContext(
        repo_dir=plugin_dir,
        profile_name="base",
        dry_run=False,
        targets=[],
        hook_type="pre-restore",
    )

    res = SandboxRunner.run_plugin(plugin, context)
    assert res["status"] == "success"
    assert res["message"] == "All forbidden OS functions blocked"


def test_sandbox_runner_malformed_args(temp_workspace: str) -> None:
    """Tests that the sandbox wrapper fails gracefully with invalid/malformed arguments."""
    import subprocess

    # 1. Less than 5 arguments
    cmd = [sys.executable, "-m", "rv.plugins.sandbox_wrapper", "arg1", "arg2"]
    res = subprocess.run(cmd, capture_output=True, text=True)
    assert res.returncode == 1
    assert "Invalid arguments to sandbox wrapper" in res.stderr

    # 2. Malformed base64 params
    cmd = [sys.executable, "-m", "rv.plugins.sandbox_wrapper", "main.py", "invalid_b64!", "invalid_b64!", "pre-restore"]
    res = subprocess.run(cmd, capture_output=True, text=True)
    assert res.returncode == 1
    assert (
        "Malformed parameters" in res.stderr
        or "Invalid base64-encoded string" in res.stderr
        or "binascii.Error" in res.stderr
        or "Incorrect padding" in res.stderr
    )


def test_sandbox_runner_plugin_exception(temp_workspace: str) -> None:
    """Tests that plugins raising exceptions are caught and reported as failures by the SandboxRunner."""
    plugin_dir = os.path.join(temp_workspace, "err-plugin")
    os.makedirs(plugin_dir, exist_ok=True)

    with open(os.path.join(plugin_dir, "plugin.yaml"), "w", encoding="utf-8") as f:
        yaml.safe_dump(
            {
                "name": "err-plugin",
                "version": "1.0.0",
                "entrypoint": "main.py",
            },
            f,
        )

    with open(os.path.join(plugin_dir, "main.py"), "w", encoding="utf-8") as f:
        f.write("""raise ValueError("Intended plugin exception")\n""")

    plugin = PluginLoader.load_from_directory(plugin_dir)
    assert plugin is not None

    context = ReviveContext(
        repo_dir=temp_workspace,
        profile_name="base",
        dry_run=False,
        targets=[],
        hook_type="pre-restore",
    )

    with pytest.raises(RuntimeError, match="Plugin 'err-plugin' execution failed"):
        SandboxRunner.run_plugin(plugin, context)


def test_sandbox_runner_allowed_shell_and_network(temp_workspace: str) -> None:
    """Tests plugin execution with both shell and network permissions enabled, empty paths, and non-string OS paths."""
    plugin_dir = os.path.join(temp_workspace, "full-plugin")
    os.makedirs(plugin_dir, exist_ok=True)

    # plugin.yaml with network: true, shell: true, and empty values in allowed_paths
    with open(os.path.join(plugin_dir, "plugin.yaml"), "w", encoding="utf-8") as f:
        yaml.safe_dump(
            {
                "name": "full-plugin",
                "version": "1.0.0",
                "entrypoint": "main.py",
                "permissions": {
                    "network": True,
                    "shell": True,
                    "allowed_paths": ["", "/tmp/allowed-test"],
                },
            },
            f,
        )

    # main.py that runs subprocess, socket, and os functions with non-string path (e.g. integer file descriptor)
    with open(os.path.join(plugin_dir, "main.py"), "w", encoding="utf-8") as f:
        f.write("""import os
import socket
import subprocess
import json

# Verify network & shell run fine without PermissionError
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
p = subprocess.run(["echo", "hello"], capture_output=True)

# Try calling os function with non-string path (e.g., fd 0) to hit path type check bypass
try:
    os.stat(0)
except Exception:
    pass

print(json.dumps({"status": "success", "message": "Shell and network allowed successfully"}))
""")

    plugin = PluginLoader.load_from_directory(plugin_dir)
    assert plugin is not None

    # Context with empty string inside targets
    context = ReviveContext(
        repo_dir=temp_workspace,
        profile_name="base",
        dry_run=False,
        targets=["", os.path.join(temp_workspace, "target-file.txt")],
        hook_type="pre-restore",
    )

    res = SandboxRunner.run_plugin(plugin, context)
    assert res["status"] == "success"
    assert res["message"] == "Shell and network allowed successfully"
