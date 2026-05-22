"""Rich-based TUI for Revive."""

import os
import sys
import typer
from rich.console import Console
from rich.panel import Panel
from rich.table import Table
from rich.prompt import Prompt, IntPrompt
from rich.live import Live
from rich.align import Align

from rv.services.workspace import WorkspaceService
from rv.services.status import StatusService
from rv.services.doctor import DoctorService
from rv.services.restore import RestoreService, ManifestLoader, ProfileResolver
from rv.security.encryptor import AgeEncryptor

class ReviveTUI:
    """Menu-driven Terminal User Interface for Revive."""

    def __init__(self) -> None:
        self.console = Console()
        self.workspace = WorkspaceService.get_current_workspace()
        self.running = True

    def clear(self) -> None:
        """Clear the terminal screen."""
        os.system('cls' if os.name == 'nt' else 'clear')

    def render_header(self) -> None:
        """Render the TUI header."""
        title = "[bold green]Revive Control Center[/]"
        if self.workspace:
            subtitle = f"Active Workspace: [cyan]{self.workspace.name}[/] ([dim]{self.workspace.path}[/])"
        else:
            subtitle = "[yellow]No active workspace detected. Please register or select one.[/]"
        
        self.console.print(Panel(Align.center(subtitle), title=title, border_style="green"))

    def run(self) -> None:
        """Start the TUI main loop."""
        while self.running:
            try:
                self.clear()
                self.render_header()
                
                if self.workspace:
                    self._workspace_menu()
                else:
                    self._global_menu()
            except KeyboardInterrupt:
                self.running = False
            except Exception as e:
                self.console.print(f"[bold red]Error:[/] {e}")
                input("\nPress Enter to continue...")

    def _global_menu(self) -> None:
        """Menu shown when no workspace is active."""
        table = Table(show_header=False, box=None)
        table.add_column("Key", style="bold yellow")
        table.add_column("Action")
        
        table.add_row("1", "List & Select Workspace")
        table.add_row("2", "Register Current Directory as Workspace")
        table.add_row("3", "Initialize New Workspace here (rv init)")
        table.add_row("q", "Exit")
        
        self.console.print(table)
        
        choice = Prompt.ask("Choice", choices=["1", "2", "3", "q"], default="q")
        
        if choice == "q":
            self.running = False
        elif choice == "1":
            self._select_workspace()
        elif choice == "2":
            ws = WorkspaceService.register_workspace(os.getcwd())
            self.workspace = ws
            self.console.print(f"[green]Registered workspace: {ws.name}[/]")
            import time
            time.sleep(1)
        elif choice == "3":
            self.console.print("[yellow]Please run 'rv init' from the command line first or use the register option if already initialized.[/]")
            input("\nPress Enter to continue...")

    def _workspace_menu(self) -> None:
        """Menu shown when a workspace is active."""
        table = Table(show_header=False, box=None)
        table.add_column("Key", style="bold yellow")
        table.add_column("Action")
        
        table.add_row("1", "Status Analysis (rv status)")
        table.add_row("2", "Restore Environment (rv restore)")
        table.add_row("3", "Run System Doctor (rv doctor)")
        table.add_row("4", "Manage Secrets (rv secret)")
        table.add_row("5", "Import Asset (File/Secret)")
        table.add_row("6", "Import Plugin (Skill)")
        table.add_row("7", "Export Asset/Secret")
        table.add_row("8", "Switch Workspace")
        table.add_row("q", "Exit")
        
        self.console.print(table)
        
        choice = Prompt.ask("Choice", choices=["1", "2", "3", "4", "5", "6", "7", "8", "q"], default="q")
        
        if choice == "q":
            self.running = False
        elif choice == "1":
            self._show_status()
        elif choice == "2":
            self._run_restore()
        elif choice == "3":
            self._run_doctor()
        elif choice == "4":
            self._manage_secrets()
        elif choice == "5":
            self._import_asset()
        elif choice == "6":
            self._import_plugin()
        elif choice == "7":
            self._export_asset()
        elif choice == "8":
            self.workspace = None

    def _select_workspace(self) -> None:
        """List and select a workspace."""
        workspaces = WorkspaceService.list_workspaces()
        if not workspaces:
            self.console.print("[yellow]No registered workspaces found.[/]")
            input("\nPress Enter to continue...")
            return
        
        table = Table(title="Registered Workspaces")
        table.add_column("#", style="cyan")
        table.add_column("Name", style="green")
        table.add_column("Path", style="dim")
        
        for i, ws in enumerate(workspaces):
            table.add_row(str(i+1), ws.name, ws.path)
        
        self.console.print(table)
        choice = Prompt.ask("Select workspace # (or 'b' for back)", default="b")
        if choice.isdigit() and 1 <= int(choice) <= len(workspaces):
            self.workspace = workspaces[int(choice)-1]

    def _show_status(self) -> None:
        """Run status check."""
        if not self.workspace: return
        
        profile = Prompt.ask("Enter profile to check", default="base")
        try:
            with self.console.status("[bold green]Analyzing drift..."):
                report = StatusService.get_status(self.workspace.path, profile)
            
            table = Table(title=f"Drift Analysis for '{profile}'")
            table.add_column("Asset ID", style="cyan")
            table.add_column("Status", style="bold")
            
            for asset_id, info in report["assets"].items():
                table.add_row(asset_id, info["status"])
            
            self.console.print(table)
        except Exception as e:
            self.console.print(f"[bold red]Error:[/] {e}")
        
        input("\nPress Enter to continue...")

    def _run_restore(self) -> None:
        """Run restore."""
        if not self.workspace: return
        
        profile = Prompt.ask("Enter profile to restore", default="base")
        dry_run = Prompt.ask("Dry run?", choices=["y", "n"], default="n") == "y"
        
        try:
            RestoreService.restore(
                repo_dir=self.workspace.path,
                profile_name=profile,
                dry_run=dry_run,
                interactive=True
            )
            self.console.print("[bold green]Restore operation completed![/]")
        except Exception as e:
            self.console.print(f"[bold red]Restore failed:[/] {e}")
        
        input("\nPress Enter to continue...")

    def _run_doctor(self) -> None:
        """Run doctor."""
        if not self.workspace: return
        
        try:
            with self.console.status("[bold blue]Running system diagnostics..."):
                report = DoctorService.check_health(self.workspace.path)
            
            self.console.print(f"Health Status: {'[bold green]HEALTHY[/]' if report['healthy'] else '[bold red]ISSUES FOUND[/]'}")
            if report["issues"]:
                for issue in report["issues"]:
                    self.console.print(f"- {issue['message']}")
            else:
                self.console.print("[green]No issues found![/]")
        except Exception as e:
            self.console.print(f"[bold red]Doctor failed:[/] {e}")
        
        input("\nPress Enter to continue...")

    def _manage_secrets(self) -> None:
        """Simple secret management sub-menu."""
        self.console.print(Panel("Secret Management", style="cyan"))
        # Implementation deferred or kept simple for now
        self.console.print("1. Generate Keypair")
        self.console.print("b. Back")
        choice = Prompt.ask("Choice", choices=["1", "b"], default="b")
        if choice == "1":
            try:
                pub, priv = AgeEncryptor.generate_keypair()
                self.console.print(f"Public Key: [bold yellow]{pub}[/]")
                self.console.print(f"Private Key: [bold cyan]{priv}[/]")
                self.console.print("[yellow]SAVE THESE SECURELY![/]")
            except Exception as e:
                self.console.print(f"[red]Failed:[/] {e}")
            input("\nPress Enter to continue...")

    def _import_asset(self) -> None:
        """Help user import an asset into the current workspace."""
        if not self.workspace:
            return

        import shutil

        import yaml

        from rv.models.manifest import Asset, AssetType, Secret

        self.console.print(Panel("Import Asset Helper", style="magenta"))
        source_path = Prompt.ask("Enter source file path to import")
        source_path = os.path.expanduser(source_path)

        if not os.path.exists(source_path):
            self.console.print("[bold red]Error:[/] File does not exist.")
            input("\nPress Enter to continue...")
            return

        asset_id = Prompt.ask("Enter asset ID (unique name)", default=os.path.basename(source_path))
        target_path = Prompt.ask("Enter target path (e.g. ~/.config/myapp/config.yaml)")
        asset_type_str = Prompt.ask("Asset type", choices=["copy", "symlink", "template", "secret"], default="copy")

        # Load manifest
        manifest_path = os.path.join(self.workspace.path, "manifest.yaml")
        try:
            manifest = ManifestLoader.load(manifest_path)
        except Exception as e:
            self.console.print(f"[bold red]Failed to load manifest:[/] {e}")
            input("\nPress Enter to continue...")
            return

        # Check for duplicates
        if any(a.id == asset_id for a in manifest.assets) or any(s.id == asset_id for s in manifest.secrets):
            self.console.print(f"[bold red]Error:[/] Asset ID '{asset_id}' already exists.")
            input("\nPress Enter to continue...")
            return

        try:
            if asset_type_str == "secret":
                recipient = Prompt.ask("Enter age public key for encryption (recipient)")
                dest_rel = os.path.join("secrets", f"{asset_id}.age")
                dest_abs = os.path.join(self.workspace.path, dest_rel)

                os.makedirs(os.path.dirname(dest_abs), exist_ok=True)
                AgeEncryptor.encrypt_file(source_path, dest_abs, [recipient])

                new_secret = Secret(id=asset_id, source=dest_rel, target=target_path)
                manifest.secrets.append(new_secret)
                self.console.print(f"[green]Encrypted and stored at {dest_rel}[/]")
            else:
                dest_rel = os.path.join("assets", os.path.basename(source_path))
                dest_abs = os.path.join(self.workspace.path, dest_rel)

                os.makedirs(os.path.dirname(dest_abs), exist_ok=True)
                shutil.copy2(source_path, dest_abs)

                new_asset = Asset(
                    id=asset_id, type=AssetType(asset_type_str), source=dest_rel, target=target_path
                )
                manifest.assets.append(new_asset)
                self.console.print(f"[green]Copied to {dest_rel}[/]")

            # Add to 'base' profile if it exists, or the first profile found
            profile_name = "base"
            if profile_name not in manifest.profiles and manifest.profiles:
                profile_name = next(iter(manifest.profiles))

            if profile_name in manifest.profiles:
                if asset_type_str == "secret":
                    manifest.profiles[profile_name].secrets.append(asset_id)
                else:
                    manifest.profiles[profile_name].assets.append(asset_id)
                self.console.print(f"[green]Added to profile '{profile_name}'[/]")

            # Save manifest
            with open(manifest_path, "w", encoding="utf-8") as f:
                # Use model_dump(mode='json') to get serializable dict, then clean up
                data = manifest.model_dump(mode="json", exclude_none=True)
                yaml.dump(data, f, sort_keys=False)

            self.console.print(f"[bold green]Successfully imported asset '{asset_id}'![/]")
        except Exception as e:
            self.console.print(f"[bold red]Import failed:[/] {e}")

        input("\nPress Enter to continue...")

    def _import_plugin(self) -> None:
        """Help user import a plugin (skill) into the workspace."""
        if not self.workspace:
            return

        import shutil

        self.console.print(Panel("Import Plugin Helper", style="blue"))
        plugin_src_dir = Prompt.ask("Enter path to the plugin directory (containing plugin.yaml)")
        plugin_src_dir = os.path.expanduser(plugin_src_dir)

        if not os.path.isdir(plugin_src_dir):
            self.console.print("[bold red]Error:[/] Directory does not exist.")
            input("\nPress Enter to continue...")
            return

        manifest_path = os.path.join(plugin_src_dir, "plugin.yaml")
        if not os.path.exists(manifest_path):
            self.console.print("[bold red]Error:[/] plugin.yaml not found in the directory.")
            input("\nPress Enter to continue...")
            return

        plugin_name = os.path.basename(plugin_src_dir.rstrip("/"))
        dest_dir = os.path.join(self.workspace.path, "plugins", plugin_name)

        if os.path.exists(dest_dir):
            self.console.print(f"[bold red]Error:[/] Plugin '{plugin_name}' already exists in workspace.")
            input("\nPress Enter to continue...")
            return

        try:
            os.makedirs(os.path.dirname(dest_dir), exist_ok=True)
            shutil.copytree(plugin_src_dir, dest_dir)
            self.console.print(f"[bold green]Successfully imported plugin '{plugin_name}'![/]")
        except Exception as e:
            self.console.print(f"[bold red]Import failed:[/] {e}")

        input("\nPress Enter to continue...")

    def _export_asset(self) -> None:
        """Help user export an asset or secret from the workspace."""
        if not self.workspace:
            return

        import shutil

        self.console.print(Panel("Export Asset Helper", style="yellow"))

        # Load manifest
        manifest_path = os.path.join(self.workspace.path, "manifest.yaml")
        try:
            manifest = ManifestLoader.load(manifest_path)
        except Exception as e:
            self.console.print(f"[bold red]Failed to load manifest:[/] {e}")
            input("\nPress Enter to continue...")
            return

        assets_and_secrets = []
        for a in manifest.assets:
            assets_and_secrets.append((a.id, a.source, "asset"))
        for s in manifest.secrets:
            assets_and_secrets.append((s.id, s.source, "secret"))

        if not assets_and_secrets:
            self.console.print("[yellow]No assets or secrets found in manifest.[/]")
            input("\nPress Enter to continue...")
            return

        table = Table(title="Available Assets & Secrets")
        table.add_column("#", style="cyan")
        table.add_column("ID", style="green")
        table.add_column("Type", style="magenta")

        for i, (aid, src, atype) in enumerate(assets_and_secrets):
            table.add_row(str(i + 1), aid, atype)

        self.console.print(table)
        choice = Prompt.ask("Select item # to export (or 'b' for back)", default="b")
        if not (choice.isdigit() and 1 <= int(choice) <= len(assets_and_secrets)):
            return

        aid, src, atype = assets_and_secrets[int(choice) - 1]
        dest_path = Prompt.ask("Enter destination path (including filename)")
        dest_path = os.path.expanduser(dest_path)

        src_abs = os.path.join(self.workspace.path, src)

        try:
            if atype == "secret":
                identity = Prompt.ask("Enter path to age identity file to decrypt secret for export")
                AgeEncryptor.decrypt_file(src_abs, dest_path, identity)
                self.console.print(f"[bold green]Successfully decrypted and exported secret to {dest_path}[/]")
            else:
                shutil.copy2(src_abs, dest_path)
                self.console.print(f"[bold green]Successfully exported asset to {dest_path}[/]")
        except Exception as e:
            self.console.print(f"[bold red]Export failed:[/] {e}")

        input("\nPress Enter to continue...")

def start_tui() -> None:
    """Entry point for the TUI."""
    tui = ReviveTUI()
    tui.run()
