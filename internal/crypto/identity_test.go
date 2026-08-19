package crypto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 4: an identity file with a `# public key:` comment line parses correctly.
func TestResolveIdentityFromFileWithComment(t *testing.T) {
	t.Parallel()
	got, err := ResolveIdentity(filepath.Join(fixtureDir, "identity.txt"))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(got, "AGE-SECRET-KEY-1"))
	require.NotContains(t, got, "#", "the comment lines must not leak into the key")
}

// Phase 4: a recipient given as a file path and as a literal both work.
func TestResolveRecipient(t *testing.T) {
	t.Parallel()
	fromFile, err := ResolveRecipient(filepath.Join(fixtureDir, "recipient.txt"))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(fromFile, "age1"))

	literal, err := ResolveRecipient(fromFile)
	require.NoError(t, err)
	require.Equal(t, fromFile, literal)
}

func TestResolveIdentityLiteral(t *testing.T) {
	t.Parallel()
	_, identity, err := GenerateKeypair()
	require.NoError(t, err)
	got, err := ResolveIdentity(identity)
	require.NoError(t, err)
	require.Equal(t, identity, got)
}

// A path that does not exist is an error, never a fallthrough to "treat it as a literal".
func TestResolveMissingPathIsAnError(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "nope.txt")
	_, err := ResolveIdentity(missing)
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = ResolveRecipient(missing)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestResolveRejectsNonKeyNonPath(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "garbage"} {
		_, err := ResolveIdentity(v)
		require.ErrorIs(t, err, ErrBadKey, "%q", v)
	}
}

// A private key must never be accepted where a public one is expected.
func TestPrivateKeyIsNotARecipient(t *testing.T) {
	t.Parallel()
	_, identity, err := GenerateKeypair()
	require.NoError(t, err)
	_, err = ResolveRecipient(identity)
	require.ErrorIs(t, err, ErrBadKey)
}

func TestResolveFileWithoutAKey(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "empty.txt")
	require.NoError(t, os.WriteFile(path, []byte("# just a comment\n"), 0o600))
	_, err := ResolveIdentity(path)
	require.ErrorIs(t, err, ErrKeyNotFound)
}

// Phase 4: PublicKeyFromIdentity returns the same key from the comment path and the derivation
// path.
func TestPublicKeyFromIdentityAgreesWithDerivation(t *testing.T) {
	t.Parallel()
	fromComment, err := PublicKeyFromIdentity(filepath.Join(fixtureDir, "identity.txt"))
	require.NoError(t, err)

	// identity2.txt has no comment line, so this exercises derivation from the private key.
	derived, err := PublicKeyFromIdentity(filepath.Join(fixtureDir, "identity2.txt"))
	require.NoError(t, err)
	require.NotEqual(t, fromComment, derived, "the fixtures are different keys")

	// Same key, both paths: strip the comment and compare.
	identity, err := ResolveIdentity(filepath.Join(fixtureDir, "identity.txt"))
	require.NoError(t, err)
	fromLiteral, err := PublicKeyFromIdentity(identity)
	require.NoError(t, err)
	require.Equal(t, fromComment, fromLiteral,
		"the comment fast path and the derivation path must agree")
}

func TestPublicKeyFromIdentityIgnoresAWrongComment(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "identity.txt")
	_, identity, err := GenerateKeypair()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("# public key: not-an-age-key\n"+identity+"\n"), 0o600))

	got, err := PublicKeyFromIdentity(path)
	require.NoError(t, err)
	derived, err := PublicKeyFromIdentity(identity)
	require.NoError(t, err)
	require.Equal(t, derived, got, "a malformed comment must fall through to derivation")
}

func TestPublicKeyFromIdentityErrors(t *testing.T) {
	t.Parallel()
	_, err := PublicKeyFromIdentity("garbage")
	require.ErrorIs(t, err, ErrBadKey)
	_, err = PublicKeyFromIdentity("AGE-SECRET-KEY-1NOTAREALKEY")
	require.ErrorIs(t, err, ErrBadKey)
}

func TestWriteIdentityFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "identity.txt")
	pub, identity, err := GenerateKeypair()
	require.NoError(t, err)
	require.NoError(t, WriteIdentityFile(path, pub, identity))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "an identity file must never be readable by others")

	got, err := PublicKeyFromIdentity(path)
	require.NoError(t, err)
	require.Equal(t, pub, got)

	require.Error(t, WriteIdentityFile(path, pub, identity), "an existing identity must not be clobbered")
}
