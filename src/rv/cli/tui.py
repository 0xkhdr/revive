"""Revive TUI — minimal agentic chat interface with autocomplete and contextual suggestions."""

from __future__ import annotations

import os
import shlex
import shutil
from dataclasses import dataclass
from typing import Any, ClassVar

import yaml
from textual import on, work
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, Vertical
from textual.css.query import NoMatches
from textual.message import Message
from textual.reactive import reactive
from textual.widget import Widget
from textual.widgets import (
    DataTable,
    Footer,
    Input,
    Label,
    RichLog,
    Static,
    TabbedContent,
    TabPane,
)

from rv import __version__
from rv.models.manifest import Asset, AssetType, ConflictStrategy, Manifest, Secret
from rv.security.encryptor import AgeEncryptor
from rv.services.doctor import DoctorService
from rv.services.restore import ManifestLoader, ProfileResolver, RestoreService
from rv.services.status import StatusService
from rv.services.workspace import WorkspaceService

# ─── Command Registry ────────────────────────────────────────────────────────

@dataclass(frozen=True)
class AgentCommand:
    path: str
    title: str
    description: str
    usage: str
    requires_workspace: bool = True


COMMANDS: dict[str, AgentCommand] = {
    "/status": AgentCommand(
        path="/status",
        title="Analyze drift",
        description="Compare system state against a profile",
        usage="/status [profile] [--identity PATH]",
    ),
    "/restore": AgentCommand(
        path="/restore",
        title="Restore environment",
        description="Apply profile to system (repo → machine)",
        usage="/restore [profile] [--dry-run] [--identity PATH]",
    ),
    "/diff": AgentCommand(
        path="/diff",
        title="Show drift diff",
        description="Print colored diffs of modified assets",
        usage="/diff [profile] [--identity PATH] [--unified]",
    ),
    "/doctor": AgentCommand(
        path="/doctor",
        title="Run diagnostics",
        description="Check repo health and tool availability",
        usage="/doctor [profile] [--json]",
    ),
    "/asset list": AgentCommand(
        path="/asset list",
        title="List assets",
        description="Show assets and secrets in active workspace",
        usage="/asset list",
    ),
    "/asset import": AgentCommand(
        path="/asset import",
        title="Import file asset",
        description="Copy file into assets/ and register in manifest",
        usage="/asset import PATH [--target PATH] [--profile NAME] [--id NAME]",
    ),
    "/asset import-secret": AgentCommand(
        path="/asset import-secret",
        title="Import encrypted secret",
        description="Encrypt file into secrets/ and register",
        usage="/asset import-secret PATH --recipient AGE_PUBKEY [--target PATH]",
    ),
    "/asset export": AgentCommand(
        path="/asset export",
        title="Export asset",
        description="Copy asset out of workspace",
        usage="/asset export ID [OUTPUT] [--identity PATH]",
    ),
    "/secret keygen": AgentCommand(
        path="/secret keygen",
        title="Generate keypair",
        description="Generate new age identity + recipient key",
        usage="/secret keygen [--output PATH]",
        requires_workspace=False,
    ),
    "/secret encrypt": AgentCommand(
        path="/secret encrypt",
        title="Encrypt file",
        description="Encrypt plaintext using age public keys",
        usage="/secret encrypt FILE --output PATH --recipient AGE_PUBKEY",
        requires_workspace=False,
    ),
    "/secret decrypt": AgentCommand(
        path="/secret decrypt",
        title="Decrypt file",
        description="Decrypt age-encrypted file",
        usage="/secret decrypt FILE --output PATH --identity PATH",
        requires_workspace=False,
    ),
    "/workspace list": AgentCommand(
        path="/workspace list",
        title="List workspaces",
        description="Show registered workspaces",
        usage="/workspace list",
        requires_workspace=False,
    ),
    "/workspace add": AgentCommand(
        path="/workspace add",
        title="Register workspace",
        description="Register a directory as workspace",
        usage="/workspace add PATH [--name NAME]",
        requires_workspace=False,
    ),
    "/workspace use": AgentCommand(
        path="/workspace use",
        title="Switch workspace",
        description="Switch active workspace context",
        usage="/workspace use NAME",
        requires_workspace=False,
    ),
    "/workspace remove": AgentCommand(
        path="/workspace remove",
        title="Remove workspace",
        description="Unregister a workspace by name",
        usage="/workspace remove NAME",
        requires_workspace=False,
    ),
    "/watch": AgentCommand(
        path="/watch",
        title="Watch for changes",
        description="Auto-restore on file changes",
        usage="/watch [profile] [--identity PATH] [--debounce SECS]",
    ),
    "/recover": AgentCommand(
        path="/recover",
        title="Recover transactions",
        description="Rollback or discard incomplete transactions",
        usage="/recover [--auto]",
        requires_workspace=False,
    ),
    "/help": AgentCommand(
        path="/help",
        title="Show commands",
        description="List available slash commands",
        usage="/help [prefix]",
        requires_workspace=False,
    ),
    "/clear": AgentCommand(
        path="/clear",
        title="Clear transcript",
        description="Clear the chat transcript",
        usage="/clear",
        requires_workspace=False,
    ),
}


def autocomplete_commands(prefix: str) -> list[AgentCommand]:
    """Return commands whose path starts with the given prefix."""
    p = prefix.strip().lower()
    if not p:
        return list(COMMANDS.values())
    if not p.startswith("/"):
        p = f"/{p}"
    return [cmd for path, cmd in COMMANDS.items() if path.startswith(p)]


# ─── Command Parser ───────────────────────────────────────────────────────────

@dataclass(frozen=True)
class ParsedCommand:
    path: str
    args: tuple[str, ...]
    flags: dict[str, str | bool]


