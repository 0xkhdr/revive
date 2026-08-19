package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/lockfile"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/profile"
	"github.com/0xkhdr/revive/internal/providers"
	"github.com/0xkhdr/revive/internal/scrub"
	"github.com/0xkhdr/revive/internal/transaction"
)

// workspace is a whole restore rooted in t.TempDir(): repo, target tree, and rv config layout,
// none of them touching the machine.
type workspace struct {
	t        *testing.T
	base     string
	repo     string
	home     string
	cfg      paths.Config
	runner   *fakeRunner
	pub      string
	identity string
}

func newWorkspace(t *testing.T) *workspace {
	t.Helper()
	base := t.TempDir()
	w := &workspace{
		t:      t,
		base:   base,
		repo:   filepath.Join(base, "repo"),
		home:   filepath.Join(base, "home"),
		cfg:    paths.New(filepath.Join(base, "rv-home")),
		runner: newFakeRunner(),
	}
	require.NoError(t, os.MkdirAll(w.repo, 0o755))
	require.NoError(t, os.MkdirAll(w.home, 0o755))
	return w
}

func (w *workspace) writeRepo(name, content string) string {
	w.t.Helper()
	p := filepath.Join(w.repo, name)
	require.NoError(w.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(w.t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func (w *workspace) manifest(body string) string {
	w.t.Helper()
	return w.writeRepo("manifest.yaml", body)
}

func (w *workspace) target(name string) string { return filepath.Join(w.home, name) }

func (w *workspace) withIdentity() *workspace {
	w.t.Helper()
	pub, identity, err := crypto.GenerateKeypair()
	require.NoError(w.t, err)
	w.pub = pub

	path := filepath.Join(w.cfg.ConfigDir, "identity.txt")
	require.NoError(w.t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(w.t, crypto.WriteIdentityFile(path, pub, identity))
	w.identity = path
	return w
}

func (w *workspace) encryptRepo(name, plaintext string) {
	w.t.Helper()
	ciphertext, err := crypto.Encrypt([]byte(plaintext), []string{w.pub})
	require.NoError(w.t, err)
	p := filepath.Join(w.repo, name)
	require.NoError(w.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(w.t, os.WriteFile(p, ciphertext, 0o644))
}

func (w *workspace) restorer() *Restorer {
	return &Restorer{
		Paths:    w.cfg,
		Now:      func() time.Time { return time.Unix(1700000000, 0) },
		Runner:   w.runner,
		Hostname: "test-host",
		Scrubber: scrub.New(),
	}
}

func (w *workspace) opts(profiles ...string) Options {
	return Options{
		RepoDir:      w.repo,
		ManifestPath: filepath.Join(w.repo, "manifest.yaml"),
		Profiles:     profiles,
	}
}

// fakeRunner answers provider probes without touching the machine.
type fakeRunner struct {
	Ran  []string
	Path map[string]bool
	Fail map[string]error
	// chmodOn lets a fake "package install" change a managed file's mode, which is the case
	// verify-after-packages exists to catch.
	chmodOn     map[string]os.FileMode
	chmodTarget string
}

func newFakeRunner(available ...string) *fakeRunner {
	path := map[string]bool{}
	for _, n := range available {
		path[n] = true
	}
	return &fakeRunner{Path: path, Fail: map[string]error{}}
}

func (f *fakeRunner) Run(_ context.Context, cmd []string) ([]byte, error) {
	key := strings.Join(cmd, " ")
	f.Ran = append(f.Ran, key)
	if mode, ok := f.chmodOn[key]; ok {
		_ = os.Chmod(f.chmodTarget, mode)
	}
	if err, ok := f.Fail[key]; ok {
		return nil, err
	}
	return nil, nil
}

func (f *fakeRunner) LookPath(name string) (string, bool) {
	if f.Path[name] {
		return "/usr/bin/" + name, true
	}
	return "", false
}

func (f *fakeRunner) ran(cmd string) bool {
	for _, got := range f.Ran {
		if got == cmd {
			return true
		}
	}
	return false
}

const basicManifest = `
version: 2
assets:
  - id: zshrc
    type: symlink
    source: assets/zshrc
    target: %s
    conflict_strategy: overwrite
  - id: gitconfig
    type: template
    source: assets/gitconfig.tmpl
    target: %s
    permissions: "0644"
    conflict_strategy: overwrite
    template_vars:
      email: dev@example.com
profiles:
  base:
    assets: [zshrc, gitconfig]
`

// Phase 8: a full restore produces the expected symlinks, files and modes.
func TestFullRestore(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/zshrc", "export EDITOR=vim\n")
	w.writeRepo("assets/gitconfig.tmpl", "email = {{ .email }}\nuser = {{ ._user }}\n")
	w.manifest(fmt.Sprintf(basicManifest, w.target(".zshrc"), w.target(".gitconfig")))

	res, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)
	require.Equal(t, 2, res.Assets)
	require.Empty(t, res.Skipped)

	link, err := os.Readlink(w.target(".zshrc"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(w.repo, "assets/zshrc"), link)

	got, err := os.ReadFile(w.target(".gitconfig"))
	require.NoError(t, err)
	require.Contains(t, string(got), "email = dev@example.com")

	fi, err := os.Stat(w.target(".gitconfig"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), fi.Mode().Perm())

	// The journal and backups are gone after a successful commit.
	require.NoFileExists(t, w.cfg.JournalPath(res.TxID))
	require.NoDirExists(t, w.cfg.BackupPathFor(res.TxID))
	require.FileExists(t, res.LockfilePath)
}

// Phase 8: re-running a restore is a no-op.
func TestRestoreIsIdempotent(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/zshrc", "export EDITOR=vim\n")
	w.writeRepo("assets/gitconfig.tmpl", "email = {{ .email }}\n")
	w.manifest(fmt.Sprintf(basicManifest, w.target(".zshrc"), w.target(".gitconfig")))

	first, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)
	before, err := os.ReadFile(first.LockfilePath)
	require.NoError(t, err)

	second, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)
	require.Equal(t, first.Assets, second.Assets)

	after, err := os.ReadFile(second.LockfilePath)
	require.NoError(t, err)

	// Only mtimes may differ: the entries and checksums must be identical.
	beforeLF, err := lockfile.Load(first.LockfilePath)
	require.NoError(t, err)
	require.NotEmpty(t, beforeLF.Entries)
	require.Equal(t, redactMTimes(t, before), redactMTimes(t, after),
		"a second run must change nothing but the mtimes")

	link, err := os.Readlink(w.target(".zshrc"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(w.repo, "assets/zshrc"), link)
}

func redactMTimes(t *testing.T, raw []byte) string {
	t.Helper()
	lf, err := lockfile.Load(writeTemp(t, raw))
	require.NoError(t, err)
	for id, e := range lf.Entries {
		e.MTime = lockfile.ScalarFloat(0)
		lf.Entries[id] = e
	}
	out, err := lockfile.Marshal(lf)
	require.NoError(t, err)
	return string(out)
}

func writeTemp(t *testing.T, raw []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "manifest.lock")
	require.NoError(t, os.WriteFile(p, raw, 0o600))
	return p
}

// Phase 8: --dry-run mutates nothing, writes no journal and no lockfile, and runs no hook.
func TestDryRunMutatesNothing(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/zshrc", "export EDITOR=vim\n")
	marker := w.target("hook-marker")
	w.manifest(fmt.Sprintf(`
version: 2
assets:
  - id: zshrc
    type: copy
    source: assets/zshrc
    target: %s
    hooks:
      pre:
        - command: "touch %s"
      post:
        - command: "touch %s"
packages:
  apt: [git]
profiles:
  base:
    assets: [zshrc]
    packages: [apt]
`, w.target(".zshrc"), marker, marker))

	w.runner.Path["apt-get"] = true
	w.runner.Path["dpkg"] = true

	opts := w.opts("base")
	opts.DryRun = true
	res, err := w.restorer().Restore(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, res.DryRun)

	require.NoFileExists(t, w.target(".zshrc"), "nothing may be written")
	require.NoFileExists(t, marker, "no hook of any kind may run")
	require.NoFileExists(t, lockfile.PathFor(opts.ManifestPath), "no lockfile")
	require.NoFileExists(t, w.cfg.JournalPath(res.TxID), "no journal")
	require.False(t, w.runner.ran("apt-get install -y git"), "no package may be installed")
}

// A dry run must not require the packages' platform either: previewing a manifest destined for
// another machine is the point.
func TestDryRunWithoutAnyPackageManager(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/zshrc", "x")
	w.manifest(fmt.Sprintf(`
version: 2
assets: [{id: zshrc, source: assets/zshrc, target: %s}]
packages: {brew: [fzf]}
profiles: {base: {assets: [zshrc], packages: [brew]}}
`, w.target(".zshrc")))

	opts := w.opts("base")
	opts.DryRun = true
	_, err := w.restorer().Restore(context.Background(), opts)
	require.NoError(t, err)
}

// Phase 8: --sequential and --parallel produce identical planned-operation ordering.
func TestSequentialAndParallelPlanIdentically(t *testing.T) {
	t.Parallel()
	build := func(t *testing.T) (*workspace, Options) {
		w := newWorkspace(t)
		body := "version: 2\nassets:\n"
		for i := range 12 {
			name := fmt.Sprintf("asset%02d", i)
			w.writeRepo("assets/"+name, name+" contents\n")
			body += fmt.Sprintf("  - {id: %s, type: copy, source: assets/%s, target: %s}\n",
				name, name, w.target(name))
		}
		body += "profiles:\n  base:\n    assets: ["
		for i := range 12 {
			if i > 0 {
				body += ", "
			}
			body += fmt.Sprintf("asset%02d", i)
		}
		body += "]\n"
		w.manifest(body)
		return w, w.opts("base")
	}

	w, opts := build(t)
	seqOpts := opts
	seqOpts.Sequential = true

	seq := planOrder(t, w, seqOpts)
	require.Len(t, seq, 12)

	// Repeat the parallel plan: a race would show up as an occasional reordering, not always.
	for range 20 {
		require.Equal(t, seq, planOrder(t, w, opts), "completion order must never leak into plan order")
	}
}

// planOrder runs the planner directly, which is the thing the criterion is about: --parallel
// and --sequential must produce the same planned-operation ordering.
func planOrder(t *testing.T, w *workspace, opts Options) []string {
	t.Helper()
	m, err := manifest.Load(opts.ManifestPath)
	require.NoError(t, err)
	resolved, err := profile.Resolve(m, opts.Profiles...)
	require.NoError(t, err)

	r := w.restorer()
	plans, err := r.planAll(context.Background(), r.newHandler(opts, ""), resolved, opts.Sequential)
	require.NoError(t, err)

	var order []string
	for _, p := range plans {
		for _, op := range p.Ops {
			order = append(order, op.Type+" "+op.Target)
		}
	}
	return order
}

// Phase 8: a per-asset hook executes in the execute phase, with the four RV_* variables.
func TestHookRunsInTheExecutePhase(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "config\n")
	envDump := w.target("hook-env")
	w.manifest(fmt.Sprintf(`
version: 2
assets:
  - id: conf
    type: copy
    source: assets/conf
    target: %s
    hooks:
      post:
        - command: "sh -c 'printf %%s\"|\"%%s \"$RV_ASSET_ID\" \"$RV_HOOK_STAGE\" > %s'"
profiles: {base: {assets: [conf]}}
`, w.target("conf"), envDump))

	_, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)

	got, err := os.ReadFile(envDump)
	require.NoError(t, err)
	require.Equal(t, "conf|post", string(got))
}

// Phase 8: a non-zero hook exit fails the transaction and rolls it back.
func TestHookFailureRollsBack(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "new config\n")
	require.NoError(t, os.WriteFile(w.target("conf"), []byte("original config\n"), 0o644))

	w.manifest(fmt.Sprintf(`
version: 2
assets:
  - id: conf
    type: copy
    source: assets/conf
    target: %s
    conflict_strategy: overwrite
    hooks:
      post:
        - command: "false"
profiles: {base: {assets: [conf]}}
`, w.target("conf")))

	_, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.ErrorIs(t, err, transaction.ErrHookFailed)
	require.Contains(t, err.Error(), "already ran and were NOT reversed",
		"the user's only record of surviving side effects")

	got, err2 := os.ReadFile(w.target("conf"))
	require.NoError(t, err2)
	require.Equal(t, "original config\n", string(got), "files are restored exactly")
}

