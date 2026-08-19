package cli

import (
	"github.com/spf13/cobra"
)

// Commands whose flags are part of the CLI contract but whose engines arrive in later stages.
// The flags are declared here so `--help` is complete and completion works; the bodies are
// filled in by the stage that builds the engine behind them.

func newStatusCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "status",
		Short:             "Report drift between the manifest and the machine",
		ValidArgsFunction: env.completeProfiles,
		RunE:              notImplemented,
	}
	addProfileFlag(cmd, env)
	addIdentityFlag(cmd)
	addManifestFlag(cmd)
	cmd.Flags().Bool("json", false, "Emit the report as JSON for CI")
	return cmd
}

func newDiffCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "diff",
		Short:             "Show content diffs for modified assets",
		ValidArgsFunction: env.completeProfiles,
		RunE:              notImplemented,
	}
	addProfileFlag(cmd, env)
	addIdentityFlag(cmd)
	addManifestFlag(cmd)
	cmd.Flags().BoolP("unified", "u", false, "Unified diff instead of side-by-side")
	return cmd
}

func newDoctorCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run health checks on the workspace and the machine",
		RunE:  notImplemented,
	}
	addProfileFlag(cmd, env)
	addManifestFlag(cmd)
	cmd.Flags().Bool("json", false, "Emit the structured report for CI")
	return cmd
}

func newBackupCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "backup <profile...>",
		Short:             "Copy machine state back into the repository",
		ValidArgsFunction: env.completeProfiles,
		RunE:              notImplemented,
	}
	addProfileFlag(cmd, env)
	addIdentityFlag(cmd)
	addManifestFlag(cmd)
	cmd.Flags().Bool("dry-run", false, "Report what would be backed up; write nothing")
	return cmd
}

func newRecoverCommand(_ *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Roll back or discard interrupted transactions",
		RunE:  notImplemented,
	}
	cmd.Flags().Bool("auto", false, "Roll back the newest incomplete journal and exit")
	return cmd
}

func newPruneCommand(_ *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete old transaction backup snapshots",
		RunE:  notImplemented,
	}
	cmd.Flags().Int("max-count", 10, "Keep at most N snapshots")
	cmd.Flags().Int("max-age-days", 30, "Delete snapshots older than N days")
	cmd.Flags().Bool("dry-run", false, "List candidates; delete nothing")
	cmd.Flags().BoolP("yes", "y", false, "Do not ask for confirmation")
	return cmd
}

func newSelfUninstallCommand(_ *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "self-uninstall",
		Short: "Remove the installed binary, and optionally the configuration",
		RunE:  notImplemented,
	}
	cmd.Flags().BoolP("force", "f", false, "Do not ask for confirmation")
	cmd.Flags().Bool("purge-config", false, "Also remove ~/.config/rv")
	return cmd
}

func notImplemented(cmd *cobra.Command, _ []string) error {
	return ErrNotImplemented
}
