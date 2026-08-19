# Plugin Authoring Guide

This guide explains how to write custom Revive plugins that run automated tasks before and after `rv restore`.

**Quick answer**: Plugins are sandboxed Python scripts that run on lifecycle hooks (`pre-restore`, `post-restore`).

---

## Overview

### What Can Plugins Do?

Plugins can:
- Run shell commands post-restore (e.g., rebuild search indices, restart services)
- Modify configuration files before/after restore
- Check system state and make decisions
- Send notifications when restore completes
- Integrate with external services (with `network: true` permission)

### What Plugins Can't Do

Plugins run in a sandbox that blocks:
- Writing outside allowed paths (`--allowed_paths` in `plugin.yaml`)
- Network access (unless `permissions.network: true`)
- Shell execution (unless `permissions.shell: true`)
- Access to dangerous Python modules (`ctypes`, `cffi`, `gc`, `importlib`)

---

## Plugin Structure

Each plugin is a directory with two required files:

```
plugins/
└── my-notifier/
    ├── plugin.yaml           # Metadata
    └── notify.py             # Entrypoint (Python script)
```

### plugin.yaml

Metadata file describing the plugin:

```yaml
name: "my-notifier"
version: "1.0.0"
entrypoint: "notify.py"
permissions:
  network: false              # Allow outbound network
  shell: false                # Allow subprocess execution
  allowed_paths: []           # Extra filesystem paths
hooks:
  - post-restore              # pre-restore or post-restore
timeout: 30                   # Max execution time (30s default, 300s max)
```

### Field Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | `str` | ✅ Yes | Unique plugin identifier (alphanumeric + hyphens) |
| `version` | `str` | ✅ Yes | Semantic version (e.g. `1.0.0`) |
| `entrypoint` | `str` | ✅ Yes | Path to Python entry script (relative to plugin directory) |
| `permissions.network` | `bool` | ❌ No | Allow `socket.socket` calls (default: `false`) |
| `permissions.shell` | `bool` | ❌ No | Allow `subprocess.Popen`, `os.system` (default: `false`) |
| `permissions.allowed_paths` | `list[str]` | ❌ No | Extra filesystem paths to allow (home paths supported: `~/.config/myapp`) |
| `hooks` | `list[str]` | ✅ Yes | Lifecycle hooks: `pre-restore` or `post-restore` |
| `timeout` | `int` | ❌ No | Max execution time in seconds (default: 30, max: 300) |

---

## Writing Plugin Code

### Minimal Example

**`plugins/hello/plugin.yaml`:**

```yaml
name: "hello"
version: "1.0.0"
entrypoint: "hello.py"
hooks:
  - post-restore
```

**`plugins/hello/hello.py`:**

```python
import json
import sys

def main() -> None:
    # Output success/error as JSON to stdout
    print(json.dumps({
        "status": "success",
        "message": "Hello, Revive!"
    }))
    sys.exit(0)

if __name__ == "__main__":
    main()
```

### Accessing Context

Plugins receive Revive context via the `REVIVE_CONTEXT` environment variable:

```python
import json
import os
import sys

def main() -> None:
    # 1. Parse context from environment
    context_raw = os.environ.get("REVIVE_CONTEXT")
    if not context_raw:
        print(json.dumps({
            "status": "error",
            "message": "Missing REVIVE_CONTEXT"
        }), file=sys.stderr)
        sys.exit(1)

    context = json.loads(context_raw)

    # 2. Access context fields
    profile_name = context.get("profile_name")
    repo_dir = context.get("repo_dir")
    dry_run = context.get("dry_run", False)
    hook_type = context.get("hook_type")  # "pre-restore" or "post-restore"
    targets = context.get("targets", [])

    # 3. Skip if dry-run
    if dry_run:
        print(json.dumps({
            "status": "success",
            "message": f"[Dry Run] Would process profile '{profile_name}'"
        }))
        sys.exit(0)

    # 4. Perform your logic
    print(f"Processing {len(targets)} targets for profile '{profile_name}'")

    # 5. Output JSON result
    print(json.dumps({
        "status": "success",
        "message": f"Processed profile '{profile_name}' with {len(targets)} targets"
    }))
    sys.exit(0)

if __name__ == "__main__":
    main()
```

