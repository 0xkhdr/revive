// Package doctor answers "is this workspace sane, and can this machine apply it".
//
// Every check is read-only. Doctor is the command a confused user runs first, and it has to be
// safe to run on a broken machine.
package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/0xkhdr/revive/internal/engine"
	"github.com/0xkhdr/revive/internal/lockfile"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/platform"
	"github.com/0xkhdr/revive/internal/profile"
)

// ErrUnhealthy is returned when the report contains a critical issue. The CLI maps it to exit 1,
// which is what makes `rv doctor` usable as a CI gate.
var ErrUnhealthy = errors.New("workspace is unhealthy")

// Severity ranks an issue.
type Severity string

// Severities.
const (
	Critical Severity = "critical"
	Warning  Severity = "warning"
	Info     Severity = "info"
)

// Check categories.
const (
	CategoryManifest          = "manifest"
	CategoryLockfile          = "lockfile"
	CategorySystem            = "system"
	CategoryProfile           = "profile"
	CategoryProfileResolution = "profile_resolution"
	CategoryAssetSource       = "asset_source"
	CategoryAssetTarget       = "asset_target"
	CategorySecretSource      = "secret_source"
	CategoryTemplateSyntax    = "template_syntax"
	CategoryHookCommand       = "hook_command"
)

// Issue is one finding.
type Issue struct {
	Category string   `json:"category"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// Report is the whole diagnosis.
type Report struct {
	Healthy   bool    `json:"healthy"`
	ChecksRun int     `json:"checks_run"`
	Issues    []Issue `json:"issues"`
}

// Doctor runs the checks.
type Doctor struct {
	RepoDir      string
	ManifestPath string
	Profiles     []string
	Paths        paths.Config
	Platform     *platform.Detector
	Hostname     string
	// Handler resolves targets and sources the same way planning does. Nil builds a default.
	Handler *engine.Handler
}

// Run produces the report. It never mutates anything.
func (d *Doctor) Run() *Report {
	report := &Report{Healthy: true}

	m := d.checkManifest(report)
	if m == nil {
		return finish(report)
	}
	d.checkLockfile(report)

	resolved := d.checkProfiles(report, m)
	if resolved == nil {
		return finish(report)
	}

	d.checkSystem(report, resolved)
	d.checkAssets(report, resolved)
	return finish(report)
}

func finish(report *Report) *Report {
	for _, issue := range report.Issues {
		if issue.Severity == Critical {
			report.Healthy = false
			break
		}
	}
	return report
}

func (r *Report) add(category string, severity Severity, format string, args ...any) {
	r.Issues = append(r.Issues, Issue{
		Category: category,
		Severity: severity,
		Message:  fmt.Sprintf(format, args...),
	})
}

func (d *Doctor) checkManifest(report *Report) *manifest.Manifest {
	report.ChecksRun++
	if _, err := os.Stat(d.ManifestPath); err != nil {
		report.add(CategoryManifest, Critical, "manifest not found at %s", d.ManifestPath)
		return nil
	}

	report.ChecksRun++
	m, err := manifest.Load(d.ManifestPath)
	if err != nil {
		report.add(CategoryManifest, Critical, "manifest failed to validate: %s", err)
		return nil
	}
	return m
}

func (d *Doctor) checkLockfile(report *Report) {
	report.ChecksRun++
	path := lockfile.PathFor(d.ManifestPath)
	if _, err := os.Stat(path); err != nil {
		report.add(CategoryLockfile, Warning,
			"lockfile %s is missing; this workspace has never been restored", filepath.Base(path))
		return
	}
	if _, err := lockfile.Load(path); err != nil {
		report.add(CategoryLockfile, Warning, "lockfile %s could not be read: %s", filepath.Base(path), err)
	}
}

func (d *Doctor) checkProfiles(report *Report, m *manifest.Manifest) *profile.Resolved {
	names := d.Profiles
	if len(names) == 0 {
		// With no profile named, check every one the manifest declares: doctor is a workspace
		// audit, not a single-run preflight.
		for name := range m.Profiles {
			names = append(names, name)
		}
	}

	for _, name := range names {
		report.ChecksRun++
		if _, ok := m.Profiles[name]; !ok {
			report.add(CategoryProfile, Critical, "profile %q is not defined in the manifest", name)
			return nil
		}
	}
	if len(names) == 0 {
		report.add(CategoryProfile, Warning, "the manifest declares no profiles")
		return nil
	}

	report.ChecksRun++
	resolved, err := profile.Resolve(m, names...)
	if err != nil {
		report.add(CategoryProfileResolution, Critical, "profile resolution failed: %s", err)
		return nil
	}

	report.ChecksRun++
	if err := profile.ApplyOverrides(m, resolved, d.RepoDir, d.Hostname); err != nil {
		report.add(CategoryProfileResolution, Critical, "machine override failed: %s", err)
		return nil
	}
	return resolved
}

func (d *Doctor) checkSystem(report *Report, resolved *profile.Resolved) {
	detector := d.Platform
	if detector == nil {
		detector = platform.Default
	}

	for _, group := range append([]string{}, sortedGroups(resolved)...) {
		report.ChecksRun++
		if !detector.HasManager(group) {
			report.add(CategorySystem, Warning,
				"the manifest uses %s packages but no %s tooling is available on this machine", group, group)
		}
	}
}

// sortedGroups lists the package groups the resolved profile actually uses, in install order so
// the report reads the way a restore runs.
func sortedGroups(resolved *profile.Resolved) []string {
	var out []string
	for _, group := range manifest.ListNames {
		if len(resolved.Packages[group]) > 0 {
			out = append(out, group)
		}
	}
	if len(resolved.DockerImages) > 0 {
		out = append(out, "docker")
	}
	if resolved.Node.Version != "" || resolved.Node.VersionFile != "" {
		out = append(out, "node")
	}
	return out
}

func (d *Doctor) checkAssets(report *Report, resolved *profile.Resolved) {
	h := d.handler()

	for _, id := range resolved.AssetIDs() {
		d.checkOne(report, h, resolved.Assets[id], CategoryAssetSource)
	}
	for _, id := range resolved.SecretIDs() {
		d.checkOne(report, h, resolved.Secrets[id].Asset(), CategorySecretSource)
	}
}

func (d *Doctor) checkOne(report *Report, h *engine.Handler, asset manifest.Asset, sourceCategory string) {
	source := h.AbsSource(asset)

	report.ChecksRun++
	sourceExists := true
	if _, err := os.Lstat(source); err != nil {
		sourceExists = false
		report.add(sourceCategory, Critical, "asset %q source not found: %s", asset.ID, asset.Source)
	}

	if sourceExists && asset.Type == manifest.TypeTemplate {
		d.checkTemplateSyntax(report, asset, source)
	}
	d.checkHooks(report, asset)

	report.ChecksRun++
	targets, err := h.Targets(asset)
	if err != nil {
		report.add(CategoryAssetTarget, Warning, "asset %q target could not be resolved: %s", asset.ID, err)
		return
	}
	for _, target := range targets {
		report.ChecksRun++
		parent := filepath.Dir(target)
		if _, err := os.Stat(parent); err != nil {
			report.add(CategoryAssetTarget, Warning,
				"asset %q target directory does not exist: %s", asset.ID, parent)
		}
	}
}

// checkTemplateSyntax is the migration linter. It is critical severity because text/template
// silently passes Jinja2 statement tags through into the output file.
func (d *Doctor) checkTemplateSyntax(report *Report, asset manifest.Asset, source string) {
	report.ChecksRun++
	content, err := os.ReadFile(source)
	if err != nil {
		report.add(CategoryTemplateSyntax, Warning,
			"template %q could not be read: %s", asset.ID, err)
		return
	}
	for _, finding := range scanTemplate(string(content)) {
		report.add(CategoryTemplateSyntax, Critical, "%s:%d: %s — %s",
			asset.Source, finding.Line, finding.Snippet, finding.Advice)
	}
}

// checkHooks reports a hook whose argv[0] is not on PATH, which would otherwise fail halfway
// through a transaction.
func (d *Doctor) checkHooks(report *Report, asset manifest.Asset) {
	detector := d.Platform
	if detector == nil {
		detector = platform.Default
	}

	for stage, hooks := range map[string][]manifest.Hook{"pre": asset.Hooks.Pre, "post": asset.Hooks.Post} {
		for _, hook := range hooks {
			if hook.Command == "" {
				continue
			}
			report.ChecksRun++
			argv, err := engine.SplitWords(hook.Command)
			if err != nil {
				report.add(CategoryHookCommand, Warning,
					"asset %q %s-hook could not be parsed: %s", asset.ID, stage, err)
				continue
			}
			if !detector.HasTool(argv[0]) && !filepath.IsAbs(argv[0]) {
				report.add(CategoryHookCommand, Warning,
					"asset %q %s-hook command %q is not on PATH, so the hook will fail mid-transaction",
					asset.ID, stage, argv[0])
			}
		}
	}
}

func (d *Doctor) handler() *engine.Handler {
	if d.Handler != nil {
		return d.Handler
	}
	h := engine.NewHandler(d.RepoDir, d.Paths)
	if d.Hostname != "" {
		h.Hostname = d.Hostname
	}
	return h
}
