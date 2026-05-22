"""High-end Textual-based TUI for Revive."""

import os
import shutil
from pathlib import Path
from typing import Iterable

import yaml
from textual import on, work
from textual.app import App, ComposeResult
from textual.containers import Container, Horizontal, Vertical
from textual.screen import ModalScreen, Screen
from textual.widgets import (
    Button,
    DirectoryTree,
    Footer,
    Header,
    Input,
    Label,
    ListItem,
    ListView,
    OptionList,
    Static,
    TabbedContent,
    TabPane,
)
from textual.widgets.option_list import Option

from rv.models.manifest import Asset, AssetType, Secret
from rv.security.encryptor import AgeEncryptor
from rv.services.doctor import DoctorService
from rv.services.restore import ManifestLoader, RestoreService
from rv.services.status import StatusService
from rv.services.workspace import WorkspaceService


class FileSelectorModal(ModalScreen[Path]):
    """A modal screen for selecting a file or directory using a tree."""

    def __init__(self, mode: str = "file", title: str = "Select Path", **kwargs):
        super().__init__(**kwargs)
        self.mode = mode
        self.title_text = title

    def compose(self) -> ComposeResult:
        with Vertical(id="modal_container"):
            yield Label(self.title_text, id="modal_title")
            yield DirectoryTree(os.path.expanduser("~"), id="dir_tree")
            with Horizontal(id="modal_buttons"):
                yield Button("Cancel", variant="error", id="cancel")
                yield Button("Select", variant="success", id="select")

    @on(DirectoryTree.FileSelected)
    def on_file_selected(self, event: DirectoryTree.FileSelected) -> None:
        if self.mode == "file":
            self.dismiss(event.path)

    @on(Button.Pressed, "#cancel")
    def on_cancel(self) -> None:
        self.dismiss(None)

    @on(Button.Pressed, "#select")
    def on_select(self) -> None:
        selected = self.query_one(DirectoryTree).cursor_node.data.path
        if self.mode == "dir" and os.path.isdir(selected):
            self.dismiss(selected)
        elif self.mode == "file" and os.path.isfile(selected):
            self.dismiss(selected)
        elif self.mode == "any":
            self.dismiss(selected)


