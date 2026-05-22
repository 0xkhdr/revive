"""Test suite for structured JSON lines auditing and log formatting.
"""

import json
import logging
import os
from typing import Any
import pytest
from rv.logging.audit import AuditLogger, JsonAuditFormatter


def test_json_audit_formatter() -> None:
    formatter = JsonAuditFormatter()
    record = logging.LogRecord(
        name="test_logger",
        level=logging.INFO,
        pathname="test.py",
        lineno=10,
        msg="Transaction completed successfully",
        args=(),
        exc_info=None
    )
    # Inject extra attributes
    setattr(record, "tx_id", "12345-abcde")
    setattr(record, "asset_id", "zshrc")
    setattr(record, "op", "symlink")

    formatted = formatter.format(record)
    data = json.loads(formatted)

    assert data["level"] == "INFO"
    assert data["message"] == "Transaction completed successfully"
    assert data["tx_id"] == "12345-abcde"
    assert data["asset_id"] == "zshrc"
    assert data["op"] == "symlink"


def test_audit_logger_setup_and_log(tmpdir: Any) -> None:
    # Set home or override location if needed, but AuditLogger writes to ~/.local/share/rv
    # We can just verify setup completes and logs a line
    AuditLogger.setup(verbose=True, headless=True)
    
    # Audit logger should not raise any exceptions
    AuditLogger.log_audit("Verification completed", tx_id="test-tx-99", asset_id="gitconfig")
    
    audit_file = os.path.expanduser("~/.local/share/rv/audit.log")
    assert os.path.exists(audit_file)

    # Read the last line of the audit file
    with open(audit_file, "r") as f:
        lines = f.readlines()
        assert len(lines) > 0
        last_line = lines[-1]
        data = json.loads(last_line)
        assert data["message"] == "Verification completed"
        assert data["tx_id"] == "test-tx-99"
        assert data["asset_id"] == "gitconfig"
