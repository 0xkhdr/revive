# Revive (`rv`) — Developer Environment Lifecycle Manager

Revive (`rv`) is a production-grade, highly secure environment lifecycle management tool. It enforces a **unidirectional state engine** (`repository → system`) to synchronize developer workspaces, application configurations, secrets, and package managers atomically. 

Revive features a rigorous transactional execution lifecycle (Plan, Validate, Snapshot, Execute, Verify, Commit, and Cleanup) alongside process-locking guarantees, sandboxed hook execution for extensibility, and cryptographic security using Age encryption.

---

## Key Capabilities

*   **Transactional File Synchronizations**: Assets are managed as atomic transactions. Any failure during execution triggers a safe rollback of mutated files from snapshots.
*   **Cryptographic Secret Management**: Seamless encryption and decryption of secrets using age keys (`pyrage` first, falling back to age CLI), with log scrubbing and memory zero-buffers.
*   **Multi-Provider Packages**: Secure native package installations (`brew`, `apt`, `flatpak`, `snap`, `docker`, `node`) with strict error recovery.
*   **Interactive Web GUI Dashboard**: A stunning, premium cosmic-dark themed dashboard for managing multiple Revive workspaces, importing assets, visual inheritance maps, and guided restorations.
*   **Isolated Sandboxed Plugins**: Extensible hook loader running custom Python scripts in sandboxed environments with resource limits and path restrictions.
*   **Real-time Watchdog Daemon**: Keeps directories in sync automatically using debounced file event observers.
*   **Disaster Recovery Engine**: Scans, lists, and recovers interrupted transactions using interactive or headless automated rollbacks.

---

## 1. Getting Started

### Installation

Install `rv` on any Linux machine with one command:

```bash
curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/scripts/install.sh | sh
```

This creates an isolated user install at `~/.local/share/rv`, installs the package into its own virtual environment, and writes the global wrapper to `~/.local/bin/rv`.

If the machine is missing common prerequisites, run the same installer with best-effort system dependency bootstrapping:

```bash
curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/scripts/install.sh | sh -s -- --system-deps
```

To reinstall over an existing local wrapper:

```bash
curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/scripts/install.sh | sh -s -- --force
```

When working from a checked-out repository, run:

```bash
./scripts/install.sh
```

For local prerequisite bootstrapping:

```bash
./scripts/install.sh --system-deps
```

For local reinstall:

```bash
./scripts/install.sh --force
```

Uninstall `rv` with one command:

```bash
./scripts/uninstall.sh
```

If `~/.local/bin` is not on your shell path, add it to your shell profile:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

For local development, install the package in editable mode from the repository root:

```bash
python3 -m venv .venv
.venv/bin/pip install -e .
```

Or build and run the single-binary standalone executable using PyInstaller:

```bash
pyinstaller --onefile --name rv src/rv/__main__.py
./dist/rv --help
```

### Initializing a Repository

Create a fresh revive configuration directory under your current path:

```bash
rv init
```

This scaffolds the following repository directory structure:
*   `manifest.yaml`: The primary global configuration manifest.
*   `assets/`: Folder to place managed files, configurations, and templates.
*   `secrets/`: Folder for age-encrypted `.age` files.
*   `machine/`: Workspace to hold machine/host-specific overrides.

### Cryptographic Identity

Before managing secrets, generate an age keypair:

```bash
rv secret keygen --output ~/.config/rv/identity.txt
```

Keep your private key secure. You will use this path with the `--identity` or `-i` flag during restoration.

---

## 2. Configuration (`manifest.yaml`)

Revive uses a clean declarative manifest. The following is an example configuration managing file copies, symlinks, templates, and packages:

```yaml
version: 2

# Global configuration for host-specific overrides
machine_overrides:
  enabled: true
  path: "machine/{hostname}.yaml"

assets:
  - id: dot_zshrc
    type: symlink
    source: assets/zshrc
    target: ~/.zshrc
    permissions: "0644"
    conflict_strategy: prompt

  - id: gitconfig_template
    type: template
    source: assets/gitconfig.j2
    target: ${HOME}/.gitconfig
    permissions: "0600"
    conflict_strategy: overwrite

secrets:
  - id: aws_creds
    source: secrets/aws.age
    target: ${AWS_CONFIG_HOME:-~/.aws}/credentials
    permissions: "0600"

packages:
  brew:
    - ripgrep
    - fzf
  apt:
    - curl
    - git
  node:
    version_file: .nvmrc

profiles:
  base:
    assets:
      - dot_zshrc
    packages:
      - brew
      - apt
  
  # Profile inheritance via 'extends'
  work:
    extends: [base]
    assets:
      - gitconfig_template
    secrets:
      - aws_creds
    packages:
      - node
```

### Advanced Manifest Features

*   **Variable Interpolation**: All `target` paths support environment variable interpolation using `${VAR}` or `${VAR:-default}` syntax.
*   **Profile Inheritance**: Profiles can use the `extends` keyword to inherit assets, secrets, and packages from one or more base profiles.
*   **Machine Overrides**: Enable `machine_overrides` to automatically merge host-specific manifests from the `machine/` directory based on the system hostname.
*   **Conflict Strategies**: Supported strategies include `prompt` (interactive), `overwrite` (force), `skip` (do nothing), and `abort` (stop execution).

---

## 3. CLI Command Reference

### Global Options

The following flags are available on all commands:
*   `--verbose`, `-v`: Enable detailed debug logging.
*   `--headless`: raw stream logs without Rich terminal formatting (ideal for CI/CD).