def parse_command(raw: str) -> ParsedCommand:
    tokens = shlex.split(raw.strip())
    if not tokens:
        raise ValueError("Empty input. Type /help.")
    if not tokens[0].startswith("/"):
        tokens[0] = f"/{tokens[0]}"

    # Check two-token paths first
    path = ""
    tail: list[str] = []
    if len(tokens) > 1 and f"{tokens[0]} {tokens[1]}" in COMMANDS:
        path = f"{tokens[0]} {tokens[1]}"
        tail = tokens[2:]
    elif tokens[0] in COMMANDS:
        path = tokens[0]
        tail = tokens[1:]
    else:
        # Try to give a helpful near-miss
        close = [p for p in COMMANDS if p.startswith(tokens[0])]
        hint = f"  Did you mean: {', '.join(close)}" if close else "  Type /help for all commands."
        raise ValueError(f"Unknown command: {tokens[0]}.{hint}")

    args: list[str] = []
    flags: dict[str, str | bool] = {}
    i = 0
    while i < len(tail):
        tok = tail[i]
        if tok.startswith("--"):
            key = tok[2:].replace("-", "_")
            if i + 1 < len(tail) and not tail[i + 1].startswith("--"):
                flags[key] = tail[i + 1]
                i += 2
            else:
                flags[key] = True
                i += 1
        else:
            args.append(tok)
            i += 1

    return ParsedCommand(path=path, args=tuple(args), flags=flags)


# ─── Suggestion Chips ─────────────────────────────────────────────────────────

CONTEXT_SUGGESTIONS: dict[str, list[str]] = {
    "start":          ["/status base", "/doctor", "/workspace list"],
    "status_clean":   ["/doctor", "/restore base --dry-run", "/diff base"],
    "status_drift":   ["/restore base --dry-run", "/diff base", "/restore base"],
    "restore_dry":    ["/restore base", "/status base"],
    "restore_done":   ["/status base", "/doctor"],
    "diff":           ["/restore base --dry-run", "/restore base"],
    "doctor_ok":      ["/status base", "/workspace list"],
    "doctor_issues":  ["/restore base --dry-run", "/status base", "/recover"],
    "asset":          ["/asset list", "/status base", "/restore base --dry-run"],
    "secret":         ["/secret keygen", "/asset import-secret PATH --recipient age1..."],
    "workspace":      ["/status base", "/asset list", "/doctor"],
    "no_workspace":   ["/workspace list", "/workspace add ."],
    "recover":        ["/status base", "/doctor"],
    "unknown":        ["/help", "/doctor", "/workspace list"],
}


# ─── Autocomplete Dropdown ────────────────────────────────────────────────────

class AutocompleteList(Widget):
    """Floating suggestion dropdown rendered below the input."""

    DEFAULT_CSS = """
    AutocompleteList {
        layer: overlay;
        dock: bottom;
        height: auto;
        max-height: 8;
        width: 100%;
        background: #1e1e2e;
        border-top: tall #cba6f7;
        display: none;
    }
    AutocompleteList.visible {
        display: block;
    }
    .ac-item {
        height: 1;
        padding: 0 2;
        color: #a6adc8;
    }
    .ac-item.highlighted {
        background: #cba6f7;
        color: #11111b;
    }
    """

    items: reactive[list[AgentCommand]] = reactive([], recompose=True)
    cursor: reactive[int] = reactive(0)

    class Selected(Message):
        def __init__(self, command: AgentCommand) -> None:
            super().__init__()
            self.command = command

    def compose(self) -> ComposeResult:
        for i, cmd in enumerate(self.items):
            classes = "ac-item highlighted" if i == self.cursor else "ac-item"
            yield Static(
                f"[bold]{cmd.path}[/]  [dim]{cmd.description}[/]",
                classes=classes,
                id=f"ac_{i}",
            )

    def watch_cursor(self, old: int, new: int) -> None:
        for i in range(len(self.items)):
            try:
                widget = self.query_one(f"#ac_{i}", Static)
                if i == new:
                    widget.add_class("highlighted")
                    widget.remove_class("ac-item")
                else:
                    widget.remove_class("highlighted")
                    widget.add_class("ac-item")
            except NoMatches:
                pass

    def move_up(self) -> None:
        if self.items:
            self.cursor = (self.cursor - 1) % len(self.items)

    def move_down(self) -> None:
        if self.items:
            self.cursor = (self.cursor + 1) % len(self.items)

    def accept(self) -> None:
        if self.items and 0 <= self.cursor < len(self.items):
            self.post_message(self.Selected(self.items[self.cursor]))

    def update_items(self, prefix: str) -> None:
        matches = autocomplete_commands(prefix)[:8]
        self.items = matches
        self.cursor = 0
        if matches:
            self.add_class("visible")
        else:
            self.remove_class("visible")

    def hide(self) -> None:
        self.items = []
        self.remove_class("visible")


# ─── Sidebar Cards ────────────────────────────────────────────────────────────

class WorkspaceDetailsCard(Static):
    """Workspace details status card."""
    DEFAULT_CSS = """
    WorkspaceDetailsCard {
        background: #181825;
        border: round #a6e3a1;
        padding: 1 2;
        margin-bottom: 1;
        height: auto;
    }
    """
    def render(self) -> str:
        app = self.app
        assert isinstance(app, ReviveApp)  # noqa: S101
        ws = app.workspace
        if not ws:
            return (
                "[bold #f38ba8]No Workspace Active[/]\n"
                "[dim]Run [bold cyan]/workspace add .[/] to register[/]"
            )
        return (
            f"[bold #a6e3a1]● Connected[/]\n"
            f"[bold #cdd6f4]{ws.name}[/]\n"
            f"[dim #a6adc8]{ws.path}[/]"
        )


class ToolsCapabilityCard(Static):
    """System capability tools status checker."""
    DEFAULT_CSS = """
    ToolsCapabilityCard {
        background: #181825;
        border: round #89b4fa;
        padding: 1 2;
        margin-bottom: 1;
        height: auto;
    }
    """
    def render(self) -> str:
        from rv.utils.platform import Platform
        tools = ["age", "brew", "docker", "git", "node", "apt"]
        lines = ["[bold #89b4fa]System Tools[/]"]
        for tool in tools:
            available = Platform.has_tool(tool)
            icon = "[#a6e3a1]●[/]" if available else "[#f38ba8]○[/]"
            lines.append(f" {icon} {tool}")
        return "\n".join(lines)


