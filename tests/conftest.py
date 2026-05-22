"""Pytest configuration and global hooks for the Revive test suite."""

import os
import subprocess
import pytest


def pytest_sessionstart(session: pytest.Session) -> None:
    """Triggered before the entire test session starts.

    Enables subprocess coverage measurement for plugins.
    """
    os.environ["REVIVE_COV_SUBPROCESS"] = "1"


def pytest_sessionfinish(session: pytest.Session, exitstatus: int) -> None:
    """Triggered after the entire test session completes.

    Combines coverage data from the main process and all sandboxed subprocesses.
    """
    # Clean up environment
    if "REVIVE_COV_SUBPROCESS" in os.environ:
        del os.environ["REVIVE_COV_SUBPROCESS"]

    # Combine coverage files generated in parallel
    try:
        # Run coverage combine to merge all parallel coverage files
        # We run it using the python coverage module in the active venv
        import sys
        cmd = [sys.executable, "-m", "coverage", "combine", "--rcfile=.coveragerc"]
        subprocess.run(cmd, check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    except Exception:
        pass
