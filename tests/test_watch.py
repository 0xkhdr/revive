"""Unit tests for the Watchdog Daemon and 'rv watch' CLI command."""

import os
import time
from unittest.mock import MagicMock, patch

import pytest
from typer.testing import CliRunner
from watchdog.events import FileSystemEvent

from rv.cli.main import app
from rv.transactions.lock import LockAcquisitionError
from rv.watchers.daemon import RepoChangeHandler, WatchdogDaemon

runner = CliRunner()


def test_repo_change_handler_ignores_git() -> None:
    """RepoChangeHandler should ignore events within .git folder."""
    handler = RepoChangeHandler(repo_dir="/tmp/fake_repo", profile_name="base", debounce_seconds=0.1)
    # Stop checker thread to prevent actual loop executions
    handler.stop()

    event_git = FileSystemEvent("/tmp/fake_repo/.git/config")
    event_git.event_type = "modified"
    handler.dispatch(event_git)
    assert handler._last_event_time is None

    event_normal = FileSystemEvent("/tmp/fake_repo/manifest.yaml")
    event_normal.event_type = "modified"
    handler.dispatch(event_normal)
    assert handler._last_event_time is not None


def test_repo_change_handler_ignores_directory_modifications() -> None:
    """RepoChangeHandler should ignore directory modified events."""
    handler = RepoChangeHandler(repo_dir="/tmp/fake_repo", profile_name="base", debounce_seconds=0.1)
    handler.stop()

    # Directory modified event
    event = MagicMock(spec=FileSystemEvent)
    event.src_path = "/tmp/fake_repo/assets"
    event.is_directory = True
    event.event_type = "modified"

    handler.on_any_event(event)
    assert handler._last_event_time is None


def test_repo_change_handler_debounce_triggers_restore() -> None:
    """RepoChangeHandler debounces events and triggers RestoreService.restore."""
    with patch("rv.services.restore.RestoreService.restore") as mock_restore:
        handler = RepoChangeHandler(repo_dir="/tmp/fake_repo", profile_name="base", debounce_seconds=0.05)
        try:
            event = FileSystemEvent("/tmp/fake_repo/manifest.yaml")
            event.event_type = "modified"
            handler.on_any_event(event)

            # Wait for debounce
            time.sleep(0.15)
            mock_restore.assert_called_once_with(
                repo_dir=os.path.abspath("/tmp/fake_repo"),
                profile_name="base",
                identity_path=None,
                interactive=False,
                dry_run=False,
                no_plugins=False,
            )
        finally:
            handler.stop()


def test_repo_change_handler_lock_collision() -> None:
    """RepoChangeHandler skips restore execution if another process holds the lock."""
    with patch("rv.services.restore.RestoreService.restore") as mock_restore:
        mock_restore.side_effect = LockAcquisitionError("Lock busy")

        handler = RepoChangeHandler(repo_dir="/tmp/fake_repo", profile_name="base", debounce_seconds=0.01)
        try:
            # Manually trigger restore execution which catches LockAcquisitionError
            handler._execute_restore()
            mock_restore.assert_called_once()
        finally:
            handler.stop()


def test_watchdog_daemon_start_stop() -> None:
    """WatchdogDaemon starts and stops observers correctly."""
    with patch("rv.watchers.daemon.Observer") as mock_observer_cls:
        mock_observer = MagicMock()
        mock_observer_cls.return_value = mock_observer

        daemon = WatchdogDaemon(
            repo_dir="/tmp/fake_repo", profile_name="base", identity_path="id_file", debounce_seconds=3.0
        )
        daemon._shutdown_event.set()  # Prevent start() from blocking
        daemon.start()
        mock_observer_cls.assert_called_once()
        mock_observer.schedule.assert_called_once()
        mock_observer.start.assert_called_once()

        daemon.stop()
        mock_observer.stop.assert_called()
        mock_observer.join.assert_called()


def test_cli_watch_command() -> None:
    """CLI watch command initializes, starts daemon, and handles KeyboardInterrupt."""
    with (
        patch("rv.watchers.daemon.WatchdogDaemon.start") as mock_start,
        patch("rv.watchers.daemon.WatchdogDaemon.stop") as mock_stop,
        patch("time.sleep", side_effect=KeyboardInterrupt),
    ):
        result = runner.invoke(app, ["watch", "--profile", "base", "--debounce", "2.0"])
        assert result.exit_code == 0
        assert "Stopping watchdog daemon..." in result.stdout
        mock_start.assert_called_once()
        mock_stop.assert_called_once()
