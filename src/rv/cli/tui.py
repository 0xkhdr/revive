"""High-end Agentic Textual-based TUI for Revive."""

import os
import shlex
import shutil
from dataclasses import dataclass
from pathlib import Path

import yaml
from textual import on, work
from textual.app import App, ComposeResult
from textual.containers import Container, Grid, Horizontal, Vertical
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
    "/asset list": AgentCommand(
        path="/asset list",
        title="List manifest assets",
        description="Show assets and secrets in the active workspace. Usage: /asset list",
    ),
    "/asset import": AgentCommand(
        path="/asset import",
        title="Import file asset",
        description="Copy a file into assets/ and add it to a profile. Usage: /asset import [path] [--target path] [--profile base] [--id name]",
    ),
    "/asset import-secret": AgentCommand(
        path="/asset import-secret",
        title="Import encrypted secret",
        description="Encrypt a file into secrets/ and add it to a profile. Usage: /asset import-secret [path] --recipient age1... [--target path]",
    ),
    "/asset export": AgentCommand(
        path="/asset export",
        title="Export asset",
        description="Copy an asset source out of the workspace. Usage: /asset export <id> [output] [--identity path]",
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
    "/workspace use": AgentCommand(
        path="/workspace use",
        title="Switch workspace",
        description="Switch the active TUI context. Usage: /workspace use <name>",
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
        background: #101318;
        color: #d7dde8;
    }

    #main_container {
        width: 100%;
        height: 100%;
        background: #101318;
    }

    #workspace_strip {
        height: 4;
        padding: 0 1;
        background: #171b22;
        border-bottom: tall #3b82f6;
        color: #d7dde8;
    }

    #modal_container {
        width: 60%;
        height: 70%;
        border: thick #3b82f6;
        background: #171b22;
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
        border: none;
        margin: 0 1 1 1;
        height: 1fr;
        background: #171b22;
        color: #d7dde8;
    }

    #sidebar {
        width: 40;
        min-width: 36;
        height: 100%;
        background: #171b22;
        border-right: tall #2f3541;
    }

    #agent_view {
        width: 1fr;
        height: 100%;
        background: #101318;
    }

    #session_grid {
        height: 6;
        margin: 1 1 0 1;
        grid-size: 3 1;
        grid-columns: 1fr 1fr 1fr;
        grid-gutter: 1;
    }

    .metric {
        height: 100%;
        padding: 1;
        background: #171b22;
        border: solid #2f3541;
        color: #d7dde8;
    }

    .metric-title {
        color: #8b95a7;
    }

    .header-panel {
        background: #3b82f6;
        color: #ffffff;
        padding: 1;
        text-align: left;
        text-style: bold;
    }

    #status_log {
        height: 1fr;
        border: solid #2f3541;
        margin: 1;
        background: #0b0d11;
        color: #d7dde8;
    }

    #suggestion_bar {
        height: 5;
        margin: 0 1;
        padding: 0 1;
        border: solid #22c55e;
        background: #121a17;
        color: #a7f3d0;
    }

    #command_input {
        margin: 0 1 1 1;
        border: tall #3b82f6;
        background: #0b0d11;
        color: #ffffff;
    }

    .info-label {
        margin: 1 1 0 1;
        color: #8b95a7;
        text-style: bold;
    }

    TabbedContent {
        background: #171b22;
    }

    TabPane {
        background: #171b22;
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
            yield Static(id="workspace_strip", classes="header-panel")
            with Horizontal():
                with Vertical(id="sidebar", name="Navigation"):
                    with TabbedContent():
                        with TabPane("Commands", id="tab_commands"):
                            yield Label("Command Deck", classes="info-label")
                            yield OptionList(
                                Option("/status  Analyze drift", id="/status"),
                                Option("/restore  Restore profile", id="/restore"),
                                Option("/doctor  Diagnose setup", id="/doctor"),
                                Option("/asset list  Show assets", id="/asset list"),
                                Option("/asset import  Import file", id="/asset import"),
                                Option("/asset import-secret  Encrypt secret", id="/asset import-secret"),
                                Option("/asset export  Export item", id="/asset export"),
                                Option("/secret keygen  Generate keys", id="/secret keygen"),
                                Option("/workspace list  Show workspaces", id="/workspace list"),
                                Option("/workspace add  Register cwd", id="/workspace add"),
                                Option("/workspace use  Switch workspace", id="/workspace use"),
                                id="command_palette",
                            )
                        with TabPane("Assets", id="tab_assets"):
                            yield Label("Asset Commands", classes="info-label")
                            yield OptionList(
                                Option("/asset list", id="/asset list"),
                                Option("/asset import", id="/asset import"),
                                Option("/asset import-secret", id="/asset import-secret"),
                                Option("/asset export", id="/asset export"),
                                id="asset_actions"
                            )
                        with TabPane("Workspaces", id="tab_workspaces"):
                            yield Label("Registry", classes="info-label")
                            yield OptionList(id="workspace_list")
                            yield Button("Register Current Dir", id="register_ws")

                with Vertical(id="agent_view"):
                    with Grid(id="session_grid"):
                        yield Static("Workspace\n-", id="metric_workspace", classes="metric")
                        yield Static("Mode\ncommand", id="metric_mode", classes="metric")
                        yield Static("Profile\nbase", id="metric_profile", classes="metric")
                    yield Label("Agent Transcript", classes="info-label")
                    yield RichLog(id="status_log", highlight=True, markup=True)
                    yield Static(id="suggestion_bar")
                    yield Input(placeholder="/help, /status, /asset import, /restore --dry-run", id="command_input")

        yield Footer()

    def on_mount(self) -> None:
        self.update_header()
        self.refresh_workspace_list()
        self.log_status("[bold green]Revive agent online.[/] Choose a slash command or type /help.")
        self.suggest_next_steps("start")

    def update_header(self) -> None:
        if self.workspace:
            self.query_one("#workspace_strip").update(f"Revive Agent | Active: {self.workspace.name} | {self.workspace.path}")
            self.query_one("#metric_workspace", Static).update(f"Workspace\n{self.workspace.name}")
        else:
            self.query_one("#workspace_strip").update("Revive Agent | No Workspace Detected")
            self.query_one("#metric_workspace", Static).update("Workspace\nnone")

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
    async def handle_command(self, event: Input.Submitted) -> None:
        command = event.value.strip()
        if not command:
            return

        self.log_status(f"[bold cyan]user[/] {command}")
        event.input.value = ""
        await self.dispatch_agent_command(command)

    @on(OptionList.OptionSelected, "#command_palette")
    async def handle_command_palette(self, event: OptionList.OptionSelected) -> None:
        command = str(event.option.id)
        if command == "/workspace add":
            command = f"{command} {shlex.quote(os.getcwd())}"
        self.query_one("#command_input", Input).value = command
        self.log_status(f"[bold cyan]user[/] {command}")
        await self.dispatch_agent_command(command)

    async def dispatch_agent_command(self, raw_command: str) -> None:
        try:
            parsed = parse_agent_command(raw_command)
        except ValueError as e:
            self.log_status(f"[bold red]agent[/] {e}")
            self.suggest_next_steps("unknown")
            return

        command = COMMANDS[parsed.path]
        self.query_one("#metric_mode", Static).update(f"Mode\n{command.title}")
        if command.requires_workspace and not self.workspace:
            self.log_status("[bold red]agent[/] No workspace selected. Run /workspace add [path] first.")
            self.suggest_next_steps("no_workspace")
            return

        if parsed.path == "/status":
            self.query_one("#metric_profile", Static).update(f"Profile\n{self._profile_from(parsed)}")
            self.run_status(self._profile_from(parsed), self._identity_from(parsed))
        elif parsed.path == "/restore":
            self.query_one("#metric_profile", Static).update(f"Profile\n{self._profile_from(parsed)}")
            self.run_restore(self._profile_from(parsed), self._identity_from(parsed), bool(parsed.flags.get("dry_run")))
        elif parsed.path == "/doctor":
            self.query_one("#metric_profile", Static).update(f"Profile\n{self._profile_from(parsed)}")
            self.run_doctor(self._profile_from(parsed))
        elif parsed.path == "/asset list":
            self.run_asset_list()
        elif parsed.path == "/asset import":
            await self.run_asset_import(parsed, is_secret=False)
        elif parsed.path == "/asset import-secret":
            await self.run_asset_import(parsed, is_secret=True)
        elif parsed.path == "/asset export":
            self.run_asset_export(parsed)
        elif parsed.path == "/secret keygen":
            self.run_keygen()
        elif parsed.path == "/workspace list":
            self.run_workspace_list()
        elif parsed.path == "/workspace add":
            self.run_workspace_add(parsed.args[0] if parsed.args else os.getcwd())
        elif parsed.path == "/workspace use":
            self.run_workspace_use(parsed.args[0] if parsed.args else "")
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
            "start": "/status base  |  /asset list  |  /doctor",
            "status_clean": "/doctor  |  /restore base --dry-run",
            "status_drift": "/restore base --dry-run  |  /restore base",
            "restore": "/status base  |  /doctor",
            "doctor": "/status base  |  /workspace list",
            "no_workspace": "/workspace list  |  /workspace add .",
            "unknown": "/help  |  /asset list  |  /doctor",
            "workspace": "/status base  |  /asset list  |  /doctor",
            "asset": "/asset list  |  /status base  |  /restore base --dry-run",
            "secret": "/asset import-secret --recipient age1...  |  /status base",
        }
        self.query_one("#suggestion_bar", Static).update(f"Next: {suggestions.get(context, suggestions['start'])}")

    @on(OptionList.OptionSelected, "#asset_actions")
    async def handle_asset_action(self, event: OptionList.OptionSelected) -> None:
        if not self.workspace:
            self.notify("No workspace selected", severity="error")
            return

        command = str(event.option.id)
        self.query_one("#command_input", Input).value = command
        self.log_status(f"[bold cyan]user[/] {command}")
        await self.dispatch_agent_command(command)

    @on(OptionList.OptionSelected, "#workspace_list")
    async def handle_workspace_select(self, event: OptionList.OptionSelected) -> None:
        ws_name = event.option.id.replace("ws_", "")
        command = f"/workspace use {shlex.quote(ws_name)}"
        self.query_one("#command_input", Input).value = command
        self.log_status(f"[bold cyan]user[/] {command}")
        await self.dispatch_agent_command(command)

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

    def run_workspace_use(self, name: str) -> None:
        if not name:
            self.log_status("[yellow]agent[/] Usage: /workspace use <name>")
            self.suggest_next_steps("workspace")
            return
        for ws in WorkspaceService.list_workspaces():
            if ws.name == name:
                self.workspace = WorkspaceService.register_workspace(ws.path)
                self.update_header()
                self.notify(f"Switched to workspace: {ws.name}")
                self.log_status(f"[bold cyan]agent[/] Workspace context switched to [bold]{ws.name}[/].")
                self.suggest_next_steps("workspace")
                return
        self.log_status(f"[bold red]agent[/] Workspace not found: {name}")
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

    def run_asset_list(self) -> None:
        manifest_path = os.path.join(self.workspace.path, "manifest.yaml")
        try:
            manifest = ManifestLoader.load(manifest_path)
        except Exception as e:
            self.log_status(f"[bold red]agent[/] Failed to load manifest: {e}")
            self.suggest_next_steps("doctor")
            return

        if not manifest.assets and not manifest.secrets:
            self.log_status("[yellow]agent[/] Manifest has no assets or secrets yet.")
            self.suggest_next_steps("asset")
            return

        self.log_status("[bold cyan]agent[/] Manifest inventory:")
        for asset in manifest.assets:
            self.log_status(f" - [bold]{asset.id}[/] asset:{asset.type.value} {asset.source} -> {asset.target}")
        for secret in manifest.secrets:
            self.log_status(f" - [bold]{secret.id}[/] secret {secret.source} -> {secret.target}")
        self.suggest_next_steps("asset")

    async def run_asset_import(self, parsed: ParsedCommand, is_secret: bool) -> None:
        path = parsed.args[0] if parsed.args else None
        if not path:
            title = "Select Secret to Import" if is_secret else "Select Asset to Import"
            selected_path = await self.push_screen_wait(FileSelectorModal(title=title))
            if not selected_path:
                self.log_status("[yellow]agent[/] Import cancelled.")
                self.suggest_next_steps("asset")
                return
            path = str(selected_path)

        recipient = parsed.flags.get("recipient")
        if is_secret and not isinstance(recipient, str):
            env_recipient = os.environ.get("REVIVE_PUBKEY")
            recipient = env_recipient if env_recipient else None

        if is_secret and not recipient:
            self.log_status("[bold red]agent[/] Secret import needs --recipient age1... or REVIVE_PUBKEY.")
            self.suggest_next_steps("secret")
            return

        asset_id = parsed.flags.get("id")
        target = parsed.flags.get("target")
        profile = parsed.flags.get("profile")
        try:
            self.import_manifest_item(
                source_path=path,
                is_secret=is_secret,
                asset_id=asset_id if isinstance(asset_id, str) else None,
                target_path=target if isinstance(target, str) else None,
                profile=profile if isinstance(profile, str) else "base",
                recipient=recipient if isinstance(recipient, str) else None,
            )
        except Exception as e:
            self.notify(f"Import failed: {e}", severity="error")
            self.log_status(f"[bold red]agent[/] Import failed: {e}")
            self.suggest_next_steps("asset")

    def import_manifest_item(
        self,
        source_path: str,
        is_secret: bool,
        asset_id: str | None = None,
        target_path: str | None = None,
        profile: str = "base",
        recipient: str | None = None,
    ) -> None:
        abs_source = os.path.abspath(os.path.expanduser(source_path))
        if not os.path.isfile(abs_source):
            raise ValueError(f"Import source is not a file: {abs_source}")

        item_id = asset_id or os.path.basename(abs_source)
        target = target_path or f"~/.config/revive_imported/{item_id}"
        manifest_path = os.path.join(self.workspace.path, "manifest.yaml")
        manifest = ManifestLoader.load(manifest_path)

        if any(asset.id == item_id for asset in manifest.assets) or any(secret.id == item_id for secret in manifest.secrets):
            raise ValueError(f"Manifest item already exists: {item_id}")
        if profile not in manifest.profiles:
            raise ValueError(f"Profile is not defined: {profile}")

        if is_secret:
            if not recipient:
                raise ValueError("Secret import requires a recipient")
            dest_rel = os.path.join("secrets", f"{item_id}.age")
            dest_abs = os.path.join(self.workspace.path, dest_rel)
            os.makedirs(os.path.dirname(dest_abs), exist_ok=True)
            AgeEncryptor.encrypt_file(abs_source, dest_abs, [recipient])
            manifest.secrets.append(Secret(id=item_id, source=dest_rel, target=target))
            manifest.profiles[profile].secrets.append(item_id)
            item_kind = "secret"
        else:
            dest_rel = os.path.join("assets", item_id)
            dest_abs = os.path.join(self.workspace.path, dest_rel)
            os.makedirs(os.path.dirname(dest_abs), exist_ok=True)
            shutil.copy2(abs_source, dest_abs)
            manifest.assets.append(Asset(id=item_id, type=AssetType.COPY, source=dest_rel, target=target))
            manifest.profiles[profile].assets.append(item_id)
            item_kind = "asset"

        self.save_manifest(manifest_path, manifest)
        self.notify(f"Imported {item_id}")
        self.log_status(f"[bold green]agent[/] Imported {item_kind} [bold]{item_id}[/] into profile [bold]{profile}[/].")
        self.suggest_next_steps("asset")

    def run_asset_export(self, parsed: ParsedCommand) -> None:
        if not parsed.args:
            self.log_status("[yellow]agent[/] Usage: /asset export <id> [output] [--identity path]")
            self.suggest_next_steps("asset")
            return

        item_id = parsed.args[0]
        output_path = parsed.args[1] if len(parsed.args) > 1 else os.path.join(os.getcwd(), item_id)
        identity = self._identity_from(parsed)
        try:
            self.export_manifest_item(item_id, output_path, identity)
        except Exception as e:
            self.notify(f"Export failed: {e}", severity="error")
            self.log_status(f"[bold red]agent[/] Export failed: {e}")
            self.suggest_next_steps("asset")

    def export_manifest_item(self, item_id: str, output_path: str, identity: str | None = None) -> None:
        manifest_path = os.path.join(self.workspace.path, "manifest.yaml")
        manifest = ManifestLoader.load(manifest_path)
        assets = {asset.id: asset for asset in manifest.assets}
        secrets = {secret.id: secret for secret in manifest.secrets}

        output_abs = os.path.abspath(os.path.expanduser(output_path))
        os.makedirs(os.path.dirname(output_abs) or ".", exist_ok=True)

        if item_id in assets:
            asset = assets[item_id]
            source_abs = os.path.join(self.workspace.path, asset.source)
            if not os.path.exists(source_abs):
                raise FileNotFoundError(f"Asset source is missing: {source_abs}")
            if os.path.isdir(source_abs):
                shutil.copytree(source_abs, output_abs, dirs_exist_ok=True)
            else:
                shutil.copy2(source_abs, output_abs)
            self.log_status(f"[bold green]agent[/] Exported asset [bold]{item_id}[/] to {output_abs}.")
        elif item_id in secrets:
            if not identity:
                raise ValueError("Secret export requires --identity path")
            secret = secrets[item_id]
            source_abs = os.path.join(self.workspace.path, secret.source)
            AgeEncryptor.decrypt_file(source_abs, output_abs, identity)
            self.log_status(f"[bold green]agent[/] Decrypted secret [bold]{item_id}[/] to {output_abs}.")
        else:
            raise ValueError(f"Manifest item not found: {item_id}")

        self.notify(f"Exported {item_id}")
        self.suggest_next_steps("asset")

    def save_manifest(self, manifest_path: str, manifest: object) -> None:
        with open(manifest_path, "w", encoding="utf-8") as f:
            data = manifest.model_dump(mode="json", exclude_none=True)
            yaml.dump(data, f, sort_keys=False)

def start_tui() -> None:
    """Entry point for the TUI."""
    app = ReviveApp()
    app.run()
