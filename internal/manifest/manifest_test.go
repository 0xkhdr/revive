package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 2 golden test: every fixture under testdata/accept must load, every fixture under
// testdata/reject must fail. The Python suite's fixtures are inline dicts rather than files, so
// the cases from reference/tests/test_models.py are ported into testdata/ here.
func TestGoldenFixtures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dir    string
		accept bool
	}{{"accept", true}, {"reject", false}} {
		entries, err := os.ReadDir(filepath.Join("testdata", tc.dir))
		require.NoError(t, err)
		require.NotEmpty(t, entries)

		for _, e := range entries {
			t.Run(tc.dir+"/"+e.Name(), func(t *testing.T) {
				t.Parallel()
				_, err := Load(filepath.Join("testdata", tc.dir, e.Name()))
				if tc.accept {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
			})
		}
	}
}

// The full worked example loads and validates.
func TestWorkedExampleLoads(t *testing.T) {
	t.Parallel()
	m, err := Load("testdata/accept/worked_example.yaml")
	require.NoError(t, err)

	require.Equal(t, 2, m.SchemaVersion())
	require.Len(t, m.Assets, 3)
	require.Equal(t, TypeSymlink, m.Assets[0].Type)
	require.Equal(t, ConflictPrompt, m.Assets[0].ConflictStrategy)
	require.Equal(t, "dev@example.com", m.Assets[2].TemplateVars["email"])
	require.Equal(t, []string{"git", "zsh", "ripgrep", "jq", "docker-ce"}, m.Packages.Apt)
	require.Equal(t, ".nvmrc", m.Packages.Node.VersionFile)
	require.Equal(t, []string{"base"}, m.Profiles["work"].Extends)

	// Scalar and list targets both land in StringOrSlice.
	require.True(t, m.Assets[0].Target.IsScalar())
	require.Equal(t, []string{"${USER_HOME}/.zshrc"}, m.Assets[0].Target.Values)
	require.False(t, m.Assets[1].Target.IsScalar())
	require.Len(t, m.Assets[1].Target.Values, 2)

	// Secrets are forced to type secret, encrypted, 0600.
	require.Len(t, m.Secrets, 1)
	require.Equal(t, TypeSecret, m.Secrets[0].Type)
	require.True(t, m.Secrets[0].Encrypted)
	require.Equal(t, "0600", m.Secrets[0].Permissions)
}

func TestDefaultsAreApplied(t *testing.T) {
	t.Parallel()
	m, err := Parse([]byte("profiles:\n  base: {}\n"))
	require.NoError(t, err)
	require.Equal(t, DefaultVersion, m.SchemaVersion())
	require.True(t, m.OverridesEnabled())
	require.Equal(t, "machine/{hostname}.yaml", m.MachineOverrides.Path)
	maxCount, maxAge := m.Retention()
	require.Equal(t, 10, maxCount)
	require.Equal(t, 30, maxAge)

	m, err = Load("testdata/accept/hooks_and_inline.yaml")
	require.NoError(t, err)
	require.False(t, m.OverridesEnabled(), "an explicit `enabled: false` must survive defaulting")
	maxCount, maxAge = m.Retention()
	require.Equal(t, 3, maxCount)
	require.Equal(t, 7, maxAge)
	require.Equal(t, TypeCopy, m.Assets[0].Type)
	require.Equal(t, "root", *m.Assets[0].Owner)
	require.Len(t, m.Assets[0].Hooks.Pre, 1)
	require.Equal(t, "mkdir -p /opt/app/conf", m.Assets[0].Hooks.Pre[0].Command)

	// Inline profile definitions parse as objects, ID references as strings.
	base := m.Profiles["base"]
	require.Equal(t, "app", base.Assets[0].ID)
	require.Nil(t, base.Assets[0].Inline)
	require.NotNil(t, base.Assets[1].Inline)
	require.Equal(t, "inline_asset", base.Assets[1].Inline.ID)
	require.NotNil(t, base.Secrets[0].Inline)
	require.Equal(t, "inline_secret", base.Secrets[0].Inline.ID)
}

