"""ProcessLock for flock-based multi-process serialization on Unix environments."""

import fcntl
import os
from typing import IO, Any


class LockAcquisitionError(Exception):
    """Raised when the process cannot acquire the revive process lock."""

    pass


class ProcessLock:
    """Acquires a exclusive flock-based process lock on a lockfile to prevent race conditions."""

    def __init__(self, lock_path: str | None = None, blocking: bool = False):
        """Initializes the process lock.

        Args:
            lock_path: Path to the lockfile. Defaults to ~/.config/rv/rv.lock.
            blocking: If True, blocks until the lock is acquired. If False, fails immediately.
        """
        if lock_path is None:
            lock_path = os.path.expanduser("~/.config/rv/rv.lock")
        self.lock_path = os.path.abspath(lock_path)
        self.blocking = blocking
        self._lock_file: IO[str] | None = None

    def __enter__(self) -> "ProcessLock":
        # Ensure parent directory exists
        os.makedirs(os.path.dirname(self.lock_path), exist_ok=True)

        try:
            # Open the file for reading/writing (creates if not exists)
            self._lock_file = open(self.lock_path, "w")

            # Formulate lock flags
            flags = fcntl.LOCK_EX
            if not self.blocking:
                flags |= fcntl.LOCK_NB

            # Apply lock
            fcntl.flock(self._lock_file.fileno(), flags)

            # Write current PID into the lockfile for auditability
            self._lock_file.write(str(os.getpid()))
            self._lock_file.flush()
        except (BlockingIOError, PermissionError) as e:
            if self._lock_file:
                self._lock_file.close()
                self._lock_file = None
            raise LockAcquisitionError(
                f"Another revive process currently holds the lock at {self.lock_path}. Concurrency constraint violated."
            ) from e
        except Exception as e:
            if self._lock_file:
                self._lock_file.close()
                self._lock_file = None
            raise RuntimeError(f"Unexpected error while acquiring lock at {self.lock_path}: {e}") from e

        return self

    def __exit__(self, exc_type: type[BaseException] | None, exc_val: BaseException | None, exc_tb: Any) -> None:
        if self._lock_file:
            try:
                # Release flock explicitly and close
                fcntl.flock(self._lock_file.fileno(), fcntl.LOCK_UN)
                self._lock_file.close()
            except Exception:
                pass
            finally:
                self._lock_file = None
