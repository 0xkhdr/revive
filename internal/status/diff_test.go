package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/scrub"
)

func TestDiffPlainCopy(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/conf", "line one\nline two\nline three\n")
	target := f.target("conf", "line one\nedited\nline three\n", 0o644)

	d, err := f.checker().DiffAsset(asset("conf", func(a *manifest.Asset) {
		a.Source, a.Target = "assets/conf", manifest.Scalar(target)
	}), target)
	require.NoError(t, err)
	require.Equal(t, "line one\nline two\nline three\n", d.Expected)
	require.Equal(t, "line one\nedited\nline three\n", d.Actual)

	unified := d.Unified(scrub.New())
	require.Contains(t, unified, "- line two")
	require.Contains(t, unified, "+ edited")
	require.Contains(t, unified, "  line one")
}

func TestDiffTemplateUsesTheRenderedOutput(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/t.tmpl", "email = {{ .email }}\n")
	target := f.target("out", "email = stale@example.com\n", 0o644)

	d, err := f.checker().DiffAsset(asset("t", func(a *manifest.Asset) {
		a.Type, a.Source = manifest.TypeTemplate, "assets/t.tmpl"
		a.Target = manifest.Scalar(target)
		a.TemplateVars = map[string]any{"email": "dev@example.com"}
	}), target)
	require.NoError(t, err)
	require.Equal(t, "email = dev@example.com\n", d.Expected)
}

func TestDiffEncryptedUsesThePlaintext(t *testing.T) {
	t.Parallel()
	f := newFixture(t).withIdentity()
	f.encrypt("secrets/env.age", "TOKEN=expected\n")
	target := f.target(".env", "TOKEN=actual\n", 0o600)

	d, err := f.checker().DiffAsset(asset("env", func(a *manifest.Asset) {
		a.Type, a.Encrypted = manifest.TypeSecret, true
		a.Source, a.Target = "secrets/env.age", manifest.Scalar(target)
	}), target)
	require.NoError(t, err)
	require.Equal(t, "TOKEN=expected\n", d.Expected)
	require.Empty(t, d.Note)
}

// Without an identity, an explanatory placeholder beats an empty diff that reads as "no changes".
func TestDiffEncryptedWithoutAnIdentityExplains(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("secrets/env.age", "ciphertext")
	target := f.target(".env", "TOKEN=actual\n", 0o600)

	d, err := f.checker().DiffAsset(asset("env", func(a *manifest.Asset) {
		a.Type, a.Encrypted = manifest.TypeSecret, true
		a.Source, a.Target = "secrets/env.age", manifest.Scalar(target)
	}), target)
	require.NoError(t, err)
	require.Empty(t, d.Expected)
	require.Contains(t, d.Note, "--identity")
	require.Contains(t, d.Unified(scrub.New()), "--identity")
}

func TestDiffUndecryptableSourceExplains(t *testing.T) {
	t.Parallel()
	f := newFixture(t).withIdentity()
	f.source("secrets/env.age", "not age ciphertext")
	target := f.target(".env", "TOKEN=actual\n", 0o600)

	d, err := f.checker().DiffAsset(asset("env", func(a *manifest.Asset) {
		a.Type, a.Encrypted = manifest.TypeSecret, true
		a.Source, a.Target = "secrets/env.age", manifest.Scalar(target)
	}), target)
	require.NoError(t, err)
	require.Contains(t, d.Note, "could not be decrypted")
}

