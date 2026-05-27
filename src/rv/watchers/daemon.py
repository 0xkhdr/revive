"""Watchdog daemon for revive. Monitors repo for changes and triggers restore."""

import os
import signal
import threading
import time
from typing import Any

from watchdog.events import FileSystemEvent, PatternMatchingEventHandler
from watchdog.observers import Observer

from rv.logging.audit import AuditLogger
from rv.services.restore import RestoreService
from rv.transactions.lock import LockAcquisitionError, ProcessLock

logger = AuditLogger.get_logger("rv.watchers.daemon")

# Patterns to exclude from filesystem event monitoring
_GIT_EXCLUDE_PATTERNS: list[str] = ["*/.git/*", "*/.git"]


class RepoChangeHandler(PatternMatchingEventHandler):
    """Handles filesystem events in the revive repository, ignoring .git changes."""

    def __init__(
        self,
        repo_dir: str,
        profile_name: str,
        identity_path: str | None = None,
        debounce_seconds: float = 5.0,
        manifest_path: str | None = None,
    ):
        # Exclude .git directory changes at the observer level using PatternMatchingEventHandler
        super().__init__(
            patterns=["*"],
            ignore_patterns=_GIT_EXCLUDE_PATTERNS,
            ignore_directories=False,
            case_sensitive=True,
        )
        self.repo_dir = os.path.abspath(repo_dir)
        self.profile_name = profile_name
        self.identity_path = identity_path
        self.debounce_seconds = debounce_seconds
        self.manifest_path = manifest_path

        self._last_event_time: float | None = None
        self._lock = threading.Lock()
        self._stop_event = threading.Event()

        # Start the debounce checker thread
        self._checker_thread = threading.Thread(target=self._debounce_loop, daemon=True)
        self._checker_thread.start()

    def on_any_event(self, event: FileSystemEvent) -> None:
        # Ignore directory modifications (only care about files)
        if event.is_directory and event.event_type == "modified":
            return

        src_path = os.fsdecode(event.src_path)
        logger.debug(f"Detected filesystem event: {event.event_type} on {src_path}")
        with self._lock:
            self._last_event_time = time.time()

    def _debounce_loop(self) -> None:
        """Continuously checks if the debounce window has elapsed since the last event."""
        while not self._stop_event.is_set():
            time.sleep(0.1)
            trigger_restore = False

            with self._lock:
                if self._last_event_time is not None:
                    elapsed = time.time() - self._last_event_time
                    if elapsed >= self.debounce_seconds:
                        self._last_event_time = None
                        trigger_restore = True

            if trigger_restore:
                logger.info(f"Debounce period of {self.debounce_seconds}s elapsed. Triggering auto-restore...")
                self._execute_restore()

    def _execute_restore(self) -> None:
        """Tries to execute the restore process. Skips if the process lock is currently held."""
        try:
            logger.info(f"Auto-applying changes in '{self.repo_dir}' for profile '{self.profile_name}'...")
            RestoreService.restore(
                repo_dir=self.repo_dir,
                profile_name=self.profile_name,
                identity_path=self.identity_path,
                interactive=False,  # Headless auto-apply must not prompt for conflicts
                dry_run=False,
                no_plugins=False,
                manifest_path=self.manifest_path,
            )
            logger.info("Auto-restore completed successfully.")
        except LockAcquisitionError:
            logger.warning("Another revive process currently holds the lock. Skipping this auto-restore trigger.")
        except Exception as e:
            logger.error(f"Auto-restore failed during execution: {e}", exc_info=True)

    def stop(self) -> None:
        """Signals the debounce loop thread to stop."""
        self._stop_event.set()
        self._checker_thread.join(timeout=2.0)


class WatchdogDaemon:
    """Watchdog daemon coordinating filesystem observation with graceful signal handling."""

    def __init__(
        self,
        repo_dir: str,
        profile_name: str,
        identity_path: str | None = None,
        debounce_seconds: float = 5.0,
        manifest_path: str | None = None,
    ):
        self.repo_dir = repo_dir
        self.profile_name = profile_name
        self.identity_path = identity_path
        self.debounce_seconds = debounce_seconds
        self.manifest_path = manifest_path
        self._observer: Any = None
        self._handler: RepoChangeHandler | None = None
        self._shutdown_event = threading.Event()

    def start(self) -> None:
        """Starts monitoring the repository directory and blocks until shutdown."""
        logger.info(f"Starting revive watchdog on '{self.repo_dir}' for profile '{self.profile_name}'...")
        self._handler = RepoChangeHandler(
            repo_dir=self.repo_dir,
            profile_name=self.profile_name,
            identity_path=self.identity_path,
            debounce_seconds=self.debounce_seconds,
            manifest_path=self.manifest_path,
        )
        self._observer = Observer()
        self._observer.schedule(self._handler, self.repo_dir, recursive=True)
        self._observer.start()
        logger.info("Watchdog started successfully. Press Ctrl+C to exit.")

        # Register signal handlers for graceful shutdown
        def _signal_handler(signum: int, frame: Any) -> None:
            sig_name = signal.Signals(signum).name
            logger.info(f"Received signal {sig_name}. Initiating graceful watchdog shutdown...")
            self._shutdown_event.set()

        original_sigint = signal.getsignal(signal.SIGINT)
        original_sigterm = signal.getsignal(signal.SIGTERM)
        signal.signal(signal.SIGINT, _signal_handler)
        signal.signal(signal.SIGTERM, _signal_handler)

        try:
            # Block main thread until shutdown signal received
            self._shutdown_event.wait()
        finally:
            # Restore original signal handlers
            signal.signal(signal.SIGINT, original_sigint)
            signal.signal(signal.SIGTERM, original_sigterm)
            self.stop()

    def stop(self) -> None:
        """Stops monitoring and cleans up threads."""
        if self._observer:
            self._observer.stop()
            self._observer.join()
        if self._handler:
            self._handler.stop()
        logger.info("Watchdog daemon stopped.")
