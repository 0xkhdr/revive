package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/engine"
	"github.com/0xkhdr/revive/internal/lockfile"
	"github.com/0xkhdr/revive/internal/manifest"
	"github.com/0xkhdr/revive/internal/profile"
	"github.com/0xkhdr/revive/internal/status"
)

func newStatusCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:               "status",
		Short:             "Report drift between the manifest and the machine",
		ValidArgsFunction: env.completeProfiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := env.status(cmd, args)
			if err != nil {
				return err
			}
			return env.renderStatus(cmd, report)
		},
	}
	addProfileFlag(cmd, env)
	addIdentityFlag(cmd)
	addManifestFlag(cmd)
	cmd.Flags().Bool("json", false, "Emit the report as JSON for CI")
	return cmd
}

// checker builds the drift engine for a command's flags.
func (e *Env) checker(cmd *cobra.Command, args []string) (*status.Checker, *profile.Resolved, error) {
	names := profiles(cmd, args)
	if len(names) == 0 {
		return nil, nil, fmt.Errorf("%w: name at least one profile", ErrUsage)
	}

	manifestPath := e.manifestPath(cmd)
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := profile.Resolve(m, names...)
	if err != nil {
		return nil, nil, err
	}
	if err := profile.ApplyOverrides(m, resolved, e.WorkDir, e.Hostname); err != nil {
		return nil, nil, err
	}

	h := engine.NewHandler(e.WorkDir, e.Paths)
	if e.Hostname != "" {
		h.Hostname = e.Hostname
	}
	// An identity is optional here: without one, encrypted assets fall back to the lockfile
	// mtime rather than failing the whole report.
	identityFlag, _ := cmd.Flags().GetString("identity")
	if identity, err := e.resolveIdentity(identityFlag); err == nil {
		h.Identity = identity
	} else if identityFlag != "" {
		return nil, nil, err
	}

	lf, err := lockfile.LoadOrEmpty(lockfile.PathFor(manifestPath))
	if err != nil {
		e.logger().Warn("lockfile could not be read", "error", err)
	}
	return status.New(h, lf), resolved, nil
}

func (e *Env) status(cmd *cobra.Command, args []string) (*status.Report, error) {
	checker, resolved, err := e.checker(cmd, args)
	if err != nil {
		return nil, err
	}
	return checker.Check(resolved), nil
}

func (e *Env) renderStatus(cmd *cobra.Command, report *status.Report) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		e.line("%s", raw)
		return nil
	}

	rows := make([][]string, 0, len(report.Results))
	for _, r := range report.Results {
		rows = append(rows, []string{r.AssetID, string(r.Status), r.Target, r.Detail})
	}
	e.table([]string{"ASSET", "STATUS", "TARGET", "DETAILS"}, rows)

	if report.Drifted {
		e.line("")
		e.line("drift detected; run `rv diff` to see what changed, or `rv restore` to fix it")
	} else {
		e.line("")
		e.line("everything is in sync")
	}
	return nil
}
