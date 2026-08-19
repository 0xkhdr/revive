package crypto

import (
	"bytes"
	"os"
	"os/exec"
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

// Phase 4 INTEROP GATE, Python -> Go: a file encrypted by the reference Python implementation
// decrypts here. The fixture is committed, so this runs with no Python installed.
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

// Phase 4 INTEROP GATE, Go -> Python: a file encrypted here decrypts under the reference Python
// implementation, driving its own AgeEncryptor class.
func TestInteropGoToPython(t *testing.T) {
	t.Parallel()
	repoRoot, err := filepath.Abs("../..")
	require.NoError(t, err)
	requirePythonReference(t, repoRoot)

	pub := strings.TrimSpace(fixture(t, "recipient.txt"))
	pub = pub[strings.LastIndex(pub, "age1"):]

	plaintext := []byte("GO_WROTE_THIS=yes\nTOKEN=sk-live-abcdef\n")
	ciphertext, err := Encrypt(plaintext, []string{pub})
	require.NoError(t, err)

	dir := t.TempDir()
	agePath := filepath.Join(dir, "go_encrypted.age")
	outPath := filepath.Join(dir, "decrypted.env")
	require.NoError(t, os.WriteFile(agePath, ciphertext, 0o600))

	identity, err := filepath.Abs(filepath.Join(fixtureDir, "identity.txt"))
	require.NoError(t, err)

	cmd := exec.Command("python3", "-c", `
import sys
sys.path.insert(0, sys.argv[1] + "/reference/src")
from rv.security.encryptor import AgeEncryptor
AgeEncryptor.decrypt_file(sys.argv[2], sys.argv[3], sys.argv[4])
`, repoRoot, agePath, outPath, identity)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "reference decrypt failed: %s", out)

	got, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Equal(t, plaintext, got)
}

// requirePythonReference fails rather than skips when the reference cannot run: a skipped
// interop gate is an unmet criterion wearing a green tick.
func requirePythonReference(t *testing.T, repoRoot string) {
	t.Helper()
	cmd := exec.Command("python3", "-c",
		"import sys; sys.path.insert(0, sys.argv[1] + '/reference/src'); import rv.security.encryptor",
		repoRoot)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "the reference Python implementation must be importable for the interop gate: %s", out)
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
