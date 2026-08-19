package profile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeOverride(t *testing.T, repo, hostname, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "machine"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repo, "machine", hostname+".yaml"), []byte(body), 0o600))
}

// Phase 3: an override replaces an asset by ID and appends to package lists.
func TestApplyOverrides(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeOverride(t, repo, "laptop", `
assets:
  - id: zshrc
    source: assets/zshrc_laptop
    target: /tmp/zshrc
  - id: extra
    source: assets/extra
    target: /tmp/extra
secrets:
  - id: app_env
    source: secrets/app_env_laptop
    target: /tmp/.env
packages:
  apt: [tmux]
  docker: { images: ["postgres:16"] }
  node: { version: "20.11.0" }
`)
	m := load(t, fixture)
	r, err := Resolve(m, "work")
	require.NoError(t, err)
	require.NoError(t, ApplyOverrides(m, r, repo, "laptop"))

	require.Equal(t, "assets/zshrc_laptop", r.Assets["zshrc"].Source)
	require.Equal(t, "assets/extra", r.Assets["extra"].Source)
	require.Equal(t, "secrets/app_env_laptop", r.Secrets["app_env"].Source)
	require.Equal(t, []string{"git", "zsh", "tmux"}, r.Packages["apt"], "package lists append")
	require.Equal(t, []string{"redis:7", "postgres:16"}, r.DockerImages)
	require.Equal(t, "20.11.0", r.Node.Version, "node settings overwrite")
	require.Equal(t, ".nvmrc", r.Node.VersionFile)
}

// Phase 3: a missing override file is silent.
func TestMissingOverrideIsSilent(t *testing.T) {
	t.Parallel()
	m := load(t, fixture)
	r, err := Resolve(m, "base")
	require.NoError(t, err)
	require.NoError(t, ApplyOverrides(m, r, t.TempDir(), "nosuchhost"))
	require.Equal(t, []string{"zshrc", "gitconfig"}, r.AssetIDs())
}

// Phase 3: a malformed override errors.
func TestMalformedOverrideErrors(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"bad yaml":          "assets: [ unclosed",
		"unknown field":     "assetz: []",
		"invalid asset":     "assets:\n  - {id: a, source: /etc/passwd, target: /tmp/a}\n",
		"invalid secret":    "secrets:\n  - {id: s, source: s.age, target: /tmp/s, permissions: \"0644\"}\n",
		"asset plugin hook": "assets:\n  - {id: a, source: assets/a, target: /tmp/a, hooks: {post: [{plugin: notify}]}}\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo := t.TempDir()
			writeOverride(t, repo, "laptop", body)
			m := load(t, fixture)
			r, err := Resolve(m, "base")
			require.NoError(t, err)
			require.ErrorIs(t, ApplyOverrides(m, r, repo, "laptop"), ErrOverride)
		})
	}
}

func TestOverridesCanBeDisabled(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeOverride(t, repo, "laptop", "assets:\n  - {id: zshrc, source: assets/other, target: /tmp/zshrc}\n")

	m := load(t, "version: 2\nmachine_overrides: { enabled: false }\n"+fixture[len("\nversion: 2\n"):])
	r, err := Resolve(m, "base")
	require.NoError(t, err)
	require.NoError(t, ApplyOverrides(m, r, repo, "laptop"))
	require.Equal(t, "assets/zshrc", r.Assets["zshrc"].Source)

	path, err := OverridePath(m, repo, "laptop")
	require.NoError(t, err)
	require.Empty(t, path)
}

func TestOverridePathHonorsThePattern(t *testing.T) {
	t.Parallel()
	m := load(t, "version: 2\nmachine_overrides: { path: \"hosts/{hostname}.yml\" }\nprofiles: { base: {} }\n")
	path, err := OverridePath(m, "/repo", "laptop")
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/repo", "hosts", "laptop.yml"), path)
}

// The override path comes from the manifest, so it must not be able to reach outside the repo.
func TestOverridePathCannotEscapeTheRepo(t *testing.T) {
	t.Parallel()
	for _, pattern := range []string{"../../etc/{hostname}.yaml", "/etc/{hostname}.yaml"} {
		m := load(t, "version: 2\nmachine_overrides: { path: \""+pattern+"\" }\nprofiles: { base: {} }\n")
		_, err := OverridePath(m, "/repo", "laptop")
		require.ErrorIs(t, err, ErrOverride, pattern)
	}
}
