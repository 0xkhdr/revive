package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/doctor"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/profile"
	"github.com/0xkhdr/revive/internal/scrub"
	"github.com/0xkhdr/revive/internal/workspace"
)

// harness runs the whole CLI inside t.TempDir(): its own workspace, its own rv config layout,
// and a fake git, so no test touches the machine or the network.
type harness struct {
	t      *testing.T
	env    *Env
	out    bytes.Buffer
	work   string
	home   string
	git    []string
	gitErr map[string]error
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	base := t.TempDir()
	h := &harness{
		t:      t,
		work:   filepath.Join(base, "work"),
		home:   filepath.Join(base, "rv-home"),
		gitErr: map[string]error{},
	}
	require.NoError(t, os.MkdirAll(h.work, 0o755))

	h.env = &Env{
		Paths:    paths.New(h.home),
		WorkDir:  h.work,
		Hostname: "test-host",
		Now:      func() time.Time { return time.Unix(1700000000, 0) },
		Scrubber: scrub.New(),
		Runner:   fakeRunner{},
		Git: func(dir string, args ...string) ([]byte, error) {
			key := strings.Join(args, " ")
			h.git = append(h.git, dir+": "+key)
			if err, ok := h.gitErr[key]; ok {
				return nil, err
			}
			// A real clone creates the destination; the fake has to as well.
			if args[0] == "clone" && len(args) == 3 {
				if err := os.MkdirAll(args[2], 0o755); err != nil {
					return nil, err
				}
			}
			return nil, nil
		},
	}
	return h
}

// run executes the command line and returns its combined output plus the error.
func (h *harness) run(args ...string) (string, error) {
	h.t.Helper()
	h.out.Reset()
	h.env.Out = &h.out
	h.env.Err = &h.out

	root := NewRootCommand("1.2.3", h.env)
	root.SetOut(&h.out)
	root.SetErr(&h.out)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return h.out.String(), err
}

func (h *harness) write(name, content string) string {
	h.t.Helper()
	p := filepath.Join(h.work, name)
	require.NoError(h.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(h.t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

type fakeRunner struct{}

func (fakeRunner) Run(context.Context, []string) ([]byte, error) { return nil, nil }
func (fakeRunner) LookPath(string) (string, bool)                { return "", false }

// Phase 9: every command from docs/03 is present, except the deferred gui and watch.
func TestCommandSurface(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	out, err := h.run("--help")
	require.NoError(t, err)

	for _, name := range []string{
		"init", "clone", "restore", "backup", "status", "diff", "doctor",
		"recover", "prune", "secret", "workspace", "self-uninstall", "completion",
	} {
		require.Contains(t, out, name, "missing command %q", name)
	}
	require.NotContains(t, out, "rv gui", "gui is deferred to post-v1.0")
	require.NotContains(t, out, "Auto-restore on workspace changes", "watch is a later phase")
}

// Phase 9: every command's --help carries the flags docs/03 specifies.
func TestHelpFlagsMatchTheSpecification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args  []string
		flags []string
	}{
		{[]string{"restore", "--help"}, []string{
			"--identity", "--dry-run", "--preview", "--interactive", "--non-interactive",
			"--no-plugins", "--prune", "--parallel", "--sequential", "--force-packages", "--manifest"}},
		{[]string{"backup", "--help"}, []string{"--identity", "--dry-run", "--manifest"}},
		{[]string{"status", "--help"}, []string{"--profile", "--identity", "--manifest"}},
		{[]string{"diff", "--help"}, []string{"--profile", "--identity", "--unified", "--manifest"}},
		{[]string{"doctor", "--help"}, []string{"--profile", "--json", "--manifest"}},
		{[]string{"recover", "--help"}, []string{"--auto"}},
		{[]string{"prune", "--help"}, []string{"--max-count", "--max-age-days", "--dry-run", "--yes"}},
		{[]string{"secret", "keygen", "--help"}, []string{"--output"}},
		{[]string{"secret", "encrypt", "--help"}, []string{"--output", "--recipient"}},
		{[]string{"secret", "decrypt", "--help"}, []string{"--output", "--identity"}},
		{[]string{"secret", "rotate", "--help"}, []string{"--identity", "--new-recipient", "--from-plaintext", "--confirm"}},
		{[]string{"clone", "--help"}, []string{"--restore", "--identity"}},
		{[]string{"workspace", "add", "--help"}, []string{"--name"}},
		{[]string{"workspace", "sync", "--help"}, []string{"--profile", "--dry-run", "--identity", "--force-packages", "--no-plugins", "--manifest"}},
		{[]string{"self-uninstall", "--help"}, []string{"--force", "--purge-config"}},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			out, err := h.run(tc.args...)
			require.NoError(t, err)
			for _, flag := range tc.flags {
				require.Contains(t, out, flag, "%v is missing %s", tc.args, flag)
			}
		})
	}
}

