"""Main Typer CLI application for Revive (rv)."""

import os
from typing import Any

import typer
from rich.console import Console
from rich.panel import Panel
from rich.syntax import Syntax
from rich.table import Table

from rv.logging.audit import AuditLogger
from rv.security.encryptor import AgeEncryptor
from rv.services.doctor import DoctorService
from rv.services.restore import RestoreService
from rv.services.status import StatusService
from rv.services.workspace import WorkspaceService

app = typer.Typer(
    name="rv", help="Revive (rv) — Production-grade environment lifecycle manager CLI.", add_completion=True
)
secret_app = typer.Typer(name="secret", help="Cryptographic secret management commands.")
workspace_app = typer.Typer(name="workspace", help="Manage registered revive workspaces.")
app.add_typer(secret_app)
app.add_typer(workspace_app)

console = Console()


def _get_repo_dir() -> str:
    """Returns the current working directory as the revive repository path."""
    repo_dir = os.getcwd()
    try:
        from rv.utils.interpolate import load_env

        load_env(repo_dir)
    except ImportError:
        pass
    return repo_dir


def complete_profile(ctx: typer.Context, incomplete: str) -> list[str]:
    """Provide shell autocompletion for profile names."""
    try:
        from rv.services.restore import ManifestLoader

        repo_dir = _get_repo_dir()
        manifest_path = os.path.join(repo_dir, "manifest.yaml")
        if os.path.exists(manifest_path):
            manifest = ManifestLoader.load(manifest_path)
            return [name for name in manifest.profiles.keys() if name.startswith(incomplete)]
    except Exception:
        pass
    return []


@app.callback()
def main(
    verbose: bool = typer.Option(False, "--verbose", "-v", help="Enable verbose debug logging."),
    headless: bool = typer.Option(False, "--headless", help="CI/headless mode: raw stream logs, no Rich styling."),
) -> None:
    """Configure structured logging and terminal settings."""
    AuditLogger.setup(verbose=verbose, headless=headless)


