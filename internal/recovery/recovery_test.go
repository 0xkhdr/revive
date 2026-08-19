package recovery

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/transaction"
)

var now = time.Unix(1700000000, 0)

type fixture struct {
	t       *testing.T
	m       *Manager
	targets string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		t:       t,
		targets: filepath.Join(base, "targets"),
		m: &Manager{
			Paths: paths.New(filepath.Join(base, "rv-home")),
			Now:   func() time.Time { return now },
		},
	}
	require.NoError(t, os.MkdirAll(f.targets, 0o755))
	require.NoError(t, os.MkdirAll(f.m.Paths.JournalDir, 0o755))
	require.NoError(t, os.MkdirAll(f.m.Paths.BackupDir, 0o755))
	return f
}

// crash runs a transaction and abandons it mid-flight, which is what a SIGKILL leaves behind.
func (f *fixture) crash(txID string, status string) (target, original string) {
	f.t.Helper()
	target = filepath.Join(f.targets, txID+".conf")
	original = "original " + txID + "\n"
	require.NoError(f.t, os.WriteFile(target, []byte(original), 0o644))

	tx := transaction.New(transaction.Options{TxID: txID, Paths: f.m.Paths, Now: f.m.Now})
	tx.Plan(transaction.Operation{
		Type:   transaction.OpTypeCopy,
		Target: target,
		Source: transaction.SourceBytes{Data: []byte("half-applied\n")},
	})
	require.NoError(f.t, tx.Validate())
	require.NoError(f.t, tx.Snapshot())
	require.NoError(f.t, tx.Execute(f.t.Context()))

	// No commit: the journal is left mid-flight, exactly like a killed process.
	tx.Status = status
	require.NoError(f.t, transaction.WriteJournal(tx.JournalPath(), tx.Journal()))
	return target, original
}

// Phase 11: a simulated crash leaves a journal that rv recover finds and rolls back to the exact
// pre-state.
func TestRecoverRollsBackToThePreState(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	target, original := f.crash("crashed", transaction.StatusExecuting)

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "half-applied\n", string(got), "the crash left the file mutated")

	pending, err := f.m.Scan()
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "crashed", pending[0].TxID)
	require.Equal(t, transaction.StatusExecuting, pending[0].Status)

	require.NoError(t, f.m.Rollback(pending[0]))

	got, err = os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, original, string(got))
	require.NoFileExists(t, pending[0].Path, "a resolved journal is removed")
	require.NoDirExists(t, f.m.Paths.BackupPathFor("crashed"))
}

// Phase 11: discard removes the journal and backups without restoring files.
func TestDiscardLeavesTheFilesAlone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	target, _ := f.crash("crashed", transaction.StatusExecuting)

	pending, err := f.m.Scan()
	require.NoError(t, err)
	require.NoError(t, f.m.Discard(pending[0]))

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "half-applied\n", string(got), "discard is for a user who fixed things by hand")
	require.NoFileExists(t, pending[0].Path)
	require.NoDirExists(t, f.m.Paths.BackupPathFor("crashed"))
}

// Scan keeps only interrupted transactions, newest first.
func TestScanFiltersAndSorts(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	write := func(txID, status string, ts float64) {
		j := &transaction.Journal{TxID: txID, Status: status, Timestamp: ts}
		require.NoError(t, transaction.WriteJournal(f.m.Paths.JournalPath(txID), j))
	}
	write("committed", transaction.StatusCommitted, 100)
	write("rolled-back", transaction.StatusRolledBack, 200)
	write("older", transaction.StatusExecuting, 300)
	write("newer", transaction.StatusVerifying, 400)
	write("pending", transaction.StatusPending, 350)

	pending, err := f.m.Scan()
	require.NoError(t, err)
	require.Equal(t, []string{"newer", "pending", "older"},
		[]string{pending[0].TxID, pending[1].TxID, pending[2].TxID},
		"finished transactions are excluded and the rest are newest first")
}

// A journal that will not parse is a warning and a skip: one corrupt file must not hide the
// others, which is exactly when the user most needs the list.
func TestUnreadableJournalIsSkipped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.m.Paths.JournalDir, "broken.json"), []byte("{not json"), 0o600))
	require.NoError(t, transaction.WriteJournal(f.m.Paths.JournalPath("good"),
		&transaction.Journal{TxID: "good", Status: transaction.StatusExecuting}))
	// A non-journal file in the directory is ignored entirely.
	require.NoError(t, os.WriteFile(filepath.Join(f.m.Paths.JournalDir, "notes.txt"), []byte("x"), 0o600))

	pending, err := f.m.Scan()
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "good", pending[0].TxID)
}

func TestScanWithNoJournalDirectory(t *testing.T) {
	t.Parallel()
	m := &Manager{Paths: paths.New(t.TempDir())}
	pending, err := m.Scan()
	require.NoError(t, err)
	require.Empty(t, pending)
	require.NoError(t, m.EnsureClean())
}

// Phase 11: a restore refuses to start with an unrecovered journal. [DIVERGE]
func TestEnsureClean(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	require.NoError(t, f.m.EnsureClean())

	f.crash("crashed", transaction.StatusExecuting)
	err := f.m.EnsureClean()
	require.ErrorIs(t, err, ErrIncomplete)
	require.Contains(t, err.Error(), "crashed", "the error must name the transaction")
	require.Contains(t, err.Error(), "rv recover", "and how to resolve it")
}

// A partial rollback still cleans up: leaving the journal behind would block every future run.
func TestPartialRollbackStillClearsTheJournal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.crash("crashed", transaction.StatusExecuting)

	pending, err := f.m.Scan()
	require.NoError(t, err)
	require.NoError(t, os.RemoveAll(f.m.Paths.BackupPathFor("crashed")))

	err = f.m.Rollback(pending[0])
	require.ErrorIs(t, err, transaction.ErrRollbackIncomplete)
	require.NoFileExists(t, pending[0].Path)
}

// The executed-hook report survives into a fresh process, days later.
func TestExecutedHooksSurviveInTheJournal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	j := &transaction.Journal{
		TxID: "hooked", Status: transaction.StatusExecuting, Timestamp: 100,
		ExecutedHooks: []transaction.ExecutedHook{
			{AssetID: "nginx", Stage: "pre", Command: []string{"nginx", "-t"}, Result: transaction.HookOK},
		},
	}
	require.NoError(t, transaction.WriteJournal(f.m.Paths.JournalPath("hooked"), j))

	pending, err := f.m.Scan()
	require.NoError(t, err)
	require.Len(t, pending[0].ExecutedHooks(), 1)
	require.Equal(t, "nginx", pending[0].ExecutedHooks()[0].AssetID)

	require.Nil(t, Incomplete{}.ExecutedHooks())
}
