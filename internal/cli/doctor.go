package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/0xkhdr/revive/internal/doctor"
	"github.com/0xkhdr/revive/internal/platform"
)

func newDoctorCommand(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run health checks on the workspace and the machine",
		RunE: func(cmd *cobra.Command, args []string) error {
			return env.runDoctor(cmd, args)
		},
	}
	addProfileFlag(cmd, env)
	addManifestFlag(cmd)
	cmd.Flags().Bool("json", false, "Emit the structured report for CI")
	return cmd
}

func (e *Env) runDoctor(cmd *cobra.Command, args []string) error {
	d := &doctor.Doctor{
		RepoDir:      e.WorkDir,
		ManifestPath: e.manifestPath(cmd),
		Profiles:     profiles(cmd, args),
		Paths:        e.Paths,
		Platform:     e.platform(),
		Hostname:     e.Hostname,
	}
	report := d.Run()

	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		e.line("%s", raw)
	} else {
		e.renderDoctor(report)
	}

	// Exit 1 when unhealthy is what makes doctor usable as a CI gate.
	if !report.Healthy {
		return fmt.Errorf("%w: %d issue(s)", doctor.ErrUnhealthy, len(report.Issues))
	}
	return nil
}

func (e *Env) renderDoctor(report *doctor.Report) {
	if len(report.Issues) == 0 {
		e.line("%d checks run, no issues found", report.ChecksRun)
		return
	}

	// Grouped by category, in the order the issues were found, so the report reads the way the
	// checks run rather than in map order.
	seen := map[string]bool{}
	var order []string
	for _, issue := range report.Issues {
		if !seen[issue.Category] {
			seen[issue.Category] = true
			order = append(order, issue.Category)
		}
	}

	for _, category := range order {
		e.heading(category)
		for _, issue := range report.Issues {
			if issue.Category == category {
				e.item("[%s] %s", issue.Severity, issue.Message)
			}
		}
	}

	e.line("")
	e.line("%d checks run, %d issue(s)", report.ChecksRun, len(report.Issues))
	if !report.Healthy {
		e.line("at least one issue is critical; fix those before restoring")
	}
}

func (e *Env) platform() *platform.Detector {
	if e.Platform != nil {
		return e.Platform
	}
	return platform.Default
}
