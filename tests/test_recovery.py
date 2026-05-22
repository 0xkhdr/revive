"""Unit tests for the Disaster Recovery service and 'rv recover' CLI command."""

from unittest.mock import MagicMock, mock_open, patch

import pytest

from rv.cli.main import app
from rv.models.transaction import TransactionJournal
from rv.services.recovery import RecoveryService
from typer.testing import CliRunner

runner = CliRunner()


@pytest.fixture
def sample_journals() -> list[TransactionJournal]:
    j1 = TransactionJournal(tx_id="tx_old", timestamp=1000.0, status="planned", entries=[])
    j2 = TransactionJournal(tx_id="tx_new", timestamp=2000.0, status="executing", entries=[])
    j3 = TransactionJournal(tx_id="tx_committed", timestamp=3000.0, status="committed", entries=[])
    return [j1, j2, j3]


def test_list_incomplete_journals(sample_journals: list[TransactionJournal]) -> None:
    """list_incomplete_journals scans journal dir, parses json, filters committed/rolled_back, and sorts by timestamp."""
    with (
        patch("os.path.exists", return_value=True),
        patch("glob.glob", return_value=["path1.json", "path2.json", "path3.json"]),
    ):
        contents = [
            sample_journals[0].model_dump_json(),
            sample_journals[1].model_dump_json(),
            sample_journals[2].model_dump_json(),
        ]

        def mock_open_file(filepath: str, *args: any, **kwargs: any):
            idx = ["path1.json", "path2.json", "path3.json"].index(filepath)
            return mock_open(read_data=contents[idx])()

        with patch("builtins.open", side_effect=mock_open_file):
            incomplete = RecoveryService.list_incomplete_journals()

            # Should filter out "committed" status (j3), and return [j2, j1] sorted by timestamp (newest first)
            assert len(incomplete) == 2
            assert incomplete[0].tx_id == "tx_new"
            assert incomplete[1].tx_id == "tx_old"


def test_list_incomplete_journals_empty_or_error() -> None:
    """list_incomplete_journals handles missing directories and malformed journals gracefully."""
    # Missing directory
    with patch("os.path.exists", return_value=False):
        assert RecoveryService.list_incomplete_journals() == []

    # Malformed journal JSON
    with (
        patch("os.path.exists", return_value=True),
        patch("glob.glob", return_value=["malformed.json"]),
        patch("builtins.open", mock_open(read_data="{invalid_json}")),
    ):
        assert RecoveryService.list_incomplete_journals() == []


def test_rollback_journal(sample_journals: list[TransactionJournal]) -> None:
    """rollback_journal reconstructs context, calls rollback, and cleans up files."""
    journal = sample_journals[1]  # tx_new

    with (
        patch("rv.services.recovery.TransactionContext") as mock_ctx_cls,
        patch("os.path.exists", return_value=True),
        patch("os.unlink") as mock_unlink,
        patch("shutil.rmtree") as mock_rmtree,
    ):
        mock_ctx = MagicMock()
        mock_ctx_cls.return_value = mock_ctx
        mock_ctx.journal_path = "/fake/journal.json"
        mock_ctx.backup_dir = "/fake/backup"

        RecoveryService.rollback_journal(journal)

        mock_ctx.rollback.assert_called_once()
        mock_unlink.assert_called_once_with("/fake/journal.json")
        mock_rmtree.assert_called_once_with("/fake/backup")


def test_discard_journal(sample_journals: list[TransactionJournal]) -> None:
    """discard_journal purges the journal and backups without executing a rollback."""
    journal = sample_journals[1]  # tx_new

    with (
        patch("rv.services.recovery.TransactionContext") as mock_ctx_cls,
        patch("os.path.exists", return_value=True),
        patch("os.unlink") as mock_unlink,
        patch("shutil.rmtree") as mock_rmtree,
    ):
        mock_ctx = MagicMock()
        mock_ctx_cls.return_value = mock_ctx
        mock_ctx.journal_path = "/fake/journal.json"
        mock_ctx.backup_dir = "/fake/backup"

        RecoveryService.discard_journal(journal)

        mock_ctx.rollback.assert_not_called()
        mock_unlink.assert_called_once_with("/fake/journal.json")
        mock_rmtree.assert_called_once_with("/fake/backup")


def test_cli_recover_no_incomplete() -> None:
    """CLI recover command exits with code 0 when no incomplete transactions exist."""
    with patch("rv.services.recovery.RecoveryService.list_incomplete_journals", return_value=[]):
        result = runner.invoke(app, ["recover"])
        assert result.exit_code == 0
        assert "No incomplete transactions found." in result.stdout


def test_cli_recover_auto_rollback(sample_journals: list[TransactionJournal]) -> None:
    """CLI recover --auto rolls back the latest incomplete transaction."""
    incomplete = [sample_journals[1], sample_journals[0]]  # tx_new, tx_old

    with (
        patch("rv.services.recovery.RecoveryService.list_incomplete_journals", return_value=incomplete),
        patch("rv.services.recovery.RecoveryService.rollback_journal") as mock_rollback,
    ):
        result = runner.invoke(app, ["recover", "--auto"])
        assert result.exit_code == 0
        assert "Auto-recovering latest transaction tx_new..." in result.stdout
        assert "Transaction tx_new successfully rolled back." in result.stdout
        mock_rollback.assert_called_once_with(sample_journals[1])


def test_cli_recover_interactive(sample_journals: list[TransactionJournal]) -> None:
    """CLI recover in interactive mode prompts action and executes chosen route."""
    incomplete = [sample_journals[1]]  # tx_new

    # 1. Test Rollback option
    with (
        patch("rv.services.recovery.RecoveryService.list_incomplete_journals", return_value=incomplete),
        patch("rv.services.recovery.RecoveryService.rollback_journal") as mock_rollback,
        patch("typer.prompt", return_value="r"),
    ):
        result = runner.invoke(app, ["recover"])
        assert result.exit_code == 0
        assert "Transaction tx_new rolled back." in result.stdout
        mock_rollback.assert_called_once_with(sample_journals[1])

    # 2. Test Discard option
    with (
        patch("rv.services.recovery.RecoveryService.list_incomplete_journals", return_value=incomplete),
        patch("rv.services.recovery.RecoveryService.discard_journal") as mock_discard,
        patch("typer.prompt", return_value="d"),
    ):
        result = runner.invoke(app, ["recover"])
        assert result.exit_code == 0
        assert "Transaction tx_new journal discarded." in result.stdout
        mock_discard.assert_called_once_with(sample_journals[1])

    # 3. Test Skip option
    with (
        patch("rv.services.recovery.RecoveryService.list_incomplete_journals", return_value=incomplete),
        patch("typer.prompt", return_value="s"),
    ):
        result = runner.invoke(app, ["recover"])
        assert result.exit_code == 0
        assert "Skipping transaction recovery." in result.stdout

    # 4. Test Invalid input then Skip
    with (
        patch("rv.services.recovery.RecoveryService.list_incomplete_journals", return_value=incomplete),
        patch("typer.prompt", side_effect=["invalid", "s"]),
    ):
        result = runner.invoke(app, ["recover"])
        assert result.exit_code == 0
        assert "Invalid action" in result.stdout
        assert "Skipping transaction recovery." in result.stdout
