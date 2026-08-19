package providers

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Retry policy: three attempts with 2 s then 4 s of backoff.
const (
	retryAttempts    = 3
	retryInitialWait = 2 * time.Second
	retryBackoff     = 2
)

// base carries the behavior every provider shares. A provider supplies its binaries, its
// installed-check and its install commands; everything else is here.
//
// The alternative — the reference's four parallel if/elif chains — means adding a provider
// touches four places and forgetting one silently drops packages.
type base struct {
	name string
	// binaries must ALL be on PATH for the provider to be available. apt needs apt-get and dpkg.
	binaries []string
	deps     Deps

	// installed answers "is this package already on the machine".
	installed func(ctx context.Context, pkg string) (bool, error)
	// commands turns the missing set into the commands that install it. Providers that install
	// one package per invocation return several.
	commands func(missing []string) [][]string
}

// Name is the binary probed on PATH, and the package cache key.
func (b *base) Name() string { return b.name }

// IsAvailable reports whether every binary this provider needs is on PATH.
func (b *base) IsAvailable() bool {
	for _, binary := range b.binaries {
		if _, ok := b.deps.Runner.LookPath(binary); !ok {
			return false
		}
	}
	return true
}

// IsInstalled reports whether a package is already installed.
func (b *base) IsInstalled(ctx context.Context, pkg string) (bool, error) {
	return b.installed(ctx, pkg)
}

// FilterMissing returns the packages that still need installing, consulting the cache first.
func (b *base) FilterMissing(ctx context.Context, pkgs []string, useCache bool) ([]string, error) {
	var missing []string
	for _, pkg := range pkgs {
		if useCache && b.deps.Cache.IsInstalled(b.name, pkg) {
			b.deps.log().Debug("package already in cache", "provider", b.name, "package", pkg)
			continue
		}
		ok, err := b.IsInstalled(ctx, pkg)
		if err != nil {
			return nil, err
		}
		if ok {
			b.deps.Cache.MarkInstalled(b.name, pkg)
			continue
		}
		missing = append(missing, pkg)
	}
	return missing, nil
}

// Install runs the uniform flow: filter, then install what is left.
//
// Availability is deliberately NOT checked under --dry-run: a dry run on a Linux laptop should
// still be able to preview a manifest destined for a macOS machine.
func (b *base) Install(ctx context.Context, pkgs []string, opts InstallOptions) error {
	if len(pkgs) == 0 {
		return nil
	}
	if !opts.DryRun && !b.IsAvailable() {
		return fmt.Errorf("%w: %s", ErrUnavailable, b.name)
	}

	missing, err := b.FilterMissing(ctx, pkgs, opts.UseCache)
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		b.deps.log().Info("all packages already installed", "provider", b.name)
		return nil
	}
	if opts.DryRun {
		b.deps.log().Info("dry run: would install", "provider", b.name, "packages", strings.Join(missing, " "))
		return nil
	}

	for _, cmd := range b.commands(missing) {
		if _, err := b.ExecuteWithRetry(ctx, cmd); err != nil {
			return err
		}
	}
	b.deps.Cache.MarkInstalled(b.name, missing...)
	return nil
}

// ExecuteWithRetry runs a command with exponential backoff, and wraps the last failure.
//
// The retry is currently blind, matching the reference. Classification — fail
// fast on "not found" and "permission denied", retry on network and lock contention — as a
// follow-up, since burning six seconds on an error that can never succeed is pure latency.
func (b *base) ExecuteWithRetry(ctx context.Context, cmd []string) ([]byte, error) {
	var lastErr error
	wait := retryInitialWait

	for attempt := 1; attempt <= retryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out, err := b.deps.Runner.Run(ctx, cmd)
		if err == nil {
			return out, nil
		}
		lastErr = err
		b.deps.log().Warn("command failed",
			"provider", b.name, "attempt", attempt, "of", retryAttempts, "error", err)

		if attempt < retryAttempts {
			b.deps.sleep(wait)
			wait *= retryBackoff
		}
	}
	return nil, fmt.Errorf("%w: %s: after %d attempts: %w", ErrProvider, b.name, retryAttempts, lastErr)
}

// exitZero turns a command into a boolean installed-check: exit 0 means installed, any failure
// means not installed. A package manager that cannot be queried is not an error here — the
// install attempt is what reports the real problem.
func (b *base) exitZero(ctx context.Context, cmd ...string) (bool, error) {
	if _, err := b.deps.Runner.Run(ctx, cmd); err != nil {
		//nolint:nilerr // A non-zero exit IS the answer "not installed"; it is not a failure to
		// report. A package manager that genuinely cannot run surfaces that at install time.
		return false, nil
	}
	return true, nil
}

// oneCommandPer builds one install command per package, for managers that cannot batch.
func oneCommandPer(missing []string, build func(pkg string) []string) [][]string {
	cmds := make([][]string, 0, len(missing))
	for _, pkg := range missing {
		cmds = append(cmds, build(pkg))
	}
	return cmds
}

// singleCommand builds one install command for the whole batch.
func singleCommand(prefix []string, missing []string) [][]string {
	return [][]string{append(append([]string{}, prefix...), missing...)}
}
