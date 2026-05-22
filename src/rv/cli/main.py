"""Main Typer CLI application for Revive (rv)."""

import os

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
    name="rv", help="Revive (rv) — Production-grade environment lifecycle manager CLI.", add_completion=False
)
secret_app = typer.Typer(name="secret", help="Cryptographic secret management commands.")
workspace_app = typer.Typer(name="workspace", help="Manage registered revive workspaces.")
app.add_typer(secret_app)
app.add_typer(workspace_app)

console = Console()


def _get_repo_dir() -> str:
    """Returns the current working directory as the revive repository path."""
    return os.getcwd()


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

    # Basic manifest template
    manifest_template = """# Revive Configuration Manifest
version: 2

assets:
  - id: example_zshrc
    type: symlink
    source: assets/example_zshrc
    target: ~/.zshrc
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

    # Example zshrc asset
    example_zshrc_path = os.path.join(repo_dir, "assets", "example_zshrc")
    with open(example_zshrc_path, "w", encoding="utf-8") as f:
        f.write('# Example zshrc managed by Revive\nexport PATH="$HOME/.bin:$PATH"\n')

    with open(manifest_path, "w", encoding="utf-8") as f:
        f.write(manifest_template)

    # Register workspace
    WorkspaceService.register_workspace(repo_dir)

    console.print(
        Panel(
            "[bold green]Success![/] Revive environment scaffolded and registered successfully.\n\n"
            "[bold white]Directories created:[/]\n"
            "  - [cyan]assets/[/] (file and symlink assets)\n"
            "  - [cyan]secrets/[/] (encrypted secrets)\n"
            "  - [cyan]machine/[/] (host-specific overrides)\n\n"
            "[bold white]Files created:[/]\n"
            "  - [cyan]manifest.yaml[/] (your global config manifest)\n"
            "  - [cyan]assets/example_zshrc[/] (example zshrc asset)\n\n"
            "Ready to manage! Try running [bold yellow]rv status --profile base[/]",
            title="Revive Initialized",
            border_style="green",
        )
    )


@app.command("restore")
def restore(
    profile: str = typer.Argument(..., help="Name of the deployment profile to restore."),
    identity: str | None = typer.Option(
        None, "--identity", "-i", help="Path to age identity file for decrypting secrets."
    ),
    dry_run: bool = typer.Option(
        False, "--dry-run", help="Plan and validate operations without mutating the filesystem."
    ),
    interactive: bool = typer.Option(
        True, "--interactive/--non-interactive", help="Toggle interactive prompting for file conflicts."
    ),
    no_plugins: bool = typer.Option(False, "--no-plugins", help="Skip executing any plugin hooks during restore."),
) -> None:
    """Synchronize the local environment state to match the repository profile (repo -> system)."""
    repo_dir = _get_repo_dir()

    try:
        RestoreService.restore(
            repo_dir=repo_dir,
            profile_name=profile,
            identity_path=identity,
            interactive=interactive,
            dry_run=dry_run,
            no_plugins=no_plugins,
        )
    except Exception as e:
        console.print(f"[bold red]Transaction Failed:[/] {e}")
        raise typer.Exit(code=2)


@app.command("status")
def status(
    profile: str = typer.Option(..., "--profile", "-p", help="Profile to evaluate sync status for."),
    identity: str | None = typer.Option(None, "--identity", "-i", help="Age identity file to check secret drift."),
) -> None:
    """Compare system state against repository profile and calculate drift."""
    repo_dir = _get_repo_dir()

    try:
        report = StatusService.get_status(repo_dir, profile, identity)
    except Exception as e:
        console.print(f"[bold red]Status check failed:[/] {e}")
        raise typer.Exit(code=1)

    table = Table(title=f"Drift Analysis for Profile '{profile}'", expand=True)
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
            info["target"],
            status_styled,
            info["details"],
        )

    console.print(table)

    if report["drifted"]:
        console.print("[bold yellow]Warning:[/] System drift detected. Run [bold green]rv restore[/] to synchronize.")
        raise typer.Exit(code=0)
    else:
        console.print("[bold green]In Sync:[/] Environment is perfectly synchronized with the repository state.")


def _render_side_by_side_diff(
    expected_text: str, actual_text: str, source_name: str, target_name: str
) -> Table:
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
    profile: str = typer.Option(..., "--profile", "-p", help="Profile name to check drift for."),
    identity: str | None = typer.Option(None, "--identity", "-i", help="Age identity file to diff encrypted secrets."),
    unified: bool = typer.Option(False, "--unified", "-u", help="Display standard unified diff format."),
) -> None:
    """Print colored diffs of all modified file assets on the filesystem."""
    repo_dir = _get_repo_dir()

    try:
        report = StatusService.get_status(repo_dir, profile, identity)
    except Exception as e:
        console.print(f"[bold red]Failed to get drift status:[/] {e}")
        raise typer.Exit(code=1)

    has_diffs = False

    for asset_id, info in report["assets"].items():
        if info["status"] == "modified":
            if unified:
                diff_text = StatusService.get_diff(repo_dir, profile, asset_id, identity)
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
                contents = StatusService.get_contents_for_diff(repo_dir, profile, asset_id, identity)
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
                            resolved = ProfileResolver.resolve(manifest, profile)
                            asset = resolved.assets.get(asset_id) or resolved.secrets.get(asset_id)
                            if asset:
                                source_name = f"repo://{asset.source}"
                        except Exception:
                            pass

                        diff_table = _render_side_by_side_diff(
                            expected_text, actual_text, source_name, info["target"]
                        )
                        console.print(
                            Panel(
                                diff_table,
                                title=f"Drift Diff (Side-by-Side): {asset_id} -> {info['target']}",
                                border_style="yellow",
                            )
                        )

    if not has_diffs:
        console.print("[green]No file content modifications detected.[/]")


@app.command("doctor")
def doctor(
    profile: str | None = typer.Option(
        None, "--profile", "-p", help="Optionally run doctor checks specific to a profile."
    ),
    json_format: bool = typer.Option(False, "--json", help="Output diagnostic report in structured JSON format."),
) -> None:
    """Evaluate repository sanity, permission safety, and system capabilities."""
    repo_dir = _get_repo_dir()

    report = DoctorService.check_health(repo_dir, profile)

    if json_format:
        import json

        console.print_json(json.dumps(report))
        raise typer.Exit(code=0 if report["healthy"] else 1)

    console.print(
        Panel(
            f"[bold white]Sanity Check summary:[/] "
            f"{'[bold green]HEALTHY[/]' if report['healthy'] else '[bold red]ISSUES FOUND[/]'}\n"
            f"Checks run: {report['checks_run']}",
            title="Revive System Doctor",
            border_style="green" if report["healthy"] else "red",
        )
    )

    # Print tools
    tools_table = Table(title="System Tool Integration")
    tools_table.add_column("Tool / Integration", style="cyan")
    tools_table.add_column("Status", style="bold")

    for tool, available in report["tools"].items():
        status_styled = "[green]Available[/]" if available else "[yellow]Missing[/]"
        tools_table.add_row(tool, status_styled)
    console.print(tools_table)

    # Print issues
    if report["issues"]:
        console.print("\n[bold red]Issues Detected:[/]")
        for issue in report["issues"]:
            prefix = "[bold red][Critical][/]" if issue["severity"] == "critical" else "[yellow][Warning][/]"
            console.print(f" {prefix} ({issue['category']}): {issue['message']}")
        raise typer.Exit(code=1)
    else:
        console.print("\n[bold green]Perfect![/] No issues detected in repository setup.")


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
        console.print(f"[bold green]Successfully encrypted secret[/] to '{output_path}'.")
    except Exception as e:
        console.print(f"[bold red]Encryption failed:[/] {e}")
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
        console.print(f"[bold green]Successfully decrypted secret[/] to '{output_path}'.")
    except Exception as e:
        console.print(f"[bold red]Decryption failed:[/] {e}")
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
            console.print(f"[bold green]Success:[/] Secret at '{file_path}' successfully rotated to new recipients.")
        except Exception as e:
            console.print(f"[bold red]Rotation failed:[/] {e}")
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

            console.print("[bold green]Success:[/] Generated a new age keypair.")
            console.print(f"Private identity key saved to: [cyan]{output}[/]")
            console.print(f"Public recipient key: [bold yellow]{public_key}[/]")
        else:
            console.print(f"Private identity key:\n[bold cyan]{private_key}[/]\n")
            console.print(f"Public recipient key:\n[bold yellow]{public_key}[/]")
    except Exception as e:
        console.print(f"[bold red]Key generation failed:[/] {e}")
        raise typer.Exit(code=1)


@app.command("watch")
def watch(
    profile: str = typer.Option(..., "--profile", "-p", help="Profile to monitor and auto-apply changes for."),
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
    daemon = WatchdogDaemon(repo_dir=repo_dir, profile_name=profile, identity_path=identity, debounce_seconds=debounce)
    try:
        daemon.start()
        # Keep main thread alive while watcher runs
        while True:
            time.sleep(1)
    except KeyboardInterrupt:
        console.print("\n[yellow]Stopping watchdog daemon...[/]")
        daemon.stop()
        console.print("[green]Watchdog daemon stopped.[/]")


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
            console.print("[green]No incomplete transactions found.[/]")
            raise typer.Exit(code=0)

        if auto:
            latest = journals[0]
            console.print(f"[yellow]Auto-recovering latest transaction {latest.tx_id}...[/]")
            RecoveryService.rollback_journal(latest)
            console.print(f"[green]Transaction {latest.tx_id} successfully rolled back.[/]")
            raise typer.Exit(code=0)

        # Interactive mode
        console.print(f"[yellow]Found {len(journals)} incomplete transaction(s):[/]")
        for journal in journals:
            console.print(f"\n[cyan]Transaction:[/] {journal.tx_id}")
            console.print(f"  [cyan]Timestamp:[/] {journal.timestamp}")
            console.print(f"  [cyan]Status:[/] {journal.status}")

            while True:
                action = typer.prompt("Action? ([r]ollback, [d]iscard, [s]kip)", default="s").strip().lower()

                if action in ("r", "rollback"):
                    try:
                        RecoveryService.rollback_journal(journal)
                        console.print(f"[green]Transaction {journal.tx_id} rolled back.[/]")
                    except Exception as e:
                        console.print(f"[bold red]Rollback failed:[/] {e}")
                    break
                elif action in ("d", "discard"):
                    RecoveryService.discard_journal(journal)
                    console.print(f"[yellow]Transaction {journal.tx_id} journal discarded.[/]")
                    break
                elif action in ("s", "skip"):
                    console.print("[yellow]Skipping transaction recovery.[/]")
                    break
                else:
                    console.print("[red]Invalid action. Please choose r, d, or s.[/]")
    except typer.Exit:
        raise
    except Exception as e:
        console.print(f"[bold red]Recovery failed:[/] {e}")
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
            f"[bold yellow]Warning:[/] An installation wrapper already exists at '{target_path}'. "
            "Use '--force' or '-f' to overwrite it."
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
        console.print(f"[bold red]Self-installation failed:[/] {e}")
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
                console.print(f"[green]Removed wrapper:[/] {target_path}")
                removed_count += 1
            except Exception as e:
                console.print(f"[red]Failed to remove wrapper:[/] {e}")
        else:
            console.print(
                f"[yellow]Skipped wrapper:[/] '{target_path}' does not look like an autogenerated wrapper. "
                "Use '--force' to remove it."
            )

    # 2. Remove isolated install root
    if os.path.exists(install_root):
        try:
            shutil.rmtree(install_root)
            console.print(f"[green]Removed isolated installation:[/] {install_root}")
            removed_count += 1
        except Exception as e:
            console.print(f"[red]Failed to remove installation root:[/] {e}")

    # 3. Purge config if requested
    if purge_config and os.path.exists(config_root):
        try:
            shutil.rmtree(config_root)
            console.print(f"[green]Purged configuration:[/] {config_root}")
            removed_count += 1
        except Exception as e:
            console.print(f"[red]Failed to purge configuration:[/] {e}")

    if removed_count > 0:
        console.print("\n[bold green]Revive CLI uninstalled successfully.[/]")
    else:
        console.print("\n[yellow]Nothing to uninstall.[/]")


@app.command("tui")
def tui() -> None:
    """Launch the interactive Revive TUI."""
    from rv.cli.tui import start_tui

    start_tui()


@workspace_app.command("list")
def workspace_list() -> None:
    """List all registered workspaces."""
    workspaces = WorkspaceService.list_workspaces()
    if not workspaces:
        console.print("[yellow]No workspaces registered.[/]")
        return

    table = Table(title="Registered Revive Workspaces")
    table.add_column("Name", style="green")
    table.add_column("Path", style="cyan")
    table.add_column("Last Accessed", style="dim")

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
        console.print(f"[bold red]Error:[/] '{path}' is not a directory.")
        raise typer.Exit(code=1)

    manifest_path = os.path.join(path, "manifest.yaml")
    if not os.path.exists(manifest_path):
        console.print(f"[bold yellow]Warning:[/] No manifest.yaml found at '{path}'. Registering anyway.")

    ws = WorkspaceService.register_workspace(path, name)
    console.print(f"[bold green]Registered workspace:[/] {ws.name} ({ws.path})")


@workspace_app.command("remove")
def workspace_remove(name: str = typer.Argument(..., help="Name of the workspace to remove.")) -> None:
    """Unregister a workspace by name."""
    if WorkspaceService.remove_workspace(name):
        console.print(f"[bold green]Unregistered workspace:[/] {name}")
    else:
        console.print(f"[bold red]Error:[/] Workspace '{name}' not found.")
        raise typer.Exit(code=1)
