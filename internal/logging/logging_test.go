package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
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

func TestAuditLogWritesScrubbedJSONLines(t *testing.T) {
	s := scrub.New()
	s.RegisterSecret("hunter2token")

	path := filepath.Join(t.TempDir(), "data", "audit.log")
	logger, closer, err := NewAudit(path, s)
	require.NoError(t, err)

	logger.Info("restoring", "tx_id", "abc", "asset_id", "zshrc", "value", "hunter2token")
	logger.Debug("detail", "op", "restore")
	require.NoError(t, closer.Close())

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(),
		"the audit log names every managed path and is not for other users to read")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.Len(t, lines, 2, "debug records reach the audit log even when the console is quiet")

	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &first))
	require.Equal(t, "restoring", first["message"])
	require.Equal(t, "INFO", first["level"])
	require.NotEmpty(t, first["timestamp"])
	require.Equal(t, "abc", first["tx_id"])
	require.Equal(t, scrub.Redacted, first["value"], "a secret in any field must be caught")
}

func TestAuditLogAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	for range 2 {
		logger, closer, err := NewAudit(path, scrub.New())
		require.NoError(t, err)
		logger.Info("run")
		require.NoError(t, closer.Close())
	}

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Len(t, strings.Split(strings.TrimSpace(string(raw)), "\n"), 2,
		"an audit log is append-only; a second run must not truncate the first")
}

func TestNewAuditErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, _, err := NewAudit(filepath.Join(dir, "sub", "audit.log"), scrub.New())
	require.Error(t, err)
}

// One logger reaches both the console and the audit file, and neither can skip the scrubber.
func TestFanout(t *testing.T) {
	s := scrub.New()
	s.RegisterSecret("hunter2token")

	var console bytes.Buffer
	auditPath := filepath.Join(t.TempDir(), "audit.log")
	audit, closer, err := NewAudit(auditPath, s)
	require.NoError(t, err)

	logger := NewFanout(New(&console, Options{Scrubber: s}), audit, nil)
	logger.With("tx_id", "abc").WithGroup("g").Info("both", "value", "hunter2token")
	require.NoError(t, closer.Close())

	require.Contains(t, console.String(), "both")
	require.NotContains(t, console.String(), "hunter2token")

	raw, err := os.ReadFile(auditPath)
	require.NoError(t, err)
	require.Contains(t, string(raw), "abc")
	require.NotContains(t, string(raw), "hunter2token")

	// Debug is filtered by the console handler but not by the audit handler, so Enabled has to
	// be the union.
	require.True(t, logger.Enabled(context.Background(), slog.LevelDebug))
	require.False(t, NewFanout().Enabled(context.Background(), slog.LevelError))
}