@app.command("init")
def init() -> None:
    """Scaffold a new revive repository in the current directory."""
    repo_dir = _get_repo_dir()
    manifest_path = os.path.join(repo_dir, "manifest.yaml")

    if os.path.exists(manifest_path):
        console.print(f"[bold red]Error:[/] A revive repository already exists at '{repo_dir}' (manifest.yaml exists).")
        raise typer.Exit(code=1)

    # Scaffold directories
    os.makedirs(os.path.join(repo_dir, "assets"), exist_ok=True)
    os.makedirs(os.path.join(repo_dir, "secrets"), exist_ok=True)
    os.makedirs(os.path.join(repo_dir, "machine"), exist_ok=True)
    os.makedirs(os.path.join(repo_dir, ".agents", "skills", "rv"), exist_ok=True)

    # Basic manifest template
    manifest_template = """# Revive Configuration Manifest
version: 2

assets:
  - id: example_zshrc
    type: symlink
    source: assets/example_zshrc
    target: ${USER_HOME}/.zshrc
    permissions: "0644"
    conflict_strategy: prompt

secrets: []

packages:
  brew: []
  apt: []
  flatpak: []
  snap: []
  docker:
    images: []
  node:
    version_file: .nvmrc

profiles:
  base:
    assets:
      - example_zshrc
    secrets: []
    packages:
      - brew
"""

    gitignore_template = """# ==========================================
# Revive Workspace Version Control Ignores
# ==========================================

# Revive State & Security (CRITICAL)
# ------------------------------------------
# NEVER commit raw Age identity keys, local lockfiles, or transactional states.
.rv.lock
manifest.lock
identity.txt
*.key
keys/
.env

# Modern IDEs
# ------------------------------------------
.vscode/
.idea/
*.suo
*.ntvs*
*.njsproj
*.sln
*.swp
*.swo
*~
.DS_Store

# AI Coding Agents & Copilots
# ------------------------------------------
# Ignore caches, history logs, and runtime state of modern AI agents.
.claude/
.claude.json
.cline/
.cline_history
.roo/
.roo_history
.copilot/
.windsurf/
.aider.chat.history.md
.aider.input.history
.aider.tags.cache
.antigravitycli/
.swe-agent/
.gpt-engineer/

# Python Virtual Environments & Packages
# ------------------------------------------
.venv/
venv/
env/
__pycache__/
*.pyc
"""

    agents_md_template = """# AI Agent Instructions for Revive (rv)

This repository is managed by **Revive (`rv`)**, a production-grade, declarative environment lifecycle manager. Any AI agent or assistant operating in this workspace must use `rv` to inspect, restore, backup, and manage configurations, packages, and secrets.

---

## 1. System Design & Philosophy

1. **Unidirectional Sync (Primary Flow)**: State normally flows from the repository to the local system (`repo → system`). The repository's `manifest.yaml` is the single source of truth. Running `rv restore <profile>` applies changes to the system.
2. **Bidirectional Sync (Optional Flow)**: State flows from the local system back into the repository (`system → repo`). Running `rv backup <profile>` captures live modifications of assets and encrypts updated secrets back into the repository.
3. **Strict Transaction Safety**: Restore operations are performed inside a transactional container. If a single step fails (e.g., missing package, permission issue, or post-apply hook crash), Revive automatically performs a complete journal-based rollback of all affected assets to their previous system state.

---

## 2. Command Reference Dictionary

Use this dictionary to formulate precise CLI operations when asked to perform environment setup, audits, or modifications.

### 2.1 Synchronization & Lifecycle

*   **`rv status`**
    *   **Description**: Evaluate sync status and calculate drift between the repository profile and the local system.
    *   **Syntax**: `rv status --profile <profile_name>` or `rv status -p <profile_name>`
    *   **Options**:
        *   `-i`, `--identity <file>`: Age private identity key file to verify and check secret drift.
    *   **Example**: `rv status -p base`

*   **`rv restore`**
    *   **Description**: Synchronize the local system state to match the repository profile (`repo → system`).
    *   **Syntax**: `rv restore <profile_name> [<profile_name2> ...]`
    *   **Options**:
        *   `-i`, `--identity <file>`: Path to Age private identity key file for decrypting secrets.
        *   `--dry-run`: Plan and validate all transactions without mutating the system filesystem.
        *   `--non-interactive`: Disable interactive prompts for file conflicts (useful in automation/CI).
        *   `--no-plugins`: Skip executing any custom plugin hooks.
    *   **Example**: `rv restore base --dry-run`

*   **`rv backup`**
    *   **Description**: Synchronize the local system state back into the repository (`system → repo`).
    *   **Syntax**: `rv backup <profile_name> [<profile_name2> ...]`
    *   **Options**:
        *   `-i`, `--identity <file>`: Path to Age identity key to re-encrypt and store secrets.
        *   `--dry-run`: Plan and validate backup operations without writing files to the repository.
    *   **Example**: `rv backup base`

*   **`rv diff`**
    *   **Description**: Generate colored, side-by-side or unified diffs of drifted file assets.
    *   **Syntax**: `rv diff --profile <profile_name>` or `rv diff -p <profile_name>`
    *   **Options**:
        *   `-i`, `--identity <file>`: Path to Age identity key to decrypt and diff encrypted secrets.
        *   `-u`, `--unified`: Display diff in standard unified diff format instead of side-by-side.
    *   **Example**: `rv diff -p base --unified`

---

### 2.2 System & Troubleshooting

*   **`rv doctor`**
    *   **Description**: Evaluate repository sanity, permission safety, system integration capabilities, and dependency readiness.
    *   **Syntax**: `rv doctor`
    *   **Options**:
        *   `-p`, `--profile <profile>`: Optionally target checks for a specific profile's packages/dependencies.
        *   `--json`: Output diagnostic reports in a structured JSON format.
    *   **Example**: `rv doctor -p base`

*   **`rv recover`**
    *   **Description**: List, replay, or abort/rollback active or incomplete transactional journals left by system crashes.
    *   **Syntax**: `rv recover`
    *   **Options**:
        *   `--auto`: Headless/CI auto-rollback of the latest incomplete transaction.
    *   **Example**: `rv recover --auto`

*   **`rv watch`**
    *   **Description**: Launch an interactive watchdog daemon monitoring the workspace repository for changes, auto-restoring them.
    *   **Syntax**: `rv watch --profile <profile_name>` or `rv watch -p <profile_name>`
    *   **Options**:
        *   `-i`, `--identity <file>`: Path to Age identity key for automatic secret decryption.
        *   `-d`, `--debounce <seconds>`: Delay (default: 5.0s) before triggering the auto-restore transaction.
    *   **Example**: `rv watch -p base -d 2`

---

### 2.3 Secrets Cryptography (`rv secret`)

Revive utilizes Age cryptography for managing credentials without leaking them in plaintext.

*   **`rv secret keygen`**
    *   **Description**: Generate a new Age cryptographic public/private keypair.
    *   **Syntax**: `rv secret keygen`
    *   **Options**:
        *   `-o`, `--output <file>`: Path to write the private key file safely (automatically applies secure 0600 permissions).
    *   **Example**: `rv secret keygen -o ~/.config/rv/identity.txt`

*   **`rv secret encrypt`**
    *   **Description**: Encrypt a plaintext file into an Age-encrypted file using a public key.
    *   **Syntax**: `rv secret encrypt <plaintext_file>`
    *   **Options**:
        *   `-o`, `--output <file>`: Target destination for the encrypted `.age` output.
        *   `-r`, `--recipient <pub_key>`: Age public key recipient string (multiple allowed).
    *   **Example**: `rv secret encrypt secrets/plain.txt -o secrets/secure.age -r age1yg7...`

*   **`rv secret decrypt`**
    *   **Description**: Decrypt an Age-encrypted `.age` secret file into a plaintext file using a private key.
    *   **Syntax**: `rv secret decrypt <encrypted_file>`
    *   **Options**:
        *   `-o`, `--output <file>`: Target destination for the decrypted plaintext file.
        *   `-i`, `--identity <file>`: Path to the private identity key file.
    *   **Example**: `rv secret decrypt secrets/secure.age -o secrets/plain.txt -i ~/.config/rv/identity.txt`

*   **`rv secret rotate`**
    *   **Description**: Re-encrypt a secret file with new recipient public keys.
    *   **Syntax**: `rv secret rotate <encrypted_file>`
    *   **Options**:
        *   `-i`, `--identity <file>`: Current private identity key file to decrypt the existing secret.
        *   `-nr`, `--new-recipient <pub_key>`: New recipient public key string (multiple allowed).
    *   **Example**: `rv secret rotate secrets/secure.age -i ~/.config/rv/identity.txt -nr age1new...`

---

### 2.4 Workspace & Installations

*   **`rv workspace list`**
    *   **Description**: List all workspaces registered in the global workspace registry (`~/.config/rv/workspaces.yaml`).
    *   **Syntax**: `rv workspace list`

*   **`rv workspace add`**
    *   **Description**: Register an existing directory as a Revive workspace.
    *   **Syntax**: `rv workspace add <directory_path>`
    *   **Options**:
        *   `-n`, `--name <friendly_name>`: Provide a custom friendly workspace identifier.
    *   **Example**: `rv workspace add /var/www/html/rai/up/revive -n my-revive`

*   **`rv workspace remove`**
    *   **Description**: De-register a registered workspace using its friendly name.
    *   **Syntax**: `rv workspace remove <workspace_name>`
    *   **Example**: `rv workspace remove my-revive`

*   **`rv self-install`**
    *   **Description**: Install the `rv` global launcher wrapper to `~/.local/bin/rv` pointing to the current virtual environment/interpreter.
    *   **Syntax**: `rv self-install`
    *   **Options**:
        *   `-f`, `--force`: Overwrite any pre-existing wrapper script.

*   **`rv self-uninstall`**
    *   **Description**: Remove the globally installed launcher wrapper and optional configurations.
    *   **Syntax**: `rv self-uninstall`
    *   **Options**:
        *   `-f`, `--force`: Force remove even if wrapper doesn't look autogenerated.
        *   `--purge-config`: Purge the entire global configurations directory `~/.config/rv`.

*   **`rv gui`**
    *   **Description**: Spin up a cosmic-dark web GUI dashboard to visually inspect drift, sync assets, and manage workspaces.
    *   **Syntax**: `rv gui`
    *   **Options**:
        *   `-p`, `--port <port>`: Change the web server port (default: 8080).
        *   `-h`, `--host <host>`: Bind to custom host address (default: 127.0.0.1).
        *   `--no-browser`: Start the server without opening the web browser automatically.

---

## 3. Best Practices & Workflow Guidelines

1. **Transactional Strategy**: Always perform structural or config updates inside the `assets/` or `secrets/` folders first, then commit them to git, and finally apply them locally with `rv restore <profile>`.
2. **Conflict Resolution**: If files on the local system have drifted and conflict with repository assets, `rv restore` will prompt you by default. Set conflict strategies inside `manifest.yaml` (options: `prompt`, `overwrite`, or `keep`).
3. **Custom Hooks & Plugins**: Use post-apply hooks or plugins to trigger environment-specific scripts. If `python-skills` is active, custom AI agent skills under the `.agents/skills/` directory of this repository will be synchronized automatically to `~/.config/rv/skills` upon restore.
4. **Environment Variables**: Revive supports variable interpolation (e.g., `${USER_HOME}`) defined in local `.env` files. Do not commit sensitive values to `.env`; rely on `rv secret` instead.
"""

    skills_md_template = """---
name: rv
description: Run Revive lifecycle commands to manage assets, packages, and secrets
---

# Revive (`rv`) AI Agent Skill

This skill enables any AI agent to use the **Revive (`rv`)** tool to inspect environment state, check configuration drift, restore assets, and encrypt/decrypt secure credentials.

## When to Use
Use this skill when you need to:
1. Verify system synchronization or diagnose drift (`rv status`).
2. Pull and apply the latest configurations or dotfiles from the repository to the local system (`rv restore`).
3. Save local configuration updates or dotfiles back into the repository (`rv backup`).
4. Generate cryptographic keypairs or manage encrypted secrets (`rv secret`).
5. Run system diagnostics and verify dependencies (`rv doctor`).

## Commands & Usage

### Check Sync Status & Drift
Check if local system files match the repository configuration:
```bash
rv status -p base
```
Generate unified diffs of all modifications:
```bash
rv diff -p base -u
```

### Apply Configuration (Repo -> System)
Deploy assets and install packages defined in the repository:
```bash
rv restore base
```
Run a dry-run check without modifying system files:
```bash
rv restore base --dry-run
```

### Backup System Configs (System -> Repo)
Backup local dotfile updates to the repository assets folder:
```bash
rv backup base
```

### Manage Secrets
Generate a secure age key pair:
```bash
rv secret keygen -o ~/.config/rv/identity.txt
```
Encrypt a file:
```bash
rv secret encrypt secrets/plain.txt -o secrets/secure.age -r age1publickey...
```
Decrypt a file:
```bash
rv secret decrypt secrets/secure.age -o secrets/plain.txt -i ~/.config/rv/identity.txt
```
"""

    readme_md_template = """# My Revive Environment

This repository contains my system configuration, managed by **Revive**.

## Quick Start

1. Install Revive.
2. Clone this repository.
3. Review `manifest.yaml` for defined assets and packages.
4. Run `rv status -p base` to see what will change.
5. Run `rv restore base` to apply the configuration.

## Directory Structure
- `assets/`: Managed dotfiles and scripts.
- `secrets/`: Encrypted credentials (requires an `age` identity to decrypt).
- `machine/`: Machine-specific overrides.
- `.agents/skills/`: Integrated agent skills.
"""

    env_template = """# Revive Environment Variables
# Used by manifest.yaml for variable interpolation.
# DO NOT commit sensitive secrets here! Use `rv secret` instead.

USER_HOME="~"
# EXAMPLE_VAR="some_value"
"""

    env_example_template = """# Revive Environment Variables (Example)
# Copy this file to .env and configure your variables.
# DO NOT commit sensitive secrets here!

USER_HOME="~"
# EXAMPLE_VAR="some_value"
"""

    # Example zshrc asset
    example_zshrc_path = os.path.join(repo_dir, "assets", "example_zshrc")
    with open(example_zshrc_path, "w", encoding="utf-8") as f:
        f.write('# Example zshrc managed by Revive\nexport PATH="$HOME/.bin:$PATH"\n')

    with open(manifest_path, "w", encoding="utf-8") as f:
        f.write(manifest_template)

    with open(os.path.join(repo_dir, ".gitignore"), "w", encoding="utf-8") as f:
        f.write(gitignore_template)

    with open(os.path.join(repo_dir, "AGENTS.md"), "w", encoding="utf-8") as f:
        f.write(agents_md_template)

    skills_dir = os.path.join(repo_dir, ".agents", "skills", "rv")
    with open(os.path.join(skills_dir, "SKILL.md"), "w", encoding="utf-8") as f:
        f.write(skills_md_template)

    with open(os.path.join(repo_dir, "README.md"), "w", encoding="utf-8") as f:
        f.write(readme_md_template)

    with open(os.path.join(repo_dir, ".env"), "w", encoding="utf-8") as f:
        f.write(env_template)

    with open(os.path.join(repo_dir, ".env.example"), "w", encoding="utf-8") as f:
        f.write(env_example_template)

    # Initialize Git repository if not already one
    git_msg = ""
    if not os.path.exists(os.path.join(repo_dir, ".git")):
        import subprocess

        try:
            subprocess.run(["git", "init"], cwd=repo_dir, capture_output=True, check=True)
            subprocess.run(["git", "add", "."], cwd=repo_dir, capture_output=True, check=True)
            try:
                subprocess.run(
                    ["git", "commit", "-m", "Initial commit from Revive init"],
                    cwd=repo_dir,
                    capture_output=True,
                    check=True,
                )
                git_msg = "\n  - [bold green]Git repository initialized with initial commit.[/]"
            except subprocess.CalledProcessError:
                git_msg = "\n  - [bold yellow]Git repository initialized, files staged (commit requires configured git identity).[/]"
        except Exception as e:
            git_msg = f"\n  - [bold red]Failed to initialize git repository: {e}[/]"

    # Register workspace
    WorkspaceService.register_workspace(repo_dir)

    console.print(
        Panel(
            "[bold green]Success![/] Revive environment scaffolded and registered successfully.\n\n"
            "[bold white]Directories created:[/]\n"
            "  - [cyan]assets/[/] (file and symlink assets)\n"
            "  - [cyan]secrets/[/] (encrypted secrets)\n"
            "  - [cyan]machine/[/] (host-specific overrides)\n"
            "  - [cyan].agents/skills/[/] (integrated agent skills)\n\n"
            "[bold white]Files created:[/]\n"
            "  - [cyan]manifest.yaml[/] (your global config manifest)\n"
            "  - [cyan]assets/example_zshrc[/] (example zshrc asset)\n"
            "  - [cyan].gitignore[/] (repository ignores)\n"
            "  - [cyan]AGENTS.md[/] (instructions for AI agents)\n"
            "  - [cyan].agents/skills/rv/SKILL.md[/] (native AI agent skill configuration)\n"
            "  - [cyan]README.md[/] (project documentation)\n"
            "  - [cyan].env[/] and [cyan].env.example[/] (environment variables)\n"
            f"{git_msg}\n\n"
            "Ready to manage! Try running [bold yellow]rv status --profile base[/]",
            title="Revive Initialized",
            border_style="green",
        )
    )


