// Package lockfile reads and writes manifest.lock, the record of the last confirmed good state.
package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/transaction"
)

// FloatOrSlice is the mtime field, scalar for a scalar-target asset and an index-aligned array
// for a multi-target one. It remembers which, because the round trip must preserve the shape.
type FloatOrSlice struct {
	Values    []float64
	wasScalar bool
}

// ScalarFloat builds a FloatOrSlice that marshals back as a bare number.
func ScalarFloat(v float64) FloatOrSlice {
	return FloatOrSlice{Values: []float64{v}, wasScalar: true}
}

// SliceFloat builds a FloatOrSlice that marshals back as an array.
func SliceFloat(v ...float64) FloatOrSlice { return FloatOrSlice{Values: v} }

// IsScalar reports whether the value was written as a scalar.
func (f FloatOrSlice) IsScalar() bool { return f.wasScalar }

// UnmarshalJSON accepts a number or an array of numbers.
func (f *FloatOrSlice) UnmarshalJSON(b []byte) error {
	var one float64
	if err := json.Unmarshal(b, &one); err == nil {
		*f = FloatOrSlice{Values: []float64{one}, wasScalar: true}
		return nil
	}
	var many []float64
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("expected a number or a list of numbers, got %s", b)
	}
	*f = FloatOrSlice{Values: many}
	return nil
}

// MarshalJSON writes a scalar back as a scalar and an array back as an array.
func (f FloatOrSlice) MarshalJSON() ([]byte, error) {
	if f.wasScalar && len(f.Values) == 1 {
		return json.Marshal(f.Values[0])
	}
	return json.Marshal(f.Values)
}

// Entry is one asset's or secret's recorded state.
//
// The scalar-or-array polymorphism is inherited from the Python schema and MUST survive both
// read and write: an asset whose target was a scalar writes scalars.
type Entry struct {
	SHA256OfSource string                 `json:"sha256_of_source"`
	TargetPath     manifest.StringOrSlice `json:"target_path"`
	Permissions    manifest.StringOrSlice `json:"permissions"`
	MTime          FloatOrSlice           `json:"mtime"`
}

// MTimeFor returns the modification time this entry recorded for one target, and whether it has
// one. A multi-target entry stores index-aligned arrays, so the target has to be matched by
// position rather than assumed.
func (e Entry) MTimeFor(target string) (float64, bool) {
	for i, path := range e.TargetPath.Values {
		if path == target && i < len(e.MTime.Values) {
			return e.MTime.Values[i], true
		}
	}
	return 0, false
}

// Lockfile is the whole manifest.lock document. Its content is JSON even though the manifest is
// YAML.
type Lockfile struct {
	Entries           map[string]Entry  `json:"entries"`
	RenderedChecksums map[string]string `json:"rendered_checksums"`
}

// New returns an empty lockfile.
func New() *Lockfile {
	return &Lockfile{Entries: map[string]Entry{}, RenderedChecksums: map[string]string{}}
}

// PathFor returns the lockfile beside a manifest, named by replacing the manifest's extension.
func PathFor(manifestPath string) string {
	ext := filepath.Ext(manifestPath)
	return strings.TrimSuffix(manifestPath, ext) + ".lock"
}

// Load reads a lockfile. A missing file yields an empty one; that is a first run, not an error.
func Load(path string) (*Lockfile, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return New(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading lockfile: %w", err)
	}

	lf := New()
	if err := json.Unmarshal(raw, lf); err != nil {
		return nil, fmt.Errorf("parsing lockfile %s: %w", path, err)
	}
	if lf.Entries == nil {
		lf.Entries = map[string]Entry{}
	}
	if lf.RenderedChecksums == nil {
		lf.RenderedChecksums = map[string]string{}
	}
	return lf, nil
}

// LoadOrEmpty reads a lockfile, treating an unreadable one as empty.
//
// An existing lockfile that will not parse is a warning and a replacement, never a fatal error:
// refusing to restore because a generated file is corrupt would strand the user with no way to
// regenerate it.
func LoadOrEmpty(path string) (*Lockfile, error) {
	lf, err := Load(path)
	if err != nil {
		return New(), err
	}
	return lf, nil
}

// Marshal serializes the lockfile the way the Python implementation writes it: two-space
// indentation and the same field order.
func Marshal(lf *Lockfile) ([]byte, error) {
	if lf.Entries == nil {
		lf.Entries = map[string]Entry{}
	}
	if lf.RenderedChecksums == nil {
		lf.RenderedChecksums = map[string]string{}
	}
	return json.MarshalIndent(lf, "", "  ")
}

// Save writes the lockfile atomically.
func Save(path string, lf *Lockfile) error {
	raw, err := Marshal(lf)
	if err != nil {
		return fmt.Errorf("serializing lockfile: %w", err)
	}
	return transaction.AtomicWrite(path, raw, 0o644)
}
