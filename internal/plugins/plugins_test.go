package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fixture struct {
	t          *testing.T
	workspace  string
	userGlobal string
	loader     *Loader
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		t:          t,
		workspace:  filepath.Join(base, "workspace", "plugins"),
		userGlobal: filepath.Join(base, "config", "plugins"),
	}
	require.NoError(t, os.MkdirAll(f.workspace, 0o755))
	require.NoError(t, os.MkdirAll(f.userGlobal, 0o755))
	f.loader = &Loader{Dirs: []string{f.workspace, f.userGlobal}}
	return f
}

// plugin writes a plugin directory. script is the entrypoint's shell body.
func (f *fixture) plugin(dir, name, manifest, script string) string {
	f.t.Helper()
	pluginDir := filepath.Join(dir, name)
	require.NoError(f.t, os.MkdirAll(pluginDir, 0o755))
	require.NoError(f.t, os.WriteFile(filepath.Join(pluginDir, manifestName), []byte(manifest), 0o644))
	if script != "" {
		require.NoError(f.t, os.WriteFile(filepath.Join(pluginDir, "run.sh"),
			[]byte("#!/bin/sh\n"+script), 0o755))
	}
	return pluginDir
}

func names(ps []Plugin) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name()
	}
	return out
}

// Phase 12: plugins are discovered in precedence order, first name wins, sorted by name within
// a directory.
func TestDiscoveryPrecedenceAndOrder(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// Same name in both directories: the workspace-local one wins.
	f.plugin(f.workspace, "shared", "name: shared\nversion: \"2.0.0\"\nentrypoint: run.sh\n", "exit 0")
	f.plugin(f.userGlobal, "shared", "name: shared\nversion: \"1.0.0\"\nentrypoint: run.sh\n", "exit 0")
	// Deliberately created out of alphabetical order.
	f.plugin(f.workspace, "zeta", "name: zeta\nversion: \"1.0.0\"\nentrypoint: run.sh\n", "exit 0")
	f.plugin(f.workspace, "alpha", "name: alpha\nversion: \"1.0.0\"\nentrypoint: run.sh\n", "exit 0")
	f.plugin(f.userGlobal, "global_only", "name: global_only\nversion: \"1.0.0\"\nentrypoint: run.sh\n", "exit 0")

	got := f.loader.Discover()
	require.Equal(t, []string{"alpha", "shared", "zeta", "global_only"}, names(got),
		"sorted within each directory, workspace directory first")

	for _, p := range got {
		if p.Name() == "shared" {
			require.Equal(t, "2.0.0", p.Manifest.Version, "the workspace-local definition wins")
			require.Equal(t, f.workspace, p.Source)
		}
	}
}

// Discovery must be stable across repeated runs.
func TestDiscoveryIsReproducible(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, name := range []string{"c", "a", "b", "d"} {
		f.plugin(f.workspace, name, "name: "+name+"\nversion: \"1.0.0\"\nentrypoint: run.sh\n", "exit 0")
	}

	first := names(f.loader.Discover())
	for range 10 {
		require.Equal(t, first, names(f.loader.Discover()))
	}
	require.Equal(t, []string{"a", "b", "c", "d"}, first)
}

// Phase 12: a malformed plugin.yaml is logged and skipped without affecting other plugins.
func TestMalformedManifestIsSkipped(t *testing.T) {
	t.Parallel()
	for name, manifest := range map[string]string{
		"bad yaml":      "name: [unclosed",
		"unknown field": "name: x\nversion: \"1\"\nentrypoint: run.sh\ntypo_field: true\n",
		"no name":       "version: \"1.0.0\"\nentrypoint: run.sh\n",
		"no version":    "name: broken\nentrypoint: run.sh\n",
		"no entrypoint": "name: broken\nversion: \"1.0.0\"\n",
		"unknown hook":  "name: broken\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [mid-restore]\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			f.plugin(f.workspace, "broken", manifest, "exit 0")
			f.plugin(f.workspace, "healthy", "name: healthy\nversion: \"1.0.0\"\nentrypoint: run.sh\n", "exit 0")

			require.Equal(t, []string{"healthy"}, names(f.loader.Discover()),
				"one broken plugin must not prevent the others from loading")
		})
	}
}

func TestMissingEntrypointIsSkipped(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.plugin(f.workspace, "broken", "name: broken\nversion: \"1.0.0\"\nentrypoint: absent.sh\n", "exit 0")
	require.Empty(t, f.loader.Discover())
}

// A manifest must not be able to point rv at an executable outside its own directory.
func TestEntrypointCannotEscapeThePluginDirectory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.plugin(f.workspace, "escaper", "name: escaper\nversion: \"1.0.0\"\nentrypoint: ../../../bin/sh\n", "exit 0")
	require.Empty(t, f.loader.Discover())
}

func TestNonDirectoryEntriesAreIgnored(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(f.workspace, "README.md"), []byte("x"), 0o644))
	f.plugin(f.workspace, "real", "name: real\nversion: \"1.0.0\"\nentrypoint: run.sh\n", "exit 0")

	require.Equal(t, []string{"real"}, names(f.loader.Discover()))
}

