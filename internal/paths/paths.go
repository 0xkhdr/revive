// Package paths owns every runtime location rv uses, plus path canonicalization and safety
// checks. It is the only package permitted to call os.UserHomeDir; everything else takes a
// Config so tests can root the whole layout at t.TempDir().
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrSymlinkLoop is returned when following a symlink chain revisits a path.
var ErrSymlinkLoop = errors.New("symlink loop detected")

// maxSymlinkDepth bounds link following independently of the visited set, so a very long
// non-cyclic chain cannot hang the walk.
const maxSymlinkDepth = 64

// Config carries every path rv reads or writes at runtime. The layout is part of the
// compatibility contract with the Python implementation.
type Config struct {
	Home      string // user home directory
	ConfigDir string // ~/.config/rv
	DataDir   string // ~/.local/share/rv

	LockFile      string // ~/.config/rv/rv.lock
	JournalDir    string // ~/.config/rv/journals
	BackupDir     string // ~/.config/rv/backups
	IdentityFile  string // ~/.config/rv/identity.txt
	PackageCache  string // ~/.config/rv/package-cache.json
	WorkspaceFile string // ~/.config/rv/workspaces.yaml
	PluginsDir    string // ~/.config/rv/plugins
	AuditLog      string // ~/.local/share/rv/audit.log
}

// New builds a Config rooted at home. Honors XDG_CONFIG_HOME and XDG_DATA_HOME only when they
// are absolute, matching the XDG spec's own rule for relative values.
func New(home string) Config {
	configHome := xdgDir("XDG_CONFIG_HOME", home, ".config")
	dataHome := xdgDir("XDG_DATA_HOME", home, ".local", "share")

	cfg := filepath.Join(configHome, "rv")
	data := filepath.Join(dataHome, "rv")
	return Config{
		Home:          home,
		ConfigDir:     cfg,
		DataDir:       data,
		LockFile:      filepath.Join(cfg, "rv.lock"),
		JournalDir:    filepath.Join(cfg, "journals"),
		BackupDir:     filepath.Join(cfg, "backups"),
		IdentityFile:  filepath.Join(cfg, "identity.txt"),
		PackageCache:  filepath.Join(cfg, "package-cache.json"),
		WorkspaceFile: filepath.Join(cfg, "workspaces.yaml"),
		PluginsDir:    filepath.Join(cfg, "plugins"),
		AuditLog:      filepath.Join(data, "audit.log"),
	}
}

// Default builds a Config rooted at the current user's home directory.
func Default() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("resolving home directory: %w", err)
	}
	return New(home), nil
}

func xdgDir(envVar, home string, fallback ...string) string {
	if v := os.Getenv(envVar); filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(append([]string{home}, fallback...)...)
}

// IdentityCandidates lists the identity file locations probed in order.
func (c Config) IdentityCandidates() []string {
	return []string{
		c.IdentityFile,
		filepath.Join(c.ConfigDir, "keys", "identity.txt"),
		filepath.Join(c.ConfigDir, "identifier.txt"),
	}
}

// JournalPath returns the journal file for a transaction ID.
func (c Config) JournalPath(txID string) string { return filepath.Join(c.JournalDir, txID+".json") }

// BackupPathFor returns the backup snapshot directory for a transaction ID.
func (c Config) BackupPathFor(txID string) string { return filepath.Join(c.BackupDir, txID) }

// Canonicalize expands ~ and $VAR, then makes the path absolute against cwd. Symlinks are
// deliberately NOT resolved: the target of a symlink asset is the link itself, and resolving
// would make rv write through it.
func (c Config) Canonicalize(path string) string {
	p := os.ExpandEnv(path)
	switch {
	case p == "~":
		p = c.Home
	case strings.HasPrefix(p, "~/"):
		p = filepath.Join(c.Home, p[2:])
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return abs
}

// IsSafeSubpath reports whether target lies inside base. Both sides are lexically cleaned and
// compared with a separator boundary so "/repo-evil" is not treated as inside "/repo".
func (c Config) IsSafeSubpath(base, target string) bool {
	b := c.Canonicalize(base)
	t := c.Canonicalize(target)
	if b == t {
		return true
	}
	rel, err := filepath.Rel(b, t)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// DetectSymlinkLoop follows the symlink chain starting at path and reports whether it cycles.
// A path that is not a symlink, or a dangling link, is not a loop.
func (c Config) DetectSymlinkLoop(path string) bool {
	visited := make(map[string]bool)
	cur := c.Canonicalize(path)
	for depth := 0; depth < maxSymlinkDepth; depth++ {
		fi, err := os.Lstat(cur)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return false
		}
		if visited[cur] {
			return true
		}
		visited[cur] = true

		next, err := os.Readlink(cur)
		if err != nil {
			return false
		}
		if !filepath.IsAbs(next) {
			next = filepath.Join(filepath.Dir(cur), next)
		}
		cur = filepath.Clean(next)
	}
	// ponytail: depth cap treated as a loop; a legitimate 64-deep link chain does not exist.
	return true
}
