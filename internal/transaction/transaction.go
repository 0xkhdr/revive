package transaction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/permissions"
)

// Sentinel errors.
var (
	// ErrUnknownOperation is returned by Validate for an operation type it does not know.
	ErrUnknownOperation = errors.New("unsupported operation type")
	// ErrNotWritable is returned by Validate when a target or its parent cannot be written.
	ErrNotWritable = errors.New("target is not writable")
	// ErrVerify is returned when post-apply verification finds a target missing or wrong.
	ErrVerify = errors.New("post-apply verification failed")
	// ErrHookFailed is returned when a hook exits non-zero or times out.
	ErrHookFailed = errors.New("hook failed")
)

// HookTimeout bounds a per-asset hook.
const HookTimeout = 30 * time.Second

// HookRunner executes a hook's argv without a shell. It is injectable so tests need no real
// subprocesses, and so Ctrl-C cancels a running hook through the context.
type HookRunner func(ctx context.Context, hook HookOp, target, txID string) error

// Options configures a transaction. Every field has a working default.
type Options struct {
	TxID   string
	Paths  paths.Config
	Now    func() time.Time
	Log    *slog.Logger
	Runner HookRunner
}

// Transaction is the filesystem-mutation state machine for one restore run.
type Transaction struct {
	TxID              string
	Timestamp         time.Time
	Status            string
	Entries           []RollbackEntry
	Planned           []Operation
	RenderedChecksums map[string]string
	ExecutedHooks     []ExecutedHook

	journalPath string
	backupDir   string
	now         func() time.Time
	log         *slog.Logger
	runner      HookRunner
}

// New starts a transaction. Nothing touches the filesystem until Snapshot.
func New(opts Options) *Transaction {
	if opts.TxID == "" {
		opts.TxID = uuid.NewString()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	if opts.Runner == nil {
		opts.Runner = ExecHook
	}
	return &Transaction{
		TxID:              opts.TxID,
		Timestamp:         opts.Now(),
		Status:            StatusPending,
		RenderedChecksums: map[string]string{},
		journalPath:       opts.Paths.JournalPath(opts.TxID),
		backupDir:         opts.Paths.BackupPathFor(opts.TxID),
		now:               opts.Now,
		log:               opts.Log,
		runner:            opts.Runner,
	}
}

// JournalPath returns where this transaction's journal is written.
func (t *Transaction) JournalPath() string { return t.journalPath }

// BackupDir returns where this transaction's pre-mutation snapshots live.
func (t *Transaction) BackupDir() string { return t.backupDir }

// Journal renders the current state as the on-disk journal structure.
func (t *Transaction) Journal() *Journal {
	return &Journal{
		TxID:          t.TxID,
		Timestamp:     float64(t.Timestamp.UnixNano()) / float64(time.Second),
		Status:        t.Status,
		Entries:       t.Entries,
		ExecutedHooks: t.ExecutedHooks,
	}
}

// Plan appends an operation. No I/O and no side effects beyond making the target absolute —
// that is what makes planning safe to parallelize, safe under --dry-run, and safe to abandon.
func (t *Transaction) Plan(op Operation) {
	if abs, err := filepath.Abs(op.Target); err == nil {
		op.Target = abs
	}
	t.Planned = append(t.Planned, op)
}

// Validate is phase 2: check every planned operation before anything is backed up, so a bad
// plan aborts with the filesystem untouched.
func (t *Transaction) Validate() error {
	for _, op := range t.Planned {
		switch op.Type {
		case OpTypeHook:
			if op.Hook == nil || len(op.Hook.Command) == 0 {
				return fmt.Errorf("%w: hook for asset %q has an empty command", ErrHookFailed, hookAsset(op))
			}
			// A hook mutates nothing at its target, so the writability checks do not apply.
			// Resolving argv[0] here makes a typo fail now rather than halfway through.
			if _, err := lookPath(op.Hook.Command[0]); err != nil {
				return fmt.Errorf("%w: hook for asset %q: %w", ErrHookFailed, op.Hook.AssetID, err)
			}
			continue
		case OpTypeCopy, OpTypeSymlink, OpTypeChmod, OpTypeDelete:
		default:
			return fmt.Errorf("%w: %q", ErrUnknownOperation, op.Type)
		}

		if _, err := os.Lstat(op.Target); err == nil {
			if !writable(op.Target) {
				return fmt.Errorf("%w: %s", ErrNotWritable, op.Target)
			}
			continue
		}
		parent := filepath.Dir(op.Target)
		if _, err := os.Stat(parent); err == nil && !writable(parent) {
			return fmt.Errorf("%w: parent directory %s", ErrNotWritable, parent)
		}
	}
	return nil
}

// Snapshot is phase 3: capture the pre-state of every target and write the journal to disk
// before returning. Hook operations are skipped — there is no pre-state to capture.
func (t *Transaction) Snapshot() error {
	if err := os.MkdirAll(filepath.Dir(t.journalPath), 0o755); err != nil {
		return fmt.Errorf("creating journal directory: %w", err)
	}
	if err := os.MkdirAll(t.backupDir, 0o700); err != nil {
		return fmt.Errorf("creating backup directory: %w", err)
	}

	for idx, op := range t.Planned {
		if op.Type == OpTypeHook {
			continue
		}
		entry, err := t.snapshotOne(idx, op)
		if err != nil {
			// A backup failure is fatal: proceeding would mutate something we cannot restore.
			return err
		}
		t.Entries = append(t.Entries, entry)
	}
	return t.flush()
}

func (t *Transaction) snapshotOne(idx int, op Operation) (RollbackEntry, error) {
	entry := RollbackEntry{Op: OpCreate, Target: op.Target}
	if op.Type == OpTypeDelete {
		entry.Op = OpDelete
	}

	fi, err := os.Lstat(op.Target)
	if os.IsNotExist(err) {
		return entry, nil
	}
	if err != nil {
		return entry, fmt.Errorf("inspecting %s: %w", op.Target, err)
	}
	if op.Type != OpTypeDelete {
		entry.Op = OpModify
	}

	mode := permissions.Format(fi.Mode())
	entry.Permissions = &mode
	if fi.Mode().IsRegular() {
		sum, err := hashFile(op.Target)
		if err != nil {
			return entry, fmt.Errorf("hashing %s: %w", op.Target, err)
		}
		entry.Checksum = &sum
	}

	backup := filepath.Join(t.backupDir, fmt.Sprintf("backup_%d_%s", idx, filepath.Base(op.Target)))
	if err := backUp(op.Target, backup, fi); err != nil {
		return entry, fmt.Errorf("creating backup snapshot for %s: %w", op.Target, err)
	}
	entry.SrcBackup = &backup
	return entry, nil
}

// backUp captures one target's pre-state. A symlink becomes a text file holding
// SYMLINK:<target>, which is how the Python implementation stores it.
func backUp(target, backup string, fi fs.FileInfo) error {
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		link, err := os.Readlink(target)
		if err != nil {
			return err
		}
		return os.WriteFile(backup, []byte(symlinkBackupPrefix+link), 0o600)
	case fi.IsDir():
		if err := os.MkdirAll(backup, fi.Mode().Perm()); err != nil {
			return err
		}
		return copyTree(target, backup)
	case fi.Mode().IsRegular():
		return copyFile(target, backup, fi.Mode().Perm())
	default:
		// Sockets and devices are not configuration and have no meaningful backup.
		return nil
	}
}

