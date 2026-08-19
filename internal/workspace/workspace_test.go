package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)

func TestRegisterAndFind(t *testing.T) {
	t.Parallel()
	cfg := &Config{}

	ws, err := cfg.Register("/home/user/dotfiles", "", now)
	require.NoError(t, err)
	require.Equal(t, "dotfiles", ws.Name, "the name defaults to the directory")
	require.Equal(t, "/home/user/dotfiles", ws.Path)
	require.Equal(t, "dotfiles", cfg.DefaultWorkspace, "the first workspace becomes the default")

	got, ok := cfg.Find("dotfiles")
	require.True(t, ok)
	require.Equal(t, ws, got)

	_, ok = cfg.Find("nope")
	require.False(t, ok)
}

func TestRegisterWithAnExplicitName(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	ws, err := cfg.Register("/home/user/dotfiles", "dots", now)
	require.NoError(t, err)
	require.Equal(t, "dots", ws.Name)
}

// Re-registering the same path is an update, not a duplicate: `rv clone` into a directory that
// was already known must not fail.
func TestReRegisteringAPathUpdatesIt(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	_, err := cfg.Register("/home/user/dotfiles", "old", now)
	require.NoError(t, err)

	later := now.Add(time.Hour)
	ws, err := cfg.Register("/home/user/dotfiles", "new", later)
	require.NoError(t, err)
	require.Len(t, cfg.Workspaces, 1)
	require.Equal(t, "new", ws.Name)
	require.Equal(t, later, ws.LastAccessed)
}

func TestDuplicateNameForADifferentPath(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	_, err := cfg.Register("/home/user/a", "dots", now)
	require.NoError(t, err)

	_, err = cfg.Register("/home/user/b", "dots", now)
	require.ErrorIs(t, err, ErrDuplicate)
	require.Contains(t, err.Error(), "/home/user/a", "the error must name the path already using it")
}

func TestRemove(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	_, err := cfg.Register("/home/user/a", "a", now)
	require.NoError(t, err)
	_, err = cfg.Register("/home/user/b", "b", now)
	require.NoError(t, err)
	require.Equal(t, "a", cfg.DefaultWorkspace)

	require.NoError(t, cfg.Remove("a"))
	require.Len(t, cfg.Workspaces, 1)
	require.Equal(t, "b", cfg.DefaultWorkspace, "removing the default promotes another")

	require.ErrorIs(t, cfg.Remove("a"), ErrNotFound)

	require.NoError(t, cfg.Remove("b"))
	require.Empty(t, cfg.DefaultWorkspace)
}

// The most specific match wins, so a workspace nested inside another resolves to the inner one.
func TestCurrentPrefersTheMostSpecificMatch(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	_, err := cfg.Register("/home/user/repos", "outer", now)
	require.NoError(t, err)
	_, err = cfg.Register("/home/user/repos/dotfiles", "inner", now)
	require.NoError(t, err)

	ws, ok := cfg.Current("/home/user/repos/dotfiles/assets")
	require.True(t, ok)
	require.Equal(t, "inner", ws.Name)

	ws, ok = cfg.Current("/home/user/repos/other")
	require.True(t, ok)
	require.Equal(t, "outer", ws.Name)

	ws, ok = cfg.Current("/home/user/repos")
	require.True(t, ok)
	require.Equal(t, "outer", ws.Name, "the directory itself counts as inside")

	_, ok = cfg.Current("/etc")
	require.False(t, ok)
}

// A prefix match is not containment: /home/user/repos-old is not inside /home/user/repos.
func TestCurrentRejectsAPrefixMatch(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	_, err := cfg.Register("/home/user/repos", "repos", now)
	require.NoError(t, err)

	_, ok := cfg.Current("/home/user/repos-old")
	require.False(t, ok)
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workspaces.yaml")

	cfg := &Config{}
	_, err := cfg.Register("/home/user/dotfiles", "dots", now)
	require.NoError(t, err)
	require.NoError(t, Save(path, cfg))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(),
		"the registry lists every repository on the machine")

	back, err := Load(path)
	require.NoError(t, err)
	require.Len(t, back.Workspaces, 1)
	require.Equal(t, "dots", back.Workspaces[0].Name)
	require.Equal(t, "/home/user/dotfiles", back.Workspaces[0].Path)
	require.True(t, now.Equal(back.Workspaces[0].LastAccessed))
	require.Equal(t, "dots", back.DefaultWorkspace)
}

// The on-disk shape is the one docs/02 §6 documents and the Python implementation reads.
func TestOnDiskFormat(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workspaces.yaml")
	require.NoError(t, os.WriteFile(path, []byte(
		"workspaces:\n"+
			"  - name: dotfiles\n"+
			"    path: /home/user/dotfiles\n"+
			"    last_accessed: 2026-08-19T10:00:00Z\n"+
			"default_workspace: dotfiles\n"), 0o600))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Workspaces, 1)
	require.Equal(t, "dotfiles", cfg.Workspaces[0].Name)
	require.Equal(t, "dotfiles", cfg.DefaultWorkspace)
	require.True(t, now.Equal(cfg.Workspaces[0].LastAccessed))
}

func TestLoadMissingIsEmpty(t *testing.T) {
	t.Parallel()
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	require.NoError(t, err)
	require.Empty(t, cfg.Workspaces)
}

func TestLoadMalformed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "workspaces.yaml")
	require.NoError(t, os.WriteFile(path, []byte("workspaces: [unclosed"), 0o600))
	_, err := Load(path)
	require.Error(t, err)

	_, err = Load(t.TempDir())
	require.Error(t, err)
}

func TestSaveFailure(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	require.Error(t, Save(filepath.Join(dir, "workspaces.yaml"), &Config{}))
}

func TestRegisterRelativePathBecomesAbsolute(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	ws, err := cfg.Register("relative/dir", "", now)
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(ws.Path))
}
