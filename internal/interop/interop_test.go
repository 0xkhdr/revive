// Package interop holds the cross-implementation gates. It lives in its own package because a
// full-workspace test needs both the restore engine and the status engine, and status imports
// engine — putting the test in either one would be an import cycle.
package interop

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/engine"
	"github.com/0xkhdr/revive/internal/lockfile"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/profile"
	"github.com/0xkhdr/revive/internal/scrub"
	"github.com/0xkhdr/revive/internal/status"
)

const workspaceFixture = "testdata/python_workspace"

// fakeRunner answers provider probes without touching the machine. The fixture declares no
// packages, so nothing should reach it.
type fakeRunner struct{}

func (fakeRunner) Run(context.Context, []string) ([]byte, error) { return nil, nil }
func (fakeRunner) LookPath(string) (string, bool)                { return "", false }

// pythonTarget is one file the Python restore produced.
type pythonTarget struct {
	Rel     string  `json:"rel"`
	Mode    string  `json:"mode"`
	MTime   float64 `json:"mtime"`
	Symlink bool    `json:"symlink"`
	Link    string  `json:"link"`
	Content string  `json:"content"`
}

// Phase 14 INTEROP GATE: a workspace restored by the Python implementation is then managed by the
// Go one — status reports in-sync, restore is a no-op, and the lockfile is preserved.
//
// The fixture is a real Python restore captured immediately afterwards; see its README.
func TestInteropPythonRestoredWorkspace(t *testing.T) {
	// Not parallel: the workspace's ${APP_DIR} comes from the process environment, the same way
	// it did for the Python run that produced the fixture.
	root := t.TempDir()
	repo, home := filepath.Join(root, "repo"), filepath.Join(root, "home")
	identity := stageWorkspace(t, root)

	// The workspace's .env supplies ${APP_DIR}, exactly as it did for the Python run.
	t.Setenv("APP_DIR", filepath.Join(home, "app"))

	manifestPath := filepath.Join(repo, "manifest.yaml")
	m, err := manifest.Load(manifestPath)
	require.NoError(t, err, "the Go loader must accept a manifest the Python one restored from")

	resolved, err := profile.Resolve(m, "base")
	require.NoError(t, err)

	lf, err := lockfile.Load(lockfile.PathFor(manifestPath))
	require.NoError(t, err, "the Python-written lockfile must load")
	require.Len(t, lf.Entries, 4)

	// 1. status reports in-sync.
	h := engine.NewHandler(repo, paths.New(filepath.Join(root, "rv-home")))
	h.Identity = identity
	h.Hostname = "test-host"
	report := status.New(h, lf).Check(resolved)
	for _, result := range report.Results {
		require.Equal(t, status.InSync, result.Status,
			"asset %q at %s: %s", result.AssetID, result.Target, result.Detail)
	}
	require.False(t, report.Drifted, "a freshly Python-restored machine has no drift")

	// 2. restore is a no-op — on the default `prompt` strategy, with nobody to ask.
	before := snapshotTargets(t, home)
	beforeLock, err := os.ReadFile(lockfile.PathFor(manifestPath))
	require.NoError(t, err)

	r := &engine.Restorer{
		Paths:    paths.New(filepath.Join(root, "rv-home")),
		Hostname: "test-host",
		Runner:   fakeRunner{},
		Scrubber: scrub.New(),
	}
	res, err := r.Restore(context.Background(), engine.Options{
		RepoDir:      repo,
		ManifestPath: manifestPath,
		Profiles:     []string{"base"},
		Identity:     identity,
	})
	require.NoError(t, err, "rv must recognize the targets the Python implementation wrote as its own")
	require.Equal(t, 3, res.Assets)
	require.Equal(t, 1, res.Secrets)
	require.Empty(t, res.Skipped)

	require.Equal(t, before, snapshotTargets(t, home),
		"content, modes and symlink destinations must all be unchanged")

	// 3. the lockfile is preserved: same entries, same shapes, same source checksums.
	afterLock, err := os.ReadFile(lockfile.PathFor(manifestPath))
	require.NoError(t, err)
	requireLockfilePreserved(t, beforeLock, afterLock)

	// And status still reports in-sync afterwards.
	report = status.New(h, mustLoadLock(t, manifestPath)).Check(resolved)
	require.False(t, report.Drifted)
}

