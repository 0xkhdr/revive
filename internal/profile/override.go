package profile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/0xkhdr/revive/internal/manifest"
)

// ErrOverride is returned when a machine override file exists but cannot be used. A *missing*
// override file is normal and silent.
var ErrOverride = errors.New("machine override")

// Override is the subset of the manifest a machine/<hostname>.yaml file may set.
type Override struct {
	Assets   []manifest.Asset   `json:"assets,omitempty"`
	Secrets  []manifest.Secret  `json:"secrets,omitempty"`
	Packages *manifest.Packages `json:"packages,omitempty"`
}

// OverridePath resolves the configured override path pattern for a hostname. It returns "" when
// overrides are disabled, and an error when the pattern escapes the repository.
func OverridePath(m *manifest.Manifest, repoDir, hostname string) (string, error) {
	if !m.OverridesEnabled() {
		return "", nil
	}
	rel := filepath.Clean(strings.ReplaceAll(m.MachineOverrides.Path, "{hostname}", hostname))
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: path %q must stay inside the repository", ErrOverride, m.MachineOverrides.Path)
	}
	return filepath.Join(repoDir, rel), nil
}

// ApplyOverrides merges machine/<hostname>.yaml over an already-resolved profile. Assets and
// secrets merge by ID, last-write-wins. Package lists append. Node settings overwrite.
func ApplyOverrides(m *manifest.Manifest, r *Resolved, repoDir, hostname string) error {
	path, err := OverridePath(m, repoDir, hostname)
	if err != nil || path == "" {
		return err
	}

	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: reading %s: %w", ErrOverride, path, err)
	}

	var o Override
	if err := yaml.UnmarshalStrict(raw, &o); err != nil {
		return fmt.Errorf("%w: parsing %s: %w", ErrOverride, path, err)
	}

	for i := range o.Assets {
		if err := o.Assets[i].Validate(); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrOverride, path, err)
		}
		r.PutAsset(o.Assets[i])
	}
	for i := range o.Secrets {
		if err := o.Secrets[i].Validate(); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrOverride, path, err)
		}
		r.PutSecret(o.Secrets[i])
	}
	if o.Packages != nil {
		for _, group := range manifest.ListNames {
			pkgs, _ := o.Packages.List(group)
			r.AddPackages(group, pkgs...)
		}
		r.AddDockerImages(o.Packages.Docker.Images...)
		if o.Packages.Node.VersionFile != "" {
			r.Node.VersionFile = o.Packages.Node.VersionFile
		}
		if o.Packages.Node.Version != "" {
			r.Node.Version = o.Packages.Node.Version
		}
	}
	return nil
}
