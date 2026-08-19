package transaction

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/paths"
)

// A per-asset hook runs without a shell and receives the four RV_* variables.
func TestExecHookPassesTheEnvironment(t *testing.T) {
	t.Parallel()
	out := filepath.Join(t.TempDir(), "env.txt")
	hook := HookOp{AssetID: "zshrc", Stage: "post", Command: []string{
		"sh", "-c", "printf '%s|%s|%s|%s' \"$RV_ASSET_ID\" \"$RV_ASSET_TARGET\" \"$RV_TX_ID\" \"$RV_HOOK_STAGE\" > " + out,
	}}
	require.NoError(t, ExecHook(context.Background(), hook, "/target/path", "tx-123"))

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	require.Equal(t, "zshrc|/target/path|tx-123|post", string(got))
}

func TestExecHookReportsANonZeroExit(t *testing.T) {
	t.Parallel()
	err := ExecHook(context.Background(), HookOp{AssetID: "a", Stage: "pre",
		Command: []string{"sh", "-c", "echo boom >&2; exit 3"}}, "/t", "tx")
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom", "the hook's own output is the useful part of the error")
}

func TestExecHookIsCancellable(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := ExecHook(ctx, HookOp{AssetID: "a", Stage: "pre", Command: []string{"sleep", "30"}}, "/t", "tx")
	require.Error(t, err, "Ctrl-C must reach a running hook")
}

// The whole hook path end to end, through the transaction's default runner.
func TestHooksRunInTheExecutePhase(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "targets")
	require.NoError(t, os.MkdirAll(root, 0o755))
	marker := filepath.Join(root, "marker")
	target := filepath.Join(root, "file.conf")

	tx := New(Options{TxID: "tx", Paths: paths.New(filepath.Join(base, "rv-home"))})
	tx.Plan(Operation{Type: OpTypeHook, Target: target,
		Hook: &HookOp{AssetID: "a", Stage: "pre", Command: []string{"touch", marker}}})
	tx.Plan(Operation{Type: OpTypeCopy, Target: target, Source: SourceBytes{Data: []byte("x")}})

	require.NoError(t, tx.Validate())
	require.NoError(t, tx.Snapshot())
	require.NoFileExists(t, marker, "planning and snapshotting must run no hook")

	require.NoError(t, tx.Execute(context.Background()))
	require.FileExists(t, marker)
	require.Len(t, tx.ExecutedHooks, 1)
	require.Equal(t, HookOK, tx.ExecutedHooks[0].Result)
	require.NoError(t, tx.Verify())
}

func TestExecuteErrorPaths(t *testing.T) {
	t.Parallel()
	for name, plan := range map[string]Operation{
		"unknown type":         {Type: "teleport", Target: "x"},
		"symlink without path": {Type: OpTypeSymlink, Target: "x", Source: SourceBytes{Data: []byte("x")}},
		"copy from a missing source": {Type: OpTypeCopy, Target: "x",
			Source: SourcePath{Path: "/no/such/source"}},
		"copy with a bad mode": {Type: OpTypeCopy, Target: "x",
			Source: SourceBytes{Data: []byte("x")}, Permissions: "644"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			tx := New(Options{Paths: paths.New(filepath.Join(base, "rv-home"))})
			op := plan
			op.Target = filepath.Join(base, "targets", "x")
			tx.Plan(op)
			require.NoError(t, tx.Snapshot())
			require.Error(t, tx.Execute(context.Background()))
		})
	}
}

func TestCopyWithNoSourceWritesAnEmptyFile(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := filepath.Join(base, "targets", "empty")
	tx := New(Options{Paths: paths.New(filepath.Join(base, "rv-home"))})
	tx.Plan(Operation{Type: OpTypeCopy, Target: target, Permissions: "0644"})

	require.NoError(t, tx.Snapshot())
	require.NoError(t, tx.Execute(context.Background()))
	require.NoError(t, tx.Verify())

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestExecuteHonorsACancelledContext(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	tx := New(Options{Paths: paths.New(filepath.Join(base, "rv-home"))})
	tx.Plan(Operation{Type: OpTypeCopy, Target: filepath.Join(base, "targets", "x"),
		Source: SourceBytes{Data: []byte("x")}})
	require.NoError(t, tx.Snapshot())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, tx.Execute(ctx))
	require.NoFileExists(t, filepath.Join(base, "targets", "x"))
}

func TestChmodOperationAppliesAMode(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := filepath.Join(base, "targets", "file")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	tx := New(Options{Paths: paths.New(filepath.Join(base, "rv-home"))})
	tx.Plan(Operation{Type: OpTypeChmod, Target: target, Permissions: "0600"})
	require.NoError(t, tx.Validate())
	require.NoError(t, tx.Snapshot())
	require.NoError(t, tx.Execute(context.Background()))
	require.NoError(t, tx.Verify())

	fi, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

func TestSnapshotFailsWhenTheJournalDirectoryCannotBeCreated(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	base := t.TempDir()
	require.NoError(t, os.Chmod(base, 0o500))
	t.Cleanup(func() { _ = os.Chmod(base, 0o700) })

	tx := New(Options{Paths: paths.New(filepath.Join(base, "rv-home"))})
	require.Error(t, tx.Snapshot())
}

func TestSourceTypesSatisfyTheSumType(t *testing.T) {
	t.Parallel()
	var sources = []Source{SourcePath{Path: "/x"}, SourceBytes{Data: []byte("y")}}
	require.Len(t, sources, 2)
	for _, s := range sources {
		s.isSource()
	}
	require.Empty(t, hookAsset(Operation{Type: OpTypeCopy}))
	require.Equal(t, "a", hookAsset(Operation{Type: OpTypeHook, Hook: &HookOp{AssetID: "a"}}))
}

func TestRollbackJournalReportsMissingBackups(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "gone")
	target := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))

	j := &Journal{TxID: "t", Status: StatusExecuting, Entries: []RollbackEntry{
		{Op: OpModify, Target: target, SrcBackup: &missing},
	}}
	err := RollbackJournal(j)
	require.ErrorIs(t, err, ErrRollbackIncomplete)
	require.Contains(t, err.Error(), target)
}

