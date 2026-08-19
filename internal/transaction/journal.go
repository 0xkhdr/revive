package transaction

import (
	"encoding/json"
	"fmt"
	"os"
)

// Journal statuses. A journal whose status is neither committed nor rolled_back describes an
// interrupted transaction that `rv recover` must deal with.
const (
	StatusPending     = "pending"
	StatusExecuting   = "executing"
	StatusVerifying   = "verifying"
	StatusCommitted   = "committed"
	StatusRollingBack = "rolling_back"
	StatusRolledBack  = "rolled_back"
	StatusAborted     = "aborted"
)

// Rollback entry operations, recorded from the pre-mutation state of a target.
const (
	OpCreate = "create" // the target did not exist; rollback removes whatever is there now
	OpModify = "modify" // the target existed; rollback restores it from its backup
	OpDelete = "delete" // the planned operation removed the target; rollback restores it
)

// symlinkBackupPrefix marks a backup file that stands in for a symlink. The backup's contents
// are the literal string SYMLINK:<link target>; this format is part of the compatibility
// contract with the Python implementation.
const symlinkBackupPrefix = "SYMLINK:"

// RollbackEntry records one target's pre-mutation state.
//
// The JSON shape, field names and null-versus-absent behavior are part of the compatibility
// contract: a Python-written journal must load here, and a Go-written journal must load there.
// That is why the pointer fields carry no omitempty — Python writes them as explicit nulls.
type RollbackEntry struct {
	Op          string  `json:"op"`
	SrcBackup   *string `json:"src_backup"`
	Target      string  `json:"target"`
	Checksum    *string `json:"checksum"`
	Permissions *string `json:"permissions"`
}

// ExecutedHook records a hook that ran. Rollback restores files; it cannot un-run a hook, so
// the journal carries what already happened for the user to review.
//
// This field does not exist in the Python journal format. It is additive: a Python-written
// journal simply lacks it, and the Python reader ignores it, so the contract holds both ways.
type ExecutedHook struct {
	AssetID string   `json:"asset_id"`
	Stage   string   `json:"stage"`
	Command []string `json:"command"`
	Started float64  `json:"started"`
	Result  string   `json:"result"` // ok | failed | timeout
}

// Hook results.
const (
	HookOK       = "ok"
	HookFailed   = "failed"
	HookTimedOut = "timeout"
)

// Journal is the on-disk record of a transaction, written before any mutation.
type Journal struct {
	TxID          string          `json:"tx_id"`
	Timestamp     float64         `json:"timestamp"`
	Status        string          `json:"status"`
	Entries       []RollbackEntry `json:"entries"`
	ExecutedHooks []ExecutedHook  `json:"executed_hooks,omitempty"`
}

// LoadJournal reads a journal file.
func LoadJournal(path string) (*Journal, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading journal: %w", err)
	}
	var j Journal
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("parsing journal %s: %w", path, err)
	}
	if j.Entries == nil {
		j.Entries = []RollbackEntry{}
	}
	return &j, nil
}

// Complete reports whether the journal describes a finished transaction, one that needs no
// recovery.
func (j *Journal) Complete() bool {
	return j.Status == StatusCommitted || j.Status == StatusRolledBack
}

// MarshalJournal serializes a journal the way the Python implementation writes it: two-space
// indentation, same field order, explicit nulls.
func MarshalJournal(j *Journal) ([]byte, error) {
	if j.Entries == nil {
		j.Entries = []RollbackEntry{}
	}
	return json.MarshalIndent(j, "", "  ")
}

// WriteJournal serializes a journal and writes it atomically.
func WriteJournal(path string, j *Journal) error {
	raw, err := MarshalJournal(j)
	if err != nil {
		return fmt.Errorf("serializing journal: %w", err)
	}
	return AtomicWrite(path, raw, 0o600)
}
