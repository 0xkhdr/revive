package manifest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"

	"sigs.k8s.io/yaml"
)

// ErrNotFound is returned when the manifest file does not exist.
var ErrNotFound = errors.New("manifest not found")

// Load reads, strictly decodes and validates a manifest file.
func Load(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrNotFound, path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	m, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// Parse decodes manifest bytes. The version is checked twice — once on the raw document before
// struct binding, once in Validate — so a future schema cannot be accepted as valid garbage by a
// lenient decoder.
func Parse(raw []byte) (*Manifest, error) {
	var probe struct {
		Version *int `json:"version"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w", err)
	}
	if probe.Version != nil && !slices.Contains(SupportedVersions, *probe.Version) {
		return nil, fmt.Errorf("%w: %d (supported: %v) — upgrade rv or downgrade the manifest",
			ErrUnsupportedSchemaVersion, *probe.Version, SupportedVersions)
	}

	var m Manifest
	if err := yaml.UnmarshalStrict(raw, &m); err != nil {
		return nil, fmt.Errorf("%w: parsing manifest: %w", ErrValidation, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}