func TestMissingSearchDirectoryIsFine(t *testing.T) {
	t.Parallel()
	l := &Loader{Dirs: []string{filepath.Join(t.TempDir(), "absent")}}
	require.Empty(t, l.Discover(), "most workspaces have no plugins at all")
}

func TestForFiltersByStage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.plugin(f.workspace, "pre", "name: pre\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [pre-restore]\n", "exit 0")
	f.plugin(f.workspace, "post", "name: post\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n", "exit 0")
	f.plugin(f.workspace, "both", "name: both\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [pre-restore, post-restore]\n", "exit 0")
	f.plugin(f.workspace, "none", "name: none\nversion: \"1.0.0\"\nentrypoint: run.sh\n", "exit 0")

	require.Equal(t, []string{"both", "pre"}, names(f.loader.For(PreRestore)))
	require.Equal(t, []string{"both", "post"}, names(f.loader.For(PostRestore)))
}

// Phase 12: a plugin receives the context and returns JSON on stdout.
func TestPluginReceivesContextAndReturnsJSON(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dump := filepath.Join(t.TempDir(), "context.json")
	f.plugin(f.workspace, "echo", "name: echo\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n",
		"cat > "+dump+"\necho '{\"status\":\"success\",\"message\":\"reloaded 3 services\"}'\n")

	runner := &Runner{Loader: f.loader}
	results, err := runner.Run(context.Background(), Context{
		RepoDir:     "/home/user/dotfiles",
		ProfileName: "base,work",
		Targets:     []string{"/home/user/.zshrc", "/home/user/.gitconfig"},
		HookType:    PostRestore,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "success", results[0].Status)
	require.Equal(t, "reloaded 3 services", results[0].Message)
	require.Equal(t, "echo", results[0].Plugin)

	raw, err := os.ReadFile(dump)
	require.NoError(t, err)
	var got Context
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, "/home/user/dotfiles", got.RepoDir)
	require.Equal(t, "base,work", got.ProfileName)
	require.Equal(t, []string{"/home/user/.zshrc", "/home/user/.gitconfig"}, got.Targets)
	require.Equal(t, PostRestore, got.HookType)
	require.False(t, got.DryRun, "v1.0 never invokes a plugin on a dry run")
}

// Exit 0 with unparseable stdout is still a success: the plugin did its job even if it printed
// prose.
func TestUnparseableStdoutIsStillSuccess(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.plugin(f.workspace, "chatty", "name: chatty\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n",
		"echo 'just some prose'\n")

	results, err := (&Runner{Loader: f.loader}).Run(context.Background(), Context{HookType: PostRestore})
	require.NoError(t, err)
	require.Equal(t, "success", results[0].Status)
	require.Equal(t, "just some prose", results[0].Stdout)
}

func TestSilentPluginIsSuccess(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.plugin(f.workspace, "quiet", "name: quiet\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n", "exit 0")

	results, err := (&Runner{Loader: f.loader}).Run(context.Background(), Context{HookType: PostRestore})
	require.NoError(t, err)
	require.Equal(t, "success", results[0].Status)
}

// Phase 12: a non-zero exit fails the restore.
func TestNonZeroExitFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.plugin(f.workspace, "failing", "name: failing\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n",
		"echo 'something went wrong' >&2\nexit 3\n")

	_, err := (&Runner{Loader: f.loader}).Run(context.Background(), Context{HookType: PostRestore})
	require.ErrorIs(t, err, ErrPlugin)
	require.Contains(t, err.Error(), "failing")
	require.Contains(t, err.Error(), "something went wrong", "the plugin's own diagnostics are the useful part")
}

// Phase 12: a plugin exceeding its timeout is killed and fails the restore.
func TestTimeoutKillsThePlugin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.plugin(f.workspace, "slow", "name: slow\nversion: \"1.0.0\"\nentrypoint: run.sh\ntimeout: 1\nhooks: [post-restore]\n",
		"sleep 30\n")

	start := time.Now()
	_, err := (&Runner{Loader: f.loader}).Run(context.Background(), Context{HookType: PostRestore})
	require.ErrorIs(t, err, ErrTimeout)
	require.Contains(t, err.Error(), "slow")
	require.Less(t, time.Since(start), 10*time.Second, "the process must actually be killed")
}

// Phase 12: the timeout is clamped to [1, 300].
func TestTimeoutClamping(t *testing.T) {
	t.Parallel()
	for declared, want := range map[int]time.Duration{
		0:    DefaultTimeout,
		-5:   DefaultTimeout,
		1:    1 * time.Second,
		30:   30 * time.Second,
		300:  300 * time.Second,
		9999: MaxTimeout,
	} {
		p := Plugin{Manifest: Manifest{Timeout: declared}}
		require.Equal(t, want, p.Timeout(), "declared %d", declared)
	}
}

