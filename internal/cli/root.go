// Package cli builds the cobra command tree. Business logic lives in the other internal
// packages; this layer parses flags, renders output, and maps errors to exit codes.
package cli

import (
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/logging"
)

// Env carries the injectable seams every command needs. Tests construct one rooted at t.TempDir().
type Env struct {
	Out      io.Writer
	Err      io.Writer
	Log      *slog.Logger
	Verbose  bool
	Headless bool
}

// NewRootCommand builds the full command tree. env is populated in PersistentPreRun.
func NewRootCommand(version string, env *Env) *cobra.Command {
	root := &cobra.Command{
		Use:           "rv",
		Short:         "Declarative, transactional, reversible machine configuration",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			env.Verbose, _ = cmd.Flags().GetBool("verbose")
			env.Headless, _ = cmd.Flags().GetBool("headless")
			env.Out = cmd.OutOrStdout()
			env.Err = cmd.ErrOrStderr()
			env.Log = logging.New(env.Err, logging.Options{Verbose: env.Verbose, Headless: env.Headless})
		},
	}
	root.PersistentFlags().BoolP("verbose", "v", false, "Debug-level logging to console")
	root.PersistentFlags().Bool("headless", false, "CI mode: no colors, no boxes, no prompts")
	root.SetVersionTemplate("rv {{.Version}}\n")

	root.AddCommand(stubCommands()...)
	return root
}

// Execute runs the root command against the real process environment.
func Execute(version string) error {
	env := &Env{Out: os.Stdout, Err: os.Stderr}
	root := NewRootCommand(version, env)
	err := root.Execute()
	if err != nil {
		_, _ = io.WriteString(os.Stderr, "rv: "+err.Error()+"\n")
	}
	return err
}
