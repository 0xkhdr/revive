# Revive (`rv`) — Developer Environment Lifecycle Manager

Revive (`rv`) is a minimal, developer-friendly environment lifecycle manager. It enforces a **unidirectional state engine** (`repository → system`) to synchronize developer workspaces, application configurations, secrets, and package managers atomically. If any part of the synchronization fails, Revive automatically rolls back all mutations safely.

---

## 🚀 Fastest Installation & Uninstallation

### 1-Second Install
Install `rv` globally to `~/.local/bin/rv` and download its isolated virtual environment to `~/.local/share/rv`:

```bash
curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/scripts/install.sh | sh
```

> [!NOTE]
> Make sure `~/.local/bin` is in your shell `PATH`. If not, add this to your shell profile (e.g., `~/.zshrc` or `~/.bashrc`):
> `export PATH="$HOME/.local/bin:$PATH"`

### 1-Second Uninstall
Remove the isolated virtual environment and global wrapper:

```bash
rv self-uninstall --purge-config
```
*(Or remote uninstall: `curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/scripts/uninstall.sh | sh`)*

---

## 🛠️ Getting Started: Init & Backing Up

### 1. Initialize Your Repository
Create a new dotfiles/configuration repository directory, navigate into it, and scaffold a Revive configuration:

```bash
mkdir -p ~/dotfiles && cd ~/dotfiles
rv init
```

This registers the workspace and scaffolds the following structure:
*   `manifest.yaml`: The primary global configuration manifest.
*   `assets/`: Folder to place managed files, configurations, and templates.
*   `secrets/`: Folder for age-encrypted `.age` files.
*   `machine/`: Workspace to hold machine/host-specific overrides.

### 2. Back Up Your Configuration

#### Files & Symlinks
Move your existing local configurations (e.g., `.zshrc`) into `assets/` and declare them in your `manifest.yaml`:

```yaml
# ~/dotfiles/manifest.yaml
version: 2

assets:
  - id: my_zshrc
    type: symlink        # Or 'copy'
    source: assets/zshrc # Relative to repository root
    target: ~/.zshrc     # System target destination
    permissions: "0644"
    conflict_strategy: prompt

profiles:
  base:
    assets:
      - my_zshrc
```

#### Secrets (Cryptographic Age Encryption)
To safely back up sensitive files (e.g. AWS credentials, SSH keys), first generate a secure age keypair:

```bash
rv secret keygen --output ~/.config/rv/identity.txt
```

Encrypt your sensitive file to the `secrets/` directory:

```bash
# Encrypts the plaintext credentials file using your public key
rv secret encrypt ~/.aws/credentials \
  --output secrets/aws_creds.age \
  --recipient $(cat ~/.config/rv/identity.txt | grep "public key" | awk '{print $4}')
```

Then register it under `secrets` in your `manifest.yaml`:

```yaml
# ~/dotfiles/manifest.yaml
secrets:
  - id: aws_credentials
    source: secrets/aws_creds.age
    target: ~/.aws/credentials
    permissions: "0600"

profiles:
  base:
    secrets:
      - aws_credentials
```

#### Packages
Declare native system packages you want to keep installed under the `packages` key:

```yaml
# ~/dotfiles/manifest.yaml
packages:
  brew:
    - ripgrep
    - fzf
  apt:
    - curl
    - git

profiles:
  base:
    packages:
      - brew
      - apt
```

---

## 🔄 Restoring Your Backup Repository

When migrating to a new machine or applying changes to your current system, run the `restore` command.

### Dry-Run (Preview Changes First)
Always preview what changes will be applied before making system mutations:

```bash
rv restore base --dry-run
```

### Full Restore
Synchronize your system state to perfectly match your repository profile (including packages, files, and secrets):

```bash
rv restore base --identity ~/.config/rv/identity.txt
```

If any mutation fails during the sync, a transactional rollback is automatically executed to restore your previous environment state safely.

---

## 💻 Essential Developer Commands

*   `rv status -p base` — Evaluate system drift compared to the repository.
*   `rv diff -p base` — Output a beautiful, syntax-highlighted side-by-side file diff.
*   `rv doctor` — Run diagnostics on permission safety and system capabilities.
*   `rv gui` — Open a stunning cosmic-dark Web GUI dashboard locally to manage workspaces, track inheritance profiles, and manage assets.
