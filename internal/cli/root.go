// Package cli builds the cobra command tree. Business logic lives in the other internal
// packages; this layer parses flags, renders output, and maps errors to exit codes.
package cli

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/logging"
	"github.com/0xkhdr/revive/internal/paths"
	"github.com/0xkhdr/revive/internal/platform"
	"github.com/0xkhdr/revive/internal/providers"
	"github.com/0xkhdr/revive/internal/scrub"
)

// Env carries every seam the commands need. Tests construct one rooted at t.TempDir(), which is
// what lets the whole CLI be exercised without touching the machine.
type Env struct {
	Out io.Writer
	Err io.Writer
	In  io.Reader

	Log      *slog.Logger
	Paths    paths.Config
	Scrubber *scrub.Scrubber
	Runner   providers.Runner
	// Git runs a git subcommand in a directory. Injectable so `clone` and `workspace sync` are
	// testable without a network.
	Git      func(dir string, args ...string) ([]byte, error)
	WorkDir  string
	Hostname string
	Now      func() time.Time

	Platform *platform.Detector

	Verbose  bool
	Headless bool

	// auditCloser is closed when the command tree finishes.
	auditCloser io.Closer
}

func (e *Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Env) scrubber() *scrub.Scrubber {
	if e.Scrubber != nil {
		return e.Scrubber
	}
	return scrub.Default
}

// out is the output stream, or a sink when none was set. A nil writer must never panic a
// command that is only reporting.
func (e *Env) out() io.Writer {
	if e.Out != nil {
		return e.Out
	}
	return io.Discard
}

func (e *Env) logger() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Confirm asks a yes/no question.
//
// Under --headless every prompt becomes an error: a CI run that silently answers for the user is
// how unattended data loss happens.
func (e *Env) Confirm(prompt string) (bool, error) {
	if e.Headless {
		return false, fmt.Errorf("%w: %s (running --headless, so there is nobody to ask; "+
			"pass an explicit flag or set conflict_strategy)", ErrUsage, prompt)
	}
	if e.In == nil {
		return false, fmt.Errorf("%w: %s (no input stream)", ErrUsage, prompt)
	}
	_, _ = fmt.Fprintf(e.out(), "%s [y/N] ", prompt)

	line, err := bufio.NewReader(e.In).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// NewRootCommand builds the full command tree.
func NewRootCommand(version string, env *Env) *cobra.Command {
	root := &cobra.Command{
		Use:   "rv",
		Short: "Declarative, transactional, reversible machine configuration",
		Long: "rv makes a developer machine's configuration declarative, transactional and " +
			"reversible. Every command runs against the current working directory as the workspace.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return env.setup(cmd)
		},
		PersistentPostRun: func(*cobra.Command, []string) {
			if env.auditCloser != nil {
				_ = env.auditCloser.Close()
				env.auditCloser = nil
			}
		},
	}
	root.PersistentFlags().BoolP("verbose", "v", false, "Debug-level logging to console")
	root.PersistentFlags().Bool("headless", false, "CI mode: plain output, no colors, no prompts")
	root.SetVersionTemplate("rv {{.Version}}\n")

	root.AddCommand(
		newInitCommand(env),
		newCloneCommand(env),
		newRestoreCommand(env),
		newBackupCommand(env),
		newStatusCommand(env),
		newDiffCommand(env),
		newDoctorCommand(env),
		newRecoverCommand(env),
		newPruneCommand(env),
		newSecretCommand(env),
		newWorkspaceCommand(env),
		newSelfUninstallCommand(env),
	)
	return root
}

// setup runs before every command body: global flags, then logging with both handlers scrubbed,
// then the workspace .env.
func (e *Env) setup(cmd *cobra.Command) error {
	e.Verbose, _ = cmd.Flags().GetBool("verbose")
	e.Headless, _ = cmd.Flags().GetBool("headless")
	if e.Out == nil {
		e.Out = cmd.OutOrStdout()
	}
	if e.Err == nil {
		e.Err = cmd.ErrOrStderr()
	}
	if e.WorkDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolving the working directory: %w", err)
		}
		e.WorkDir = wd
	}
	if e.Hostname == "" {
		if name, err := os.Hostname(); err == nil {
			e.Hostname = name
		}
	}
	if e.Runner == nil {
		e.Runner = providers.ExecRunner{}
	}
	if e.Git == nil {
		e.Git = execGit
	}
	if e.Paths == (paths.Config{}) {
		cfg, err := paths.Default()
		if err != nil {
			return err
		}
		e.Paths = cfg
	}

	console := logging.New(e.Err, logging.Options{
		Verbose: e.Verbose, Headless: e.Headless, Scrubber: e.scrubber(),
	})
	audit, closer, err := logging.NewAudit(e.Paths.AuditLog, e.scrubber())
	if err != nil {
		// An unwritable audit log must not stop the user working; it is a record, not a gate.
		console.Debug("audit log unavailable", "error", err)
		e.Log = console
	} else {
		e.auditCloser = closer
		e.Log = logging.NewFanout(console, audit)
	}

	// The workspace .env is loaded before any command body, so ${VAR} targets resolve.
	if err := paths.LoadEnv(e.WorkDir); err != nil {
		e.Log.Warn("loading .env", "error", err)
	}
	return nil
}

// Execute runs the root command against the real process environment.
func Execute(version string) error {
	env := &Env{Out: os.Stdout, Err: os.Stderr, In: os.Stdin}
	root := NewRootCommand(version, env)

	err := root.Execute()
	if err != nil {
		// The CLI layer is the only place an error is formatted for a human, and it goes
		// through the scrubber like everything else.
		fmt.Fprintln(os.Stderr, "rv: "+env.scrubber().Scrub(err.Error()))
	}
	return err
}
