// Package scrub redacts secrets from text before it is emitted. Every output channel — console,
// audit log, error messages — passes through it; there is no unscrubbed path.
package scrub

import (
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Redacted replaces every match.
const Redacted = "[REDACTED]"

// minSecretLength is the shortest string worth registering. Shorter values match everywhere and
// redact ordinary words.
const minSecretLength = 5

// staticPatterns are compiled once and applied to every line.
var staticPatterns = []*regexp.Regexp{
	// age private keys.
	regexp.MustCompile(`(?i)AGE-SECRET-KEY-1[a-zA-Z0-9]+`),
	// SSH public keys.
	regexp.MustCompile(`(?i)(?:ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp256)\s+[a-zA-Z0-9+/=]+`),
	// PEM private key blocks.
	//
	// docs/05 §2 lists `-----BEGIN\s+(?:RSA|OPENSSH|PRIVATE)\s+KEY-----…` verbatim, copied from
	// the Python implementation. That pattern cannot match the two commonest real headers,
	// `BEGIN RSA PRIVATE KEY` and `BEGIN OPENSSH PRIVATE KEY`, because it requires KEY to follow
	// the algorithm directly. The acceptance criterion — "the scrubber redacts a PEM block" —
	// outranks the pattern listing, so the algorithm segment is optional here instead.
	regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----[^-]+-----END [A-Z0-9 ]*PRIVATE KEY-----`),
}

// Scrubber holds the dynamic registry of secrets discovered at runtime.
//
// Parallel asset planning logs concurrently, so the registry is mutex-guarded rather than the
// bare package-level set the Python implementation used.
type Scrubber struct {
	mu sync.RWMutex
	// secrets is kept sorted by length descending, so a secret that is a prefix of another is
	// substituted after the longer one. The other order leaves "[REDACTED]<tail>" on screen.
	secrets []string
}

// New returns an empty scrubber.
func New() *Scrubber { return &Scrubber{} }

// Default is the process-wide scrubber. The identity private key is registered here as soon as
// it is read, before anything else can log.
var Default = New()

// RegisterSecret adds a literal secret to the registry. Strings shorter than 5 characters are
// ignored as false-positive bait.
func (s *Scrubber) RegisterSecret(secret string) {
	if len(secret) < minSecretLength {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Contains(s.secrets, secret) {
		return
	}
	s.secrets = append(s.secrets, secret)
	slices.SortStableFunc(s.secrets, func(a, b string) int { return len(b) - len(a) })
}

// Clear empties the dynamic registry. Tests use it; production code does not.
func (s *Scrubber) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secrets = nil
}

// Scrub redacts every static pattern and every registered secret in text.
func (s *Scrubber) Scrub(text string) string {
	if text == "" {
		return text
	}
	for _, p := range staticPatterns {
		text = p.ReplaceAllLiteralString(text, Redacted)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, secret := range s.secrets {
		text = strings.ReplaceAll(text, secret, Redacted)
	}
	return text
}

// RegisterSecret registers a secret with the process-wide scrubber.
func RegisterSecret(secret string) { Default.RegisterSecret(secret) }

// Scrub redacts text with the process-wide scrubber.
func Scrub(text string) string { return Default.Scrub(text) }
