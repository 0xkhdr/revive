# New Machine Setup with Revive

This guide walks you through bootstrapping a fresh machine from your dotfiles repository. This is the golden path for setting up development environments quickly and consistently.

---

## Prerequisites

Before starting, ensure:
- **Git** is installed (required for cloning)
- **Python 3.11+** is available
- Your dotfiles are in a **GitHub/GitLab/Gitea repository** (any Git host works)
- You have **Revive installed** (see [main README](../README.md#-installation))

If you don't have Revive installed yet:

```bash
curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/scripts/install.sh | sh
```

---

## Step 1: Generate Your Age Identity Key (if using secrets)

If your dotfiles include encrypted secrets (AWS credentials, SSH keys, etc.), you need an **Age identity keypair**:

```bash
rv secret keygen --output ~/.config/rv/identity.txt
```

This creates a private key at `~/.config/rv/identity.txt`. Keep it safe — you'll need it whenever restoring secrets.

> **Tip**: You can generate the keypair on an existing machine and store the identity file safely (e.g., in a password manager, or synced via a separate secure method) so you can copy it to new machines.

---

## Step 2: Clone & Auto-Restore in One Step

The `rv clone` command combines three operations:
1. Clone your dotfiles repository
2. Register it as a Revive workspace
3. Optionally auto-restore a profile

### With Secrets

```bash
rv clone https://github.com/user/dotfiles ~/dotfiles \
  --restore base \
  --identity ~/.config/rv/identity.txt
```

### Without Secrets

```bash
rv clone https://github.com/user/dotfiles ~/dotfiles \
  --restore base
```

### What Happens During Clone+Restore

```
✅ Clones git repository
✅ Registers workspace in ~/.config/rv/workspaces.yaml
✅ Decrypts secrets to memory (if present)
✅ Creates symlinks/copies files to their targets (~/.zshrc, ~/.gitconfig, etc.)
✅ Installs packages (apt, brew, etc.)
✅ Runs post-restore plugins
✅ Saves audit log to ~/.config/rv/audit.log
```

If any step fails, the entire restore is rolled back — nothing is left in a broken half-configured state.

---

## Step 3: Verify the Restore

Check that everything synced correctly:

```bash
rv status -p base
```

You should see output like:

```
Profile: base

 ✅ In Sync     ~/.zshrc
 ✅ In Sync     ~/.gitconfig
 ✅ In Sync     ~/.ssh/config
```

If there are any `Modified` or `Missing` files, investigate:

```bash
rv diff -p base   # See what changed
```

---

## Step 4 (Optional): Set Up Auto-Sync

Watch your dotfiles repository and automatically re-apply changes when you pull updates:

```bash
cd ~/dotfiles
rv watch -p base --identity ~/.config/rv/identity.txt
```

This daemon watches the repository and runs `rv restore` whenever files change. Press `Ctrl+C` to stop.

> **Tip**: For CI/non-interactive environments, use `rv restore base --non-interactive` instead.

---

## Step 5 (Optional): Edit Your Dotfiles

If you modify files directly on your machine (e.g., edit `~/.zshrc`), capture those changes back into your repository:

```bash
rv backup base --identity ~/.config/rv/identity.txt
git -C ~/dotfiles add -A
git -C ~/dotfiles commit -m "chore: update dotfiles"
git -C ~/dotfiles push
```

Now the changes are saved in your repo and can be applied to other machines.

---

## Troubleshooting

### Clone fails with "git not found"

Install Git first:

```bash
# Ubuntu/Debian
sudo apt-get install git

# macOS
brew install git

# Arch
sudo pacman -S git
```

### Auto-restore fails with "permission denied"

Check file ownership and permissions:

```bash
rv doctor --manifest ~/dotfiles/manifest.yaml
```

If a file already exists at the target path with different permissions, `rv restore` will prompt you for a conflict strategy (`prompt`, `skip`, `overwrite`, `abort`).

### Secrets decrypt fails

Ensure the identity file path is correct:

```bash
ls -la ~/.config/rv/identity.txt
```

If missing, regenerate it (you'll need the corresponding private key to decrypt existing secrets) or ask the repository owner for a new keypair.

### Specific files don't exist

Check the manifest to ensure the `source:` paths are correct:

```bash
cd ~/dotfiles
rv doctor
```

This runs a full diagnostic and reports missing or inaccessible source files.

---

## Next Steps

- **Sync changes back**: [Backup guide](../README.md#-backup)
- **Add custom packages**: Edit `manifest.yaml` and run `rv restore base --force-packages`
- **Machine-specific config**: Create `machine/<hostname>.yaml` for host-specific overrides
- **Multiple profiles**: Define multiple profiles in `manifest.yaml` for different use cases (e.g. `base`, `work`, `media`)
- **Write plugins**: Automate post-restore setup with custom plugins

See the [main README](../README.md) for comprehensive command reference.
