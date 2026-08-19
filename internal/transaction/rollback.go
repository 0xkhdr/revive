package transaction

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/0xkhdr/revive/internal/permissions"
)

// ErrRollbackIncomplete is returned when rollback could not fully restore the prior state. Its
// message names every path involved: this is the one place rv may leave a machine in a mixed
// state, and the user has to be told exactly which files those are.
var ErrRollbackIncomplete = errors.New("rollback did not fully restore prior state")

// Rollback restores every entry from its backup, in reverse order.
//
// Reverse order is mandatory. A transaction that deletes ~/.zshrc and then symlinks it must undo
// the symlink before restoring the original, or the restore writes through the link.
//
// A single entry's failure does not abort the loop: partial recovery beats none. Failures are
// collected and reported together.
func (t *Transaction) Rollback() error {
	t.Status = StatusRollingBack
	if err := t.flush(); err != nil {
		t.log.Error("writing journal before rollback", "error", err, "tx_id", t.TxID)
	}

	var failed []string
	for _, entry := range slices.Backward(t.Entries) {
		if err := restoreEntry(entry); err != nil {
			t.log.Error("rolling back", "target", entry.Target, "error", err, "tx_id", t.TxID)
			failed = append(failed, entry.Target)
		}
	}

	t.Status = StatusRolledBack
	if err := t.flush(); err != nil {
		t.log.Error("writing journal after rollback", "error", err, "tx_id", t.TxID)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%w: %s", ErrRollbackIncomplete, strings.Join(failed, ", "))
	}
	return nil
}

// RollbackJournal replays a journal recorded by an earlier process — including one written by
// the Python implementation — without needing the plan that produced it.
func RollbackJournal(j *Journal) error {
	var failed []string
	for _, entry := range slices.Backward(j.Entries) {
		if err := restoreEntry(entry); err != nil {
			failed = append(failed, entry.Target)
		}
	}
	j.Status = StatusRolledBack
	if len(failed) > 0 {
		return fmt.Errorf("%w: %s", ErrRollbackIncomplete, strings.Join(failed, ", "))
	}
	return nil
}

// restoreEntry reverts one target to its pre-mutation state.
func restoreEntry(entry RollbackEntry) error {
	if entry.Op == OpCreate {
		// The target did not exist before, so whatever is there now must go.
		return removeAny(entry.Target)
	}
	if entry.SrcBackup == nil {
		// Nothing was backed up, so there is nothing to put back.
		return nil
	}
	backup := *entry.SrcBackup

	if err := removeAny(entry.Target); err != nil {
		return err
	}
	if err := restoreFromBackup(backup, entry.Target); err != nil {
		return err
	}
	if entry.Permissions != nil {
		return permissions.Enforce(entry.Target, *entry.Permissions, nil)
	}
	return nil
}

func restoreFromBackup(backup, target string) error {
	fi, err := os.Lstat(backup)
	if os.IsNotExist(err) {
		return fmt.Errorf("backup %s is missing", backup)
	}
	if err != nil {
		return err
	}

	if fi.IsDir() {
		if err := os.MkdirAll(target, fi.Mode().Perm()); err != nil {
			return err
		}
		return copyTree(backup, target)
	}

	content, err := os.ReadFile(backup)
	if err != nil {
		return err
	}
	// A backup holding SYMLINK:<target> stands in for a symlink and must be restored as one,
	// not as a text file with that content.
	if link, ok := strings.CutPrefix(string(content), symlinkBackupPrefix); ok {
		return os.Symlink(link, target)
	}
	return os.WriteFile(target, content, fi.Mode().Perm())
}