@app.command("restore")
def restore(
    profiles: list[str] = typer.Argument(
        ..., help="Name(s) of the deployment profile(s) to restore.", autocompletion=complete_profile
    ),
    identity: str | None = typer.Option(
        None, "--identity", "-i", help="Path to age identity file for decrypting secrets."
    ),
    dry_run: bool = typer.Option(
        False, "--dry-run", help="Plan and validate operations without mutating the filesystem."
    ),
    preview: bool = typer.Option(
        False, "--preview", help="Show drift analysis between repository and system state without making any changes."
    ),
    interactive: bool = typer.Option(
        True, "--interactive/--non-interactive", help="Toggle interactive prompting for file conflicts."
    ),
    no_plugins: bool = typer.Option(False, "--no-plugins", help="Skip executing any plugin hooks during restore."),
    prune_backups: bool = typer.Option(False, "--prune", help="Prune old backup snapshots after a successful restore."),
) -> None:
    """Synchronize the local environment state to match the repository profile (repo → system)."""
    repo_dir = _get_repo_dir()

    profile_list = []
    for p in profiles:
        for item in p.split(","):
            if item.strip():
                profile_list.append(item.strip())

    if not profile_list:
        console.print("[bold red]Error:[/] No profiles specified.")
        raise typer.Exit(code=1)

    profile_str = ",".join(profile_list)

    # --preview: Show drift analysis without restoring
    if preview:
        try:
            from rv.services.status import StatusService

            status_result = StatusService.get_status(
                repo_dir=repo_dir, profile_name=profile_str, identity_path=identity
            )
            assets_status: dict[str, dict[str, Any]] = status_result.get("assets", {})
            if not status_result.get("drifted"):
                console.print(
                    Panel(
                        "[green]✓ System is in sync with repository.[/]",
                        title=f"Preview: {profile_str}",
                        border_style="green",
                    )
                )
                return
            table = Table(title=f"Preview: Changes for profile '{profile_str}'", expand=True)
            table.add_column("Asset", style="cyan", width=25)
            table.add_column("Target", style="white")
            table.add_column("Status", style="yellow")
            for asset_id, asset_info in assets_status.items():
                target = (
                    ", ".join(asset_info.get("targets", []))
                    if isinstance(asset_info.get("targets"), list)
                    else str(asset_info.get("target", "-"))
                )
                table.add_row(asset_id, target, str(asset_info.get("status", "-")))
            console.print(table)
        except Exception as e:
            console.print(f"[bold red]Preview failed:[/] {e}")
            raise typer.Exit(code=1)
        return

    try:
        tx_id = RestoreService.restore(
            repo_dir=repo_dir,
            profile_name=profile_str,
            identity_path=identity,
            interactive=interactive,
            dry_run=dry_run,
            no_plugins=no_plugins,
        )

        # Resolve the profile to summarize what was restored if manifest exists
        manifest_path = os.path.join(repo_dir, "manifest.yaml")
        if os.path.exists(manifest_path):
            from rv.services.restore import ManifestLoader, ProfileResolver

            manifest = ManifestLoader.load(manifest_path)
            resolved = ProfileResolver.resolve(manifest, profile_str)

            # Build list of assets restored
            assets_summary = []
            for asset_id, asset in resolved.assets.items():
                targets = [asset.target] if isinstance(asset.target, str) else asset.target
                targets_str = ", ".join(targets)
                assets_summary.append(f"  - [green]✓[/] {asset_id} [cyan]→[/] {targets_str}")

            # Build list of secrets restored
            secrets_summary = []
            for secret_id, secret in resolved.secrets.items():
                targets = [secret.target] if isinstance(secret.target, str) else secret.target
                targets_str = ", ".join(targets)
                secrets_summary.append(f"  - [green]✓[/] [yellow]{secret_id}[/] [cyan]→[/] {targets_str}")

            # Build list of packages restored
            pkgs_summary = []
            for manager, pkgs in resolved.packages.items():
                if pkgs:
                    pkgs_summary.append(f"  - [bold white]{manager.upper()}:[/] [cyan]{', '.join(pkgs)}[/]")
            if resolved.docker_images:
                pkgs_summary.append(f"  - [bold white]DOCKER IMAGES:[/] [cyan]{', '.join(resolved.docker_images)}[/]")
            if resolved.node_config.get("version") or resolved.node_config.get("version_file"):
                node_ver = resolved.node_config.get("version") or resolved.node_config.get("version_file")
                pkgs_summary.append(f"  - [bold white]NODE.JS VERSION:[/] [cyan]{node_ver}[/]")

            # Prepare details
            details = []
            details.append(f"[bold white]Profile(s):[/] [magenta]{profile_str}[/]")
            if dry_run:
                details.append("[bold yellow]Mode:[/] Dry Run (No changes applied)")
            else:
                details.append(f"[bold white]Transaction ID:[/] [cyan]{tx_id}[/]")

            if assets_summary:
                details.append("\n[bold white]Assets Restored:[/]")
                details.extend(assets_summary)
            if secrets_summary:
                details.append("\n[bold white]Secrets Decrypted & Applied:[/]")
                details.extend(secrets_summary)
            if pkgs_summary:
                details.append("\n[bold white]Packages Installed/Orchestrated:[/]")
                details.extend(pkgs_summary)

            if not assets_summary and not secrets_summary and not pkgs_summary:
                details.append("\n(No assets, secrets, or packages configured for this profile)")
        else:
            # Fallback simple success panel if manifest is missing (e.g. in test mock environment)
            details = []
            details.append(f"[bold white]Profile(s):[/] [magenta]{profile_str}[/]")
            if dry_run:
                details.append("[bold yellow]Mode:[/] Dry Run (No changes applied)")
            else:
                details.append(f"[bold white]Transaction ID:[/] [cyan]{tx_id}[/]")

        title_str = "Revive Restore Plan (Dry Run)" if dry_run else "Revive Restore Completed"
        border_style_str = "yellow" if dry_run else "green"

        console.print(
            Panel(
                "\n".join(details),
                title=title_str,
                border_style=border_style_str,
            )
        )
    except Exception as e:
        console.print(
            Panel(
                f"[bold red]Transaction Failed:[/] {e}\n\n"
                "[yellow]Note:[/] Revive has automatically rolled back all file and symlink modifications to their pre-existing states. Your environment remains safe.",
                title="Restore Failed & Rolled Back",
                border_style="red",
            )
        )
        raise typer.Exit(code=2)

    # Post-restore prune if requested
    if prune_backups and not dry_run:
        try:
            from rv.services.recovery import BackupPruner
            from rv.services.restore import ManifestLoader

            manifest_path = os.path.join(repo_dir, "manifest.yaml")
            if os.path.exists(manifest_path):
                manifest = ManifestLoader.load(manifest_path)
                deleted = BackupPruner.prune(
                    max_count=manifest.backup_retention.max_count,
                    max_age_days=manifest.backup_retention.max_age_days,
                )
                if deleted:
                    console.print(f"[dim]Pruned {len(deleted)} old backup snapshot(s).[/]")
        except Exception as prune_err:
            console.print(f"[yellow]Warning: Post-restore prune failed: {prune_err}[/]")


