package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/lockfile"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/transaction"
)

type fixture struct {
	h     *Handler
	repo  string
	home  string
	t     *testing.T
	pub   string
	idKey string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	home := filepath.Join(base, "home")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	require.NoError(t, os.MkdirAll(home, 0o755))

	f := &fixture{repo: repo, home: home, t: t}
	f.h = &Handler{
		RepoDir:  repo,
		Paths:    paths.New(home),
		Lookup:   func(k string) (string, bool) { v, ok := map[string]string{"HOME_DIR": home}[k]; return v, ok },
		Hostname: "test-host",
		User:     "test-user",
		Platform: "linux",
		Arch:     "amd64",
		Home:     home,
	}
	return f
}

func (f *fixture) source(name, content string) string {
	f.t.Helper()
	p := filepath.Join(f.repo, name)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(f.t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func (f *fixture) target(name string) string { return filepath.Join(f.home, name) }

// withIdentity generates a keypair and points the handler at it.
func (f *fixture) withIdentity() *fixture {
	f.t.Helper()
	pub, identity, err := crypto.GenerateKeypair()
	require.NoError(f.t, err)
	f.pub, f.idKey = pub, identity
	f.h.Identity = identity
	return f
}

func (f *fixture) encrypt(name, plaintext string) string {
	f.t.Helper()
	ciphertext, err := crypto.Encrypt([]byte(plaintext), []string{f.pub})
	require.NoError(f.t, err)
	p := filepath.Join(f.repo, name)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(f.t, os.WriteFile(p, ciphertext, 0o644))
	return p
}

func asset(id string, mods ...func(*manifest.Asset)) manifest.Asset {
	a := manifest.Asset{
		ID:               id,
		Type:             manifest.TypeSymlink,
		ConflictStrategy: manifest.ConflictOverwrite,
	}
	for _, m := range mods {
		m(&a)
	}
	return a
}

func opTypes(ops []transaction.Operation) []string {
	out := make([]string, len(ops))
	for i, op := range ops {
		out[i] = op.Type
	}
	return out
}

// Phase 6: a symlink asset plans delete-if-exists then a symlink to the absolute source.
func TestPlanSymlink(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	src := f.source("assets/zshrc", "export EDITOR=vim\n")

	plan, err := f.h.PlanAsset(asset("zshrc", func(a *manifest.Asset) {
		a.Source = "assets/zshrc"
		a.Target = manifest.Scalar(f.target(".zshrc"))
	}))
	require.NoError(t, err)
	require.False(t, plan.Skipped)
	require.Equal(t, []string{transaction.OpTypeSymlink}, opTypes(plan.Ops),
		"nothing exists at the target, so no delete is planned")
	require.Equal(t, transaction.SourcePath{Path: src}, plan.Ops[0].Source)
	require.Equal(t, []string{f.target(".zshrc")}, plan.Targets)
}

func TestPlanSymlinkOverAnExistingTarget(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/zshrc", "new")
	require.NoError(t, os.WriteFile(f.target(".zshrc"), []byte("old"), 0o644))

	plan, err := f.h.PlanAsset(asset("zshrc", func(a *manifest.Asset) {
		a.Source = "assets/zshrc"
		a.Target = manifest.Scalar(f.target(".zshrc"))
	}))
	require.NoError(t, err)
	require.Equal(t, []string{transaction.OpTypeDelete, transaction.OpTypeSymlink}, opTypes(plan.Ops))
}

// Phase 6: symlink loop detection on the source is a hard error.
func TestPlanSymlinkDetectsALoop(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	a := filepath.Join(f.repo, "assets", "a")
	b := filepath.Join(f.repo, "assets", "b")
	require.NoError(t, os.MkdirAll(filepath.Dir(a), 0o755))
	require.NoError(t, os.Symlink(b, a))
	require.NoError(t, os.Symlink(a, b))

	_, err := f.h.PlanAsset(asset("looped", func(x *manifest.Asset) {
		x.Source = "assets/a"
		x.Target = manifest.Scalar(f.target("out"))
	}))
	require.ErrorIs(t, err, ErrSymlinkLoop)
}

// Phase 6: a copy asset with a directory source plans a copy of the directory.
func TestPlanCopyOfADirectory(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dir := filepath.Join(f.repo, "assets", "conf")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inner"), []byte("x"), 0o644))

	plan, err := f.h.PlanAsset(asset("conf", func(a *manifest.Asset) {
		a.Type = manifest.TypeCopy
		a.Source = "assets/conf"
		a.Target = manifest.Scalar(f.target("conf"))
	}))
	require.NoError(t, err)
	require.Equal(t, []string{transaction.OpTypeCopy}, opTypes(plan.Ops))
	require.Equal(t, transaction.SourcePath{Path: dir}, plan.Ops[0].Source)
}

// Phase 6: a directory source with list targets matches each target's basename inside the
// source, trying <basename>.age first for encrypted assets.
func TestDirectoryFanOut(t *testing.T) {
	t.Parallel()
	f := newFixture(t).withIdentity()
	f.source("assets/app/compose", "compose contents")
	f.source("assets/app/docker-compose.yml", "yml contents")

	plan, err := f.h.PlanAsset(asset("app", func(a *manifest.Asset) {
		a.Type = manifest.TypeCopy
		a.Source = "assets/app"
		a.Target = manifest.Slice(f.target("compose"), f.target("docker-compose.yml"))
	}))
	require.NoError(t, err)
	require.Len(t, plan.Ops, 2)
	require.Equal(t, transaction.SourcePath{Path: filepath.Join(f.repo, "assets/app/compose")}, plan.Ops[0].Source)
	require.Equal(t, transaction.SourcePath{Path: filepath.Join(f.repo, "assets/app/docker-compose.yml")}, plan.Ops[1].Source)
}

func TestEncryptedDirectoryFanOutPrefersTheAgeFile(t *testing.T) {
	t.Parallel()
	f := newFixture(t).withIdentity()
	f.encrypt("secrets/app_env/.env.age", "SECRET=from-age\n")
	// A plaintext file of the same basename must lose to the .age one.
	f.source("secrets/app_env/.env", "SECRET=from-plaintext\n")

	plan, err := f.h.PlanAsset(asset("app_env", func(a *manifest.Asset) {
		a.Type = manifest.TypeSecret
		a.Encrypted = true
		a.Source = "secrets/app_env"
		a.Target = manifest.Scalar(f.target(".env"))
	}))
	require.NoError(t, err)
	require.Equal(t, transaction.SourceBytes{Data: []byte("SECRET=from-age\n")}, plan.Ops[0].Source)
}

// Phase 6: a secret decrypts and plans a copy at mode 0600; a missing identity errors.
func TestPlanSecret(t *testing.T) {
	t.Parallel()
	f := newFixture(t).withIdentity()
	f.encrypt("secrets/env.age", "TOKEN=sk-live-1\n")

	plan, err := f.h.PlanAsset(asset("env", func(a *manifest.Asset) {
		a.Type = manifest.TypeSecret
		a.Encrypted = true
		a.Source = "secrets/env.age"
		a.Target = manifest.Scalar(f.target(".env"))
	}))
	require.NoError(t, err)
	require.Equal(t, transaction.SourceBytes{Data: []byte("TOKEN=sk-live-1\n")}, plan.Ops[0].Source)
	require.Equal(t, "0600", plan.Ops[0].Permissions, "a secret with no declared mode lands 0600")
	require.Equal(t, transaction.OpTypeChmod, plan.Ops[1].Type)
}

func TestPlanSecretWithoutAnIdentity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("secrets/env.age", "not really encrypted")

	_, err := f.h.PlanAsset(asset("env", func(a *manifest.Asset) {
		a.Type = manifest.TypeSecret
		a.Encrypted = true
		a.Source = "secrets/env.age"
		a.Target = manifest.Scalar(f.target(".env"))
	}))
	require.ErrorIs(t, err, crypto.ErrIdentityRequired)
	require.Contains(t, err.Error(), "--identity", "the error must say how to fix it")
}

func TestPlanEncryptedCopy(t *testing.T) {
	t.Parallel()
	f := newFixture(t).withIdentity()
	f.encrypt("secrets/config.age", "encrypted config\n")

	plan, err := f.h.PlanAsset(asset("config", func(a *manifest.Asset) {
		a.Type = manifest.TypeCopy
		a.Encrypted = true
		a.Source = "secrets/config.age"
		a.Target = manifest.Scalar(f.target("config"))
	}))
	require.NoError(t, err)
	require.Equal(t, transaction.SourceBytes{Data: []byte("encrypted config\n")}, plan.Ops[0].Source)
}

func TestDecryptionFailureIsReported(t *testing.T) {
	t.Parallel()
	f := newFixture(t).withIdentity()
	f.source("secrets/broken.age", "this is not age ciphertext")

	_, err := f.h.PlanAsset(asset("broken", func(a *manifest.Asset) {
		a.Type = manifest.TypeSecret
		a.Encrypted = true
		a.Source = "secrets/broken.age"
		a.Target = manifest.Scalar(f.target(".env"))
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "broken")
}

// An encrypted source is allowed to be missing at this check; decryption is what fails.
func TestMissingSourceIsAnError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.h.PlanAsset(asset("gone", func(a *manifest.Asset) {
		a.Source = "assets/gone"
		a.Target = manifest.Scalar(f.target("gone"))
	}))
	require.ErrorIs(t, err, ErrSourceNotFound)
}

// Phase 6: conflict strategies.
func TestConflictStrategies(t *testing.T) {
	t.Parallel()
	t.Run("skip", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/a", "new")
		require.NoError(t, os.WriteFile(f.target("a"), []byte("old"), 0o644))

		plan, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
			a.Source, a.ConflictStrategy = "assets/a", manifest.ConflictSkip
			a.Target = manifest.Scalar(f.target("a"))
		}))
		require.NoError(t, err)
		require.True(t, plan.Skipped)
		require.Empty(t, plan.Ops)
	})

	t.Run("abort", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/a", "new")
		require.NoError(t, os.WriteFile(f.target("a"), []byte("old"), 0o644))

		_, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
			a.Source, a.ConflictStrategy = "assets/a", manifest.ConflictAbort
			a.Target = manifest.Scalar(f.target("a"))
		}))
		require.ErrorIs(t, err, ErrTargetConflict)
	})

	t.Run("prompt in non-interactive mode errors", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/a", "new")
		require.NoError(t, os.WriteFile(f.target("a"), []byte("old"), 0o644))

		_, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
			a.Source, a.ConflictStrategy = "assets/a", manifest.ConflictPrompt
			a.Target = manifest.Scalar(f.target("a"))
		}))
		require.ErrorIs(t, err, ErrTargetConflict, "silently skipping would be silent data loss")
	})

	t.Run("prompt declined skips", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/a", "new")
		require.NoError(t, os.WriteFile(f.target("a"), []byte("old"), 0o644))
		f.h.Confirm = func(string) (bool, error) { return false, nil }

		plan, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
			a.Source, a.ConflictStrategy = "assets/a", manifest.ConflictPrompt
			a.Target = manifest.Scalar(f.target("a"))
		}))
		require.NoError(t, err)
		require.True(t, plan.Skipped)
	})

	t.Run("prompt accepted proceeds", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/a", "new")
		require.NoError(t, os.WriteFile(f.target("a"), []byte("old"), 0o644))
		f.h.Confirm = func(string) (bool, error) { return true, nil }

		plan, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
			a.Source, a.ConflictStrategy = "assets/a", manifest.ConflictPrompt
			a.Target = manifest.Scalar(f.target("a"))
		}))
		require.NoError(t, err)
		require.False(t, plan.Skipped)
	})
}

