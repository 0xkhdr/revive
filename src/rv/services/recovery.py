"""Disaster recovery service for Revive (rv)."""

import glob
import os
import shutil
import time
from typing import Any, cast

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
                except OSError as e:
                    logger.warning(f"Could not delete journal {tx_context.journal_path}: {e}")
            if os.path.exists(tx_context.backup_dir):
                try:
                    shutil.rmtree(tx_context.backup_dir)
                except OSError as e:
                    logger.warning(f"Could not delete backup dir {tx_context.backup_dir}: {e}")
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
            except OSError as e:
                logger.warning(f"Could not delete journal {tx_context.journal_path}: {e}")
        if os.path.exists(tx_context.backup_dir):
            try:
                shutil.rmtree(tx_context.backup_dir)
            except OSError as e:
                logger.warning(f"Could not delete backup dir {tx_context.backup_dir}: {e}")
        logger.info(f"Transaction journal {journal.tx_id} discarded.")


class BackupPruner:
    """Manages automatic cleanup of old transaction backup snapshots to prevent disk bloat.

    Pruning respects two safety guards:
    1. Never deletes backup directories that have an active (non-committed) transaction journal.
    2. Evaluates candidates sorted by last-modified time, applying both max_count and max_age_days.
    """

    _BACKUP_BASE_DIR: str = os.path.expanduser("~/.config/rv/backups")

    @classmethod
    def _get_active_tx_ids(cls) -> frozenset[str]:
        """Returns the set of transaction IDs that have incomplete (active) journals."""
        journal_dir = RecoveryService.get_journal_dir()
        active_ids: set[str] = set()
        if not os.path.exists(journal_dir):
            return frozenset()
        for path in glob.glob(os.path.join(journal_dir, "*.json")):
            try:
                with open(path, encoding="utf-8") as f:
                    content = f.read()
                journal = TransactionJournal.model_validate_json(content)
                if journal.status not in ("committed", "rolled_back"):
                    active_ids.add(journal.tx_id)
            except Exception:
                continue
        return frozenset(active_ids)

    @classmethod
    def list_backup_dirs(cls) -> list[dict[str, Any]]:
        """Returns all backup snapshot directories with metadata.

        Returns:
            List of dicts with: tx_id, path, mtime, age_days, size_bytes.
        """
        backup_base = cls._BACKUP_BASE_DIR
        if not os.path.exists(backup_base):
            return []

        entries = []
        try:
            for name in os.listdir(backup_base):
                full_path = os.path.join(backup_base, name)
                if not os.path.isdir(full_path):
                    continue
                try:
                    stat = os.stat(full_path)
                    mtime = stat.st_mtime
                    age_days = (time.time() - mtime) / 86400.0

                    # Calculate directory size
                    total_size = 0
                    for dirpath, _dirnames, filenames in os.walk(full_path):
                        for fname in filenames:
                            try:
                                total_size += os.path.getsize(os.path.join(dirpath, fname))
                            except OSError:
                                pass

                    entries.append(
                        {
                            "tx_id": name,
                            "path": full_path,
                            "mtime": mtime,
                            "age_days": age_days,
                            "size_bytes": total_size,
                        }
                    )
                except OSError:
                    continue
        except OSError:
            return []

        # Sort oldest first (candidates for pruning come from the front)
        entries.sort(key=lambda e: cast(float, e["mtime"]))
        return entries

    @classmethod
    def prune(cls, max_count: int = 10, max_age_days: int = 30, dry_run: bool = False) -> list[str]:
        """Removes old backup snapshots according to the retention policy.

        Args:
            max_count: Keep at most N backup directories (FIFO — oldest removed first).
            max_age_days: Remove any backup older than N days.
            dry_run: If True, report what would be deleted without removing anything.

        Returns:
            List of paths that were deleted (or would be deleted in dry-run mode).
        """
        active_tx_ids = cls._get_active_tx_ids()
        all_entries = cls.list_backup_dirs()

        # Determine candidates for pruning
        candidates: list[dict[str, Any]] = []

        # 1. Age-based pruning: mark all backups older than max_age_days
        age_cutoff_secs = time.time() - (max_age_days * 86400.0)
        for entry in all_entries:
            if entry["tx_id"] in active_tx_ids:
                logger.debug(f"Skipping active transaction backup: {entry['tx_id']}")
                continue
            if entry["mtime"] < age_cutoff_secs:
                candidates.append(entry)

        # 2. Count-based pruning: if still too many after age pruning, FIFO evict oldest
        remaining = [e for e in all_entries if e not in candidates and e["tx_id"] not in active_tx_ids]
        excess_count = len(remaining) - max_count
        if excess_count > 0:
            # remaining is sorted oldest-first; take the oldest excess entries
            candidates.extend(remaining[:excess_count])

        # Deduplicate (a backup can qualify on both criteria)
        seen_paths: set[str] = set()
        unique_candidates = []
        for entry in candidates:
            if entry["path"] not in seen_paths:
                seen_paths.add(entry["path"])
                unique_candidates.append(entry)

        deleted_paths = []
        for entry in unique_candidates:
            path = entry["path"]
            if dry_run:
                logger.info(
                    f"[Dry Run] Would delete backup snapshot: {path} "
                    f"(age: {entry['age_days']:.1f}d, size: {entry['size_bytes'] // 1024}KB)"
                )
                deleted_paths.append(path)
            else:
                try:
                    shutil.rmtree(path)
                    logger.info(f"Pruned backup snapshot: {path} (age: {entry['age_days']:.1f}d)")
                    deleted_paths.append(path)
                except OSError as e:
                    logger.warning(f"Failed to delete backup snapshot {path}: {e}")

        return deleted_paths
