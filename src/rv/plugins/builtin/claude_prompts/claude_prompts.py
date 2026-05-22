"""Claude Prompts first-party plugin. Deploys custom prompts to ClaudeCode."""

import json
import os
import shutil
import sys


def main() -> None:
    """Executes claude-prompts deployment."""
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

    # Source directory in repository
    src_dir = os.path.join(repo_dir, "claude-prompts")
    if not os.path.isdir(src_dir):
        print(json.dumps({"status": "success", "message": "No claude-prompts directory in repo, skipping."}))
        sys.exit(0)

    # Determine target directories depending on OS
    home = os.path.expanduser("~")
    if sys.platform == "darwin":
        target_dir = os.path.join(home, "Library", "Application Support", "ClaudeCode")
    else:
        target_dir = os.path.join(home, ".config", "ClaudeCode")

    if dry_run:
        print(json.dumps({"status": "success", "message": f"[Dry Run] Would deploy prompts to {target_dir}"}))
        sys.exit(0)

    try:
        os.makedirs(target_dir, exist_ok=True)
        for entry in os.listdir(src_dir):
            src_file = os.path.join(src_dir, entry)
            if os.path.isfile(src_file):
                shutil.copy2(src_file, os.path.join(target_dir, entry))
        print(json.dumps({"status": "success", "message": f"Successfully deployed prompts to {target_dir}"}))
    except Exception as e:
        print(json.dumps({"status": "error", "message": f"Failed to deploy prompts: {e}"}), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
