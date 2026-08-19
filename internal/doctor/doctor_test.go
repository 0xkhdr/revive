package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/platform"
)

type fixture struct {
	t    *testing.T
	repo string
	home string
	d    *Doctor
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		t:    t,
		repo: filepath.Join(base, "repo"),
		home: filepath.Join(base, "home"),
	}
	require.NoError(t, os.MkdirAll(f.repo, 0o755))
	require.NoError(t, os.MkdirAll(f.home, 0o755))

	f.d = &Doctor{
		RepoDir:      f.repo,
		ManifestPath: filepath.Join(f.repo, "manifest.yaml"),
		Paths:        paths.New(filepath.Join(base, "rv-home")),
		Hostname:     "test-host",
		// Everything is installed unless a test says otherwise.
		Platform: &platform.Detector{
			GOOS:     "linux",
			LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		},
	}
	return f
}

func (f *fixture) write(name, content string) string {
	f.t.Helper()
	p := filepath.Join(f.repo, name)
	require.NoError(f.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(f.t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

// issues returns the messages for one category.
func issues(report *Report, category string) []string {
	var out []string
	for _, issue := range report.Issues {
		if issue.Category == category {
			out = append(out, issue.Message)
		}
	}
	return out
}

func severityOf(report *Report, category string) Severity {
	for _, issue := range report.Issues {
		if issue.Category == category {
			return issue.Severity
		}
	}
	return ""
}

// Phase 10: doctor produces every category in the table.
func TestEveryCategory(t *testing.T) {
	t.Parallel()

	t.Run("manifest missing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		report := f.d.Run()
		require.False(t, report.Healthy)
		require.Equal(t, Critical, severityOf(report, CategoryManifest))
		require.Contains(t, issues(report, CategoryManifest)[0], "not found")
	})

	t.Run("manifest invalid", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.write("manifest.yaml", "version: 3\n")
		report := f.d.Run()
		require.False(t, report.Healthy)
		require.Equal(t, Critical, severityOf(report, CategoryManifest))
		require.Contains(t, issues(report, CategoryManifest)[0], "validate")
	})

	t.Run("lockfile missing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.write("manifest.yaml", "version: 2\nprofiles: {base: {}}\n")
		report := f.d.Run()
		require.True(t, report.Healthy, "a missing lockfile is a warning, not a blocker")
		require.Equal(t, Warning, severityOf(report, CategoryLockfile))
	})

	t.Run("lockfile unparseable", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.write("manifest.yaml", "version: 2\nprofiles: {base: {}}\n")
		f.write("manifest.lock", "{not json")
		report := f.d.Run()
		require.Equal(t, Warning, severityOf(report, CategoryLockfile))
		require.Contains(t, issues(report, CategoryLockfile)[0], "could not be read")
	})

	t.Run("profile not defined", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.write("manifest.yaml", "version: 2\nprofiles: {base: {}}\n")
		f.d.Profiles = []string{"nope"}
		report := f.d.Run()
		require.False(t, report.Healthy)
		require.Equal(t, Critical, severityOf(report, CategoryProfile))
	})

	t.Run("profile resolution fails", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.write("manifest.yaml", "version: 2\nprofiles: {a: {extends: [b]}, b: {extends: [a]}}\n")
		f.d.Profiles = []string{"a"}
		report := f.d.Run()
		require.False(t, report.Healthy)
		require.Equal(t, Critical, severityOf(report, CategoryProfileResolution))
		require.Contains(t, issues(report, CategoryProfileResolution)[0], "a -> b -> a")
	})

	t.Run("system missing a package manager", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.write("manifest.yaml", "version: 2\npackages: {brew: [fzf]}\nprofiles: {base: {packages: [brew]}}\n")
		f.d.Platform = &platform.Detector{
			GOOS:     "linux",
			LookPath: func(string) (string, error) { return "", os.ErrNotExist },
		}
		report := f.d.Run()
		require.True(t, report.Healthy, "a missing package manager is a warning")
		require.Equal(t, Warning, severityOf(report, CategorySystem))
		require.Contains(t, issues(report, CategorySystem)[0], "brew")
	})

	t.Run("asset source missing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.write("manifest.yaml",
			"version: 2\nassets: [{id: zshrc, source: assets/zshrc, target: /tmp/x}]\nprofiles: {base: {assets: [zshrc]}}\n")
		report := f.d.Run()
		require.False(t, report.Healthy)
		require.Equal(t, Critical, severityOf(report, CategoryAssetSource))
		require.Contains(t, issues(report, CategoryAssetSource)[0], "assets/zshrc")
	})

	t.Run("asset target parent missing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.write("assets/zshrc", "x")
		f.write("manifest.yaml", "version: 2\nassets: [{id: zshrc, source: assets/zshrc, target: /nonexistent/dir/x}]\nprofiles: {base: {assets: [zshrc]}}\n")
		report := f.d.Run()
		require.True(t, report.Healthy)
		require.Equal(t, Warning, severityOf(report, CategoryAssetTarget))
		require.Contains(t, issues(report, CategoryAssetTarget)[0], "/nonexistent/dir")
	})

	t.Run("asset target interpolation fails", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.write("assets/zshrc", "x")
		f.write("manifest.yaml", "version: 2\nassets: [{id: zshrc, source: assets/zshrc, target: \"${RV_DOCTOR_NEVER_SET}/x\"}]\nprofiles: {base: {assets: [zshrc]}}\n")
		report := f.d.Run()
		require.Equal(t, Warning, severityOf(report, CategoryAssetTarget))
		require.Contains(t, issues(report, CategoryAssetTarget)[0], "RV_DOCTOR_NEVER_SET")
	})

	t.Run("secret source missing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.write("manifest.yaml",
			"version: 2\nsecrets: [{id: env, source: secrets/env.age, target: /tmp/.env}]\nprofiles: {base: {secrets: [env]}}\n")
		report := f.d.Run()
		require.False(t, report.Healthy)
		require.Equal(t, Critical, severityOf(report, CategorySecretSource))
	})

	t.Run("hook command not on PATH", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.write("assets/conf", "x")
		f.write("manifest.yaml", `
version: 2
assets:
  - id: conf
    type: copy
    source: assets/conf
    target: /tmp/conf
    hooks:
      post:
        - command: "rv-not-a-real-binary --flag"
profiles: {base: {assets: [conf]}}
`)
		f.d.Platform = &platform.Detector{
			GOOS: "linux",
			LookPath: func(name string) (string, error) {
				if name == "rv-not-a-real-binary" {
					return "", os.ErrNotExist
				}
				return "/usr/bin/" + name, nil
			},
		}
		report := f.d.Run()
		require.Equal(t, Warning, severityOf(report, CategoryHookCommand))
		require.Contains(t, issues(report, CategoryHookCommand)[0], "rv-not-a-real-binary")
		require.Contains(t, issues(report, CategoryHookCommand)[0], "mid-transaction")
	})
}

