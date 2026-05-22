"""Disaster recovery service for Revive (rv).
"""

import glob
import os
import shutil

from rv.logging.audit import AuditLogger
from rv.models.transaction import TransactionJournal
from rv.transactions.context import TransactionContext

logger = AuditLogger.get_logger("rv.services.recovery")


class RecoveryService:
    """Disaster recovery service to scan, list, and recover incomplete transactions."""

    @staticmethod
    def get_journal_dir() -> str:
        """Returns the journals directory path."""
        return os.path.expanduser("~/.config/rv/journals")

    @classmethod
    def list_incomplete_journals(cls) -> list[TransactionJournal]:
        """Scans the journals directory for incomplete/interrupted transactions.

        Returns:
            A list of TransactionJournal models sorted by timestamp (newest first).
        """
        journal_dir = cls.get_journal_dir()
        if not os.path.exists(journal_dir):
            return []

        incomplete = []
        for path in glob.glob(os.path.join(journal_dir, "*.json")):
            try:
                with open(path, encoding="utf-8") as f:
                    content = f.read()
                journal = TransactionJournal.model_validate_json(content)
                if journal.status not in ("committed", "rolled_back"):
                    incomplete.append(journal)
            except Exception as e:
                logger.warning(f"Failed to read/validate journal file {path}: {e}")
                continue

        # Sort newest first
        incomplete.sort(key=lambda j: j.timestamp, reverse=True)
        return incomplete

    @classmethod
    def rollback_journal(cls, journal: TransactionJournal) -> None:
        """Performs a full rollback of the given transaction journal.

        Args:
            journal: The TransactionJournal to roll back.
        """
        logger.info(f"Initiating rollback recovery for transaction {journal.tx_id}...")

        # Reconstruct TransactionContext from the journal
        tx_context = TransactionContext(tx_id=journal.tx_id)
        tx_context.timestamp = journal.timestamp
        tx_context.status = journal.status
        tx_context.entries = journal.entries

        try:
            # Perform rollback
            tx_context.rollback()
            logger.info(f"Rollback completed successfully for transaction {journal.tx_id}.")

            # Clean up the journal and backup files
            if os.path.exists(tx_context.journal_path):
                try:
                    os.unlink(tx_context.journal_path)
                except Exception:
                    pass
            if os.path.exists(tx_context.backup_dir):
                try:
                    shutil.rmtree(tx_context.backup_dir)
                except Exception:
                    pass
        except Exception as e:
            logger.error(f"Failed to roll back transaction {journal.tx_id}: {e}")
            raise RuntimeError(f"Rollback failed: {e}") from e

    @classmethod
    def discard_journal(cls, journal: TransactionJournal) -> None:
        """Discards the journal and backup directory without rolling back mutated files.

        Args:
            journal: The TransactionJournal to discard.
        """
        logger.info(f"Discarding transaction journal {journal.tx_id}...")
        tx_context = TransactionContext(tx_id=journal.tx_id)

        if os.path.exists(tx_context.journal_path):
            try:
                os.unlink(tx_context.journal_path)
            except Exception:
                pass
        if os.path.exists(tx_context.backup_dir):
            try:
                shutil.rmtree(tx_context.backup_dir)
            except Exception:
                pass
        logger.info(f"Transaction journal {journal.tx_id} discarded.")
