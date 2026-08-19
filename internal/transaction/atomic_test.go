package transaction

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 5: an atomic write creates its temp file in the target's directory and renames into
// place.
func TestAtomicWriteUsesASiblingTempFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "file.conf")

	require.NoError(t, AtomicWrite(target, []byte("content"), 0o640))

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "content", string(got))

	fi, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), fi.Mode().Perm())

	entries, err := os.ReadDir(filepath.Dir(target))
	require.NoError(t, err)
	require.Len(t, entries, 1, "the temp file must be gone, and it must have been a sibling")
}

// Phase 5: an interrupted write leaves no partial target.
func TestAtomicWriteLeavesNoPartialTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// A directory where the target should be makes the rename fail after the temp write.
	target := filepath.Join(dir, "target")
	require.NoError(t, os.MkdirAll(filepath.Join(target, "occupied"), 0o755))

	require.Error(t, AtomicWrite(target, []byte("content"), 0o644))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no stray temp file may survive a failed write")
	require.Equal(t, "target", entries[0].Name())
	require.DirExists(t, filepath.Join(target, "occupied"), "the existing target is untouched")
}

func TestAtomicWriteReplacesExistingContentInPlace(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o600))
	require.NoError(t, AtomicWrite(target, []byte("new"), 0o600))

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "new", string(got))
}

func TestAtomicWriteZeroModeKeepsTheDefault(t *testing.T) {
	t.Parallel()
	target := filepath.Join(t.TempDir(), "file")
	require.NoError(t, AtomicWrite(target, []byte("x"), 0))
	require.FileExists(t, target)
}

func TestAtomicWriteIntoAnUnwritableParent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	require.NoError(t, os.Mkdir(locked, 0o500))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	err := AtomicWrite(filepath.Join(locked, "file"), []byte("x"), 0o600)
	require.Error(t, err)
}

func TestCopyTreePreservesSymlinksAndModes(t *testing.T) {
	t.Parallel()
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "copy")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "nested", "file"), []byte("x"), 0o640))
	require.NoError(t, os.Symlink("/elsewhere", filepath.Join(src, "link")))

	require.NoError(t, os.MkdirAll(dst, 0o755))
	require.NoError(t, copyTree(src, dst))

	fi, err := os.Stat(filepath.Join(dst, "nested", "file"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), fi.Mode().Perm())

	link, err := os.Readlink(filepath.Join(dst, "link"))
	require.NoError(t, err)
	require.Equal(t, "/elsewhere", link, "a symlink must be copied as a symlink, not followed")
}

func TestAtomicCopyDirReplacesTheTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	target := filepath.Join(root, "target")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "new.txt"), []byte("new"), 0o644))
	require.NoError(t, os.MkdirAll(target, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(target, "stale.txt"), []byte("stale"), 0o644))

	require.NoError(t, atomicCopyDir(src, target))
	require.FileExists(t, filepath.Join(target, "new.txt"))
	require.NoFileExists(t, filepath.Join(target, "stale.txt"), "the old tree must be replaced, not merged")

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.HasPrefix(e.Name(), atomicDirPrefix), "no temp directory may survive")
	}
}

func TestRemoveAny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	tree := filepath.Join(dir, "tree")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(tree, "nested"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(dir, "nowhere"), link))

	for _, p := range []string{file, tree, link, filepath.Join(dir, "absent")} {
		require.NoError(t, removeAny(p), p)
	}
	require.NoFileExists(t, file)
	require.NoDirExists(t, tree)
	_, err := os.Lstat(link)
	require.Error(t, err, "a dangling symlink must be removed, not skipped")
}
