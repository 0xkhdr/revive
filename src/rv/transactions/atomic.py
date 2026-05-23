"""Atomic write helpers using temporary files and directory renames."""

import os
import tempfile


class AtomicWrite:
    """Performs atomic filesystem mutations using the temp-file-and-rename pattern."""

    @staticmethod
    def write(target_path: str, content: str | bytes) -> None:
        """Atomically writes content to target_path using a temporary sibling file and os.rename.

        Guarantees that the target path is either completely updated or untouched in case of failure.
        """
        abs_target = os.path.abspath(target_path)
        parent_dir = os.path.dirname(abs_target)

        # Ensure the target directory exists
        os.makedirs(parent_dir, exist_ok=True)

        # Create temporary file in the SAME directory to avoid cross-device renames failing
        fd, temp_path = tempfile.mkstemp(dir=parent_dir, prefix=".rv_atomic_tmp_")

        try:
            mode = "wb" if isinstance(content, bytes) else "w"
            encoding = None if isinstance(content, bytes) else "utf-8"

            with os.fdopen(fd, mode, encoding=encoding) as f:
                f.write(content)
                f.flush()
                # Ensure all OS buffers are flushed to disk
                os.fsync(f.fileno())

            # Atomically rename temporary file to target path
            os.rename(temp_path, abs_target)
        except Exception as e:
            # Clean up the temporary file if anything goes wrong
            if os.path.exists(temp_path):
                try:
                    os.unlink(temp_path)
                except OSError:
                    pass
            raise RuntimeError(f"Atomic write to {target_path} failed: {e}") from e