// requireLockfilePreserved asserts the Go rewrite kept everything the Python lockfile recorded,
// mtimes aside — those necessarily move when a file is rewritten.
func requireLockfilePreserved(t *testing.T, beforeRaw, afterRaw []byte) {
	t.Helper()
	var before, after lockfile.Lockfile
	require.NoError(t, json.Unmarshal(beforeRaw, &before))
	require.NoError(t, json.Unmarshal(afterRaw, &after))

	require.Len(t, after.Entries, len(before.Entries))
	for id, want := range before.Entries {
		got, ok := after.Entries[id]
		require.True(t, ok, "entry %q was dropped", id)
		require.Equal(t, want.SHA256OfSource, got.SHA256OfSource,
			"entry %q: the Go source checksum must match the Python one", id)
		require.Equal(t, want.TargetPath.Values, got.TargetPath.Values, "entry %q targets", id)
		require.Equal(t, want.TargetPath.IsScalar(), got.TargetPath.IsScalar(),
			"entry %q: a scalar target must stay scalar", id)
		require.Equal(t, want.Permissions.Values, got.Permissions.Values, "entry %q permissions", id)
		require.Len(t, got.MTime.Values, len(want.MTime.Values), "entry %q mtime shape", id)
		require.Equal(t, want.MTime.IsScalar(), got.MTime.IsScalar(), "entry %q mtime shape", id)
	}
	require.Equal(t, before.RenderedChecksums, after.RenderedChecksums)
}

// stageWorkspace copies the fixture into root, substituting {{ROOT}} and reproducing every target
// the Python restore produced, modification times included.
func stageWorkspace(t *testing.T, root string) string {
	t.Helper()
	repo, home := filepath.Join(root, "repo"), filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))

	src := filepath.Join(workspaceFixture, "repo")
	require.NoError(t, filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(repo, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(out, []byte(strings.ReplaceAll(string(content), "{{ROOT}}", root)), 0o644)
	}))

	identityRaw, err := os.ReadFile(filepath.Join(workspaceFixture, "identity.txt"))
	require.NoError(t, err)
	identity := filepath.Join(root, "identity.txt")
	require.NoError(t, os.WriteFile(identity, identityRaw, 0o600))

	stateRaw, err := os.ReadFile(filepath.Join(workspaceFixture, "state.json"))
	require.NoError(t, err)
	var state struct {
		Targets []pythonTarget `json:"targets"`
	}
	require.NoError(t, json.Unmarshal(stateRaw, &state))

	for _, target := range state.Targets {
		path := filepath.Join(home, target.Rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))

		if target.Symlink {
			require.NoError(t, os.Symlink(strings.ReplaceAll(target.Link, "{{ROOT}}", root), path))
			continue
		}
		mode, err := strconv.ParseUint(strings.TrimPrefix(target.Mode, "0o"), 8, 32)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, []byte(target.Content), fs.FileMode(mode)))
		require.NoError(t, os.Chmod(path, fs.FileMode(mode)))

		// Git does not preserve modification times, so they are restored from the capture.
		// Conflict resolution compares them against what the lockfile recorded, and the two
		// were captured independently.
		when := time.Unix(0, int64(target.MTime*float64(time.Second)))
		require.NoError(t, os.Chtimes(path, when, when))
	}
	return identity
}

// snapshotTargets records every managed file with its mode and content.
func snapshotTargets(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		fi, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			out[rel] = "symlink:" + link
			return err
		case d.IsDir():
			return nil
		default:
			content, err := os.ReadFile(path)
			out[rel] = fi.Mode().Perm().String() + ":" + string(content)
			return err
		}
	}))
	return out
}

func mustLoadLock(t *testing.T, manifestPath string) *lockfile.Lockfile {
	t.Helper()
	lf, err := lockfile.Load(lockfile.PathFor(manifestPath))
	require.NoError(t, err)
	return lf
}
