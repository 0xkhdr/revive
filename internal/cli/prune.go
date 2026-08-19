package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/recovery"
)

func newPruneCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete old transaction backup snapshots",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return env.runPrune(cmd)
		},
	}
	cmd.Flags().Int("max-count", 10, "Keep at most N snapshots")
	cmd.Flags().Int("max-age-days", 30, "Delete snapshots older than N days")
	cmd.Flags().Bool("dry-run", false, "List candidates; delete nothing")
	cmd.Flags().BoolP("yes", "y", false, "Do not ask for confirmation")
	addManifestFlag(cmd)
	return cmd
}

func (e *Env) runPrune(cmd *cobra.Command) error {
	maxCount, _ := cmd.Flags().GetInt("max-count")
	maxAge, _ := cmd.Flags().GetInt("max-age-days")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	yes, _ := cmd.Flags().GetBool("yes")

	// Manifest retention is the default; a flag the user actually set overrides it.
	fromManifest := e.retentionFor(e.manifestPath(cmd))
	if !cmd.Flags().Changed("max-count") {
		maxCount = fromManifest.MaxCount
	}
	if !cmd.Flags().Changed("max-age-days") {
		maxAge = fromManifest.MaxAgeDays
	}

	policy := recovery.Retention{MaxCount: maxCount, MaxAgeDays: maxAge}
	m := e.recovery()

	candidates, err := m.Prune(policy, true)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		e.line("nothing to prune (keeping at most %d snapshots, newer than %d days)", maxCount, maxAge)
		return nil
	}

	rows := make([][]string, 0, len(candidates))
	var total int64
	for _, s := range candidates {
		total += s.Size
		rows = append(rows, []string{s.TxID, fmt.Sprintf("%d", s.AgeDays), formatSize(s.Size)})
	}
	e.table([]string{"TRANSACTION", "AGE (DAYS)", "SIZE"}, rows)
	e.line("")
	e.line("%d snapshot(s), %s total", len(candidates), formatSize(total))

	if dryRun {
		e.line("dry run: nothing was deleted")
		return nil
	}
	if !yes {
		ok, err := e.Confirm("Delete these snapshots?")
		if err != nil {
			return err
		}
		if !ok {
			e.line("nothing was deleted")
			return nil
		}
	}

	deleted, err := m.Prune(policy, false)
	if err != nil {
		return err
	}
	e.line("deleted %d snapshot(s)", len(deleted))
	return nil
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGT"[exp])
}