@app.command("backup")
def backup(
    profiles: list[str] = typer.Argument(
        ..., help="Name(s) of the deployment profile(s) to backup.", autocompletion=complete_profile
    ),
    identity: str | None = typer.Option(
        None, "--identity", "-i", help="Path to age identity file for encrypting secrets."
    ),
    dry_run: bool = typer.Option(
        False, "--dry-run", help="Plan and validate backup operations without mutating the repository."
    ),
) -> None:
    """Synchronize the local environment state back into the repository profile (system -> repo)."""
    repo_dir = _get_repo_dir()

    profile_list = []
    for p in profiles:
        for item in p.split(","):
            if item.strip():
                profile_list.append(item.strip())

    if not profile_list:
        console.print("[bold red]Error:[/] No profiles specified.")
        raise typer.Exit(code=1)

    profile_str = ",".join(profile_list)

    try:
        from rv.services.backup import BackupService

        backed_up = BackupService.backup(
            repo_dir=repo_dir,
            profile_name=profile_str,
            identity_path=identity,
            dry_run=dry_run,
        )

        if dry_run:
            summary = []
            summary.append(f"[bold white]Profile(s):[/] [magenta]{profile_str}[/]")
            summary.append("[bold yellow]Mode:[/] Dry Run (No changes committed to repository)")
            if backed_up:
                summary.append("\n[bold white]Assets/Secrets planned for backup:[/]")
                for item in backed_up:
                    summary.append(f"  - [yellow]→[/] {item}")
            else:
                summary.append("\n(No assets or secrets planned for backup)")

            console.print(
                Panel(
                    "\n".join(summary),
                    title="Revive Backup Plan (Dry Run)",
                    border_style="yellow",
                )
            )
        else:
            summary = []
            summary.append(f"[bold white]Profile(s):[/] [magenta]{profile_str}[/]")
            summary.append(f"[bold white]Repository:[/] [cyan]{repo_dir}[/]")
            if backed_up:
                summary.append(f"\n[bold green]Successfully backed up {len(backed_up)} item(s):[/]")
                for item in backed_up:
                    summary.append(f"  - [green]✓[/] {item}")
            else:
                summary.append("\n(No assets or secrets were backed up)")

            console.print(
                Panel(
                    "\n".join(summary),
                    title="Revive Backup Completed",
                    border_style="green",
                )
            )
    except Exception as e:
        console.print(
            Panel(
                f"[bold red]Backup Failed![/]\n\n[bold white]Error details:[/] {e}",
                title="Backup Failed",
                border_style="red",
            )
        )
        raise typer.Exit(code=2)


@app.command("status")
def status(
    profile: list[str] = typer.Option(
        ..., "--profile", "-p", help="Profile(s) to evaluate sync status for.", autocompletion=complete_profile
    ),
    identity: str | None = typer.Option(None, "--identity", "-i", help="Age identity file to check secret drift."),
) -> None:
    """Compare system state against repository profile and calculate drift."""
    repo_dir = _get_repo_dir()

    profile_list = []
    for p in profile:
        for item in p.split(","):
            if item.strip():
                profile_list.append(item.strip())

    if not profile_list:
        console.print("[bold red]Error:[/] No profiles specified.")
        raise typer.Exit(code=1)

    profile_str = ",".join(profile_list)

    try:
        report = StatusService.get_status(repo_dir, profile_str, identity)
    except Exception as e:
        console.print(f"[bold red]Status check failed:[/] {e}")
        raise typer.Exit(code=1)

    table = Table(title=f"Drift Analysis for Profile '{profile_str}'", expand=True)
    table.add_column("Asset ID", style="cyan", width=20)
    table.add_column("Type", style="magenta", width=12)
    table.add_column("Target Path", style="blue")
    table.add_column("Status", style="bold", width=20)
    table.add_column("Details", style="italic")

    status_color_map = {
        "in_sync": "[green]In Sync[/]",
        "missing": "[red]Missing[/]",
        "modified": "[red]Modified[/]",
        "permissions_drifted": "[yellow]Permissions Mismatch[/]",
        "type_mismatch": "[bold red]Type Mismatch[/]",
        "error": "[bold red]Error[/]",
    }

    for asset_id, info in report["assets"].items():
        status_raw = info["status"]
        status_styled = status_color_map.get(status_raw, f"[white]{status_raw}[/]")
        table.add_row(
            asset_id,
            info["type"].value if hasattr(info["type"], "value") else str(info["type"]),
            "\n".join(info["target"]) if isinstance(info["target"], (list, tuple)) else str(info["target"]),
            status_styled,
            info["details"],
        )

    console.print(table)

    if report["drifted"]:
        console.print(
            Panel(
                "[bold yellow]Warning: System Drift Detected[/]\n\n"
                "Your local system state has drifted from the repository configuration.\n"
                f"To synchronize, run: [bold green]rv restore {profile_str}[/]",
                title="Status: Drifted",
                border_style="yellow",
            )
        )
        raise typer.Exit(code=0)
    else:
        console.print(
            Panel(
                "[bold green]In Sync![/]\n\nEnvironment is perfectly synchronized with the repository state.",
                title="Status: Synchronized",
                border_style="green",
            )
        )


