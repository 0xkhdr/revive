package status

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/crypto"
	"github.com/0xkhdr/revive/internal/engine"
	"github.com/0xkhdr/revive/internal/lockfile"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/profile"
)

type fixture struct {
	t    *testing.T
	repo string
	home string
	h    *engine.Handler
	lf   *lockfile.Lockfile
	pub  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		t:    t,
		repo: filepath.Join(base, "repo"),
		home: filepath.Join(base, "home"),
		lf:   lockfile.New(),
	}
	require.NoError(t, os.MkdirAll(f.repo, 0o755))
	require.NoError(t, os.MkdirAll(f.home, 0o755))

	f.h = &engine.Handler{
		RepoDir:  f.repo,
		Paths:    paths.New(filepath.Join(base, "rv-home")),
		Lookup:   func(string) (string, bool) { return "", false },
		Hostname: "test-host",
		User:     "test-user",
		Platform: "linux",
		Arch:     "amd64",
		Home:     f.home,
	}
	return f
}

func (f *fixture) checker() *Checker { return New(f.h, f.lf) }

func (f *fixture) source(name, content string) string {
	f.t.Helper()
	p := filepath.Join(f.repo, name)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(f.t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func (f *fixture) target(name, content string, mode os.FileMode) string {
	f.t.Helper()
	p := filepath.Join(f.home, name)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(f.t, os.WriteFile(p, []byte(content), mode))
	require.NoError(f.t, os.Chmod(p, mode))
	return p
}

func (f *fixture) withIdentity() *fixture {
	f.t.Helper()
	pub, identity, err := crypto.GenerateKeypair()
	require.NoError(f.t, err)
	f.pub = pub
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
	a := manifest.Asset{ID: id, Type: manifest.TypeCopy, ConflictStrategy: manifest.ConflictOverwrite}
	for _, m := range mods {
		m(&a)
	}
	return a
}

func perms(s string) *string { return &s }

// Phase 10: each of the six status values is produced by a targeted fixture.
func TestEachStatusValue(t *testing.T) {
	t.Parallel()

	t.Run("in_sync", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/conf", "content\n")
		target := f.target("conf", "content\n", 0o644)

		got := f.checker().CheckAsset(asset("conf", func(a *manifest.Asset) {
			a.Source, a.Target = "assets/conf", manifest.Scalar(target)
		}))
		require.Equal(t, InSync, got[0].Status, got[0].Detail)
	})

	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/conf", "content\n")

		got := f.checker().CheckAsset(asset("conf", func(a *manifest.Asset) {
			a.Source = "assets/conf"
			a.Target = manifest.Scalar(filepath.Join(f.home, "absent"))
		}))
		require.Equal(t, Missing, got[0].Status)
	})

	t.Run("type_mismatch", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		source := f.source("assets/conf", "content\n")
		link := filepath.Join(f.home, "conf")
		require.NoError(t, os.Symlink(source, link))

		got := f.checker().CheckAsset(asset("conf", func(a *manifest.Asset) {
			a.Source, a.Target = "assets/conf", manifest.Scalar(link)
		}))
		require.Equal(t, TypeMismatch, got[0].Status,
			"a symlink where a file was declared is a type problem, not a content one")
	})

	t.Run("permissions_drifted", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/conf", "content\n")
		target := f.target("conf", "content\n", 0o666)

		got := f.checker().CheckAsset(asset("conf", func(a *manifest.Asset) {
			a.Source, a.Target = "assets/conf", manifest.Scalar(target)
			a.Permissions = perms("0644")
		}))
		require.Equal(t, PermissionsDrifted, got[0].Status)
		require.Contains(t, got[0].Detail, "0644")
		require.Contains(t, got[0].Detail, "0666")
	})

	t.Run("modified", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/conf", "from the repo\n")
		target := f.target("conf", "edited by hand\n", 0o644)

		got := f.checker().CheckAsset(asset("conf", func(a *manifest.Asset) {
			a.Source, a.Target = "assets/conf", manifest.Scalar(target)
		}))
		require.Equal(t, Modified, got[0].Status)
	})

	t.Run("error", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.source("assets/conf", "content\n")

		got := f.checker().CheckAsset(asset("conf", func(a *manifest.Asset) {
			a.Source, a.Target = "assets/conf", manifest.Scalar("${NEVER_SET}/conf")
		}))
		require.Equal(t, Error, got[0].Status)
		require.Contains(t, got[0].Detail, "NEVER_SET")
	})
}

// Phase 10: a hand-edited symlink target reports type_mismatch, not modified.
func TestSymlinkChecks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	source := f.source("assets/zshrc", "export EDITOR=vim\n")
	elsewhere := f.source("assets/other", "something else\n")

	link := filepath.Join(f.home, ".zshrc")
	require.NoError(t, os.Symlink(source, link))
	declared := asset("zshrc", func(a *manifest.Asset) {
		a.Type, a.Source, a.Target = manifest.TypeSymlink, "assets/zshrc", manifest.Scalar(link)
	})

	got := f.checker().CheckAsset(declared)
	require.Equal(t, InSync, got[0].Status, got[0].Detail)

	// Repointed at another file: that is content drift for a symlink.
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(elsewhere, link))
	got = f.checker().CheckAsset(declared)
	require.Equal(t, Modified, got[0].Status)
	require.Contains(t, got[0].Detail, elsewhere)

	// Replaced by a regular file: that is a type problem.
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.WriteFile(link, []byte("a real file now\n"), 0o644))
	got = f.checker().CheckAsset(declared)
	require.Equal(t, TypeMismatch, got[0].Status)
}

