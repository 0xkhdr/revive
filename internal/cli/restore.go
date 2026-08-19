package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/engine"
)

func newRestoreCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <profile...>",
		Short: "Apply the repository to the machine, transactionally",
		Long: "Repo to system. Runs the fourteen-step apply order inside one transaction: any " +
			"failure rolls the whole run back.",
		ValidArgsFunction: env.completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			return env.runRestore(cmd, args)
		},
	}
	addManifestFlag(cmd)
	addIdentityFlag(cmd)
	addProfileFlag(cmd, env)
	cmd.Flags().Bool("dry-run", false, "Plan and validate; mutate nothing and run no hook")
	cmd.Flags().Bool("preview", false, "Report what would change, without restoring")
	cmd.Flags().Bool("interactive", true, "Prompt on conflicts")
	cmd.Flags().Bool("non-interactive", false, "Never prompt; a prompt conflict becomes an error")
	cmd.Flags().Bool("no-plugins", false, "Skip all plugin hooks")
	cmd.Flags().Bool("prune", false, "Prune old backup snapshots after a successful restore")
	cmd.Flags().Bool("parallel", true, "Plan assets in a worker pool")
	cmd.Flags().Bool("sequential", false, "Plan assets one at a time, for debugging")
	cmd.Flags().Bool("force-packages", false, "Invalidate the package cache and re-query every provider")
	cmd.MarkFlagsMutuallyExclusive("interactive", "non-interactive")
	cmd.MarkFlagsMutuallyExclusive("parallel", "sequential")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "preview")
	return cmd
}

func (e *Env) runRestore(cmd *cobra.Command, args []string) error {
	names := profiles(cmd, args)
	if len(names) == 0 {
		return fmt.Errorf("%w: name at least one profile", ErrUsage)
	}

	preview, _ := cmd.Flags().GetBool("preview")
	if preview {
		// --preview is the status engine, not a restore. Stage 10 builds it.
		return fmt.Errorf("%w: --preview", ErrNotImplemented)
	}

	identity, _ := cmd.Flags().GetString("identity")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	noPlugins, _ := cmd.Flags().GetBool("no-plugins")
	sequential, _ := cmd.Flags().GetBool("sequential")
	forcePackages, _ := cmd.Flags().GetBool("force-packages")
	prune, _ := cmd.Flags().GetBool("prune")
	nonInteractive, _ := cmd.Flags().GetBool("non-interactive")

	r := &engine.Restorer{
		Paths:    e.Paths,
		Log:      e.logger(),
		Now:      e.now,
		Runner:   e.Runner,
		Hostname: e.Hostname,
		Scrubber: e.scrubber(),
	}
	// --headless implies non-interactive: there is nobody at the terminal to answer.
	if !nonInteractive && !e.Headless {
		r.Confirm = e.Confirm
	}

	res, err := r.Restore(cmd.Context(), engine.Options{
		RepoDir:       e.WorkDir,
		ManifestPath:  e.manifestPath(cmd),
		Profiles:      names,
		Identity:      identity,
		DryRun:        dryRun,
		NoPlugins:     noPlugins,
		Sequential:    sequential,
		ForcePackages: forcePackages,
		Prune:         prune,
	})
	if err != nil {
		return err
	}

	if res.DryRun {
		e.heading("Dry run — nothing was changed")
	} else {
		e.heading("Restore complete")
	}
	e.item("transaction: %s", res.TxID)
	e.item("profiles:    %s", strings.Join(res.Profiles, ", "))
	e.item("assets:      %d", res.Assets)
	e.item("secrets:     %d", res.Secrets)
	e.item("packages:    %d", res.Packages)
	if len(res.Skipped) > 0 {
		e.item("skipped:     %s", strings.Join(res.Skipped, ", "))
	}
	if res.LockfilePath != "" {
		e.item("lockfile:    %s", res.LockfilePath)
	}
	return nil
}