func TestRestoreEntryWithoutABackupIsANoOp(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.WriteFile(target, []byte("untouched"), 0o600))

	require.NoError(t, restoreEntry(RollbackEntry{Op: OpModify, Target: target}))
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "untouched", string(got), "with nothing backed up there is nothing to put back")
}

func TestHashFileErrors(t *testing.T) {
	t.Parallel()
	_, err := hashFile(filepath.Join(t.TempDir(), "absent"))
	require.Error(t, err)
}

func TestSyncDirErrors(t *testing.T) {
	t.Parallel()
	require.Error(t, syncDir(filepath.Join(t.TempDir(), "absent")))
}

func TestBackupOfANamedPipeIsSkipped(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	fifo := filepath.Join(base, "targets", "fifo")
	require.NoError(t, os.MkdirAll(filepath.Dir(fifo), 0o755))
	if err := mkfifo(fifo); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	tx := New(Options{Paths: paths.New(filepath.Join(base, "rv-home"))})
	tx.Plan(Operation{Type: OpTypeDelete, Target: fifo})
	require.NoError(t, tx.Snapshot(), "a fifo is not configuration and has no meaningful backup")
	require.Len(t, tx.Entries, 1)
}

func TestCleanupToleratesAMissingBackupDir(t *testing.T) {
	t.Parallel()
	tx := New(Options{Paths: paths.New(t.TempDir())})
	tx.Status = StatusCommitted
	require.NotPanics(t, tx.Cleanup)
}

func TestJournalPathsAreExposed(t *testing.T) {
	t.Parallel()
	cfg := paths.New(t.TempDir())
	tx := New(Options{TxID: "abc", Paths: cfg})
	require.Equal(t, cfg.JournalPath("abc"), tx.JournalPath())
	require.Equal(t, cfg.BackupPathFor("abc"), tx.BackupDir())
	require.True(t, strings.HasSuffix(tx.JournalPath(), "abc.json"))
}

func TestAtomicCopyDirFailsOnAMissingSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.Error(t, atomicCopyDir(filepath.Join(dir, "absent"), filepath.Join(dir, "target")))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "a failed directory copy must leave no temp directory")
}

func TestAtomicCopyDirFailsWhenTheParentIsUnwritable(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	root := t.TempDir()
	src := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	locked := filepath.Join(root, "locked")
	require.NoError(t, os.MkdirAll(locked, 0o500))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	require.Error(t, atomicCopyDir(src, filepath.Join(locked, "target")))
}

func TestCopyTreeFailsOnAnUnreadableSubdirectory(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read bit")
	}
	src, dst := t.TempDir(), t.TempDir()
	locked := filepath.Join(src, "locked")
	require.NoError(t, os.MkdirAll(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	require.Error(t, copyTree(src, dst))
}

func TestCopyFileFailsOnAMissingSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.Error(t, copyFile(filepath.Join(dir, "absent"), filepath.Join(dir, "out"), 0o644))
}

// A journal that cannot be written is a hard error: without it there is no recovery path.
func TestFlushFailureAbortsExecute(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	base := t.TempDir()
	tx := New(Options{TxID: "tx", Paths: paths.New(filepath.Join(base, "rv-home"))})
	tx.Plan(Operation{Type: OpTypeCopy, Target: filepath.Join(base, "targets", "x"),
		Source: SourceBytes{Data: []byte("x")}})
	require.NoError(t, tx.Snapshot())

	journalDir := filepath.Dir(tx.JournalPath())
	require.NoError(t, os.Chmod(journalDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(journalDir, 0o700) })

	require.Error(t, tx.Execute(context.Background()))
	require.Error(t, tx.Verify())
	require.Error(t, tx.Commit())
}

func TestWriteJournalFailsOnAnUnwritablePath(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	require.Error(t, WriteJournal(filepath.Join(dir, "j.json"), &Journal{TxID: "t"}))
}

func TestCleanupLogsRatherThanFailing(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	base := t.TempDir()
	tx := New(Options{TxID: "tx", Paths: paths.New(filepath.Join(base, "rv-home"))})
	require.NoError(t, tx.Snapshot())
	require.NoError(t, tx.Commit())

	journalDir := filepath.Dir(tx.JournalPath())
	require.NoError(t, os.Chmod(journalDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(journalDir, 0o700) })

	// A leftover journal is harmless and pruning collects it, so cleanup never escalates.
	require.NotPanics(t, tx.Cleanup)
	require.FileExists(t, tx.JournalPath())
}
