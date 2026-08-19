package transaction

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 5 INTEROP GATE: read a journal written by the reference Python implementation and roll
// it back successfully.
//
// The fixture — journal and backups alike — is genuine Python output; only the absolute path
// prefix was replaced with {{ROOT}} so it can be relocated into t.TempDir(). See the fixture's
// README for how it was produced.
func TestInteropRollbackPythonJournal(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stagePythonFixture(t, root)

	targets := filepath.Join(root, "targets")
	sources := filepath.Join(root, "sources")

	// Recreate the post-mutation state the Python transaction left behind.
	require.NoError(t, os.MkdirAll(sources, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sources, "linked"), []byte("link destination\n"), 0o664))
	require.NoError(t, os.WriteFile(filepath.Join(sources, "new.conf"), []byte("REPLACED\n"), 0o664))
	require.NoError(t, os.MkdirAll(targets, 0o775))
	require.NoError(t, os.WriteFile(filepath.Join(targets, "file.conf"), []byte("REPLACED\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(sources, "new.conf"), filepath.Join(targets, "link")))
	require.NoError(t, os.MkdirAll(filepath.Join(targets, "dir"), 0o775))
	require.NoError(t, os.WriteFile(filepath.Join(targets, "dir", "other.txt"), []byte("replacement tree\n"), 0o664))
	require.NoError(t, os.WriteFile(filepath.Join(targets, "fresh.conf"), []byte("REPLACED\n"), 0o600))

	j, err := LoadJournal(filepath.Join(root, "journals", "interop-fixture.json"))
	require.NoError(t, err)
	require.Equal(t, "interop-fixture", j.TxID)
	require.False(t, j.Complete(), "the fixture is a crashed run, which is what recovery is for")
	require.Len(t, j.Entries, 4)
	require.Equal(t, "0o640", *j.Entries[0].Permissions, "Python's oct() spelling must parse")

	require.NoError(t, RollbackJournal(j))
	require.Equal(t, StatusRolledBack, j.Status)

	// The regular file is back, with its original content and its original mode.
	got, err := os.ReadFile(filepath.Join(targets, "file.conf"))
	require.NoError(t, err)
	require.Equal(t, "original file contents\n", string(got))
	fi, err := os.Stat(filepath.Join(targets, "file.conf"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), fi.Mode().Perm())

	// The symlink is back as a symlink, pointing where it originally did — the SYMLINK:<target>
	// backup format round-trips.
	fi, err = os.Lstat(filepath.Join(targets, "link"))
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&fs.ModeSymlink)
	link, err := os.Readlink(filepath.Join(targets, "link"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(sources, "linked"), link)

	// The directory is back as a directory, with its original contents and not the replacement.
	got, err = os.ReadFile(filepath.Join(targets, "dir", "nested", "inner.txt"))
	require.NoError(t, err)
	require.Equal(t, "original nested contents\n", string(got))
	require.NoFileExists(t, filepath.Join(targets, "dir", "other.txt"))

	// The target that did not exist before the transaction is gone again.
	require.NoFileExists(t, filepath.Join(targets, "fresh.conf"))
}

// stagePythonFixture copies the committed fixture into root, substituting the {{ROOT}} token
// with root in the journal and in the backups that reference paths.
func stagePythonFixture(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "journals"), 0o755))

	journal, err := os.ReadFile(filepath.Join(pythonJournalFixture, "journal.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "journals", "interop-fixture.json"),
		[]byte(strings.ReplaceAll(string(journal), "{{ROOT}}", root)), 0o600))

	src := filepath.Join(pythonJournalFixture, "backups")
	require.NoError(t, filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(root, "backups", rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(out, info.Mode().Perm())
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(out, []byte(strings.ReplaceAll(string(content), "{{ROOT}}", root)), info.Mode().Perm())
	}))
}
