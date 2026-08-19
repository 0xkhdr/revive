package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Phase 10: `{% … %}` is the important case. text/template passes it through as literal text
// rather than failing, so only this linter stands between the user and a config file containing
// the words "{% if x %}".
func TestJinja2StatementTagsAreCritical(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"{% if x %}yes{% endif %}",
		"{%- if x -%}yes{%- endif -%}",
		"{% for item in items %}{{ item }}{% endfor %}",
		"{% set name = 'x' %}",
	} {
		findings := scanTemplate(source)
		require.NotEmpty(t, findings, "%q must be caught", source)
	}

	findings := scanTemplate("line one\n{% if enabled %}\nvalue\n{% endif %}\n")
	require.Len(t, findings, 2)
	require.Equal(t, 2, findings[0].Line, "the line number must be reported")
	require.Equal(t, "{% if enabled %}", findings[0].Snippet)
	require.Contains(t, findings[0].Advice, "{{ if .x }}", "the replacement must be named")
	require.Contains(t, findings[0].Advice, "literal text")
	require.Equal(t, 4, findings[1].Line)
	require.Contains(t, findings[1].Advice, "{{ end }}")
}

// Phase 10: a bare `{{ x }}` with no leading dot is caught.
func TestBareIdentifiersAreCaught(t *testing.T) {
	t.Parallel()
	findings := scanTemplate("email = {{ email }}\n")
	require.Len(t, findings, 1)
	require.Equal(t, 1, findings[0].Line)
	require.Contains(t, findings[0].Advice, "{{ .email }}")
	require.Contains(t, findings[0].Advice, "leading dot")
}

// Phase 10: Jinja2 filter syntax is caught.
func TestFilterSyntaxIsCaught(t *testing.T) {
	t.Parallel()
	findings := scanTemplate("editor = {{ editor | upper }}\n")
	require.Len(t, findings, 1)
	require.Contains(t, findings[0].Advice, "{{ upper .editor }}")
}

// Phase 10: a valid text/template source produces no issue.
func TestValidTemplatesAreClean(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"email = {{ .email }}\n",
		"{{ if .x }}yes{{ end }}\n",
		"{{ if eq ._platform \"darwin\" }}mac{{ end }}\n",
		"{{ range .xs }}[{{ . }}]{{ end }}\n",
		"{{ upper .name }}\n",
		"{{ .EDITOR | default \"vim\" }}\n",
		"{{ .name | trim | upper }}\n",
		"{{ index . \"weird-key\" }}\n",
		"{{ printf \"%s\" .x }}\n",
		"no actions at all\n",
		"{{ end }}\n",
		"{{ else }}\n",
		"{{ . }}\n",
		"{{ $x := .y }}\n",
	} {
		require.Empty(t, scanTemplate(source), "%q must be accepted", source)
	}
}

// A pipe between two actions is valid text/template piping and must not be mistaken for a
// Jinja2 filter.
func TestValidPipingIsNotFlagged(t *testing.T) {
	t.Parallel()
	require.Empty(t, scanTemplate("{{ .name | upper }}\n"))
	require.Empty(t, scanTemplate("{{ env \"HOME\" | trim }}\n"))
}

// The linter runs through the full doctor, at critical severity, naming file and line.
func TestTemplateSyntaxThroughDoctor(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write("assets/gitconfig.tmpl", "[user]\n{% if work %}\nemail = {{ email }}\n{% endif %}\n")
	f.write("manifest.yaml", "version: 2\nassets: [{id: gitconfig, type: template, source: assets/gitconfig.tmpl, target: "+
		filepath.Join(f.home, ".gitconfig")+"}]\nprofiles: {base: {assets: [gitconfig]}}\n")

	report := f.d.Run()
	require.False(t, report.Healthy, "Jinja2 in a template must block a restore")

	found := issues(report, CategoryTemplateSyntax)
	require.Len(t, found, 3, "two statement tags and one bare identifier")
	require.Equal(t, Critical, severityOf(report, CategoryTemplateSyntax))
	for _, msg := range found {
		require.Contains(t, msg, "assets/gitconfig.tmpl", "the file must be named")
	}
	require.Contains(t, strings.Join(found, "\n"), ":2:", "the line must be named")
	require.Contains(t, strings.Join(found, "\n"), "{{ .email }}")
}

func TestValidTemplateThroughDoctorIsHealthy(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write("assets/gitconfig.tmpl", "[user]\n{{ if .work }}\nemail = {{ .email }}\n{{ end }}\n")
	f.write("manifest.yaml", "version: 2\nassets: [{id: gitconfig, type: template, source: assets/gitconfig.tmpl, target: "+
		filepath.Join(f.home, ".gitconfig")+"}]\nprofiles: {base: {assets: [gitconfig]}}\n")

	report := f.d.Run()
	require.Empty(t, issues(report, CategoryTemplateSyntax))
	require.True(t, report.Healthy, "%v", report.Issues)
}

// Only `type: template` sources are linted; a copy asset may legitimately contain braces.
func TestOnlyTemplatesAreLinted(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write("assets/conf", "{% this is not a template asset %}\n")
	f.write("manifest.yaml", "version: 2\nassets: [{id: conf, type: copy, source: assets/conf, target: "+
		filepath.Join(f.home, "conf")+"}]\nprofiles: {base: {assets: [conf]}}\n")

	require.Empty(t, issues(f.d.Run(), CategoryTemplateSyntax))
}

func TestUnreadableTemplateIsAWarning(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(f.repo, "assets", "tmpl"), 0o755))
	f.write("manifest.yaml", "version: 2\nassets: [{id: t, type: template, source: assets/tmpl, target: "+
		filepath.Join(f.home, "out")+"}]\nprofiles: {base: {assets: [t]}}\n")

	report := f.d.Run()
	require.Equal(t, Warning, severityOf(report, CategoryTemplateSyntax))
}
