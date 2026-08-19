package providers

import (
	"context"
	"strings"
)

// The catalogue. Each provider is the base type plus its binaries, its installed-check and its
// install commands — see docs/06 §3 for the contract these implement.

// NewApt builds the Debian/Ubuntu provider. It probes dpkg as well as apt-get: the install runs
// through one and the check through the other, so either being absent means unavailable.
func NewApt(d Deps) Provider {
	b := &base{name: "apt-get", binaries: []string{"apt-get", "dpkg"}, deps: d}
	b.installed = func(ctx context.Context, pkg string) (bool, error) {
		out, err := d.Runner.Run(ctx, []string{"dpkg", "-s", pkg})
		// dpkg reports removed-but-configured packages with exit 0, so the status line is what
		// actually answers the question.
		return err == nil && strings.Contains(string(out), "Status: install ok installed"), nil
	}
	// No sudo is prepended: a silent privilege escalation is worse than a clear permission error.
	b.commands = func(missing []string) [][]string {
		return singleCommand([]string{"apt-get", "install", "-y"}, missing)
	}
	return b
}

// NewBrew builds the Homebrew provider.
func NewBrew(d Deps) Provider {
	b := &base{name: "brew", binaries: []string{"brew"}, deps: d}
	b.installed = func(ctx context.Context, pkg string) (bool, error) {
		return b.exitZero(ctx, "brew", "list", "--versions", pkg)
	}
	b.commands = func(missing []string) [][]string {
		return singleCommand([]string{"brew", "install"}, missing)
	}
	return b
}

// NewPacman builds the Arch provider.
func NewPacman(d Deps) Provider {
	b := &base{name: "pacman", binaries: []string{"pacman"}, deps: d}
	b.installed = func(ctx context.Context, pkg string) (bool, error) {
		return b.exitZero(ctx, "pacman", "-Q", pkg)
	}
	b.commands = func(missing []string) [][]string {
		return singleCommand([]string{"pacman", "-S", "--noconfirm"}, missing)
	}
	return b
}

// NewDnf builds the Fedora/RHEL provider. It installs with dnf and checks with rpm.
func NewDnf(d Deps) Provider {
	b := &base{name: "dnf", binaries: []string{"dnf"}, deps: d}
	b.installed = func(ctx context.Context, pkg string) (bool, error) {
		return b.exitZero(ctx, "rpm", "-q", pkg)
	}
	b.commands = func(missing []string) [][]string {
		return singleCommand([]string{"dnf", "install", "-y"}, missing)
	}
	return b
}

// NewFlatpak builds the Flatpak provider, which installs one ref at a time.
func NewFlatpak(d Deps) Provider {
	b := &base{name: "flatpak", binaries: []string{"flatpak"}, deps: d}
	b.installed = func(ctx context.Context, ref string) (bool, error) {
		return b.exitZero(ctx, "flatpak", "info", ref)
	}
	b.commands = func(missing []string) [][]string {
		return oneCommandPer(missing, func(ref string) []string {
			return []string{"flatpak", "install", "-y", ref}
		})
	}
	return b
}

// NewSnap builds the Snap provider, which installs one package at a time.
func NewSnap(d Deps) Provider {
	b := &base{name: "snap", binaries: []string{"snap"}, deps: d}
	b.installed = func(ctx context.Context, pkg string) (bool, error) {
		return b.exitZero(ctx, "snap", "list", pkg)
	}
	b.commands = func(missing []string) [][]string {
		return oneCommandPer(missing, func(pkg string) []string {
			return []string{"snap", "install", pkg}
		})
	}
	return b
}

// NewNix builds the Nix provider.
//
// Package names are written bare in the manifest (`ripgrep`) and gain the `nixpkgs.` prefix only
// at the command boundary, so the manifest stays portable.
func NewNix(d Deps) Provider {
	b := &base{name: "nix-env", binaries: []string{"nix-env"}, deps: d}
	b.installed = func(ctx context.Context, pkg string) (bool, error) {
		return b.exitZero(ctx, "nix-env", "-q", pkg)
	}
	b.commands = func(missing []string) [][]string {
		return oneCommandPer(missing, func(pkg string) []string {
			return []string{"nix-env", "-iA", "nixpkgs." + pkg}
		})
	}
	return b
}

// NewCargo builds the Rust provider.
func NewCargo(d Deps) Provider {
	b := &base{name: "cargo", binaries: []string{"cargo"}, deps: d}
	b.installed = func(ctx context.Context, pkg string) (bool, error) {
		out, err := d.Runner.Run(ctx, []string{"cargo", "install", "--list"})
		if err != nil {
			//nolint:nilerr // Not being able to list crates means "not installed", the same as
			// an empty list; the install attempt is what reports a real cargo problem.
			return false, nil
		}
		// `cargo install --list` prints "<crate> v1.2.3:" then an indented binary list, so the
		// crate name has to be matched at the start of a line.
		for _, line := range strings.Split(string(out), "\n") {
			if name, _, _ := strings.Cut(strings.TrimRight(line, ":"), " "); name == pkg {
				return true, nil
			}
		}
		return false, nil
	}
	b.commands = func(missing []string) [][]string {
		return singleCommand([]string{"cargo", "install"}, missing)
	}
	return b
}

// NewPip builds the Python provider. It always installs with --user and never touches a system
// site-packages.
func NewPip(d Deps) Provider {
	b := &base{name: "pip", deps: d}
	pipCmd := func() string {
		if _, ok := d.Runner.LookPath("pip3"); ok {
			return "pip3"
		}
		return "pip"
	}
	// Availability is either binary, not both, so binaries stays empty and IsAvailable is
	// overridden through the wrapper below.
	b.installed = func(ctx context.Context, pkg string) (bool, error) {
		return b.exitZero(ctx, pipCmd(), "show", pkg)
	}
	b.commands = func(missing []string) [][]string {
		return singleCommand([]string{pipCmd(), "install", "--user"}, missing)
	}
	return &pipProvider{base: b, runner: d.Runner}
}

// pipProvider exists only because pip is available under either of two binary names.
type pipProvider struct {
	*base
	runner Runner
}

// IsAvailable reports whether either pip or pip3 is on PATH.
func (p *pipProvider) IsAvailable() bool {
	if _, ok := p.runner.LookPath("pip3"); ok {
		return true
	}
	_, ok := p.runner.LookPath("pip")
	return ok
}

// NewDocker builds the Docker image provider, which pulls one image at a time.
//
// Available iff docker is on PATH — the daemon may still be unreachable, so a pull failure
// surfaces docker's own error rather than claiming the image does not exist.
func NewDocker(d Deps) Provider {
	b := &base{name: "docker", binaries: []string{"docker"}, deps: d}
	b.installed = func(ctx context.Context, image string) (bool, error) {
		return b.exitZero(ctx, "docker", "image", "inspect", image)
	}
	b.commands = func(missing []string) [][]string {
		return oneCommandPer(missing, func(image string) []string {
			return []string{"docker", "pull", image}
		})
	}
	return b
}
