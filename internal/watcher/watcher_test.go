package watcher

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/0xkhdr/revive/internal/transaction"
)

// debounce is short enough to keep tests fast and long enough that a burst of writes lands
// inside one window.
const debounce = 120 * time.Millisecond

type fixture struct {
	t        *testing.T
	repo     string
	lockFile string
	watcher  *Watcher

	mu      sync.Mutex
	fired   int
	fireErr error
	notify  chan struct{}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		t:        t,
		repo:     filepath.Join(base, "repo"),
		lockFile: filepath.Join(base, "rv.lock"),
		notify:   make(chan struct{}, 32),
	}
	require.NoError(t, os.MkdirAll(f.repo, 0o755))
	return f
}

// start runs the daemon and waits until it is watching. The returned stop ends it and asserts a
// clean exit.
func (f *fixture) start() (stop func()) {
	f.t.Helper()
	ready := make(chan struct{})

	w, err := New(Options{
		RepoDir:  f.repo,
		LockFile: f.lockFile,
		Debounce: debounce,
		Ready:    ready,
		OnTrigger: func(context.Context) error {
			f.mu.Lock()
			f.fired++
			err := f.fireErr
			f.mu.Unlock()
			f.notify <- struct{}{}
			return err
		},
	})
	require.NoError(f.t, err)
	f.watcher = w

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		f.t.Fatal("the watcher never started")
	}

	return func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(f.t, err, "cancellation is a clean exit, not an error")
		case <-time.After(5 * time.Second):
			f.t.Fatal("the watcher did not shut down")
		}
	}
}

// newFixtureAndStart is the common setup: a running daemon in its own temporary workspace.
func newFixtureAndStart(t *testing.T) (*fixture, func()) {
	t.Helper()
	f := newFixture(t)
	return f, f.start()
}

func (f *fixture) write(name, content string) {
	f.t.Helper()
	p := filepath.Join(f.repo, name)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(f.t, os.WriteFile(p, []byte(content), 0o644))
}

// awaitFire waits for one restore to run.
func (f *fixture) awaitFire() {
	f.t.Helper()
	select {
	case <-f.notify:
	case <-time.After(5 * time.Second):
		f.t.Fatal("no restore was triggered")
	}
}

// expectNoFire asserts nothing runs within the window.
func (f *fixture) expectNoFire(window time.Duration) {
	f.t.Helper()
	select {
	case <-f.notify:
		f.t.Fatal("a restore ran when none should have")
	case <-time.After(window):
	}
}

func (f *fixture) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fired
}

// Phase 13: a file change triggers a restore after the debounce window.
func TestChangeTriggersRestore(t *testing.T) {
	f, stop := newFixtureAndStart(t)
	defer stop()

	f.write("assets/conf", "content\n")
	f.awaitFire()
	require.Equal(t, 1, f.count())
}

// Phase 13: rapid successive changes collapse into one restore.
func TestRapidChangesCollapse(t *testing.T) {
	f, stop := newFixtureAndStart(t)
	defer stop()

	// A burst well inside one debounce window.
	for i := range 20 {
		f.write("assets/conf", string(rune('a'+i%26)))
		time.Sleep(2 * time.Millisecond)
	}
	f.awaitFire()

	// Nothing further may fire from that same burst.
	f.expectNoFire(3 * debounce)
	require.Equal(t, 1, f.count(), "a burst of writes is one restore, not twenty")
}

// A change after the window is a separate restore.
func TestSeparateChangesTriggerSeparately(t *testing.T) {
	f, stop := newFixtureAndStart(t)
	defer stop()

	f.write("assets/one", "1\n")
	f.awaitFire()
	f.write("assets/two", "2\n")
	f.awaitFire()
	require.Equal(t, 2, f.count())
}

// Phase 13: .git changes are ignored.
func TestGitChangesAreIgnored(t *testing.T) {
	f, stop := newFixtureAndStart(t)
	defer stop()

	f.write(".git/index", "git churn\n")
	f.write(".git/refs/heads/main", "abc123\n")
	f.expectNoFire(4 * debounce)
	require.Zero(t, f.count(), "git churns constantly and none of it is a manifest change")

	// A real change still fires, proving the watch is live.
	f.write("assets/conf", "content\n")
	f.awaitFire()
}

