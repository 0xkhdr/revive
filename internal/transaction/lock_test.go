package transaction

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 5: two processes cannot hold the lock at once, and the second gets ErrLockHeld
// immediately rather than blocking.
//
// flock is per-open-file-description, so a second Acquire in this process would succeed; a real
// subprocess is the only honest test.
func TestLockIsExclusiveAcrossProcesses(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rv.lock")

	lock, err := Acquire(path)
	require.NoError(t, err)
	defer func() { _ = lock.Release() }()

	require.False(t, lockAvailable(t, path), "a second process must not get the lock")

	require.NoError(t, lock.Release())
	require.True(t, lockAvailable(t, path), "the lock must be free once released")
}

// lockAvailable reports whether a separate process can take the lock, using flock(1) with -n so
// the check is non-blocking.
func lockAvailable(t *testing.T, path string) bool {
	t.Helper()
	if _, err := exec.LookPath("flock"); err != nil {
		t.Skipf("flock(1) is needed to prove cross-process exclusion: %v", err)
	}
	return exec.Command("flock", "-n", path, "true").Run() == nil
}

func TestLockRecordsThePID(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rv.lock")
	lock, err := Acquire(path)
	require.NoError(t, err)
	defer func() { _ = lock.Release() }()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	require.Equal(t, os.Getpid(), pid, "the PID answers 'who is holding this?'")
	require.Equal(t, path, lock.Path())
}

func TestLockCreatesItsDirectory(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "config", "rv.lock")
	lock, err := Acquire(path)
	require.NoError(t, err)
	require.NoError(t, lock.Release())
	require.FileExists(t, path)
}

// Phase 5: the lock is released on every exit path, panic included.
func TestLockIsReleasedOnPanic(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rv.lock")

	func() {
		defer func() { _ = recover() }()
		lock, err := Acquire(path)
		require.NoError(t, err)
		defer func() { _ = lock.Release() }()
		panic("boom")
	}()

	require.True(t, lockAvailable(t, path), "a panicking run must not strand the lock")
}

func TestReleaseIsIdempotent(t *testing.T) {
	t.Parallel()
	lock, err := Acquire(filepath.Join(t.TempDir(), "rv.lock"))
	require.NoError(t, err)
	require.NoError(t, lock.Release())
	require.NoError(t, lock.Release())

	var nilLock *ProcessLock
	require.NoError(t, nilLock.Release())
}

func TestAcquireOnAnUnwritablePathErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err := Acquire(filepath.Join(dir, "rv.lock"))
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrLockHeld, "a permission problem is not a contended lock")
}