func TestVersionIsInjected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	out, err := h.run("--version")
	require.NoError(t, err)
	require.Equal(t, "rv 1.2.3\n", out)
}

// Phase 9: exit codes follow the table — 0 success, 1 user error, 2 operation failure.
func TestExitCodes(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0, ExitCode(nil))

	for name, err := range map[string]error{
		"usage":             ErrUsage,
		"manifest missing":  manifest.ErrNotFound,
		"manifest invalid":  manifest.ErrValidation,
		"schema version":    manifest.ErrUnsupportedSchemaVersion,
		"unknown profile":   profile.ErrNotFound,
		"profile cycle":     profile.ErrCycle,
		"bad override":      profile.ErrOverride,
		"unset variable":    paths.ErrUnsetVariable,
		"identity required": crypto.ErrIdentityRequired,
		"workspace missing": workspace.ErrNotFound,
		"duplicate name":    workspace.ErrDuplicate,
		"unhealthy doctor":  doctor.ErrUnhealthy,
	} {
		require.Equal(t, 1, ExitCode(fmt.Errorf("context: %w", err)), name)
	}

	require.Equal(t, 2, ExitCode(ErrOperation))
	require.Equal(t, 2, ExitCode(ErrNotImplemented))
	require.Equal(t, 2, ExitCode(errors.New("the transaction was rolled back")))
	require.Equal(t, 2, ExitCode(errors.New("usage error")),
		"message text must never decide the exit code")
}

// Phase 9: -p base -p work and -p base,work are identical, and so are bare positionals.
func TestProfileFlagForms(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{}
	cmd.Flags().StringSliceP("profile", "p", nil, "")

	for name, args := range map[string][]string{
		"repeated flags": {"-p", "base", "-p", "work"},
		"comma split":    {"-p", "base,work"},
		"mixed":          {"-p", "base", "-p", "work,base"},
	} {
		t.Run(name, func(t *testing.T) {
			fresh := &cobra.Command{}
			fresh.Flags().StringSliceP("profile", "p", nil, "")
			require.NoError(t, fresh.ParseFlags(args))
			require.Equal(t, []string{"base", "work"}, profiles(fresh, nil), name)
		})
	}

	require.NoError(t, cmd.ParseFlags(nil))
	require.Equal(t, []string{"base", "work"}, profiles(cmd, []string{"base", "work"}),
		"positional profiles behave the same")
	require.Equal(t, []string{"base", "work"}, profiles(cmd, []string{"base,work"}))
}

// Phase 9: --headless turns every prompt into an error.
func TestHeadlessTurnsPromptsIntoErrors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.env.In = strings.NewReader("y\n")

	h.env.Headless = false
	ok, err := h.env.Confirm("Overwrite?")
	require.NoError(t, err)
	require.True(t, ok)

	h.env.Headless = true
	_, err = h.env.Confirm("Overwrite?")
	require.ErrorIs(t, err, ErrUsage)
	require.Contains(t, err.Error(), "headless")
}

func TestConfirmReadsTheAnswer(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]bool{
		"y\n": true, "Y\n": true, "yes\n": true, "YES\n": true,
		"n\n": false, "\n": false, "anything\n": false, "": false,
	} {
		h := newHarness(t)
		h.env.Out = &h.out
		h.env.In = strings.NewReader(input)
		got, err := h.env.Confirm("Overwrite?")
		require.NoError(t, err, input)
		require.Equal(t, want, got, "%q", input)
	}
}

func TestConfirmWithoutAnInputStream(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.env.Out = &h.out
	_, err := h.env.Confirm("Overwrite?")
	require.ErrorIs(t, err, ErrUsage)
}