def _render_side_by_side_diff(expected_text: str, actual_text: str, source_name: str, target_name: str) -> Table:
    """Generates a beautiful aligned side-by-side terminal comparison using Rich."""
    import difflib

    from rich.text import Text

    expected_lines = expected_text.splitlines()
    actual_lines = actual_text.splitlines()

    table = Table(show_header=True, header_style="bold magenta", box=None, expand=True)
    table.add_column("L#", style="dim cyan", width=5, justify="right")
    table.add_column(f"Expected (Repository: {source_name})", ratio=1)
    table.add_column("L#", style="dim cyan", width=5, justify="right")
    table.add_column(f"Actual (System: {target_name})", ratio=1)

    matcher = difflib.SequenceMatcher(None, expected_lines, actual_lines)

    for tag, i1, i2, j1, j2 in matcher.get_opcodes():
        if tag == "equal":
            for idx in range(i2 - i1):
                exp_line_num = str(i1 + idx + 1)
                act_line_num = str(j1 + idx + 1)
                table.add_row(
                    exp_line_num,
                    Text(expected_lines[i1 + idx]),
                    act_line_num,
                    Text(actual_lines[j1 + idx]),
                )
        elif tag == "delete":
            for idx in range(i2 - i1):
                exp_line_num = str(i1 + idx + 1)
                table.add_row(
                    exp_line_num,
                    Text(expected_lines[i1 + idx], style="red bold"),
                    "",
                    Text(""),
                )
        elif tag == "insert":
            for idx in range(j2 - j1):
                act_line_num = str(j1 + idx + 1)
                table.add_row(
                    "",
                    Text(""),
                    act_line_num,
                    Text(actual_lines[j1 + idx], style="green bold"),
                )
        elif tag == "replace":
            max_len = max(i2 - i1, j2 - j1)
            for idx in range(max_len):
                exp_content = ""
                exp_line_num = ""
                exp_style = ""
                if idx < (i2 - i1):
                    exp_content = expected_lines[i1 + idx]
                    exp_line_num = str(i1 + idx + 1)
                    exp_style = "red bold"

                act_content = ""
                act_line_num = ""
                act_style = ""
                if idx < (j2 - j1):
                    act_content = actual_lines[j1 + idx]
                    act_line_num = str(j1 + idx + 1)
                    act_style = "green bold"

                table.add_row(
                    exp_line_num,
                    Text(exp_content, style=exp_style),
                    act_line_num,
                    Text(act_content, style=act_style),
                )
    return table


@app.command("diff")
def diff(
    profile: list[str] = typer.Option(
        ..., "--profile", "-p", help="Profile name(s) to check drift for.", autocompletion=complete_profile
    ),
    identity: str | None = typer.Option(None, "--identity", "-i", help="Age identity file to diff encrypted secrets."),
    unified: bool = typer.Option(False, "--unified", "-u", help="Display standard unified diff format."),
) -> None:
    """Print colored diffs of all modified file assets on the filesystem."""
    repo_dir = _get_repo_dir()

    profile_list = []
    for p in profile:
        for item in p.split(","):
            if item.strip():
                profile_list.append(item.strip())

    if not profile_list:
        console.print("[bold red]Error:[/] No profiles specified.")
        raise typer.Exit(code=1)

    profile_str = ",".join(profile_list)

    try:
        report = StatusService.get_status(repo_dir, profile_str, identity)
    except Exception as e:
        console.print(f"[bold red]Failed to get drift status:[/] {e}")
        raise typer.Exit(code=1)

    has_diffs = False

    for asset_id, info in report["assets"].items():
        if info["status"] == "modified":
            if unified:
                diff_text = StatusService.get_diff(repo_dir, profile_str, asset_id, identity)
                if diff_text:
                    has_diffs = True
                    console.print(
                        Panel(
                            Syntax(diff_text, "diff", theme="monokai", background_color="default"),
                            title=f"Drift Diff: {asset_id} -> {info['target']}",
                            border_style="yellow",
                        )
                    )
            else:
                contents = StatusService.get_contents_for_diff(repo_dir, profile_str, asset_id, identity)
                if contents:
                    expected_text, actual_text = contents
                    if not actual_text and expected_text.startswith("["):
                        has_diffs = True
                        console.print(
                            Panel(
                                f"[bold red]Error rendering diff:[/] {expected_text}",
                                title=f"Drift Diff: {asset_id} -> {info['target']}",
                                border_style="red",
                            )
                        )
                    elif expected_text != actual_text:
                        has_diffs = True
                        from rv.services.restore import ManifestLoader, ProfileResolver

                        source_name = f"repo://{asset_id}"
                        try:
                            manifest_path = os.path.join(repo_dir, "manifest.yaml")
                            manifest = ManifestLoader.load(manifest_path)
                            resolved = ProfileResolver.resolve(manifest, profile_str)
                            asset = resolved.assets.get(asset_id) or resolved.secrets.get(asset_id)
                            if asset:
                                source_name = f"repo://{asset.source}"
                        except Exception:
                            pass

                        diff_table = _render_side_by_side_diff(expected_text, actual_text, source_name, info["target"])
                        console.print(
                            Panel(
                                diff_table,
                                title=f"Drift Diff (Side-by-Side): {asset_id} -> {info['target']}",
                                border_style="yellow",
                            )
                        )

    if not has_diffs:
        console.print(
            Panel(
                "[bold green]No file content modifications detected.[/]\n\n"
                "All configured assets match the repository contents perfectly.",
                title="Drift Diff Summary",
                border_style="green",
            )
        )


@app.command("doctor")
def doctor(
    profile: list[str] = typer.Option(
        None,
        "--profile",
        "-p",
        help="Optionally run doctor checks specific to profile(s).",
        autocompletion=complete_profile,
    ),
    json_format: bool = typer.Option(False, "--json", help="Output diagnostic report in structured JSON format."),
) -> None:
    """Evaluate repository sanity, permission safety, and system capabilities."""
    repo_dir = _get_repo_dir()

    profile_str = None
    if profile:
        profile_list = []
        for p in profile:
            for item in p.split(","):
                if item.strip():
                    profile_list.append(item.strip())
        if profile_list:
            profile_str = ",".join(profile_list)

    report = DoctorService.check_health(repo_dir, profile_str)

    if json_format:
        import json

        console.print_json(json.dumps(report))
        raise typer.Exit(code=0 if report["healthy"] else 1)

    # Build tools integration list
    tools_str = ""
    for tool, available in report["tools"].items():
        status_styled = "[bold green]✓ Available[/]" if available else "[bold yellow]✗ Missing[/]"
        tools_str += f"  - {tool:<25}: {status_styled}\n"

    # Build issues list
    issues_str = ""
    if report["issues"]:
        issues_str = "\n[bold red]Issues Detected:[/]\n"
        for issue in report["issues"]:
            prefix = "[bold red][Critical][/]" if issue["severity"] == "critical" else "[bold yellow][Warning][/]"
            issues_str += f"  {prefix} ({issue['category']}): {issue['message']}\n"
    else:
        issues_str = "\n[bold green]Perfect![/] No issues detected in repository setup."

    console.print(
        Panel(
            f"[bold white]Sanity Check Summary:[/] "
            f"{'[bold green]HEALTHY[/]' if report['healthy'] else '[bold red]ISSUES FOUND[/]'}\n"
            f"Checks run: {report['checks_run']}\n\n"
            f"[bold white]System Tool Integration:[/]\n{tools_str}"
            f"{issues_str}",
            title="Revive System Doctor",
            border_style="green" if report["healthy"] else "red",
        )
    )

    if not report["healthy"]:
        raise typer.Exit(code=1)