// Phase 10: doctor exits unhealthy when a critical issue exists, and healthy otherwise.
func TestHealthyWorkspace(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write("assets/zshrc", "export EDITOR=vim\n")
	f.write("manifest.yaml", "version: 2\nassets: [{id: zshrc, source: assets/zshrc, target: "+
		filepath.Join(f.home, ".zshrc")+"}]\nprofiles: {base: {assets: [zshrc]}}\n")
	f.write("manifest.lock", `{"entries":{},"rendered_checksums":{}}`)

	report := f.d.Run()
	require.True(t, report.Healthy, "%v", report.Issues)
	require.Empty(t, report.Issues)
	require.Positive(t, report.ChecksRun)
}

// Phase 10: doctor mutates nothing.
func TestDoctorMutatesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write("assets/zshrc", "x")
	f.write("manifest.yaml", "version: 2\nassets: [{id: zshrc, source: assets/zshrc, target: "+
		filepath.Join(f.home, ".zshrc")+"}]\nprofiles: {base: {assets: [zshrc]}}\n")

	before := snapshot(t, f.repo)
	beforeHome := snapshot(t, f.home)
	beforeConfig := snapshot(t, f.d.Paths.Home)

	f.d.Run()

	require.Equal(t, before, snapshot(t, f.repo), "the repository must be untouched")
	require.Equal(t, beforeHome, snapshot(t, f.home), "no target may be created")
	require.Equal(t, beforeConfig, snapshot(t, f.d.Paths.Home), "no journal, lock or cache may appear")
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			//nolint:nilerr // A snapshot of a tree that may not exist yet: an unreadable entry
			// is simply absent from the comparison, and both sides are taken the same way.
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			out[rel] = "dir"
			return nil
		}
		content, _ := os.ReadFile(path)
		out[rel] = string(content)
		return nil
	})
	return out
}

// With no profile named, doctor audits every profile the manifest declares.
func TestNoProfileChecksThemAll(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write("assets/good", "x")
	f.write("manifest.yaml", `
version: 2
assets:
  - {id: good, source: assets/good, target: /tmp/good}
  - {id: bad, source: assets/missing, target: /tmp/bad}
profiles:
  base: {assets: [good]}
  work: {assets: [bad]}
`)
	report := f.d.Run()
	require.False(t, report.Healthy)
	require.Contains(t, issues(report, CategoryAssetSource)[0], "assets/missing")
}

func TestManifestWithNoProfiles(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write("manifest.yaml", "version: 2\n")
	report := f.d.Run()
	require.True(t, report.Healthy)
	require.Equal(t, Warning, severityOf(report, CategoryProfile))
}

func TestMalformedMachineOverrideIsCritical(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write("manifest.yaml", "version: 2\nprofiles: {base: {}}\n")
	f.write("machine/test-host.yaml", "assetz: []\n")

	report := f.d.Run()
	require.False(t, report.Healthy)
	require.Equal(t, Critical, severityOf(report, CategoryProfileResolution))
}

func TestDoctorBuildsADefaultHandler(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.d.Handler = nil
	f.write("assets/conf", "x")
	f.write("manifest.yaml", "version: 2\nassets: [{id: conf, source: assets/conf, target: "+
		filepath.Join(f.home, "conf")+"}]\nprofiles: {base: {assets: [conf]}}\n")
	require.True(t, f.d.Run().Healthy)
}

func TestReportShape(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	report := f.d.Run()
	require.False(t, report.Healthy)
	require.Positive(t, report.ChecksRun)
	require.NotEmpty(t, report.Issues)
	require.NotEmpty(t, report.Issues[0].Category)
	require.NotEmpty(t, report.Issues[0].Severity)
	require.NotEmpty(t, report.Issues[0].Message)
}
