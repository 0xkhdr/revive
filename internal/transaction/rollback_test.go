package transaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Phase 5: a failure in execute leaves the filesystem byte-identical to its pre-transaction
// state.
func TestExecuteFailureRollsBackCompletely(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	first := h.write(t, "first.conf", "original first", 0o640)
	second := h.write(t, "second.conf", "original second", 0o600)
	fresh := h.path("fresh.conf")

	before := snapshotDir(t, h.root)

	h.tx.Plan(Operation{Type: OpTypeCopy, Target: first, Source: SourceBytes{Data: []byte("new first")}, Permissions: "0640"})
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: fresh, Source: SourceBytes{Data: []byte("new fresh")}})
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: second, Source: SourceBytes{Data: []byte("new second")}, Permissions: "0600"})
	// A chmod on a target that does not exist fails in execute, after the earlier writes landed.
	h.tx.Plan(Operation{Type: OpTypeChmod, Target: h.path("nowhere.conf"), Permissions: "0644"})

	require.NoError(t, h.tx.Snapshot())
	err := h.tx.Execute(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "rolled back")
	require.NotErrorIs(t, err, ErrRollbackIncomplete)

	require.Equal(t, StatusRolledBack, h.tx.Status)
	require.NoFileExists(t, fresh, "a target created by the run must be removed again")
	require.Equal(t, before, snapshotDir(t, h.root), "the filesystem must be byte-identical")
}

// Phase 5: a failure in verify (mode mismatch) triggers rollback.
func TestVerifyFailureRollsBack(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := h.write(t, "file.conf", "original", 0o644)

	h.tx.Plan(Operation{Type: OpTypeCopy, Target: target, Source: SourceBytes{Data: []byte("new")}, Permissions: "0600"})
	require.NoError(t, h.tx.Snapshot())
	require.NoError(t, h.tx.Execute(context.Background()))

	// Something changes the mode out from under the transaction between execute and verify.
	require.NoError(t, os.Chmod(target, 0o666))
	err := h.tx.Verify()
	require.ErrorIs(t, err, ErrVerify)

	got, err2 := os.ReadFile(target)
	require.NoError(t, err2)
	require.Equal(t, "original", string(got))
	fi, err2 := os.Stat(target)
	require.NoError(t, err2)
	require.Equal(t, os.FileMode(0o644), fi.Mode().Perm(), "the pre-mutation mode is restored too")
}

// Phase 5: rollback replays in reverse order. A delete-then-symlink run must undo the symlink
// before restoring the original, or the restore writes through the link.
func TestRollbackReplaysInReverseOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := h.write(t, "zshrc", "original zshrc", 0o644)
	elsewhere := h.write(t, "elsewhere", "other file", 0o644)

	h.tx.Plan(Operation{Type: OpTypeDelete, Target: target})
	h.tx.Plan(Operation{Type: OpTypeSymlink, Target: target, Source: SourcePath{Path: elsewhere}})
	require.NoError(t, h.tx.Snapshot())
	require.NoError(t, h.tx.Execute(context.Background()))
	require.NoError(t, h.tx.Rollback())

	fi, err := os.Lstat(target)
	require.NoError(t, err)
	require.Zero(t, fi.Mode()&os.ModeSymlink, "the symlink must be gone, not left in place")
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "original zshrc", string(got))
	got, err = os.ReadFile(elsewhere)
	require.NoError(t, err)
	require.Equal(t, "other file", string(got), "nothing may be written through the link")
}

// Phase 5: a backed-up symlink restores as a symlink, not as a file holding SYMLINK:<target>.
func TestRollbackRestoresSymlinksAndDirectories(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dest := h.write(t, "dest", "destination", 0o644)
	link := h.path("link")
	require.NoError(t, os.Symlink(dest, link))
	tree := h.path("tree")
	require.NoError(t, os.MkdirAll(filepath.Join(tree, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tree, "nested", "f"), []byte("inner"), 0o644))

	// delete-then-write is what asset planning emits, and it is the only way a directory
	// target can be replaced by file content.
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: link, Source: SourceBytes{Data: []byte("clobbered")}})
	h.tx.Plan(Operation{Type: OpTypeDelete, Target: tree})
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: tree, Source: SourceBytes{Data: []byte("clobbered")}})
	require.NoError(t, h.tx.Snapshot())
	require.NoError(t, h.tx.Execute(context.Background()))
	require.NoError(t, h.tx.Rollback())

	fi, err := os.Lstat(link)
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&os.ModeSymlink, "it must come back as a link")
	got, err := os.Readlink(link)
	require.NoError(t, err)
	require.Equal(t, dest, got)

	fi, err = os.Stat(tree)
	require.NoError(t, err)
	require.True(t, fi.IsDir(), "it must come back as a directory")
	content, err := os.ReadFile(filepath.Join(tree, "nested", "f"))
	require.NoError(t, err)
	require.Equal(t, "inner", string(content))
}

