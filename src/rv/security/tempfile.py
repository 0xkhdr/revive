"""SecureTempFile context manager for highly restricted temporary storage."""

import os
import shutil
import tempfile
from collections.abc import Generator
from contextlib import contextmanager


class SecureTempFile:
    """Manages creation and teardown of highly restricted temporary files and directories."""

    @staticmethod
    @contextmanager
    def file(suffix: str | None = None, prefix: str | None = "rv_sec_") -> Generator[str, None, None]:
        """Creates an extremely restricted temp file (mode 0600).

        Yields:
            The absolute path of the created temporary file.
        """
        fd, path = tempfile.mkstemp(suffix=suffix, prefix=prefix)
        try:
            # Re-enforce 0600 permissions just to be safe
            os.chmod(path, 0o600)
            os.close(fd)
            yield path
        finally:
            # Overwrite file contents with zeros before unlinking to ensure secret sanitization
            try:
                if os.path.exists(path):
                    size = os.path.getsize(path)
                    if size > 0:
                        with open(path, "wb") as f:
                            f.write(b"\x00" * size)
                            f.flush()
                            os.fsync(f.fileno())
                    os.unlink(path)
            except Exception:
                pass

    @staticmethod
    @contextmanager
    def directory(prefix: str | None = "rv_sec_dir_") -> Generator[str, None, None]:
        """Creates an extremely restricted temp directory (mode 0700).

        Yields:
            The absolute path of the created directory.
        """
        path = tempfile.mkdtemp(prefix=prefix)
        try:
            os.chmod(path, 0o700)
            yield path
        finally:
            try:
                if os.path.exists(path):
                    # Sanitize any files inside the temp directory first
                    for root, _dirs, files in os.walk(path):
                        for file in files:
                            filepath = os.path.join(root, file)
                            try:
                                size = os.path.getsize(filepath)
                                if size > 0:
                                    with open(filepath, "wb") as f:
                                        f.write(b"\x00" * size)
                                        f.flush()
                                        os.fsync(f.fileno())
                            except Exception:
                                pass
                    shutil.rmtree(path)
            except Exception:
                pass