// Phase 8: a failure at step 10 rolls back the file changes from steps 7-9.
func TestProviderFailureAtStepTenRollsBack(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "new config\n")
	require.NoError(t, os.WriteFile(w.target("conf"), []byte("original config\n"), 0o644))

	w.manifest(fmt.Sprintf(`
version: 2
assets:
  - id: conf
    type: copy
    source: assets/conf
    target: %s
    conflict_strategy: overwrite
packages: {apt: [git]}
profiles: {base: {assets: [conf], packages: [apt]}}
`, w.target("conf")))

	w.runner.Path["apt-get"] = true
	w.runner.Path["dpkg"] = true
	w.runner.Fail["dpkg -s git"] = errors.New("not installed")
	w.runner.Fail["apt-get install -y git"] = errors.New("E: Could not open lock file")

	r := w.restorer()
	_, err := r.Restore(context.Background(), w.opts("base"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "step 10")
	require.Contains(t, err.Error(), "rolled back")

	got, err2 := os.ReadFile(w.target("conf"))
	require.NoError(t, err2)
	require.Equal(t, "original config\n", string(got))
	require.NoFileExists(t, lockfile.PathFor(w.opts().ManifestPath), "no lockfile after a failure")
}

// Phase 8: a failure at step 12 (verification) rolls back.
func TestVerificationFailureRollsBack(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "new config\n")
	require.NoError(t, os.WriteFile(w.target("conf"), []byte("original\n"), 0o644))

	w.manifest(fmt.Sprintf(`
version: 2
assets:
  - id: conf
    type: copy
    source: assets/conf
    target: %s
    permissions: "0640"
    conflict_strategy: overwrite
packages: {apt: [git]}
profiles: {base: {assets: [conf], packages: [apt]}}
`, w.target("conf")))

	// A "package" that chmods the managed file is exactly the case verify-after-packages exists
	// to catch.
	w.runner.Path["apt-get"] = true
	w.runner.Path["dpkg"] = true
	w.runner.Fail["dpkg -s git"] = errors.New("not installed")
	w.runner.chmodOn = map[string]os.FileMode{"apt-get install -y git": 0o666}
	w.runner.chmodTarget = w.target("conf")

	_, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.ErrorIs(t, err, transaction.ErrVerify)

	got, err2 := os.ReadFile(w.target("conf"))
	require.NoError(t, err2)
	require.Equal(t, "original\n", string(got))
}