// Execute is phase 4. Any error here triggers an immediate rollback; the returned error says
// whether that rollback was complete.
func (t *Transaction) Execute(ctx context.Context) error {
	t.Status = StatusExecuting
	if err := t.flush(); err != nil {
		return err
	}

	for _, op := range t.Planned {
		if err := t.executeOne(ctx, op); err != nil {
			return t.failAndRollback(fmt.Errorf("executing %s on %s: %w", op.Type, op.Target, err))
		}
	}
	return nil
}

func (t *Transaction) executeOne(ctx context.Context, op Operation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if op.Type == OpTypeHook {
		return t.runHook(ctx, op)
	}
	if err := os.MkdirAll(filepath.Dir(op.Target), 0o755); err != nil {
		return err
	}

	switch op.Type {
	case OpTypeCopy:
		if err := t.copy(op); err != nil {
			return err
		}
	case OpTypeSymlink:
		src, ok := op.Source.(SourcePath)
		if !ok {
			return fmt.Errorf("%w: symlink needs a source path", ErrUnknownOperation)
		}
		if err := removeAny(op.Target); err != nil {
			return err
		}
		if err := os.Symlink(src.Path, op.Target); err != nil {
			return err
		}
	case OpTypeChmod:
		return permissions.Enforce(op.Target, op.Permissions, op.Owner)
	case OpTypeDelete:
		return removeAny(op.Target)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownOperation, op.Type)
	}

	// A copy or symlink that declared permissions gets them immediately, not in a later pass:
	// the window where a secret sits on disk with the wrong mode has to be zero-length.
	if op.Permissions != "" {
		return permissions.Enforce(op.Target, op.Permissions, op.Owner)
	}
	return nil
}

func (t *Transaction) copy(op Operation) error {
	mode := fs.FileMode(0)
	if op.Permissions != "" {
		m, err := permissions.Parse(op.Permissions)
		if err != nil {
			return err
		}
		mode = m
	}

	switch src := op.Source.(type) {
	case SourceBytes:
		return AtomicWrite(op.Target, src.Data, mode)
	case SourcePath:
		fi, err := os.Stat(src.Path)
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return atomicCopyDir(src.Path, op.Target)
		}
		content, err := os.ReadFile(src.Path)
		if err != nil {
			return err
		}
		if mode == 0 {
			mode = fi.Mode().Perm()
		}
		return AtomicWrite(op.Target, content, mode)
	case nil:
		return AtomicWrite(op.Target, nil, mode)
	default:
		return fmt.Errorf("%w: copy from %T", ErrUnknownOperation, op.Source)
	}
}

