package crypto

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"filippo.io/age"
)

// Sentinel errors.
var (
	// ErrIdentityRequired is returned when a decryption is attempted with no identity.
	ErrIdentityRequired = errors.New("age identity required to decrypt")
	// ErrNoRecipients is returned when an encryption is attempted with no recipients.
	ErrNoRecipients = errors.New("at least one age recipient is required to encrypt")
	// ErrBadKey covers an unparseable identity or recipient.
	ErrBadKey = errors.New("malformed age key")
)

// GenerateKeypair creates a new X25519 age keypair.
func GenerateKeypair() (pub, identity string, err error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", fmt.Errorf("generating age keypair: %w", err)
	}
	return id.Recipient().String(), id.String(), nil
}

// Encrypt encrypts plaintext to every recipient. Each recipient may be an `age1…` literal or a
// path to a file containing one.
//
// Encryption happens in-process; rv never hands plaintext to another program.
func Encrypt(plaintext []byte, recipients []string) ([]byte, error) {
	if len(recipients) == 0 {
		return nil, ErrNoRecipients
	}
	parsed := make([]age.Recipient, 0, len(recipients))
	for _, r := range recipients {
		key, err := ResolveRecipient(r)
		if err != nil {
			return nil, err
		}
		rec, err := age.ParseX25519Recipient(key)
		if err != nil {
			return nil, fmt.Errorf("%w: recipient: %w", ErrBadKey, err)
		}
		parsed = append(parsed, rec)
	}

	var out bytes.Buffer
	w, err := age.Encrypt(&out, parsed...)
	if err != nil {
		return nil, fmt.Errorf("starting age encryption: %w", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, fmt.Errorf("encrypting: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("finalizing age encryption: %w", err)
	}
	return out.Bytes(), nil
}

// Decrypt decrypts ciphertext with an identity, given as an `AGE-SECRET-KEY-1…` literal or as a
// path to an identity file.
//
// The returned plaintext is the caller's to zero once consumed.
func Decrypt(ciphertext []byte, identity string) ([]byte, error) {
	if identity == "" {
		return nil, ErrIdentityRequired
	}
	key, err := ResolveIdentity(identity)
	if err != nil {
		return nil, err
	}
	id, err := age.ParseX25519Identity(key)
	if err != nil {
		return nil, fmt.Errorf("%w: identity: %w", ErrBadKey, err)
	}

	r, err := age.Decrypt(bytes.NewReader(ciphertext), id)
	if err != nil {
		return nil, fmt.Errorf("decrypting: %w", err)
	}
	plaintext, err := io.ReadAll(r)
	if err != nil {
		Zero(plaintext)
		return nil, fmt.Errorf("reading decrypted content: %w", err)
	}
	return plaintext, nil
}