@secret_app.command("encrypt")
def secret_encrypt(
    file_path: str = typer.Argument(..., help="Path to the plaintext file to encrypt."),
    output_path: str = typer.Option(..., "--output", "-o", help="Path to write the encrypted age file."),
    recipient: list[str] = typer.Option(
        ..., "--recipient", "-r", help="Age public key recipient (can specify multiple)."
    ),
) -> None:
    """Encrypt a plaintext secret using age public keys."""
    try:
        AgeEncryptor.encrypt_file(file_path, output_path, recipient)
        console.print(
            Panel(
                f"[bold green]Success![/] Successfully encrypted secret to '{output_path}'.\n\n"
                f"[bold white]Plaintext source:[/] [cyan]{file_path}[/]\n"
                f"[bold white]Encrypted output:[/] [magenta]{output_path}[/]\n"
                f"[bold white]Recipients:[/] [yellow]{', '.join(recipient)}[/]",
                title="Secret Encrypted",
                border_style="green",
            )
        )
    except Exception as e:
        console.print(
            Panel(
                f"[bold red]Encryption failed:[/] {e}",
                title="Encryption Failed",
                border_style="red",
            )
        )
        raise typer.Exit(code=1)


@secret_app.command("decrypt")
def secret_decrypt(
    file_path: str = typer.Argument(..., help="Path to the encrypted .age secret file."),
    output_path: str = typer.Option(..., "--output", "-o", help="Path to write the decrypted plaintext file."),
    identity: str = typer.Option(..., "--identity", "-i", help="Path to the age identity private key file."),
) -> None:
    """Decrypt an age-encrypted secret file using an identity private key."""
    try:
        AgeEncryptor.decrypt_file(file_path, output_path, identity)
        console.print(
            Panel(
                f"[bold green]Success![/] Successfully decrypted secret to '{output_path}'.\n\n"
                f"[bold white]Encrypted source:[/] [cyan]{file_path}[/]\n"
                f"[bold white]Decrypted output:[/] [magenta]{output_path}[/]\n"
                f"[bold white]Identity used:[/] [yellow]{identity}[/]",
                title="Secret Decrypted",
                border_style="green",
            )
        )
    except Exception as e:
        console.print(
            Panel(
                f"[bold red]Decryption failed:[/] {e}",
                title="Decryption Failed",
                border_style="red",
            )
        )
        raise typer.Exit(code=1)


@secret_app.command("rotate")
def secret_rotate(
    file_path: str = typer.Argument(..., help="Path to the encrypted secret file to rotate."),
    identity: str = typer.Option(..., "--identity", "-i", help="Path to the current age identity file."),
    new_recipient: list[str] = typer.Option(
        ..., "--new-recipient", "-nr", help="New age public key recipient (multiple allowed)."
    ),
) -> None:
    """Decrypt a secret using existing key, and re-encrypt with a new list of recipients."""
    from rv.security.tempfile import SecureTempFile

    with SecureTempFile.file() as tmp_plain:
        try:
            # Decrypt existing
            AgeEncryptor.decrypt_file(file_path, tmp_plain, identity)
            # Re-encrypt with new keys
            AgeEncryptor.encrypt_file(tmp_plain, file_path, new_recipient)
            console.print(
                Panel(
                    f"[bold green]Success:[/] Secret at '{file_path}' successfully rotated to new recipients.\n\n"
                    f"[bold white]Decryption identity:[/] [yellow]{identity}[/]\n"
                    f"[bold white]New recipients:[/] [yellow]{', '.join(new_recipient)}[/]",
                    title="Secret Rotated",
                    border_style="green",
                )
            )
        except Exception as e:
            console.print(
                Panel(
                    f"[bold red]Rotation failed:[/] {e}",
                    title="Rotation Failed",
                    border_style="red",
                )
            )
            raise typer.Exit(code=1)


@secret_app.command("keygen")
def secret_keygen(
    output: str | None = typer.Option(None, "--output", "-o", help="Path to write the generated private age key file."),
) -> None:
    """Generate a new age keypair for encrypting/decrypting secrets."""
    try:
        public_key, private_key = AgeEncryptor.generate_keypair()

        if output:
            parent_dir = os.path.dirname(output)
            if parent_dir:
                os.makedirs(parent_dir, exist_ok=True)

            # Write key file with public key comment
            with open(output, "w", encoding="utf-8") as f:
                f.write(f"# public key: {public_key}\n")
                f.write(f"{private_key}\n")

            # Set secure file permissions (0600)
            os.chmod(output, 0o600)

            console.print(
                Panel(
                    "[bold green]Success:[/] Generated a new age keypair.\n\n"
                    f"Private identity key saved to: [cyan]{output}[/]\n"
                    f"[bold white]File permissions:[/] [magenta]0600 (Secure)[/]\n"
                    f"[bold white]Public recipient key:[/] [bold yellow]{public_key}[/]",
                    title="Age Keypair Generated",
                    border_style="green",
                )
            )
        else:
            console.print(
                Panel(
                    "[bold green]Success:[/] Generated a new age keypair.\n\n"
                    f"Private identity key:\n[bold cyan]{private_key}[/]\n\n"
                    f"Public recipient key:\n[bold yellow]{public_key}[/]\n\n"
                    "[bold red]WARNING:[/] Copy the private key safely immediately. It will not be stored.",
                    title="Age Keypair Generated",
                    border_style="green",
                )
            )
    except Exception as e:
        console.print(
            Panel(
                f"[bold red]Key generation failed:[/] {e}",
                title="Key Generation Failed",
                border_style="red",
            )
        )
        raise typer.Exit(code=1)


@app.command("watch")
def watch(
    profile: list[str] = typer.Option(
        ...,
        "--profile",
        "-p",
        help="Profile(s) to monitor and auto-apply changes for.",
        autocompletion=complete_profile,
    ),
    identity: str | None = typer.Option(
        None, "--identity", "-i", help="Path to age identity file for decrypting secrets."
    ),
    debounce: float = typer.Option(
        5.0, "--debounce", "-d", help="Debounce delay in seconds before triggering auto-apply."
    ),
) -> None:
    """Watch directory for changes and automatically run restore on update."""
    import time

    from rv.watchers.daemon import WatchdogDaemon

    repo_dir = _get_repo_dir()

    profile_list = []
    for p in profile:
        for item in p.split(","):
            if item.strip():
                profile_list.append(item.strip())

    if not profile_list:
        console.print("[bold red]Error:[/] No profiles specified.")
        raise typer.Exit(code=1)

    profile_str = ",".join(profile_list)

    console.print(
        Panel(
            "Monitoring revive repository for changes...\n\n"
            f"[bold white]Repository:[/] [cyan]{repo_dir}[/]\n"
            f"[bold white]Profile(s):[/] [magenta]{profile_str}[/]\n"
            f"[bold white]Debounce delay:[/] [yellow]{debounce}s[/]\n\n"
            "Press [bold red]Ctrl+C[/] to stop the watchdog daemon.",
            title="Revive Watchdog Daemon Active",
            border_style="cyan",
        )
    )

    daemon = WatchdogDaemon(
        repo_dir=repo_dir, profile_name=profile_str, identity_path=identity, debounce_seconds=debounce
    )
    try:
        daemon.start()
        # Keep main thread alive while watcher runs
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        console.print("\n[yellow]Stopping watchdog daemon...[/]")
        daemon.stop()
        console.print(
            Panel(
                "Watchdog daemon stopped successfully.\nSystem state remains unchanged.",
                title="Watchdog Stopped",
                border_style="yellow",
            )
        )


