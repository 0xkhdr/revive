// Package manifest owns the manifest.yaml schema, its strict loader, and its validation rules.
// The schema is part of the compatibility contract: versions 1 and 2 must load exactly as the
// Python implementation loaded them.
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Sentinel errors. Callers branch on these with errors.Is, never on message text.
var (
	// ErrValidation covers every domain rule violation found by Validate.
	ErrValidation = errors.New("manifest validation failed")
	// ErrUnsupportedSchemaVersion is returned for any version outside {1, 2}.
	ErrUnsupportedSchemaVersion = errors.New("unsupported manifest schema version")
)

// SupportedVersions is the set of schema versions this release understands.
var SupportedVersions = []int{1, 2}

// DefaultVersion applies when the manifest omits `version`.
const DefaultVersion = 2

// AssetType is the orchestration type of an asset.
type AssetType string

// Asset types.
const (
	TypeSymlink  AssetType = "symlink"
	TypeCopy     AssetType = "copy"
	TypeTemplate AssetType = "template"
	TypeSecret   AssetType = "secret"
)

// Valid reports whether t is a known asset type.
func (t AssetType) Valid() bool {
	switch t {
	case TypeSymlink, TypeCopy, TypeTemplate, TypeSecret:
		return true
	}
	return false
}

// ConflictStrategy decides what happens when a target already exists.
type ConflictStrategy string

// Conflict strategies.
const (
	ConflictPrompt    ConflictStrategy = "prompt"
	ConflictOverwrite ConflictStrategy = "overwrite"
	ConflictSkip      ConflictStrategy = "skip"
	ConflictAbort     ConflictStrategy = "abort"
)

// Valid reports whether s is a known conflict strategy.
func (s ConflictStrategy) Valid() bool {
	switch s {
	case ConflictPrompt, ConflictOverwrite, ConflictSkip, ConflictAbort:
		return true
	}
	return false
}

// StringOrSlice holds a field written as either a scalar or a list. It remembers which, because
// the lockfile round trip must write back the same shape it read.
type StringOrSlice struct {
	Values    []string
	wasScalar bool
}

// Scalar builds a StringOrSlice that marshals back as a bare string.
func Scalar(v string) StringOrSlice { return StringOrSlice{Values: []string{v}, wasScalar: true} }

// Slice builds a StringOrSlice that marshals back as a list.
func Slice(v ...string) StringOrSlice { return StringOrSlice{Values: v} }

// IsScalar reports whether the value was written as a scalar.
func (s StringOrSlice) IsScalar() bool { return s.wasScalar }

// UnmarshalJSON accepts a string or a list of strings.
func (s *StringOrSlice) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*s = StringOrSlice{Values: []string{one}, wasScalar: true}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("expected a string or a list of strings, got %s", b)
	}
	*s = StringOrSlice{Values: many}
	return nil
}

// MarshalJSON writes a scalar back as a scalar and a list back as a list.
func (s StringOrSlice) MarshalJSON() ([]byte, error) {
	if s.wasScalar && len(s.Values) == 1 {
		return json.Marshal(s.Values[0])
	}
	return json.Marshal(s.Values)
}

// Hook is one entry in an asset's pre or post hook list. Exactly one field may be set.
type Hook struct {
	Command string `json:"command,omitempty"`
	Plugin  string `json:"plugin,omitempty"`
}

// Hooks are the per-asset lifecycle hooks. They are planned during planning and executed inside
// the transaction, interleaved around their own asset's mutation.
type Hooks struct {
	Pre  []Hook `json:"pre,omitempty"`
	Post []Hook `json:"post,omitempty"`
}

// Asset is one file or directory managed by rv.
type Asset struct {
	ID               string           `json:"id"`
	Type             AssetType        `json:"type,omitempty"`
	Source           string           `json:"source"`
	Target           StringOrSlice    `json:"target"`
	Permissions      *string          `json:"permissions,omitempty"`
	Owner            *string          `json:"owner,omitempty"`
	ConflictStrategy ConflictStrategy `json:"conflict_strategy,omitempty"`
	Encrypted        bool             `json:"encrypted,omitempty"`
	TemplateVars     map[string]any   `json:"template_vars,omitempty"`
	Hooks            Hooks            `json:"hooks,omitempty"`
}

// Secret is an age-encrypted asset. Its type, encryption and permission floor are forced at load
// time, so it deliberately carries fewer fields than Asset.
type Secret struct {
	ID          string        `json:"id"`
	Type        AssetType     `json:"type,omitempty"`
	Source      string        `json:"source"`
	Target      StringOrSlice `json:"target"`
	Permissions string        `json:"permissions,omitempty"`
	Owner       *string       `json:"owner,omitempty"`
	Encrypted   bool          `json:"encrypted,omitempty"`
}

// Asset renders the secret as an Asset for the engine, which treats both uniformly.
func (s Secret) Asset() Asset {
	perms := s.Permissions
	return Asset{
		ID:               s.ID,
		Type:             TypeSecret,
		Source:           s.Source,
		Target:           s.Target,
		Permissions:      &perms,
		Owner:            s.Owner,
		ConflictStrategy: ConflictOverwrite,
		Encrypted:        true,
	}
}

