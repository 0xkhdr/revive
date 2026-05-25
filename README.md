# Revive (`rv`) — Developer Environment Lifecycle Manager

Revive (`rv`) is a transaction-safe developer environment manager. It synchronizes your dotfiles, application configs, encrypted secrets, system packages, and AI agent skills directly from your Git repository. To guarantee system stability, Revive operates on a strict transactional model: if any symlink, copy, package installation, or plugin hook fails, the entire run is rolled back, ensuring your machine is never left in a broken, half-configured state.

---

## Table of Contents

- [Installation](#-installation)
  - [1-Second Install (Linux/macOS)](#1-second-install-linuxmacos)
  - [Manual Install from Source](#manual-install-from-source)
  - [Uninstall](#uninstall)
- [Core Concepts](#-core-concepts)
- [Quick Start](#-quick-start)
- [manifest.yaml Reference](#-manifestyaml-reference)
  - [Assets](#assets)
  - [Secrets](#secrets)
  - [Packages](#packages)
  - [Profiles](#profiles)
  - [Machine Overrides](#machine-overrides)
- [CLI Command Reference](#-cli-command-reference)
  - [Global Flags](#global-flags)
  - [`rv init`](#rv-init)
  - [`rv restore`](#rv-restore)
  - [`rv backup`](#rv-backup)
  - [`rv status`](#rv-status)
  - [`rv diff`](#rv-diff)
  - [`rv doctor`](#rv-doctor)
  - [`rv watch`](#rv-watch)
  - [`rv recover`](#rv-recover)
  - [`rv gui`](#rv-gui)
  - [`rv secret`](#rv-secret-subcommands)
  - [`rv workspace`](#rv-workspace-subcommands)
  - [`rv self-install`](#rv-self-install)
  - [`rv self-uninstall`](#rv-self-uninstall)
- [Plugin System](#-plugin-system)
  - [Built-in Plugins](#built-in-plugins)
  - [Writing a Custom Plugin](#writing-a-custom-plugin)
  - [Plugin Discovery Order](#plugin-discovery-order)
  - [Plugin Security Sandbox](#plugin-security-sandbox)
- [Security Model](#-security-model)
- [Transaction & Rollback Engine](#-transaction--rollback-engine)
- [Development Guide](#-development-guide)
  - [Setup](#setup)
  - [Running Tests](#running-tests)
  - [Code Quality](#code-quality)
  - [Extending Revive](#extending-revive)

---

## 📦 Installation

### 1-Second Install (Linux/macOS)

Installs `rv` globally to `~/.local/bin/rv` with an isolated virtual environment at `~/.local/share/rv`:

```bash
curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/scripts/install.sh | sh
```

> [!NOTE]
> Make sure `~/.local/bin` is in your `PATH`. If not, add to your shell profile (e.g. `~/.bashrc` or `~/.zshrc`):
> ```bash
> export PATH="$HOME/.local/bin:$PATH"
> ```

**Install script options:**

| Option | Description |
|--------|-------------|
| `--force` | Recreate the venv and overwrite `~/.local/bin/rv` |
| `--system-deps` | Best-effort install of Python/venv/pip/age via the system package manager |
| `-h`, `--help` | Show help |

**Install script environment variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `REVIVE_INSTALL_DIR` | `~/.local/share/rv` | Root installation directory |
| `REVIVE_BIN_DIR` | `~/.local/bin` | Directory to place the `rv` wrapper |
| `REVIVE_SOURCE_URL` | `https://github.com/0xkhdr/revive.git` | Git URL for streamed installs |
| `REVIVE_SOURCE_REF` | `main` | Git branch/tag/ref |
| `PYTHON` | auto-detected | Python 3.11+ executable to use |

### Manual Install from Source

```bash
git clone https://github.com/0xkhdr/revive.git && cd revive
python3 -m venv .venv && source .venv/bin/activate
pip install -e ".[dev]"
```

### Uninstall

```bash
# Using the CLI (recommended)
rv self-uninstall --purge-config

# Or via remote script
curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/scripts/uninstall.sh | sh
```

---

## 🧠 Core Concepts

| Concept | Description |
|---------|-------------|
| **Unidirectional Sync** | Primary flow: state flows `repository → system`. Git commits are the source of truth; `rv restore` applies them. |
| **Bidirectional Capability** | `rv backup` captures live system files and secrets back into the repository (`system → repo`). Use this to preserve dotfile edits made directly on the system before committing. |
| **Profile** | A named set of assets, secrets, and packages that can be restored as a unit. |
| **Asset** | A file/directory managed as a symlink, copy, or Jinja2 template. |
| **Secret** | An age-encrypted file decrypted directly to memory during restore, never written to disk in plaintext. |
| **Transaction** | A 7-step atomic execution context. Every mutation is journaled; any failure triggers a full rollback. |
| **Machine Overrides** | Host-specific `manifest.yaml` fragments that override values at restore time. |
| **Plugin** | A sandboxed Python script invoked on `pre-restore` or `post-restore` hooks. |

### Typical Bidirectional Workflow

```bash
# On machine A: edit dotfiles directly, then capture changes back into the repo
rv backup base --identity ~/.config/rv/identity.txt
git add -A && git commit -m "chore: update dotfiles" && git push

# On machine B: pull and apply
git pull
rv restore base --identity ~/.config/rv/identity.txt
```

---

## 🚀 Quick Start

```bash
# 1. Create your dotfiles repo and scaffold it
mkdir -p ~/dotfiles && cd ~/dotfiles
rv init

# 2. Move your zshrc into assets and declare it
mv ~/.zshrc assets/zshrc

# 3. Edit manifest.yaml to declare the asset under a profile
# (See manifest.yaml Reference below)

# 4. Preview what will happen
rv restore base --dry-run

# 5. Apply the restore
rv restore base

# 6. Check for drift later
rv status -p base
```

---

## 📄 `manifest.yaml` Reference

The `manifest.yaml` at the root of your repository is the single source of truth. Revive validates it strictly using Pydantic v2 models on every operation.

```yaml
version: 2

assets: []      # Global pool of file assets
secrets: []     # Global pool of encrypted secrets
packages: {}    # Package manager declarations
profiles: {}    # Named restore profiles
machine_overrides:
  enabled: true
  path: "machine/{hostname}.yaml"
```

### Assets

Assets represent files and directories to manage. Supported types: `symlink`, `copy`, `template`.

```yaml
assets:
  - id: my_zshrc
    type: symlink          # symlink | copy | template
    source: assets/zshrc   # Relative to repository root (no '..' traversal)
    target: ~/.zshrc       # System destination. Supports ${VAR:-default} interpolation
    permissions: "0644"    # 4-digit octal string (e.g. "0644", "0755")
    owner: null            # Owner username; null = current user
    conflict_strategy: prompt  # prompt | overwrite | skip | abort
    template_vars:         # Only for type: template (Jinja2 rendering)
      MY_VAR: hello
```

**Asset types:**

| Type | Behavior |
|------|----------|
| `symlink` | Creates a symlink at `target` pointing to the source in the repo |
| `copy` | Atomically copies the file/directory to `target` |
| `template` | Renders a Jinja2 template with `template_vars` before copying |

**Conflict strategies:**

| Strategy | Behavior |
|----------|----------|
| `prompt` | Interactively ask the user (default) |
| `overwrite` | Silently replace the existing target |
| `skip` | Leave the existing target untouched |
| `abort` | Abort the entire restore on conflict |

**Target arrays (multi-destination):**

A single asset can copy to multiple targets. When `source` is a directory and `target` is a list, Revive automatically matches each target's basename to a child in the source directory:

```yaml
assets:
  - id: my_configs
    type: copy
    source: assets/my-app
    target:
      - /etc/my-app/config.toml
      - /etc/my-app/compose/
    permissions: "0644"
    conflict_strategy: overwrite
```

> [!TIP]
> **Sub-item resolution:** If `source` is a directory and a target basename matches a child file/folder within it, only that child is copied to the target. This allows one source directory to fan-out to many destinations.

### Secrets

Secrets are age-encrypted files. They are decrypted to an in-memory buffer during restore and written to the target path, then the buffer is zeroed. **Plaintext is never written to disk or logs.**

Like assets, secrets support **target arrays** (multiple destinations). If `source` is a directory and `target` is a list of paths, Revive matches each target path's basename with the corresponding `.age` file in the source directory during restore (or appends `.age` to decrypt it).

```yaml
secrets:
  - id: aws_credentials
    source: secrets/aws_creds       # Can be a directory containing encrypted files
    target:
      - ~/.aws/credentials          # Decrypted from secrets/aws_creds/credentials.age
      - ~/.aws/credentials.deploy   # Decrypted from secrets/aws_creds/credentials.deploy.age
    permissions: "0600"             # Must be group/world restrictive (no 077 bits)
    owner: null
```

> [!IMPORTANT]
> * **Permissions**: Secrets enforce strict permissions. The `permissions` field must restrict group and world access (e.g. `"0600"` or `"0700"`). Setting world-readable permissions will fail Pydantic validation.
> * **Bidirectional Backup**: During `rv backup`, if a secret has multiple targets, the live files on the system are encrypted to separate files under the secret's repository directory with a `.age` suffix (e.g., `credentials` -> `credentials.age`).

### Packages

Declare system-level packages to install. Each package manager is optional:

```yaml
packages:
  brew:
    - ripgrep
    - fzf
    - starship
  apt:
    - curl
    - git
    - build-essential
  pacman:
    - ripgrep
    - starship
  dnf:
    - curl
  nix:
    - ripgrep
  cargo:
    - ripgrep
  pip:
    - black
  flatpak:
    - com.spotify.Client
  snap:
    - nvim
  docker:
    images:
      - postgres:16
      - redis:alpine
  node:
    version_file: .nvmrc       # Use a version file
    # version: "20.11.0"       # Or specify explicitly
```

### Profiles

Profiles reference the global asset/secret/package pools by ID. Profiles can extend other profiles (inheritance):

```yaml
profiles:
  base:
    assets:
      - my_zshrc
      - my_gitconfig
    secrets:
      - aws_credentials
    packages:
      - apt
      - brew

  work:
    extends:
      - base              # Inherits all of 'base'
    assets:
      - work_ssh_config
    secrets:
      - work_vpn_key
    packages:
      - docker
```

> [!TIP]
> Profile inheritance is resolved recursively. You can chain as many levels as needed.

### Machine Overrides

Place machine-specific YAML files in `machine/` to override manifest values per host:

```yaml
# machine_overrides in manifest.yaml (default)
machine_overrides:
  enabled: true
  path: "machine/{hostname}.yaml"  # {hostname} is resolved at runtime
```

Example override file at `machine/my-work-laptop.yaml`:

```yaml
# Overrides applied only on this machine
packages:
  apt:
    - libssl-dev
    - docker.io
```

---

## 💻 CLI Command Reference

### Global Flags

These flags apply to **all** commands and must appear before the subcommand:

```bash
rv [--verbose] [--headless] <command> [options]
```

| Flag | Description |
|------|-------------|
| `--verbose`, `-v` | Enable verbose debug logging |
| `--headless` | CI/headless mode: raw stream logs, no Rich styling |

---

### `rv init`

Scaffold a new Revive repository in the current directory. It automatically initializes a new local Git repository (`git init`) and stages/commits the generated files (or stages them if a Git identity is not yet configured).

```bash
rv init
```

Creates:
- `manifest.yaml` — your default configuration manifest
- `manifest-build.yaml` — your build/development configuration manifest
- `manifest-restore.yaml` — your restore/system configuration manifest
- `assets/` — folder for managed files and templates
- `secrets/` — folder for encrypted `.age` files
- `machine/` — folder for host-specific override YAMLs
- `.agents/skills/` — folder for AI agent skill definitions (with a default `rv/SKILL.md` skill template)
- `AGENTS.md` — instructions for AI agents working in the repository
- `README.md` — basic project documentation template
- `.gitignore` — repository ignores
- `.env` & `.env.example` — environment variables for interpolation (Revive automatically loads `.env` on CLI entry)

Also registers the current directory as a workspace in `~/.config/rv/workspaces.yaml`.

> [!NOTE]
> Running `rv init` in a directory that already contains manifest.yaml, manifest-build.yaml, or manifest-restore.yaml will exit with an error.

---

### `rv restore`

Synchronize the local system state to match the repository profile (`repo → system`).

```bash
rv restore <profile> [<profile2> ...] [options]
```

| Argument/Flag | Description |
|---------------|-------------|
| `<profile>` | **Required.** Name(s) of the profile(s) to restore. Multiple profiles or comma-separated values are accepted (e.g. `rv restore base work` or `rv restore base,work`). |
| `--identity`, `-i <path>` | Path to age identity file for decrypting secrets |
| `--dry-run` | Plan and validate without mutating the filesystem |
| `--interactive` / `--non-interactive` | Toggle interactive prompting for file conflicts (default: interactive) |
| `--no-plugins` | Skip all plugin hook execution |
| `--force-packages` | Bypass and invalidate the package status cache to force package reinstalls |
| `--preview` | Show a beautiful color-coded summary of system/repo differences without applying changes |
| `--parallel` / `--sequential` | Controls parallel planning of assets (ThreadPoolExecutor max 8 threads, default: parallel) |
| `--prune` | Perform automatic retention-based pruning of old backup snapshots |
| `--manifest`, `-m <path>` | Path to a custom manifest file (e.g. `manifest-build.yaml`) |

**Examples:**

```bash
# Preview changes without applying them
rv restore base --dry-run

# Full restore with secrets
rv restore base --identity ~/.config/rv/identity.txt

# CI/non-interactive mode
rv restore base --identity ~/.config/rv/identity.txt --non-interactive

# Restore without running plugin hooks
rv restore base --no-plugins
```

The 14-step restore process runs atomically. Any failure triggers a full rollback:

1. **Process Lock** — prevents concurrent restores
2. **Manifest Validation** — Pydantic v2 strict validation
3. **Profile Resolution** — resolves inheritance chain
4. **Machine Overrides Merge** — applies host-specific overrides
5. **Dependency Verification** — checks required tools
6. **Secret Decryption** — decrypts secrets to memory
7. **Pre-Restore Hooks** — runs plugins subscribed to `pre-restore`
8. **Backup Snapshot** — journals current state to `~/.config/rv/backups/`
9. **Atomic Symlinks, Copies & Permissions** — mutates filesystem
10. **Native Package Orchestration** — installs declared packages
11. **Post-Restore Hooks** — runs plugins subscribed to `post-restore`
12. **Post-Apply Verification** — checksums & permission comparison
13. **manifest.lock Update** — records committed state
14. **Structured Audit Log** — writes JSON audit entry

---

### `rv status`

Compare system state against the repository profile and report drift.

```bash
rv status --profile <profile> [options]
```

| Flag | Description |
|------|-------------|
| `--profile`, `-p <profile>` | **Required.** Profile(s) to evaluate. Accepts multiple profile names or comma-separated values (can be provided multiple times, e.g. `-p base -p work` or `-p base,work`) |
| `--identity`, `-i <path>` | Age identity file to also check secret drift |
| `--manifest`, `-m <path>` | Path to a custom manifest file (e.g. `manifest-build.yaml`) |

**Drift status values:**

| Status | Meaning |
|--------|---------|
| `In Sync` | Target matches repository source |
| `Missing` | Target path does not exist on the system |
| `Modified` | File content differs from repository source |
| `Permissions Mismatch` | File exists and matches but permissions differ |
| `Type Mismatch` | Expected a symlink but found a regular file (or vice versa) |

**Example:**

```bash
rv status -p base
rv status -p base --identity ~/.config/rv/identity.txt
```

Exit code is `0` whether in sync or drifted (drift is reported as a warning). Use `rv diff` for file-level diffs. For CI drift gating (exit `1` on issues), use `rv doctor`.

---

### `rv diff`

Print colored diffs of all modified assets between the repository and the live system.

```bash
rv diff --profile <profile> [options]
```

| Flag | Description |
|------|-------------|
| `--profile`, `-p <profile>` | **Required.** Profile name(s) to diff. Accepts multiple profile names or comma-separated values (can be provided multiple times, e.g. `-p base -p work` or `-p base,work`) |
| `--identity`, `-i <path>` | Age identity file to diff encrypted secrets |
| `--unified`, `-u` | Display standard unified diff format instead of side-by-side |
| `--manifest`, `-m <path>` | Path to a custom manifest file (e.g. `manifest-build.yaml`) |

**Examples:**

```bash
# Side-by-side Rich diff (default)
rv diff -p base

# Standard unified diff
rv diff -p base --unified

# Include secrets in diff
rv diff -p base --identity ~/.config/rv/identity.txt --unified
```

---

### `rv doctor`

Run diagnostics on repository health, permission safety, and system tool capabilities.

```bash
rv doctor [options]
```

| Flag | Description |
|------|-------------|
| `--profile`, `-p <profile>` | Optionally scope checks to specific profile(s). Accepts multiple profile names or comma-separated values (can be provided multiple times, e.g. `-p base -p work` or `-p base,work`) |
| `--json` | Output the diagnostic report in structured JSON |
| `--manifest`, `-m <path>` | Path to a custom manifest file (e.g. `manifest-build.yaml`) |

Checks include: manifest validity, tool availability (brew, apt, flatpak, snap, docker, age, nvm/fnm), permission safety, and asset source file existence.

**Examples:**

```bash
rv doctor
rv doctor --profile base
rv doctor --json | jq '.issues'
```

Exit code is `0` if healthy, `1` if any issues are found.

---

### `rv watch`

Watch the repository directory for file changes and automatically trigger a restore.

```bash
rv watch --profile <profile> [options]
```

| Flag | Description |
|------|-------------|
| `--profile`, `-p <profile>` | **Required.** Profile(s) to monitor and auto-apply. Accepts multiple profile names or comma-separated values (can be provided multiple times, e.g. `-p base -p work` or `-p base,work`) |
| `--identity`, `-i <path>` | Age identity file for decrypting secrets |
| `--debounce`, `-d <seconds>` | Debounce delay before triggering restore (default: `5.0`) |
| `--manifest`, `-m <path>` | Path to a custom manifest file (e.g. `manifest-build.yaml`) |

Changes to `.git/` are automatically ignored. Restores are debounced to avoid rapid re-triggering during batch saves.

**Example:**

```bash
rv watch -p base --identity ~/.config/rv/identity.txt --debounce 3.0
```

Press `Ctrl+C` to stop the daemon.

---

### `rv backup`

Synchronize live system state back into the repository (`system → repo`). Use this to capture dotfile edits made directly on the system before committing.

```bash
rv backup <profile> [<profile2> ...] [options]
```

| Argument/Flag | Description |
|---------------|-------------|
| `<profile>` | **Required.** Name(s) of the profile(s) to back up. Multiple profiles and comma-separated values accepted. |
| `--identity`, `-i <path>` | Path to age identity file for re-encrypting secrets back into the repo. Defaults to `~/.config/rv/identity.txt`. |
| `--dry-run` | Preview what would be copied/encrypted without writing to the repository. |
| `--manifest`, `-m <path>` | Path to a custom manifest file (e.g. `manifest-build.yaml`) |

**Behavior by asset type:**

| Asset Type | Behavior |
|------------|----------|
| `copy` | Copies the live system file to the repo source path. |
| `symlink` | Follows the symlink to copy the actual file content to the repo. Skipped if the symlink already points to the repo source (already in sync). |
| `template` | **Skipped** — rendered outputs cannot be reversed to source templates. |
| `secret` | Derives the public key from the identity file and re-encrypts the live system file to the repo `.age` path. |

**Examples:**

```bash
# Preview what would be captured
rv backup base --dry-run

# Capture live dotfiles and secrets into the repo
rv backup base --identity ~/.config/rv/identity.txt

# Capture multiple profiles at once
rv backup base,work --identity ~/.config/rv/identity.txt
```

> [!NOTE]
> After running `rv backup`, commit and push the repository changes to propagate them to other machines.

---

### `rv recover`

Scan transaction journals and interactively rollback or discard incomplete transactions.

```bash
rv recover [options]
```

| Flag | Description |
|------|-------------|
| `--auto` | Non-interactive: auto-rollback the latest incomplete journal and exit |

**Interactive mode actions:**

| Key | Action |
|-----|--------|
| `r` | Rollback the transaction (restores pre-existing state) |
| `d` | Discard the journal entry without rollback |
| `s` | Skip (leave the incomplete transaction as-is) |

**Examples:**

```bash
# Interactive recovery
rv recover

# CI/headless auto-recovery
rv recover --auto
```

---

### `rv prune`

Prune old transaction backups under `~/.config/rv/backups/` manually or based on manifest retention settings.

```bash
rv prune [options]
```

| Flag | Description |
|------|-------------|
| `--dry-run` | Preview deleted backup folders without removing them |
| `--confirm` | Skip interactive confirmation prompts |

**Examples:**

```bash
# Interactively prune old backups
rv prune

# Dry-run preview of what would be pruned
rv prune --dry-run
```

---

### `rv gui`

Launch the interactive Revive Web GUI dashboard locally.

```bash
rv gui [options]
```

| Flag | Description |
|------|-------------|
| `--port`, `-p <int>` | Port to run the server on (default: `8080`) |
| `--host`, `-h <addr>` | Host address to bind to (default: `127.0.0.1`) |
| `--no-browser` | Do not auto-open the browser |
| `--auth-token <string>` | Set or override the API access authentication token (defaults to an auto-generated secure 32-character random hex token) |
| `--manifest`, `-m <path>` | Path to a custom manifest file (e.g. `manifest-build.yaml`) |

**Example:**

```bash
rv gui
rv gui --port 9000 --no-browser
```

The GUI provides a cosmic-dark dashboard that supports:
- **Workspace Management**: Easily register and switch between multiple Revive repositories.
- **Visual Profile Analysis**: Inspect profile inheritance maps, resolve configuration states, and preview assets.
- **Cryptographic Key Management**: Generate new Age private/public keypairs natively via a clean web interface.
- **Active Transaction Recovery**: Interactively view incomplete transaction journals and trigger transactional rollbacks or discard states directly from the web dashboard.

---

### `rv secret` Subcommands

Cryptographic secret management using [age](https://github.com/FiloSottile/age) encryption.

#### `rv secret keygen`

Generate a new age keypair.

```bash
rv secret keygen [--output <path>]
```

| Flag | Description |
|------|-------------|
| `--output`, `-o <path>` | Save the private key to a file (with `0600` permissions). Prints both keys if omitted. |

```bash
# Save private key to file
rv secret keygen --output ~/.config/rv/identity.txt

# Print to stdout
rv secret keygen
```

The generated file has the format:
```
# public key: age1...
AGE-SECRET-KEY-1...
```

#### `rv secret encrypt`

Encrypt a plaintext file using one or more age public keys.

```bash
rv secret encrypt <file> --output <path> --recipient <pubkey> [--recipient <pubkey2>]
```

| Argument/Flag | Description |
|---------------|-------------|
| `<file>` | **Required.** Path to the plaintext source file |
| `--output`, `-o <path>` | **Required.** Destination path for the `.age` encrypted file |
| `--recipient`, `-r <key>` | **Required.** Age public key (repeat for multiple recipients) |

```bash
# Encrypt AWS credentials for a single recipient
rv secret encrypt ~/.aws/credentials \
  --output secrets/aws_creds.age \
  --recipient age1ql3z7hjy...

# Encrypt for multiple recipients (team use case)
rv secret encrypt ~/.ssh/id_rsa \
  --output secrets/ssh_key.age \
  --recipient age1ql3z7hjy... \
  --recipient age1lggyhqr...
```

#### `rv secret decrypt`

Decrypt an age-encrypted file using an identity private key.

```bash
rv secret decrypt <file> --output <path> --identity <path>
```

| Argument/Flag | Description |
|---------------|-------------|
| `<file>` | **Required.** Path to the `.age` encrypted file |
| `--output`, `-o <path>` | **Required.** Destination path for the decrypted file |
| `--identity`, `-i <path>` | **Required.** Path to the age private key file |

```bash
rv secret decrypt secrets/aws_creds.age \
  --output ~/.aws/credentials \
  --identity ~/.config/rv/identity.txt
```

#### `rv secret rotate`

Re-encrypt an existing secret with a new set of recipients (key rotation).

```bash
rv secret rotate <file> --identity <path> --new-recipient <key> [--new-recipient <key2>]
```

| Argument/Flag | Description |
|---------------|-------------|
| `<file>` | **Required.** Path to the encrypted `.age` file to rotate |
| `--identity`, `-i <path>` | Current age identity to decrypt with (optional if using `--from-plaintext`) |
| `--new-recipient`, `-nr <key>` | **Required.** New public key recipient (repeat for multiple) |
| `--from-plaintext <file>` | Rotate a secret starting directly from a plaintext source file (useful if the old private key is lost). Securely shreds/wipes the plaintext source file after successful encryption. |
| `--confirm` | Required when rotating from a plaintext file to confirm secure shredding |

```bash
rv secret rotate secrets/aws_creds.age \
  --identity ~/.config/rv/identity.txt \
  --new-recipient age1newkey...
```

The rotation decrypts to a secure temp file, re-encrypts with the new key(s), and wipes the temp file. The original `.age` file is overwritten in place.

---

### `rv workspace` Subcommands

Manage the global registry of revive repositories (`~/.config/rv/workspaces.yaml`).

#### `rv workspace list`

List all registered workspaces.

```bash
rv workspace list
```

#### `rv workspace add`

Register an existing directory as a revive workspace.

```bash
rv workspace add <path> [--name <name>]
```

| Argument/Flag | Description |
|---------------|-------------|
| `<path>` | **Required.** Absolute or relative path to the revive repository |
| `--name`, `-n <name>` | Friendly display name for the workspace |

```bash
rv workspace add ~/dotfiles --name personal-dotfiles
rv workspace add ~/work/configs --name work-configs
```

#### `rv workspace remove`

Unregister a workspace by name.

```bash
rv workspace remove <name>
```

```bash
rv workspace remove personal-dotfiles
```

#### `rv workspace sync`

Pull and synchronize all registered workspaces sequentially. Exits with code 1 if any workspace fails.

```bash
rv workspace sync [options]
```

| Flag | Description |
|------|-------------|
| `--dry-run` | Preview pull and restore changes without making modifications |
| `--profile <profile>` | Override the default profile to restore for all workspaces |
| `--manifest`, `-m <path>` | Path to a custom manifest file (e.g. `manifest-build.yaml`) |

```bash
# Sync all registered workspaces
rv workspace sync

# Dry-run sync across all workspaces
rv workspace sync --dry-run
```

---

### `rv self-install`

Install the `rv` wrapper globally to `~/.local/bin/rv`.

```bash
rv self-install [--force]
```

| Flag | Description |
|------|-------------|
| `--force`, `-f` | Overwrite an existing wrapper |

Useful when managing Revive in a virtual environment and wanting global `rv` access.

---

### `rv self-uninstall`

Remove the Revive CLI installation.

```bash
rv self-uninstall [--force] [--purge-config]
```

| Flag | Description |
|------|-------------|
| `--force`, `-f` | Force removal even if the wrapper doesn't look autogenerated |
| `--purge-config` | Also remove `~/.config/rv` (workspaces, journals, backups) |

```bash
# Standard uninstall
rv self-uninstall

# Full purge including config data
rv self-uninstall --purge-config
```

---

## 🔌 Plugin System

Plugins extend Revive by running sandboxed Python scripts on lifecycle hooks.

### Built-in Plugins

Revive ships three first-party plugins:

| Plugin | Hook | Description |
|--------|------|-------------|
| `mcp-config` | `post-restore` | Sync MCP server configuration to Claude Desktop app paths |
| `claude-prompts` | `post-restore` | Sync Claude AI prompt templates |
| `python-skills` | `post-restore` | Sync AI agent skill files |

### Writing a Custom Plugin

Create a subdirectory under `plugins/` in your revive repository with two files:

**`plugins/my-plugin/plugin.yaml`:**

```yaml
name: "my-plugin"
version: "1.0.0"
entrypoint: "run.py"
permissions:
  network: false     # Allow outbound network connections
  shell: false       # Allow subprocess execution
  allowed_paths:     # Additional filesystem paths allowed
    - "~/.config/myapp"
hooks:
  - pre-restore      # Runs before filesystem mutations
  - post-restore     # Runs after successful restore
timeout: 30          # Execution timeout in seconds (max 300)
```

**`plugins/my-plugin/run.py`:**

```python
import json
import os
import sys


def main() -> None:
    # 1. Receive context from the REVIVE_CONTEXT environment variable
    context_raw = os.environ.get("REVIVE_CONTEXT")
    if not context_raw:
        print(json.dumps({"status": "error", "message": "Missing context"}), file=sys.stderr)
        sys.exit(1)

    context = json.loads(context_raw)
    profile_name = context.get("profile_name")
    repo_dir = context.get("repo_dir")
    dry_run = context.get("dry_run", False)
    hook_type = context.get("hook_type")
    targets = context.get("targets", [])

    # 2. Perform your logic here
    if dry_run:
        print(json.dumps({"status": "success", "message": "Dry run — skipping actions"}))
        sys.exit(0)

    # 3. Output a success or error JSON block to stdout
    print(json.dumps({
        "status": "success",
        "message": f"Plugin ran for profile '{profile_name}' on hook '{hook_type}'"
    }))
    sys.exit(0)


if __name__ == "__main__":
    main()
```

**`ReviveContext` fields available to plugins:**

| Field | Type | Description |
|-------|------|-------------|
| `repo_dir` | `str` | Absolute path to the repository |
| `profile_name` | `str` | Active deployment profile name |
| `dry_run` | `bool` | Whether this is a dry-run |
| `targets` | `list[str]` | Filesystem paths that will be (pre-restore) or were (post-restore) mutated by this transaction. Empty list for `pre-restore` hooks if no assets have been processed yet. |
| `hook_type` | `str` | The hook name (`pre-restore` or `post-restore`) |

### Plugin Discovery Order

Plugins are discovered and deduplicated by name in this priority order (first wins):

1. `<repo_dir>/plugins/` — workspace-local plugins
2. `~/.config/rv/plugins/` — user-global plugins
3. `<rv_package>/plugins/builtin/` — built-in first-party plugins

### Plugin Security Sandbox

All plugins run in an isolated Python subprocess via:
```
python -m rv.plugins.sandbox_wrapper <entrypoint> <perms_b64> <context_b64> <hook_type>
```

The sandbox enforces:

| Restriction | Detail |
|-------------|--------|
| **Filesystem** | `builtins.open`, `os.remove`, `os.mkdir`, etc. are patched. Access is gated to: plugin source folder, repo root, system temp, and transaction targets. Paths in `allowed_paths` are additionally permitted. |
| **Network** | If `permissions.network: false`, `socket.socket` raises `PermissionError`. |
| **Shell** | If `permissions.shell: false`, `subprocess.Popen`, `subprocess.run`, `os.system`, `os.popen`, and `os.spawn*` raise `PermissionError`. |
| **Timeout** | Default 30s, max 300s. Subprocess is forcibly terminated on expiry. |

> [!CAUTION]
> The sandbox is an in-process patch, not a container or OS-level jail. Treat it as defense-in-depth, not an absolute security boundary. Avoid running untrusted third-party plugins.

---

## 🔒 Security Model

Revive is built with defense-in-depth from the ground up:

| Component | Security Property |
|-----------|------------------|
| **Age Encryption** | Secrets use [age](https://github.com/FiloSottile/age) (`pyrage` library with CLI fallback). Only the identity holder can decrypt. |
| **In-Memory Decryption** | Decrypted secret bytes are held in `ZeroBuffer` — an in-memory buffer that is explicitly zeroed after use. Plaintext is never written to disk or temp files during restore. |
| **Secret Temp Files** | When temp files are required (e.g. key rotation), they are created with `0600` permissions via `SecureTempFile`. |
| **Log Scrubbing** | `SecretScrubber` applies regex patterns to all log output and audit entries to strip credentials, tokens, and key material. |
| **POSIX Permission Enforcement** | `PermissionValidator` validates and applies `chmod` at both write-time and verification-time. |
| **Path Traversal Prevention** | All `source` paths in manifest assets are validated to be relative and free of `..` components. |
| **Process Lock** | `ProcessLock` uses `flock` on `~/.config/rv/rv.lock` to prevent concurrent restore operations. |
| **No `shell=True`** | All subprocess invocations pass argument lists — never shell strings. |

> [!TIP]
> Store your age identity key at `~/.config/rv/identity.txt` (created via `rv secret keygen --output ~/.config/rv/identity.txt`). This is the default path used by both `rv restore` and `rv backup` when `--identity` is omitted.

---

## ⚙️ Transaction & Rollback Engine

Every `rv restore` operation runs inside a 7-step `TransactionContext`:

| Step | Description |
|------|-------------|
| **1. Plan** | Compute all source→target mutations |
| **2. Validate** | Pre-flight: permissions, storage space, parent directory availability |
| **3. Snapshot** | Back up existing files/symlinks/directories to `~/.config/rv/backups/<tx_id>/` and write the transaction journal |
| **4. Execute** | Atomically mutate the filesystem (write to temp → `chmod` → atomic rename) |
| **5. Verify** | Checksum and POSIX permission comparison to confirm success |
| **6. Commit** | Mark the journal as `committed` and update `manifest.lock` |
| **7. Cleanup** | Wipe backup snapshots and journals |

If any step fails, the journal is used to replay rollback operations, restoring the exact pre-existing state. `rv recover` can replay journals from interrupted or crashed restores.

---

## 🛠️ Development Guide

### Setup

```bash
git clone https://github.com/0xkhdr/revive.git && cd revive
python3 -m venv .venv && source .venv/bin/activate
pip install -e ".[dev]"
```

### Running Tests

```bash
# Run full test suite with coverage
.venv/bin/pytest --cov=src/rv

# Run a specific test file
.venv/bin/pytest tests/test_services.py -v

# Run with verbose output
.venv/bin/pytest --cov=src/rv --cov-report=term-missing -v
```

> [!IMPORTANT]
> Maintain **>90% test coverage** for `core/`, `security/`, `services/`, and `transactions/` before committing.

### Code Quality

```bash
# Format code (line length: 120)
.venv/bin/ruff format src/rv tests

# Lint check
.venv/bin/ruff check src/rv

# Static type checking (strict mode)
.venv/bin/mypy src/rv

# Security vulnerability scan
.venv/bin/bandit -r src/rv
```

**Non-negotiable code standards:**

- All source code in `src/rv/` must be **strictly type-annotated** (`mypy --strict` passes clean)
- **No `shell=True`** in any subprocess call — always use argument lists
- **No placeholder/stub logic** in transaction or recovery engines
- **All secrets registered** with `SecretScrubber` before any logging
- **Pydantic strict mode** — never suppress validation errors or bypass with raw dicts

### Extending Revive

#### Adding a New Package Provider

1. Create `src/rv/providers/myprovider.py` extending `BaseProvider`:

```python
from rv.providers.base import BaseProvider, ProviderError

class MyProvider(BaseProvider):
    def __init__(self) -> None:
        super().__init__("myprovider")

    def install(self, packages: list[str], dry_run: bool = False) -> None:
        if not packages:
            return
        if not self.is_available():
            raise ProviderError("myprovider is not installed on this system.")
        if dry_run:
            return
        self.execute_with_retry(["myprovider", "install", "-y"] + packages)
```

2. Register in `RestoreService.restore` (`src/rv/services/restore.py`) and `DoctorService` (`src/rv/services/doctor.py`).

#### Adding a New Asset Handler

1. Add the enum value to `AssetType` in `src/rv/models/manifest.py`
2. Add the `_handle_<type>` classmethod to `AssetHandler` in `src/rv/services/handlers.py`

#### Project Layout

```text
src/rv/
├── cli/main.py          # Typer CLI commands
├── models/
│   ├── manifest.py      # Manifest, Asset, Secret, Profile Pydantic models
│   ├── transaction.py   # Transaction journal & manifest.lock models
│   └── workspace.py     # Workspace registry models
├── services/
│   ├── restore.py       # 14-step unidirectional restore coordinator
│   ├── status.py        # Drift detection & diff generation
│   ├── doctor.py        # System health diagnostics
│   ├── handlers.py      # Asset type executors (copy, symlink, template, secret)
│   ├── recovery.py      # Journal replay & rollback engine
│   └── workspace.py     # Workspace registration service
├── transactions/
│   ├── context.py       # 7-step TransactionContext with rollback
│   ├── atomic.py        # Atomic temp-write + rename
│   └── lock.py          # flock-based process serialization
├── security/
│   ├── encryptor.py     # age encryption/decryption engine
│   ├── scrubber.py      # Credential log scrubber
│   ├── permissions.py   # POSIX chmod validator/enforcer
│   ├── tempfile.py      # Secure 0600 temp file creator
│   └── zerobuffer.py    # In-memory secret buffer with explicit zeroing
├── plugins/
│   ├── loader.py        # Plugin discovery & manifest parsing
│   ├── sandbox.py       # Subprocess coordinator & timeout enforcer
│   ├── sandbox_wrapper.py # In-process builtins/socket/subprocess patcher
│   └── builtin/         # First-party plugins (mcp-config, claude-prompts, python-skills)
├── providers/           # Package manager orchestration (apt, brew, docker, flatpak, snap, node)
├── watchers/daemon.py   # Watchdog daemon for auto-restore on file changes
├── gui/server.py        # Web GUI HTTP server
├── logging/audit.py     # Dual-output: structured JSON audit log + Rich console
└── utils/
    ├── interpolate.py   # ${VAR:-default} env variable interpolation
    ├── path.py          # Path canonicalization & traversal checks
    └── platform.py      # OS/distro detection
```

---

## Requirements

- **Python 3.11+**
- **age** encryption tool (for secret operations — `pyrage` library handles it in-process, `age` CLI as fallback)
- Package manager tools as needed: `brew`, `apt`, `flatpak`, `snap`, `docker`, `nvm`/`fnm`

---

## License

MIT — see [LICENSE](LICENSE) for details.
