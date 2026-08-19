//go:build unix

package transaction

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ErrLockHeld is returned when another rv process holds the process lock.
var ErrLockHeld = errors.New("another rv process holds the lock")

// ProcessLock is an advisory flock on ~/.config/rv/rv.lock, held for a whole restore run.
//
// Non-blocking is deliberate: a second rv should fail immediately with "another process holds
// the lock" rather than queue behind a ten-minute apt install.
type ProcessLock struct {
	path string
	f    *os.File
}

// Acquire takes the lock at path, non-blocking. The path is injectable so tests get their own
// lock file rather than serializing on a shared one.
func Acquire(path string) (*ProcessLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file %s: %w", path, err)
	}

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %s", ErrLockHeld, path)
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	// Record the PID for auditability: "which process is holding this?" is the first question
	// anyone asks when they hit ErrLockHeld.
	if err := f.Truncate(0); err == nil {
		_, _ = fmt.Fprintf(f, "%d", os.Getpid())
		_ = f.Sync()
	}
	return &ProcessLock{path: path, f: f}, nil
}

// Path returns the lock file's path.
func (l *ProcessLock) Path() string { return l.path }

// Release unlocks and closes the lock file. It is safe to call twice, which is what makes
// `defer lock.Release()` correct on every exit path, panics included.
func (l *ProcessLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	unlockErr := unix.Flock(int(f.Fd()), unix.LOCK_UN)
	closeErr := f.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlocking %s: %w", l.path, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("closing lock file %s: %w", l.path, closeErr)
	}
	return nil
}