// A directory created after the watch starts is watched too.
func TestNewDirectoriesAreWatched(t *testing.T) {
	f, stop := newFixtureAndStart(t)
	defer stop()

	require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "nested", "deep"), 0o755))
	f.awaitFire()

	f.write("nested/deep/conf", "content\n")
	f.awaitFire()
	require.GreaterOrEqual(t, f.count(), 2)
}

// Phase 13: a trigger while the process lock is held is skipped, not queued.
func TestTriggerDuringHeldLockIsSkipped(t *testing.T) {
	f, stop := newFixtureAndStart(t)
	defer stop()

	// flock is per open file description, so a subprocess is the only honest way to hold it
	// against this process.
	held := holdLock(t, f.lockFile)

	f.write("assets/conf", "content\n")
	f.expectNoFire(4 * debounce)
	require.Zero(t, f.count())

	_, skipped := f.watcher.Stats()
	require.Positive(t, skipped, "the trigger is skipped rather than queued")

	held()
	// Releasing the lock does not replay the skipped trigger; the next change does.
	f.expectNoFire(2 * debounce)
	f.write("assets/conf", "changed again\n")
	f.awaitFire()
}

// A failing restore must not stop the daemon: the user fixes the manifest and saves again.
func TestFailedRestoreKeepsWatching(t *testing.T) {
	f, stop := newFixtureAndStart(t)
	defer stop()

	f.mu.Lock()
	f.fireErr = errors.New("the manifest is invalid")
	f.mu.Unlock()

	f.write("assets/conf", "broken\n")
	f.awaitFire()

	f.mu.Lock()
	f.fireErr = nil
	f.mu.Unlock()

	f.write("assets/conf", "fixed\n")
	f.awaitFire()
	require.Equal(t, 2, f.count())
}

// Phase 13: SIGINT and SIGTERM shut down cleanly with no goroutine leak.
//
// The daemon is one goroutine — events, the debounce timer and the restore all run on it — so
// there is nothing to leak, and this asserts that stays true.
func TestCleanShutdownLeaksNothing(t *testing.T) {
	defer goleak.VerifyNone(t,
		// fsnotify's inotify reader can outlive Close briefly; it is not ours to wait on.
		goleak.IgnoreTopFunction("github.com/fsnotify/fsnotify.(*inotify).readEvents"),
	)

	f := newFixture(t)
	ready := make(chan struct{})
	w, err := New(Options{
		RepoDir:   f.repo,
		LockFile:  f.lockFile,
		Debounce:  debounce,
		Ready:     ready,
		OnTrigger: func(context.Context) error { return nil },
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	<-ready

	f.write("assets/conf", "content\n")
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err, "cancellation is a clean exit, not an error")
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher did not shut down")
	}
}

// Cancelling with a debounce timer pending must not hang.
func TestShutdownWithAPendingTimer(t *testing.T) {
	f := newFixture(t)
	ready := make(chan struct{})
	w, err := New(Options{
		RepoDir:   f.repo,
		LockFile:  f.lockFile,
		Debounce:  time.Hour, // long enough that it certainly has not fired
		Ready:     ready,
		OnTrigger: func(context.Context) error { return nil },
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	<-ready

	f.write("assets/conf", "content\n")
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("shutting down with a pending timer hung")
	}
}

func TestNewRequiresATrigger(t *testing.T) {
	t.Parallel()
	_, err := New(Options{RepoDir: t.TempDir()})
	require.Error(t, err)
}

func TestNewAppliesDefaults(t *testing.T) {
	t.Parallel()
	w, err := New(Options{RepoDir: t.TempDir(), OnTrigger: func(context.Context) error { return nil }})
	require.NoError(t, err)
	require.Equal(t, DefaultDebounce, w.opts.Debounce)
	require.NotNil(t, w.opts.Log)
}

func TestRunOnAMissingDirectory(t *testing.T) {
	t.Parallel()
	w, err := New(Options{
		RepoDir:   filepath.Join(t.TempDir(), "absent"),
		OnTrigger: func(context.Context) error { return nil },
	})
	require.NoError(t, err)
	require.Error(t, w.Run(context.Background()))
}

func TestIgnored(t *testing.T) {
	t.Parallel()
	w := &Watcher{opts: Options{RepoDir: "/repo"}}
	require.True(t, w.ignored("/repo/.git"))
	require.True(t, w.ignored("/repo/.git/objects/ab/cdef"))
	require.True(t, w.ignored("/repo/nested/.git/index"))
	require.False(t, w.ignored("/repo/assets/conf"))
	require.False(t, w.ignored("/repo/.gitignore"), ".gitignore is a real file, not git churn")
}

// holdLock holds the process lock from a separate process and returns a release function.
//
// Acquiring it in this process would not block the watcher: flock is per open file description,
// so only a real second process reproduces what a concurrent rv looks like.
func holdLock(t *testing.T, path string) func() {
	t.Helper()
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skipf("flock(1) is needed to hold the lock from another process: %v", err)
	}
	// Create the file first so flock(1) and rv agree on the same inode.
	lock, err := transaction.Acquire(path)
	require.NoError(t, err)
	require.NoError(t, lock.Release())

	held := make(chan struct{})
	release := make(chan struct{})
	cmd := exec.Command("flock", path, "sh", "-c", "echo ready; read _")
	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())

	go func() {
		buf := make([]byte, 16)
		_, _ = stdout.Read(buf)
		close(held)
		<-release
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("the helper never took the lock")
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			close(release)
			// Give the helper a moment to actually exit and drop the lock.
			time.Sleep(50 * time.Millisecond)
		})
	}
}

