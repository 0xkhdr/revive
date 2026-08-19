package crypto

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// zeroChunk is the buffer used to overwrite a file's contents on close.
var zeroChunk = make([]byte, 32*1024)

// TempFile is a temporary file holding plaintext. It is created mode 0600 from the start —
// never create-then-chmod, which leaves a window where the plaintext is world-readable — and
// Close overwrites it with zeros, fsyncs, and unlinks it.
//
// Use it via defer:
//
//	tf, err := crypto.NewTempFile(dir, "rv-secret-")
//	if err != nil { return err }
//	defer tf.Close()
type TempFile struct {
	f      *os.File
	path   string
	closed bool
}

// NewTempFile creates a 0600 temp file in dir (os.TempDir when empty).
func NewTempFile(dir, pattern string) (*TempFile, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	// os.CreateTemp creates with 0600 already; the explicit chmod-free path is the point.
	f, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("creating secure temp file: %w", err)
	}
	return &TempFile{f: f, path: f.Name()}, nil
}

// Name returns the temp file's path.
func (t *TempFile) Name() string { return t.path }

// Write writes to the temp file.
func (t *TempFile) Write(p []byte) (int, error) { return t.f.Write(p) }

// Sync flushes the file to disk.
func (t *TempFile) Sync() error { return t.f.Sync() }

// Close overwrites the file with zeros, syncs, closes and unlinks it. It is safe to call twice.
func (t *TempFile) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true
	err := zeroFile(t.f)
	if closeErr := t.f.Close(); err == nil {
		err = closeErr
	}
	if rmErr := os.Remove(t.path); err == nil && !errors.Is(rmErr, fs.ErrNotExist) {
		err = rmErr
	}
	if err != nil {
		return fmt.Errorf("closing secure temp file: %w", err)
	}
	return nil
}

// zeroFile overwrites a file's whole length with zeros and fsyncs it.
func zeroFile(f *os.File) error {
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	for remaining := size; remaining > 0; {
		n := int64(len(zeroChunk))
		if remaining < n {
			n = remaining
		}
		written, err := f.Write(zeroChunk[:n])
		if err != nil {
			return err
		}
		remaining -= int64(written)
	}
	return f.Sync()
}

// TempDir is a 0700 temporary directory whose files are each zeroed before the tree is removed.
type TempDir struct {
	path   string
	closed bool
}

// NewTempDir creates a 0700 temp directory in dir (os.TempDir when empty).
func NewTempDir(dir, pattern string) (*TempDir, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	path, err := os.MkdirTemp(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("creating secure temp dir: %w", err)
	}
	// MkdirTemp already uses 0700, but the mode is a security property worth stating.
	// #nosec G302 -- 0700 is the documented mode for a secure temp *directory*; the 0600 rule
	// the linter is applying is about files.
	if err := os.Chmod(path, 0o700); err != nil {
		_ = os.RemoveAll(path)
		return nil, fmt.Errorf("securing temp dir: %w", err)
	}
	return &TempDir{path: path}, nil
}

// Path returns the directory's path.
func (t *TempDir) Path() string { return t.path }

// Close zeros every file in the tree, then removes it. Safe to call twice.
func (t *TempDir) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true

	walkErr := filepath.WalkDir(t.path, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return err
		}
		f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		return zeroFile(f)
	})
	rmErr := os.RemoveAll(t.path)
	if walkErr != nil {
		return fmt.Errorf("zeroing secure temp dir: %w", walkErr)
	}
	if rmErr != nil {
		return fmt.Errorf("removing secure temp dir: %w", rmErr)
	}
	return nil
}
