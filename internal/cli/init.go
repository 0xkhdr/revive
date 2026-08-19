package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// starterManifest is what `rv init` scaffolds: schema v2, one example asset, empty package
// lists, a base profile.
const starterManifest = `# Revive manifest. See https://github.com/0xkhdr/revive for the full schema.
version: 2

# Files and directories rv manages. ` + "`source`" + ` is relative to this file and must stay inside
# the repository; ` + "`target`" + ` may use ${VAR} and ${VAR:-default}.
assets:
  - id: example_zshrc
    type: symlink            # symlink | copy | template | secret
    source: assets/zshrc
    target: ${HOME}/.zshrc
    permissions: "0644"
    conflict_strategy: prompt  # prompt | overwrite | skip | abort

# age-encrypted files. These always land at mode 0600.
secrets: []

# Packages, by manager. A profile pulls in a whole group by name.
packages:
  brew: []
  apt: []
  flatpak: []
  snap: []
  pacman: []
  dnf: []
  nix: []
  cargo: []
  pip: []
  docker:
    images: []
  node:
    version_file: null
    version: null

profiles:
  base:
    assets: [example_zshrc]
    secrets: []
    packages: []

# Per-host overrides layered on top of a resolved profile.
machine_overrides:
  enabled: true
  path: "machine/{hostname}.yaml"

backup_retention:
  max_count: 10
  max_age_days: 30
`

// skillTemplate documents rv for an AI agent working in the repository.
const skillTemplate = `---
name: rv
description: Manage this machine's configuration with rv.
---

This repository is a Revive workspace. ` + "`manifest.yaml`" + ` declares the files, secrets and
packages that make up the machine's configuration.

- ` + "`rv status -p base`" + ` reports drift between the manifest and the machine.
- ` + "`rv restore base`" + ` applies the repository to the machine, transactionally.
- ` + "`rv doctor`" + ` checks the workspace for problems.

Never commit a plaintext secret. Encrypt it with ` + "`rv secret encrypt`" + ` and declare it
under ` + "`secrets:`" + `.
`

// manifestNames are the files whose presence means "this is already a workspace".
var manifestNames = []string{"manifest.yaml", "manifest-build.yaml", "manifest-restore.yaml"}

func newInitCommand(env *Env) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Scaffold a new workspace in the current directory",
		RunE: func(*cobra.Command, []string) error {
			return env.runInit()
		},
	}
}

func (e *Env) runInit() error {
	// Refusing over an existing workspace is the whole safety property here: scaffolding on top
	// of a real manifest would overwrite the user's declaration.
	for _, name := range manifestNames {
		if _, err := os.Stat(filepath.Join(e.WorkDir, name)); err == nil {
			return fmt.Errorf("%w: %s already exists, so this is already a workspace", ErrUsage, name)
		}
	}

	skillsDir := filepath.Join(e.WorkDir, ".agents", "skills", "rv")
	for _, dir := range []string{"assets", "secrets", "machine"} {
		if err := os.MkdirAll(filepath.Join(e.WorkDir, dir), 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		return fmt.Errorf("creating the agent skills directory: %w", err)
	}

	manifestPath := filepath.Join(e.WorkDir, "manifest.yaml")
	if err := os.WriteFile(manifestPath, []byte(starterManifest), 0o644); err != nil {
		return fmt.Errorf("writing manifest.yaml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(skillTemplate), 0o644); err != nil {
		return fmt.Errorf("writing the agent skill: %w", err)
	}

	e.heading("Workspace created")
	e.item("manifest.yaml          your declaration")
	e.item("assets/                plaintext files to manage")
	e.item("secrets/               age-encrypted files")
	e.item("machine/               per-host overrides")
	e.item(".agents/skills/rv/     agent skill documentation")
	e.line("")
	e.heading("Next steps")
	e.item("add a file under assets/ and declare it in manifest.yaml")
	e.item("rv secret keygen --output %s", e.Paths.IdentityFile)
	e.item("rv doctor")
	e.item("rv restore base --dry-run")
	return nil
}