// Phase 5: a rollback whose individual entry fails continues with the rest and reports
// ErrRollbackIncomplete naming the affected paths.
func TestPartialRollbackIsReported(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	broken := h.write(t, "broken.conf", "original broken", 0o644)
	fine := h.write(t, "fine.conf", "original fine", 0o644)

	h.tx.Plan(Operation{Type: OpTypeCopy, Target: broken, Source: SourceBytes{Data: []byte("new")}})
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: fine, Source: SourceBytes{Data: []byte("new")}})
	require.NoError(t, h.tx.Snapshot())
	require.NoError(t, h.tx.Execute(context.Background()))

	// Losing one backup is what a partial rollback looks like from the inside.
	require.NoError(t, os.Remove(*h.tx.Entries[0].SrcBackup))

	err := h.tx.Rollback()
	require.ErrorIs(t, err, ErrRollbackIncomplete)
	require.Contains(t, err.Error(), broken, "the message must name the affected path")
	require.NotContains(t, err.Error(), "fine.conf")

	got, err2 := os.ReadFile(fine)
	require.NoError(t, err2)
	require.Equal(t, "original fine", string(got), "the loop must not abort on the first failure")
}

func TestExecuteReportsAnIncompleteRollback(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := h.write(t, "file.conf", "original", 0o644)

	h.tx.Plan(Operation{Type: OpTypeCopy, Target: target, Source: SourceBytes{Data: []byte("new")}})
	h.tx.Plan(Operation{Type: OpTypeChmod, Target: h.path("nowhere"), Permissions: "0644"})
	require.NoError(t, h.tx.Snapshot())
	require.NoError(t, os.Remove(*h.tx.Entries[0].SrcBackup))

	err := h.tx.Execute(context.Background())
	require.ErrorIs(t, err, ErrRollbackIncomplete)
	require.Contains(t, err.Error(), "rollback was incomplete")
}

// A hook that ran and was then rolled back is recorded in the journal, and the files are still
// restored exactly.
func TestExecutedHooksAreRecordedAcrossARollback(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := h.write(t, "file.conf", "original", 0o644)
	h.tx.runner = func(_ context.Context, hook HookOp, _, _ string) error {
		if hook.Stage == "post" {
			return errors.New("hook exited 1")
		}
		return nil
	}

	h.tx.Plan(Operation{Type: OpTypeHook, Target: target, Hook: &HookOp{AssetID: "a", Stage: "pre", Command: []string{"true"}}})
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: target, Source: SourceBytes{Data: []byte("new")}})
	h.tx.Plan(Operation{Type: OpTypeHook, Target: target, Hook: &HookOp{AssetID: "a", Stage: "post", Command: []string{"true"}}})

	require.NoError(t, h.tx.Snapshot())
	err := h.tx.Execute(context.Background())
	require.ErrorIs(t, err, ErrHookFailed)

	require.Len(t, h.tx.ExecutedHooks, 2)
	require.Equal(t, HookOK, h.tx.ExecutedHooks[0].Result)
	require.Equal(t, HookFailed, h.tx.ExecutedHooks[1].Result,
		"a hook that started and failed still ran, and must be reported as such")

	got, err2 := os.ReadFile(target)
	require.NoError(t, err2)
	require.Equal(t, "original", string(got), "files are restored exactly even though a hook is not reversible")

	// The report has to survive into a fresh process, so it lives in the journal.
	j, err2 := LoadJournal(h.tx.JournalPath())
	require.NoError(t, err2)
	require.Len(t, j.ExecutedHooks, 2)
	require.Equal(t, "a", j.ExecutedHooks[0].AssetID)
}

func TestHookTimeoutIsRecorded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tx.runner = func(ctx context.Context, _ HookOp, _, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	h.tx.Plan(Operation{Type: OpTypeHook, Target: h.path("x"),
		Hook: &HookOp{AssetID: "slow", Stage: "pre", Command: []string{"sleep"}}})
	require.NoError(t, h.tx.Snapshot())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := h.tx.Execute(ctx)
	require.ErrorIs(t, err, ErrHookFailed)
	require.Equal(t, HookTimedOut, h.tx.ExecutedHooks[0].Result)
}

// snapshotDir records every path under root with its mode and content, so a test can assert
// that a failed transaction left the tree byte-identical.
func snapshotDir(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			out[rel] = "symlink:" + link
			return err
		case d.IsDir():
			out[rel] = "dir:" + fi.Mode().Perm().String()
			return nil
		default:
			content, err := os.ReadFile(path)
			out[rel] = fi.Mode().Perm().String() + ":" + string(content)
			return err
		}
	}))
	return out
}