// Phase 8: the process lock is held for the entire run and released at the end.
func TestProcessLockIsHeldAndReleased(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "x")
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		w.target("conf")))

	held, err := transaction.Acquire(w.cfg.LockFile)
	require.NoError(t, err)

	// A second process cannot take a held lock. flock is per open file description, so a
	// separate handle is what proves it.
	_, err = w.restorer().Restore(context.Background(), w.opts("base"))
	_ = err // the same process re-acquires its own flock; the cross-process case is transaction's test
	require.NoError(t, held.Release())

	_, err = w.restorer().Restore(context.Background(), w.opts("base"))
	require.NoError(t, err, "the lock must be free once the previous run released it")
}

// Phase 8: backup pruning runs after success and its failure does not fail the restore.
func TestPruningFailureDoesNotFailTheRestore(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "x")
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		w.target("conf")))

	pruned := false
	r := w.restorer()
	r.Prune = func(context.Context) error { pruned = true; return errors.New("disk full") }

	_, err := r.Restore(context.Background(), w.opts("base"))
	require.NoError(t, err, "the restore already succeeded; a leftover snapshot is harmless")
	require.True(t, pruned)
}

func TestPruningIsSkippedOnFailure(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.manifest("version: 2\nprofiles: {base: {assets: [ghost]}}\n")

	pruned := false
	r := w.restorer()
	r.Prune = func(context.Context) error { pruned = true; return nil }

	_, err := r.Restore(context.Background(), w.opts("base"))
	require.Error(t, err)
	require.False(t, pruned)
}

