// Package providers adapts each package manager to one uniform interface. Step 10 of the
// restore engine walks the providers with non-empty lists, in a fixed order.
package providers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// ErrProvider wraps every provider failure, so callers branch on it rather than on text.
var ErrProvider = errors.New("package provider failed")

// ErrUnavailable is returned when a provider's binary is not on this machine.
var ErrUnavailable = errors.New("package manager is not available on this platform")

// Provider adapts one package manager.
type Provider interface {
	// Name is the binary probed on PATH, and the cache key.
	Name() string
	// IsAvailable reports whether this manager is usable on this machine.
	IsAvailable() bool
	// IsInstalled is mandatory for every provider: it is what makes restore idempotent. A
	// provider that cannot answer it has no business installing anything.
	IsInstalled(ctx context.Context, pkg string) (bool, error)
	// Install installs the packages that are missing.
	Install(ctx context.Context, pkgs []string, opts InstallOptions) error
}

// InstallOptions controls one install call.
type InstallOptions struct {
	DryRun bool
	// UseCache is false under --force-packages.
	UseCache bool
}

// Runner executes commands and resolves binaries. Providers depend on this rather than on
// os/exec, so every one of them is testable without touching the system.
type Runner interface {
	Run(ctx context.Context, cmd []string) ([]byte, error)
	LookPath(name string) (string, bool)
}

// Deps are the seams every provider shares.
type Deps struct {
	Runner Runner
	Cache  *Cache
	Log    *slog.Logger
	// Sleep is the retry backoff, injectable so tests do not wait six real seconds.
	Sleep func(time.Duration)
	// Now backs the cache TTL.
	Now func() time.Time
}

func (d Deps) sleep(dur time.Duration) {
	if d.Sleep != nil {
		d.Sleep(dur)
		return
	}
	time.Sleep(dur)
}

func (d Deps) log() *slog.Logger {
	if d.Log != nil {
		return d.Log
	}
	return slog.New(slog.DiscardHandler)
}

// ExecRunner is the real Runner: os/exec with a context, never a shell.
type ExecRunner struct{}

// Run executes cmd and returns its combined output.
func (ExecRunner) Run(ctx context.Context, cmd []string) ([]byte, error) {
	if len(cmd) == 0 {
		return nil, fmt.Errorf("%w: empty command", ErrProvider)
	}
	out, err := exec.CommandContext(ctx, cmd[0], cmd[1:]...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", cmd[0], err, out)
	}
	return out, nil
}

// LookPath resolves a binary on PATH.
func (ExecRunner) LookPath(name string) (string, bool) {
	path, err := exec.LookPath(name)
	return path, err == nil
}
