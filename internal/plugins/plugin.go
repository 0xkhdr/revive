// Package plugins discovers and runs user-supplied plugins around a restore.
//
// A plugin is trusted code with a seatbelt, not an untrusted-code sandbox. See docs/07 §3 for
// exactly what the isolation does and does not provide.
package plugins

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors.
var (
	// ErrPlugin wraps every plugin failure. A plugin failure fails the restore: plugins are not
	// advisory.
	ErrPlugin = errors.New("plugin failed")
	// ErrTimeout is returned when a plugin exceeded its timeout and was killed.
	ErrTimeout = errors.New("plugin timed out")
	// ErrManifest is returned for a plugin.yaml that will not load.
	ErrManifest = errors.New("invalid plugin manifest")
)

// Stage names a lifecycle hook a plugin can subscribe to.
type Stage string

// The two stages.
const (
	PreRestore  Stage = "pre-restore"
	PostRestore Stage = "post-restore"
)

// Timeout bounds. A plugin's declared timeout is clamped into this range: zero would make the
// plugin unrunnable, and an unbounded one would hang a restore forever.
const (
	DefaultTimeout = 30 * time.Second
	MinTimeout     = 1 * time.Second
	MaxTimeout     = 300 * time.Second
)

// Permissions is a plugin's declared permission set.
//
// These are declarations the wrapper is expected to honor, not kernel-enforced confinement.
// AllowedPaths in particular is advisory in v1.0.
type Permissions struct {
	Network      bool     `json:"network,omitempty"`
	Shell        bool     `json:"shell,omitempty"`
	AllowedPaths []string `json:"allowed_paths,omitempty"`
}

// Manifest is a plugin.yaml.
type Manifest struct {
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	Entrypoint  string      `json:"entrypoint"`
	Timeout     int         `json:"timeout,omitempty"`
	Hooks       []Stage     `json:"hooks,omitempty"`
	Permissions Permissions `json:"permissions,omitempty"`
}

// Plugin is a discovered plugin.
type Plugin struct {
	Manifest Manifest
	// Dir is the plugin's own directory; Entrypoint is the resolved executable.
	Dir        string
	Entrypoint string
	// Source is the search directory it was found in, for diagnostics.
	Source string
}

// Name is the plugin's declared name.
func (p Plugin) Name() string { return p.Manifest.Name }

// Timeout is the declared timeout, clamped into the permitted range.
func (p Plugin) Timeout() time.Duration {
	if p.Manifest.Timeout <= 0 {
		return DefaultTimeout
	}
	return min(max(time.Duration(p.Manifest.Timeout)*time.Second, MinTimeout), MaxTimeout)
}

// Subscribes reports whether the plugin runs at a stage.
func (p Plugin) Subscribes(stage Stage) bool {
	for _, h := range p.Manifest.Hooks {
		if h == stage {
			return true
		}
	}
	return false
}

// Context is the JSON document a plugin receives on stdin.
//
// The field names are the protocol; changing one breaks every existing plugin.
type Context struct {
	RepoDir     string   `json:"repo_dir"`
	ProfileName string   `json:"profile_name"`
	DryRun      bool     `json:"dry_run"`
	Targets     []string `json:"targets"`
	HookType    Stage    `json:"hook_type"`
}

// Result is the JSON document a plugin writes to stdout.
type Result struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	// Stdout carries the raw output when a plugin exited 0 without parseable JSON.
	Stdout string `json:"stdout,omitempty"`
	// Plugin names which plugin produced this result.
	Plugin string `json:"plugin,omitempty"`
}

// Validate checks a plugin manifest.
func (m *Manifest) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("%w: name is required", ErrManifest)
	}
	if m.Version == "" {
		return fmt.Errorf("%w: plugin %q: version is required", ErrManifest, m.Name)
	}
	if m.Entrypoint == "" {
		return fmt.Errorf("%w: plugin %q: entrypoint is required", ErrManifest, m.Name)
	}
	for _, hook := range m.Hooks {
		if hook != PreRestore && hook != PostRestore {
			return fmt.Errorf("%w: plugin %q: unknown hook stage %q", ErrManifest, m.Name, hook)
		}
	}
	return nil
}
