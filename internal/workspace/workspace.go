// Package workspace maintains the registry of known workspaces at ~/.config/rv/workspaces.yaml,
// so `rv workspace sync` can update all of them.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/0xkhdr/revive/internal/transaction"
)

// Sentinel errors.
var (
	// ErrNotFound is returned when a named workspace is not registered.
	ErrNotFound = errors.New("workspace not registered")
	// ErrDuplicate is returned when a name is already taken by a different path.
	ErrDuplicate = errors.New("workspace name already in use")
)

// Workspace is one registered repository.
type Workspace struct {
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	LastAccessed time.Time `json:"last_accessed"`
}

// Config is the whole registry file.
type Config struct {
	Workspaces       []Workspace `json:"workspaces"`
	DefaultWorkspace string      `json:"default_workspace,omitempty"`
}

// Load reads the registry. A missing file is an empty registry, not an error.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading workspace registry: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parsing workspace registry %s: %w", path, err)
	}
	return &cfg, nil
}

// Save writes the registry atomically.
func Save(path string, cfg *Config) error {
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("serializing workspace registry: %w", err)
	}
	return transaction.AtomicWrite(path, raw, 0o600)
}

// Find returns a workspace by name.
func (c *Config) Find(name string) (Workspace, bool) {
	for _, ws := range c.Workspaces {
		if ws.Name == name {
			return ws, true
		}
	}
	return Workspace{}, false
}

// Register adds a workspace, or refreshes the one already registered at that path.
//
// The name defaults to the directory's basename. Re-registering the same path is an update, not
// a duplicate: `rv clone` into a directory that was already known should not fail.
func (c *Config) Register(path, name string, now time.Time) (Workspace, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Workspace{}, fmt.Errorf("resolving workspace path: %w", err)
	}
	if name == "" {
		name = filepath.Base(abs)
	}

	for i, ws := range c.Workspaces {
		if ws.Path == abs {
			c.Workspaces[i].Name = name
			c.Workspaces[i].LastAccessed = now
			return c.Workspaces[i], nil
		}
		if ws.Name == name {
			return Workspace{}, fmt.Errorf("%w: %q is registered at %s", ErrDuplicate, name, ws.Path)
		}
	}

	ws := Workspace{Name: name, Path: abs, LastAccessed: now}
	c.Workspaces = append(c.Workspaces, ws)
	if c.DefaultWorkspace == "" {
		c.DefaultWorkspace = name
	}
	return ws, nil
}

// Remove unregisters a workspace by name. It never touches the directory itself.
func (c *Config) Remove(name string) error {
	idx := slices.IndexFunc(c.Workspaces, func(ws Workspace) bool { return ws.Name == name })
	if idx < 0 {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	c.Workspaces = slices.Delete(c.Workspaces, idx, idx+1)
	if c.DefaultWorkspace == name {
		c.DefaultWorkspace = ""
		if len(c.Workspaces) > 0 {
			c.DefaultWorkspace = c.Workspaces[0].Name
		}
	}
	return nil
}

// Current returns the registered workspace containing dir, preferring the most specific match so
// a nested workspace wins over the parent it lives in.
func (c *Config) Current(dir string) (Workspace, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Workspace{}, false
	}

	best, found := Workspace{}, false
	for _, ws := range c.Workspaces {
		if abs != ws.Path && !strings.HasPrefix(abs, ws.Path+string(filepath.Separator)) {
			continue
		}
		if !found || len(ws.Path) > len(best.Path) {
			best, found = ws, true
		}
	}
	return best, found
}
