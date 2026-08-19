package logging

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLevelFollowsVerbose(t *testing.T) {
	t.Parallel()
	var quiet, loud bytes.Buffer
	New(&quiet, Options{}).Debug("hidden")
	New(&loud, Options{Verbose: true}).Debug("shown")
	require.Empty(t, quiet.String())
	require.Contains(t, loud.String(), "shown")
}

func TestHeadlessKeepsTheTimestamp(t *testing.T) {
	t.Parallel()
	var interactive, headless bytes.Buffer
	New(&interactive, Options{}).Info("hello", "key", "value")
	New(&headless, Options{Headless: true}).Info("hello")

	require.NotContains(t, interactive.String(), "time=")
	require.Contains(t, interactive.String(), "key=value")
	require.True(t, strings.HasPrefix(headless.String(), "time="))
}

func TestAttributesNamedTimeAreNotStripped(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	New(&buf, Options{}).Info("m", "group", "x")
	require.Contains(t, buf.String(), "group=x")
}
