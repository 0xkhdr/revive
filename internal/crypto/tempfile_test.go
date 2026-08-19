package crypto

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 4: a secure temp file is created mode 0600 and is zeroed and unlinked on close.
func TestTempFileIsSecureAndZeroed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tf, err := NewTempFile(dir, "rv-secret-")
	require.NoError(t, err)

	fi, err := os.Stat(tf.Name())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(),
		"the mode must be right from creation, never create-then-chmod")

	secret := []byte("API_TOKEN=sk-live-0123456789")
	_, err = tf.Write(secret)
	require.NoError(t, err)
	require.NoError(t, tf.Sync())

	// Observe the on-disk bytes through a second handle, so the zeroing is visible after close.
	observer, err := os.Open(tf.Name())
	require.NoError(t, err)
	defer func() { _ = observer.Close() }()

	require.NoError(t, tf.Close())
	require.NoFileExists(t, tf.Name(), "the file must be unlinked")

	after := make([]byte, len(secret))
	n, _ := observer.ReadAt(after, 0)
	require.Equal(t, make([]byte, len(secret)), after[:n], "the bytes must have been overwritten")
}

func TestTempFileCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	tf, err := NewTempFile(t.TempDir(), "rv-")
	require.NoError(t, err)
	require.NoError(t, tf.Close())
	require.NoError(t, tf.Close())
}

func TestTempFileDefaultsToTempDir(t *testing.T) {
	t.Parallel()
	tf, err := NewTempFile("", "rv-")
	require.NoError(t, err)
	defer func() { _ = tf.Close() }()
	require.Equal(t, os.TempDir(), filepath.Dir(tf.Name()))
}

func TestTempFileInAMissingDirectoryErrors(t *testing.T) {
	t.Parallel()
	_, err := NewTempFile(filepath.Join(t.TempDir(), "nope"), "rv-")
	require.Error(t, err)
}

// Phase 4: the directory variant is 0700 and zeros every file inside before removing the tree.
func TestTempDirIsSecureAndZeroed(t *testing.T) {
	t.Parallel()
	td, err := NewTempDir(t.TempDir(), "rv-secrets-")
	require.NoError(t, err)

	fi, err := os.Stat(td.Path())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), fi.Mode().Perm())

	require.NoError(t, os.Mkdir(filepath.Join(td.Path(), "nested"), 0o700))
	secretPath := filepath.Join(td.Path(), "nested", "env")
	secret := []byte("PASSWORD=hunter2")
	require.NoError(t, os.WriteFile(secretPath, secret, 0o600))
	require.NoError(t, os.Symlink(secretPath, filepath.Join(td.Path(), "link")))

	observer, err := os.Open(secretPath)
	require.NoError(t, err)
	defer func() { _ = observer.Close() }()

	require.NoError(t, td.Close())
	require.NoDirExists(t, td.Path())

	after := make([]byte, len(secret))
	n, _ := observer.ReadAt(after, 0)
	require.Equal(t, make([]byte, len(secret)), after[:n],
		"a file inside the tree must be zeroed, not merely unlinked")
}

func TestTempDirCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	td, err := NewTempDir(t.TempDir(), "rv-")
	require.NoError(t, err)
	require.NoError(t, td.Close())
	require.NoError(t, td.Close())
}

func TestTempDirDefaultsToTempDir(t *testing.T) {
	t.Parallel()
	td, err := NewTempDir("", "rv-")
	require.NoError(t, err)
	defer func() { _ = td.Close() }()
	require.Equal(t, os.TempDir(), filepath.Dir(td.Path()))
}

func TestTempDirInAMissingDirectoryErrors(t *testing.T) {
	t.Parallel()
	_, err := NewTempDir(filepath.Join(t.TempDir(), "nope"), "rv-")
	require.Error(t, err)
}

func TestTempFileZeroingSpansMultipleChunks(t *testing.T) {
	t.Parallel()
	tf, err := NewTempFile(t.TempDir(), "rv-big-")
	require.NoError(t, err)

	big := make([]byte, len(zeroChunk)*2+17)
	for i := range big {
		big[i] = 'A'
	}
	_, err = tf.Write(big)
	require.NoError(t, err)

	observer, err := os.Open(tf.Name())
	require.NoError(t, err)
	defer func() { _ = observer.Close() }()

	require.NoError(t, tf.Close())

	after := make([]byte, len(big))
	n, _ := observer.ReadAt(after, 0)
	require.Equal(t, make([]byte, len(big)), after[:n], "every chunk must be overwritten, not just the first")
}

func TestTempDirCloseReportsAnUnreadableTree(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read bit")
	}
	td, err := NewTempDir(t.TempDir(), "rv-")
	require.NoError(t, err)

	locked := filepath.Join(td.Path(), "locked")
	require.NoError(t, os.Mkdir(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	require.Error(t, td.Close(), "a file that could not be zeroed must be reported, not swallowed")
}
