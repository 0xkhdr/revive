package cli

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/lockfile"
	"github.com/0xkhdr/revive/internal/watcher"
)

func newWatchCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "watch",
		Short:             "Auto-restore when the workspace changes",
		Long:              "Watches the workspace and re-runs a restore once changes settle. Ignores .git.",
		ValidArgsFunction: env.completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			return env.runWatch(cmd, args)
		},
	}
	addProfileFlag(cmd, env)
	addIdentityFlag(cmd)
	addManifestFlag(cmd)
	cmd.Flags().Float64P("debounce", "d", watcher.DefaultDebounce.Seconds(), "Seconds to wait for changes to settle")
	return cmd
}

func (e *Env) runWatch(cmd *cobra.Command, args []string) error {
	names := profiles(cmd, args)
	if len(names) == 0 {
		return fmt.Errorf("%w: name at least one profile", ErrUsage)
	}
	debounce, _ := cmd.Flags().GetFloat64("debounce")

	// The lockfile is written into the workspace by every restore, so the daemon has to be told
	// to ignore it — otherwise each restore triggers the next one indefinitely.
	manifestPath := e.manifestPath(cmd)

	w, err := watcher.New(watcher.Options{
		RepoDir:     e.WorkDir,
		LockFile:    e.Paths.LockFile,
		Debounce:    time.Duration(debounce * float64(time.Second)),
		Log:         e.logger(),
		IgnorePaths: []string{lockfile.PathFor(manifestPath)},
		OnTrigger: func(ctx context.Context) error {
			return e.runRestore(cmd, args)
		},
	})
	if err != nil {
		return err
	}

	// SIGINT and SIGTERM cancel the context, which ends the watch loop and returns cleanly.
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	e.line("watching %s for changes; press Ctrl-C to stop", e.WorkDir)
	return w.Run(ctx)
}