### ReviveContext Fields

| Field | Type | Description |
|-------|------|-------------|
| `profile_name` | `str` | The profile being restored |
| `repo_dir` | `str` | Absolute path to the Revive repository |
| `dry_run` | `bool` | Whether this is a `--dry-run` (skip mutations) |
| `hook_type` | `str` | Either `"pre-restore"` or `"post-restore"` |
| `targets` | `list[str]` | Filesystem paths that were/will be mutated |

---

## Plugin Examples

### Post-Restore Notification

Send a Slack message when restore completes:

```python
import json
import os
import sys
import urllib.request
import urllib.error

def main() -> None:
    context_raw = os.environ.get("REVIVE_CONTEXT")
    context = json.loads(context_raw)

    webhook_url = os.environ.get("SLACK_WEBHOOK")
    if not webhook_url:
        print(json.dumps({
            "status": "error",
            "message": "SLACK_WEBHOOK not set"
        }), file=sys.stderr)
        sys.exit(1)

    if context.get("dry_run"):
        sys.exit(0)

    # Send Slack notification
    message = {
        "text": f"✅ Restored profile '{context['profile_name']}' on {os.uname().nodename}"
    }

    try:
        req = urllib.request.Request(
            webhook_url,
            data=json.dumps(message).encode(),
            headers={"Content-Type": "application/json"}
        )
        urllib.request.urlopen(req)
        print(json.dumps({"status": "success", "message": "Notification sent"}))
    except urllib.error.URLError as e:
        print(json.dumps({
            "status": "error",
            "message": f"Failed to send notification: {e}"
        }), file=sys.stderr)
        sys.exit(1)

if __name__ == "__main__":
    main()
```

**`plugin.yaml`:**

```yaml
name: "slack-notifier"
version: "1.0.0"
entrypoint: "notify.py"
permissions:
  network: true    # Need outbound network for Slack
hooks:
  - post-restore
```

**Usage:**

```bash
export SLACK_WEBHOOK=https://hooks.slack.com/services/...
rv restore base
# On success: Slack notification is sent
```

### Pre-Restore Validation

Check system state before restore:

```python
import json
import os
import shutil
import sys

def main() -> None:
    context = json.loads(os.environ.get("REVIVE_CONTEXT", "{}"))

    # Validate git is installed
    if not shutil.which("git"):
        print(json.dumps({
            "status": "error",
            "message": "git not found in PATH"
        }), file=sys.stderr)
        sys.exit(1)

    # Validate Python version
    if sys.version_info < (3, 11):
        print(json.dumps({
            "status": "error",
            "message": "Python 3.11+ required"
        }), file=sys.stderr)
        sys.exit(1)

    print(json.dumps({
        "status": "success",
        "message": "Pre-flight checks passed"
    }))
    sys.exit(0)

if __name__ == "__main__":
    main()
```

### Conditional Execution Based on Profile

```python
import json
import os
import subprocess
import sys

def main() -> None:
    context = json.loads(os.environ.get("REVIVE_CONTEXT", "{}"))
    profile = context.get("profile_name")

    if profile == "work":
        # Only on work profile
        subprocess.run(["systemctl", "--user", "restart", "gpg-agent"], check=True)
    elif profile == "media":
        # Only on media profile
        subprocess.run(["docker", "restart", "jellyfin"], check=False)

    print(json.dumps({"status": "success"}))
    sys.exit(0)

if __name__ == "__main__":
    main()
```

---

## Plugin Discovery

Plugins are discovered in this order (first match wins):

1. **Workspace plugins**: `<repo>/plugins/<name>/`
2. **User-global plugins**: `~/.config/rv/plugins/<name>/`
3. **Built-in plugins**: `<rv_package>/plugins/builtin/<name>/`

