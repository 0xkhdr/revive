"""MCP Config first-party plugin. Deploys MCP config to target IDE directory.
"""

import json
import os
import shutil
import sys


def main() -> None:
    """Executes mcp-config deployment."""
    context_raw = os.environ.get("REVIVE_CONTEXT")
    if not context_raw:
        print(json.dumps({"status": "error", "message": "Missing REVIVE_CONTEXT"}), file=sys.stderr)
        sys.exit(1)

    try:
        context = json.loads(context_raw)
    except Exception as e:
        print(json.dumps({"status": "error", "message": f"Invalid context JSON: {e}"}), file=sys.stderr)
        sys.exit(1)

    repo_dir = context.get("repo_dir")
    dry_run = context.get("dry_run", False)

    if not repo_dir:
        print(json.dumps({"status": "error", "message": "Missing repo_dir in context"}), file=sys.stderr)
        sys.exit(1)

    # Source file in repository
    src_file = os.path.join(repo_dir, "mcp-config.json")
    if not os.path.exists(src_file):
        print(json.dumps({"status": "success", "message": "No mcp-config.json in repo, skipping."}))
        sys.exit(0)

    # Determine target directories depending on OS
    home = os.path.expanduser("~")
    if sys.platform == "darwin":
        target_dir = os.path.join(home, "Library", "Application Support", "Claude")
    else:
        target_dir = os.path.join(home, ".config", "Claude")

    target_file = os.path.join(target_dir, "claude_desktop_config.json")

    if dry_run:
        print(json.dumps({"status": "success", "message": f"[Dry Run] Would deploy MCP config to {target_file}"}))
        sys.exit(0)

    try:
        os.makedirs(target_dir, exist_ok=True)
        shutil.copy2(src_file, target_file)
        print(json.dumps({"status": "success", "message": f"Successfully deployed MCP config to {target_file}"}))
    except Exception as e:
        print(json.dumps({"status": "error", "message": f"Failed to deploy MCP config: {e}"}), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
