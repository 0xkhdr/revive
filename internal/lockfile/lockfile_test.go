package lockfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/manifest"
)

// Phase 8 INTEROP GATE: a lockfile written by the reference Python implementation is read
// without loss and rewritten equivalently.
//
// The fixture is genuine Python output from its own Lockfile model; only the absolute path
// prefix was replaced with {{ROOT}} so the file is relocatable.
func TestInteropPythonLockfile(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/python_manifest.lock")
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "manifest.lock")
	require.NoError(t, os.WriteFile(path, raw, 0o644))

	lf, err := Load(path)
	require.NoError(t, err)
	require.Len(t, lf.Entries, 2)

	// A scalar-target asset reads as a scalar.
	zshrc := lf.Entries["zshrc"]
	require.True(t, zshrc.TargetPath.IsScalar())
	require.Equal(t, []string{"{{ROOT}}/.zshrc"}, zshrc.TargetPath.Values)
	require.Equal(t, []string{"0644"}, zshrc.Permissions.Values)
	require.True(t, zshrc.MTime.IsScalar())
	require.InDelta(t, 1716000000.123456, zshrc.MTime.Values[0], 1e-6)
	require.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", zshrc.SHA256OfSource)

	// A multi-target asset reads as index-aligned arrays.
	appEnv := lf.Entries["app_env"]
	require.False(t, appEnv.TargetPath.IsScalar())
	require.Equal(t, []string{"{{ROOT}}/.env", "{{ROOT}}/.env.deploy"}, appEnv.TargetPath.Values)
	require.Equal(t, []string{"0600", "0600"}, appEnv.Permissions.Values)
	require.Equal(t, []float64{1716000001.5, 1716000002.5}, appEnv.MTime.Values)

	require.Equal(t, "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03",
		lf.RenderedChecksums["gitconfig"])

	// Rewriting it produces an equivalent document: nothing was dropped and no scalar became an
	// array. Key order differs — Go marshals maps sorted — so this is JSON equality, which is
	// what the criterion asks for.
	out, err := Marshal(lf)
	require.NoError(t, err)
	require.JSONEq(t, string(raw), string(out))
}

// Phase 8: the lockfile round-trips — scalar targets write scalars, list targets write
// index-aligned arrays.
func TestShapeIsPreservedOnWrite(t *testing.T) {
	t.Parallel()
	lf := New()
	lf.Entries["scalar"] = Entry{
		SHA256OfSource: "abc",
		TargetPath:     manifest.Scalar("/tmp/a"),
		Permissions:    manifest.Scalar("0644"),
		MTime:          ScalarFloat(1.5),
	}
	lf.Entries["list"] = Entry{
		SHA256OfSource: "def",
		TargetPath:     manifest.Slice("/tmp/a", "/tmp/b"),
		Permissions:    manifest.Slice("0600", "0600"),
		MTime:          SliceFloat(1.5, 2.5),
	}

	raw, err := Marshal(lf)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"target_path": "/tmp/a"`)
	require.Contains(t, string(raw), `"mtime": 1.5`)
	require.Contains(t, string(raw), `"target_path": [`)

	var back Lockfile
	require.NoError(t, json.Unmarshal(raw, &back))
	require.True(t, back.Entries["scalar"].TargetPath.IsScalar())
	require.False(t, back.Entries["list"].TargetPath.IsScalar())
	require.True(t, back.Entries["scalar"].MTime.IsScalar())
	require.False(t, back.Entries["list"].MTime.IsScalar())
}

func TestFloatOrSlice(t *testing.T) {
	t.Parallel()
	var f FloatOrSlice
	require.NoError(t, json.Unmarshal([]byte("1.5"), &f))
	require.True(t, f.IsScalar())
	require.Equal(t, []float64{1.5}, f.Values)

	require.NoError(t, json.Unmarshal([]byte("[1.5,2.5]"), &f))
	require.False(t, f.IsScalar())

	out, err := json.Marshal(SliceFloat(1.5))
	require.NoError(t, err)
	require.JSONEq(t, "[1.5]", string(out))

	require.Error(t, json.Unmarshal([]byte(`"not a number"`), &f))
}

func TestPathFor(t *testing.T) {
	t.Parallel()
	require.Equal(t, "/repo/manifest.lock", PathFor("/repo/manifest.yaml"))
	require.Equal(t, "/repo/manifest-build.lock", PathFor("/repo/manifest-build.yaml"))
	require.Equal(t, "/repo/manifest.lock", PathFor("/repo/manifest.yml"))
}

func TestLoadMissingIsEmpty(t *testing.T) {
	t.Parallel()
	lf, err := Load(filepath.Join(t.TempDir(), "absent.lock"))
	require.NoError(t, err)
	require.Empty(t, lf.Entries)
	require.NotNil(t, lf.RenderedChecksums)
}

// An unreadable lockfile is a warning and a replacement, never a fatal error: refusing to
// restore because a generated file is corrupt would strand the user.
func TestLoadOrEmptyReplacesACorruptLockfile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "manifest.lock")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

	_, err := Load(path)
	require.Error(t, err)

	lf, err := LoadOrEmpty(path)
	require.Error(t, err, "the caller still learns it was corrupt")
	require.NotNil(t, lf)
	require.Empty(t, lf.Entries, "and gets a usable empty lockfile anyway")
}

func TestLoadHandlesNullMaps(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "manifest.lock")
	require.NoError(t, os.WriteFile(path, []byte(`{"entries":null,"rendered_checksums":null}`), 0o644))

	lf, err := Load(path)
	require.NoError(t, err)
	require.NotNil(t, lf.Entries)
	require.NotNil(t, lf.RenderedChecksums)
}

func TestSaveIsAtomic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.lock")

	lf := New()
	lf.Entries["a"] = Entry{SHA256OfSource: "x", TargetPath: manifest.Scalar("/tmp/a"),
		Permissions: manifest.Scalar("0644"), MTime: ScalarFloat(1)}
	require.NoError(t, Save(path, lf))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temp file may survive")

	back, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, lf.Entries["a"].SHA256OfSource, back.Entries["a"].SHA256OfSource)
}

func TestMarshalNilMaps(t *testing.T) {
	t.Parallel()
	raw, err := Marshal(&Lockfile{})
	require.NoError(t, err)
	require.JSONEq(t, `{"entries":{},"rendered_checksums":{}}`, string(raw),
		"a nil map must serialize as {} rather than null, which Python would reject")
}

func TestSaveFailure(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	require.Error(t, Save(filepath.Join(dir, "manifest.lock"), New()))
}

func TestReadFailureIsNotAMissingFile(t *testing.T) {
	t.Parallel()
	_, err := Load(t.TempDir())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "reading lockfile"))
}
