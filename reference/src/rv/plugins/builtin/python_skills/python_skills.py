"""Python Skills first-party plugin. Deploys skills definitions to ~/.config/rv/skills."""

import json
import os
import shutil
import sys


def main() -> None:
    """Executes python-skills deployment."""
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
    src_dir = os.path.join(repo_dir, ".agents", "skills")
    if not os.path.isdir(src_dir):
        src_dir = os.path.join(repo_dir, "skills")
        if not os.path.isdir(src_dir):
            print(json.dumps({"status": "success", "message": "No skills directory in repo, skipping."}))
            sys.exit(0)

    home = os.path.expanduser("~")
    target_dir = os.path.join(home, ".config", "rv", "skills")

    if dry_run:
        print(json.dumps({"status": "success", "message": f"[Dry Run] Would deploy skills to {target_dir}"}))
        sys.exit(0)

    try:
        if os.path.exists(target_dir):
            shutil.rmtree(target_dir)
        shutil.copytree(src_dir, target_dir)
        print(json.dumps({"status": "success", "message": f"Successfully deployed skills to {target_dir}"}))
    except Exception as e:
        print(json.dumps({"status": "error", "message": f"Failed to deploy skills: {e}"}), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
