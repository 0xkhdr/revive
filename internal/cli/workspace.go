package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/workspace"
)

func newWorkspaceCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{Use: "workspace", Short: "Manage the registry of known workspaces"}
	cmd.AddCommand(
		newWorkspaceListCommand(env),
		newWorkspaceAddCommand(env),
		newWorkspaceRemoveCommand(env),
		newWorkspaceSyncCommand(env),
	)
	return cmd
}

func newWorkspaceListCommand(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered workspaces",
		RunE: func(*cobra.Command, []string) error {
			cfg, err := workspace.Load(env.Paths.WorkspaceFile)
			if err != nil {
				return err
			}
			if len(cfg.Workspaces) == 0 {
				env.line("no workspaces registered")
				return nil
			}
			rows := make([][]string, 0, len(cfg.Workspaces))
			for _, ws := range cfg.Workspaces {
				marker := ""
				if ws.Name == cfg.DefaultWorkspace {
					marker = "*"
				}
				rows = append(rows, []string{marker, ws.Name, ws.Path, ws.LastAccessed.Format("2006-01-02 15:04")})
			}
			env.table([]string{"", "NAME", "PATH", "LAST ACCESSED"}, rows)
			return nil
		},
	}
}

func newWorkspaceAddCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <path>",
		Short: "Register a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, _ := cmd.Flags().GetString("name")
			ws, err := env.register(args[0], name)
			if err != nil {
				return err
			}
			env.line("registered %s at %s", ws.Name, ws.Path)
			return nil
		},
	}
	cmd.Flags().StringP("name", "n", "", "Name for the workspace; defaults to the directory name")
	return cmd
}

func newWorkspaceRemoveCommand(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Unregister a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := workspace.Load(env.Paths.WorkspaceFile)
			if err != nil {
				return err
			}
			if err := cfg.Remove(args[0]); err != nil {
				return err
			}
			if err := workspace.Save(env.Paths.WorkspaceFile, cfg); err != nil {
				return err
			}
			env.line("unregistered %s (the directory itself was not touched)", args[0])
			return nil
		},
	}
}

func newWorkspaceSyncCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Pull and restore every registered workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			return env.runSync(cmd, args)
		},
	}
	addManifestFlag(cmd)
	addIdentityFlag(cmd)
	addProfileFlag(cmd, env)
	cmd.Flags().Bool("dry-run", false, "Plan and validate; mutate nothing")
	cmd.Flags().Bool("no-plugins", false, "Skip all plugin hooks")
	cmd.Flags().Bool("force-packages", false, "Invalidate the package cache before installing")
	return cmd
}

// runSync updates every registered workspace.
//
// A failure in one workspace MUST NOT stop the others: the whole point is unattended upkeep of
// several machines' worth of config, and one bad repo should not strand the rest.
func (e *Env) runSync(cmd *cobra.Command, args []string) error {
	cfg, err := workspace.Load(e.Paths.WorkspaceFile)
	if err != nil {
		return err
	}
	if len(cfg.Workspaces) == 0 {
		e.line("no workspaces registered")
		return nil
	}

	names := profiles(cmd, args)
	rows := make([][]string, 0, len(cfg.Workspaces))
	failed := false

	for _, ws := range cfg.Workspaces {
		gitResult, restoreResult, detail := "ok", "skipped", ""

		if _, err := e.Git(ws.Path, "pull"); err != nil {
			gitResult, restoreResult, detail = "failed", "skipped", e.scrubber().Scrub(err.Error())
			failed = true
			e.logger().Error("workspace sync: git pull failed", "workspace", ws.Name, "error", err)
			rows = append(rows, []string{ws.Name, ws.Path, gitResult, restoreResult, detail})
			continue
		}

		if len(names) == 0 {
			detail = "no profile given"
			rows = append(rows, []string{ws.Name, ws.Path, gitResult, restoreResult, detail})
			continue
		}

		// Each workspace restores with its own working directory, so relative manifests and
		// .env files resolve against the right repo.
		sub := *e
		sub.WorkDir = ws.Path
		if err := sub.runRestore(cmd, args); err != nil {
			restoreResult, detail = "failed", e.scrubber().Scrub(err.Error())
			failed = true
			e.logger().Error("workspace sync: restore failed", "workspace", ws.Name, "error", err)
		} else {
			restoreResult = "ok"
		}
		rows = append(rows, []string{ws.Name, ws.Path, gitResult, restoreResult, detail})
	}

	e.table([]string{"WORKSPACE", "PATH", "GIT", "RESTORE", "DETAILS"}, rows)
	if failed {
		return fmt.Errorf("%w: at least one workspace failed to sync", ErrOperation)
	}
	return nil
}

// register adds a path to the registry.
func (e *Env) register(path, name string) (workspace.Workspace, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(e.WorkDir, path)
	}
	cfg, err := workspace.Load(e.Paths.WorkspaceFile)
	if err != nil {
		return workspace.Workspace{}, err
	}
	ws, err := cfg.Register(abs, name, e.now())
	if err != nil {
		return workspace.Workspace{}, err
	}
	if err := workspace.Save(e.Paths.WorkspaceFile, cfg); err != nil {
		return workspace.Workspace{}, err
	}
	return ws, nil
}