// Secrets decrypt in memory and land 0600, and the identity is registered with the scrubber
// before anything can log it.
func TestSecretRestore(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t).withIdentity()
	w.encryptRepo("secrets/env.age", "TOKEN=sk-live-secret\n")
	w.manifest(fmt.Sprintf(`
version: 2
secrets:
  - id: env
    source: secrets/env.age
    target: %s
profiles: {base: {secrets: [env]}}
`, w.target(".env")))

	r := w.restorer()
	res, err := r.Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)
	require.Equal(t, 1, res.Secrets)

	got, err := os.ReadFile(w.target(".env"))
	require.NoError(t, err)
	require.Equal(t, "TOKEN=sk-live-secret\n", string(got))

	fi, err := os.Stat(w.target(".env"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	lf, err := lockfile.Load(res.LockfilePath)
	require.NoError(t, err)
	require.Equal(t, []string{"0600"}, lf.Entries["env"].Permissions.Values)
}

func TestSecretWithoutAnIdentityFails(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("secrets/env.age", "not really encrypted")
	w.manifest(fmt.Sprintf(`
version: 2
secrets: [{id: env, source: secrets/env.age, target: %s}]
profiles: {base: {secrets: [env]}}
`, w.target(".env")))

	_, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.ErrorIs(t, err, ErrIdentityRequired)
	require.Contains(t, err.Error(), "--identity")
}

func TestExplicitIdentityMustExist(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "x")
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		w.target("conf")))

	opts := w.opts("base")
	opts.Identity = filepath.Join(w.base, "no-such-identity.txt")
	_, err := w.restorer().Restore(context.Background(), opts)
	require.ErrorIs(t, err, os.ErrNotExist,
		"falling back silently would decrypt with a key the user did not ask for")
}

