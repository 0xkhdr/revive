package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newSelfUninstallCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "self-uninstall",
		Short: "Remove the installed binary, and optionally the configuration",
		Long: "Removes the rv binary. With --purge-config it also removes ~/.config/rv, which " +
			"contains your age identity and any unrecovered transaction journals.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return env.runSelfUninstall(cmd)
		},
	}
	cmd.Flags().BoolP("force", "f", false, "Do not ask for confirmation")
	cmd.Flags().Bool("purge-config", false, "Also remove ~/.config/rv")
	return cmd
}

func (e *Env) runSelfUninstall(cmd *cobra.Command) error {
	force, _ := cmd.Flags().GetBool("force")
	purge, _ := cmd.Flags().GetBool("purge-config")

	binary, err := e.executable()
	if err != nil {
		return fmt.Errorf("locating the rv binary: %w", err)
	}

	e.heading("This will remove")
	e.item("%s", binary)
	if purge {
		e.item("%s (age identity, journals, backups, workspace registry)", e.Paths.ConfigDir)
	}

	if purge {
		// The identity is the only thing that can decrypt this user's secrets. If it is not
		// also in a password manager or a second machine, deleting it loses them permanently.
		if _, err := os.Stat(e.Paths.IdentityFile); err == nil {
			e.line("")
			e.line("WARNING: %s is your age identity. Without it, every secret in every "+
				"workspace becomes permanently undecryptable. Back it up before continuing.",
				e.Paths.IdentityFile)
		}
		if pending, err := e.recovery().Scan(); err == nil && len(pending) > 0 {
			e.line("")
			e.line("WARNING: %d interrupted transaction(s) have not been recovered. Purging the "+
				"config directory deletes their backups, and those files cannot be restored "+
				"afterwards. Run `rv recover` first.", len(pending))
		}
	}

	if !force {
		ok, err := e.Confirm("Continue?")
		if err != nil {
			return err
		}
		if !ok {
			e.line("nothing was removed")
			return nil
		}
	}

	if purge {
		if err := os.RemoveAll(e.Paths.ConfigDir); err != nil {
			return fmt.Errorf("removing %s: %w", e.Paths.ConfigDir, err)
		}
		e.line("removed %s", e.Paths.ConfigDir)
	}
	// The binary goes last: if removing the config fails, rv is still there to try again.
	if err := os.Remove(binary); err != nil {
		return fmt.Errorf("removing %s: %w", binary, err)
	}
	e.line("removed %s", binary)
	e.line("")
	e.line("Your workspaces were not touched. Reinstall with `go install github.com/0xkhdr/revive/cmd/rv@latest`.")
	return nil
}

// executable resolves the running binary. It is a field on Env so tests do not have to delete
// the test binary to prove the command works.
func (e *Env) executable() (string, error) {
	if e.Executable != "" {
		return e.Executable, nil
	}
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}