// Phase 6: hooks are planned in the interleaved order delete → pre → write → chmod → post.
func TestHookOrdering(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/a", "new")
	require.NoError(t, os.WriteFile(f.target("a"), []byte("old"), 0o644))

	plan, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
		a.Type = manifest.TypeCopy
		a.Source = "assets/a"
		a.Target = manifest.Scalar(f.target("a"))
		perms := "0644"
		a.Permissions = &perms
		a.Hooks.Pre = []manifest.Hook{{Command: "mkdir -p /opt/app"}}
		a.Hooks.Post = []manifest.Hook{{Command: "systemctl --user restart app"}}
	}))
	require.NoError(t, err)
	require.Equal(t, []string{
		transaction.OpTypeDelete,
		transaction.OpTypeHook,
		transaction.OpTypeCopy,
		transaction.OpTypeChmod,
		transaction.OpTypeHook,
	}, opTypes(plan.Ops))

	require.Equal(t, "pre", plan.Ops[1].Hook.Stage)
	require.Equal(t, []string{"mkdir", "-p", "/opt/app"}, plan.Ops[1].Hook.Command)
	require.Equal(t, "post", plan.Ops[4].Hook.Stage)
	require.Equal(t, []string{"systemctl", "--user", "restart", "app"}, plan.Ops[4].Hook.Command)
}