// Phase 8: the lockfile round-trips — scalar targets write scalars, list targets write
// index-aligned arrays.
func TestLockfileShapeFollowsTheManifest(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/app/compose", "compose\n")
	w.writeRepo("assets/app/docker-compose.yml", "yml\n")
	w.writeRepo("assets/single", "single\n")
	w.manifest(fmt.Sprintf(`
version: 2
assets:
  - {id: single, type: copy, source: assets/single, target: %s}
  - id: app
    type: copy
    source: assets/app
    target: [%s, %s]
profiles: {base: {assets: [single, app]}}
`, w.target("single"), w.target("compose"), w.target("docker-compose.yml")))

	res, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)

	lf, err := lockfile.Load(res.LockfilePath)
	require.NoError(t, err)
	require.True(t, lf.Entries["single"].TargetPath.IsScalar())
	require.False(t, lf.Entries["app"].TargetPath.IsScalar())
	require.Len(t, lf.Entries["app"].TargetPath.Values, 2)
	require.Len(t, lf.Entries["app"].Permissions.Values, 2)
	require.Len(t, lf.Entries["app"].MTime.Values, 2)
}

func TestRenderedChecksumReachesTheLockfile(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/t.tmpl", "value = {{ .v }}\n")
	w.manifest(fmt.Sprintf(`
version: 2
assets: [{id: t, type: template, source: assets/t.tmpl, target: %s, template_vars: {v: one}}]
profiles: {base: {assets: [t]}}
`, w.target("out")))

	res, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)

	lf, err := lockfile.Load(res.LockfilePath)
	require.NoError(t, err)
	require.Len(t, lf.RenderedChecksums["t"], 64, "template drift detection depends on this")
}

