"""AuditLogger providing structured JSON logging and human console formatting.

Enforces dynamic secret scrubbing across both channels.
"""

import json
import logging
import os
import sys
from typing import Any

from rv.security.scrubber import ScrubbingFormatter, SecretScrubber


class JsonAuditFormatter(logging.Formatter):
    """Formats log records as structured JSON lines, ensuring all values are scrubbed."""

    def format(self, record: logging.LogRecord) -> str:
        log_data: dict[str, Any] = {
            "timestamp": self.formatTime(record, self.datefmt),
            "level": record.levelname,
            "logger": record.name,
            "message": record.getMessage(),
        }

        # Include extra attributes if provided
        if hasattr(record, "tx_id"):
            log_data["tx_id"] = record.tx_id
        if hasattr(record, "asset_id"):
            log_data["asset_id"] = record.asset_id
        if hasattr(record, "op"):
            log_data["op"] = record.op

        # Serialize to JSON and scrub the entire line
        serialized = json.dumps(log_data)
        return SecretScrubber.scrub(serialized)


class AuditLogger:
    """Configures and provides structured audit and human console logging."""

    _configured = False
    audit_logger: logging.Logger | None = None

    @classmethod
    def setup(cls, verbose: bool = False, headless: bool = False) -> None:
        """Initializes the logging system with human console and JSON audit file logging."""
        if cls._configured:
            return

        # 1. Setup Root Logger
        root_logger = logging.getLogger()
        root_logger.setLevel(logging.DEBUG if verbose else logging.INFO)

        # Clear existing handlers
        root_logger.handlers = []

        # 2. Add Human Console Handler (if not headless)
        if not headless:
            try:
                from rich.logging import RichHandler

                console_handler = RichHandler(
                    show_path=False,
                    omit_repeated_times=True,
                    markup=True,
                    rich_tracebacks=True,
                    tracebacks_show_locals=False,
                )
                console_handler.setFormatter(ScrubbingFormatter("[rv] %(message)s"))
                root_logger.addHandler(console_handler)
            except ImportError:
                # Fallback to standard stream handler
                stream_handler = logging.StreamHandler(sys.stdout)
                stream_handler.setFormatter(ScrubbingFormatter("[rv] %(levelname)s - %(message)s"))
                root_logger.addHandler(stream_handler)
        else:
            # CI/headless mode stream handler (no colors)
            stream_handler = logging.StreamHandler(sys.stdout)
            stream_handler.setFormatter(ScrubbingFormatter("[rv_ci] %(asctime)s - %(levelname)s - %(message)s"))
            root_logger.addHandler(stream_handler)

        # 3. Add Structured JSON Audit File Handler
        audit_dir = os.path.expanduser("~/.local/share/rv")
        os.makedirs(audit_dir, exist_ok=True)
        audit_file = os.path.join(audit_dir, "audit.log")

        # Configure specific audit logger
        cls.audit_logger = logging.getLogger("rv_audit")
        cls.audit_logger.propagate = False  # Avoid duplicating in console logs
        cls.audit_logger.setLevel(logging.INFO)

        # Create file handler
        file_handler = logging.FileHandler(audit_file, encoding="utf-8")
        file_handler.setFormatter(JsonAuditFormatter())
        cls.audit_logger.addHandler(file_handler)

        cls._configured = True

    @classmethod
    def get_logger(cls, name: str) -> logging.Logger:
        """Retrieves a named logger configured with the root settings."""
        if not cls._configured:
            cls.setup()
        return logging.getLogger(name)

    @classmethod
    def log_audit(cls, message: str, level: int = logging.INFO, **extra: Any) -> None:
        """Logs a structured audit event to the JSON audit file."""
        if not cls._configured:
            cls.setup()

        logger = cls.audit_logger or logging.getLogger("rv_audit")
        # Log with extra attributes so JsonAuditFormatter serializes them
        logger.log(level, message, extra=extra)
