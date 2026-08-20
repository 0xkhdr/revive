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
description: Manage files, encrypted secrets, packages, profiles, and machine drift in a Revive workspace containing manifest.yaml.
---

# Revive workspace

Treat ` + "`manifest.yaml`" + ` and its repository-relative sources as desired state. Run ` + "`rv`" + ` from the
workspace root. Read the manifest to select the relevant profile; do not assume ` + "`base`" + ` exists.
Use ` + "`-m <manifest>`" + ` when the workspace uses a non-default manifest.

## Direction matters

- ` + "`rv restore <profile>`" + ` copies repository state to the machine.
- ` + "`rv backup <profile>`" + ` copies current machine state into the repository. Templates are skipped.

For a requested configuration change, edit the source declared in ` + "`manifest.yaml`" + `, not its target.
Use ` + "`backup`" + ` only when the user wants to import an existing target-side change. Inspect its dry run
first and review the resulting repository diff.

## Inspect and validate

Prefer non-interactive diagnostics that cannot prompt:

` + "```sh" + `
rv --headless doctor -p <profile> --json
rv --headless status -p <profile> --json
rv --headless diff -p <profile> --unified
` + "```" + `

After editing the manifest, an asset, template, or encrypted secret, run ` + "`doctor`" + ` and then:

` + "```sh" + `
rv --headless restore <profile> --dry-run --non-interactive
` + "```" + `

A dry run does not change targets, create snapshots, run hooks, or write the lockfile. A real
restore changes the machine and can run package managers, hooks, and plugins; run it only when the
user explicitly asks to apply the change. Do not use ` + "`--no-plugins`" + ` or ` + "`--force-packages`" + ` unless
diagnosing the corresponding subsystem. Treat a non-interactive prompt conflict as a finding; do
not weaken its conflict strategy merely to make validation pass.

If Revive reports an incomplete transaction, stop normal operations and ask before running
` + "`rv recover`" + `; do not delete journals or backups manually. Recovery changes machine state, and
rollback cannot reverse hook, plugin, package-manager, service, or network side effects.

## Manifest and secrets

- Keep asset sources inside the repository and targets in ` + "`manifest.yaml`" + `; use machine overrides
  only for genuine host differences.
- Preserve strict schema types, quoted permission modes such as ` + "`\"0644\"`" + `, profile inheritance, and
  ` + "`${VAR}`" + ` / ` + "`${VAR:-default}`" + ` target syntax.
- Never print, commit, or stage plaintext secrets or age identities. Commit only encrypted ` + "`.age`" + `
  files and declare them under ` + "`secrets:`" + ` with restrictive permissions.
- Prefer normal restore/backup flows for secrets. If explicit encryption is required, use
  ` + "`rv secret encrypt <plaintext> -o <destination>.age -r <recipient>`" + ` and remove the plaintext
  safely after verifying the ciphertext; never decrypt to stdout or into the repository.

Before handoff, review ` + "`git diff`" + ` and ` + "`git status`" + ` for unexpected generated state, identities,
plaintext secrets, ` + "`.env`" + ` files, or ` + "`manifest*.lock`" + ` files.
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