// Phase 6: planning is side-effect free — a hook that would create a marker creates nothing.
func TestPlanningRunsNoHook(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/a", "new")
	marker := f.target("marker")

	_, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
		a.Type = manifest.TypeCopy
		a.Source = "assets/a"
		a.Target = manifest.Scalar(f.target("a"))
		a.Hooks.Pre = []manifest.Hook{{Command: "touch " + marker}}
		a.Hooks.Post = []manifest.Hook{{Command: "touch " + marker}}
	}))
	require.NoError(t, err)
	require.NoFileExists(t, marker, "planning must execute nothing")
}

// Phase 6: a skipped target contributes no operations, hooks included.
func TestSkippedTargetPlansNoHooks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/a", "new")
	require.NoError(t, os.WriteFile(f.target("a"), []byte("old"), 0o644))

	plan, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
		a.Source, a.ConflictStrategy = "assets/a", manifest.ConflictSkip
		a.Target = manifest.Scalar(f.target("a"))
		a.Hooks.Pre = []manifest.Hook{{Command: "echo pre"}}
		a.Hooks.Post = []manifest.Hook{{Command: "echo post"}}
	}))
	require.NoError(t, err)
	require.Empty(t, plan.Ops, "a hook attached to a target that was skipped has nothing to bracket")
}