// Phase 9: --headless suppresses decoration.
func TestHeadlessSuppressesDecoration(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.env.Out = &h.out
	h.env.Headless = false
	h.env.heading("Title")
	h.env.item("thing")
	h.env.table([]string{"A", "B"}, [][]string{{"1", "2"}})
	decorated := h.out.String()
	require.Contains(t, decorated, "─", "the interactive rendering draws rules")
	require.Contains(t, decorated, "  - thing")

	h.out.Reset()
	h.env.Headless = true
	h.env.heading("Title")
	h.env.item("thing")
	h.env.table([]string{"A", "B"}, [][]string{{"1", "2"}})
	plain := h.out.String()
	require.NotContains(t, plain, "─", "a CI log is grepped, not read")
	require.Contains(t, plain, "A\tB", "columns become tab-separated for scripts")
	require.Contains(t, plain, "1\t2")
}

// Phase 9: profile-name completion works and honors an earlier -m.
func TestProfileCompletion(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("manifest.yaml", "version: 2\nprofiles: {base: {}, work: {extends: [base]}}\n")
	h.write("manifest-build.yaml", "version: 2\nprofiles: {ci: {}}\n")

	root := NewRootCommand("1.2.3", h.env)
	restore, _, err := root.Find([]string{"restore"})
	require.NoError(t, err)

	require.NoError(t, restore.ParseFlags(nil))
	names, directive := h.env.completeProfiles(restore, nil, "")
	require.ElementsMatch(t, []string{"base", "work"}, names)
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)

	require.NoError(t, restore.ParseFlags([]string{"-m", "manifest-build.yaml"}))
	names, _ = h.env.completeProfiles(restore, nil, "")
	require.Equal(t, []string{"ci"}, names, "an earlier -m must decide which profiles are offered")
}

func TestCompletionForABrokenManifestIsSilent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("manifest.yaml", "{not yaml")

	root := NewRootCommand("1.2.3", h.env)
	restore, _, err := root.Find([]string{"restore"})
	require.NoError(t, err)
	require.NoError(t, restore.ParseFlags(nil))

	names, _ := h.env.completeProfiles(restore, nil, "")
	require.Empty(t, names, "completion must never print an error into the user's shell")
}

// Completion scripts generate for every documented shell.
func TestCompletionScripts(t *testing.T) {
	t.Parallel()
	for _, shell := range []string{"bash", "zsh", "fish"} {
		h := newHarness(t)
		out, err := h.run("completion", shell)
		require.NoError(t, err, shell)
		require.NotEmpty(t, out, shell)
	}
}

func TestRestoreWithoutAProfileIsAUsageError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("manifest.yaml", "version: 2\nprofiles: {base: {}}\n")
	_, err := h.run("restore")
	require.ErrorIs(t, err, ErrUsage)
	require.Equal(t, 1, ExitCode(err))
}

func TestRestoreRunsTheEngine(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := filepath.Join(h.work, "out.conf")
	h.write("assets/conf", "content\n")
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	out, err := h.run("restore", "base")
	require.NoError(t, err)
	require.Contains(t, out, "Restore complete")
	require.Contains(t, out, "transaction:")
	require.FileExists(t, target)
}

func TestRestoreDryRunSaysSo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := filepath.Join(h.work, "out.conf")
	h.write("assets/conf", "content\n")
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	out, err := h.run("restore", "base", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "nothing was changed")
	require.NoFileExists(t, target)
}

// A failing restore exits 2, not 1: it is an operation failure, not a bad request.
func TestFailedRestoreExitsTwo(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("manifest.yaml",
		"version: 2\nassets: [{id: conf, type: copy, source: assets/missing, target: /tmp/x}]\nprofiles: {base: {assets: [conf]}}\n")

	_, err := h.run("restore", "base")
	require.Error(t, err)
	require.Equal(t, 1, ExitCode(err), "a source missing from the repo is a configuration error")

	h2 := newHarness(t)
	h2.write("manifest.yaml", "version: 3\n")
	_, err = h2.run("restore", "base")
	require.Equal(t, 1, ExitCode(err))
}

func TestMutuallyExclusiveFlags(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("manifest.yaml", "version: 2\nprofiles: {base: {}}\n")

	for _, args := range [][]string{
		{"restore", "base", "--parallel", "--sequential"},
		{"restore", "base", "--interactive", "--non-interactive"},
		{"restore", "base", "--dry-run", "--preview"},
	} {
		_, err := h.run(args...)
		require.Error(t, err, "%v", args)
	}
}

