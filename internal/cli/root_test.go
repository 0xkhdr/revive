package cli

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/profile"
)

func run(t *testing.T, args ...string) error {
	t.Helper()
	var out bytes.Buffer
	root := NewRootCommand("1.2.3", &Env{})
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	return root.Execute()
}

// Phase 0: --help lists every command in docs/03, gui aside.
func TestHelpListsEveryCommand(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	root := NewRootCommand("1.2.3", &Env{})
	root.SetOut(&out)
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.Execute())

	for _, name := range []string{
		"init", "clone", "restore", "backup", "status", "diff", "doctor",
		"watch", "recover", "prune", "secret", "workspace", "self-uninstall", "completion",
	} {
		require.Contains(t, out.String(), name, "missing command %q", name)
	}
	require.NotContains(t, out.String(), "gui", "gui is deferred to post-v1.0")
}

func TestSubcommandsExist(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"secret", "keygen"}, {"secret", "encrypt"}, {"secret", "decrypt"}, {"secret", "rotate"},
		{"workspace", "list"}, {"workspace", "add"}, {"workspace", "remove"}, {"workspace", "sync"},
	} {
		require.ErrorIs(t, run(t, args...), ErrNotImplemented, "%v", args)
	}
}

// Phase 0: --version prints the build-time injected version.
func TestVersionIsInjected(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	root := NewRootCommand("1.2.3", &Env{})
	root.SetOut(&out)
	root.SetArgs([]string{"--version"})
	require.NoError(t, root.Execute())
	require.Equal(t, "rv 1.2.3\n", out.String())
}

func TestGlobalFlagsReachEnv(t *testing.T) {
	t.Parallel()
	env := &Env{}
	root := NewRootCommand("1.2.3", env)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"--verbose", "--headless", "status"})
	require.ErrorIs(t, root.Execute(), ErrNotImplemented)
	require.True(t, env.Verbose)
	require.True(t, env.Headless)
	require.NotNil(t, env.Log)
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	t.Parallel()
	err := run(t, "nope")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "unknown command"))
}

func TestExitCode(t *testing.T) {
	t.Parallel()
	require.Equal(t, 0, ExitCode(nil))
	require.Equal(t, 1, ExitCode(ErrUsage))
	require.Equal(t, 1, ExitCode(fmt.Errorf("loading: %w", manifest.ErrValidation)))
	require.Equal(t, 2, ExitCode(errors.New("manifest validation failed")),
		"an unwrapped message must not be mistaken for the sentinel")
	require.Equal(t, 1, ExitCode(manifest.ErrUnsupportedSchemaVersion))
	require.Equal(t, 1, ExitCode(profile.ErrNotFound))
	require.Equal(t, 2, ExitCode(ErrNotImplemented))
}