// Phase 6: a hook command with a malformed quote fails at plan time.
func TestMalformedHookQuoteFailsAtPlanTime(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/a", "new")

	_, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
		a.Type = manifest.TypeCopy
		a.Source = "assets/a"
		a.Target = manifest.Scalar(f.target("a"))
		a.Hooks.Pre = []manifest.Hook{{Command: `echo "unterminated`}}
	}))
	require.ErrorIs(t, err, ErrBadHookSyntax)
}

// Phase 6: a plugin reference inside asset hooks errors.
func TestAssetPluginHookErrors(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/a", "new")

	_, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
		a.Type = manifest.TypeCopy
		a.Source = "assets/a"
		a.Target = manifest.Scalar(f.target("a"))
		a.Hooks.Post = []manifest.Hook{{Plugin: "notify"}}
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "pre-restore", "the error must name the supported alternative")
}

func TestTargetInterpolation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/a", "new")

	plan, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
		a.Source = "assets/a"
		a.Target = manifest.Scalar("${HOME_DIR}/interpolated")
	}))
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(f.home, "interpolated")}, plan.Targets)

	_, err = f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
		a.Source = "assets/a"
		a.Target = manifest.Scalar("${NOT_SET}/x")
	}))
	require.ErrorIs(t, err, paths.ErrUnsetVariable)
}