// A relative link resolves against the link's own directory, not the process's cwd.
func TestRelativeSymlinkResolution(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	source := f.source("assets/conf", "content\n")

	link := filepath.Join(f.home, "conf")
	rel, err := filepath.Rel(f.home, source)
	require.NoError(t, err)
	require.NoError(t, os.Symlink(rel, link))

	got := f.checker().CheckAsset(asset("conf", func(a *manifest.Asset) {
		a.Type, a.Source, a.Target = manifest.TypeSymlink, "assets/conf", manifest.Scalar(link)
	}))
	require.Equal(t, InSync, got[0].Status, got[0].Detail)
}

// Phase 10: permissions are checked before any content check runs.
func TestPermissionsAreCheckedBeforeContent(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/conf", "from the repo\n")
	// Both the mode AND the content differ; the mode must be what is reported.
	target := f.target("conf", "edited by hand\n", 0o600)

	got := f.checker().CheckAsset(asset("conf", func(a *manifest.Asset) {
		a.Source, a.Target = "assets/conf", manifest.Scalar(target)
		a.Permissions = perms("0644")
	}))
	require.Equal(t, PermissionsDrifted, got[0].Status)
}

func TestDefaultModes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/conf", "content\n")
	f.source("secrets/env.age", "ciphertext")

	// An asset with no declared mode defaults to 0644.
	target := f.target("conf", "content\n", 0o644)
	got := f.checker().CheckAsset(asset("conf", func(a *manifest.Asset) {
		a.Source, a.Target = "assets/conf", manifest.Scalar(target)
	}))
	require.Equal(t, InSync, got[0].Status, got[0].Detail)

	// A secret with no declared mode defaults to 0600.
	secretTarget := f.target(".env", "TOKEN=x\n", 0o644)
	got = f.checker().CheckAsset(asset("env", func(a *manifest.Asset) {
		a.Type, a.Encrypted = manifest.TypeSecret, true
		a.Source, a.Target = "secrets/env.age", manifest.Scalar(secretTarget)
	}))
	require.Equal(t, PermissionsDrifted, got[0].Status)
	require.Contains(t, got[0].Detail, "0600")
}

// Phase 10: template drift is detected when a template input variable changes.
func TestTemplateDrift(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/gitconfig.tmpl", "email = {{ .email }}\n")

	declared := asset("gitconfig", func(a *manifest.Asset) {
		a.Type = manifest.TypeTemplate
		a.Source = "assets/gitconfig.tmpl"
		a.Target = manifest.Scalar(f.target(".gitconfig", "email = dev@example.com\n", 0o644))
		a.TemplateVars = map[string]any{"email": "dev@example.com"}
	})
	got := f.checker().CheckAsset(declared)
	require.Equal(t, InSync, got[0].Status, got[0].Detail)

	// Changing the input changes what the declaration would produce today, which is drift.
	declared.TemplateVars = map[string]any{"email": "work@example.com"}
	got = f.checker().CheckAsset(declared)
	require.Equal(t, Modified, got[0].Status)
}

func TestTemplateRenderFailureIsAnError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/t.tmpl", "{{ .never_defined }}\n")

	got := f.checker().CheckAsset(asset("t", func(a *manifest.Asset) {
		a.Type, a.Source = manifest.TypeTemplate, "assets/t.tmpl"
		a.Target = manifest.Scalar(f.target("out", "anything\n", 0o644))
	}))
	require.Equal(t, Error, got[0].Status)
}

// Phase 10: encrypted drift detection works with an identity.
func TestEncryptedDriftWithAnIdentity(t *testing.T) {
	t.Parallel()
	f := newFixture(t).withIdentity()
	f.encrypt("secrets/env.age", "TOKEN=sk-live-1\n")

	declared := asset("env", func(a *manifest.Asset) {
		a.Type, a.Encrypted = manifest.TypeSecret, true
		a.Source = "secrets/env.age"
		a.Target = manifest.Scalar(f.target(".env", "TOKEN=sk-live-1\n", 0o600))
	})
	got := f.checker().CheckAsset(declared)
	require.Equal(t, InSync, got[0].Status, got[0].Detail)

	f.target(".env", "TOKEN=tampered\n", 0o600)
	got = f.checker().CheckAsset(declared)
	require.Equal(t, Modified, got[0].Status)
}

