// Package recovery deals with transactions that ended badly: journals left behind by an
// interrupted restore, and the backup snapshots they own.
package recovery

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/transaction"
)

// ErrIncomplete is returned when an unrecovered journal blocks an operation.
//
// Restoring on top of a partial transaction means the new snapshot captures the *broken* state
// as the pre-state, which quietly destroys the ability to get back to the original. Refusing is
// the only safe answer. [DIVERGE] — the Python implementation only recovered on request.
var ErrIncomplete = errors.New("an interrupted transaction has not been recovered")

// Incomplete describes one journal that needs attention.
type Incomplete struct {
	TxID      string
	Path      string
	Status    string
	Timestamp time.Time
	Journal   *transaction.Journal
}

// Manager scans and resolves interrupted transactions.
type Manager struct {
	Paths paths.Config
	Log   *slog.Logger
	Now   func() time.Time
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Scan lists interrupted transactions, newest first.
//
// A journal that will not parse is a warning and a skip: one corrupt file must not hide the
// others, which is exactly when the user most needs the list.
func (m *Manager) Scan() ([]Incomplete, error) {
	entries, err := os.ReadDir(m.Paths.JournalDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading journal directory: %w", err)
	}

	var out []Incomplete
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(m.Paths.JournalDir, entry.Name())

		j, err := transaction.LoadJournal(path)
		if err != nil {
			m.log().Warn("skipping unreadable journal", "path", path, "error", err)
			continue
		}
		if j.Complete() {
			continue
		}
		out = append(out, Incomplete{
			TxID:      j.TxID,
			Path:      path,
			Status:    j.Status,
			Timestamp: time.Unix(0, int64(j.Timestamp*float64(time.Second))),
			Journal:   j,
		})
	}

	slices.SortFunc(out, func(a, b Incomplete) int { return b.Timestamp.Compare(a.Timestamp) })
	return out, nil
}

// EnsureClean reports an error when any interrupted transaction remains, naming `rv recover`.
func (m *Manager) EnsureClean() error {
	pending, err := m.Scan()
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %d journal(s), newest %s (%s); run `rv recover` to roll back or discard",
		ErrIncomplete, len(pending), pending[0].TxID, pending[0].Status)
}

// Rollback restores the filesystem from a journal, then removes the journal and its backups.
//
// A partial rollback still cleans up: the entries that could not be restored are named in the
// returned error, and leaving the journal behind would only block every future run.
func (m *Manager) Rollback(inc Incomplete) error {
	rollbackErr := transaction.RollbackJournal(inc.Journal)
	m.cleanup(inc)
	if rollbackErr != nil {
		return fmt.Errorf("rolling back %s: %w", inc.TxID, rollbackErr)
	}
	return nil
}

// Discard removes a journal and its backups without restoring anything, for the user who has
// already fixed things by hand and just wants the warning to stop.
func (m *Manager) Discard(inc Incomplete) error {
	m.cleanup(inc)
	return nil
}

func (m *Manager) cleanup(inc Incomplete) {
	if err := os.Remove(inc.Path); err != nil && !os.IsNotExist(err) {
		m.log().Warn("removing journal", "path", inc.Path, "error", err)
	}
	backups := m.Paths.BackupPathFor(inc.TxID)
	if err := os.RemoveAll(backups); err != nil {
		m.log().Warn("removing backup snapshot", "path", backups, "error", err)
	}
}

// ExecutedHooks returns the hooks a journal records as having run. Rollback restores files but
// cannot un-run a hook, and this is the user's record of what survived — including days later,
// in a different process.
func (inc Incomplete) ExecutedHooks() []transaction.ExecutedHook {
	if inc.Journal == nil {
		return nil
	}
	return inc.Journal.ExecutedHooks
}
