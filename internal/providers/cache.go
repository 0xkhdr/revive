package providers

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// cacheTTL bounds how long a cached "installed" answer is trusted. An expired entry is a
// complete miss.
const cacheTTL = 24 * time.Hour

// cacheEntry is one provider's cached state, in the on-disk shape the Python implementation
// wrote: {"apt-get": {"installed": [...], "last_updated": 1716000000.0}}.
type cacheEntry struct {
	Installed   []string `json:"installed"`
	LastUpdated float64  `json:"last_updated"`
}

// Cache is the package idempotency cache at ~/.config/rv/package-cache.json.
//
// It is an optimization and never a correctness dependency: IsInstalled is the real check, so a
// load failure yields an empty cache rather than an error, and a save failure is a warning.
//
// Loaded once per run, mutated in memory, written once — the Python version read-modify-wrote it
// from several call sites with no locking.
type Cache struct {
	mu      sync.Mutex
	path    string
	now     func() time.Time
	entries map[string]cacheEntry
	dirty   bool
}

// NewCache loads the cache at path. It never fails: an unreadable or malformed file is simply an
// empty cache.
func NewCache(path string, now func() time.Time) *Cache {
	if now == nil {
		now = time.Now
	}
	c := &Cache{path: path, now: now, entries: map[string]cacheEntry{}}

	raw, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	var entries map[string]cacheEntry
	if err := json.Unmarshal(raw, &entries); err != nil || entries == nil {
		return c
	}
	c.entries = entries
	return c
}

// IsInstalled reports whether the cache says a package is installed and has not expired.
func (c *Cache) IsInstalled(provider, pkg string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[provider]
	if !ok {
		return false
	}
	updated := time.Unix(0, int64(entry.LastUpdated*float64(time.Second)))
	if c.now().Sub(updated) > cacheTTL {
		return false
	}
	return slices.Contains(entry.Installed, pkg)
}

// MarkInstalled records packages as installed and refreshes the provider's timestamp.
func (c *Cache) MarkInstalled(provider string, pkgs ...string) {
	if c == nil || len(pkgs) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	entry := c.entries[provider]
	for _, pkg := range pkgs {
		if !slices.Contains(entry.Installed, pkg) {
			entry.Installed = append(entry.Installed, pkg)
		}
	}
	entry.LastUpdated = float64(c.now().UnixNano()) / float64(time.Second)
	c.entries[provider] = entry
	c.dirty = true
}

// Invalidate drops one provider's entry.
func (c *Cache) Invalidate(provider string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, provider)
	c.dirty = true
}

// InvalidateAll empties the cache and removes the file. --force-packages calls this before
// step 10.
func (c *Cache) InvalidateAll() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]cacheEntry{}
	c.dirty = false
	if err := os.Remove(c.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Save writes the cache through a temp file and a rename. Callers log a failure rather than
// failing the run.
func (c *Cache) Save() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return nil
	}

	raw, err := json.MarshalIndent(c.entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".package-cache-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), c.path); err != nil {
		return err
	}
	c.dirty = false
	return nil
}