func (t *Transaction) runHook(ctx context.Context, op Operation) error {
	hook := *op.Hook
	// Record the hook BEFORE running it: one that starts and then fails or times out still ran,
	// and the user needs to know that.
	record := ExecutedHook{
		AssetID: hook.AssetID,
		Stage:   hook.Stage,
		Command: hook.Command,
		Started: float64(t.now().UnixNano()) / float64(time.Second),
		Result:  HookOK,
	}
	idx := len(t.ExecutedHooks)
	t.ExecutedHooks = append(t.ExecutedHooks, record)

	hookCtx, cancel := context.WithTimeout(ctx, HookTimeout)
	defer cancel()

	err := t.runner(hookCtx, hook, op.Target, t.TxID)
	switch {
	case err == nil:
		return nil
	case errors.Is(hookCtx.Err(), context.DeadlineExceeded):
		t.ExecutedHooks[idx].Result = HookTimedOut
		return fmt.Errorf("%w: asset %q %s hook timed out after %s", ErrHookFailed, hook.AssetID, hook.Stage, HookTimeout)
	default:
		t.ExecutedHooks[idx].Result = HookFailed
		return fmt.Errorf("%w: asset %q %s hook: %w", ErrHookFailed, hook.AssetID, hook.Stage, err)
	}
}

// Verify is phase 5. A failure here triggers rollback.
func (t *Transaction) Verify() error {
	t.Status = StatusVerifying
	if err := t.flush(); err != nil {
		return err
	}

	for idx, op := range t.Planned {
		if err := t.verifyOne(idx, op); err != nil {
			return t.failAndRollback(err)
		}
	}
	return nil
}

func (t *Transaction) verifyOne(idx int, op Operation) error {
	switch op.Type {
	case OpTypeHook:
		// A hook that exited 0 is done; its effect is not rv's to assert.
		return nil
	case OpTypeDelete:
		// The delete-then-write pattern is normal, so a later operation recreating the target
		// is not a verification failure.
		if slices.ContainsFunc(t.Planned[idx+1:], func(other Operation) bool {
			return other.Target == op.Target && (other.Type == OpTypeCopy || other.Type == OpTypeSymlink)
		}) {
			return nil
		}
		if _, err := os.Lstat(op.Target); err == nil {
			return fmt.Errorf("%w: deleted target still exists at %s", ErrVerify, op.Target)
		}
		return nil
	}

	// Lstat, not Stat: a dangling symlink still counts as existing. rv created the link, and
	// whether its destination exists is not rv's claim to make.
	if _, err := os.Lstat(op.Target); err != nil {
		return fmt.Errorf("%w: target not found at %s", ErrVerify, op.Target)
	}
	if op.Permissions == "" {
		return nil
	}
	ok, err := permissions.Verify(op.Target, op.Permissions)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrVerify, err)
	}
	if !ok {
		return fmt.Errorf("%w: permissions mismatch on %s, expected %s", ErrVerify, op.Target, op.Permissions)
	}
	return nil
}

// Commit is phase 6. The transaction becomes durable and irreversible.
func (t *Transaction) Commit() error {
	t.Status = StatusCommitted
	return t.flush()
}

// Cleanup is phase 7. Failures are logged and never escalate: a leftover backup is harmless and
// pruning collects it.
func (t *Transaction) Cleanup() {
	if err := os.RemoveAll(t.backupDir); err != nil {
		t.log.Debug("removing backup directory", "error", err, "tx_id", t.TxID)
	}
	if t.Status != StatusCommitted {
		return
	}
	if err := os.Remove(t.journalPath); err != nil && !os.IsNotExist(err) {
		t.log.Debug("removing journal", "error", err, "tx_id", t.TxID)
	}
}

// failAndRollback rolls back and reports both what failed and whether the machine was restored.
func (t *Transaction) failAndRollback(cause error) error {
	if err := t.Rollback(); err != nil {
		return fmt.Errorf("%w; rollback was incomplete: %w", cause, err)
	}
	return fmt.Errorf("%w; the transaction was rolled back and your files are unchanged", cause)
}

// flush writes the journal to disk.
func (t *Transaction) flush() error {
	if err := WriteJournal(t.journalPath, t.Journal()); err != nil {
		return fmt.Errorf("writing journal: %w", err)
	}
	return nil
}

// hashFile streams a file's SHA-256 rather than reading it whole.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writable(path string) bool {
	return unixAccessWritable(path)
}

func hookAsset(op Operation) string {
	if op.Hook == nil {
		return ""
	}
	return op.Hook.AssetID
}
