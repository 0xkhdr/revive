// Package watcher runs the `rv watch` daemon: it watches the workspace and re-runs a restore
// after changes settle.
package watcher

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/0xkhdr/revive/internal/transaction"
)

// DefaultDebounce matches the documented `rv watch` default.
const DefaultDebounce = 5 * time.Second

// ignoredDirs are never watched. `.git` churns constantly during any git operation and none of it
// is a manifest change.
var ignoredDirs = map[string]bool{
	".git": true,
}

// ignoredPrefixes are basename prefixes rv itself creates. An atomic write puts its temp file
// beside the target, so without this the daemon reacts to its own writes.
var ignoredPrefixes = []string{".rv_atomic_tmp_", ".rv_atomic_dir_tmp_"}

// Trigger runs one restore. It returns an error only for a genuine failure; a skipped run is not
// one.
type Trigger func(ctx context.Context) error

// Options configures the daemon.
type Options struct {
	RepoDir  string
	LockFile string
	Debounce time.Duration
	Log      *slog.Logger
	// OnTrigger is called for each fired restore. Required.
	OnTrigger Trigger
	// IgnorePaths are absolute paths rv writes itself, chiefly the lockfile. A restore writes
	// its lockfile into the workspace, so without this every restore triggers the next one and
	// the daemon never settles.
	IgnorePaths []string
	// Ready is closed once the watch is established. Tests use it; production leaves it nil.
	Ready chan<- struct{}
}

// Watcher is the daemon.
type Watcher struct {
	opts Options

	mu       sync.Mutex
	restores int
	skipped  int
}

// New builds a Watcher.
func New(opts Options) (*Watcher, error) {
	if opts.OnTrigger == nil {
		return nil, errors.New("watcher: OnTrigger is required")
	}
	if opts.Debounce <= 0 {
		opts.Debounce = DefaultDebounce
	}
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}
	return &Watcher{opts: opts}, nil
}

// Stats reports how many restores ran and how many triggers were skipped because the process
// lock was held.
func (w *Watcher) Stats() (restores, skipped int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.restores, w.skipped
}

// Run watches until the context is cancelled, then returns.
//
// Everything happens on this one goroutine: fsnotify events, the debounce timer, and the restore
// itself. There is no shared state to guard and nothing to leak — cancelling the context ends the
// loop, closes the watcher, and returns.
func (w *Watcher) Run(ctx context.Context) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating filesystem watcher: %w", err)
	}
	defer func() { _ = fsw.Close() }()

	if err := w.addTree(fsw, w.opts.RepoDir); err != nil {
		return err
	}
	if w.opts.Ready != nil {
		close(w.opts.Ready)
	}
	w.opts.Log.Info("watching for changes",
		"repo_dir", w.opts.RepoDir, "debounce", w.opts.Debounce)

	// A stopped timer with a drained channel is the idle state; each event resets it, so a burst
	// of changes collapses into one restore.
	timer := time.NewTimer(w.opts.Debounce)
	if !timer.Stop() {
		<-timer.C
	}
	pending := false

	for {
		select {
		case <-ctx.Done():
			w.opts.Log.Info("shutting down")
			if !timer.Stop() && pending {
				<-timer.C
			}
			return nil

		case event, ok := <-fsw.Events:
			if !ok {
				return nil
			}
			if w.ignored(event.Name) {
				continue
			}
			// A newly created directory has to be watched too, or changes inside it are missed.
			if event.Has(fsnotify.Create) {
				if fi, err := os.Stat(event.Name); err == nil && fi.IsDir() {
					if err := w.addTree(fsw, event.Name); err != nil {
						w.opts.Log.Warn("watching new directory", "path", event.Name, "error", err)
					}
				}
			}
			w.opts.Log.Debug("change detected", "path", event.Name, "op", event.Op.String())

			if pending && !timer.Stop() {
				<-timer.C
			}
			timer.Reset(w.opts.Debounce)
			pending = true

		case err, ok := <-fsw.Errors:
			if !ok {
				return nil
			}
			w.opts.Log.Warn("filesystem watch error", "error", err)

		case <-timer.C:
			pending = false
			w.fire(ctx)
		}
	}
}

// fire runs one restore, unless another rv process holds the lock.
//
// A trigger during a held lock is skipped, never queued: the run that holds the lock is already
// applying the current state of the repository, so queueing would just repeat work.
func (w *Watcher) fire(ctx context.Context) {
	lock, err := transaction.Acquire(w.opts.LockFile)
	if errors.Is(err, transaction.ErrLockHeld) {
		w.mu.Lock()
		w.skipped++
		w.mu.Unlock()
		w.opts.Log.Info("another rv process holds the lock; skipping this trigger")
		return
	}
	if err != nil {
		w.opts.Log.Error("acquiring the process lock", "error", err)
		return
	}
	// The restore takes the lock itself, so it is released before triggering.
	if err := lock.Release(); err != nil {
		w.opts.Log.Warn("releasing the probe lock", "error", err)
	}

	w.mu.Lock()
	w.restores++
	w.mu.Unlock()

	if err := w.opts.OnTrigger(ctx); err != nil {
		// A failed restore must not stop the daemon: the user fixes the manifest and saves
		// again, which is the next trigger.
		w.opts.Log.Error("restore failed", "error", err)
		return
	}
	w.opts.Log.Info("restore complete")
}

// addTree watches dir and every subdirectory under it. fsnotify is not recursive.
//
// A missing or unreadable root is a hard error — there is nothing to watch and the daemon would
// otherwise sit forever on an empty watch. A missing *sub*directory is skipped: it may simply
// have been deleted between the walk starting and reaching it.
func (w *Watcher) addTree(fsw *fsnotify.Watcher, dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("watching %s: %w", dir, err)
	}
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// An unreadable subdirectory is skipped rather than failing the whole watch.
			return nil //nolint:nilerr // a directory rv cannot read has nothing to watch
		}
		if !d.IsDir() {
			return nil
		}
		if w.ignored(path) {
			return filepath.SkipDir
		}
		if err := fsw.Add(path); err != nil {
			if path == dir {
				return fmt.Errorf("watching %s: %w", path, err)
			}
			// A subdirectory rv cannot watch — no read permission, or deleted mid-walk — is
			// skipped. Failing the whole daemon over one unreadable directory would be worse
			// than watching the rest of the workspace.
			w.opts.Log.Warn("skipping unwatchable directory", "path", path, "error", err)
			return filepath.SkipDir
		}
		return nil
	})
}

// ignored reports whether a path is one the daemon does not react to: git churn, rv's own
// atomic-write temp files, or a path the caller named as rv's own output.
func (w *Watcher) ignored(path string) bool {
	for _, ignore := range w.opts.IgnorePaths {
		if path == ignore {
			return true
		}
	}
	base := filepath.Base(path)
	for _, prefix := range ignoredPrefixes {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}

	rel, err := filepath.Rel(w.opts.RepoDir, path)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if ignoredDirs[part] {
			return true
		}
	}
	return false
}
