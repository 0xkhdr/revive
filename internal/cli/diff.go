package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/status"
)

// diffWidth is the terminal width assumed for side-by-side rendering. rv does not query the
// terminal: the output is as often piped as read, and a fixed width keeps it reproducible.
const diffWidth = 160

func newDiffCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "diff",
		Short:             "Show content diffs for modified assets",
		ValidArgsFunction: env.completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			return env.runDiff(cmd, args)
		},
	}
	addProfileFlag(cmd, env)
	addIdentityFlag(cmd)
	addManifestFlag(cmd)
	cmd.Flags().BoolP("unified", "u", false, "Unified diff instead of side-by-side")
	return cmd
}

func (e *Env) runDiff(cmd *cobra.Command, args []string) error {
	checker, resolved, err := e.checker(cmd, args)
	if err != nil {
		return err
	}
	unified, _ := cmd.Flags().GetBool("unified")
	report := checker.Check(resolved)

	shown := 0
	for _, result := range report.Results {
		if result.Status != status.Modified {
			continue
		}
		asset, ok := resolved.Assets[result.AssetID]
		if !ok {
			secret, found := resolved.Secrets[result.AssetID]
			if !found {
				continue
			}
			asset = secret.Asset()
		}

		d, err := checker.DiffAsset(asset, result.Target)
		if errors.Is(err, status.ErrNoDiff) {
			e.logger().Debug("skipping diff", "asset_id", result.AssetID, "reason", err)
			continue
		}
		if err != nil {
			return err
		}

		shown++
		e.heading(result.AssetID)
		if unified {
			e.line("%s", d.Unified(e.scrubber()))
		} else {
			e.line("%s", d.SideBySide(e.scrubber(), diffWidth))
		}
	}

	if shown == 0 {
		e.line("no differences to show")
	}
	return nil
}