// Phase 10: diff skips symlinks and binaries.
func TestDiffSkips(t *testing.T) {
	t.Parallel()

	t.Run("symlink asset", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		source := f.source("assets/conf", "content\n")
		link := filepath.Join(f.home, "conf")
		require.NoError(t, os.Symlink(source, link))

		_, err := f.checker().DiffAsset(asset("conf", func(a *manifest.Asset) {
			a.Type, a.Source, a.Target = manifest.TypeSymlink, "assets/conf", manifest.Scalar(link)
		}), link)
		require.ErrorIs(t, err, ErrNoDiff)
	})

	t.Run("symlink target", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		source := f.source("assets/conf", "content\n")
		link := filepath.Join(f.home, "conf")
		require.NoError(t, os.Symlink(source, link))

		_, err := f.checker().DiffAsset(asset("conf", func(a *manifest.Asset) {
			a.Source, a.Target = "assets/conf", manifest.Scalar(link)
		}), link)
		require.ErrorIs(t, err, ErrNoDiff)
	})

	t.Run("binary target", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/bin", "text\n")
		target := filepath.Join(f.home, "bin")
		require.NoError(t, os.WriteFile(target, []byte("\x7fELF\x00\x01binary"), 0o644))

		_, err := f.checker().DiffAsset(asset("bin", func(a *manifest.Asset) {
			a.Source, a.Target = "assets/bin", manifest.Scalar(target)
		}), target)
		require.ErrorIs(t, err, ErrNoDiff)
	})

	t.Run("binary source", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "assets"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(f.repo, "assets", "bin"), []byte("\x00\x01\x02"), 0o644))
		target := f.target("bin", "text\n", 0o644)

		_, err := f.checker().DiffAsset(asset("bin", func(a *manifest.Asset) {
			a.Source, a.Target = "assets/bin", manifest.Scalar(target)
		}), target)
		require.ErrorIs(t, err, ErrNoDiff)
	})

	t.Run("missing target", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/conf", "content\n")
		absent := filepath.Join(f.home, "absent")

		_, err := f.checker().DiffAsset(asset("conf", func(a *manifest.Asset) {
			a.Source, a.Target = "assets/conf", manifest.Scalar(absent)
		}), absent)
		require.ErrorIs(t, err, ErrNoDiff)
	})
}

// Phase 10: diff output passes through the scrubber. This is the single most likely place for a
// secret to reach a terminal.
func TestDiffOutputIsScrubbed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/conf", "TOKEN=hunter2secret\n")
	target := f.target("conf", "TOKEN=different\n", 0o644)

	s := scrub.New()
	s.RegisterSecret("hunter2secret")

	d, err := f.checker().DiffAsset(asset("conf", func(a *manifest.Asset) {
		a.Source, a.Target = "assets/conf", manifest.Scalar(target)
	}), target)
	require.NoError(t, err)

	require.NotContains(t, d.Unified(s), "hunter2secret")
	require.Contains(t, d.Unified(s), scrub.Redacted)
	require.NotContains(t, d.SideBySide(s, 120), "hunter2secret")

	// An age key is caught by the static patterns even when nothing was registered.
	f2 := newFixture(t)
	f2.source("assets/key", "AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQ\n")
	target2 := f2.target("key", "different\n", 0o644)
	d2, err := f2.checker().DiffAsset(asset("key", func(a *manifest.Asset) {
		a.Source, a.Target = "assets/key", manifest.Scalar(target2)
	}), target2)
	require.NoError(t, err)
	require.NotContains(t, d2.Unified(scrub.New()), "AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQ")
}

func TestSideBySideRendering(t *testing.T) {
	t.Parallel()
	d := &Diff{
		AssetID:  "conf",
		Target:   "/home/user/conf",
		Expected: "same\nexpected only\n",
		Actual:   "same\nactual only\n",
	}
	out := d.SideBySide(scrub.New(), 80)
	require.Contains(t, out, "expected: conf")
	require.Contains(t, out, "actual: /home/user/conf")
	require.Contains(t, out, "expected only")
	require.Contains(t, out, "actual only")
	require.Contains(t, out, "<", "a deletion is marked")
	require.Contains(t, out, ">", "an insertion is marked")

	// A long line is truncated rather than wrapping and destroying the columns.
	wide := &Diff{Expected: strings.Repeat("x", 500) + "\n", Actual: "short\n"}
	for _, line := range strings.Split(wide.SideBySide(scrub.New(), 80), "\n") {
		require.LessOrEqual(t, len([]rune(line)), 90, line)
	}
}