// Phase 12: with network: false, proxy environment variables are set.
//
// This stops a well-behaved HTTP client. It is not network isolation, and the documentation
// says so.
func TestNetworkPermissionSetsProxyVariables(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dump := filepath.Join(t.TempDir(), "env.txt")
	f.plugin(f.workspace, "env", "name: env\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n"+
		"permissions:\n  network: false\n  shell: true\n  allowed_paths: [/tmp, /var/tmp]\n",
		"env > "+dump+"\n")

	_, err := (&Runner{Loader: f.loader}).Run(context.Background(), Context{HookType: PostRestore})
	require.NoError(t, err)

	raw, err := os.ReadFile(dump)
	require.NoError(t, err)
	env := string(raw)
	require.Contains(t, env, "http_proxy=http://127.0.0.1:0")
	require.Contains(t, env, "HTTPS_PROXY=http://127.0.0.1:0")
	require.Contains(t, env, "no_proxy=*")
	require.Contains(t, env, "RV_PLUGIN_SHELL=1")
	require.Contains(t, env, "RV_PLUGIN_NETWORK=0")
	require.Contains(t, env, "RV_PLUGIN_ALLOWED_PATHS=/tmp:/var/tmp")
}

func TestNetworkAllowedLeavesProxiesAlone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dump := filepath.Join(t.TempDir(), "env.txt")
	f.plugin(f.workspace, "net", "name: net\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n"+
		"permissions:\n  network: true\n", "env > "+dump+"\n")

	_, err := (&Runner{Loader: f.loader}).Run(context.Background(), Context{HookType: PostRestore})
	require.NoError(t, err)

	raw, err := os.ReadFile(dump)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "127.0.0.1:0")
	require.Contains(t, string(raw), "RV_PLUGIN_NETWORK=1")
}

// Plugins run in discovery order, and the first failure stops the rest.
func TestRunOrderAndShortCircuit(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	log := filepath.Join(t.TempDir(), "order.txt")
	f.plugin(f.workspace, "a_first", "name: a_first\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n",
		"echo a >> "+log+"\n")
	f.plugin(f.workspace, "b_fails", "name: b_fails\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n",
		"echo b >> "+log+"\nexit 1\n")
	f.plugin(f.workspace, "c_never", "name: c_never\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n",
		"echo c >> "+log+"\n")

	_, err := (&Runner{Loader: f.loader}).Run(context.Background(), Context{HookType: PostRestore})
	require.ErrorIs(t, err, ErrPlugin)

	raw, err := os.ReadFile(log)
	require.NoError(t, err)
	require.Equal(t, "a\nb\n", string(raw), "a plugin after a failure must not run")
}

func TestRunWithNoPlugins(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	results, err := (&Runner{Loader: f.loader}).Run(context.Background(), Context{HookType: PostRestore})
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestPluginRunsInItsOwnDirectory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dump := filepath.Join(t.TempDir(), "pwd.txt")
	dir := f.plugin(f.workspace, "cwd", "name: cwd\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n",
		"pwd > "+dump+"\n")

	_, err := (&Runner{Loader: f.loader}).Run(context.Background(), Context{HookType: PostRestore})
	require.NoError(t, err)

	raw, err := os.ReadFile(dump)
	require.NoError(t, err)
	require.Equal(t, dir, strings.TrimSpace(string(raw)),
		"a plugin's relative paths resolve against its own directory")
}

func TestCancellationReachesThePlugin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.plugin(f.workspace, "slow", "name: slow\nversion: \"1.0.0\"\nentrypoint: run.sh\nhooks: [post-restore]\n",
		"sleep 30\n")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := (&Runner{Loader: f.loader}).Run(ctx, Context{HookType: PostRestore})
	require.Error(t, err, "Ctrl-C must reach a running plugin")
}

func TestManifestValidate(t *testing.T) {
	t.Parallel()
	valid := Manifest{Name: "x", Version: "1.0.0", Entrypoint: "run.sh", Hooks: []Stage{PreRestore}}
	require.NoError(t, valid.Validate())

	require.Error(t, (&Manifest{}).Validate())
	require.Error(t, (&Manifest{Name: "x"}).Validate())
	require.Error(t, (&Manifest{Name: "x", Version: "1"}).Validate())
	require.Error(t, (&Manifest{Name: "x", Version: "1", Entrypoint: "r", Hooks: []Stage{"nope"}}).Validate())
}

func TestSubscribes(t *testing.T) {
	t.Parallel()
	p := Plugin{Manifest: Manifest{Hooks: []Stage{PostRestore}}}
	require.True(t, p.Subscribes(PostRestore))
	require.False(t, p.Subscribes(PreRestore))
	require.False(t, Plugin{}.Subscribes(PostRestore))
}

func TestTruncate(t *testing.T) {
	t.Parallel()
	require.Equal(t, "short", truncate("  short  "))
	long := strings.Repeat("x", stderrLimit+100)
	require.Len(t, truncate(long), stderrLimit+len("…"))
}