This allows:
- **Local overrides**: Repository-specific plugins override user-global plugins
- **Global setup**: User-wide plugins (e.g., notification handler) work for all repositories
- **Built-in fallbacks**: First-party plugins (MCP config, Claude prompts, Python skills) are included

---

## Plugin Execution & Sandboxing

### Execution Model

Plugins run in isolated Python subprocesses. The sandbox enforces:

| Restriction | Effect |
|-------------|--------|
| **Filesystem** | `open()`, `os.remove()`, etc. only access plugin dir, repo root, temp, and `allowed_paths` |
| **Network** | `socket.socket` blocked unless `permissions.network: true` |
| **Shell** | `subprocess.Popen`, `os.system` blocked unless `permissions.shell: true` |
| **Imports** | `ctypes`, `cffi`, `gc`, `importlib` blocked (prevents sandbox escape) |
| **Timeout** | Process killed after timeout expires (default 30s, max 300s) |
| **Exit** | Non-zero exit code aborts restore and rolls back all changes |

### Exit Codes & Rollback

- **Exit 0 + valid JSON** → Success, continue
- **Exit 0 + invalid JSON** → Error, rollback
- **Exit non-zero** → Error, rollback immediately
- **Timeout** → Process killed, rollback

If a plugin fails during `post-restore`, the system is already restored but post-hooks failed. The failure is logged but the restore is not rolled back (use `rv recover` if needed).

---

## Error Handling

Always output valid JSON to indicate success/failure:

```python
import json
import sys

def main() -> None:
    try:
        # Your logic here
        result = do_something()
        print(json.dumps({
            "status": "success",
            "message": f"Completed: {result}"
        }))
        sys.exit(0)
    except Exception as e:
        print(json.dumps({
            "status": "error",
            "message": f"Failed: {e}"
        }), file=sys.stderr)  # Log errors to stderr
        sys.exit(1)

if __name__ == "__main__":
    main()
```

---

## Built-In Plugins

Revive includes three first-party plugins:

| Plugin | Hook | Purpose |
|--------|------|---------|
| **mcp-config** | `post-restore` | Sync MCP server configuration to Claude Desktop app |
| **claude-prompts** | `post-restore` | Sync Claude AI prompt templates |
| **python-skills** | `post-restore` | Sync AI agent skill files |

These are loaded automatically when a repository is set up with `rv init`.

---

## Testing Your Plugin

1. **Create plugin structure**:
   ```bash
   mkdir -p plugins/my-test
   cat > plugins/my-test/plugin.yaml << EOF
   name: "my-test"
   version: "1.0.0"
   entrypoint: "test.py"
   hooks:
     - post-restore
   EOF
   ```

2. **Write test script**:
   ```bash
   cat > plugins/my-test/test.py << 'EOF'
   import json
   print(json.dumps({"status": "success", "message": "Test passed"}))
   EOF
   ```

3. **Run restore with verbose logging**:
   ```bash
   rv --verbose restore base
   ```

4. **Check the audit log**:
   ```bash
   cat ~/.config/rv/audit.log | jq '.[] | select(.event_type == "plugin")'
   ```

---

## Security Best Practices

1. **Request minimal permissions** — only enable `network` and `shell` if truly needed
2. **Validate input** — check context fields and environment variables
3. **Use `allowed_paths`** — explicitly allow only necessary directories
4. **Handle errors gracefully** — exit with proper status codes
5. **Avoid hardcoded secrets** — use environment variables (`os.environ`)
6. **Log securely** — don't log passwords, tokens, or keys

---

## See Also

- [Plugin Security Sandbox](../ARCHITECTURE.md#-plugin-security-sandbox)
- [CLI Reference - Plugins](../README.md#-plugin-system)
- [Architecture - Plugin Sandbox](../ARCHITECTURE.md#plugin-sandbox-architecture)
