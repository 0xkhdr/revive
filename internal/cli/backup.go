package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/engine"
)

func newBackupCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "backup <profile...>",
		Short:             "Copy machine state back into the repository",
		Long:              "System to repo: the inverse of restore, for a file that was edited in place.",
		ValidArgsFunction: env.completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			return env.runBackup(cmd, args)
		},
	}
	addProfileFlag(cmd, env)
	addIdentityFlag(cmd)
	addManifestFlag(cmd)
	cmd.Flags().Bool("dry-run", false, "Report what would be backed up; write nothing")
	return cmd
}

func (e *Env) runBackup(cmd *cobra.Command, args []string) error {
	names := profiles(cmd, args)
	if len(names) == 0 {
		return fmt.Errorf("%w: name at least one profile", ErrUsage)
	}
	identity, _ := cmd.Flags().GetString("identity")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	res, err := e.restorer().Backup(cmd.Context(), engine.BackupOptions{
		RepoDir:      e.WorkDir,
		ManifestPath: e.manifestPath(cmd),
		Profiles:     names,
		Identity:     identity,
		DryRun:       dryRun,
	})
	if err != nil {
		return err
	}

	rows := make([][]string, 0, len(res.Items))
	failed := 0
	for _, item := range res.Items {
		if item.Action == engine.FailedItem {
			failed++
		}
		rows = append(rows, []string{item.AssetID, string(item.Action), item.Destination, item.Reason})
	}
	e.table([]string{"ASSET", "ACTION", "DESTINATION", "DETAILS"}, rows)

	if res.DryRun {
		e.line("")
		e.line("dry run: nothing was written")
	}
	if failed > 0 {
		return fmt.Errorf("%w: %d item(s) could not be backed up", ErrOperation, failed)
	}
	return nil
}
