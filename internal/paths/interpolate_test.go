package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixed(vars map[string]string) Lookup {
	return func(name string) (string, bool) { v, ok := vars[name]; return v, ok }
}

// Phase 1: ${HOME}/x interpolates, ${UNSET}/x errors, ${UNSET:-/tmp}/x yields /tmp/x.
func TestInterpolate(t *testing.T) {
	t.Parallel()
	env := fixed(map[string]string{"HOME": "/home/dev", "EMPTY": ""})

	got, err := Interpolate("${HOME}/x", env)
	require.NoError(t, err)
	require.Equal(t, "/home/dev/x", got)

	_, err = Interpolate("${UNSET}/x", env)
	require.ErrorIs(t, err, ErrUnsetVariable)
	require.Contains(t, err.Error(), "UNSET", "the error must name the variable")

	got, err = Interpolate("${UNSET:-/tmp}/x", env)
	require.NoError(t, err)
	require.Equal(t, "/tmp/x", got)

	got, err = Interpolate("${EMPTY:-/fallback}/x", env)
	require.NoError(t, err)
	require.Equal(t, "/x", got, "a set-but-empty variable wins over the default")

	got, err = Interpolate("${HOME}/${HOME}", env)
	require.NoError(t, err)
	require.Equal(t, "/home/dev//home/dev", got)

	got, err = Interpolate("no variables here", env)
	require.NoError(t, err)
	require.Equal(t, "no variables here", got)

	got, err = Interpolate("$HOME and ${1BAD} and ${}", env)
	require.NoError(t, err)
	require.Equal(t, "$HOME and ${1BAD} and ${}", got, "only the documented syntax is interpolated")
}

func TestInterpolateReportsTheFirstMissingVariable(t *testing.T) {
	t.Parallel()
	_, err := Interpolate("${A}/${B}", fixed(nil))
	require.ErrorIs(t, err, ErrUnsetVariable)
	require.Contains(t, err.Error(), "A")
}

func TestInterpolateUsesProcessEnv(t *testing.T) {
	t.Setenv("RV_TEST_VAR", "value")
	got, err := Interpolate("${RV_TEST_VAR}", os.LookupEnv)
	require.NoError(t, err)
	require.Equal(t, "value", got)
}

func TestParseEnv(t *testing.T) {
	t.Parallel()
	got, err := ParseEnv(strings.NewReader(`
# a comment
PLAIN=value
  SPACED  =  spaced value
DOUBLE="quoted"
SINGLE='quoted'
MISMATCHED="quoted'
EMPTY=
WITH_EQUALS=a=b=c
INNER=say "hi"
no_equals_line
`))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"PLAIN":       "value",
		"SPACED":      "spaced value",
		"DOUBLE":      "quoted",
		"SINGLE":      "quoted",
		"MISMATCHED":  `"quoted'`,
		"EMPTY":       "",
		"WITH_EQUALS": "a=b=c",
		"INNER":       `say "hi"`,
	}, got)
}

// Phase 1: .env loading never overwrites a variable already set in the real environment.
func TestLoadEnvFileDoesNotOverwrite(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".env"),
		[]byte("PRESET=from_file\nFRESH=from_file\n"), 0o600))

	env := map[string]string{"PRESET": "from_environment"}
	err := LoadEnvFile(repo, fixed(env), func(k, v string) error { env[k] = v; return nil })
	require.NoError(t, err)
	require.Equal(t, "from_environment", env["PRESET"])
	require.Equal(t, "from_file", env["FRESH"])
}

func TestLoadEnvFileMissingIsNotAnError(t *testing.T) {
	t.Parallel()
	require.NoError(t, LoadEnvFile(t.TempDir(), fixed(nil), func(string, string) error { return nil }))
}

func TestLoadEnvAppliesToTheProcess(t *testing.T) {
	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".env"), []byte("RV_FROM_DOTENV=yes\n"), 0o600))
	t.Setenv("RV_FROM_DOTENV", "")
	require.NoError(t, os.Unsetenv("RV_FROM_DOTENV"))
	require.NoError(t, LoadEnv(repo))
	require.Equal(t, "yes", os.Getenv("RV_FROM_DOTENV"))
}

func TestLoadEnvFileSurfacesSetenvFailures(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repo, ".env"), []byte("K=v\n"), 0o600))
	err := LoadEnvFile(repo, fixed(nil), func(string, string) error { return os.ErrPermission })
	require.ErrorIs(t, err, os.ErrPermission)
	require.Contains(t, err.Error(), "K")
}

func TestLoadEnvFileSurfacesOpenFailures(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(repo, ".env"), 0o755))
	require.Error(t, LoadEnvFile(repo, fixed(nil), func(string, string) error { return nil }))
}
