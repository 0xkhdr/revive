package providers

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// catalogue drives one table across every provider, so the per-provider criteria are covered
// uniformly rather than nine times by hand.
type providerCase struct {
	build      func(Deps) Provider
	binaries   []string // must all be present for availability
	pkg        string
	installedC string // the command that answers "is it installed"
	installedO string // output that means "installed"
	installC   string // the command that installs it
}

func catalogue() map[string]providerCase {
	return map[string]providerCase{
		"apt": {
			build: NewApt, binaries: []string{"apt-get", "dpkg"}, pkg: "git",
			installedC: "dpkg -s git", installedO: "Status: install ok installed\n",
			installC: "apt-get install -y git",
		},
		"brew": {
			build: NewBrew, binaries: []string{"brew"}, pkg: "fzf",
			installedC: "brew list --versions fzf", installC: "brew install fzf",
		},
		"pacman": {
			build: NewPacman, binaries: []string{"pacman"}, pkg: "ripgrep",
			installedC: "pacman -Q ripgrep", installC: "pacman -S --noconfirm ripgrep",
		},
		"dnf": {
			build: NewDnf, binaries: []string{"dnf"}, pkg: "jq",
			installedC: "rpm -q jq", installC: "dnf install -y jq",
		},
		"flatpak": {
			build: NewFlatpak, binaries: []string{"flatpak"}, pkg: "org.gimp.GIMP",
			installedC: "flatpak info org.gimp.GIMP", installC: "flatpak install -y org.gimp.GIMP",
		},
		"snap": {
			build: NewSnap, binaries: []string{"snap"}, pkg: "code",
			installedC: "snap list code", installC: "snap install code",
		},
		"nix": {
			build: NewNix, binaries: []string{"nix-env"}, pkg: "ripgrep",
			installedC: "nix-env -q ripgrep",
			// The manifest writes the bare name; the nixpkgs. prefix is added at the boundary.
			installC: "nix-env -iA nixpkgs.ripgrep",
		},
		"cargo": {
			build: NewCargo, binaries: []string{"cargo"}, pkg: "bat",
			installedC: "cargo install --list", installedO: "bat v0.24.0:\n    bat\n",
			installC: "cargo install bat",
		},
		"pip": {
			build: NewPip, binaries: []string{"pip3"}, pkg: "httpie",
			installedC: "pip3 show httpie", installC: "pip3 install --user httpie",
		},
		"docker": {
			build: NewDocker, binaries: []string{"docker"}, pkg: "redis:7",
			installedC: "docker image inspect redis:7", installC: "docker pull redis:7",
		},
	}
}

// Phase 7: each provider reports unavailable when its binary is absent.
func TestProvidersReportUnavailability(t *testing.T) {
	t.Parallel()
	for name, tc := range catalogue() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newFakeRunner(tc.binaries...)
			d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))
			require.True(t, tc.build(d).IsAvailable())

			// Dropping any single required binary makes it unavailable — apt needs dpkg too.
			for _, missing := range tc.binaries {
				partial := newFakeRunner(tc.binaries...)
				delete(partial.Path, missing)
				d, _ := testDeps(partial, filepath.Join(t.TempDir(), "cache.json"))
				require.False(t, tc.build(d).IsAvailable(), "without %s", missing)
			}
		})
	}
}

// Phase 7: each provider skips an already-installed package without running an install command.
func TestProvidersSkipInstalledPackages(t *testing.T) {
	t.Parallel()
	for name, tc := range catalogue() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newFakeRunner(tc.binaries...)
			r.Out[tc.installedC] = tc.installedO
			d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

			require.NoError(t, tc.build(d).Install(context.Background(),
				[]string{tc.pkg}, InstallOptions{UseCache: true}))
			require.False(t, r.ran(tc.installC), "nothing to install, so nothing may run")
		})
	}
}

// Phase 7: the install path runs the documented command.
func TestProvidersInstallMissingPackages(t *testing.T) {
	t.Parallel()
	for name, tc := range catalogue() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newFakeRunner(tc.binaries...)
			r.Fail[tc.installedC] = errors.New("not installed")
			d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

			require.NoError(t, tc.build(d).Install(context.Background(),
				[]string{tc.pkg}, InstallOptions{UseCache: true}))
			require.True(t, r.ran(tc.installC), "expected %q, got %v", tc.installC, r.commands())
		})
	}
}