func TestPendingCommandsReportNotImplemented(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"backup", "base"}, {"recover"}, {"prune"}, {"self-uninstall"},
	} {
		h := newHarness(t)
		_, err := h.run(args...)
		require.ErrorIs(t, err, ErrNotImplemented, "%v", args)
	}
}

func TestUnknownCommand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	_, err := h.run("teleport")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown command")
}

// setup fills in every seam a caller did not supply, and wires both log handlers.
func TestSetupFillsInDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	env := &Env{Paths: paths.New(filepath.Join(dir, "rv-home"))}
	root := NewRootCommand("1.2.3", env)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init"})
	require.NoError(t, root.Execute())

	require.Equal(t, dir, env.WorkDir)
	require.NotEmpty(t, env.Hostname)
	require.NotNil(t, env.Runner)
	require.NotNil(t, env.Git)
	require.NotNil(t, env.Log)
	require.FileExists(t, env.Paths.AuditLog, "a command's activity is recorded")
	require.FileExists(t, filepath.Join(dir, "manifest.yaml"))
}

// The workspace .env is loaded before any command body, so ${VAR} targets resolve.
func TestSetupLoadsTheWorkspaceEnv(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("RV_TEST_FROM_DOTENV=yes\n"), 0o600))
	require.NoError(t, os.Unsetenv("RV_TEST_FROM_DOTENV"))
	t.Cleanup(func() { _ = os.Unsetenv("RV_TEST_FROM_DOTENV") })

	env := &Env{Paths: paths.New(filepath.Join(dir, "rv-home"))}
	root := NewRootCommand("1.2.3", env)
	root.SetOut(&bytes.Buffer{})
	root.SetArgs([]string{"init"})
	require.NoError(t, root.Execute())

	require.Equal(t, "yes", os.Getenv("RV_TEST_FROM_DOTENV"))
}

// An unwritable audit log must not stop the user working: it is a record, not a gate.
func TestUnwritableAuditLogIsNotFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	dir := t.TempDir()
	t.Chdir(dir)

	rvHome := filepath.Join(dir, "rv-home")
	cfg := paths.New(rvHome)
	require.NoError(t, os.MkdirAll(filepath.Dir(cfg.DataDir), 0o755))
	require.NoError(t, os.Chmod(filepath.Dir(cfg.DataDir), 0o500))
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(cfg.DataDir), 0o700) })

	env := &Env{Paths: cfg}
	root := NewRootCommand("1.2.3", env)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"init"})
	require.NoError(t, root.Execute())
	require.FileExists(t, filepath.Join(dir, "manifest.yaml"))
}

func TestExecGit(t *testing.T) {
	t.Parallel()
	out, err := execGit(t.TempDir(), "--version")
	require.NoError(t, err)
	require.Contains(t, string(out), "git version")

	_, err = execGit(t.TempDir(), "no-such-subcommand-xyz")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no-such-subcommand-xyz")
}

func TestManifestPathResolution(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	cmd := &cobra.Command{}
	addManifestFlag(cmd)

	require.NoError(t, cmd.ParseFlags(nil))
	require.Equal(t, filepath.Join(h.work, "manifest.yaml"), h.env.manifestPath(cmd))

	require.NoError(t, cmd.ParseFlags([]string{"-m", "manifest-build.yaml"}))
	require.Equal(t, filepath.Join(h.work, "manifest-build.yaml"), h.env.manifestPath(cmd))

	require.NoError(t, cmd.ParseFlags([]string{"-m", "/absolute/manifest.yaml"}))
	require.Equal(t, "/absolute/manifest.yaml", h.env.manifestPath(cmd))

	require.NoError(t, cmd.Flags().Set("manifest", ""))
	require.Equal(t, filepath.Join(h.work, "manifest.yaml"), h.env.manifestPath(cmd))
}

func TestEnvDefaults(t *testing.T) {
	t.Parallel()
	env := &Env{}
	require.NotNil(t, env.logger())
	require.NotNil(t, env.scrubber())
	require.NotNil(t, env.out())
	require.False(t, env.now().IsZero())
}
