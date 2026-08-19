package cli

import (
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newCloneCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clone <repo-url> [dest]",
		Short: "Clone a workspace, register it, and optionally restore",
		Long:  "The fresh-machine bootstrap path: git clone, register, and optionally restore in one step.",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return env.runClone(cmd, args)
		},
	}
	cmd.Flags().StringSliceP("restore", "r", nil, "Restore this profile immediately after cloning")
	addIdentityFlag(cmd)
	addManifestFlag(cmd)
	return cmd
}

func (e *Env) runClone(cmd *cobra.Command, args []string) error {
	url := args[0]
	dest := defaultCloneDest(url)
	if len(args) == 2 {
		dest = args[1]
	}
	absDest := dest
	if !filepath.IsAbs(absDest) {
		absDest = filepath.Join(e.WorkDir, dest)
	}

	if _, err := e.Git(e.WorkDir, "clone", url, absDest); err != nil {
		return err
	}
	e.line("cloned %s into %s", url, absDest)

	ws, err := e.register(absDest, "")
	if err != nil {
		return err
	}
	e.line("registered workspace %s", ws.Name)

	restoreProfiles, _ := cmd.Flags().GetStringSlice("restore")
	if len(restoreProfiles) == 0 {
		e.line("run `cd %s && rv restore <profile>` when you are ready", absDest)
		return nil
	}

	// The restore runs inside the clone, so relative manifests and its .env resolve there.
	sub := *e
	sub.WorkDir = absDest
	return sub.runRestore(cmd, restoreProfiles)
}

// defaultCloneDest is the repository name, the same default git itself uses.
func defaultCloneDest(url string) string {
	trimmed := strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git")
	if idx := strings.LastIndexAny(trimmed, "/:"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	if trimmed == "" {
		return "workspace"
	}
	return trimmed
}
