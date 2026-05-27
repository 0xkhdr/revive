# Troubleshooting Guide — Revive (`rv`)

Common errors, debug techniques, and FAQ.

---

## Table of Contents

- [Debug Mode](#debug-mode)
- [Installation Issues](#installation-issues)
- [Restore Errors](#restore-errors)
- [Secret / Encryption Errors](#secret--encryption-errors)
- [Plugin Errors](#plugin-errors)
- [Package Manager Errors](#package-manager-errors)
- [GUI Issues](#gui-issues)
- [Transaction & Recovery](#transaction--recovery)
- [FAQ](#faq)

---

## Debug Mode

Enable verbose output on **any** command with `--verbose` or `-v`:

```bash
rv --verbose restore base
rv -v doctor
rv -v status -p base
```

In CI/headless environments (no Rich styling, raw logs):

```bash
rv --headless restore base --non-interactive
```

Audit logs are written to `~/.config/rv/audit.log` (JSON format). View with:

```bash
cat ~/.config/rv/audit.log | jq '.'
# or tail live:
tail -f ~/.config/rv/audit.log | jq '.'
```

---

## Installation Issues

### `rv: command not found`

`~/.local/bin` is not in `$PATH`. Add to your shell profile:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

### `Python 3.11+ was not found`

```bash
# Check available Python versions
python3 --version
python3.12 --version

# Install with --system-deps (uses your OS package manager)
curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/scripts/install.sh | sh -s -- --system-deps
```

### `Failed to create venv`

The `python3-venv` package may be missing on Debian/Ubuntu:

```bash
sudo apt-get install python3-venv
```

### Re-install / force overwrite

```bash
curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/scripts/install.sh | sh -s -- --force
```

---

## Restore Errors

### `ManifestValidationError: ...`

The `manifest.yaml` failed Pydantic v2 strict validation. Common causes:

| Error message | Cause | Fix |
|---------------|-------|-----|
| `permissions must be a 4-digit octal string` | `permissions: 644` instead of `permissions: "0644"` | Quote the value |
| `secret permissions allow world access` | Secret `permissions` has group/world bits set | Use `"0600"` or `"0400"` |
| `source path contains '..'` | Manifest asset `source` traverses upward | Use a path inside the repo only |
| `profile references unknown asset id` | Profile references an asset not in the global `assets:` list | Add the asset to the global pool |

Run `rv doctor` for a structured validation report:

```bash
rv doctor --json | jq '.issues'
```

### `ProfileNotFoundError: profile 'foo' not found`

The profile name doesn't exist in `manifest.yaml`. Check available profiles:

```bash
rv doctor
# or check the manifest directly
grep -A1 "^profiles:" manifest.yaml
```

### `ProcessLockError: another rv process is running`

A concurrent `rv` process holds the lock. Wait for it to finish, or if it crashed:

```bash
rm ~/.config/rv/rv.lock
```

> [!WARNING]
> Only delete `rv.lock` if you are certain no other `rv` process is running. Removing
> the lock while another process holds it will corrupt the serialization guarantee.

### `TransactionRollbackError` / Restore rolled back

The transaction failed mid-way. Revive automatically rolls back to the pre-restore state.
Check what failed:

```bash
rv --verbose restore base 2>&1 | grep -i error
# or run doctor
rv doctor -p base
```

If the machine is in an inconsistent state, run:

```bash
rv recover
```

### `FileNotFoundError: source asset not found`

The source file declared in `manifest.yaml` doesn't exist in the repository:

```bash
# Check what's declared
grep -A3 "id: my_asset" manifest.yaml

# Check if the file exists
ls assets/my_file
```

---

## Secret / Encryption Errors

### `IdentityNotFoundError: identity file not found`

```bash
# Check the default identity path
ls ~/.config/rv/identity.txt

# Generate a new identity if missing
rv secret keygen --output ~/.config/rv/identity.txt
```

### `DecryptionError: failed to decrypt`

The identity file does not match the key used to encrypt the secret. Possible causes:

1. Wrong identity file passed via `--identity`
2. Secret was encrypted for a different recipient key
3. Secret file is corrupted

To verify the recipient: the `.age` file header contains `-> age1...` lines with
recipient public keys. Compare against your identity's public key:

```bash
head -5 secrets/my_secret.age          # shows recipient public keys
grep "public key:" ~/.config/rv/identity.txt   # shows your public key
```

### `PermissionError: secret permissions too permissive`

Secrets must have restrictive permissions (`0600` or `0400`). The world/group read bits
(`0644`, `0655`) are blocked by Pydantic validation.

Fix in `manifest.yaml`:
```yaml
secrets:
  - id: my_secret
    permissions: "0600"   # was "0644"
```

---

## Plugin Errors

### `PluginTimeout: plugin 'my-plugin' exceeded 30s`

The plugin took longer than its configured timeout. Increase in `plugin.yaml`:

```yaml
timeout: 60   # up to 300 seconds max
```

### `PluginPermissionError: network access denied`

The plugin tried to make a network connection but `permissions.network` is `false`.
Either enable network access in `plugin.yaml` or remove the network call from the plugin:

```yaml
permissions:
  network: true
```

### `PluginSandboxError: import of 'ctypes' blocked`

Plugins cannot import `ctypes`, `cffi`, `gc`, or `importlib`. This is a sandbox
restriction that cannot be bypassed. If your plugin genuinely needs these, it cannot
run inside Revive's sandbox.

### Skip all plugins for debugging

```bash
rv restore base --no-plugins
```

---

## Package Manager Errors

### `ProviderError: <manager> is not installed`

The package manager declared in `manifest.yaml` is not available on the system.
Run `rv doctor` to see which tools are detected:

```bash
rv doctor
```

Install the missing tool, or remove that package manager's block from `manifest.yaml`
if it's not needed on this machine (use machine overrides instead).

### Package install fails / retries exhausted

Providers use exponential backoff retry. For transient failures (network, lock contention):

```bash
# Force a fresh install attempt, bypassing the package cache
rv restore base --force-packages
```

### Package cache stale

The package cache at `~/.config/rv/package-cache.json` has a 24h TTL. Force refresh:

```bash
rv restore base --force-packages
```

---

## GUI Issues

### `rv gui` auth token lost

The GUI auto-generates a new token on each startup. Pass a fixed token:

```bash
rv gui --auth-token my-fixed-token-here
```

### GUI accessible from other hosts (unintended)

By default, `rv gui` binds to `127.0.0.1`. If you bound to `0.0.0.0`, a warning is
printed. Change back:

```bash
rv gui --host 127.0.0.1
```

### CORS errors in browser (development)

If running a separate frontend dev server, enable wildcard CORS:

```bash
rv gui --cors-wildcard   # DEVELOPMENT ONLY — never use in production
```

---

## Transaction & Recovery

### Incomplete transaction from a crashed restore

```bash
# Interactive recovery (prompts for each incomplete journal)
rv recover

# Headless auto-rollback (CI use case)
rv recover --auto
```

### List / prune old backup snapshots

```bash
# Preview what would be pruned
rv prune --dry-run

# Prune interactively
rv prune

# Skip confirmation
rv prune --yes
```

### Where are backup snapshots stored?

```bash
ls ~/.config/rv/backups/
```

Each subdirectory is a transaction snapshot named by transaction ID (UUID).

---

## Clone Errors

### `rv clone` fails with `git not found`

Git is not installed or not in `$PATH`. Install it:

```bash
# Debian/Ubuntu
sudo apt-get install git

# macOS (Homebrew)
brew install git

# Arch Linux
sudo pacman -S git
```

### `rv clone https://... --restore base` fails during auto-restore

If the clone succeeds but the auto-restore fails:

1. **Check the repo has a valid manifest:**
   ```bash
   rv doctor --manifest path/to/cloned/repo/manifest.yaml
   ```

2. **If secrets are present, verify the identity file:**
   ```bash
   ls -la ~/.config/rv/identity.txt
   # If missing, generate one:
   rv secret keygen --output ~/.config/rv/identity.txt
   ```

3. **Run the restore manually after cloning:**
   ```bash
   cd path/to/cloned/repo
   rv restore base --identity ~/.config/rv/identity.txt
   ```

4. **Check for file conflicts** — if a dotfile already exists on the system, the restore may have been aborted. Review the pre-restore output for conflict prompts.

### `workspace already registered` after cloning

The clone succeeded and registered the workspace, but you're running `rv clone` again on the same path. This is expected behavior — the workspace is now registered and you can use `rv restore` directly:

```bash
cd my-dotfiles
rv restore base --identity ~/.config/rv/identity.txt
```

---

## FAQ

**Q: Can I use Revive without secrets?**

Yes. Secrets are optional. Omit the `secrets:` section in `manifest.yaml` and skip
the `--identity` flag.

---

**Q: Does `rv backup` overwrite my `.age` files?**

Yes — `rv backup` overwrites the repository source for each asset/secret. Run
`git diff` after `rv backup` to review what changed before committing.

---

**Q: What happens if a package is already installed?**

Providers use `filter_missing()` to check installation status before running. Packages
already present are skipped. Use `--force-packages` to reinstall regardless.

---

**Q: Can I use multiple manifests?**

Yes. Pass `-m` / `--manifest` to any command:

```bash
rv restore base -m manifest-build.yaml
rv status -p base -m manifest-build.yaml
```

The lockfile is derived from the manifest path:
`manifest-build.yaml` → `manifest-build.lock`.

---

**Q: How do I add machine-specific packages without editing the main manifest?**

Create `machine/<your-hostname>.yaml`:

```yaml
packages:
  apt:
    - libssl-dev
    - docker.io
```

Revive automatically merges this at restore time on that host only.

---

**Q: `rv watch` keeps triggering immediately — how do I slow it down?**

Increase the debounce delay:

```bash
rv watch -p base --debounce 10.0   # 10 seconds
```

---

**Q: Why are template assets skipped during `rv backup`?**

Rendered template outputs cannot be trivially reversed to the original Jinja2 source.
`rv backup` skips them by design. Edit the source template directly in the repository.

---

**Q: How do I bootstrap a new machine from my dotfiles repo?**

Use `rv clone` — it combines git clone, workspace registration, and auto-restore in one step:

```bash
# With secrets
rv clone https://github.com/user/dotfiles ~/dotfiles \
  --restore base \
  --identity ~/.config/rv/identity.txt

# Without secrets (secrets are optional)
rv clone https://github.com/user/dotfiles ~/dotfiles \
  --restore base
```

If you prefer to clone manually first, you can restore separately:

```bash
git clone https://github.com/user/dotfiles ~/dotfiles
cd ~/dotfiles
rv restore base --identity ~/.config/rv/identity.txt
```

---

**Q: How do I completely remove Revive?**

```bash
rv self-uninstall --purge-config
```

This removes:
- `~/.local/bin/rv` wrapper
- `~/.local/share/rv/` installation
- `~/.config/rv/` all config, journals, backups (with `--purge-config`)