// Phase 2: version 3 returns ErrUnsupportedSchemaVersion naming versions 1 and 2.
func TestUnsupportedVersion(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte("version: 3\n"))
	require.ErrorIs(t, err, ErrUnsupportedSchemaVersion)
	require.Contains(t, err.Error(), "[1 2]")

	// The raw pre-check runs before struct binding, so a version-3 document whose other fields
	// would also fail still reports the version error.
	_, err = Parse([]byte("version: 3\nassets: [{id: a, source: /etc/passwd, target: /tmp/x}]\n"))
	require.ErrorIs(t, err, ErrUnsupportedSchemaVersion)

	// The second check catches a struct built in code rather than loaded from YAML.
	v := 3
	m := &Manifest{Version: &v}
	require.ErrorIs(t, m.Validate(), ErrUnsupportedSchemaVersion)
}

// Phase 2: `type: secret` forces `encrypted: true` even when the YAML says false.
func TestSecretTypeForcesEncrypted(t *testing.T) {
	t.Parallel()
	m, err := Parse([]byte(`
version: 2
assets:
  - id: a
    type: secret
    source: secrets/a.age
    target: /tmp/a
    encrypted: false
secrets:
  - id: s
    type: symlink
    source: secrets/s.age
    target: /tmp/s
    encrypted: false
`))
	require.NoError(t, err)
	require.True(t, m.Assets[0].Encrypted)
	require.Equal(t, TypeSecret, m.Secrets[0].Type, "a secret's type is forced, whatever the YAML says")
	require.True(t, m.Secrets[0].Encrypted)
	require.Equal(t, "0600", m.Secrets[0].Permissions, "a secret with no permissions defaults to 0600")
}

func TestParsePermissions(t *testing.T) {
	t.Parallel()
	mode, err := ParsePermissions("0644")
	require.NoError(t, err)
	require.Equal(t, uint32(0o644), mode)

	mode, err = ParsePermissions("0600")
	require.NoError(t, err)
	require.Equal(t, uint32(0o600), mode)

	for _, bad := range []string{"644", "0o644", "rwx", "", "00644", "0999", "0-44"} {
		_, err := ParsePermissions(bad)
		require.Error(t, err, "%q must be rejected", bad)
	}
}

func TestValidationErrorsAreAggregatedAndTyped(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte(`
version: 2
assets:
  - id: a
    source: ../escape
    target: /tmp/a
    permissions: "644"
`))
	require.ErrorIs(t, err, ErrValidation)
	require.Contains(t, err.Error(), "must be relative")
	require.Contains(t, err.Error(), "4-digit octal")
}

func TestSecretPermissionFloor(t *testing.T) {
	t.Parallel()
	for _, perms := range []string{"0600", "0700", "0400"} {
		_, err := Parse([]byte("version: 2\nsecrets: [{id: s, source: s.age, target: /tmp/s, permissions: \"" + perms + "\"}]\n"))
		require.NoError(t, err, perms)
	}
	for _, perms := range []string{"0640", "0604", "0666", "0644"} {
		_, err := Parse([]byte("version: 2\nsecrets: [{id: s, source: s.age, target: /tmp/s, permissions: \"" + perms + "\"}]\n"))
		require.ErrorIs(t, err, ErrValidation, perms)
	}
}

func TestStringOrSliceRoundTrip(t *testing.T) {
	t.Parallel()
	var scalar, slice StringOrSlice
	require.NoError(t, json.Unmarshal([]byte(`"/tmp/a"`), &scalar))
	require.NoError(t, json.Unmarshal([]byte(`["/tmp/a","/tmp/b"]`), &slice))
	require.True(t, scalar.IsScalar())
	require.False(t, slice.IsScalar())

	// The lockfile round trip must preserve the shape it read.
	out, err := json.Marshal(scalar)
	require.NoError(t, err)
	require.JSONEq(t, `"/tmp/a"`, string(out))
	out, err = json.Marshal(slice)
	require.NoError(t, err)
	require.JSONEq(t, `["/tmp/a","/tmp/b"]`, string(out))

	out, err = json.Marshal(Scalar("/x"))
	require.NoError(t, err)
	require.JSONEq(t, `"/x"`, string(out))
	out, err = json.Marshal(Slice("/x"))
	require.NoError(t, err)
	require.JSONEq(t, `["/x"]`, string(out))

	require.Error(t, json.Unmarshal([]byte(`42`), &scalar))
}

