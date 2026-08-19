package status

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/scrub"
)

// ErrNoDiff is returned when an asset has nothing to diff — a symlink, a binary, a missing
// target.
var ErrNoDiff = errors.New("nothing to diff")

// Diff is one asset's expected-versus-actual content.
type Diff struct {
	AssetID string
	Target  string
	// Expected is what the declaration would produce; Actual is what is on disk.
	Expected string
	Actual   string
	// Note explains a diff that could not be produced in full, such as a missing identity.
	Note string
}

// DiffAsset builds the expected and actual text for one target.
//
// Symlinks are skipped (there is no content to compare), as are binaries on either side.
func (c *Checker) DiffAsset(asset manifest.Asset, target string) (*Diff, error) {
	if asset.Type == manifest.TypeSymlink {
		return nil, fmt.Errorf("%w: %q is a symlink", ErrNoDiff, asset.ID)
	}

	fi, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s does not exist", ErrNoDiff, target)
	}
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s is a symlink", ErrNoDiff, target)
	}

	actual, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	if isBinary(actual) {
		return nil, fmt.Errorf("%w: %s is binary", ErrNoDiff, target)
	}

	source, err := c.Handler.ResolveSource(c.Handler.AbsSource(asset), target, asset.Encrypted)
	if err != nil {
		return nil, err
	}

	expected, note, err := c.expectedContent(asset, source)
	if err != nil {
		return nil, err
	}
	if expected != nil && isBinary(expected) {
		return nil, fmt.Errorf("%w: the source of %q is binary", ErrNoDiff, asset.ID)
	}

	return &Diff{
		AssetID:  asset.ID,
		Target:   target,
		Expected: string(expected),
		Actual:   string(actual),
		Note:     note,
	}, nil
}

// expectedContent produces what the declaration says the target should contain.
func (c *Checker) expectedContent(asset manifest.Asset, source string) ([]byte, string, error) {
	switch {
	case asset.Type == manifest.TypeTemplate:
		rendered, err := c.Handler.RenderAsset(asset, source)
		return rendered, "", err

	case asset.Encrypted:
		if c.Handler.Identity == "" {
			// An explanatory placeholder beats an empty diff that reads as "no changes".
			return nil, "the source is encrypted and no identity is available, so the expected " +
				"content cannot be shown; pass --identity", nil
		}
		plaintext, err := c.Handler.Decrypt(asset.ID, source)
		if err != nil {
			return nil, "the source could not be decrypted with this identity: " + err.Error(), nil
		}
		defer crypto.Zero(plaintext)
		// The bytes are copied because the originals are zeroed on return.
		return append([]byte(nil), plaintext...), "", nil

	default:
		content, err := os.ReadFile(source)
		return content, "", err
	}
}

// opKind is one line's role in a diff.
type opKind int

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

type op struct {
	kind opKind
	text string
}

// maxDiffLines caps the quadratic LCS table.
//
// ponytail: beyond this the diff degrades to "whole file replaced" rather than allocating an
// n*m table. Swap in Myers with a linear-space refinement if real config files ever get this
// big; they do not.
const maxDiffLines = 5000

// lineDiff computes a line-level edit script.
//
// Common prefix and suffix are trimmed first, which is what makes the quadratic middle small for
// the usual case of a config file with one changed line.
func lineDiff(before, after []string) []op {
	var ops []op

	start := 0
	for start < len(before) && start < len(after) && before[start] == after[start] {
		ops = append(ops, op{opEqual, before[start]})
		start++
	}
	b, a := before[start:], after[start:]

	var suffix []op
	for len(b) > 0 && len(a) > 0 && b[len(b)-1] == a[len(a)-1] {
		suffix = append([]op{{opEqual, b[len(b)-1]}}, suffix...)
		b, a = b[:len(b)-1], a[:len(a)-1]
	}

	if len(b) > maxDiffLines || len(a) > maxDiffLines {
		for _, line := range b {
			ops = append(ops, op{opDelete, line})
		}
		for _, line := range a {
			ops = append(ops, op{opInsert, line})
		}
		return append(ops, suffix...)
	}

	// Longest common subsequence over the differing middle.
	table := make([][]int, len(b)+1)
	for i := range table {
		table[i] = make([]int, len(a)+1)
	}
	for i := len(b) - 1; i >= 0; i-- {
		for j := len(a) - 1; j >= 0; j-- {
			if b[i] == a[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}

	i, j := 0, 0
	for i < len(b) && j < len(a) {
		switch {
		case b[i] == a[j]:
			ops = append(ops, op{opEqual, b[i]})
			i, j = i+1, j+1
		case table[i+1][j] >= table[i][j+1]:
			ops = append(ops, op{opDelete, b[i]})
			i++
		default:
			ops = append(ops, op{opInsert, a[j]})
			j++
		}
	}
	for ; i < len(b); i++ {
		ops = append(ops, op{opDelete, b[i]})
	}
	for ; j < len(a); j++ {
		ops = append(ops, op{opInsert, a[j]})
	}
	return append(ops, suffix...)
}

// Unified renders a standard unified diff.
//
// Output is scrubbed: a diff is the single most likely place for a secret to reach a terminal.
func (d *Diff) Unified(s *scrub.Scrubber) string {
	var out strings.Builder
	fmt.Fprintf(&out, "--- expected: %s\n+++ actual:   %s\n", d.AssetID, d.Target)
	if d.Note != "" {
		fmt.Fprintf(&out, "# %s\n", d.Note)
	}
	for _, o := range lineDiff(splitLines(d.Expected), splitLines(d.Actual)) {
		switch o.kind {
		case opEqual:
			fmt.Fprintf(&out, "  %s\n", o.text)
		case opDelete:
			fmt.Fprintf(&out, "- %s\n", o.text)
		case opInsert:
			fmt.Fprintf(&out, "+ %s\n", o.text)
		}
	}
	return scrubText(s, out.String())
}

// SideBySide renders the two versions in columns, which is the default rendering.
func (d *Diff) SideBySide(s *scrub.Scrubber, width int) string {
	const gutter = 3
	col := max((width-gutter)/2, 20)

	var out strings.Builder
	fmt.Fprintf(&out, "%-*s | %s\n", col, "expected: "+d.AssetID, "actual: "+d.Target)
	fmt.Fprintf(&out, "%s-+-%s\n", strings.Repeat("-", col), strings.Repeat("-", col))
	if d.Note != "" {
		fmt.Fprintf(&out, "# %s\n", d.Note)
	}

	for _, o := range lineDiff(splitLines(d.Expected), splitLines(d.Actual)) {
		switch o.kind {
		case opEqual:
			fmt.Fprintf(&out, "%-*s | %s\n", col, truncate(o.text, col), truncate(o.text, col))
		case opDelete:
			fmt.Fprintf(&out, "%-*s <\n", col, truncate(o.text, col))
		case opInsert:
			fmt.Fprintf(&out, "%-*s > %s\n", col, "", truncate(o.text, col))
		}
	}
	return scrubText(s, out.String())
}

func scrubText(s *scrub.Scrubber, text string) string {
	if s == nil {
		return scrub.Scrub(text)
	}
	return s.Scrub(text)
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

func truncate(text string, width int) string {
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "…"
}