// Phase 7: the dry-run path runs no command.
func TestProvidersDryRunRunsNothing(t *testing.T) {
	t.Parallel()
	for name, tc := range catalogue() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newFakeRunner(tc.binaries...)
			r.Fail[tc.installedC] = errors.New("not installed")
			d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

			require.NoError(t, tc.build(d).Install(context.Background(),
				[]string{tc.pkg}, InstallOptions{DryRun: true, UseCache: true}))
			require.False(t, r.ran(tc.installC))
		})
	}
}

// Availability is deliberately not checked under --dry-run: previewing a macOS manifest from a
// Linux laptop has to work.
func TestDryRunDoesNotRequireAvailability(t *testing.T) {
	t.Parallel()
	r := newFakeRunner() // nothing on PATH at all
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

	require.NoError(t, NewBrew(d).Install(context.Background(),
		[]string{"fzf"}, InstallOptions{DryRun: true, UseCache: true}))
}

func TestInstallOnAnUnavailableProviderErrors(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

	err := NewBrew(d).Install(context.Background(), []string{"fzf"}, InstallOptions{UseCache: true})
	require.ErrorIs(t, err, ErrUnavailable)
	require.Contains(t, err.Error(), "brew")
}

func TestInstallWithNoPackagesDoesNothing(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))
	require.NoError(t, NewApt(d).Install(context.Background(), nil, InstallOptions{}))
	require.Empty(t, r.commands())
}

// apt's dpkg check must read the status line: dpkg exits 0 for a removed-but-configured package.
func TestAptRequiresTheStatusLine(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("apt-get", "dpkg")
	r.Out["dpkg -s git"] = "Status: deinstall ok config-files\n"
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

	installed, err := NewApt(d).IsInstalled(context.Background(), "git")
	require.NoError(t, err)
	require.False(t, installed, "a removed-but-configured package is not installed")
}

// cargo's crate list is line-oriented; a crate must not match a substring of another.
func TestCargoMatchesTheCrateName(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("cargo")
	r.Out["cargo install --list"] = "bat-extras v1.0.0:\n    batman\nripgrep v14.0.0:\n    rg\n"
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))
	p := NewCargo(d)

	installed, err := p.IsInstalled(context.Background(), "ripgrep")
	require.NoError(t, err)
	require.True(t, installed)

	installed, err = p.IsInstalled(context.Background(), "bat")
	require.NoError(t, err)
	require.False(t, installed, "bat must not match bat-extras")
}

func TestCargoListFailureMeansNotInstalled(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("cargo")
	r.Fail["cargo install --list"] = errors.New("cargo exploded")
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

	installed, err := NewCargo(d).IsInstalled(context.Background(), "bat")
	require.NoError(t, err)
	require.False(t, installed)
}

// pip is available under either name, and prefers pip3.
func TestPipPrefersPip3(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ have, want string }{
		"pip3 present": {"pip3", "pip3"},
		"only pip":     {"pip", "pip"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newFakeRunner(tc.have)
			r.Fail[tc.want+" show httpie"] = errors.New("not installed")
			d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

			p := NewPip(d)
			require.True(t, p.IsAvailable())
			require.NoError(t, p.Install(context.Background(), []string{"httpie"}, InstallOptions{UseCache: true}))
			require.True(t, r.ran(tc.want+" install --user httpie"), "%v", r.commands())
		})
	}

	r := newFakeRunner()
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))
	require.False(t, NewPip(d).IsAvailable())
}

// Providers that cannot batch install one package per command.
func TestOnePackagePerCommandProviders(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build  func(Deps) Provider
		binary string
		checks []string
		pkgs   []string
		expect []string
	}{
		"flatpak": {NewFlatpak, "flatpak", []string{"flatpak info a", "flatpak info b"},
			[]string{"a", "b"}, []string{"flatpak install -y a", "flatpak install -y b"}},
		"snap": {NewSnap, "snap", []string{"snap list a", "snap list b"},
			[]string{"a", "b"}, []string{"snap install a", "snap install b"}},
		"nix": {NewNix, "nix-env", []string{"nix-env -q a", "nix-env -q b"},
			[]string{"a", "b"}, []string{"nix-env -iA nixpkgs.a", "nix-env -iA nixpkgs.b"}},
		"docker": {NewDocker, "docker", []string{"docker image inspect a", "docker image inspect b"},
			[]string{"a", "b"}, []string{"docker pull a", "docker pull b"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newFakeRunner(tc.binary)
			for _, c := range tc.checks {
				r.Fail[c] = errors.New("not installed")
			}
			d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

			require.NoError(t, tc.build(d).Install(context.Background(), tc.pkgs, InstallOptions{UseCache: true}))
			for _, want := range tc.expect {
				require.True(t, r.ran(want), "expected %q, got %v", want, r.commands())
			}
		})
	}
}
