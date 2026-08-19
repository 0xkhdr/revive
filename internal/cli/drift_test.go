package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/doctor"
	"github.com/0xkhdr/revive/internal/platform"
	"github.com/0xkhdr/revive/internal/scrub"
	"github.com/0xkhdr/revive/internal/status"
)

// driftWorkspace lays out a repo with one in-sync asset and one drifted one.
func driftWorkspace(t *testing.T, h *harness) (inSync, drifted string) {
	t.Helper()
	inSync = filepath.Join(h.work, "targets", "same.conf")
	drifted = filepath.Join(h.work, "targets", "changed.conf")
	require.NoError(t, os.MkdirAll(filepath.Dir(inSync), 0o755))

	h.write("assets/same.conf", "identical\n")
	h.write("assets/changed.conf", "from the repo\nsecond line\n")
	require.NoError(t, os.WriteFile(inSync, []byte("identical\n"), 0o644))
	require.NoError(t, os.WriteFile(drifted, []byte("edited by hand\nsecond line\n"), 0o644))

	h.write("manifest.yaml", fmt.Sprintf(`
version: 2
assets:
  - {id: same, type: copy, source: assets/same.conf, target: %s}
  - {id: changed, type: copy, source: assets/changed.conf, target: %s}
profiles: {base: {assets: [same, changed]}}
`, inSync, drifted))
	return inSync, drifted
}

func TestStatusCommand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	driftWorkspace(t, h)

	out, err := h.run("status", "-p", "base")
	require.NoError(t, err)
	require.Contains(t, out, "in_sync")
	require.Contains(t, out, "modified")
	require.Contains(t, out, "drift detected")
}

func TestStatusJSON(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	driftWorkspace(t, h)

	out, err := h.run("status", "-p", "base", "--json")
	require.NoError(t, err)

	var report status.Report
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.True(t, report.Drifted)
	require.Len(t, report.Results, 2)
	require.Equal(t, status.InSync, report.Results[0].Status)
	require.Equal(t, status.Modified, report.Results[1].Status)
}

func TestStatusInSyncWorkspace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := filepath.Join(h.work, "conf")
	h.write("assets/conf", "content\n")
	require.NoError(t, os.WriteFile(target, []byte("content\n"), 0o644))
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	out, err := h.run("status", "-p", "base")
	require.NoError(t, err)
	require.Contains(t, out, "everything is in sync")
}

func TestStatusWithoutAProfile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("manifest.yaml", "version: 2\nprofiles: {base: {}}\n")
	_, err := h.run("status")
	require.ErrorIs(t, err, ErrUsage)
}

// Phase 10: restore --preview runs the status engine and mutates nothing.
func TestRestorePreview(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	inSync, drifted := driftWorkspace(t, h)

	out, err := h.run("restore", "base", "--preview")
	require.NoError(t, err)
	require.Contains(t, out, "modified")
	require.Contains(t, out, "drift detected")

	// --preview reports the current difference; it must not restore anything.
	got, err := os.ReadFile(drifted)
	require.NoError(t, err)
	require.Equal(t, "edited by hand\nsecond line\n", string(got))
	require.FileExists(t, inSync)
	require.NoFileExists(t, filepath.Join(h.work, "manifest.lock"))
}

func TestDiffCommand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	driftWorkspace(t, h)

	out, err := h.run("diff", "-p", "base")
	require.NoError(t, err)
	require.Contains(t, out, "changed")
	require.Contains(t, out, "from the repo")
	require.Contains(t, out, "edited by hand")
	require.NotContains(t, out, "identical", "an in-sync asset has nothing to diff")

	out, err = h.run("diff", "-p", "base", "--unified")
	require.NoError(t, err)
	require.Contains(t, out, "- from the repo")
	require.Contains(t, out, "+ edited by hand")
	require.Contains(t, out, "  second line")
}

func TestDiffWithNoChanges(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	target := filepath.Join(h.work, "conf")
	h.write("assets/conf", "content\n")
	require.NoError(t, os.WriteFile(target, []byte("content\n"), 0o644))
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	out, err := h.run("diff", "-p", "base")
	require.NoError(t, err)
	require.Contains(t, out, "no differences to show")
}

// A symlink asset that drifted has nothing to diff, and that must not be an error.
func TestDiffSkipsSymlinks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	source := h.write("assets/conf", "content\n")
	other := h.write("assets/other", "other\n")
	link := filepath.Join(h.work, "link")
	require.NoError(t, os.Symlink(other, link))

	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: symlink, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		link))
	_ = source

	out, err := h.run("diff", "-p", "base")
	require.NoError(t, err)
	require.Contains(t, out, "no differences to show")
}

func TestDiffOutputIsScrubbed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.env.Scrubber = scrub.New()
	h.env.Scrubber.RegisterSecret("hunter2secret")

	target := filepath.Join(h.work, "conf")
	h.write("assets/conf", "TOKEN=hunter2secret\n")
	require.NoError(t, os.WriteFile(target, []byte("TOKEN=changed\n"), 0o644))
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	out, err := h.run("diff", "-p", "base", "--unified")
	require.NoError(t, err)
	require.NotContains(t, out, "hunter2secret")
	require.Contains(t, out, scrub.Redacted)
}

// Phase 10: doctor exits 1 when a critical issue exists.
func TestDoctorCommand(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.env.Platform = &platform.Detector{
		GOOS:     "linux",
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
	}
	h.write("manifest.yaml",
		"version: 2\nassets: [{id: zshrc, source: assets/missing, target: /tmp/x}]\nprofiles: {base: {assets: [zshrc]}}\n")

	out, err := h.run("doctor")
	require.ErrorIs(t, err, doctor.ErrUnhealthy)
	require.Equal(t, 1, ExitCode(err), "an unhealthy doctor is a configuration problem, not a crash")
	require.Contains(t, out, "asset_source")
	require.Contains(t, out, "critical")
}

func TestDoctorHealthyExitsZero(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.env.Platform = &platform.Detector{
		GOOS:     "linux",
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
	}
	target := filepath.Join(h.work, "conf")
	h.write("assets/conf", "content\n")
	h.write("manifest.lock", `{"entries":{},"rendered_checksums":{}}`)
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: conf, source: assets/conf, target: %s}]\nprofiles: {base: {assets: [conf]}}\n",
		target))

	out, err := h.run("doctor")
	require.NoError(t, err)
	require.Contains(t, out, "no issues found")
}

func TestDoctorJSON(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("manifest.yaml", "version: 3\n")

	out, err := h.run("doctor", "--json")
	require.ErrorIs(t, err, doctor.ErrUnhealthy)

	var report doctor.Report
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.False(t, report.Healthy)
	require.Positive(t, report.ChecksRun)
	require.NotEmpty(t, report.Issues)
	require.Equal(t, doctor.Critical, report.Issues[0].Severity)
}

// The doctor exit code is what makes it usable as a CI gate.
func TestDoctorIsACIGate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.write("assets/t.tmpl", "{% if x %}jinja{% endif %}\n")
	h.write("manifest.yaml", fmt.Sprintf(
		"version: 2\nassets: [{id: t, type: template, source: assets/t.tmpl, target: %s}]\nprofiles: {base: {assets: [t]}}\n",
		filepath.Join(h.work, "out")))

	out, err := h.run("doctor")
	require.Error(t, err)
	require.Equal(t, 1, ExitCode(err))
	require.Contains(t, out, "template_syntax")
	require.Contains(t, out, "{{ if .x }}")
}
