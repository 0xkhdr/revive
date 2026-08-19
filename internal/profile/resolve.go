// Package profile flattens a manifest's profile inheritance into the single ResolvedProfile the
// engine applies.
package profile

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/0xkhdr/revive/internal/manifest"
)

// Sentinel errors.
var (
	// ErrNotFound covers an unknown profile name and an unknown asset or secret ID.
	ErrNotFound = errors.New("not found")
	// ErrCycle is returned when profile inheritance loops.
	ErrCycle = errors.New("cyclic profile inheritance detected")
)

// Resolved is the flattened profile: everything the engine needs, with inheritance,
// multi-profile merging and machine overrides already applied.
type Resolved struct {
	Assets       map[string]manifest.Asset
	Secrets      map[string]manifest.Secret
	Packages     map[string][]string // provider name -> package names
	DockerImages []string
	Node         manifest.NodeConfig

	// order records asset and secret IDs in resolution order, so the engine's plan is
	// deterministic rather than map-iteration order.
	assetOrder  []string
	secretOrder []string
}

// New builds an empty Resolved with every package group present.
func New() *Resolved {
	r := &Resolved{
		Assets:   map[string]manifest.Asset{},
		Secrets:  map[string]manifest.Secret{},
		Packages: map[string][]string{},
	}
	for _, name := range manifest.ListNames {
		r.Packages[name] = []string{}
	}
	return r
}

// AssetIDs returns asset IDs in resolution order.
func (r *Resolved) AssetIDs() []string { return slices.Clone(r.assetOrder) }

// SecretIDs returns secret IDs in resolution order.
func (r *Resolved) SecretIDs() []string { return slices.Clone(r.secretOrder) }

// PutAsset inserts or replaces an asset by ID, preserving its first-seen position.
func (r *Resolved) PutAsset(a manifest.Asset) {
	if _, exists := r.Assets[a.ID]; !exists {
		r.assetOrder = append(r.assetOrder, a.ID)
	}
	r.Assets[a.ID] = a
}

// PutSecret inserts or replaces a secret by ID, preserving its first-seen position.
func (r *Resolved) PutSecret(s manifest.Secret) {
	if _, exists := r.Secrets[s.ID]; !exists {
		r.secretOrder = append(r.secretOrder, s.ID)
	}
	r.Secrets[s.ID] = s
}

// AddPackages appends names to a group, skipping duplicates and preserving order.
func (r *Resolved) AddPackages(group string, names ...string) {
	for _, n := range names {
		if !slices.Contains(r.Packages[group], n) {
			r.Packages[group] = append(r.Packages[group], n)
		}
	}
}

// AddDockerImages appends images, skipping duplicates.
func (r *Resolved) AddDockerImages(images ...string) {
	for _, img := range images {
		if !slices.Contains(r.DockerImages, img) {
			r.DockerImages = append(r.DockerImages, img)
		}
	}
}

// ParseNames splits profile arguments. `-p base -p work` and `-p base,work` are equivalent, and
// both normalize to the same list.
func ParseNames(args ...string) []string {
	var out []string
	for _, arg := range args {
		for _, name := range strings.Split(arg, ",") {
			if name = strings.TrimSpace(name); name != "" && !slices.Contains(out, name) {
				out = append(out, name)
			}
		}
	}
	return out
}

// Resolve flattens one or more profile names, comma-separated or repeated.
//
// Parents resolve before children, so a child's asset overrides its parent's by ID. The visited
// chain is copied per branch, so a diamond is legal while a true cycle is reported with its full
// chain.
func Resolve(m *manifest.Manifest, names ...string) (*Resolved, error) {
	profiles := ParseNames(names...)
	if len(profiles) == 0 {
		return nil, fmt.Errorf("%w: no profile names provided", ErrNotFound)
	}

	out := New()
	for _, name := range profiles {
		if _, ok := m.Profiles[name]; !ok {
			return nil, fmt.Errorf("%w: profile %q is not defined in the manifest", ErrNotFound, name)
		}
		// Each named profile resolves independently against a fresh visited chain, then merges
		// last-write-wins — resolving them into one accumulator would be indistinguishable here
		// but would leak one profile's cycle detection into the next.
		single := New()
		if err := resolveRecursive(m, name, single, nil); err != nil {
			return nil, err
		}
		merge(out, single)
	}
	return out, nil
}

func merge(dst, src *Resolved) {
	for _, id := range src.assetOrder {
		dst.PutAsset(src.Assets[id])
	}
	for _, id := range src.secretOrder {
		dst.PutSecret(src.Secrets[id])
	}
	for group, pkgs := range src.Packages {
		dst.AddPackages(group, pkgs...)
	}
	dst.AddDockerImages(src.DockerImages...)
	if src.Node.VersionFile != "" {
		dst.Node.VersionFile = src.Node.VersionFile
	}
	if src.Node.Version != "" {
		dst.Node.Version = src.Node.Version
	}
}

func resolveRecursive(m *manifest.Manifest, name string, out *Resolved, visited []string) error {
	if slices.Contains(visited, name) {
		return fmt.Errorf("%w: %s", ErrCycle, strings.Join(append(slices.Clone(visited), name), " -> "))
	}
	visited = append(slices.Clone(visited), name)

	p, ok := m.Profiles[name]
	if !ok {
		return fmt.Errorf("%w: profile %q is not defined in the manifest", ErrNotFound, name)
	}

	// 1. Parents first, so a child overrides them.
	for _, parent := range p.Extends {
		if err := resolveRecursive(m, parent, out, visited); err != nil {
			return err
		}
	}

	// 2. Assets, by ID from the global pool or inline.
	pool := make(map[string]manifest.Asset, len(m.Assets))
	for _, a := range m.Assets {
		pool[a.ID] = a
	}
	for _, ref := range p.Assets {
		if ref.Inline != nil {
			out.PutAsset(*ref.Inline)
			continue
		}
		a, ok := pool[ref.ID]
		if !ok {
			return fmt.Errorf("%w: asset %q referenced by profile %q is not in the global pool",
				ErrNotFound, ref.ID, name)
		}
		out.PutAsset(a)
	}

	// 3. Secrets, same rules.
	secretPool := make(map[string]manifest.Secret, len(m.Secrets))
	for _, s := range m.Secrets {
		secretPool[s.ID] = s
	}
	for _, ref := range p.Secrets {
		if ref.Inline != nil {
			out.PutSecret(*ref.Inline)
			continue
		}
		s, ok := secretPool[ref.ID]
		if !ok {
			return fmt.Errorf("%w: secret %q referenced by profile %q is not in the global pool",
				ErrNotFound, ref.ID, name)
		}
		out.PutSecret(s)
	}

	// 4. Package groups. A profile lists group names, not package names.
	for _, group := range p.Packages {
		switch group {
		case "docker":
			out.AddDockerImages(m.Packages.Docker.Images...)
		case "node":
			if m.Packages.Node.VersionFile != "" {
				out.Node.VersionFile = m.Packages.Node.VersionFile
			}
			if m.Packages.Node.Version != "" {
				out.Node.Version = m.Packages.Node.Version
			}
		default:
			pkgs, ok := m.Packages.List(group)
			if !ok {
				return fmt.Errorf("%w: package group %q referenced by profile %q", ErrNotFound, group, name)
			}
			out.AddPackages(group, pkgs...)
		}
	}
	return nil
}
