package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/engine"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/plugins"
	"github.com/0xkhdr/revive/internal/recovery"
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
	cmd.Flags().Bool("json", false, "With --preview, emit the drift report as JSON")
	cmd.MarkFlagsMutuallyExclusive("interactive", "non-interactive")
	cmd.MarkFlagsMutuallyExclusive("parallel", "sequential")
	cmd.MarkFlagsMutuallyExclusive("dry-run", "preview")
	return cmd
}

// restorer builds the engine with every seam wired: recovery refuses to start on top of an
// unrecovered transaction, and pruning runs after a successful restore.
func (e *Env) restorer() *engine.Restorer {
	return &engine.Restorer{
		Paths:        e.Paths,
		Log:          e.logger(),
		Now:          e.now,
		Runner:       e.Runner,
		Hostname:     e.Hostname,
		Scrubber:     e.scrubber(),
		RequireClean: e.recovery().EnsureClean,
		Plugins:      e.pluginRunner(),
	}
}

// pluginRunner dispatches profile-level plugin hooks. The engine skips it entirely under
// --no-plugins and --dry-run, so this only has to translate the hook into a plugin context.
func (e *Env) pluginRunner() engine.PluginRunner {
	runner := &plugins.Runner{
		Loader: &plugins.Loader{
			// Precedence: workspace-local before user-global, first name wins.
			Dirs: []string{filepath.Join(e.WorkDir, "plugins"), e.Paths.PluginsDir},
			Log:  e.logger(),
		},
		Log: e.logger(),
	}
	return func(ctx context.Context, hook engine.PluginHook) error {
		_, err := runner.Run(ctx, plugins.Context{
			RepoDir:     hook.RepoDir,
			ProfileName: strings.Join(hook.Profiles, ","),
			// v1.0 never invokes a plugin on a dry run; the field exists for protocol stability.
			DryRun:   false,
			Targets:  hook.Targets,
			HookType: plugins.Stage(hook.Stage),
		})
		return err
	}
}

// retentionFor reads the retention policy from a manifest, falling back to the documented
// defaults when it cannot be read.
func (e *Env) retentionFor(manifestPath string) recovery.Retention {
	policy := recovery.Retention{MaxCount: 10, MaxAgeDays: 30}
	if m, err := manifest.Load(manifestPath); err == nil {
		policy.MaxCount, policy.MaxAgeDays = m.Retention()
	}
	return policy
}

func (e *Env) runRestore(cmd *cobra.Command, args []string) error {
	names := profiles(cmd, args)
	if len(names) == 0 {
		return fmt.Errorf("%w: name at least one profile", ErrUsage)
	}

	// --preview is the status engine, not a restore: it reports the current difference rather
	// than exercising the planner. Use --preview to decide whether to restore, --dry-run to
	// check that a restore would succeed.
	preview, _ := cmd.Flags().GetBool("preview")
	if preview {
		report, err := e.status(cmd, args)
		if err != nil {
			return err
		}
		return e.renderStatus(cmd, report)
	}

	identity, _ := cmd.Flags().GetString("identity")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	noPlugins, _ := cmd.Flags().GetBool("no-plugins")
	sequential, _ := cmd.Flags().GetBool("sequential")
	forcePackages, _ := cmd.Flags().GetBool("force-packages")
	prune, _ := cmd.Flags().GetBool("prune")
	nonInteractive, _ := cmd.Flags().GetBool("non-interactive")

	manifestPath := e.manifestPath(cmd)
	r := e.restorer()
	// Pruning is automatic after every successful restore, per backup_retention.
	r.Prune = func(context.Context) error {
		_, err := e.recovery().Prune(e.retentionFor(manifestPath), false)
		return err
	}
	// --headless implies non-interactive: there is nobody at the terminal to answer.
	if !nonInteractive && !e.Headless {
		r.Confirm = e.Confirm
	}

	res, err := r.Restore(cmd.Context(), engine.Options{
		RepoDir:       e.WorkDir,
		ManifestPath:  manifestPath,
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
