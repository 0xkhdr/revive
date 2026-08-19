package scrub

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 4: the scrubber redacts an AGE-SECRET-KEY-1 literal, an SSH public key, and a PEM block.
func TestStaticPatterns(t *testing.T) {
	t.Parallel()
	s := New()

	for name, tc := range map[string]struct{ in, leaked string }{
		"age key": {
			"identity is AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQ done",
			"AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQ",
		},
		"age key lowercase": {
			"age-secret-key-1qqqqqqqqqqqqqqqqq",
			"age-secret-key-1qqqqqqqqqqqqqqqqq",
		},
		"ssh ed25519": {
			"key: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample user@host",
			"AAAAC3NzaC1lZDI1NTE5AAAAIExample",
		},
		"ssh rsa": {
			"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAB",
			"AAAAB3NzaC1yc2EAAAADAQABAAAB",
		},
		"ecdsa": {
			"ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY=",
			"AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY=",
		},
		"pem block": {
			"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaAAAA\nsecretline\n-----END OPENSSH PRIVATE KEY-----",
			"b3BlbnNzaAAAA",
		},
		"pem rsa multiline": {
			"before\n-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----\nafter",
			"MIIEow",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := s.Scrub(tc.in)
			require.NotContains(t, got, tc.leaked)
			require.Contains(t, got, Redacted)
		})
	}
}

func TestPemScrubbingKeepsSurroundingText(t *testing.T) {
	t.Parallel()
	got := New().Scrub("before\n-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----\nafter")
	require.Equal(t, "before\n"+Redacted+"\nafter", got)
}

// Phase 4: a registered secret is redacted, and a secret that is a prefix of another is fully
// redacted — the length-descending ordering.
func TestDynamicRegistry(t *testing.T) {
	t.Parallel()
	s := New()
	s.RegisterSecret("hunter2secret")
	require.Equal(t, "password is "+Redacted, s.Scrub("password is hunter2secret"))

	s.RegisterSecret("hunter2secretLONGER")
	got := s.Scrub("value hunter2secretLONGER end")
	require.Equal(t, "value "+Redacted+" end", got,
		"the longer secret must be substituted first, or its tail leaks")
	require.NotContains(t, got, "LONGER")
}

func TestRegistrationOrderDoesNotMatter(t *testing.T) {
	t.Parallel()
	long := New()
	long.RegisterSecret("abcdefghij")
	long.RegisterSecret("abcde")

	short := New()
	short.RegisterSecret("abcde")
	short.RegisterSecret("abcdefghij")

	require.Equal(t, long.Scrub("x abcdefghij y"), short.Scrub("x abcdefghij y"))
	require.Equal(t, "x "+Redacted+" y", short.Scrub("x abcdefghij y"))
}

func TestShortSecretsAreIgnored(t *testing.T) {
	t.Parallel()
	s := New()
	s.RegisterSecret("abcd")
	s.RegisterSecret("")
	require.Equal(t, "abcd stays", s.Scrub("abcd stays"), "4 characters is false-positive bait")

	s.RegisterSecret("abcde")
	require.Equal(t, Redacted+" goes", s.Scrub("abcde goes"))
}

func TestDuplicateRegistration(t *testing.T) {
	t.Parallel()
	s := New()
	s.RegisterSecret("hunter2")
	s.RegisterSecret("hunter2")
	require.Equal(t, Redacted, s.Scrub("hunter2"))
}

func TestClear(t *testing.T) {
	t.Parallel()
	s := New()
	s.RegisterSecret("hunter2")
	s.Clear()
	require.Equal(t, "hunter2", s.Scrub("hunter2"))
}

func TestEmptyInput(t *testing.T) {
	t.Parallel()
	require.Empty(t, New().Scrub(""))
}

// Phase 4: concurrent RegisterSecret and Scrub calls do not race (go test -race).
func TestConcurrentUseIsRaceFree(t *testing.T) {
	t.Parallel()
	s := New()
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(2)
		go func() { defer wg.Done(); s.RegisterSecret(strings.Repeat("s", i+5)) }()
		go func() { defer wg.Done(); s.Scrub("a line with sssssssss in it") }()
	}
	wg.Wait()
	require.Equal(t, "a line with "+Redacted+" in it", s.Scrub("a line with sssssssss in it"))
}

func TestPackageLevelDefault(t *testing.T) {
	Default.Clear()
	t.Cleanup(Default.Clear)
	RegisterSecret("package-level-secret")
	require.Equal(t, Redacted, Scrub("package-level-secret"))
}
