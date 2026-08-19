package providers

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 7: the registry's install order is the fixed sequence, not map iteration order.
func TestInstallOrderIsFixed(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{
		"brew", "apt", "flatpak", "snap", "pacman", "dnf", "nix", "cargo", "pip",
	}, InstallOrder)

	d, _ := testDeps(newFakeRunner(), filepath.Join(t.TempDir(), "cache.json"))
	r := NewRegistry(d, t.TempDir(), t.TempDir())

	// Repeating it must give the same order every time — a map range would not.
	var first []string
	for range 20 {
		var got []string
		for _, entry := range r.Ordered() {
			got = append(got, entry.Group)
		}
		if first == nil {
			first = got
		}
		require.Equal(t, InstallOrder, got)
		require.Equal(t, first, got)
	}
}

// Every group a profile may name resolves to a provider, and every provider is in the order.
func TestRegistryCoversEveryGroup(t *testing.T) {
	t.Parallel()
	d, _ := testDeps(newFakeRunner(), filepath.Join(t.TempDir(), "cache.json"))
	r := NewRegistry(d, t.TempDir(), t.TempDir())

	for _, group := range InstallOrder {
		p, ok := r.Get(group)
		require.True(t, ok, group)
		require.NotEmpty(t, p.Name(), group)
	}
	require.Len(t, r.providers, len(InstallOrder), "a provider missing from InstallOrder never runs")

	_, ok := r.Get("gem")
	require.False(t, ok)

	require.Equal(t, "docker", r.Docker().Name())
	require.Equal(t, "node", r.Node().Name())
}

// The provider name is the binary probed on PATH, which is also the cache key the Python
// implementation wrote.
func TestProviderNamesMatchTheCacheKeys(t *testing.T) {
	t.Parallel()
	d, _ := testDeps(newFakeRunner(), filepath.Join(t.TempDir(), "cache.json"))
	r := NewRegistry(d, t.TempDir(), t.TempDir())

	for group, want := range map[string]string{
		"apt": "apt-get", "brew": "brew", "pacman": "pacman", "dnf": "dnf",
		"flatpak": "flatpak", "snap": "snap", "nix": "nix-env", "cargo": "cargo", "pip": "pip",
	} {
		p, ok := r.Get(group)
		require.True(t, ok, group)
		require.Equal(t, want, p.Name(), group)
	}
}