class ActiveProfileCard(Static):
    """Active deployment profile statistics."""
    DEFAULT_CSS = """
    ActiveProfileCard {
        background: #181825;
        border: round #cba6f7;
        padding: 1 2;
        margin-bottom: 1;
        height: auto;
    }
    """
    profile_name: reactive[str] = reactive("base")
    assets_count: reactive[int] = reactive(0)
    secrets_count: reactive[int] = reactive(0)
    packages_count: reactive[int] = reactive(0)

    def render(self) -> str:
        app = self.app
        assert isinstance(app, ReviveApp)  # noqa: S101
        if not app.workspace:
            return "[bold #f9e2af]No Profile Loaded[/]"
        return (
            f"[bold #cba6f7]Profile: {self.profile_name}[/]\n"
            f" ✦ Assets:   [bold #cdd6f4]{self.assets_count}[/]\n"
            f" ✦ Secrets:  [bold #cdd6f4]{self.secrets_count}[/]\n"
            f" ✦ Packages: [bold #cdd6f4]{self.packages_count}[/]"
        )

    def update_stats(self, profile: str) -> None:
        app = self.app
        assert isinstance(app, ReviveApp)  # noqa: S101
        self.profile_name = profile
        if not app.workspace:
            return
        try:
            manifest_path = os.path.join(app.workspace.path, "manifest.yaml")
            if os.path.exists(manifest_path):
                manifest = ManifestLoader.load(manifest_path)
                resolved = ProfileResolver.resolve(manifest, profile)
                self.assets_count = len(resolved.assets)
                self.secrets_count = len(resolved.secrets)
                self.packages_count = sum(len(pkgs) for pkgs in resolved.packages.values() if isinstance(pkgs, list))
        except Exception:
            pass


class WorkspaceSwitcherCard(Static):
    """Workspace switcher list panel."""
    DEFAULT_CSS = """
    WorkspaceSwitcherCard {
        background: #181825;
        border: round #f9e2af;
        padding: 1 2;
        margin-bottom: 1;
        height: auto;
    }
    """
    def render(self) -> str:
        app = self.app
        assert isinstance(app, ReviveApp)  # noqa: S101
        workspaces = WorkspaceService.list_workspaces()
        lines = ["[bold #f9e2af]Workspaces[/]"]
        if not workspaces:
            lines.append(" [dim]none registered[/]")
            return "\n".join(lines)
        for ws in workspaces:
            active = " [bold #a6e3a1]←[/]" if app.workspace and ws.path == app.workspace.path else ""
            lines.append(f" [@click=app.switch_ws('{ws.name}')]• {ws.name}[/]{active}")
        return "\n".join(lines)


# ─── Suggestion Chips Bar ─────────────────────────────────────────────────────

class SuggestionBar(Static):
    """Row of clickable next-step command chips."""

    DEFAULT_CSS = """
    SuggestionBar {
        height: 3;
        background: #1e1e2e;
        border-top: solid #313244;
        padding: 1 2;
        align: left middle;
    }
    """

    class ChipPressed(Message):
        def __init__(self, command: str) -> None:
            super().__init__()
            self.command = command

    suggestions: reactive[list[str]] = reactive([])

    def watch_suggestions(self, old: list[str], new: list[str]) -> None:
        self.update_content()

    def set_context(self, context: str) -> None:
        self.suggestions = CONTEXT_SUGGESTIONS.get(context, CONTEXT_SUGGESTIONS["start"])

    def update_content(self) -> None:
        if not self.suggestions:
            self.update("[dim]type /help or press / to begin[/]")
            return
        parts = ["[dim]next →[/] "]
        for s in self.suggestions:
            parts.append(f"[@click=app.select_suggestion('{s}')][#89b4fa]⬡ {s}[/][/]")
        self.update("  ".join(parts))


# ─── Header Bar ───────────────────────────────────────────────────────────────

class HeaderBar(Static):
    """Single-line status strip at top."""

    DEFAULT_CSS = """
    HeaderBar {
        height: 3;
        background: #1e1e2e;
        color: #cdd6f4;
        border-bottom: double #cba6f7;
        align: center middle;
        content-align: center middle;
        text-style: bold;
    }
    """
    def render(self) -> str:
        return f"⚡ [bold #cba6f7]REVIVE[/] [bold #89b4fa]System Sync Agent[/] [dim]•[/] [#a6adc8]v{__version__}[/]"


# ─── Main App ─────────────────────────────────────────────────────────────────

