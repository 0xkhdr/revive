package transaction

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/paths"
)

// harness roots a transaction entirely inside t.TempDir(), so tests need no shared state and
// run in parallel.
type harness struct {
	tx    *Transaction
	root  string
	paths paths.Config
	hooks *[]HookOp
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	base := t.TempDir()
	// The config layout lives outside the target tree, so a test can assert on the target tree
	// alone without journals and backups showing up inside it.
	root := filepath.Join(base, "targets")
	require.NoError(t, os.MkdirAll(root, 0o755))
	cfg := paths.New(filepath.Join(base, "rv-home"))
	ran := &[]HookOp{}
	tx := New(Options{
		TxID:  "test-tx",
		Paths: cfg,
		Now:   func() time.Time { return time.Unix(1700000000, 0) },
		Runner: func(_ context.Context, hook HookOp, _, _ string) error {
			*ran = append(*ran, hook)
			return nil
		},
	})
	return &harness{tx: tx, root: root, paths: cfg, hooks: ran}
}

func (h *harness) path(name string) string { return filepath.Join(h.root, name) }

func (h *harness) write(t *testing.T, name, content string, mode fs.FileMode) string {
	t.Helper()
	p := h.path(name)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), mode))
	return p
}

func (h *harness) run(t *testing.T) error {
	t.Helper()
	if err := h.tx.Validate(); err != nil {
		return err
	}
	if err := h.tx.Snapshot(); err != nil {
		return err
	}
	if err := h.tx.Execute(context.Background()); err != nil {
		return err
	}
	if err := h.tx.Verify(); err != nil {
		return err
	}
	return h.tx.Commit()
}

// Phase 1: Plan does no I/O and no side effects beyond making the target absolute.
func TestPlanIsSideEffectFree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: "relative/file", Source: SourceBytes{Data: []byte("x")}})

	require.Len(t, h.tx.Planned, 1)
	require.True(t, filepath.IsAbs(h.tx.Planned[0].Target))
	require.NoFileExists(t, h.tx.JournalPath(), "planning must not write a journal")
	require.NoDirExists(t, h.tx.BackupDir())
}

// Phase 2: unknown operation types are rejected before anything is backed up.
func TestValidateRejectsUnknownOperations(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tx.Plan(Operation{Type: "teleport", Target: h.path("x")})
	require.ErrorIs(t, h.tx.Validate(), ErrUnknownOperation)
	require.NoDirExists(t, h.tx.BackupDir(), "a failed validation must leave nothing behind")
}

func TestValidateRejectsUnwritableTargets(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := h.write(t, "readonly.conf", "x", 0o400)
	require.NoError(t, os.Chmod(target, 0o400))
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}

	h.tx.Plan(Operation{Type: OpTypeCopy, Target: target, Source: SourceBytes{Data: []byte("y")}})
	require.ErrorIs(t, h.tx.Validate(), ErrNotWritable)
}

func TestValidateRejectsUnwritableParents(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	h := newHarness(t)
	locked := h.path("locked")
	require.NoError(t, os.MkdirAll(locked, 0o500))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	h.tx.Plan(Operation{Type: OpTypeCopy, Target: filepath.Join(locked, "new"), Source: SourceBytes{}})
	require.ErrorIs(t, h.tx.Validate(), ErrNotWritable)
}

func TestValidateRejectsBadHooks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tx.Plan(Operation{Type: OpTypeHook, Target: h.path("x"),
		Hook: &HookOp{AssetID: "a", Stage: "pre", Command: []string{"rv-no-such-binary-xyz"}}})
	err := h.tx.Validate()
	require.ErrorIs(t, err, ErrHookFailed)
	require.Contains(t, err.Error(), "rv-no-such-binary-xyz")

	h = newHarness(t)
	h.tx.Plan(Operation{Type: OpTypeHook, Target: h.path("x"), Hook: &HookOp{AssetID: "a", Stage: "pre"}})
	require.ErrorIs(t, h.tx.Validate(), ErrHookFailed)
}

// Phase 3: a snapshot records mode and checksum for an existing target, and creates no backup
// for one that does not exist.
func TestSnapshotRecordsPreState(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	existing := h.write(t, "existing.conf", "before", 0o640)
	fresh := h.path("fresh.conf")

	h.tx.Plan(Operation{Type: OpTypeCopy, Target: existing, Source: SourceBytes{Data: []byte("after")}})
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: fresh, Source: SourceBytes{Data: []byte("new")}})
	require.NoError(t, h.tx.Snapshot())

	require.Len(t, h.tx.Entries, 2)
	require.Equal(t, OpModify, h.tx.Entries[0].Op)
	require.NotNil(t, h.tx.Entries[0].SrcBackup)
	require.NotNil(t, h.tx.Entries[0].Checksum)
	require.Equal(t, "0o640", *h.tx.Entries[0].Permissions)

	require.Equal(t, OpCreate, h.tx.Entries[1].Op)
	require.Nil(t, h.tx.Entries[1].SrcBackup, "a target that did not exist has nothing to back up")
	require.Nil(t, h.tx.Entries[1].Checksum)

	require.FileExists(t, h.tx.JournalPath(), "the journal must be on disk before any mutation")
}

// Phase 3: hook operations contribute no rollback entry — there is no pre-state to capture.
func TestSnapshotSkipsHooks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := h.write(t, "file.conf", "x", 0o644)
	h.tx.Plan(Operation{Type: OpTypeHook, Target: target, Hook: &HookOp{AssetID: "a", Stage: "pre", Command: []string{"true"}}})
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: target, Source: SourceBytes{Data: []byte("y")}})

	require.NoError(t, h.tx.Snapshot())
	require.Len(t, h.tx.Entries, 1)
	require.Equal(t, target, h.tx.Entries[0].Target)
}

