"""High-end Agentic Textual-based TUI for Revive."""

import os
import shlex
import shutil
from dataclasses import dataclass
from pathlib import Path

import yaml
from textual import on, work
from textual.app import App, ComposeResult
from textual.containers import Container, Horizontal, Vertical
from textual.screen import ModalScreen
from textual.widgets import (
    Button,
    DirectoryTree,
    Footer,
    Header,
    Input,
    Label,
    OptionList,
    RichLog,
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


@dataclass(frozen=True)
class AgentCommand:
    """A slash command exposed by the TUI command center."""

    path: str
    title: str
    description: str
    requires_workspace: bool = True


COMMANDS: dict[str, AgentCommand] = {
    "/status": AgentCommand(
        path="/status",
        title="Analyze drift",
        description="Check the active workspace against a profile. Usage: /status [profile] [--identity path]",
    ),
    "/restore": AgentCommand(
        path="/restore",
        title="Restore environment",
        description="Apply a profile to the machine. Usage: /restore [profile] [--dry-run] [--identity path]",
    ),
    "/doctor": AgentCommand(
        path="/doctor",
        title="Run diagnostics",
        description="Inspect repository health and local tool availability. Usage: /doctor [profile]",
    ),
    "/secret keygen": AgentCommand(
        path="/secret keygen",
        title="Generate age keypair",
        description="Create a new age identity and recipient key. Usage: /secret keygen",
        requires_workspace=False,
    ),
    "/workspace list": AgentCommand(
        path="/workspace list",
        title="List workspaces",
        description="Show registered Revive workspaces. Usage: /workspace list",
        requires_workspace=False,
    ),
    "/workspace add": AgentCommand(
        path="/workspace add",
        title="Register workspace",
        description="Register a workspace path. Usage: /workspace add [path]",
        requires_workspace=False,
    ),
    "/help": AgentCommand(
        path="/help",
        title="Show commands",
        description="List slash commands and suggested next steps. Usage: /help [prefix]",
        requires_workspace=False,
    ),
    "/clear": AgentCommand(
        path="/clear",
        title="Clear transcript",
        description="Clear the agent transcript. Usage: /clear",
        requires_workspace=False,
    ),
}


@dataclass(frozen=True)
class ParsedCommand:
    """A parsed slash command with positional args and flags."""

    path: str
    args: tuple[str, ...]
    flags: dict[str, str | bool]


def parse_agent_command(raw_command: str) -> ParsedCommand:
    """Parse a TUI slash command, including subcommands and simple flags."""
    tokens = shlex.split(raw_command.strip())
    if not tokens:
        raise ValueError("Type /help to see available commands.")

    if not tokens[0].startswith("/"):
        tokens[0] = f"/{tokens[0]}"

    if len(tokens) > 1 and f"{tokens[0]} {tokens[1]}" in COMMANDS:
        path = f"{tokens[0]} {tokens[1]}"
        tail = tokens[2:]
    else:
        path = tokens[0]
        tail = tokens[1:]

    if path not in COMMANDS:
        raise ValueError(f"Unknown command: {path}")

    args: list[str] = []
    flags: dict[str, str | bool] = {}
    index = 0
    while index < len(tail):
        token = tail[index]
        if token.startswith("--"):
            key = token[2:].replace("-", "_")
            if index + 1 < len(tail) and not tail[index + 1].startswith("--"):
                flags[key] = tail[index + 1]
                index += 2
            else:
                flags[key] = True
                index += 1
        else:
            args.append(token)
            index += 1

    return ParsedCommand(path=path, args=tuple(args), flags=flags)


def suggest_commands(prefix: str = "") -> list[AgentCommand]:
    """Return commands matching a slash prefix or plain text fragment."""
    normalized = prefix.strip().lower()
    if normalized and not normalized.startswith("/"):
        normalized = f"/{normalized}"
    return [command for path, command in COMMANDS.items() if path.startswith(normalized)]


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
    """The main Revive TUI application with an agentic feel."""

    CSS = """
    Screen {
        background: $surface;
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
        border: solid $primary;
        margin: 1;
        height: 1fr;
    }

    #sidebar {
        width: 36;
        min-width: 32;
        height: 100%;
        border-right: solid $primary;
    }

    #agent_view {
        width: 1fr;
        height: 100%;
    }

    .header-panel {
        background: $primary;
        color: white;
        padding: 1;
        text-align: center;
        text-style: bold;
    }

    #status_log {
        height: 1fr;
        border: double $primary;
        margin: 1;
        background: $surface;
    }

    #suggestion_bar {
        height: 5;
        margin: 0 1;
        padding: 0 1;
        border: solid $secondary;
        color: $text-muted;
    }

    #command_input {
        margin: 0 1 1 1;
        border: solid $secondary;
    }

    .info-label {
        margin-left: 2;
        color: $secondary;
        text-style: bold;
    }
    """

    BINDINGS = [
        ("q", "quit", "Quit"),
        ("r", "refresh", "Refresh"),
        ("c", "focus_input", "Command"),
        ("/", "slash_command", "Slash"),
    ]

    def __init__(self):
        super().__init__()
        self.workspace = WorkspaceService.get_current_workspace()

    def compose(self) -> ComposeResult:
        yield Header()
        with Container(id="main_container"):
            yield Static(id="header_text", classes="header-panel")
            with Horizontal():
                with Vertical(id="sidebar", name="Navigation"):
                    with TabbedContent():
                        with TabPane("Commands", id="tab_commands"):
                            yield Label("Slash Command Center", classes="info-label")
                            yield OptionList(
                                Option("/status  Analyze drift", id="/status"),
                                Option("/restore  Restore profile", id="/restore"),
                                Option("/doctor  Diagnose setup", id="/doctor"),
                                Option("/secret keygen  Generate keys", id="/secret keygen"),
                                Option("/workspace list  Show workspaces", id="/workspace list"),
                                Option("/workspace add  Register cwd", id="/workspace add"),
                                id="command_palette",
                            )
                        with TabPane("Assets", id="tab_assets"):
                            yield Label("Management", classes="info-label")
                            yield OptionList(
                                Option("Import Asset (File)", id="import_file"),
                                Option("Import Secret", id="import_secret"),
                                Option("Export Asset/Secret", id="export"),
                                id="asset_actions"
                            )
                        with TabPane("Workspaces", id="tab_workspaces"):
                            yield Label("Registry", classes="info-label")
                            yield OptionList(id="workspace_list")
                            yield Button("Register Current Dir", id="register_ws")

                with Vertical(id="agent_view"):
                    yield Label("Agent Transcript", classes="info-label")
                    yield RichLog(id="status_log", highlight=True, markup=True)
                    yield Static(id="suggestion_bar")
                    yield Input(placeholder="Type /help, /status, /restore --dry-run, or choose a command", id="command_input")

        yield Footer()

    def on_mount(self) -> None:
        self.update_header()
        self.refresh_workspace_list()
        self.log_status("[bold green]Revive agent online.[/] Choose a slash command or type /help.")
        self.suggest_next_steps("start")

    def update_header(self) -> None:
        if self.workspace:
            self.query_one("#header_text").update(f"Revive Agent | Active: {self.workspace.name} ({self.workspace.path})")
        else:
            self.query_one("#header_text").update("Revive Agent | No Workspace Detected")

    def log_status(self, message: str) -> None:
        log = self.query_one("#status_log", RichLog)
        log.write(message)

    def action_focus_input(self) -> None:
        self.query_one("#command_input").focus()

    def action_slash_command(self) -> None:
        command_input = self.query_one("#command_input", Input)
        if not command_input.value.startswith("/"):
            command_input.value = "/"
        command_input.focus()

    def refresh_workspace_list(self) -> None:
        workspaces = WorkspaceService.list_workspaces()
        option_list = self.query_one("#workspace_list", OptionList)
        option_list.clear_options()
        for ws in workspaces:
            option_list.add_option(Option(f"{ws.name}", id=f"ws_{ws.name}"))

    @on(Input.Submitted, "#command_input")
    def handle_command(self, event: Input.Submitted) -> None:
        command = event.value.strip()
        if not command:
            return

        self.log_status(f"[bold cyan]user[/] {command}")
        event.input.value = ""
        self.dispatch_agent_command(command)

    @on(OptionList.OptionSelected, "#command_palette")
    def handle_command_palette(self, event: OptionList.OptionSelected) -> None:
        command = str(event.option.id)
        if command == "/workspace add":
            command = f"{command} {shlex.quote(os.getcwd())}"
        self.query_one("#command_input", Input).value = command
        self.log_status(f"[bold cyan]user[/] {command}")
        self.dispatch_agent_command(command)

    def dispatch_agent_command(self, raw_command: str) -> None:
        try:
            parsed = parse_agent_command(raw_command)
        except ValueError as e:
            self.log_status(f"[bold red]agent[/] {e}")
            self.suggest_next_steps("unknown")
            return

        command = COMMANDS[parsed.path]
        if command.requires_workspace and not self.workspace:
            self.log_status("[bold red]agent[/] No workspace selected. Run /workspace add [path] first.")
            self.suggest_next_steps("no_workspace")
            return

        if parsed.path == "/status":
            self.run_status(self._profile_from(parsed), self._identity_from(parsed))
        elif parsed.path == "/restore":
            self.run_restore(self._profile_from(parsed), self._identity_from(parsed), bool(parsed.flags.get("dry_run")))
        elif parsed.path == "/doctor":
            self.run_doctor(self._profile_from(parsed))
        elif parsed.path == "/secret keygen":
            self.run_keygen()
        elif parsed.path == "/workspace list":
            self.run_workspace_list()
        elif parsed.path == "/workspace add":
            self.run_workspace_add(parsed.args[0] if parsed.args else os.getcwd())
        elif parsed.path == "/help":
            self.run_help(parsed.args[0] if parsed.args else "")
        elif parsed.path == "/clear":
            self.query_one("#status_log", RichLog).clear()
            self.suggest_next_steps("start")

    def _profile_from(self, parsed: ParsedCommand) -> str:
        profile = parsed.flags.get("profile")
        if isinstance(profile, str):
            return profile
        return parsed.args[0] if parsed.args else "base"

    def _identity_from(self, parsed: ParsedCommand) -> str | None:
        identity = parsed.flags.get("identity")
        return identity if isinstance(identity, str) else None

    def suggest_next_steps(self, context: str) -> None:
        suggestions = {
            "start": "/status base  |  /doctor  |  /workspace list",
            "status_clean": "/doctor  |  /restore base --dry-run",
            "status_drift": "/restore base --dry-run  |  /restore base",
            "restore": "/status base  |  /doctor",
            "doctor": "/status base  |  /workspace list",
            "no_workspace": "/workspace list  |  /workspace add .",
            "unknown": "/help  |  /status base  |  /doctor",
            "workspace": "/status base  |  /doctor",
            "secret": "Set REVIVE_PUBKEY for imports  |  /status base",
        }
        self.query_one("#suggestion_bar", Static).update(f"Next: {suggestions.get(context, suggestions['start'])}")

    @on(OptionList.OptionSelected, "#workspace_actions")
    def handle_workspace_action(self, event: OptionList.OptionSelected) -> None:
        if not self.workspace:
            self.notify("No workspace selected", severity="error")
            return

        action_id = event.option.id
        if action_id == "status":
            self.run_status("base")
        elif action_id == "restore":
            self.run_restore("base")
        elif action_id == "doctor":
            self.run_doctor("base")

    @on(OptionList.OptionSelected, "#asset_actions")
    async def handle_asset_action(self, event: OptionList.OptionSelected) -> None:
        if not self.workspace:
            self.notify("No workspace selected", severity="error")
            return

        action_id = event.option.id
        if action_id == "import_file":
            await self.action_import_asset(is_secret=False)
        elif action_id == "import_secret":
            await self.action_import_asset(is_secret=True)
        elif action_id == "export":
            await self.action_export_asset()

    @on(OptionList.OptionSelected, "#workspace_list")
    def handle_workspace_select(self, event: OptionList.OptionSelected) -> None:
        ws_name = event.option.id.replace("ws_", "")
        workspaces = WorkspaceService.list_workspaces()
        for ws in workspaces:
            if ws.name == ws_name:
                self.workspace = ws
                WorkspaceService.register_workspace(ws.path) 
                self.update_header()
                self.notify(f"Switched to workspace: {ws_name}")
                self.log_status(f"Agent context switched to: [bold]{ws_name}[/]")
                self.suggest_next_steps("workspace")
                break

    @on(Button.Pressed, "#register_ws")
    def handle_register_ws(self) -> None:
        self.run_workspace_add(os.getcwd())

    @work
    async def run_status(self, profile: str = "base", identity: str | None = None) -> None:
        self.log_status(f"[bold blue]agent[/] Analyzing drift for profile [bold]{profile}[/]...")
        try:
            report = StatusService.get_status(self.workspace.path, profile, identity)
            self.log_status(f"Checked [bold]{len(report['assets'])}[/] assets. Drift: [bold]{report['drifted']}[/]")
            for aid, info in report["assets"].items():
                status_style = "green" if info['status'] == "in_sync" else "red"
                self.log_status(f" - {aid}: [{status_style}]{info['status']}[/]")
            self.suggest_next_steps("status_drift" if report["drifted"] else "status_clean")
        except Exception as e:
            self.log_status(f"[bold red]agent[/] Analysis failed: {e}")
            self.suggest_next_steps("doctor")

    @work
    async def run_restore(
        self, profile: str = "base", identity: str | None = None, dry_run: bool = False
    ) -> None:
        mode = "planning" if dry_run else "applying"
        self.log_status(f"[bold green]agent[/] {mode.title()} restore for profile [bold]{profile}[/]...")
        try:
            RestoreService.restore(
                repo_dir=self.workspace.path,
                profile_name=profile,
                identity_path=identity,
                dry_run=dry_run,
                interactive=False,
            )
            if dry_run:
                self.log_status("[bold green]agent[/] Dry-run completed. No files were changed.")
            else:
                self.log_status("[bold green]agent[/] System restored to manifest state.")
            self.suggest_next_steps("restore")
        except Exception as e:
            self.log_status(f"[bold red]agent[/] Restoration failed: {e}")
            self.suggest_next_steps("doctor")

    @work
    async def run_doctor(self, profile: str | None = None) -> None:
        self.log_status("[bold cyan]agent[/] Running system diagnostics...")
        try:
            report = DoctorService.check_health(self.workspace.path if self.workspace else os.getcwd(), profile)
            health_color = "green" if report['healthy'] else "red"
            self.log_status(f"System Health: [{health_color}]{'HEALTHY' if report['healthy'] else 'ISSUES FOUND'}[/]")
            for issue in report["issues"]:
                self.log_status(f" [yellow]![/] {issue['message']}")
            self.suggest_next_steps("doctor")
        except Exception as e:
            self.log_status(f"[bold red]agent[/] Doctor diagnostic failed: {e}")
            self.suggest_next_steps("unknown")

    def run_keygen(self) -> None:
        try:
            self.log_status("[bold magenta]agent[/] Generating new cryptographic identity...")
            pub, priv = AgeEncryptor.generate_keypair()
            self.log_status(f"Generated Keypair:\nPublic: [yellow]{pub}[/]\nPrivate: [cyan]{priv}[/]")
            self.notify("Keypair generated. See logs.")
            self.suggest_next_steps("secret")
        except Exception as e:
            self.notify(f"Keygen failed: {e}", severity="error")

    def run_workspace_list(self) -> None:
        workspaces = WorkspaceService.list_workspaces()
        if not workspaces:
            self.log_status("[yellow]agent[/] No workspaces registered.")
            self.suggest_next_steps("no_workspace")
            return
        self.log_status(f"[bold cyan]agent[/] Registered workspaces ({len(workspaces)}):")
        for ws in workspaces:
            current_marker = " [bold green]*[/]" if self.workspace and ws.path == self.workspace.path else ""
            self.log_status(f" - {ws.name}: [cyan]{ws.path}[/]{current_marker}")
        self.suggest_next_steps("workspace")

    def run_workspace_add(self, path: str) -> None:
        ws = WorkspaceService.register_workspace(os.path.abspath(os.path.expanduser(path)))
        self.workspace = ws
        self.update_header()
        self.refresh_workspace_list()
        self.notify(f"Registered: {ws.name}")
        self.log_status(f"[bold cyan]agent[/] Workspace registered and selected: [bold]{ws.name}[/] ({ws.path})")
        self.suggest_next_steps("workspace")

    def run_help(self, prefix: str = "") -> None:
        commands = suggest_commands(prefix)
        if not commands:
            self.log_status(f"[yellow]agent[/] No commands match '{prefix}'.")
            self.suggest_next_steps("unknown")
            return
        self.log_status("[bold cyan]agent[/] Available slash commands:")
        for command in commands:
            self.log_status(f" - [bold]{command.path}[/] - {command.description}")
        self.suggest_next_steps("start")

    async def action_import_asset(self, is_secret: bool) -> None:
        path = await self.push_screen_wait(FileSelectorModal(title=f"Select {'Secret' if is_secret else 'Asset'} to Import"))
        if not path:
            return

        asset_id = os.path.basename(path)
        target_path = f"~/.config/revive_imported/{asset_id}"

        self.log_status(f"Agent importing {path} as {'secret' if is_secret else 'asset'}...")

        manifest_path = os.path.join(self.workspace.path, "manifest.yaml")
        try:
            manifest = ManifestLoader.load(manifest_path)
        except Exception as e:
            self.notify(f"Failed to load manifest: {e}", severity="error")
            return

        try:
            if is_secret:
                recipient = os.environ.get("REVIVE_PUBKEY", "age1...") 
                if recipient == "age1...":
                    self.log_status("[yellow]Warning: Using placeholder recipient. Set REVIVE_PUBKEY env var.[/]")

                dest_rel = os.path.join("secrets", f"{asset_id}.age")
                dest_abs = os.path.join(self.workspace.path, dest_rel)
                os.makedirs(os.path.dirname(dest_abs), exist_ok=True)
                
                AgeEncryptor.encrypt_file(str(path), dest_abs, [recipient])
                
                new_secret = Secret(id=asset_id, source=dest_rel, target=target_path)
                manifest.secrets.append(new_secret)
                if "base" in manifest.profiles:
                    manifest.profiles["base"].secrets.append(asset_id)
            else:
                dest_rel = os.path.join("assets", asset_id)
                dest_abs = os.path.join(self.workspace.path, dest_rel)
                os.makedirs(os.path.dirname(dest_abs), exist_ok=True)
                shutil.copy2(path, dest_abs)
                
                new_asset = Asset(id=asset_id, type=AssetType.COPY, source=dest_rel, target=target_path)
                manifest.assets.append(new_asset)
                if "base" in manifest.profiles:
                    manifest.profiles["base"].assets.append(asset_id)

            with open(manifest_path, "w", encoding="utf-8") as f:
                data = manifest.model_dump(mode="json", exclude_none=True)
                yaml.dump(data, f, sort_keys=False)

            self.notify(f"Imported {asset_id}")
            self.log_status(f"[green]Successfully ingested {asset_id} into workspace manifest.[/]")
        except Exception as e:
            self.notify(f"Import failed: {e}", severity="error")
            self.log_status(f"[bold red]Import operation failed:[/] {e}")

    async def action_export_asset(self) -> None:
        self.notify("Export asset logic triggered")


def start_tui() -> None:
    """Entry point for the TUI."""
    app = ReviveApp()
    app.run()
