package cli

import (
	"github.com/spf13/cobra"
)

// Commands whose flags are part of the CLI contract but whose engines arrive in later stages.
// The flags are declared here so `--help` is complete and completion works; the bodies are
// filled in by the stage that builds the engine behind them.

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