@app.command("recover")
def recover(
    auto: bool = typer.Option(
        False, "--auto", help="In headless/CI environments, auto-rollback the latest incomplete journal and exit."
    ),
) -> None:
    """Scan journals to abort/rollback or discard incomplete transactions."""
    from rv.services.recovery import RecoveryService

    try:
        journals = RecoveryService.list_incomplete_journals()
        if not journals:
            console.print(
                Panel(
                    "No incomplete transactions found.\nYour environment state is safe.",
                    title="Transaction Recovery",
                    border_style="green",
                )
            )
            raise typer.Exit(code=0)

        if auto:
            latest = journals[0]
            console.print(f"[yellow]Auto-recovering latest transaction {latest.tx_id}...[/]")
            RecoveryService.rollback_journal(latest)
            console.print(
                Panel(
                    f"Transaction {latest.tx_id} successfully rolled back.\n"
                    "All affected files and symlinks have been restored to their pre-transaction states.",
                    title="Rollback Complete",
                    border_style="green",
                )
            )
            raise typer.Exit(code=0)

        # Interactive mode
        journals_list = ""
        for journal in journals:
            journals_list += (
                f"  - [cyan]ID:[/] {journal.tx_id}\n"
                f"    [dim]Timestamp:[/] {journal.timestamp}\n"
                f"    [dim]Status:[/] {journal.status}\n\n"
            )
        console.print(
            Panel(
                f"Found [yellow]{len(journals)}[/] incomplete transaction(s):\n\n{journals_list.strip()}",
                title="Incomplete Transactions Detected",
                border_style="yellow",
            )
        )

        for journal in journals:
            console.print(f"\n[cyan]Transaction:[/] {journal.tx_id}")
            console.print(f"  [cyan]Timestamp:[/] {journal.timestamp}")
            console.print(f"  [cyan]Status:[/] {journal.status}")

            while True:
                action = typer.prompt("Action? ([r]ollback, [d]iscard, [s]kip)", default="s").strip().lower()

                if action in ("r", "rollback"):
                    try:
                        RecoveryService.rollback_journal(journal)
                        console.print(
                            Panel(
                                f"Transaction {journal.tx_id} rolled back.\n"
                                "All affected files and symlinks have been restored to their pre-transaction states.",
                                title="Rollback Complete",
                                border_style="green",
                            )
                        )
                    except Exception as e:
                        console.print(
                            Panel(
                                f"[bold red]Rollback failed![/]\n\n[bold white]Error details:[/] {e}",
                                title="Rollback Failed",
                                border_style="red",
                            )
                        )
                    break
                elif action in ("d", "discard"):
                    RecoveryService.discard_journal(journal)
                    console.print(
                        Panel(
                            f"Transaction {journal.tx_id} journal discarded.\n"
                            "Backup snapshots deleted. No files were modified.",
                            title="Journal Discarded",
                            border_style="yellow",
                        )
                    )
                    break
                elif action in ("s", "skip"):
                    console.print("[yellow]Skipping transaction recovery.[/]")
                    break
                else:
                    console.print("[red]Invalid action. Please choose r, d, or s.[/]")
    except typer.Exit:
        raise
    except Exception as e:
        console.print(
            Panel(
                f"[bold red]Recovery failed![/]\n\n[bold white]Error details:[/] {e}",
                title="Recovery Failed",
                border_style="red",
            )
        )
        raise typer.Exit(code=1)


@app.command("self-install")
def self_install(
    force: bool = typer.Option(False, "--force", "-f", help="Force overwrite existing installation wrapper."),
) -> None:
    """Install the rv tool wrapper globally to ~/.local/bin/rv for easy access.

    This creates an executable wrapper script that points to the current Python
    virtual environment or package installation, ensuring you can run 'rv' from anywhere.
    """
    import sys

    home = os.path.expanduser("~")
    local_bin = os.path.join(home, ".local", "bin")
    target_path = os.path.join(local_bin, "rv")

    # Ensure local_bin exists
    os.makedirs(local_bin, exist_ok=True)

    if os.path.exists(target_path) and not force:
        console.print(
            Panel(
                f"[bold yellow]Warning:[/] An installation wrapper already exists at '{target_path}'. Use '--force' or '-f' to overwrite it.",
                title="Installation Aborted",
                border_style="yellow",
            )
        )
        raise typer.Exit(code=0)

    # Get path to python interpreter in the active environment
    python_bin = sys.executable

    # Construct shell wrapper contents
    wrapper_content = f"""#!/bin/sh
# Revive CLI Autogenerated Wrapper
exec "{python_bin}" -m rv "$@"
"""

    try:
        with open(target_path, "w", encoding="utf-8") as f:
            f.write(wrapper_content)

        # Make the target file executable (0755)
        os.chmod(target_path, 0o755)  # noqa: S103

        console.print(
            Panel(
                "[bold green]Successfully installed Revive CLI wrapper globally![/]\n\n"
                f"Wrapper created at: [cyan]{target_path}[/]\n"
                f"Points to environment: [magenta]{python_bin}[/]\n\n"
                "You can now run [bold yellow]rv[/] from anywhere in your shell!",
                title="Installation Successful",
                border_style="green",
            )
        )

        # Check if local_bin is in PATH
        paths = os.environ.get("PATH", "").split(os.pathsep)
        if local_bin not in paths and os.path.abspath(local_bin) not in [os.path.abspath(p) for p in paths]:
            console.print(
                "\n[bold yellow]Note:[/] '~/.local/bin' is not currently in your system PATH variable.\n"
                "To run 'rv' globally, please add it to your shell config file (e.g., ~/.bashrc or ~/.zshrc):\n"
                '  [bold cyan]export PATH="$HOME/.local/bin:$PATH"[/]'
            )
    except Exception as e:
        console.print(
            Panel(
                f"[bold red]Self-installation failed:[/] {e}",
                title="Self-Installation Failed",
                border_style="red",
            )
        )
        raise typer.Exit(code=1)


@app.command("self-uninstall")
def self_uninstall(
    force: bool = typer.Option(
        False, "--force", "-f", help="Force removal of the wrapper even if it doesn't look autogenerated."
    ),
    purge_config: bool = typer.Option(
        False, "--purge-config", help="Also remove the global configuration directory (~/.config/rv)."
    ),
) -> None:
    """Remove the Revive CLI installation, including the wrapper and isolated environment."""
    import shutil

    home = os.path.expanduser("~")
    local_bin = os.path.join(home, ".local", "bin")
    target_path = os.path.join(local_bin, "rv")
    install_root = os.path.join(home, ".local", "share", "rv")
    config_root = os.path.join(home, ".config", "rv")

    removed_count = 0
    actions = []

    # 1. Remove wrapper
    if os.path.exists(target_path):
        is_ours = False
        try:
            with open(target_path, encoding="utf-8") as f:
                content = f.read()
                if "Revive CLI Autogenerated Wrapper" in content or "Revive CLI Installer Wrapper" in content:
                    is_ours = True
        except Exception:
            pass

        if is_ours or force:
            try:
                os.remove(target_path)
                actions.append(f"  - [red]Removed wrapper:[/] {target_path}")
                removed_count += 1
            except Exception as e:
                actions.append(f"  - [bold red]Failed to remove wrapper:[/] {e}")
        else:
            actions.append(
                f"  - [bold yellow]Skipped wrapper:[/] '{target_path}' does not look like an autogenerated wrapper. (Use '--force')"
            )

    # 2. Remove isolated install root
    if os.path.exists(install_root):
        try:
            shutil.rmtree(install_root)
            actions.append(f"  - [red]Removed isolated installation:[/] {install_root}")
            removed_count += 1
        except Exception as e:
            actions.append(f"  - [bold red]Failed to remove installation root:[/] {e}")

    # 3. Purge config if requested
    if purge_config and os.path.exists(config_root):
        try:
            shutil.rmtree(config_root)
            actions.append(f"  - [red]Purged configuration:[/] {config_root}")
            removed_count += 1
        except Exception as e:
            actions.append(f"  - [bold red]Failed to purge configuration:[/] {e}")

    if removed_count > 0:
        console.print(
            Panel(
                "[bold green]Success![/] Revive CLI uninstalled successfully.\n\n"
                "[bold white]Actions performed:[/]\n" + "\n".join(actions),
                title="Revive Uninstalled",
                border_style="green",
            )
        )
    else:
        console.print(
            Panel(
                "No Revive components or configuration directories were found to remove.\n"
                "Revive is not active on this system.",
                title="Uninstall: Nothing to do",
                border_style="yellow",
            )
        )


