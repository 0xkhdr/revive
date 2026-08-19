package recovery

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/transaction"
)

// snapshot creates a backup directory of a given age.
func (f *fixture) snapshot(txID string, ageDays int, size int) string {
	f.t.Helper()
	dir := f.m.Paths.BackupPathFor(txID)
	require.NoError(f.t, os.MkdirAll(dir, 0o700))
	require.NoError(f.t, os.WriteFile(filepath.Join(dir, "backup_0_file"), make([]byte, size), 0o600))

	when := now.AddDate(0, 0, -ageDays)
	require.NoError(f.t, os.Chtimes(dir, when, when))
	return dir
}

// journalFor marks a snapshot as belonging to an in-flight transaction.
func (f *fixture) journalFor(txID string) {
	f.t.Helper()
	require.NoError(f.t, transaction.WriteJournal(f.m.Paths.JournalPath(txID),
		&transaction.Journal{TxID: txID, Status: transaction.StatusExecuting, Timestamp: 100}))
}

func txIDs(snapshots []Snapshot) []string {
	out := make([]string, len(snapshots))
	for i, s := range snapshots {
		out[i] = s.TxID
	}
	return out
}

// Phase 11: max_age_days is enforced.
func TestPruneByAge(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.snapshot("fresh", 1, 10)
	f.snapshot("old", 45, 10)
	f.snapshot("ancient", 90, 10)

	deleted, err := f.m.Prune(Retention{MaxCount: 100, MaxAgeDays: 30}, false)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"old", "ancient"}, txIDs(deleted))

	require.DirExists(t, f.m.Paths.BackupPathFor("fresh"))
	require.NoDirExists(t, f.m.Paths.BackupPathFor("old"))
	require.NoDirExists(t, f.m.Paths.BackupPathFor("ancient"))
}

// Phase 11: max_count is enforced, evicting oldest first.
func TestPruneByCount(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for i, txID := range []string{"oldest", "middle", "newest"} {
		f.snapshot(txID, 3-i, 10)
	}

	deleted, err := f.m.Prune(Retention{MaxCount: 2, MaxAgeDays: 365}, false)
	require.NoError(t, err)
	require.Equal(t, []string{"oldest"}, txIDs(deleted), "the oldest goes first")
	require.DirExists(t, f.m.Paths.BackupPathFor("middle"))
	require.DirExists(t, f.m.Paths.BackupPathFor("newest"))
}

// Phase 11: both bounds apply and their candidate sets are deduplicated.
func TestBothBoundsDeduplicate(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// "ancient" is both too old and over the count; it must appear once.
	f.snapshot("ancient", 90, 10)
	f.snapshot("old", 45, 10)
	f.snapshot("fresh", 1, 10)

	deleted, err := f.m.Prune(Retention{MaxCount: 1, MaxAgeDays: 30}, true)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ancient", "old"}, txIDs(deleted))
	require.Len(t, deleted, 2, "a snapshot caught by both rules is listed once")
}

// Phase 11: pruning never touches a backup belonging to an incomplete journal, regardless of
// age. Deleting it would destroy that transaction's rollback ability permanently.
func TestPruneNeverTouchesAnActiveSnapshot(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.snapshot("in-flight", 999, 10)
	f.journalFor("in-flight")
	f.snapshot("finished", 999, 10)

	deleted, err := f.m.Prune(Retention{MaxCount: 0, MaxAgeDays: 1}, false)
	require.NoError(t, err)
	require.Equal(t, []string{"finished"}, txIDs(deleted))
	require.DirExists(t, f.m.Paths.BackupPathFor("in-flight"),
		"age is never a reason to delete a crashed transaction's only way back")
}

// An active snapshot is also excluded from the max_count arithmetic, so it cannot push a
// healthy snapshot over the limit.
func TestActiveSnapshotsDoNotCountTowardMaxCount(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.snapshot("in-flight", 10, 10)
	f.journalFor("in-flight")
	f.snapshot("keep", 5, 10)

	deleted, err := f.m.Prune(Retention{MaxCount: 1, MaxAgeDays: 365}, true)
	require.NoError(t, err)
	require.Empty(t, deleted)
}

// Phase 11: --dry-run lists candidates and deletes nothing.
func TestPruneDryRun(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.snapshot("old", 90, 10)

	deleted, err := f.m.Prune(Retention{MaxCount: 10, MaxAgeDays: 30}, true)
	require.NoError(t, err)
	require.Equal(t, []string{"old"}, txIDs(deleted))
	require.DirExists(t, f.m.Paths.BackupPathFor("old"))
}

func TestSnapshotsReportAgeAndSize(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.snapshot("recent", 2, 1024)

	snapshots, err := f.m.Snapshots()
	require.NoError(t, err)
	require.Len(t, snapshots, 1)
	require.Equal(t, 2, snapshots[0].AgeDays)
	require.Equal(t, int64(1024), snapshots[0].Size)
	require.False(t, snapshots[0].Active)
}

func TestSnapshotsAreOldestFirst(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.snapshot("b", 5, 1)
	f.snapshot("a", 50, 1)
	f.snapshot("c", 1, 1)

	snapshots, err := f.m.Snapshots()
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b", "c"}, txIDs(snapshots))
}

func TestSnapshotsWithNoBackupDirectory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	require.NoError(t, os.RemoveAll(f.m.Paths.BackupDir))

	snapshots, err := f.m.Snapshots()
	require.NoError(t, err)
	require.Empty(t, snapshots)

	deleted, err := f.m.Prune(Retention{MaxCount: 1, MaxAgeDays: 1}, false)
	require.NoError(t, err)
	require.Empty(t, deleted)
}

func TestStrayFilesInTheBackupDirectoryAreIgnored(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.m.Paths.BackupDir, "stray.txt"), []byte("x"), 0o600))
	f.snapshot("real", 1, 1)

	snapshots, err := f.m.Snapshots()
	require.NoError(t, err)
	require.Equal(t, []string{"real"}, txIDs(snapshots))
}

func TestZeroPolicyKeepsEverything(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.snapshot("old", 999, 1)

	deleted, err := f.m.Prune(Retention{}, false)
	require.NoError(t, err)
	require.Empty(t, deleted, "an unset bound is not a bound of zero")
	require.DirExists(t, f.m.Paths.BackupPathFor("old"))
}

func TestPruneRemovalFailureIsReported(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	f := newFixture(t)
	f.snapshot("old", 90, 1)
	require.NoError(t, os.Chmod(f.m.Paths.BackupDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(f.m.Paths.BackupDir, 0o700) })

	_, err := f.m.Prune(Retention{MaxCount: 10, MaxAgeDays: 30}, false)
	require.Error(t, err)
}

func TestManagerDefaults(t *testing.T) {
	t.Parallel()
	m := &Manager{Paths: newFixture(t).m.Paths}
	require.NotNil(t, m.log())
	require.WithinDuration(t, time.Now(), m.now(), time.Minute)
}