// Phase 10: without an identity it falls back to the lockfile mtime.
func TestEncryptedDriftFallsBackToTheLockfileMTime(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("secrets/env.age", "ciphertext nobody here can read")
	target := f.target(".env", "TOKEN=whatever\n", 0o600)

	declared := asset("env", func(a *manifest.Asset) {
		a.Type, a.Encrypted = manifest.TypeSecret, true
		a.Source, a.Target = "secrets/env.age", manifest.Scalar(target)
	})

	// No identity and no lockfile entry: fail safe and assume it changed.
	got := f.checker().CheckAsset(declared)
	require.Equal(t, Modified, got[0].Status)
	require.Contains(t, got[0].Detail, "no identity")

	// With a matching recorded mtime, it is in sync.
	fi, err := os.Stat(target)
	require.NoError(t, err)
	recorded := float64(fi.ModTime().UnixNano()) / float64(time.Second)
	f.lf.Entries["env"] = lockfile.Entry{
		TargetPath: manifest.Scalar(target),
		MTime:      lockfile.ScalarFloat(recorded),
	}
	got = f.checker().CheckAsset(declared)
	require.Equal(t, InSync, got[0].Status, got[0].Detail)

	// Touching the file drifts it.
	later := fi.ModTime().Add(time.Hour)
	require.NoError(t, os.Chtimes(target, later, later))
	got = f.checker().CheckAsset(declared)
	require.Equal(t, Modified, got[0].Status)
}

// A wrong identity falls through to the mtime path rather than making status useless.
func TestUndecryptableSourceFallsBackRatherThanErroring(t *testing.T) {
	t.Parallel()
	f := newFixture(t).withIdentity()
	f.source("secrets/env.age", "not actually age ciphertext")
	target := f.target(".env", "TOKEN=x\n", 0o600)

	got := f.checker().CheckAsset(asset("env", func(a *manifest.Asset) {
		a.Type, a.Encrypted = manifest.TypeSecret, true
		a.Source, a.Target = "secrets/env.age", manifest.Scalar(target)
	}))
	require.Equal(t, Modified, got[0].Status)
	require.NotEqual(t, Error, got[0].Status)
}

// Directory assets use the deterministic sorted-walk hash rather than an mtime comparison.
func TestDirectoryDriftUsesTheContentHash(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "assets", "conf.d"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "assets", "conf.d", "a.conf"), []byte("a\n"), 0o644))

	targetDir := filepath.Join(f.home, "conf.d")
	require.NoError(t, os.MkdirAll(targetDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "a.conf"), []byte("a\n"), 0o644))

	declared := asset("confd", func(a *manifest.Asset) {
		a.Source, a.Target = "assets/conf.d", manifest.Scalar(targetDir)
		a.Permissions = perms("0755")
	})
	got := f.checker().CheckAsset(declared)
	require.Equal(t, InSync, got[0].Status, got[0].Detail)

	// An edited file inside is drift, which an mtime comparison on the directory would miss.
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "a.conf"), []byte("edited\n"), 0o644))
	got = f.checker().CheckAsset(declared)
	require.Equal(t, Modified, got[0].Status)
}

func TestMultiTargetProducesOneResultEach(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "assets", "app"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "assets", "app", "one"), []byte("one\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(f.repo, "assets", "app", "two"), []byte("two\n"), 0o644))

	one := f.target("one", "one\n", 0o644)
	two := f.target("two", "drifted\n", 0o644)

	got := f.checker().CheckAsset(asset("app", func(a *manifest.Asset) {
		a.Source, a.Target = "assets/app", manifest.Slice(one, two)
	}))
	require.Len(t, got, 2)
	require.Equal(t, InSync, got[0].Status, got[0].Detail)
	require.Equal(t, Modified, got[1].Status)
}

func TestReportAggregation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/good", "same\n")
	f.source("assets/bad", "repo\n")
	f.source("secrets/env.age", "ciphertext")

	m, err := manifest.Parse([]byte("version: 2\nprofiles: {base: {}}\n"))
	require.NoError(t, err)
	resolved, err := profile.Resolve(m, "base")
	require.NoError(t, err)

	resolved.PutAsset(asset("good", func(a *manifest.Asset) {
		a.Source, a.Target = "assets/good", manifest.Scalar(f.target("good", "same\n", 0o644))
	}))
	report := f.checker().Check(resolved)
	require.False(t, report.Drifted)
	require.Len(t, report.Results, 1)

	resolved.PutAsset(asset("bad", func(a *manifest.Asset) {
		a.Source, a.Target = "assets/bad", manifest.Scalar(f.target("bad", "machine\n", 0o644))
	}))
	resolved.PutSecret(manifest.Secret{
		ID: "env", Source: "secrets/env.age", Permissions: "0600",
		Target: manifest.Scalar(f.target(".env", "x\n", 0o600)),
	})

	report = f.checker().Check(resolved)
	require.True(t, report.Drifted, "any target not in sync drifts the whole report")
	require.Len(t, report.Results, 3)
	require.Equal(t, []string{"good", "bad", "env"},
		[]string{report.Results[0].AssetID, report.Results[1].AssetID, report.Results[2].AssetID},
		"results follow resolution order so the report is reproducible")
}

func TestNewWithoutALockfile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	c := New(f.h, nil)
	require.NotNil(t, c.Lockfile)
}