// Phase 3: a backed-up symlink is stored as SYMLINK:<target>, and a directory as a tree.
func TestSnapshotBacksUpSymlinksAndDirectories(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dest := h.write(t, "dest", "x", 0o644)
	link := h.path("link")
	require.NoError(t, os.Symlink(dest, link))
	tree := h.path("tree")
	require.NoError(t, os.MkdirAll(filepath.Join(tree, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tree, "nested", "f"), []byte("inner"), 0o644))

	h.tx.Plan(Operation{Type: OpTypeDelete, Target: link})
	h.tx.Plan(Operation{Type: OpTypeDelete, Target: tree})
	require.NoError(t, h.tx.Snapshot())

	backup, err := os.ReadFile(*h.tx.Entries[0].SrcBackup)
	require.NoError(t, err)
	require.Equal(t, symlinkBackupPrefix+dest, string(backup))
	require.Nil(t, h.tx.Entries[0].Checksum, "a symlink is not hashed")

	require.FileExists(t, filepath.Join(*h.tx.Entries[1].SrcBackup, "nested", "f"))
	require.Equal(t, OpDelete, h.tx.Entries[0].Op)
}

// Phase 4 and 5: a full run applies every operation type and verifies it.
func TestFullTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	source := h.write(t, "repo/zshrc", "export EDITOR=vim\n", 0o644)
	sourceDir := h.path("repo/tree")
	require.NoError(t, os.MkdirAll(sourceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sourceDir, "inner"), []byte("tree"), 0o644))

	link := h.path("home/.zshrc")
	copied := h.path("home/.gitconfig")
	secret := h.path("home/.env")
	treeTarget := h.path("home/tree")

	h.tx.Plan(Operation{Type: OpTypeSymlink, Target: link, Source: SourcePath{Path: source}})
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: copied, Source: SourcePath{Path: source}, Permissions: "0644"})
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: secret, Source: SourceBytes{Data: []byte("TOKEN=x")}, Permissions: "0600"})
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: treeTarget, Source: SourcePath{Path: sourceDir}})
	require.NoError(t, h.run(t))

	got, err := os.Readlink(link)
	require.NoError(t, err)
	require.Equal(t, source, got)

	fi, err := os.Stat(secret)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	require.FileExists(t, filepath.Join(treeTarget, "inner"))

	require.Equal(t, StatusCommitted, h.tx.Status)
}

// Phase 7: cleanup removes the backups and, once committed, the journal.
func TestCleanup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: h.path("f"), Source: SourceBytes{Data: []byte("x")}})
	require.NoError(t, h.run(t))

	h.tx.Cleanup()
	require.NoDirExists(t, h.tx.BackupDir())
	require.NoFileExists(t, h.tx.JournalPath())
}

func TestCleanupKeepsTheJournalWhenNotCommitted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: h.path("f"), Source: SourceBytes{Data: []byte("x")}})
	require.NoError(t, h.tx.Snapshot())

	h.tx.Cleanup()
	require.FileExists(t, h.tx.JournalPath(), "an uncommitted journal is what rv recover needs")
}

// Phase 5: a delete verifies as gone, unless a later operation recreates the target.
func TestVerifyAllowsDeleteThenWrite(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := h.write(t, "file.conf", "old", 0o644)
	h.tx.Plan(Operation{Type: OpTypeDelete, Target: target})
	h.tx.Plan(Operation{Type: OpTypeCopy, Target: target, Source: SourceBytes{Data: []byte("new")}})

	require.NoError(t, h.run(t))
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "new", string(got))
}

func TestVerifyFailsOnALingeringDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := h.write(t, "file.conf", "old", 0o644)
	h.tx.Plan(Operation{Type: OpTypeDelete, Target: target})
	require.NoError(t, h.tx.Validate())
	require.NoError(t, h.tx.Snapshot())
	require.NoError(t, h.tx.Execute(context.Background()))

	// Something outside the transaction puts the file back.
	require.NoError(t, os.WriteFile(target, []byte("resurrected"), 0o644))
	require.ErrorIs(t, h.tx.Verify(), ErrVerify)
}

// Phase 5: a dangling symlink still counts as existing — rv created the link, not its
// destination.
func TestVerifyAcceptsADanglingSymlink(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.tx.Plan(Operation{Type: OpTypeSymlink, Target: h.path("link"), Source: SourcePath{Path: h.path("nowhere")}})
	require.NoError(t, h.run(t))
}

func TestJournalTimestampIsUnixSeconds(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	require.NoError(t, h.tx.Snapshot())

	raw, err := os.ReadFile(h.tx.JournalPath())
	require.NoError(t, err)
	var probe struct {
		TxID      string  `json:"tx_id"`
		Timestamp float64 `json:"timestamp"`
		Status    string  `json:"status"`
	}
	require.NoError(t, json.Unmarshal(raw, &probe))
	require.Equal(t, "test-tx", probe.TxID)
	require.InDelta(t, 1.7e9, probe.Timestamp, 1e6)
	require.Equal(t, StatusPending, probe.Status)
}

func TestNewFillsInDefaults(t *testing.T) {
	t.Parallel()
	tx := New(Options{Paths: paths.New(t.TempDir())})
	require.NotEmpty(t, tx.TxID, "a transaction ID is generated when none is given")
	require.NotNil(t, tx.RenderedChecksums)
	require.Equal(t, StatusPending, tx.Status)
}
