package crypto

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const fixtureDir = "testdata/interop"

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtureDir, name))
	require.NoError(t, err)
	return string(b)
}

// Phase 4: Encrypt then Decrypt round-trips.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	pub, identity, err := GenerateKeypair()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(pub, "age1"))
	require.True(t, strings.HasPrefix(identity, "AGE-SECRET-KEY-1"))

	plaintext := []byte("API_TOKEN=sk-live-0123456789\n")
	ciphertext, err := Encrypt(plaintext, []string{pub})
	require.NoError(t, err)
	require.NotContains(t, string(ciphertext), "sk-live", "the ciphertext must not carry the plaintext")

	got, err := Decrypt(ciphertext, identity)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)
}

func TestRoundTripMultipleRecipients(t *testing.T) {
	t.Parallel()
	pubA, idA, err := GenerateKeypair()
	require.NoError(t, err)
	pubB, idB, err := GenerateKeypair()
	require.NoError(t, err)

	ciphertext, err := Encrypt([]byte("shared"), []string{pubA, pubB})
	require.NoError(t, err)
	for _, id := range []string{idA, idB} {
		got, err := Decrypt(ciphertext, id)
		require.NoError(t, err)
		require.Equal(t, []byte("shared"), got)
	}

	_, wrongID, err := GenerateKeypair()
	require.NoError(t, err)
	_, err = Decrypt(ciphertext, wrongID)
	require.Error(t, err, "an unlisted identity must not decrypt")
}

// Ciphertext produced by an earlier release remains decryptable. The fixture is committed, so
// this test has no dependency on that release's source or runtime.
func TestInteropPythonToGo(t *testing.T) {
	t.Parallel()
	want := []byte(fixture(t, "plaintext.env"))

	ciphertext, err := os.ReadFile(filepath.Join(fixtureDir, "python_encrypted.age"))
	require.NoError(t, err)

	// Decrypting through the identity *file* also exercises the `# public key:` comment line.
	got, err := Decrypt(ciphertext, filepath.Join(fixtureDir, "identity.txt"))
	require.NoError(t, err)
	require.Equal(t, want, got)

	// The multi-recipient fixture decrypts with either identity.
	ciphertext, err = os.ReadFile(filepath.Join(fixtureDir, "python_encrypted_multi.age"))
	require.NoError(t, err)
	for _, id := range []string{"identity.txt", "identity2.txt"} {
		got, err := Decrypt(ciphertext, filepath.Join(fixtureDir, id))
		require.NoError(t, err, id)
		require.Equal(t, want, got, id)
	}
}

func TestEncryptRequiresRecipients(t *testing.T) {
	t.Parallel()
	_, err := Encrypt([]byte("x"), nil)
	require.ErrorIs(t, err, ErrNoRecipients)
}

func TestDecryptRequiresIdentity(t *testing.T) {
	t.Parallel()
	_, err := Decrypt([]byte("x"), "")
	require.ErrorIs(t, err, ErrIdentityRequired)
}

func TestMalformedKeysAreTyped(t *testing.T) {
	t.Parallel()
	_, err := Encrypt([]byte("x"), []string{"age1notarealkey"})
	require.ErrorIs(t, err, ErrBadKey)
	_, err = Decrypt([]byte("x"), "AGE-SECRET-KEY-1NOTAREALKEY")
	require.ErrorIs(t, err, ErrBadKey)
}

func TestDecryptRejectsGarbage(t *testing.T) {
	t.Parallel()
	_, identity, err := GenerateKeypair()
	require.NoError(t, err)
	_, err = Decrypt([]byte("not an age file"), identity)
	require.Error(t, err)
}

func TestZero(t *testing.T) {
	t.Parallel()
	b := []byte("secret")
	Zero(b)
	require.Equal(t, make([]byte, 6), b)
	require.NotPanics(t, func() { Zero(nil) })
}

func TestPlaintextIsNotRetained(t *testing.T) {
	t.Parallel()
	pub, identity, err := GenerateKeypair()
	require.NoError(t, err)
	ciphertext, err := Encrypt([]byte("hunter2"), []string{pub})
	require.NoError(t, err)

	plaintext, err := Decrypt(ciphertext, identity)
	require.NoError(t, err)
	Zero(plaintext)
	require.False(t, bytes.Contains(plaintext, []byte("hunter2")))
}

func TestEncryptRejectsARecipientPathThatIsMissing(t *testing.T) {
	t.Parallel()
	_, err := Encrypt([]byte("x"), []string{filepath.Join(t.TempDir(), "absent.txt")})
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDecryptRejectsAnIdentityPathThatIsMissing(t *testing.T) {
	t.Parallel()
	_, err := Decrypt([]byte("x"), filepath.Join(t.TempDir(), "absent.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestEncryptToAFileRecipient(t *testing.T) {
	t.Parallel()
	pub, identity, err := GenerateKeypair()
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "recipient.txt")
	require.NoError(t, os.WriteFile(path, []byte("# a comment\n"+pub+"\n"), 0o600))

	ciphertext, err := Encrypt([]byte("payload"), []string{path})
	require.NoError(t, err)
	got, err := Decrypt(ciphertext, identity)
	require.NoError(t, err)
	require.Equal(t, []byte("payload"), got)
}