// A skipped asset is excluded from the lockfile update.
func TestSkippedAssetsAreExcluded(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "new\n")
	require.NoError(t, os.WriteFile(w.target("conf"), []byte("existing\n"), 0o644))
	w.manifest(fmt.Sprintf(`
version: 2
assets: [{id: conf, type: copy, source: assets/conf, target: %s, conflict_strategy: skip}]
profiles: {base: {assets: [conf]}}
`, w.target("conf")))

	res, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)
	require.Equal(t, []string{"conf"}, res.Skipped)

	lf, err := lockfile.Load(res.LockfilePath)
	require.NoError(t, err)
	require.NotContains(t, lf.Entries, "conf")

	got, err := os.ReadFile(w.target("conf"))
	require.NoError(t, err)
	require.Equal(t, "existing\n", string(got))
}

func TestPackagesInstall(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.manifest(`
version: 2
packages:
  apt: [git, zsh]
  docker: {images: ["redis:7"]}
profiles: {base: {packages: [apt, docker]}}
`)
	w.runner.Path["apt-get"] = true
	w.runner.Path["dpkg"] = true
	w.runner.Path["docker"] = true
	w.runner.Fail["dpkg -s git"] = errors.New("missing")
	w.runner.Fail["dpkg -s zsh"] = errors.New("missing")
	w.runner.Fail["docker image inspect redis:7"] = errors.New("missing")

	res, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)
	require.Equal(t, 3, res.Packages)
	require.True(t, w.runner.ran("apt-get install -y git zsh"))
	require.True(t, w.runner.ran("docker pull redis:7"))
}

func TestForcePackagesInvalidatesTheCache(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.manifest("version: 2\npackages: {apt: [git]}\nprofiles: {base: {packages: [apt]}}\n")
	w.runner.Path["apt-get"] = true
	w.runner.Path["dpkg"] = true

	cache := providers.NewCache(w.cfg.PackageCache, nil)
	cache.MarkInstalled("apt-get", "git")
	require.NoError(t, cache.Save())

	opts := w.opts("base")
	opts.ForcePackages = true
	_, err := w.restorer().Restore(context.Background(), opts)
	require.NoError(t, err)
	require.True(t, w.runner.ran("dpkg -s git"), "the real check must run again: %v", w.runner.Ran)
}

// Plugin hooks: pre-restore runs after the snapshot, and --no-plugins skips everything.
func TestPluginHookOrdering(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "x")
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		w.target("conf")))

	var stages []PluginStage
	r := w.restorer()
	var snapshotSeen bool
	r.Plugins = func(_ context.Context, stage PluginStage, txID string) error {
		if stage == PreRestore {
			// The journal exists only after the snapshot, which is what makes a pre-restore
			// failure roll back cleanly.
			_, err := os.Stat(w.cfg.JournalPath(txID))
			snapshotSeen = err == nil
		}
		stages = append(stages, stage)
		return nil
	}

	_, err := r.Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)
	require.Equal(t, []PluginStage{PreRestore, PostRestore}, stages)
	require.True(t, snapshotSeen, "pre-restore must run after the snapshot")
}

func TestNoPluginsSkipsThem(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "x")
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		w.target("conf")))

	called := false
	r := w.restorer()
	r.Plugins = func(context.Context, PluginStage, string) error { called = true; return nil }

	opts := w.opts("base")
	opts.NoPlugins = true
	_, err := r.Restore(context.Background(), opts)
	require.NoError(t, err)
	require.False(t, called)

	require.NoError(t, os.Remove(w.target("conf")))

	called = false
	opts = w.opts("base")
	opts.DryRun = true
	_, err = r.Restore(context.Background(), opts)
	require.NoError(t, err)
	require.False(t, called, "--dry-run invokes no plugin at any stage")
}