class ReviveApp(App):
    """The main Revive TUI application."""

    CSS = """
    Screen {
        align: center middle;
    }

    #main_container {
        width: 100%;
        height: 100%;
    }

    #modal_container {
        width: 60%;
        height: 70%;
        border: thick $primary;
        background: $surface;
        padding: 1;
    }

    #modal_title {
        text-align: center;
        width: 100%;
        margin-bottom: 1;
        text-style: bold;
    }

    #modal_buttons {
        height: 3;
        align: center middle;
        margin-top: 1;
    }

    #modal_buttons Button {
        margin: 0 1;
    }

    OptionList {
        border: solid $accent;
        margin: 1;
    }

    .header-panel {
        background: $primary;
        color: white;
        padding: 1;
        text-align: center;
        text-style: bold;
    }

    #status_area {
        height: 10;
        border: double $primary;
        margin: 1;
        padding: 1;
        overflow-y: scroll;
    }

    .info-label {
        margin-left: 2;
        color: $secondary;
    }
    """

    BINDINGS = [
        ("q", "quit", "Quit"),
        ("r", "refresh", "Refresh"),
    ]

    def __init__(self):
        super().__init__()
        self.workspace = WorkspaceService.get_current_workspace()

    def compose(self) -> ComposeResult:
        yield Header()
        with Container(id="main_container"):
            yield Static(id="header_text", classes="header-panel")
            with TabbedContent():
                with TabPane("Dashboard", id="tab_dashboard"):
                    yield Label("Workspace Actions", classes="info-label")
                    yield OptionList(
                        Option("Status Analysis", id="status"),
                        Option("Restore Environment", id="restore"),
                        Option("Run System Doctor", id="doctor"),
                        id="workspace_actions"
                    )
                with TabPane("Assets", id="tab_assets"):
                    yield Label("Asset Management", classes="info-label")
                    yield OptionList(
                        Option("Import Asset (File)", id="import_file"),
                        Option("Import Secret", id="import_secret"),
                        Option("Export Asset/Secret", id="export"),
                        Option("Import Plugin (Skill)", id="import_plugin"),
                        id="asset_actions"
                    )
                with TabPane("Secrets", id="tab_secrets"):
                    yield Label("Secrets Management", classes="info-label")
                    yield OptionList(
                        Option("Generate Keypair", id="keygen"),
                        id="secret_actions"
                    )
                with TabPane("Workspaces", id="tab_workspaces"):
                    yield Label("Registered Workspaces", classes="info-label")
                    yield OptionList(id="workspace_list")
                    yield Button("Register Current Directory", id="register_ws")

            yield Static("Ready", id="status_area")
        yield Footer()

    def on_mount(self) -> None:
        self.update_header()
        self.refresh_workspace_list()

    def update_header(self) -> None:
        if self.workspace:
            self.query_one("#header_text").update(f"Active Workspace: {self.workspace.name} ({self.workspace.path})")
        else:
            self.query_one("#header_text").update("No active workspace selected")

    def log_status(self, message: str) -> None:
        status_area = self.query_one("#status_area")
        status_area.update(f"{status_area.renderable}\n{message}")

    def refresh_workspace_list(self) -> None:
        workspaces = WorkspaceService.list_workspaces()
        option_list = self.query_one("#workspace_list", OptionList)
        option_list.clear_options()
        for ws in workspaces:
            option_list.add_option(Option(f"{ws.name} ({ws.path})", id=f"ws_{ws.name}"))

    @on(OptionList.OptionSelected, "#workspace_actions")
    def handle_workspace_action(self, event: OptionList.OptionSelected) -> None:
        if not self.workspace:
            self.notify("No workspace selected", variant="error")
            return

        action_id = event.option.id
        if action_id == "status":
            self.run_status()
        elif action_id == "restore":
            self.run_restore()
        elif action_id == "doctor":
            self.run_doctor()

    @on(OptionList.OptionSelected, "#asset_actions")
    async def handle_asset_action(self, event: OptionList.OptionSelected) -> None:
        if not self.workspace:
            self.notify("No workspace selected", variant="error")
            return

        action_id = event.option.id
        if action_id == "import_file":
            await self.action_import_asset(is_secret=False)
        elif action_id == "import_secret":
            await self.action_import_asset(is_secret=True)
        elif action_id == "export":
            await self.action_export_asset()
        elif action_id == "import_plugin":
            await self.action_import_plugin()

    @on(OptionList.OptionSelected, "#secret_actions")
    def handle_secret_action(self, event: OptionList.OptionSelected) -> None:
        action_id = event.option.id
        if action_id == "keygen":
            self.run_keygen()

    @on(OptionList.OptionSelected, "#workspace_list")
    def handle_workspace_select(self, event: OptionList.OptionSelected) -> None:
        ws_name = event.option.id.replace("ws_", "")
        workspaces = WorkspaceService.list_workspaces()
        for ws in workspaces:
            if ws.name == ws_name:
                self.workspace = ws
                WorkspaceService.register_workspace(ws.path) # Update last accessed
                self.update_header()
                self.notify(f"Switched to workspace: {ws_name}")
                break

    @on(Button.Pressed, "#register_ws")
    def handle_register_ws(self) -> None:
        ws = WorkspaceService.register_workspace(os.getcwd())
        self.workspace = ws
        self.update_header()
        self.refresh_workspace_list()
        self.notify(f"Registered current directory as workspace: {ws.name}")

    @work
    async def run_status(self) -> None:
        self.log_status("[bold blue]Running status analysis...[/]")
        try:
            report = StatusService.get_status(self.workspace.path, "base")
            self.log_status(f"Drift Analysis for 'base': {len(report['assets'])} assets checked.")
            for aid, info in report["assets"].items():
                self.log_status(f" - {aid}: {info['status']}")
        except Exception as e:
            self.log_status(f"[bold red]Error:[/] {e}")

    @work
    async def run_restore(self) -> None:
        self.log_status("[bold green]Running restore...[/]")
        try:
            # We use non-interactive for TUI unless we implement a custom prompter
            RestoreService.restore(
                repo_dir=self.workspace.path,
                profile_name="base",
                dry_run=False,
                interactive=False
            )
            self.log_status("[bold green]Restore completed![/]")
        except Exception as e:
            self.log_status(f"[bold red]Restore failed:[/] {e}")

    @work
    async def run_doctor(self) -> None:
        self.log_status("[bold cyan]Running system doctor...[/]")
        try:
            report = DoctorService.check_health(self.workspace.path)
            self.log_status(f"Health: {'HEALTHY' if report['healthy'] else 'ISSUES FOUND'}")
            for issue in report["issues"]:
                self.log_status(f" - {issue['message']}")
        except Exception as e:
            self.log_status(f"[bold red]Doctor failed:[/] {e}")

    def run_keygen(self) -> None:
        try:
            pub, priv = AgeEncryptor.generate_keypair()
            self.log_status(f"Generated Keypair:\nPublic: {pub}\nPrivate: {priv}")
            self.notify("Keypair generated. See status area.")
        except Exception as e:
            self.notify(f"Keygen failed: {e}", variant="error")

    async def action_import_asset(self, is_secret: bool) -> None:
        path = await self.push_screen_wait(FileSelectorModal(title="Select Asset to Import"))
        if not path:
            return

        asset_id = os.path.basename(path)
        target_path = f"~/.config/revive_imported/{asset_id}"

        self.log_status(f"Importing {path} as {'secret' if is_secret else 'asset'}...")

        manifest_path = os.path.join(self.workspace.path, "manifest.yaml")
        try:
            manifest = ManifestLoader.load(manifest_path)
        except Exception as e:
            self.notify(f"Failed to load manifest: {e}", variant="error")
            return

        try:
            if is_secret:
                # In a high-end TUI, we'd prompt for the recipient key.
                # For now, we'll try to find one in the environment or use a default.
                recipient = os.environ.get("REVIVE_PUBKEY", "age1...") 
                
                if recipient == "age1...":
                    self.log_status("[yellow]Warning: Using placeholder recipient. Set REVIVE_PUBKEY env var.[/]")

                dest_rel = os.path.join("secrets", f"{asset_id}.age")
                dest_abs = os.path.join(self.workspace.path, dest_rel)
                os.makedirs(os.path.dirname(dest_abs), exist_ok=True)
                
                AgeEncryptor.encrypt_file(str(path), dest_abs, [recipient])
                
                new_secret = Secret(id=asset_id, source=dest_rel, target=target_path)
                manifest.secrets.append(new_secret)
                
                # Add to base profile
                if "base" in manifest.profiles:
                    manifest.profiles["base"].secrets.append(asset_id)
            else:
                dest_rel = os.path.join("assets", asset_id)
                dest_abs = os.path.join(self.workspace.path, dest_rel)
                os.makedirs(os.path.dirname(dest_abs), exist_ok=True)
                shutil.copy2(path, dest_abs)
                
                new_asset = Asset(id=asset_id, type=AssetType.COPY, source=dest_rel, target=target_path)
                manifest.assets.append(new_asset)
                
                # Add to base profile
                if "base" in manifest.profiles:
                    manifest.profiles["base"].assets.append(asset_id)

            # Save manifest
            with open(manifest_path, "w", encoding="utf-8") as f:
                data = manifest.model_dump(mode="json", exclude_none=True)
                yaml.dump(data, f, sort_keys=False)

            self.notify(f"Successfully imported {asset_id}")
            self.log_status(f"[green]Successfully imported {asset_id}[/]")
        except Exception as e:
            self.notify(f"Import failed: {e}", variant="error")
            self.log_status(f"[bold red]Import failed:[/] {e}")

    async def action_export_asset(self) -> None:
        # High-end export would list assets in an OptionList
        self.notify("Export asset logic triggered")

    async def action_import_plugin(self) -> None:
        path = await self.push_screen_wait(FileSelectorModal(mode="dir", title="Select Plugin Directory"))
        if path:
            self.notify(f"Importing plugin from {path}")
            # ... implementation ...


def start_tui() -> None:
    """Entry point for the TUI."""
    app = ReviveApp()
    app.run()
