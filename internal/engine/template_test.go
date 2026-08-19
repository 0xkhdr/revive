package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/transaction"
)

// Phase 6: a template renders with env vars, built-ins and template_vars, with template_vars
// winning, and the rendered SHA-256 is recorded.
func TestPlanTemplate(t *testing.T) {
	t.Setenv("RV_TEST_EDITOR", "nvim")
	f := newFixture(t)
	f.source("assets/gitconfig.tmpl",
		"user={{ ._user }} host={{ ._hostname }} platform={{ ._platform }} arch={{ ._arch }}\n"+
			"home={{ ._home }} repo={{ ._repo_dir }}\n"+
			"email={{ .email }} editor={{ .RV_TEST_EDITOR }}\n")

	plan, err := f.h.PlanAsset(asset("gitconfig", func(a *manifest.Asset) {
		a.Type = manifest.TypeTemplate
		a.Source = "assets/gitconfig.tmpl"
		a.Target = manifest.Scalar(f.target(".gitconfig"))
		a.TemplateVars = map[string]any{"email": "dev@example.com"}
	}))
	require.NoError(t, err)

	rendered := string(plan.Ops[0].Source.(transaction.SourceBytes).Data)
	require.Contains(t, rendered, "user=test-user host=test-host platform=linux arch=amd64")
	require.Contains(t, rendered, "home="+f.home+" repo="+f.repo)
	require.Contains(t, rendered, "email=dev@example.com editor=nvim")
	require.Len(t, plan.RenderedChecksum, 64, "the rendered SHA-256 is what makes template drift detectable")
}

// template_vars win over both the environment and the built-ins.
func TestTemplateVarsWinOverEverything(t *testing.T) {
	t.Setenv("RV_TEST_OVERRIDE", "from-environment")
	f := newFixture(t)
	f.source("assets/t.tmpl", "{{ .RV_TEST_OVERRIDE }}|{{ ._user }}")

	plan, err := f.h.PlanAsset(asset("t", func(a *manifest.Asset) {
		a.Type = manifest.TypeTemplate
		a.Source = "assets/t.tmpl"
		a.Target = manifest.Scalar(f.target("out"))
		a.TemplateVars = map[string]any{"RV_TEST_OVERRIDE": "from-vars", "_user": "from-vars-too"}
	}))
	require.NoError(t, err)
	require.Equal(t, "from-vars|from-vars-too", string(plan.Ops[0].Source.(transaction.SourceBytes).Data))
}

// Phase 6: an undefined template variable is an error, not an empty string.
func TestUndefinedTemplateVariableIsAnError(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.source("assets/t.tmpl", "value={{ .never_defined }}\n")

	_, err := f.h.PlanAsset(asset("t", func(a *manifest.Asset) {
		a.Type = manifest.TypeTemplate
		a.Source = "assets/t.tmpl"
		a.Target = manifest.Scalar(f.target("out"))
	}))
	require.ErrorIs(t, err, ErrTemplate)
	require.Contains(t, err.Error(), `asset "t"`, "the error must name the asset")
}

// Phase 6: every function in the registered func map works.
func TestFuncMap(t *testing.T) {
	t.Setenv("RV_TEST_ENVFUNC", "from-env-func")
	ctx := map[string]any{
		"name":  "  Revive  ",
		"empty": "",
		"list":  []string{"a", "b", "c"},
	}
	for name, tc := range map[string]struct{ tmpl, want string }{
		"upper":           {`{{ upper "abc" }}`, "ABC"},
		"lower":           {`{{ lower "ABC" }}`, "abc"},
		"trim":            {`{{ trim .name }}`, "Revive"},
		"replace":         {`{{ replace "a" "X" "banana" }}`, "bXnXnX"},
		"join":            {`{{ join "," .list }}`, "a,b,c"},
		"default used":    {`{{ default "vim" .empty }}`, "vim"},
		"default skipped": {`{{ default "vim" "emacs" }}`, "emacs"},
		"env":             {`{{ env "RV_TEST_ENVFUNC" }}`, "from-env-func"},
		"pipe":            {`{{ .name | trim | upper }}`, "REVIVE"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Render("t", []byte(tc.tmpl), ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(got))
		})
	}
}

// Phase 6: an unregistered function is a parse error naming the asset.
func TestUnregisteredFunctionIsAParseError(t *testing.T) {
	t.Parallel()
	_, err := Render("gitconfig", []byte(`{{ capitalize .x }}`), map[string]any{"x": "y"})
	require.ErrorIs(t, err, ErrTemplate)
	require.Contains(t, err.Error(), `asset "gitconfig"`)
	require.Contains(t, err.Error(), "capitalize")
}

func TestTemplateControlStructures(t *testing.T) {
	t.Parallel()
	got, err := Render("t", []byte(`{{ if eq ._platform "darwin" }}mac{{ else }}other{{ end }}`),
		map[string]any{"_platform": "darwin"})
	require.NoError(t, err)
	require.Equal(t, "mac", string(got))

	got, err = Render("t", []byte(`{{ range .xs }}[{{ . }}]{{ end }}`), map[string]any{"xs": []string{"a", "b"}})
	require.NoError(t, err)
	require.Equal(t, "[a][b]", string(got))
}

// Jinja2 statement tags are literal text to text/template. Nothing here rescues them — that is
// rv doctor's job — but the render must not silently claim success on a variable it dropped.
func TestJinja2StatementTagsPassThroughAsText(t *testing.T) {
	t.Parallel()
	got, err := Render("t", []byte("{% if x %}yes{% endif %}"), map[string]any{})
	require.NoError(t, err)
	require.Equal(t, "{% if x %}yes{% endif %}", string(got),
		"this is exactly why rv doctor has to detect it")
}

func TestTemplateSourceMustBeReadable(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	dir := filepath.Join(f.repo, "assets", "tmpl")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	_, err := f.h.PlanAsset(asset("t", func(a *manifest.Asset) {
		a.Type = manifest.TypeTemplate
		a.Source = "assets/tmpl"
		a.Target = manifest.Scalar(f.target("out"))
	}))
	require.ErrorIs(t, err, ErrTemplate)
}