@app.command("gui")
def gui(
    port: int = typer.Option(8080, "--port", "-p", help="Port to run the GUI server on."),
    host: str = typer.Option("127.0.0.1", "--host", "-h", help="Host address to bind to."),
    no_browser: bool = typer.Option(False, "--no-browser", help="Do not open the browser automatically."),
    auth_token: str | None = typer.Option(
        None,
        "--auth-token",
        help="Authentication token for API access. Auto-generated if not provided. Pass '' to disable auth.",
    ),
) -> None:
    """Launch the interactive Revive Web GUI."""
    from rv.gui.server import start_gui_server

    start_gui_server(host=host, port=port, open_browser=not no_browser, auth_token=auth_token)


@app.command("prune")
def prune(
    max_count: int = typer.Option(10, "--max-count", help="Keep at most N backup snapshots."),
    max_age_days: int = typer.Option(30, "--max-age-days", help="Delete backup snapshots older than N days."),
    dry_run: bool = typer.Option(False, "--dry-run", help="Preview what would be pruned without deleting."),
    yes: bool = typer.Option(False, "--yes", "-y", help="Skip confirmation prompt."),
) -> None:
    """Remove old transaction backup snapshots to reclaim disk space."""
    from rv.services.recovery import BackupPruner

    # List what would be deleted
    candidates = BackupPruner.prune(max_count=max_count, max_age_days=max_age_days, dry_run=True)

    if not candidates:
        console.print(
            Panel("[green]✓ No backup snapshots qualify for pruning.[/]", title="Prune", border_style="green")
        )
        return

    table = Table(title="Backup Snapshots to Prune", expand=True)
    table.add_column("Path", style="cyan")
    for path in candidates:
        table.add_row(path)
    console.print(table)

    if dry_run:
        console.print(f"[yellow][Dry Run] {len(candidates)} snapshot(s) would be pruned.[/]")
        return

    if not yes:
        confirmed = typer.confirm(f"Permanently delete {len(candidates)} backup snapshot(s)?")
        if not confirmed:
            console.print("[yellow]Prune cancelled.[/]")
            return

    deleted = BackupPruner.prune(max_count=max_count, max_age_days=max_age_days, dry_run=False)
    console.print(
        Panel(f"[green]✓ Pruned {len(deleted)} backup snapshot(s).[/]", title="Prune Complete", border_style="green")
    )


@workspace_app.command("list")
def workspace_list() -> None:
    """List all registered workspaces."""
    workspaces = WorkspaceService.list_workspaces()
    if not workspaces:
        console.print(
            Panel(
                "No revive workspaces are registered in the global configuration (~/.config/rv/workspaces.yaml).\n"
                "Run [bold green]rv init[/] or [bold green]rv workspace add <path>[/] to register one.",
                title="Workspaces Registry",
                border_style="yellow",
            )
        )
        return

    table = Table(title="Registered Revive Workspaces", expand=True)
    table.add_column("Name", style="green", width=20)
    table.add_column("Path", style="cyan")
    table.add_column("Last Accessed", style="dim", width=25)

    for ws in workspaces:
        table.add_row(ws.name, ws.path, ws.last_accessed.strftime("%Y-%m-%d %H:%M:%S"))

    console.print(table)


@workspace_app.command("add")
def workspace_add(
    path: str = typer.Argument(..., help="Path to the revive repository."),
    name: str | None = typer.Option(None, "--name", "-n", help="Friendly name for the workspace."),
) -> None:
    """Register an existing directory as a revive workspace."""
    if not os.path.isdir(path):
        console.print(
            Panel(
                f"[bold red]Error:[/] '{path}' is not a directory.",
                title="Registration Failed",
                border_style="red",
            )
        )
        raise typer.Exit(code=1)

    manifest_path = os.path.join(path, "manifest.yaml")
    warning_msg = ""
    if not os.path.exists(manifest_path):
        warning_msg = "\n\n[bold yellow]Warning:[/] No manifest.yaml found at this path. Registering anyway."

    ws = WorkspaceService.register_workspace(path, name)
    console.print(
        Panel(
            "[bold green]Success![/] Workspace registered successfully.\n\n"
            f"[bold white]Friendly Name:[/] [magenta]{ws.name}[/]\n"
            f"[bold white]Absolute Path:[/] [cyan]{ws.path}[/]"
            f"{warning_msg}",
            title="Workspace Registered",
            border_style="green",
        )
    )


@workspace_app.command("remove")
def workspace_remove(name: str = typer.Argument(..., help="Name of the workspace to remove.")) -> None:
    """Unregister a workspace by name."""
    if WorkspaceService.remove_workspace(name):
        console.print(
            Panel(
                "[bold green]Success![/] Workspace de-registered successfully.\n\n"
                f"[bold white]Workspace Name:[/] [magenta]{name}[/]\n"
                "The physical workspace directory and files were not modified.",
                title="Workspace Unregistered",
                border_style="green",
            )
        )
    else:
        console.print(
            Panel(
                f"[bold red]Error:[/] Workspace '{name}' not found.",
                title="Unregistration Failed",
                border_style="red",
            )
        )
        raise typer.Exit(code=1)


@workspace_app.command("sync")
def workspace_sync(
    profile: str | None = typer.Option(None, "--profile", "-p", help="Profile name to restore after git pull."),
    dry_run: bool = typer.Option(False, "--dry-run", help="Preview sync operations without executing."),
    identity: str | None = typer.Option(None, "--identity", "-i", help="Path to age identity file for secrets."),
) -> None:
    """Pull latest changes and restore all registered workspaces (git pull → rv restore)."""
    import subprocess

    workspaces = WorkspaceService.list_workspaces()
    if not workspaces:
        console.print(
            Panel(
                "No workspaces registered. Use [bold green]rv workspace add <path>[/] first.",
                title="Workspace Sync",
                border_style="yellow",
            )
        )
        return

    results_table = Table(title="Workspace Sync Results", expand=True)
    results_table.add_column("Workspace", style="cyan", width=20)
    results_table.add_column("Path", style="white")
    results_table.add_column("Git Pull", style="yellow", width=12)
    results_table.add_column("Restore", style="green", width=12)
    results_table.add_column("Details", style="dim")

    for ws in workspaces:
        git_status = "✓"
        restore_status = "✓"
        details = ""

        # Step 1: git pull
        if not dry_run:
            try:
                git_result = subprocess.run(
                    ["git", "-C", ws.path, "pull", "--ff-only"],
                    capture_output=True,
                    text=True,
                    check=False,
                )
                if git_result.returncode != 0:
                    git_status = "[red]✗[/]"
                    details = f"git pull failed: {git_result.stderr.strip()[:80]}"
                    results_table.add_row(ws.name, ws.path, git_status, "—", details)
                    continue
                else:
                    git_status = "[green]✓[/]"
            except Exception as e:
                git_status = "[red]✗[/]"
                details = f"git error: {e}"
                results_table.add_row(ws.name, ws.path, git_status, "—", details)
                continue
        else:
            git_status = "[yellow]skip[/]"

        # Step 2: rv restore
        restore_profile = profile
        if not restore_profile:
            # Try to detect the default profile from workspace manifest
            try:
                from rv.services.restore import ManifestLoader

                manifest_path = os.path.join(ws.path, "manifest.yaml")
                if os.path.exists(manifest_path):
                    manifest = ManifestLoader.load(manifest_path)
                    if manifest.profiles:
                        restore_profile = next(iter(manifest.profiles))
            except Exception:
                pass

        if restore_profile:
            if not dry_run:
                try:
                    RestoreService.restore(
                        repo_dir=ws.path,
                        profile_name=restore_profile,
                        identity_path=identity,
                        interactive=False,
                        dry_run=False,
                        no_plugins=False,
                    )
                    restore_status = "[green]✓[/]"
                except Exception as e:
                    restore_status = "[red]✗[/]"
                    details = f"restore failed: {str(e)[:80]}"
            else:
                restore_status = "[yellow]skip[/]"
        else:
            restore_status = "[yellow]no profile[/]"
            details = "No profile specified or detected"

        results_table.add_row(ws.name, ws.path, git_status, restore_status, details)

    console.print(results_table)
