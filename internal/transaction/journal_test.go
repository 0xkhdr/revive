package transaction

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const pythonJournalFixture = "testdata/python_journal"

// Phase 5: the journal JSON is byte-compatible with a Python-written one — same field names,
// same order, explicit nulls, two-space indentation.
func TestJournalIsByteCompatibleWithPython(t *testing.T) {
	t.Parallel()
	pythonJSON, err := os.ReadFile(filepath.Join(pythonJournalFixture, "journal.json"))
	require.NoError(t, err)

	var j Journal
	require.NoError(t, json.Unmarshal(pythonJSON, &j))

	got, err := MarshalJournal(&j)
	require.NoError(t, err)
	require.JSONEq(t, string(pythonJSON), string(got))
	require.Equal(t, strings.TrimSpace(string(pythonJSON)), strings.TrimSpace(string(got)),
		"the serialization must match Python's byte for byte, not merely semantically")
}

func TestJournalRoundTripsExplicitNulls(t *testing.T) {
	t.Parallel()
	j := &Journal{TxID: "t", Status: StatusPending, Entries: []RollbackEntry{{Op: OpCreate, Target: "/tmp/x"}}}
	raw, err := MarshalJournal(j)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"src_backup": null`)
	require.Contains(t, string(raw), `"checksum": null`)
	require.Contains(t, string(raw), `"permissions": null`)
	require.NotContains(t, string(raw), "executed_hooks",
		"the additive field must be absent when unused, so a Python reader sees the shape it expects")
}

func TestJournalCarriesExecutedHooks(t *testing.T) {
	t.Parallel()
	j := &Journal{TxID: "t", Status: StatusRolledBack, ExecutedHooks: []ExecutedHook{
		{AssetID: "nginx", Stage: "pre", Command: []string{"nginx", "-t"}, Started: 1.5, Result: HookOK},
	}}
	raw, err := MarshalJournal(j)
	require.NoError(t, err)

	var back Journal
	require.NoError(t, json.Unmarshal(raw, &back))
	require.Equal(t, j.ExecutedHooks, back.ExecutedHooks)
}

func TestLoadJournal(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "j.json")
	require.NoError(t, WriteJournal(path, &Journal{TxID: "t", Status: StatusCommitted}))

	j, err := LoadJournal(path)
	require.NoError(t, err)
	require.Equal(t, "t", j.TxID)
	require.NotNil(t, j.Entries)
	require.True(t, j.Complete())

	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(),
		"a journal names every managed path and is not for other users to read")
}

func TestLoadJournalErrors(t *testing.T) {
	t.Parallel()
	_, err := LoadJournal(filepath.Join(t.TempDir(), "absent.json"))
	require.Error(t, err)

	bad := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte("{not json"), 0o600))
	_, err = LoadJournal(bad)
	require.Error(t, err)
}

func TestJournalCompleteness(t *testing.T) {
	t.Parallel()
	for status, complete := range map[string]bool{
		StatusPending:     false,
		StatusExecuting:   false,
		StatusVerifying:   false,
		StatusRollingBack: false,
		StatusAborted:     false,
		StatusCommitted:   true,
		StatusRolledBack:  true,
	} {
		require.Equal(t, complete, (&Journal{Status: status}).Complete(), status)
	}
}
