package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The directory walk order is part of the compatibility contract: a workspace restored by the
// Python implementation has to hash identically here, or every asset reports drift.
//
// The fixture digests were computed by reference/'s own RestoreService.calculate_sha256.
func TestChecksumMatchesThePythonImplementation(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/tree_sha256.json")
	require.NoError(t, err)
	var want struct {
		Dir  string `json:"dir"`
		File string `json:"file"`
	}
	require.NoError(t, json.Unmarshal(raw, &want))

	got, err := SHA256("testdata/tree")
	require.NoError(t, err)
	require.Equal(t, want.Dir, got,
		"the walk must hash every file in a directory before descending, both in name order")

	got, err = SHA256("testdata/tree/a_first.txt")
	require.NoError(t, err)
	require.Equal(t, want.File, got)
}

func TestFileChecksumIsPlainSHA256(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "f")
	content := []byte("hello world")
	require.NoError(t, os.WriteFile(path, content, 0o644))

	sum := sha256.Sum256(content)
	got, err := SHA256(path)
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(sum[:]), got)
}

// A large file is streamed in chunks, so the digest must not depend on the buffer boundary.
func TestLargeFileStreams(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "big")
	content := make([]byte, chunkSize*3+7)
	for i := range content {
		content[i] = byte(i % 251)
	}
	require.NoError(t, os.WriteFile(path, content, 0o644))

	sum := sha256.Sum256(content)
	got, err := SHA256(path)
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(sum[:]), got)
}

// A missing path hashes to the empty string rather than failing: an asset whose source has gone
// still needs a lockfile entry.
func TestMissingPathHashesToEmpty(t *testing.T) {
	t.Parallel()
	got, err := SHA256(filepath.Join(t.TempDir(), "absent"))
	require.NoError(t, err)
	require.Empty(t, got)
}

// The relative path goes into the digest, so a rename is a change even when the bytes are not.
func TestRenameChangesTheDigest(t *testing.T) {
	t.Parallel()
	a, b := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(a, "one.txt"), []byte("same"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(b, "two.txt"), []byte("same"), 0o644))

	sumA, err := SHA256(a)
	require.NoError(t, err)
	sumB, err := SHA256(b)
	require.NoError(t, err)
	require.NotEqual(t, sumA, sumB)
}

func TestContentChangesTheDigest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(path, []byte("before"), 0o644))
	before, err := SHA256(dir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(path, []byte("after"), 0o644))
	after, err := SHA256(dir)
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}

func TestEmptyDirectory(t *testing.T) {
	t.Parallel()
	got, err := SHA256(t.TempDir())
	require.NoError(t, err)
	require.Len(t, got, 64, "an empty directory still has the digest of no input")
}

// An unreadable file contributes its path but no content, rather than failing the whole walk: a
// permissions problem is doctor's business, not the checksum's.
func TestUnreadableFileDoesNotFailTheWalk(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read bit")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "locked")
	require.NoError(t, os.WriteFile(path, []byte("secret"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	got, err := SHA256(dir)
	require.NoError(t, err)
	require.Len(t, got, 64)
}

func TestUnreadableDirectoryFails(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read bit")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	require.NoError(t, os.Mkdir(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	_, err := SHA256(dir)
	require.Error(t, err, "a directory that cannot be listed makes the digest meaningless")
}

func TestChecksumIsStable(t *testing.T) {
	t.Parallel()
	first, err := SHA256("testdata/tree")
	require.NoError(t, err)
	for range 5 {
		got, err := SHA256("testdata/tree")
		require.NoError(t, err)
		require.Equal(t, first, got)
	}
}
