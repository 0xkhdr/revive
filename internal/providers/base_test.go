package providers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Phase 7: ExecuteWithRetry retries three times with 2 s then 4 s of backoff, then returns a
// ProviderError wrapping the last failure.
func TestExecuteWithRetry(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("brew")
	r.Fail["brew install fzf"] = errors.New("network unreachable")
	d, slept := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

	b := &base{name: "brew", binaries: []string{"brew"}, deps: d}
	_, err := b.ExecuteWithRetry(context.Background(), []string{"brew", "install", "fzf"})

	require.ErrorIs(t, err, ErrProvider)
	require.Contains(t, err.Error(), "network unreachable", "the last failure must survive the wrap")
	require.Len(t, r.commands(), 3, "three attempts")
	require.Equal(t, []time.Duration{2 * time.Second, 4 * time.Second}, *slept,
		"backoff is 2s then 4s, and there is no sleep after the final attempt")
}

func TestExecuteWithRetrySucceedsAfterATransientFailure(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("brew")
	r.FailTimes["brew install fzf"] = 2
	r.Out["brew install fzf"] = "installed"
	d, slept := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

	b := &base{name: "brew", deps: d}
	out, err := b.ExecuteWithRetry(context.Background(), []string{"brew", "install", "fzf"})
	require.NoError(t, err)
	require.Equal(t, "installed", string(out))
	require.Len(t, *slept, 2)
}

func TestExecuteWithRetryStopsOnCancellation(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("brew")
	r.Fail["brew install fzf"] = errors.New("boom")
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &base{name: "brew", deps: d}
	_, err := b.ExecuteWithRetry(ctx, []string{"brew", "install", "fzf"})
	require.ErrorIs(t, err, context.Canceled, "Ctrl-C must not wait out the backoff")
	require.Empty(t, r.commands())
}

func TestInstallFailureIsAProviderError(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("brew")
	r.Fail["brew list --versions fzf"] = errors.New("not installed")
	r.Fail["brew install fzf"] = errors.New("formula not found")
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

	err := NewBrew(d).Install(context.Background(), []string{"fzf"}, InstallOptions{UseCache: true})
	require.ErrorIs(t, err, ErrProvider)
}

// Phase 7: the cache honors its 24 h TTL.
func TestCacheTTL(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.json")
	now := time.Unix(1700000000, 0)
	c := NewCache(path, func() time.Time { return now })

	c.MarkInstalled("brew", "fzf")
	require.True(t, c.IsInstalled("brew", "fzf"))
	require.False(t, c.IsInstalled("brew", "ripgrep"))
	require.False(t, c.IsInstalled("apt-get", "fzf"), "the TTL and the entries are per provider")

	now = now.Add(23 * time.Hour)
	require.True(t, c.IsInstalled("brew", "fzf"))

	now = now.Add(2 * time.Hour)
	require.False(t, c.IsInstalled("brew", "fzf"), "an expired entry is a complete miss")
}

func TestCacheSkipsTheInstalledCheck(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("brew")
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))
	d.Cache.MarkInstalled("brew", "fzf")

	require.NoError(t, NewBrew(d).Install(context.Background(), []string{"fzf"}, InstallOptions{UseCache: true}))
	require.Empty(t, r.commands(), "a cache hit must not shell out at all")
}

// --force-packages bypasses the cache, so the real check runs again.
func TestUseCacheFalseBypassesTheCache(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("brew")
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))
	d.Cache.MarkInstalled("brew", "fzf")

	require.NoError(t, NewBrew(d).Install(context.Background(), []string{"fzf"}, InstallOptions{UseCache: false}))
	require.True(t, r.ran("brew list --versions fzf"))
}

// A successful IsInstalled populates the cache for the next run.
func TestFilterMissingPopulatesTheCache(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("brew")
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))

	require.NoError(t, NewBrew(d).Install(context.Background(), []string{"fzf"}, InstallOptions{UseCache: true}))
	require.True(t, d.Cache.IsInstalled("brew", "fzf"))
}

func TestInvalidate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.json")
	c := NewCache(path, nil)
	c.MarkInstalled("brew", "fzf")
	c.MarkInstalled("apt-get", "git")

	c.Invalidate("brew")
	require.False(t, c.IsInstalled("brew", "fzf"))
	require.True(t, c.IsInstalled("apt-get", "git"), "invalidating one provider leaves the others")
}

// Phase 7: InvalidateAll clears everything, which is what --force-packages calls.
func TestInvalidateAll(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.json")
	c := NewCache(path, nil)
	c.MarkInstalled("brew", "fzf")
	require.NoError(t, c.Save())
	require.FileExists(t, path)

	require.NoError(t, c.InvalidateAll())
	require.False(t, c.IsInstalled("brew", "fzf"))
	require.NoFileExists(t, path)
	require.NoError(t, c.InvalidateAll(), "removing an absent cache is not an error")
}

// Phase 7: a cache read failure yields an empty cache, never an error.
func TestCacheLoadFailuresAreNeverFatal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"malformed.json":   "{not json",
		"wrong shape.json": `["a list, not an object"]`,
		"null.json":        "null",
	} {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		c := NewCache(path, nil)
		require.False(t, c.IsInstalled("brew", "fzf"), name)
	}

	require.False(t, NewCache(filepath.Join(dir, "absent.json"), nil).IsInstalled("brew", "fzf"))
}

// The on-disk shape is the one the Python implementation reads and writes.
func TestCacheOnDiskFormat(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.json")
	now := time.Unix(1716000000, 0)
	c := NewCache(path, func() time.Time { return now })
	c.MarkInstalled("apt-get", "git", "zsh")
	require.NoError(t, c.Save())

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]struct {
		Installed   []string `json:"installed"`
		LastUpdated float64  `json:"last_updated"`
	}
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, []string{"git", "zsh"}, got["apt-get"].Installed)
	require.InDelta(t, 1716000000.0, got["apt-get"].LastUpdated, 0.001)

	// It reloads into an equivalent cache.
	reloaded := NewCache(path, func() time.Time { return now })
	require.True(t, reloaded.IsInstalled("apt-get", "git"))
}

func TestMarkInstalledDeduplicates(t *testing.T) {
	t.Parallel()
	c := NewCache(filepath.Join(t.TempDir(), "cache.json"), nil)
	c.MarkInstalled("brew", "fzf", "fzf")
	c.MarkInstalled("brew", "fzf")
	require.NoError(t, c.Save())

	raw, err := os.ReadFile(c.path)
	require.NoError(t, err)
	var got map[string]cacheEntry
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, []string{"fzf"}, got["brew"].Installed)
}

func TestSaveIsANoOpWhenNothingChanged(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cache.json")
	require.NoError(t, NewCache(path, nil).Save())
	require.NoFileExists(t, path, "an untouched cache writes nothing")
}

// A nil cache is usable, so a caller that has no cache needs no special case.
func TestNilCacheIsSafe(t *testing.T) {
	t.Parallel()
	var c *Cache
	require.False(t, c.IsInstalled("brew", "fzf"))
	require.NotPanics(t, func() { c.MarkInstalled("brew", "fzf"); c.Invalidate("brew") })
	require.NoError(t, c.Save())
	require.NoError(t, c.InvalidateAll())
}
