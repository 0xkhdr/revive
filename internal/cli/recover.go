package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/recovery"
)

func newRecoverCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Roll back or discard interrupted transactions",
		Long: "A journal that exists at all means a transaction is in flight or ended badly. " +
			"Rollback restores every file from its snapshot; discard leaves the files alone and " +
			"just clears the record.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return env.runRecover(cmd)
		},
	}
	cmd.Flags().Bool("auto", false, "Roll back the newest incomplete journal and exit")
	return cmd
}

func (e *Env) runRecover(cmd *cobra.Command) error {
	m := e.recovery()
	pending, err := m.Scan()
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		e.line("no interrupted transactions; nothing to recover")
		return nil
	}

	auto, _ := cmd.Flags().GetBool("auto")
	if auto {
		// For CI and boot scripts: take the newest and get the machine back to a known state.
		newest := pending[0]
		e.line("rolling back %s (%s)", newest.TxID, newest.Status)
		if err := m.Rollback(newest); err != nil {
			return err
		}
		e.reportHooks(newest)
		e.line("rolled back %s", newest.TxID)
		return nil
	}

	rows := make([][]string, 0, len(pending))
	for _, inc := range pending {
		rows = append(rows, []string{
			inc.TxID, inc.Status, inc.Timestamp.Format(time.RFC3339),
			fmt.Sprintf("%d file(s)", len(inc.Journal.Entries)),
		})
	}
	e.table([]string{"TRANSACTION", "STATUS", "STARTED", "ENTRIES"}, rows)

	for _, inc := range pending {
		rollback, err := e.Confirm(fmt.Sprintf(
			"Roll back %s? (answering no discards it, leaving the files as they are)", inc.TxID))
		if err != nil {
			return err
		}
		if rollback {
			if err := m.Rollback(inc); err != nil {
				return err
			}
			e.reportHooks(inc)
			e.line("rolled back %s", inc.TxID)
			continue
		}
		if err := m.Discard(inc); err != nil {
			return err
		}
		e.line("discarded %s; the files were left as they are", inc.TxID)
	}
	return nil
}

// reportHooks prints what a rollback could not undo. Rollback restores files exactly, but
// `systemctl restart` has no inverse, and this is the user's only record of what survived.
func (e *Env) reportHooks(inc recovery.Incomplete) {
	hooks := inc.ExecutedHooks()
	if len(hooks) == 0 {
		return
	}
	e.line("")
	e.line("These hooks already ran and were NOT reversed:")
	for _, h := range hooks {
		e.item("%s (%s): %v [%s]", h.AssetID, h.Stage, h.Command, h.Result)
	}
	e.line("Review them if they had lasting effects.")
}

func (e *Env) recovery() *recovery.Manager {
	return &recovery.Manager{Paths: e.Paths, Log: e.logger(), Now: e.now}
}
