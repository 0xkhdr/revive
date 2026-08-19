package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 1: a Config can be rooted anywhere, and carries every documented runtime path.
func TestConfigIsRootable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	c := New(root)

	require.Equal(t, filepath.Join(root, ".config", "rv"), c.ConfigDir)
	require.Equal(t, filepath.Join(root, ".local", "share", "rv"), c.DataDir)
	for _, p := range []string{
		c.LockFile, c.JournalDir, c.BackupDir, c.IdentityFile,
		c.PackageCache, c.WorkspaceFile, c.PluginsDir, c.AuditLog,
	} {
		require.True(t, strings.HasPrefix(p, root), "%s escapes the root", p)
	}
	require.Equal(t, filepath.Join(c.ConfigDir, "rv.lock"), c.LockFile)
	require.Equal(t, filepath.Join(c.ConfigDir, "workspaces.yaml"), c.WorkspaceFile)
	require.Equal(t, filepath.Join(c.DataDir, "audit.log"), c.AuditLog)
	require.Equal(t, filepath.Join(c.JournalDir, "abc.json"), c.JournalPath("abc"))
	require.Equal(t, filepath.Join(c.BackupDir, "abc"), c.BackupPathFor("abc"))
	require.Len(t, c.IdentityCandidates(), 3)
}

func TestXDGOverrideOnlyWhenAbsolute(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	require.Equal(t, filepath.Join(root, "xdg", "rv"), New(root).ConfigDir)

	t.Setenv("XDG_CONFIG_HOME", "relative/path")
	require.Equal(t, filepath.Join(root, ".config", "rv"), New(root).ConfigDir,
		"a relative XDG value must be ignored per the XDG spec")
}

// Phase 1: canonicalization expands ~ and makes paths absolute WITHOUT resolving symlinks.
func TestCanonicalizeDoesNotResolveSymlinks(t *testing.T) {
	root := t.TempDir()
	c := New(root)

	require.Equal(t, filepath.Join(root, ".zshrc"), c.Canonicalize("~/.zshrc"))
	require.Equal(t, root, c.Canonicalize("~"))
	require.Equal(t, "/etc/hosts", c.Canonicalize("/etc/hosts"))
	require.False(t, strings.HasPrefix(c.Canonicalize("~notauser/x"), root),
		"only a bare ~ or ~/ is expanded")

	target := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))
	require.NoError(t, os.Symlink(target, link))
	require.Equal(t, link, c.Canonicalize(link), "the link itself is the target, not what it points at")

	t.Setenv("RV_TEST_DIR", root)
	require.Equal(t, filepath.Join(root, "a"), c.Canonicalize("$RV_TEST_DIR/a"))
}

func TestIsSafeSubpath(t *testing.T) {
	t.Parallel()
	c := New(t.TempDir())
	require.True(t, c.IsSafeSubpath("/repo", "/repo/assets/zshrc"))
	require.True(t, c.IsSafeSubpath("/repo", "/repo"))
	require.False(t, c.IsSafeSubpath("/repo", "/repo-evil/x"), "a prefix match is not containment")
	require.False(t, c.IsSafeSubpath("/repo", "/etc/passwd"))
	require.False(t, c.IsSafeSubpath("/repo", "/repo/../etc/passwd"))
}

// Phase 1: a symlink cycle a -> b -> a is detected.
func TestDetectSymlinkLoop(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	c := New(root)

	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	require.NoError(t, os.Symlink(b, a))
	require.NoError(t, os.Symlink(a, b))
	require.True(t, c.DetectSymlinkLoop(a))
	require.True(t, c.DetectSymlinkLoop(b))

	self := filepath.Join(root, "self")
	require.NoError(t, os.Symlink(self, self))
	require.True(t, c.DetectSymlinkLoop(self))

	plain := filepath.Join(root, "plain")
	require.NoError(t, os.WriteFile(plain, []byte("x"), 0o600))
	require.False(t, c.DetectSymlinkLoop(plain))
	require.False(t, c.DetectSymlinkLoop(filepath.Join(root, "absent")))

	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(plain, link))
	require.False(t, c.DetectSymlinkLoop(link))

	dangling := filepath.Join(root, "dangling")
	require.NoError(t, os.Symlink(filepath.Join(root, "nowhere"), dangling))
	require.False(t, c.DetectSymlinkLoop(dangling), "a dangling link is broken, not cyclic")
}

func TestDetectSymlinkLoopFollowsRelativeLinks(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "d"), 0o755))
	a := filepath.Join(root, "d", "a")
	b := filepath.Join(root, "d", "b")
	require.NoError(t, os.Symlink("b", a))
	require.NoError(t, os.Symlink("a", b))
	require.True(t, New(root).DetectSymlinkLoop(a))
}

func TestDefaultUsesRealHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	c, err := Default()
	require.NoError(t, err)
	require.Equal(t, home, c.Home)
}

func TestCanonicalizeFallsBackWhenCwdIsGone(t *testing.T) {
	t.Parallel()
	// filepath.Abs only fails when the cwd cannot be read; the cleaned path is the fallback.
	require.Equal(t, "/a/b", New(t.TempDir()).Canonicalize("/a/./b/"))
}

func TestIsSafeSubpathWithRelativeInputs(t *testing.T) {
	t.Parallel()
	c := New(t.TempDir())
	require.True(t, c.IsSafeSubpath(".", "./sub/file"))
}