func TestUnsupportedAssetType(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/a", "new")

	_, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
		a.Type = "hardlink"
		a.Source = "assets/a"
		a.Target = manifest.Scalar(f.target("a"))
	}))
	require.ErrorIs(t, err, ErrUnsupportedType)
}

func TestNewHandlerFillsInBuiltins(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	h := NewHandler("/repo", paths.New(home))
	require.Equal(t, "/repo", h.RepoDir)
	require.Equal(t, home, h.Home)
	require.NotEmpty(t, h.Platform)
	require.NotEmpty(t, h.Arch)
	require.NotNil(t, h.Lookup)
}

func TestSplitEnv(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][2]string{
		"KEY=value": {"KEY", "value"},
		"KEY=":      {"KEY", ""},
		"KEY=a=b":   {"KEY", "a=b"},
	} {
		k, v, ok := splitEnv(in)
		require.True(t, ok, in)
		require.Equal(t, want[0], k, in)
		require.Equal(t, want[1], v, in)
	}

	for _, in := range []string{"=value", "novalue"} {
		_, _, ok := splitEnv(in)
		require.False(t, ok, in)
	}
}

func TestHandlerFallsBackToTheProcessEnvironment(t *testing.T) {
	t.Setenv("RV_TEST_TARGET_DIR", t.TempDir())
	f := newFixture(t)
	f.h.Lookup = nil // no injected lookup: os.LookupEnv is the fallback
	f.source("assets/a", "content")

	plan, err := f.h.PlanAsset(asset("a", func(a *manifest.Asset) {
		a.Source = "assets/a"
		a.Target = manifest.Scalar("${RV_TEST_TARGET_DIR}/out")
	}))
	require.NoError(t, err)
	require.Len(t, plan.Targets, 1)
}

func TestOwnsTarget(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	target := f.target("conf")
	require.NoError(t, os.WriteFile(target, []byte("content\n"), 0o644))

	fi, err := os.Lstat(target)
	require.NoError(t, err)
	recorded := float64(fi.ModTime().UnixNano()) / float64(time.Second)

	// Nothing is owned without a lockfile.
	require.False(t, f.h.ownsTarget("conf", target))

	f.h.Lockfile = lockfile.New()
	require.False(t, f.h.ownsTarget("conf", target), "an empty lockfile owns nothing")

	f.h.Lockfile.Entries["conf"] = lockfile.Entry{
		TargetPath: manifest.Scalar(target),
		MTime:      lockfile.ScalarFloat(recorded),
	}
	require.True(t, f.h.ownsTarget("conf", target))
	require.False(t, f.h.ownsTarget("other", target), "ownership is per asset")
	require.False(t, f.h.ownsTarget("conf", f.target("elsewhere")), "and per target")

	// Any edit moves the modification time.
	later := fi.ModTime().Add(time.Second)
	require.NoError(t, os.Chtimes(target, later, later))
	require.False(t, f.h.ownsTarget("conf", target))

	// A recorded target that no longer exists is not owned.
	require.NoError(t, os.Remove(target))
	require.False(t, f.h.ownsTarget("conf", target))
}

func TestOwnershipToleratesTimestampGranularity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	target := f.target("conf")
	require.NoError(t, os.WriteFile(target, []byte("content\n"), 0o644))

	fi, err := os.Lstat(target)
	require.NoError(t, err)
	recorded := float64(fi.ModTime().UnixNano())/float64(time.Second) + 0.0005

	f.h.Lockfile = lockfile.New()
	f.h.Lockfile.Entries["conf"] = lockfile.Entry{
		TargetPath: manifest.Scalar(target),
		MTime:      lockfile.ScalarFloat(recorded),
	}
	require.True(t, f.h.ownsTarget("conf", target),
		"sub-millisecond drift is filesystem granularity, not an edit")
}
