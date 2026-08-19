package plugins

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

// manifestName is the file that marks a directory as a plugin.
const manifestName = "plugin.yaml"

// Loader discovers plugins across the search path.
type Loader struct {
	// Dirs are searched in precedence order: workspace-local first, then user-global. The first
	// definition of a given name wins.
	//
	// Built-in plugins are deliberately absent. The Python implementation shipped Python scripts
	// loaded from its package directory; a Go binary has no interpreter to offer them, and
	// shipping one would undermine the single-binary goal. [DIVERGE]
	Dirs []string
	Log  *slog.Logger
}

func (l *Loader) log() *slog.Logger {
	if l.Log != nil {
		return l.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Discover returns the plugins found, in the order they will run.
//
// Within a directory plugins are sorted by name, because directory listing order is not
// guaranteed stable and an unstable plugin order makes runs unreproducible.
func (l *Loader) Discover() []Plugin {
	var out []Plugin
	claimed := map[string]string{}

	for _, dir := range l.Dirs {
		for _, p := range l.scan(dir) {
			if owner, taken := claimed[p.Name()]; taken {
				l.log().Debug("plugin name already claimed by a higher-precedence directory",
					"plugin", p.Name(), "ignored", p.Dir, "using", owner)
				continue
			}
			claimed[p.Name()] = p.Dir
			out = append(out, p)
		}
	}
	return out
}

// scan reads one search directory.
func (l *Loader) scan(dir string) []Plugin {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A search directory that does not exist is normal: most workspaces have no plugins.
		return nil
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })

	var out []Plugin
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pluginDir := filepath.Join(dir, entry.Name())
		p, err := l.load(pluginDir)
		if err != nil {
			// One broken plugin must not prevent the others from loading. The reference skipped
			// silently; a warning naming the path is the minimum a user needs. [DIVERGE]
			l.log().Warn("skipping plugin", "dir", pluginDir, "error", err)
			continue
		}
		p.Source = dir
		out = append(out, p)
	}
	return out
}

// load reads and validates one plugin directory.
func (l *Loader) load(dir string) (Plugin, error) {
	raw, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return Plugin{}, fmt.Errorf("%w: %w", ErrManifest, err)
	}

	var m Manifest
	if err := yaml.UnmarshalStrict(raw, &m); err != nil {
		return Plugin{}, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	if err := m.Validate(); err != nil {
		return Plugin{}, err
	}

	// The entrypoint is resolved inside the plugin's own directory: a manifest must not be able
	// to point rv at an arbitrary executable elsewhere on the machine.
	entrypoint := filepath.Join(dir, filepath.Clean(m.Entrypoint))
	rel, err := filepath.Rel(dir, entrypoint)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Plugin{}, fmt.Errorf("%w: plugin %q: entrypoint %q escapes the plugin directory",
			ErrManifest, m.Name, m.Entrypoint)
	}
	if _, err := os.Stat(entrypoint); err != nil {
		return Plugin{}, fmt.Errorf("%w: plugin %q: entrypoint not found: %w", ErrManifest, m.Name, err)
	}

	return Plugin{Manifest: m, Dir: dir, Entrypoint: entrypoint}, nil
}

// For returns the plugins subscribed to a stage, in run order.
func (l *Loader) For(stage Stage) []Plugin {
	var out []Plugin
	for _, p := range l.Discover() {
		if p.Subscribes(stage) {
			out = append(out, p)
		}
	}
	return out
}
