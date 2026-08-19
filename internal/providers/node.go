package providers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrBadNodeVersion is returned when a version string fails validation. It is checked before the
// value goes anywhere near the nvm shell command.
var ErrBadNodeVersion = errors.New("invalid node version")

// nodeVersionPattern is the whitelist a version must satisfy. This is the one place rv invokes a
// shell — nvm exists only as a shell function — so the interpolated value is validated first.
var nodeVersionPattern = regexp.MustCompile(`^v?\d+(\.\d+)*$`)

// NodeProvider manages a Node version rather than a package list.
type NodeProvider struct {
	deps Deps
	// RepoDir resolves a relative version_file.
	RepoDir string
	// Home backs the default NVM_DIR of ~/.nvm.
	Home string
	// Getenv reads NVM_DIR; injectable so tests need no process environment.
	Getenv func(string) string
}

// NewNode builds the node provider.
func NewNode(d Deps, repoDir, home string) *NodeProvider {
	return &NodeProvider{deps: d, RepoDir: repoDir, Home: home, Getenv: os.Getenv}
}

// Name is the binary probed on PATH.
func (n *NodeProvider) Name() string { return "node" }

// IsAvailable reports whether node itself is present. It is not required to install one.
func (n *NodeProvider) IsAvailable() bool {
	_, ok := n.deps.Runner.LookPath("node")
	return ok
}

// IsInstalled reports whether the requested version is the one currently active. "20" matches
// "20.11.0", because a manifest that pins a major line should not churn on every patch release.
func (n *NodeProvider) IsInstalled(ctx context.Context, version string) (bool, error) {
	current := n.currentVersion(ctx)
	if current == "" {
		return false, nil
	}
	return strings.HasPrefix(current, strings.TrimPrefix(version, "v")), nil
}

// Install makes the target version active, via fnm or nvm.
func (n *NodeProvider) Install(ctx context.Context, versions []string, opts InstallOptions) error {
	if len(versions) == 0 {
		return nil
	}
	return n.InstallVersion(ctx, versions[0], "", opts)
}

// InstallVersion resolves the target from an explicit version or a version file and installs it.
//
// A missing version file is a warning and a skip, not an error: the manifest may be shared with
// a machine that does not use node.
func (n *NodeProvider) InstallVersion(ctx context.Context, version, versionFile string, opts InstallOptions) error {
	target, err := n.resolveTarget(version, versionFile)
	if err != nil {
		return err
	}
	if target == "" {
		n.deps.log().Info("no Node.js version target defined")
		return nil
	}

	current := n.currentVersion(ctx)
	if current != "" && strings.HasPrefix(current, target) {
		n.deps.log().Info("node version matches", "current", current, "target", target)
		return nil
	}
	if opts.DryRun {
		n.deps.log().Info("dry run: would install node", "target", target, "current", current)
		return nil
	}

	if _, ok := n.deps.Runner.LookPath("fnm"); ok {
		if _, err := n.deps.Runner.Run(ctx, []string{"fnm", "install", target}); err == nil {
			return nil
		}
		n.deps.log().Warn("fnm install failed, falling back to nvm", "target", target)
	}

	nvmSh := filepath.Join(n.nvmDir(), "nvm.sh")
	if _, statErr := os.Stat(nvmSh); statErr == nil {
		// The single place rv runs a shell, because nvm is a shell function and cannot be
		// exec'd. `target` passed the version whitelist above before reaching this string.
		cmd := []string{"bash", "-c", ". " + nvmSh + " && nvm install " + target}
		if _, err := n.deps.Runner.Run(ctx, cmd); err == nil {
			return nil
		}
		n.deps.log().Warn("nvm install failed", "target", target)
	}

	return fmt.Errorf("%w: node version mismatch (current: %s, target: %s) and neither fnm nor nvm "+
		"is available to install it", ErrProvider, orNone(current), target)
}

// resolveTarget picks the version: explicit wins over the version file. The result is validated
// before any caller can interpolate it into the nvm command.
func (n *NodeProvider) resolveTarget(version, versionFile string) (string, error) {
	target := version
	if target == "" && versionFile != "" {
		raw, err := os.ReadFile(filepath.Join(n.RepoDir, versionFile))
		if err != nil {
			n.deps.log().Warn("node version file could not be read", "file", versionFile, "error", err)
			return "", nil
		}
		target = strings.TrimSpace(string(raw))
	}
	if target == "" {
		return "", nil
	}
	if !nodeVersionPattern.MatchString(target) {
		return "", fmt.Errorf("%w: %q must match %s", ErrBadNodeVersion, target, nodeVersionPattern)
	}
	return strings.TrimPrefix(target, "v"), nil
}

// currentVersion returns the active node version without its leading v, or "" when node is
// absent.
func (n *NodeProvider) currentVersion(ctx context.Context) string {
	if _, ok := n.deps.Runner.LookPath("node"); !ok {
		return ""
	}
	out, err := n.deps.Runner.Run(ctx, []string{"node", "-v"})
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(string(out)), "v")
}

func (n *NodeProvider) nvmDir() string {
	if n.Getenv != nil {
		if dir := n.Getenv("NVM_DIR"); dir != "" {
			return dir
		}
	}
	return filepath.Join(n.Home, ".nvm")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
