//go:build unix

package permissions

import (
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseAcceptsBothSpellings(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]fs.FileMode{
		"0644":  0o644,
		"0600":  0o600,
		"0o644": 0o644, // the spelling Python's oct() writes into a journal
		"0o640": 0o640,
		"0O600": 0o600,
		"0o0":   0,
	} {
		got, err := Parse(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
}

func TestParseRejectsEverythingElse(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"644", "rwx", "", "0999", "0o", "0x644", "012345", "0"} {
		_, err := Parse(in)
		require.ErrorIs(t, err, ErrInvalidMode, in)
	}
}

func TestFormat(t *testing.T) {
	t.Parallel()
	require.Equal(t, "0o644", Format(0o644), "the journal spelling must match Python's oct()")
	require.Equal(t, "0o600", Format(0o600))
	require.Equal(t, "0644", FormatManifest(0o644))
	require.Equal(t, "0600", FormatManifest(0o600))
	require.Equal(t, "0000", FormatManifest(0))
}

func TestEnforceAndVerify(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	require.NoError(t, Enforce(path, "0600", nil))
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o600), fi.Mode().Perm())

	ok, err := Verify(path, "0600")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = Verify(path, "0644")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestEnforceAcceptsTheJournalSpelling(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
	require.NoError(t, Enforce(path, "0o640", nil), "rollback replays modes recorded by Python")

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o640), fi.Mode().Perm())
}

// chmod follows a symlink, which would change the mode of whatever it points at rather than the
// link rv just created.
func TestEnforceDoesNotChmodThroughASymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.WriteFile(dest, []byte("x"), 0o644))
	require.NoError(t, os.Symlink(dest, link))

	require.NoError(t, Enforce(link, "0600", nil))
	fi, err := os.Stat(dest)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o644), fi.Mode().Perm(), "the destination's mode must be untouched")

	ok, err := Verify(link, "0600")
	require.NoError(t, err)
	require.True(t, ok, "a symlink's own mode is not meaningful, so verification passes")
}

func TestEnforceOnAMissingPath(t *testing.T) {
	t.Parallel()
	err := Enforce(filepath.Join(t.TempDir(), "absent"), "0600", nil)
	require.ErrorIs(t, err, ErrEnforce)

	_, err = Verify(filepath.Join(t.TempDir(), "absent"), "0600")
	require.ErrorIs(t, err, ErrEnforce)
}

func TestEnforceRejectsBadModes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
	require.ErrorIs(t, Enforce(path, "644", nil), ErrInvalidMode)

	_, err := Verify(path, "rwx")
	require.ErrorIs(t, err, ErrInvalidMode)
}

// A nonexistent user is a validation error, distinct from an operating-system refusal.
func TestUnknownOwnerIsAValidationError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	owner := "rv-no-such-user-xyz"
	err := Enforce(path, "0600", &owner)
	require.ErrorIs(t, err, ErrUnknownUser)
	require.NotErrorIs(t, err, ErrEnforce, "a bad manifest is not an OS failure")
}

func TestOwnerIsOptional(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	empty := ""
	require.NoError(t, Enforce(path, "0600", &empty))
	require.NoError(t, Enforce(path, "0600", nil))
}

// Setting the current user as owner is a no-op that must still succeed, which is the only chown
// an unprivileged test can make.
func TestEnforceOwnerToSelf(t *testing.T) {
	t.Parallel()
	u, err := user.Current()
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
	require.NoError(t, Enforce(path, "0600", &u.Username))
}

func TestVerifyMatchesBothSpellings(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o640))

	for _, spelling := range []string{"0640", "0o640"} {
		ok, err := Verify(path, spelling)
		require.NoError(t, err, spelling)
		require.True(t, ok, spelling)
	}
}

func TestEnforceOwnerOnASymlinkDoesNotFollowIt(t *testing.T) {
	t.Parallel()
	u, err := user.Current()
	require.NoError(t, err)

	dir := t.TempDir()
	dest := filepath.Join(dir, "dest")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.WriteFile(dest, []byte("x"), 0o644))
	require.NoError(t, os.Symlink(dest, link))

	require.NoError(t, Enforce(link, "0600", &u.Username))
	fi, err := os.Stat(dest)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0o644), fi.Mode().Perm())
}

func TestEnforceOwnerOnAMissingPath(t *testing.T) {
	t.Parallel()
	u, err := user.Current()
	require.NoError(t, err)
	require.Error(t, enforceOwner(filepath.Join(t.TempDir(), "absent"), &u.Username))
}
