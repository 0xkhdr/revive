package recovery

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Snapshot is one transaction's backup directory.
type Snapshot struct {
	TxID    string
	Path    string
	ModTime time.Time
	AgeDays int
	Size    int64
	// Active is true when an incomplete journal still owns this snapshot.
	Active bool
}

// Retention bounds how many snapshots survive and for how long.
type Retention struct {
	MaxCount   int
	MaxAgeDays int
}

// Snapshots lists every backup directory, oldest first, marking those an incomplete journal
// still owns.
func (m *Manager) Snapshots() ([]Snapshot, error) {
	active, err := m.activeTxIDs()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(m.Paths.BackupDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading backup directory: %w", err)
	}

	now := m.now()
	var out []Snapshot
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(m.Paths.BackupDir, entry.Name())
		out = append(out, Snapshot{
			TxID:    entry.Name(),
			Path:    path,
			ModTime: info.ModTime(),
			AgeDays: int(now.Sub(info.ModTime()).Hours() / 24),
			Size:    dirSize(path),
			Active:  active[entry.Name()],
		})
	}

	slices.SortFunc(out, func(a, b Snapshot) int { return a.ModTime.Compare(b.ModTime) })
	return out, nil
}

func (m *Manager) activeTxIDs() (map[string]bool, error) {
	pending, err := m.Scan()
	if err != nil {
		return nil, err
	}
	active := make(map[string]bool, len(pending))
	for _, inc := range pending {
		active[inc.TxID] = true
	}
	return active, nil
}

// Candidates selects the snapshots that may be deleted under a retention policy.
//
// The active-transaction guard is the critical part: deleting the backup directory of an
// in-flight or crashed transaction destroys its rollback ability permanently, and age is never a
// reason to override that.
func Candidates(snapshots []Snapshot, policy Retention) []Snapshot {
	var candidates []Snapshot
	seen := map[string]bool{}

	add := func(s Snapshot) {
		if s.Active || seen[s.Path] {
			return
		}
		seen[s.Path] = true
		candidates = append(candidates, s)
	}

	// Too old.
	for _, s := range snapshots {
		if !s.Active && policy.MaxAgeDays > 0 && s.AgeDays >= policy.MaxAgeDays {
			add(s)
		}
	}

	// Too many. What remains after the age sweep is what counts against max_count, and the
	// oldest go first.
	var remaining []Snapshot
	for _, s := range snapshots {
		if !s.Active && !seen[s.Path] {
			remaining = append(remaining, s)
		}
	}
	if policy.MaxCount > 0 && len(remaining) > policy.MaxCount {
		for _, s := range remaining[:len(remaining)-policy.MaxCount] {
			add(s)
		}
	}
	return candidates
}

// Prune deletes the candidates for a retention policy and returns those it removed. Under
// dryRun it reports the same list without deleting anything.
func (m *Manager) Prune(policy Retention, dryRun bool) ([]Snapshot, error) {
	snapshots, err := m.Snapshots()
	if err != nil {
		return nil, err
	}
	candidates := Candidates(snapshots, policy)
	if dryRun {
		return candidates, nil
	}

	for _, s := range candidates {
		if err := os.RemoveAll(s.Path); err != nil {
			return candidates, fmt.Errorf("removing snapshot %s: %w", s.TxID, err)
		}
		m.log().Debug("pruned backup snapshot", "tx_id", s.TxID, "age_days", s.AgeDays)
	}
	return candidates, nil
}

func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable entry contributes nothing to a size report
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