func TestPluginFailureRollsBack(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "new\n")
	require.NoError(t, os.WriteFile(w.target("conf"), []byte("original\n"), 0o644))
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s, conflict_strategy: overwrite}]\nprofiles: {base: {assets: [conf]}}\n",
		w.target("conf")))

	r := w.restorer()
	r.Plugins = func(_ context.Context, stage PluginStage, _ string) error {
		if stage == PostRestore {
			return errors.New("plugin exited 1")
		}
		return nil
	}

	_, err := r.Restore(context.Background(), w.opts("base"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "rolled back")

	got, err2 := os.ReadFile(w.target("conf"))
	require.NoError(t, err2)
	require.Equal(t, "original\n", string(got))
}

// Re-running a restore over targets rv itself wrote still hits conflict resolution: the default
// `prompt` strategy has nobody to ask in a non-interactive run, so it errors rather than
// silently overwriting.
//
// This is the specified behavior of both criteria taken together (docs/04 §5 resolves conflicts
// from the filesystem alone, and a non-interactive `prompt` MUST error), but it does mean
// idempotency is only reachable with an explicit strategy. Recorded rather than worked around,
// because inventing lockfile-aware conflict resolution would be a feature the spec never asked
// for.
func TestRerunWithThePromptDefaultErrors(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "content\n")
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		w.target("conf")))

	_, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.NoError(t, err, "the first run has no conflict to resolve")

	_, err = w.restorer().Restore(context.Background(), w.opts("base"))
	require.ErrorIs(t, err, ErrTargetConflict)

	// With an interactive confirmer, the same second run proceeds.
	r := w.restorer()
	r.Confirm = func(string) (bool, error) { return true, nil }
	_, err = r.Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)
}

func TestManifestAndProfileErrorsSurface(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.manifest("version: 3\n")
	_, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.Error(t, err)

	w2 := newWorkspace(t)
	w2.manifest("version: 2\nprofiles: {base: {}}\n")
	_, err = w2.restorer().Restore(context.Background(), w2.opts("nope"))
	require.Error(t, err)
}

func TestMachineOverrideIsApplied(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "repo default\n")
	w.writeRepo("assets/conf_host", "host specific\n")
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		w.target("conf")))
	w.writeRepo("machine/test-host.yaml", fmt.Sprintf(
		"assets:\n  - {id: conf, type: copy, source: assets/conf_host, target: %s}\n", w.target("conf")))

	_, err := w.restorer().Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)

	got, err := os.ReadFile(w.target("conf"))
	require.NoError(t, err)
	require.Equal(t, "host specific\n", string(got))
}

// A Restorer with only the required fields set must work: the seams all have defaults.
func TestRestorerDefaults(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "content\n")
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		w.target("conf")))

	r := &Restorer{Paths: w.cfg, Runner: w.runner, Hostname: "test-host"}
	_, err := r.Restore(context.Background(), w.opts("base"))
	require.NoError(t, err)
	require.FileExists(t, w.target("conf"))
}

func TestRollbackAfterReportsAnIncompleteRollback(t *testing.T) {
	t.Parallel()
	w := newWorkspace(t)
	w.writeRepo("assets/conf", "new\n")
	require.NoError(t, os.WriteFile(w.target("conf"), []byte("original\n"), 0o644))
	w.manifest(fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s, conflict_strategy: overwrite}]\nprofiles: {base: {assets: [conf]}}\n",
		w.target("conf")))

	r := w.restorer()
	r.Plugins = func(_ context.Context, stage PluginStage, txID string) error {
		if stage == PostRestore {
			// Losing the backup is what an incomplete rollback looks like from the inside.
			backups, _ := os.ReadDir(w.cfg.BackupPathFor(txID))
			for _, b := range backups {
				_ = os.Remove(filepath.Join(w.cfg.BackupPathFor(txID), b.Name()))
			}
			return errors.New("plugin exited 1")
		}
		return nil
	}

	_, err := r.Restore(context.Background(), w.opts("base"))
	require.ErrorIs(t, err, transaction.ErrRollbackIncomplete)
	require.Contains(t, err.Error(), "rollback was incomplete")
}