func TestLineDiff(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		before, after   []string
		equal, del, ins int
	}{
		"identical":     {[]string{"a", "b"}, []string{"a", "b"}, 2, 0, 0},
		"one changed":   {[]string{"a", "b", "c"}, []string{"a", "x", "c"}, 2, 1, 1},
		"insert":        {[]string{"a", "c"}, []string{"a", "b", "c"}, 2, 0, 1},
		"delete":        {[]string{"a", "b", "c"}, []string{"a", "c"}, 2, 1, 0},
		"empty before":  {nil, []string{"a"}, 0, 0, 1},
		"empty after":   {[]string{"a"}, nil, 0, 1, 0},
		"both empty":    {nil, nil, 0, 0, 0},
		"all different": {[]string{"a"}, []string{"b"}, 0, 1, 1},
		"reordered":     {[]string{"a", "b"}, []string{"b", "a"}, 1, 1, 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var equal, del, ins int
			for _, o := range lineDiff(tc.before, tc.after) {
				switch o.kind {
				case opEqual:
					equal++
				case opDelete:
					del++
				case opInsert:
					ins++
				}
			}
			require.Equal(t, tc.equal, equal, "equal")
			require.Equal(t, tc.del, del, "deleted")
			require.Equal(t, tc.ins, ins, "inserted")
		})
	}
}

// The edit script must reconstruct both sides exactly, which is the real correctness property.
func TestLineDiffReconstructsBothSides(t *testing.T) {
	t.Parallel()
	before := []string{"one", "two", "three", "four", "five"}
	after := []string{"one", "TWO", "three", "five", "six"}

	var gotBefore, gotAfter []string
	for _, o := range lineDiff(before, after) {
		switch o.kind {
		case opEqual:
			gotBefore = append(gotBefore, o.text)
			gotAfter = append(gotAfter, o.text)
		case opDelete:
			gotBefore = append(gotBefore, o.text)
		case opInsert:
			gotAfter = append(gotAfter, o.text)
		}
	}
	require.Equal(t, before, gotBefore)
	require.Equal(t, after, gotAfter)
}

// Beyond the cap the diff degrades to "whole file replaced" rather than allocating an n*m table.
func TestLineDiffDegradesOnHugeInput(t *testing.T) {
	t.Parallel()
	before := make([]string, maxDiffLines+10)
	after := make([]string, maxDiffLines+10)
	for i := range before {
		before[i] = "before line"
		after[i] = "after line"
	}

	ops := lineDiff(before, after)
	require.NotEmpty(t, ops)
	for _, o := range ops {
		require.NotEqual(t, opEqual, o.kind)
	}
}

func TestSplitLines(t *testing.T) {
	t.Parallel()
	require.Nil(t, splitLines(""))
	require.Equal(t, []string{"a"}, splitLines("a\n"))
	require.Equal(t, []string{"a", "b"}, splitLines("a\nb\n"))
	require.Equal(t, []string{"a", "b"}, splitLines("a\nb"))
	require.Equal(t, []string{"a", ""}, splitLines("a\n\n"))
}

func TestIsBinary(t *testing.T) {
	t.Parallel()
	require.False(t, isBinary([]byte("plain text\n")))
	require.False(t, isBinary(nil))
	require.True(t, isBinary([]byte("text\x00more")))
	// A NUL beyond the sniffed window is not detected, matching git's heuristic.
	require.False(t, isBinary(append(make([]byte, 0, 9000), append([]byte(strings.Repeat("a", 8500)), 0)...)))
}

func TestScrubTextFallsBackToTheDefaultScrubber(t *testing.T) {
	scrub.Default.Clear()
	t.Cleanup(scrub.Default.Clear)
	scrub.RegisterSecret("package-level-secret")

	d := &Diff{Expected: "package-level-secret\n", Actual: "other\n"}
	require.NotContains(t, d.Unified(nil), "package-level-secret")
}