class ReviveApp(App[None]):
    """Revive TUI — agentic chat interface."""

    TITLE = "rv"
    CSS = """
    Screen {
        background: #11111b;
        color: #cdd6f4;
        layers: base overlay;
    }

    #main-container {
        layout: horizontal;
        height: 1fr;
    }

    #sidebar {
        width: 35;
        height: 100%;
        background: #1e1e2e;
        border-right: solid #313244;
        padding: 1 1;
    }

    #sidebar-logo {
        height: 3;
        content-align: center middle;
        text-style: bold;
        border-bottom: solid #313244;
        margin-bottom: 1;
    }

    #right-pane {
        width: 1fr;
        height: 100%;
        background: #11111b;
    }

    TabbedContent {
        height: 1fr;
        background: #11111b;
    }

    TabPane {
        padding: 1 1;
        background: #11111b;
    }

    RichLog {
        background: #11111b;
        border: none;
        scrollbar-size: 1 1;
    }

    #doctor-log {
        background: #11111b;
        border: none;
        scrollbar-size: 1 1;
    }

    DataTable {
        background: #181825;
        border: solid #313244;
        height: 1fr;
        scrollbar-size: 1 1;
    }

    #drift-details {
        height: 4;
        background: #181825;
        border: round #313244;
        margin-top: 1;
        padding: 0 1;
    }

    #input-row {
        height: 3;
        padding: 0 2;
        align: left middle;
        background: #1e1e2e;
        border-top: solid #313244;
    }

    #prompt-label {
        width: 3;
        color: #cba6f7;
        text-style: bold;
    }

    #cmd-input {
        width: 1fr;
        background: #181825;
        color: #cdd6f4;
        border: round #313244;
    }

    #cmd-input:focus {
        border: round #cba6f7;
    }

    #autocomplete {
        layer: overlay;
        dock: bottom;
        offset-y: -4;
        width: 100%;
    }
    """

    BINDINGS: ClassVar[list[Binding | tuple[str, str] | tuple[str, str, str]]] = [
        Binding("ctrl+c", "quit", "Quit"),
        Binding("ctrl+l", "clear_transcript", "Clear"),
        Binding("escape", "hide_autocomplete", "Close"),
        Binding("up", "ac_up", show=False),
        Binding("down", "ac_down", show=False),
        Binding("tab", "ac_accept", "Complete"),
        Binding("ctrl+p", "show_palette", "Palette"),
    ]

    def __init__(self) -> None:
        super().__init__()
        self.workspace = WorkspaceService.get_current_workspace()
        self._ac_active = False

    # ── Layout ──────────────────────────────────────────────────────────────

    def compose(self) -> ComposeResult:
        yield HeaderBar(id="header")
        with Horizontal(id="main-container"):
            # Left Sidebar
            with Vertical(id="sidebar"):
                yield WorkspaceDetailsCard(id="ws-details-card")
                yield ToolsCapabilityCard(id="tools-capability-card")
                yield ActiveProfileCard(id="profile-card")
                yield WorkspaceSwitcherCard(id="ws-switcher-card")
            # Right Area
            with Vertical(id="right-pane"):
                with TabbedContent(initial="tab-console", id="tabs"):
                    with TabPane("Console Chat", id="tab-console"):
                        yield RichLog(id="transcript", highlight=True, markup=True, wrap=True)
                    with TabPane("Drift Explorer", id="tab-drift"):
                        yield DataTable(id="drift-table")
                        yield Static("[dim]Select an asset row above to inspect details.[/]", id="drift-details")
                    with TabPane("System Diagnostics", id="tab-doctor"):
                        yield RichLog(id="doctor-log", highlight=True, markup=True, wrap=True)
                yield SuggestionBar(id="suggestions")
                with Horizontal(id="input-row"):
                    yield Label("❯ ", id="prompt-label")
                    yield Input(
                        placeholder="/help  or  /status base",
                        id="cmd-input",
                    )
                yield AutocompleteList(id="autocomplete")
        yield Footer()

    def on_mount(self) -> None:
        self._log_agent(
            "[bold]Revive agent online.[/]\n"
            "Type [bold cyan]/help[/] for commands, [bold cyan]/[/] + Tab to autocomplete.\n"
            f"Workspace: [bold]{self.workspace.name if self.workspace else 'none — run /workspace add .'}[/]"
        )
        self._set_context("start")
        self.query_one("#cmd-input").focus()
        self._refresh_all()

    # ── Helpers ──────────────────────────────────────────────────────────────

    def _refresh_all(self) -> None:
        """Reactive refresh of all sidebar card widgets."""
        self.query_one("#ws-details-card", WorkspaceDetailsCard).refresh()
        self.query_one("#ws-switcher-card", WorkspaceSwitcherCard).refresh()
        
        profile = "base"
        self.query_one("#profile-card", ActiveProfileCard).update_stats(profile)
        self._run_status_in_background(profile)

    @work
    async def _run_status_in_background(self, profile: str) -> None:
        """Quietly checks sync state in background to update the table."""
        if not self.workspace:
            return
        try:
            report = StatusService.get_status(self.workspace.path, profile)
            self.call_from_thread(self._populate_table, report)
        except Exception:
            pass

    def _populate_table(self, report: dict[str, Any]) -> None:
        """Populates Tab 2 DataTable with live drift status."""
        table = self.query_one("#drift-table", DataTable)
        table.clear(columns=True)
        table.cursor_type = "row"
        table.add_columns("Asset / Secret", "Type", "Target Path", "Status")
        
        for item_id, info in report.get("assets", {}).items():
            status = info["status"]
            if status == "in_sync":
                status_str = "[bold #a6e3a1]✓ In Sync[/]"
            elif status == "modified":
                status_str = "[bold #f38ba8]✗ Drifted[/]"
            elif status == "missing":
                status_str = "[bold #f9e2af]⚠ Missing[/]"
            else:
                status_str = f"[bold #f38ba8]⚠ {status}[/]"
            table.add_row(item_id, str(info["type"]), info["target"], status_str)

    @on(DataTable.RowSelected, "#drift-table")
    def on_row_selected(self, event: DataTable.RowSelected) -> None:
        """Update selected details under Table tab when navigating rows."""
        table = self.query_one("#drift-table", DataTable)
        try:
            row = table.get_row(event.row_key)
            item_id = row[0]
            details = self.query_one("#drift-details", Static)
            details.update(
                f"[bold #89b4fa]Selected ID:[/] [bold]{item_id}[/]\n"
                f"[dim #a6adc8]Type: {row[1]}  |  Target: {row[2]}[/]\n"
                f"Status: {row[3]}  |  Run [/][bold cyan]/diff[/][dim] or [/][bold cyan]/restore[/][dim] to apply.[/]"
            )
        except Exception:
            pass

    def action_switch_ws(self, name: str) -> None:
        """Action handler when clicking workspace links in sidebar switcher."""
        self._log_sep()
        self._log_user(f"use workspace {name}")
        self._run_workspace_use(name)

    def action_select_suggestion(self, command: str) -> None:
        """Action handler when clicking suggestions pills."""
        inp = self.query_one("#cmd-input", Input)
        inp.value = command
        inp.cursor_position = len(inp.value)
        inp.focus()

    def _log_user(self, text: str) -> None:
        self.query_one("#transcript", RichLog).write(f"[bold cyan]you[/]  {text}")

    def _log_agent(self, text: str) -> None:
        self.query_one("#transcript", RichLog).write(f"[bold green]rv[/]   {text}")

    def _log_err(self, text: str) -> None:
        self.query_one("#transcript", RichLog).write(f"[bold red]err[/]  {text}")

    def _log_sep(self) -> None:
        self.query_one("#transcript", RichLog).write("[dim]─[/]" * 40)

    def _set_context(self, ctx: str) -> None:
        self.query_one("#suggestions", SuggestionBar).set_context(ctx)

    def _profile_from(self, parsed: ParsedCommand) -> str:
        p = parsed.flags.get("profile")
        return p if isinstance(p, str) else (parsed.args[0] if parsed.args else "base")

    def _identity_from(self, parsed: ParsedCommand) -> str | None:
        v = parsed.flags.get("identity")
        return v if isinstance(v, str) else None

    # ── Input / Autocomplete ─────────────────────────────────────────────────

    @on(Input.Changed, "#cmd-input")
    def on_input_changed(self, event: Input.Changed) -> None:
        val = event.value
        ac = self.query_one("#autocomplete", AutocompleteList)
        if val.startswith("/") and len(val) >= 1:
            ac.update_items(val)
            self._ac_active = bool(ac.items)
        else:
            ac.hide()
            self._ac_active = False

    @on(Input.Submitted, "#cmd-input")
    async def on_submitted(self, event: Input.Submitted) -> None:
        ac = self.query_one("#autocomplete", AutocompleteList)
        # If autocomplete visible and user hits enter, accept suggestion
        if self._ac_active and ac.items:
            ac.accept()
            return
        cmd = event.value.strip()
        if not cmd:
            return
        event.input.value = ""
        ac.hide()
        self._ac_active = False
        self._log_sep()
        self._log_user(cmd)
        await self._dispatch(cmd)

    @on(AutocompleteList.Selected)
    def on_ac_selected(self, event: AutocompleteList.Selected) -> None:
        inp = self.query_one("#cmd-input", Input)
        inp.value = event.command.path + " "
        inp.cursor_position = len(inp.value)
        self.query_one("#autocomplete", AutocompleteList).hide()
        self._ac_active = False
        inp.focus()

    def action_ac_up(self) -> None:
        if self._ac_active:
            self.query_one("#autocomplete", AutocompleteList).move_up()

    def action_ac_down(self) -> None:
        if self._ac_active:
            self.query_one("#autocomplete", AutocompleteList).move_down()

    def action_ac_accept(self) -> None:
        if self._ac_active:
            self.query_one("#autocomplete", AutocompleteList).accept()
        else:
            # Tab when not in AC — show all completions
            val = self.query_one("#cmd-input", Input).value
            ac = self.query_one("#autocomplete", AutocompleteList)
            ac.update_items(val or "/")
            self._ac_active = bool(ac.items)

    def action_hide_autocomplete(self) -> None:
        self.query_one("#autocomplete", AutocompleteList).hide()
        self._ac_active = False

    def action_clear_transcript(self) -> None:
        self.query_one("#transcript", RichLog).clear()
        self._set_context("start")

    def action_show_palette(self) -> None:
        # Ctrl+P: show full command list inline
        self.query_one("#cmd-input", Input).value = "/"
        ac = self.query_one("#autocomplete", AutocompleteList)
        ac.update_items("/")
        self._ac_active = True
        self.query_one("#cmd-input").focus()

    # ── Dispatch ─────────────────────────────────────────────────────────────

    async def _dispatch(self, raw: str) -> None:
        try:
            parsed = parse_command(raw)
        except ValueError as e:
            self._log_err(str(e))
            self._set_context("unknown")
            return

        cmd = COMMANDS[parsed.path]
        if cmd.requires_workspace and not self.workspace:
            self._log_err("No workspace active. Run: /workspace add .")
            self._set_context("no_workspace")
            return

        p = parsed.path
        if p == "/status":
            self._run_status(self._profile_from(parsed), self._identity_from(parsed))
        elif p == "/restore":
            dry = bool(parsed.flags.get("dry_run"))
            self._run_restore(self._profile_from(parsed), self._identity_from(parsed), dry)
        elif p == "/diff":
            unified = bool(parsed.flags.get("unified"))
            self._run_diff(self._profile_from(parsed), self._identity_from(parsed), unified)
        elif p == "/doctor":
            self._run_doctor(self._profile_from(parsed) if parsed.args else None)
        elif p == "/asset list":
            self._run_asset_list()
        elif p == "/asset import":
            self._run_asset_import(parsed, is_secret=False)
        elif p == "/asset import-secret":
            self._run_asset_import(parsed, is_secret=True)
        elif p == "/asset export":
            self._run_asset_export(parsed)
        elif p == "/secret keygen":
            self._run_keygen(parsed)
        elif p == "/secret encrypt":
            self._run_secret_encrypt(parsed)
        elif p == "/secret decrypt":
            self._run_secret_decrypt(parsed)
        elif p == "/workspace list":
            self._run_workspace_list()
        elif p == "/workspace add":
            path = parsed.args[0] if parsed.args else os.getcwd()
            name = parsed.flags.get("name")
            self._run_workspace_add(path, name if isinstance(name, str) else None)
        elif p == "/workspace use":
            self._run_workspace_use(parsed.args[0] if parsed.args else "")
        elif p == "/workspace remove":
            self._run_workspace_remove(parsed.args[0] if parsed.args else "")
        elif p == "/watch":
            self._log_agent("Watch runs in foreground — use [bold]rv watch --profile base[/] in terminal.")
            self._set_context("start")
        elif p == "/recover":
            self._run_recover(bool(parsed.flags.get("auto")))
        elif p == "/help":
            self._run_help(parsed.args[0] if parsed.args else "")
        elif p == "/clear":
            self.action_clear_transcript()

    # ── Command Implementations ───────────────────────────────────────────────

    @work
    async def _run_status(self, profile: str, identity: str | None) -> None:
        self._log_agent(f"Checking drift — profile [bold]{profile}[/] …")
        if not self.workspace:
            return
        try:
            report = StatusService.get_status(self.workspace.path, profile, identity)
            total = len(report["assets"])
            drifted_items = [aid for aid, info in report["assets"].items() if info["status"] != "in_sync"]
            
            # Populate our visual DataTable
            self._populate_table(report)
            
            if drifted_items:
                self._log_agent(f"[yellow]Drift detected[/] — {len(drifted_items)}/{total} assets out of sync:")
                for aid in drifted_items:
                    info = report["assets"][aid]
                    self._log_agent(f"  [red]✗[/] {aid}  [dim]{info['status']}[/]  {info.get('details','')}")
                self._set_context("status_drift")
            else:
                self._log_agent(f"[green]✓ In sync[/] — all {total} assets match profile [bold]{profile}[/].")
                self._set_context("status_clean")
        except Exception as e:
            self._log_err(f"Status failed: {e}")
            self._set_context("unknown")

    @work
    async def _run_restore(self, profile: str, identity: str | None, dry_run: bool) -> None:
        mode = "[yellow]dry-run[/]" if dry_run else "[green]applying[/]"
        self._log_agent(f"Restore {mode} — profile [bold]{profile}[/] …")
        if not self.workspace:
            return
        try:
            RestoreService.restore(
                repo_dir=self.workspace.path,
                profile_name=profile,
                identity_path=identity,
                dry_run=dry_run,
                interactive=False,
            )
            # Re-read drift status to update cards and table
            self._refresh_all()
            
            if dry_run:
                self._log_agent("Dry-run complete — no files changed. Review output, then run without --dry-run.")
                self._set_context("restore_dry")
            else:
                self._log_agent(f"[green]✓ Restored[/] — system now matches profile [bold]{profile}[/].")
                self._set_context("restore_done")
        except Exception as e:
            self._log_err(f"Restore failed: {e}")
            self._set_context("doctor_issues")

    @work
    async def _run_diff(self, profile: str, identity: str | None, unified: bool) -> None:
        self._log_agent(f"Computing diff — profile [bold]{profile}[/] …")
        if not self.workspace:
            return
        try:
            report = StatusService.get_status(self.workspace.path, profile, identity)
            modified = [aid for aid, info in report["assets"].items() if info["status"] == "modified"]
            if not modified:
                self._log_agent("[green]No content modifications detected.[/]")
                self._set_context("status_clean")
                return
            for asset_id in modified:
                contents = StatusService.get_contents_for_diff(self.workspace.path, profile, asset_id, identity)
                if not contents:
                    continue
                expected, actual = contents
                import difflib
                if unified:
                    diff_lines = list(difflib.unified_diff(
                        expected.splitlines(), actual.splitlines(),
                        fromfile=f"repo/{asset_id}", tofile=f"system/{asset_id}", lineterm=""
                    ))
                    self._log_agent(f"[bold yellow]── diff: {asset_id} ──[/]")
                    for line in diff_lines[:80]:
                        color = "green" if line.startswith("+") else ("red" if line.startswith("-") else "dim")
                        self._log_agent(f"[{color}]{line}[/]")
                else:
                    self._log_agent(f"[bold yellow]── modified: {asset_id} ──[/]  (use --unified for line diff)")
            self._set_context("diff")
        except Exception as e:
            self._log_err(f"Diff failed: {e}")
            self._set_context("unknown")

    @work
    async def _run_doctor(self, profile: str | None) -> None:
        self._log_agent("Running diagnostics …")
        try:
            repo = self.workspace.path if self.workspace else os.getcwd()
            report = DoctorService.check_health(repo, profile)
            health = "[green]HEALTHY[/]" if report["healthy"] else "[red]ISSUES FOUND[/]"
            self._log_agent(f"System health: {health}  ({report['checks_run']} checks)")
            for tool, ok in report.get("tools", {}).items():
                icon = "[green]✓[/]" if ok else "[yellow]✗[/]"
                self._log_agent(f"  {icon} {tool}")
            for issue in report.get("issues", []):
                sev = "[red][crit][/]" if issue.get("severity") == "critical" else "[yellow][warn][/]"
                self._log_agent(f"  {sev} {issue['category']}: {issue['message']}")
            
            # Format in detailed Tab 3 diagnostics log
            doc_log = self.query_one("#doctor-log", RichLog)
            doc_log.clear()
            doc_log.write("[bold #cba6f7]System Diagnostics Report[/]\n")
            doc_log.write(f"Workspace Status: {health}\n")
            doc_log.write(f"Audited: {report['checks_run']} check points\n")
            doc_log.write("[dim]─[/]" * 40 + "\n")
            
            doc_log.write("[bold #89b4fa]Tools Audit:[/]")
            for tool, ok in report.get("tools", {}).items():
                icon = "[#a6e3a1]✓[/]" if ok else "[#f38ba8]✗[/]"
                doc_log.write(f"  {icon} {tool}")
                
            doc_log.write("\n[bold #f9e2af]Discovered Issues:[/]")
            if not report.get("issues"):
                doc_log.write("  [#a6e3a1]No configuration or security warnings registered![/]")
            else:
                for issue in report.get("issues", []):
                    sev = "[bold #f38ba8][CRIT][/]" if issue.get("severity") == "critical" else "[bold #f9e2af][WARN][/]"
                    doc_log.write(f"  {sev} {issue['category']}: {issue['message']}")
            
            self._set_context("doctor_ok" if report["healthy"] else "doctor_issues")
        except Exception as e:
            self._log_err(f"Doctor failed: {e}")
            self._set_context("unknown")

    def _run_asset_list(self) -> None:
        if not self.workspace:
            self._set_context("no_workspace")
            return
        manifest_path = os.path.join(self.workspace.path, "manifest.yaml")
        try:
            manifest = ManifestLoader.load(manifest_path)
        except Exception as e:
            self._log_err(f"Could not load manifest: {e}")
            self._set_context("doctor_issues")
            return
        if not manifest.assets and not manifest.secrets:
            self._log_agent("Manifest is empty — no assets or secrets yet.")
            self._set_context("asset")
            return
        self._log_agent(f"Manifest inventory ({len(manifest.assets)} assets, {len(manifest.secrets)} secrets):")
        for a in manifest.assets:
            self._log_agent(f"  [cyan]{a.id}[/]  [dim]{a.type.value}[/]  {a.source} → {a.target}")
        for s in manifest.secrets:
            self._log_agent(f"  [magenta]{s.id}[/]  [dim]secret[/]  {s.source} → {s.target}")
        self._set_context("asset")

    @work
    async def _run_asset_import(self, parsed: ParsedCommand, is_secret: bool) -> None:
        path = parsed.args[0] if parsed.args else None
        if not path:
            self._log_err(f"Usage: {COMMANDS[parsed.path].usage}")
            self._set_context("asset")
            return
        recipient = parsed.flags.get("recipient")
        if is_secret and not isinstance(recipient, str):
            recipient = os.environ.get("REVIVE_PUBKEY")
        if is_secret and not recipient:
            self._log_err("Secret import needs --recipient age1...  or REVIVE_PUBKEY env var.")
            self._set_context("secret")
            return
        asset_id = parsed.flags.get("id")
        target = parsed.flags.get("target")
        profile = parsed.flags.get("profile")
        try:
            self._import_item(
                source_path=path,
                is_secret=is_secret,
                asset_id=asset_id if isinstance(asset_id, str) else None,
                target_path=target if isinstance(target, str) else None,
                profile=profile if isinstance(profile, str) else "base",
                recipient=recipient if isinstance(recipient, str) else None,
            )
        except Exception as e:
            self._log_err(f"Import failed: {e}")
            self._set_context("asset")

    def _import_item(
        self,
        source_path: str,
        is_secret: bool,
        asset_id: str | None,
        target_path: str | None,
        profile: str,
        recipient: str | None,
    ) -> None:
        if not self.workspace:
            return
        abs_src = os.path.abspath(os.path.expanduser(source_path))
        if not os.path.isfile(abs_src):
            raise ValueError(f"Not a file: {abs_src}")
        item_id = asset_id or os.path.basename(abs_src)
        target = target_path or f"~/.config/revive_imported/{item_id}"
        manifest_path = os.path.join(self.workspace.path, "manifest.yaml")
        manifest = ManifestLoader.load(manifest_path)
        if any(a.id == item_id for a in manifest.assets) or any(s.id == item_id for s in manifest.secrets):
            raise ValueError(f"ID already in manifest: {item_id}")
        if profile not in manifest.profiles:
            raise ValueError(f"Profile not defined: {profile}")
        if is_secret:
            assert recipient is not None  # noqa: S101
            dest_rel = os.path.join("secrets", f"{item_id}.age")
            dest_abs = os.path.join(self.workspace.path, dest_rel)
            os.makedirs(os.path.dirname(dest_abs), exist_ok=True)
            AgeEncryptor.encrypt_file(abs_src, dest_abs, [recipient])
            manifest.secrets.append(
                Secret(
                    id=item_id,
                    type=AssetType.SECRET,
                    source=dest_rel,
                    target=target,
                    permissions="0600",
                    owner=None,
                    encrypted=True,
                )
            )
            manifest.profiles[profile].secrets.append(item_id)
            kind = "secret"
        else:
            dest_rel = os.path.join("assets", item_id)
            dest_abs = os.path.join(self.workspace.path, dest_rel)
            os.makedirs(os.path.dirname(dest_abs), exist_ok=True)
            shutil.copy2(abs_src, dest_abs)
            manifest.assets.append(
                Asset(
                    id=item_id,
                    type=AssetType.COPY,
                    source=dest_rel,
                    target=target,
                    permissions=None,
                    owner=None,
                    conflict_strategy=ConflictStrategy.PROMPT,
                    encrypted=False,
                    template_vars=None,
                )
            )
            manifest.profiles[profile].assets.append(item_id)
            kind = "asset"
        self._save_manifest(manifest_path, manifest)
        self._log_agent(f"[green]✓ Imported[/] {kind} [bold]{item_id}[/] into profile [bold]{profile}[/].")
        self._set_context("asset")
        self._refresh_all()

    @work
    async def _run_asset_export(self, parsed: ParsedCommand) -> None:
        if not parsed.args:
            self._log_err(f"Usage: {COMMANDS['/asset export'].usage}")
            self._set_context("asset")
            return
        if not self.workspace:
            return
        item_id = parsed.args[0]
        output = parsed.args[1] if len(parsed.args) > 1 else os.path.join(os.getcwd(), item_id)
        identity = self._identity_from(parsed)
        try:
            manifest_path = os.path.join(self.workspace.path, "manifest.yaml")
            manifest = ManifestLoader.load(manifest_path)
            assets = {a.id: a for a in manifest.assets}
            secrets = {s.id: s for s in manifest.secrets}
            out_abs = os.path.abspath(os.path.expanduser(output))
            os.makedirs(os.path.dirname(out_abs) or ".", exist_ok=True)
            if item_id in assets:
                a = assets[item_id]
                src = os.path.join(self.workspace.path, a.source)
                if os.path.isdir(src):
                    shutil.copytree(src, out_abs, dirs_exist_ok=True)
                else:
                    shutil.copy2(src, out_abs)
                self._log_agent(f"[green]✓ Exported[/] asset [bold]{item_id}[/] → {out_abs}")
            elif item_id in secrets:
                if not identity:
                    raise ValueError("Secret export needs --identity PATH")
                s = secrets[item_id]
                src = os.path.join(self.workspace.path, s.source)
                AgeEncryptor.decrypt_file(src, out_abs, identity)
                self._log_agent(f"[green]✓ Decrypted[/] secret [bold]{item_id}[/] → {out_abs}")
            else:
                raise ValueError(f"ID not found in manifest: {item_id}")
            self._set_context("asset")
        except Exception as e:
            self._log_err(f"Export failed: {e}")
            self._set_context("asset")

    @work
    async def _run_keygen(self, parsed: ParsedCommand) -> None:
        self._log_agent("Generating age keypair …")
        try:
            pub, priv = AgeEncryptor.generate_keypair()
            output = parsed.flags.get("output")
            if isinstance(output, str):
                out = os.path.abspath(os.path.expanduser(output))
                os.makedirs(os.path.dirname(out) or ".", exist_ok=True)
                with open(out, "w", encoding="utf-8") as f:
                    f.write(f"# public key: {pub}\n{priv}\n")
                os.chmod(out, 0o600)
                self._log_agent(f"[green]✓ Keypair saved[/] → {out}")
                self._log_agent(f"Public key: [yellow]{pub}[/]")
            else:
                self._log_agent(f"Public key:  [yellow]{pub}[/]")
                self._log_agent(f"Private key: [cyan]{priv}[/]")
                self._log_agent("[dim](store private key safely — not logged to disk)[/]")
            self._set_context("secret")
        except Exception as e:
            self._log_err(f"Keygen failed: {e}")
            self._set_context("unknown")

    @work
    async def _run_secret_encrypt(self, parsed: ParsedCommand) -> None:
        if not parsed.args:
            self._log_err(f"Usage: {COMMANDS['/secret encrypt'].usage}")
            return
        file_path = parsed.args[0]
        output = parsed.flags.get("output")
        recipients = []
        for k, v in parsed.flags.items():
            if k == "recipient" and isinstance(v, str):
                recipients.append(v)
        if not isinstance(output, str) or not recipients:
            self._log_err("Need --output PATH and --recipient AGE_PUBKEY")
            return
        try:
            AgeEncryptor.encrypt_file(file_path, output, recipients)
            self._log_agent(f"[green]✓ Encrypted[/] → {output}")
            self._set_context("secret")
        except Exception as e:
            self._log_err(f"Encrypt failed: {e}")

    @work
    async def _run_secret_decrypt(self, parsed: ParsedCommand) -> None:
        if not parsed.args:
            self._log_err(f"Usage: {COMMANDS['/secret decrypt'].usage}")
            return
        file_path = parsed.args[0]
        output = parsed.flags.get("output")
        identity = parsed.flags.get("identity")
        if not isinstance(output, str) or not isinstance(identity, str):
            self._log_err("Need --output PATH and --identity PATH")
            return
        try:
            AgeEncryptor.decrypt_file(file_path, output, identity)
            self._log_agent(f"[green]✓ Decrypted[/] → {output}")
            self._set_context("secret")
        except Exception as e:
            self._log_err(f"Decrypt failed: {e}")

    def _run_workspace_list(self) -> None:
        workspaces = WorkspaceService.list_workspaces()
        if not workspaces:
            self._log_agent("No workspaces registered. Run: /workspace add .")
            self._set_context("no_workspace")
            return
        self._log_agent(f"Registered workspaces ({len(workspaces)}):")
        for ws in workspaces:
            active = "  [bold green]← active[/]" if self.workspace and ws.path == self.workspace.path else ""
            self._log_agent(f"  [bold]{ws.name}[/]  [dim]{ws.path}[/]{active}")
        self._set_context("workspace")

    def _run_workspace_add(self, path: str, name: str | None) -> None:
        abs_path = os.path.abspath(os.path.expanduser(path))
        ws = WorkspaceService.register_workspace(abs_path, name)
        self.workspace = ws
        self._refresh_all()
        self._log_agent(f"[green]✓ Registered[/] workspace [bold]{ws.name}[/]  ({ws.path})")
        self._set_context("workspace")

    def _run_workspace_use(self, name: str) -> None:
        if not name:
            self._log_err("Usage: /workspace use NAME")
            return
        for ws in WorkspaceService.list_workspaces():
            if ws.name == name:
                self.workspace = WorkspaceService.register_workspace(ws.path)
                self._refresh_all()
                self._log_agent(f"[green]✓ Switched[/] to workspace [bold]{ws.name}[/].")
                self._set_context("workspace")
                return
        self._log_err(f"Workspace not found: {name}  (run /workspace list)")
        self._set_context("workspace")

    def _run_workspace_remove(self, name: str) -> None:
        if not name:
            self._log_err("Usage: /workspace remove NAME")
            return
        if WorkspaceService.remove_workspace(name):
            if self.workspace and self.workspace.name == name:
                self.workspace = WorkspaceService.get_current_workspace()
                self._refresh_all()
            self._log_agent(f"[yellow]Removed[/] workspace [bold]{name}[/].")
        else:
            self._log_err(f"Workspace not found: {name}")
        self._set_context("workspace")

    def _run_recover(self, auto: bool) -> None:
        from rv.services.recovery import RecoveryService
        try:
            journals = RecoveryService.list_incomplete_journals()
            if not journals:
                self._log_agent("[green]No incomplete transactions found.[/]")
                self._set_context("start")
                return
            self._log_agent(f"Found {len(journals)} incomplete transaction(s):")
            for j in journals:
                self._log_agent(f"  [bold]{j.tx_id}[/]  {j.timestamp}  [{j.status}]")
            if auto:
                latest = journals[0]
                RecoveryService.rollback_journal(latest)
                self._log_agent(f"[green]✓ Auto-rolled back[/] {latest.tx_id}")
            else:
                self._log_agent("Use /recover --auto to rollback latest, or run [bold]rv recover[/] in terminal for interactive mode.")
            self._set_context("recover")
        except Exception as e:
            self._log_err(f"Recovery failed: {e}")
            self._set_context("unknown")

    def _run_help(self, prefix: str) -> None:
        cmds = autocomplete_commands(prefix)
        if not cmds:
            self._log_err(f"No commands match: {prefix}")
            self._set_context("unknown")
            return
        self._log_agent("Slash commands:")
        for cmd in cmds:
            self._log_agent(f"  [bold cyan]{cmd.path}[/]  [dim]{cmd.description}[/]")
            self._log_agent(f"    [dim]{cmd.usage}[/]")
        self._set_context("start")

    # ── Persist ───────────────────────────────────────────────────────────────

    def _save_manifest(self, manifest_path: str, manifest: Manifest) -> None:
        with open(manifest_path, "w", encoding="utf-8") as f:
            data = manifest.model_dump(mode="json", exclude_none=True)
            yaml.dump(data, f, sort_keys=False)


# ─── Entry ────────────────────────────────────────────────────────────────────

def start_tui() -> None:
    ReviveApp().run()


# ─── Back-compat aliases (tests) ─────────────────────────────────────────────
parse_agent_command = parse_command


def suggest_commands(prefix: str = "") -> list[AgentCommand]:
    """Alias for autocomplete_commands — back-compat."""
    return autocomplete_commands(prefix)