// DockerConfig lists images to pull.
type DockerConfig struct {
	Images []string `json:"images,omitempty"`
}

// NodeConfig selects a node version. Version wins over VersionFile.
type NodeConfig struct {
	VersionFile string `json:"version_file,omitempty"`
	Version     string `json:"version,omitempty"`
}

// Packages is the global package pool, one list per provider.
type Packages struct {
	Brew    []string     `json:"brew,omitempty"`
	Apt     []string     `json:"apt,omitempty"`
	Flatpak []string     `json:"flatpak,omitempty"`
	Snap    []string     `json:"snap,omitempty"`
	Pacman  []string     `json:"pacman,omitempty"`
	Dnf     []string     `json:"dnf,omitempty"`
	Nix     []string     `json:"nix,omitempty"`
	Cargo   []string     `json:"cargo,omitempty"`
	Pip     []string     `json:"pip,omitempty"`
	Docker  DockerConfig `json:"docker,omitempty"`
	Node    NodeConfig   `json:"node,omitempty"`
}

// ListNames are the package groups a profile may reference that map to a flat name list.
var ListNames = []string{"brew", "apt", "flatpak", "snap", "pacman", "dnf", "nix", "cargo", "pip"}

// List returns the package list for a group name, and whether that group is a flat list.
func (p *Packages) List(group string) ([]string, bool) {
	switch group {
	case "brew":
		return p.Brew, true
	case "apt":
		return p.Apt, true
	case "flatpak":
		return p.Flatpak, true
	case "snap":
		return p.Snap, true
	case "pacman":
		return p.Pacman, true
	case "dnf":
		return p.Dnf, true
	case "nix":
		return p.Nix, true
	case "cargo":
		return p.Cargo, true
	case "pip":
		return p.Pip, true
	}
	return nil, false
}

// ProfileRef is a profile's reference to an asset or secret: either an ID from the global pool
// or an inline definition.
type ProfileRef[T any] struct {
	ID     string
	Inline *T
}

// UnmarshalJSON accepts a bare ID string or an inline object.
func (r *ProfileRef[T]) UnmarshalJSON(b []byte) error {
	var id string
	if err := json.Unmarshal(b, &id); err == nil {
		*r = ProfileRef[T]{ID: id}
		return nil
	}
	var inline T
	if err := json.Unmarshal(b, &inline); err != nil {
		return fmt.Errorf("expected an ID string or an inline definition: %w", err)
	}
	*r = ProfileRef[T]{Inline: &inline}
	return nil
}

// MarshalJSON writes a reference back in the shape it was read.
func (r ProfileRef[T]) MarshalJSON() ([]byte, error) {
	if r.Inline != nil {
		return json.Marshal(r.Inline)
	}
	return json.Marshal(r.ID)
}

// Profile names a subset of assets, secrets and package groups, optionally extending others.
type Profile struct {
	Extends  []string             `json:"extends,omitempty"`
	Assets   []ProfileRef[Asset]  `json:"assets,omitempty"`
	Secrets  []ProfileRef[Secret] `json:"secrets,omitempty"`
	Packages []string             `json:"packages,omitempty"`
}

// BackupRetention bounds how many transaction snapshots survive, and for how long.
type BackupRetention struct {
	MaxCount   *int `json:"max_count,omitempty"`
	MaxAgeDays *int `json:"max_age_days,omitempty"`
}

// MachineOverrides configures per-host manifest overrides.
type MachineOverrides struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Path    string `json:"path,omitempty"`
}

// Manifest is the root declaration at the workspace root.
type Manifest struct {
	Version          *int               `json:"version,omitempty"`
	Assets           []Asset            `json:"assets,omitempty"`
	Secrets          []Secret           `json:"secrets,omitempty"`
	Packages         Packages           `json:"packages,omitempty"`
	Profiles         map[string]Profile `json:"profiles,omitempty"`
	MachineOverrides MachineOverrides   `json:"machine_overrides,omitempty"`
	BackupRetention  BackupRetention    `json:"backup_retention,omitempty"`
}

// SchemaVersion returns the declared version, or DefaultVersion when it was omitted.
func (m *Manifest) SchemaVersion() int {
	if m.Version == nil {
		return DefaultVersion
	}
	return *m.Version
}

// OverridesEnabled reports whether machine overrides apply. They default to on.
func (m *Manifest) OverridesEnabled() bool {
	return m.MachineOverrides.Enabled == nil || *m.MachineOverrides.Enabled
}

// Retention returns the effective retention bounds, applying the documented defaults.
func (m *Manifest) Retention() (maxCount, maxAgeDays int) {
	maxCount, maxAgeDays = 10, 30
	if m.BackupRetention.MaxCount != nil {
		maxCount = *m.BackupRetention.MaxCount
	}
	if m.BackupRetention.MaxAgeDays != nil {
		maxAgeDays = *m.BackupRetention.MaxAgeDays
	}
	return maxCount, maxAgeDays
}
