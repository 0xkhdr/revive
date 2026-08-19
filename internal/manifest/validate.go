package manifest

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Validate applies every domain rule from docs/02 §1 and fills in the derived fields. It mutates
// the manifest: defaults are applied, and `type: secret` forces `encrypted: true`.
//
// All violations are reported together; each one wraps ErrValidation.
func (m *Manifest) Validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: "+format, append([]any{ErrValidation}, args...)...))
	}

	if !slices.Contains(SupportedVersions, m.SchemaVersion()) {
		return fmt.Errorf("%w: %d (supported: %v) — upgrade rv or downgrade the manifest",
			ErrUnsupportedSchemaVersion, m.SchemaVersion(), SupportedVersions)
	}

	seen := make(map[string]string, len(m.Assets)+len(m.Secrets))
	for i := range m.Assets {
		a := &m.Assets[i]
		if err := a.Validate(); err != nil {
			errs = append(errs, err)
		}
		if kind, dup := seen[a.ID]; dup && a.ID != "" {
			fail("duplicate id %q: already declared as %s", a.ID, kind)
		}
		seen[a.ID] = "asset"
	}
	for i := range m.Secrets {
		s := &m.Secrets[i]
		if err := s.Validate(); err != nil {
			errs = append(errs, err)
		}
		if kind, dup := seen[s.ID]; dup && s.ID != "" {
			fail("duplicate id %q: already declared as %s", s.ID, kind)
		}
		seen[s.ID] = "secret"
	}

	for name, p := range m.Profiles {
		for i := range p.Assets {
			if inline := p.Assets[i].Inline; inline != nil {
				if err := inline.Validate(); err != nil {
					errs = append(errs, fmt.Errorf("profile %q: %w", name, err))
				}
			}
		}
		for i := range p.Secrets {
			if inline := p.Secrets[i].Inline; inline != nil {
				if err := inline.Validate(); err != nil {
					errs = append(errs, fmt.Errorf("profile %q: %w", name, err))
				}
			}
		}
		for _, group := range p.Packages {
			if _, ok := m.Packages.List(group); !ok && group != "docker" && group != "node" {
				fail("profile %q references unknown package group %q", name, group)
			}
		}
		m.Profiles[name] = p
	}

	if m.MachineOverrides.Path == "" {
		m.MachineOverrides.Path = "machine/{hostname}.yaml"
	}
	if maxCount, maxAge := m.Retention(); maxCount < 1 || maxAge < 1 {
		fail("backup_retention: max_count and max_age_days must both be >= 1, got %d and %d", maxCount, maxAge)
	}

	return errors.Join(errs...)
}

// Validate applies the asset rules and fills in defaults.
func (a *Asset) Validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: asset %q: "+format, append([]any{ErrValidation, a.ID}, args...)...))
	}

	if a.ID == "" {
		errs = append(errs, fmt.Errorf("%w: asset is missing an id", ErrValidation))
	}
	if a.Type == "" {
		a.Type = TypeSymlink
	}
	if !a.Type.Valid() {
		fail("unknown type %q", a.Type)
	}
	if a.ConflictStrategy == "" {
		a.ConflictStrategy = ConflictPrompt
	}
	if !a.ConflictStrategy.Valid() {
		fail("unknown conflict_strategy %q", a.ConflictStrategy)
	}
	// `type: secret` forces encryption regardless of what the YAML said.
	if a.Type == TypeSecret {
		a.Encrypted = true
	}
	if err := validateSource(a.Source); err != nil {
		fail("%s", err)
	}
	if err := validateTarget(a.Target); err != nil {
		fail("%s", err)
	}
	if a.Permissions != nil {
		if _, err := ParsePermissions(*a.Permissions); err != nil {
			fail("%s", err)
		}
	}
	for stage, hooks := range map[string][]Hook{"pre": a.Hooks.Pre, "post": a.Hooks.Post} {
		for _, h := range hooks {
			switch {
			case h.Plugin != "":
				fail("%s hook references plugin %q: plugin hooks are not supported at the asset "+
					"level, use a profile-level pre-restore/post-restore hook", stage, h.Plugin)
			case h.Command == "":
				fail("%s hook must set `command`", stage)
			}
		}
	}
	return errors.Join(errs...)
}

// Validate applies the secret rules and forces type, encryption and the permission floor.
func (s *Secret) Validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%w: secret %q: "+format, append([]any{ErrValidation, s.ID}, args...)...))
	}

	if s.ID == "" {
		errs = append(errs, fmt.Errorf("%w: secret is missing an id", ErrValidation))
	}
	// Forced at load time: a secret is always an encrypted secret, whatever the YAML claims.
	s.Type = TypeSecret
	s.Encrypted = true
	if s.Permissions == "" {
		s.Permissions = "0600"
	}
	if err := validateSource(s.Source); err != nil {
		fail("%s", err)
	}
	if err := validateTarget(s.Target); err != nil {
		fail("%s", err)
	}
	mode, err := ParsePermissions(s.Permissions)
	switch {
	case err != nil:
		fail("%s", err)
	case mode&0o077 != 0:
		fail("permissions %q grant group or world access; secrets must not", s.Permissions)
	}
	return errors.Join(errs...)
}

// validateSource is the primary path-traversal guard: a source must stay inside the repo.
func validateSource(source string) error {
	if source == "" {
		return errors.New("source is required")
	}
	normalized := filepath.Clean(source)
	if filepath.IsAbs(normalized) || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return fmt.Errorf("source %q must be relative to the repository and must not traverse outside it", source)
	}
	return nil
}

func validateTarget(t StringOrSlice) error {
	if len(t.Values) == 0 {
		return errors.New("target is required")
	}
	for _, v := range t.Values {
		if strings.TrimSpace(v) == "" {
			return errors.New("target entries must not be empty")
		}
	}
	return nil
}

// ParsePermissions accepts exactly a 4-character octal string starting with 0, e.g. "0644".
// "644", "0o644" and "rwxr-xr-x" are all rejected.
func ParsePermissions(s string) (uint32, error) {
	if len(s) != 4 || s[0] != '0' {
		return 0, fmt.Errorf("permissions %q must be a 4-digit octal string starting with 0", s)
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, fmt.Errorf("permissions %q is not valid octal", s)
	}
	return uint32(v), nil
}
