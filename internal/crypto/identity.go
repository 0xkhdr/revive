package crypto

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
)

// Key prefixes. A value carrying one of these is a literal key, not a path.
const (
	identityPrefix  = "AGE-SECRET-KEY-1"
	recipientPrefix = "age1"
)

// ErrKeyNotFound is returned when a key file exists but contains no key of the expected kind.
var ErrKeyNotFound = errors.New("no age key found in file")

// ResolveIdentity turns an identity value into an `AGE-SECRET-KEY-1…` literal. The value is
// either the literal itself or a path to a file containing one.
//
// Identity files written by `rv secret keygen` carry a `# public key:` comment, so the file is
// scanned line by line rather than read whole.
func ResolveIdentity(value string) (string, error) {
	return resolveKey(value, identityPrefix, "identity")
}

// ResolveRecipient turns a recipient value into an `age1…` literal, from the literal itself or
// from a file containing one.
func ResolveRecipient(value string) (string, error) {
	return resolveKey(value, recipientPrefix, "recipient")
}

func resolveKey(value, prefix, kind string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%w: empty %s", ErrBadKey, kind)
	}
	// An identity literal also starts with "age" — check the longer prefix first so an identity
	// is never mistaken for a recipient.
	if strings.HasPrefix(value, identityPrefix) {
		if prefix != identityPrefix {
			return "", fmt.Errorf("%w: a private key was given where a %s was expected", ErrBadKey, kind)
		}
		return value, nil
	}
	if strings.HasPrefix(value, prefix) {
		return value, nil
	}
	if !looksLikePath(value) {
		return "", fmt.Errorf("%w: %q is neither an age %s nor a path", ErrBadKey, value, kind)
	}

	f, err := os.Open(value)
	if err != nil {
		// A path that does not exist is an error, never a fallthrough to "treat as a literal".
		return "", fmt.Errorf("reading age %s file: %w", kind, err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); strings.HasPrefix(line, prefix) {
			return line, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("reading age %s file %s: %w", kind, value, err)
	}
	return "", fmt.Errorf("%w: %s in %s", ErrKeyNotFound, kind, value)
}

func looksLikePath(v string) bool {
	return strings.ContainsRune(v, filepath.Separator) || strings.HasPrefix(v, ".") || filepath.IsAbs(v)
}

// PublicKeyFromIdentity returns the `age1…` recipient for an identity, given as a literal or as
// a path to an identity file.
//
// The `# public key:` comment is tried first as a fast path; deriving from the private key is
// the authority, and the two must agree.
func PublicKeyFromIdentity(value string) (string, error) {
	if pub, ok := publicKeyComment(value); ok {
		return pub, nil
	}
	key, err := ResolveIdentity(value)
	if err != nil {
		return "", err
	}
	id, err := age.ParseX25519Identity(key)
	if err != nil {
		return "", fmt.Errorf("%w: identity: %w", ErrBadKey, err)
	}
	return id.Recipient().String(), nil
}

// publicKeyComment scans an identity file for the `# public key: age1…` line keygen writes.
func publicKeyComment(value string) (string, bool) {
	if !looksLikePath(value) {
		return "", false
	}
	f, err := os.Open(value)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		_, rest, found := strings.Cut(line, "public key:")
		if !strings.HasPrefix(line, "#") || !found {
			continue
		}
		if pub := strings.TrimSpace(rest); strings.HasPrefix(pub, recipientPrefix) {
			return pub, true
		}
	}
	return "", false
}

// WriteIdentityFile writes an identity file in the shape `rv secret keygen` produces: a public
// key comment, the private key, and mode 0600 from creation.
func WriteIdentityFile(path, pub, identity string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating identity file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "# created by rv\n# public key: %s\n%s\n", pub, identity); err != nil {
		return fmt.Errorf("writing identity file: %w", err)
	}
	return nil
}