### Synchronizing State

#### `rv restore`
Synchronize the local environment state to match a repository profile.
```bash
rv restore <profile> [OPTIONS]
```
*   `--identity`, `-i`: Path to the age private key file (required if decrypting secrets).
*   `--dry-run`: Plan and validate operations without mutating the filesystem.
*   `--non-interactive`: Turn off interactive prompting for asset conflicts.
*   `--no-plugins`: Skip executing plugin hooks during restore.

---

### Auditing & State Verification

#### `rv status`
Evaluate environment drift by comparing current system files against the repository manifest.
```bash
rv status --profile <profile> [OPTIONS]
```
*   `--identity`, `-i`: Age identity file to check encrypted secret drift.

#### `rv diff`
Output detailed syntax-highlighted diffs of modified file assets.
```bash
rv diff --profile <profile> [OPTIONS]
```
*   `--unified`, `-u`: Output in standard unified diff format.

#### `rv doctor`
Evaluate repository sanity, permission safety, and native system tool capabilities.
```bash
rv doctor [OPTIONS]
```
*   `--profile`, `-p`: Run diagnostic checks specific to a profile.
*   `--json`: Output results in structured JSON for programmatic pipelines.

---

### Watcher Daemon

#### `rv watch`
Continuously watch the repository for updates and automatically synchronize the state.
```bash
rv watch --profile <profile> [OPTIONS]
```
*   `--identity`, `-i`: Age identity private key file for decrypting secrets.
*   `--debounce`, `-d`: Delay window in seconds to debounce multiple file modifications (default: `5.0`).

---

### Disaster Recovery

#### `rv recover`
Scan transaction logs (`~/.config/rv/journals`) to clean up or safely roll back interrupted executions.
```bash
rv recover [OPTIONS]
```
*   `--auto`: Headless recovery. Automatically rolls back the latest incomplete transaction and exits without prompting.

---

### Interactive Control Center (Web GUI)

#### `rv gui`
Launch a stunning, cosmic-dark themed Web GUI Dashboard locally to manage your Revive workspaces, import files as assets/secrets, view real-time restore logs, track visual manifest profiles/inheritance maps, and execute diffs side-by-side.
```bash
rv gui [OPTIONS]
```
*   `--port`, `-p`: Port to run the GUI server on (default: `8080`).
*   `--host`, `-h`: Host address to bind to (default: `127.0.0.1` for loopback security).
*   `--no-browser`: Start the server without automatically opening the browser.

---

### Workspace Management (`rv workspace`)

Manage registered Revive repositories globally:

#### `rv workspace list`
List all registered workspaces and their last access times.

#### `rv workspace add <path>`
Register an existing directory as a Revive workspace.
*   `--name`, `-n`: Friendly name for the workspace.

#### `rv workspace remove <name>`
Unregister a workspace by name.

---

### Secret Management (`rv secret`)

Manage encrypted variables and files securely:

#### `rv secret keygen`
Generate a new age keypair for encryption and decryption.
```bash
rv secret keygen [OPTIONS]
```
*   `--output`, `-o`: Path to write the generated private identity file.

#### `rv secret encrypt`
```bash
rv secret encrypt <plaintext_file> --output <encrypted_file> --recipient <pubkey>
```
*   Supports multiple `--recipient` options to encrypt for several keys.

#### `rv secret decrypt`
```bash
rv secret decrypt <encrypted_file> --output <plaintext_file> --identity <private_key>
```

#### `rv secret rotate`
Decrypt an existing encrypted secret and re-encrypt it for a new set of recipients:
```bash
rv secret rotate <encrypted_file> --identity <current_private_key> --new-recipient <new_pubkey>
```

---

### Installation Management

#### `rv self-install`
Install the `rv` tool wrapper globally to `~/.local/bin/rv`.
*   `--force`, `-f`: Overwrite existing wrapper.

#### `rv self-uninstall`
Remove the `rv` wrapper and isolated installation directory.
*   `--force`, `-f`: Force removal of the wrapper.
*   `--purge-config`: Also remove the `~/.config/rv` directory.

---

## 4. Extensibility & Plugins

Revive supports custom lifecycle hooks loaded dynamically from `plugins/` (repo), `~/.config/rv/plugins/`, or built-in paths. Plugins must include a `plugin.yaml` manifest.

### Plugin Manifest (`plugin.yaml`)

```yaml
name: "my-custom-plugin"
version: "1.0.0"
entrypoint: "main.py"
hooks:
  - pre-restore
  - post-restore
permissions:
  network: false
  shell: true
  allowed_paths: ["/tmp"]
timeout: 30
```

### Hook Types

*   **`pre-restore`**: Executes after profile resolution but before any filesystem mutations. Ideal for pre-flight checks.
*   **`post-restore`**: Executes after all assets and packages have been successfully applied. Ideal for restarting services or clearing caches.

Plugins execute inside a highly secure sandbox with restricted permissions:
*   Standard imports are restricted for safety.
*   Disk accesses outside the repository, workspace, and temporary directory are blocked unless explicitly allowed.
*   Execution is governed by strict timeouts to prevent terminal hangs.

---

## 5. Development & Testing

### Running the Test Suite
We enforce strict test coverage. Run the test suite:
```bash
pytest
```
Run with coverage tracking:
```bash
pytest --cov=src/rv --cov-report=term-missing tests/
```

### Static Analysis & Lints
All changes must pass strict type checking and style validation:
```bash
mypy --strict src/rv
ruff check src/rv
```
