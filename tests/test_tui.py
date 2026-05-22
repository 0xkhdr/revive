"""Tests for the Revive TUI command parser."""

import pytest

from rv.cli.tui import parse_agent_command, suggest_commands


def test_parse_agent_command_normalizes_plain_commands() -> None:
    parsed = parse_agent_command("status work --identity ~/.age/key.txt")

    assert parsed.path == "/status"
    assert parsed.args == ("work",)
    assert parsed.flags == {"identity": "~/.age/key.txt"}


def test_parse_agent_command_supports_subcommands_and_boolean_flags() -> None:
    parsed = parse_agent_command('/workspace add "/tmp/my repo" --dry-run')

    assert parsed.path == "/workspace add"
    assert parsed.args == ("/tmp/my repo",)
    assert parsed.flags == {"dry_run": True}


def test_parse_agent_command_supports_asset_commands() -> None:
    parsed = parse_agent_command('/asset import-secret "./token.txt" --recipient age1abc --target ~/.token')

    assert parsed.path == "/asset import-secret"
    assert parsed.args == ("./token.txt",)
    assert parsed.flags == {"recipient": "age1abc", "target": "~/.token"}


def test_parse_agent_command_rejects_unknown_commands() -> None:
    with pytest.raises(ValueError, match="Unknown command"):
        parse_agent_command("/missing")


def test_suggest_commands_filters_by_prefix() -> None:
    suggestions = suggest_commands("/workspace")
    paths = [command.path for command in suggestions]

    assert "/workspace list" in paths
    assert "/workspace add" in paths
    assert "/workspace use" in paths
    assert all(p.startswith("/workspace") for p in paths)


def test_parse_agent_command_supports_multiple_assets() -> None:
    parsed = parse_agent_command('/asset import @file1.txt @file2.txt --profile main')
    assert parsed.path == "/asset import"
    assert parsed.args == ("@file1.txt", "@file2.txt")
    assert parsed.flags == {"profile": "main"}


def test_get_path_completions(tmp_path) -> None:
    from rv.cli.tui import get_path_completions
    
    # Create some temp files and directories
    d = tmp_path / "sub"
    d.mkdir()
    (d / "file1.txt").touch()
    (d / "file2.txt").touch()
    (d / "nested").mkdir()
    
    # Test search with relative directory prefix
    matches = get_path_completions(str(d) + "/f")
    assert matches == [str(d) + "/file1.txt", str(d) + "/file2.txt"]
    
    # Test search with @ prefix
    matches = get_path_completions("@" + str(d) + "/f")
    assert matches == ["@" + str(d) + "/file1.txt", "@" + str(d) + "/file2.txt"]


def test_tui_command_history_navigation() -> None:
    from unittest.mock import MagicMock
    from rv.cli.tui import ReviveApp
    
    app = ReviveApp()
    app._history = ["first_command", "second_command"]
    app._history_cursor = -1
    
    mock_input = MagicMock()
    mock_input.value = "current_typed_text"
    
    app.query_one = MagicMock(return_value=mock_input)
    
    # Pressing Up should store current typed text and show the last command
    app._history_up()
    assert app._saved_input == "current_typed_text"
    assert app._history_cursor == 1
    assert mock_input.value == "second_command"
    
    # Pressing Up again should show the first command
    app._history_up()
    assert app._history_cursor == 0
    assert mock_input.value == "first_command"
    
    # Pressing Up at the top should do nothing
    app._history_up()
    assert app._history_cursor == 0
    assert mock_input.value == "first_command"
    
    # Pressing Down should show the second command
    app._history_down()
    assert app._history_cursor == 1
    assert mock_input.value == "second_command"
    
    # Pressing Down at the last command should restore the saved typed text
    app._history_down()
    assert app._history_cursor == -1
    assert mock_input.value == "current_typed_text"


