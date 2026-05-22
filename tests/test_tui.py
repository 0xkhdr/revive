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

    assert [command.path for command in suggestions] == ["/workspace list", "/workspace add", "/workspace use"]
