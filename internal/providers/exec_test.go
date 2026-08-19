package providers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ExecRunner is the only place providers touch the real system, so it gets its own coverage.
func TestExecRunner(t *testing.T) {
	t.Parallel()
	var r ExecRunner

	out, err := r.Run(context.Background(), []string{"echo", "hello"})
	require.NoError(t, err)
	require.Equal(t, "hello\n", string(out))

	_, err = r.Run(context.Background(), []string{"sh", "-c", "echo oops >&2; exit 3"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "oops", "the command's own output is the useful part")

	_, err = r.Run(context.Background(), nil)
	require.ErrorIs(t, err, ErrProvider)

	_, ok := r.LookPath("sh")
	require.True(t, ok)
	_, ok = r.LookPath("rv-no-such-binary-xyz")
	require.False(t, ok)
}

func TestExecRunnerIsCancellable(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := ExecRunner{}.Run(ctx, []string{"sleep", "30"})
	require.Error(t, err, "Ctrl-C must reach a package manager mid-install")
}

// The Deps defaults exist so a caller can supply only a Runner.
func TestDepsDefaults(t *testing.T) {
	t.Parallel()
	d := Deps{Runner: newFakeRunner()}
	require.NotNil(t, d.log())
	start := time.Now()
	d.sleep(time.Millisecond)
	require.GreaterOrEqual(t, time.Since(start), time.Millisecond)
}

func TestNodeInstallTakesTheFirstVersion(t *testing.T) {
	t.Parallel()
	r := newFakeRunner("node", "fnm")
	r.Out["node -v"] = "v16.0.0\n"
	n := newNode(t, r, t.TempDir())

	require.NoError(t, n.Install(context.Background(), []string{"20.11.0"}, InstallOptions{}))
	require.True(t, r.ran("fnm install 20.11.0"))
}

func TestCacheSaveFailsOnAnUnwritableDirectory(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	c := NewCache(filepath.Join(dir, "cache.json"), nil)
	c.MarkInstalled("brew", "fzf")
	require.Error(t, c.Save(), "a save failure is a warning to the caller, not a silent success")
}

func TestInvalidateAllReportsARemovalFailure(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	require.Error(t, NewCache(path, nil).InvalidateAll())
}
