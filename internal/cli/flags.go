package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/profile"
)

// Common flags shared by many commands, declared in one place so their names and shorthands
// cannot drift apart.

func addManifestFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("manifest", "m", "manifest.yaml", "Manifest to use; relative paths resolve against the workspace")
}

func addIdentityFlag(cmd *cobra.Command) {
	cmd.Flags().StringP("identity", "i", "", "age identity file")
}

func addProfileFlag(cmd *cobra.Command, env *Env) {
	cmd.Flags().StringSliceP("profile", "p", nil, "Profile(s); repeatable and comma-splittable")
	_ = cmd.RegisterFlagCompletionFunc("profile", env.completeProfiles)
}

// manifestPath resolves -m against the workspace.
func (e *Env) manifestPath(cmd *cobra.Command) string {
	value, _ := cmd.Flags().GetString("manifest")
	if value == "" {
		value = "manifest.yaml"
	}
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(e.WorkDir, value)
}

// profiles merges positional arguments with every -p flag.
//
// `-p base -p work`, `-p base,work` and `rv restore base work` all normalize to the same list.
func profiles(cmd *cobra.Command, args []string) []string {
	flagValues, _ := cmd.Flags().GetStringSlice("profile")
	return profile.ParseNames(append(append([]string{}, args...), flagValues...)...)
}

// completeProfiles completes profile names from the workspace's manifest, honoring a -m flag
// that appeared earlier on the command line.
func (e *Env) completeProfiles(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	m, err := manifest.Load(e.manifestPath(cmd))
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(m.Profiles))
	for name := range m.Profiles {
		names = append(names, name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
