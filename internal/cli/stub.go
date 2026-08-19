package cli

import "github.com/spf13/cobra"

// stub builds a placeholder command. Flags are declared by the phase that implements the
// command's body, so no flag exists without code that reads it.
func stub(use, short string, sub ...*cobra.Command) *cobra.Command {
	c := &cobra.Command{
		Use:   use,
		Short: short,
		RunE:  func(*cobra.Command, []string) error { return ErrNotImplemented },
	}
	c.AddCommand(sub...)
	return c
}

// stubCommands is the full command surface from docs/03-cli-spec.md, minus the deferred `gui`.
func stubCommands() []*cobra.Command {
	return []*cobra.Command{
		stub("init", "Scaffold a new workspace in the current directory"),
		stub("clone <repo-url> [dest]", "Clone a workspace, register it, optionally restore"),
		stub("restore <profile...>", "Apply the repo to the machine, transactionally"),
		stub("backup <profile...>", "Copy machine state back into the repo"),
		stub("status", "Report drift between the manifest and the machine"),
		stub("diff", "Show content diffs for modified assets"),
		stub("doctor", "Run health checks on the workspace and the machine"),
		stub("watch", "Auto-restore on workspace changes"),
		stub("recover", "Roll back or discard interrupted transactions"),
		stub("prune", "Delete old transaction backup snapshots"),
		stub("secret", "Manage age keys and encrypted files",
			stub("keygen", "Generate an age keypair"),
			stub("encrypt <file>", "Encrypt a file to one or more recipients"),
			stub("decrypt <file>", "Decrypt a file with an identity"),
			stub("rotate <file>", "Re-encrypt a secret to new recipients"),
		),
		stub("workspace", "Manage the registry of known workspaces",
			stub("list", "List registered workspaces"),
			stub("add <path>", "Register a workspace"),
			stub("remove <name>", "Unregister a workspace"),
			stub("sync", "Pull and restore every registered workspace"),
		),
		stub("self-uninstall", "Remove the installed binary and optionally the config"),
	}
}