// An unreadable subdirectory is skipped rather than failing the whole watch.
func TestUnreadableSubdirectoryIsSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read bit")
	}
	f := newFixture(t)
	locked := filepath.Join(f.repo, "locked")
	require.NoError(t, os.Mkdir(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	stop := f.start()
	defer stop()

	f.write("assets/conf", "content\n")
	f.awaitFire()
}

// A watch error on the channel is logged and the daemon keeps going.
func TestWatchSurvivesADeletedDirectory(t *testing.T) {
	f, stop := newFixtureAndStart(t)
	defer stop()

	nested := filepath.Join(f.repo, "nested")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	f.awaitFire()

	require.NoError(t, os.RemoveAll(nested))
	f.awaitFire()

	f.write("assets/conf", "content\n")
	f.awaitFire()
}

// rv writes its lockfile into the workspace on every restore. Without ignoring it, each restore
// would trigger the next one and the daemon would never settle.
func TestOwnOutputDoesNotRetrigger(t *testing.T) {
	f := newFixture(t)
	lockfile := filepath.Join(f.repo, "manifest.lock")

	ready := make(chan struct{})
	w, err := New(Options{
		RepoDir:     f.repo,
		LockFile:    f.lockFile,
		Debounce:    debounce,
		Ready:       ready,
		IgnorePaths: []string{lockfile},
		OnTrigger: func(context.Context) error {
			// A restore writes the lockfile and an atomic temp file beside its targets.
			require.NoError(t, os.WriteFile(lockfile, []byte("{}"), 0o644))
			tmp := filepath.Join(f.repo, ".rv_atomic_tmp_12345")
			require.NoError(t, os.WriteFile(tmp, []byte("x"), 0o644))
			require.NoError(t, os.Remove(tmp))
			f.mu.Lock()
			f.fired++
			f.mu.Unlock()
			f.notify <- struct{}{}
			return nil
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	<-ready

	f.write("assets/conf", "content\n")
	f.awaitFire()

	// The restore's own writes must not produce a second one.
	f.expectNoFire(5 * debounce)
	require.Equal(t, 1, f.count(), "a restore must not trigger itself")

	cancel()
	require.NoError(t, <-done)
}

func TestIgnoresAtomicTempFiles(t *testing.T) {
	t.Parallel()
	w := &Watcher{opts: Options{RepoDir: "/repo", IgnorePaths: []string{"/repo/manifest.lock"}}}
	require.True(t, w.ignored("/repo/manifest.lock"))
	require.True(t, w.ignored("/repo/assets/.rv_atomic_tmp_123"))
	require.True(t, w.ignored("/repo/.rv_atomic_dir_tmp_456"))
	require.False(t, w.ignored("/repo/manifest.yaml"))
	require.False(t, w.ignored("/repo/assets/Cargo.lock"), "only rv's own lockfile is ignored")
}