func TestPackagesList(t *testing.T) {
	t.Parallel()
	p := &Packages{Apt: []string{"git"}, Brew: []string{"jq"}}
	got, ok := p.List("apt")
	require.True(t, ok)
	require.Equal(t, []string{"git"}, got)
	_, ok = p.List("docker")
	require.False(t, ok, "docker is not a flat name list")
	_, ok = p.List("gem")
	require.False(t, ok)
	for _, name := range ListNames {
		_, ok := p.List(name)
		require.True(t, ok, name)
	}
}

func TestSecretAsAsset(t *testing.T) {
	t.Parallel()
	s := Secret{ID: "s", Source: "secrets/s.age", Target: Scalar("/tmp/s"), Permissions: "0600"}
	a := s.Asset()
	require.Equal(t, TypeSecret, a.Type)
	require.True(t, a.Encrypted)
	require.Equal(t, "0600", *a.Permissions)
	require.Equal(t, ConflictPrompt, a.ConflictStrategy,
		"a secret carries no strategy of its own, so it gets the same default an asset does")
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()
	_, err := Load(filepath.Join(t.TempDir(), "manifest.yaml"))
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAssetTypeAndConflictStrategyValidity(t *testing.T) {
	t.Parallel()
	for _, ty := range []AssetType{TypeSymlink, TypeCopy, TypeTemplate, TypeSecret} {
		require.True(t, ty.Valid(), string(ty))
	}
	require.False(t, AssetType("hardlink").Valid())
	for _, s := range []ConflictStrategy{ConflictPrompt, ConflictOverwrite, ConflictSkip, ConflictAbort} {
		require.True(t, s.Valid(), string(s))
	}
	require.False(t, ConflictStrategy("ask").Valid())
}

func TestProfileRefRoundTrip(t *testing.T) {
	t.Parallel()
	var ref ProfileRef[Asset]
	require.NoError(t, json.Unmarshal([]byte(`"zshrc"`), &ref))
	out, err := json.Marshal(ref)
	require.NoError(t, err)
	require.JSONEq(t, `"zshrc"`, string(out))

	require.NoError(t, json.Unmarshal([]byte(`{"id":"inline","source":"a","target":"/tmp/a"}`), &ref))
	require.NotNil(t, ref.Inline)
	out, err = json.Marshal(ref)
	require.NoError(t, err)
	require.Contains(t, string(out), `"inline"`)

	require.Error(t, json.Unmarshal([]byte(`42`), &ref))
}

func TestEmptyTargetEntryIsRejected(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte("version: 2\nassets: [{id: a, source: assets/a, target: [\"/tmp/a\", \"  \"]}]\n"))
	require.ErrorIs(t, err, ErrValidation)
	require.Contains(t, err.Error(), "must not be empty")
}

func TestMissingIDIsRejected(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte("version: 2\nassets: [{source: assets/a, target: /tmp/a}]\n"))
	require.ErrorIs(t, err, ErrValidation)
	_, err = Parse([]byte("version: 2\nsecrets: [{source: s.age, target: /tmp/s}]\n"))
	require.ErrorIs(t, err, ErrValidation)
}

func TestEmptyHookCommandIsRejected(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte("version: 2\nassets: [{id: a, source: assets/a, target: /tmp/a, hooks: {pre: [{}]}}]\n"))
	require.ErrorIs(t, err, ErrValidation)
	require.Contains(t, err.Error(), "must set `command`")
}

func TestInlineProfileDefinitionsAreValidated(t *testing.T) {
	t.Parallel()
	_, err := Parse([]byte("version: 2\nprofiles:\n  base:\n    assets: [{id: a, source: /etc/passwd, target: /tmp/a}]\n"))
	require.ErrorIs(t, err, ErrValidation)
	require.Contains(t, err.Error(), `profile "base"`)

	_, err = Parse([]byte("version: 2\nprofiles:\n  base:\n    secrets: [{id: s, source: s.age, target: /tmp/s, permissions: \"0666\"}]\n"))
	require.ErrorIs(t, err, ErrValidation)
	require.Contains(t, err.Error(), `profile "base"`)
}

func TestLoadOnADirectory(t *testing.T) {
	t.Parallel()
	_, err := Load(t.TempDir())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNotFound)
}
