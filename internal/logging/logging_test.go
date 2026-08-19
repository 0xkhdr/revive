package logging

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/0xkhdr/revive/internal/scrub"
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

func TestOutputIsScrubbed(t *testing.T) {
	scrub.Default.Clear()
	t.Cleanup(scrub.Default.Clear)
	scrub.RegisterSecret("hunter2token")

	var buf bytes.Buffer
	New(&buf, Options{}).Info("writing secret", "value", "hunter2token",
		"identity", "AGE-SECRET-KEY-1QQQQQQQQQQQQQQ")

	require.NotContains(t, buf.String(), "hunter2token", "a registered secret must not reach the console")
	require.NotContains(t, buf.String(), "AGE-SECRET-KEY-1QQQQQQQQQQQQQQ")
	require.Contains(t, buf.String(), scrub.Redacted)
}

func TestScrubbedWriterReportsTheCallerLength(t *testing.T) {
	scrub.Default.Clear()
	t.Cleanup(scrub.Default.Clear)
	scrub.RegisterSecret("hunter2token")

	var buf bytes.Buffer
	w := Scrubbed(&buf, scrub.Default)
	line := []byte("value=hunter2token\n")
	n, err := w.Write(line)
	require.NoError(t, err)
	require.Equal(t, len(line), n, "a short write would be reported as an error by callers")
	require.Equal(t, "value="+scrub.Redacted+"\n", buf.String())
}

func TestScrubbedWriterPropagatesErrors(t *testing.T) {
	_, err := Scrubbed(failingWriter{}, scrub.New()).Write([]byte("x"))
	require.ErrorIs(t, err, os.ErrClosed)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }
