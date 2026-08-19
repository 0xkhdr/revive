# manifest.yaml Complete Reference

This is the comprehensive reference for `manifest.yaml` — the single source of truth for your Revive configuration.

**Quick links**: [Assets](#assets) • [Secrets](#secrets) • [Packages](#packages) • [Profiles](#profiles) • [Machine Overrides](#machine-overrides) • [Backup Retention](#backup-retention)

---

## Schema Overview

Every Revive repository has a `manifest.yaml` at its root:

```yaml
version: 2

assets: []              # Global pool of files to manage
secrets: []             # Global pool of encrypted secrets
packages: {}            # Package manager declarations
profiles: {}            # Named restore profiles
backup_retention:       # Retention policy for transaction backups
  max_count: 10
  max_age_days: 30
machine_overrides:      # Host-specific configuration overrides
  enabled: true
  path: "machine/{hostname}.yaml"
```

**Version**: Always `2` (v1 is no longer supported).

Revive validates your manifest using **Pydantic v2 strict mode** on every operation. This means:
- All field types are checked strictly
- No implicit conversions (e.g., `"0644"` string, not `644` int)
- Unknown fields cause validation errors

---

## Assets

Assets are files and directories you want to manage: symlinked, copied, or templated into your system.

### Asset Types

| Type | Behavior |
|------|----------|
| **`symlink`** | Creates a symlink at `target` pointing to the source in the repo. Useful for configs that need fast re-reading (dotfiles, vim configs). |
| **`copy`** | Atomically copies the file/directory to `target`. Useful for files that need to be writable on the system (scripts, binaries). |
| **`template`** | Renders a Jinja2 template with `template_vars` before copying. Useful for host-specific configs (e.g., `${HOSTNAME}` interpolation). |

### Asset Schema

```yaml
assets:
  - id: my_zshrc
    type: symlink                    # symlink | copy | template
    source: assets/zshrc             # Relative to repo root (no '..' allowed)
    target: ~/.zshrc                 # System destination (supports ${VAR:-default})
    permissions: "0644"              # 4-digit octal string (must be quoted)
    owner: null                       # Owner username; null = current user
    conflict_strategy: prompt         # prompt | overwrite | skip | abort
    template_vars:                    # Only for type: template
      MY_VAR: hello
      HOSTNAME: ${HOSTNAME}
    hooks:                            # Optional pre/post-restore hooks
      pre-restore:
        - /bin/bash -c "echo Preparing..."
      post-restore:
        - /bin/bash -c "echo Done!"
```

### Field Reference

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `id` | `str` | ✅ Yes | — | Unique identifier. Referenced by profiles. |
| `type` | `symlink \| copy \| template` | ✅ Yes | — | Asset type determines how `source` is applied to `target`. |
| `source` | `str` | ✅ Yes | — | Relative path to source file/directory in the repo. No `..` traversal allowed. |
| `target` | `str \| list[str]` | ✅ Yes | — | System destination path. Supports `${VAR:-default}` interpolation. Can be a list for multi-destination. |
| `permissions` | `str` | ❌ No | — | 4-digit octal string (e.g. `"0644"`, `"0755"`). Must be quoted. Applied after write. |
| `owner` | `str \| null` | ❌ No | `null` | Unix username. `null` means current user. Requires sudo on most systems. |
| `conflict_strategy` | `prompt \| overwrite \| skip \| abort` | ❌ No | `prompt` | What to do if `target` already exists. |
| `template_vars` | `dict[str, str]` | ❌ No | `{}` | Key-value pairs for Jinja2 template rendering (type: template only). |
| `hooks` | `AssetHooks` | ❌ No | `{}` | Pre-restore and post-restore shell commands. |

### Conflict Strategies

| Strategy | Behavior |
|----------|----------|
| `prompt` | Ask the user interactively (default). |
| `overwrite` | Silently replace the existing target. ⚠️ **Warning**: Data loss possible. |
| `skip` | Leave the existing target untouched. |
| `abort` | Abort the entire restore. ❌ Transaction rolls back. |

### Template Variables

For `type: template` assets, you can interpolate environment variables and custom values:

```yaml
assets:
  - id: my_config
    type: template
    source: assets/config.j2
    target: ~/.config/myapp/config.yaml
    template_vars:
      HOSTNAME: ${HOSTNAME}                # System env var
      USER: ${USER}
      CUSTOM_VALUE: production             # Custom literal
      DEFAULT_VAL: ${OPTIONAL_VAR:-none}  # Default fallback
```

Inside `assets/config.j2`:

```jinja
hostname: {{ HOSTNAME }}
user: {{ USER }}
env: {{ CUSTOM_VALUE }}
optional: {{ DEFAULT_VAL }}
```

### Multi-Destination Assets (Target Arrays)

A single asset can copy to multiple destinations:

```yaml
assets:
  - id: configs
    type: copy
    source: assets/my-app/          # Directory
    target:
      - /etc/my-app/config.toml     # Copy config.toml from source
      - /etc/my-app/compose/        # Copy compose/ dir from source
    permissions: "0644"
    conflict_strategy: overwrite
```

Revive automatically matches each target's **basename** to a child in the source directory. So the above example:
- Copies `assets/my-app/config.toml` → `/etc/my-app/config.toml`
- Copies `assets/my-app/compose/` → `/etc/my-app/compose/`

---

## Secrets

Secrets are age-encrypted files. They're decrypted to memory during `rv restore` and never written to disk in plaintext.

### Secret Schema

```yaml
secrets:
  - id: aws_credentials
    source: secrets/aws_creds              # Directory or file path
    target:
      - ~/.aws/credentials                 # Decrypts from secrets/aws_creds/credentials.age
      - ~/.aws/credentials.deploy          # Decrypts from secrets/aws_creds/credentials.deploy.age
    permissions: "0600"                    # Must be restrictive (no world/group access)
    owner: null                            # Owner username
```

### Field Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | `str` | ✅ Yes | Unique identifier. Referenced by profiles. |
| `source` | `str` | ✅ Yes | Path to encrypted `.age` file or directory. |
| `target` | `str \| list[str]` | ✅ Yes | System destination path(s). |
| `permissions` | `str` | ✅ Yes (or ❌ No?) | 4-digit octal string. **Must be restrictive**: `"0600"` or `"0400"` only. Group/world bits cause validation error. |
| `owner` | `str \| null` | ❌ No | Unix username. `null` means current user. |

### Secret Workflow

1. **Encryption**: `rv secret encrypt secrets/aws_creds ~/.aws/credentials --recipient <pubkey>`
2. **Backup**: Store `secrets/aws_creds.age` in your repository
3. **Restore**: `rv restore base --identity ~/.config/rv/identity.txt` decrypts and writes to `~/.aws/credentials`
4. **Update**: `rv backup base --identity ~/.config/rv/identity.txt` re-encrypts live files back to repo

### Multi-Target Secrets

Like assets, secrets can have multiple targets:

```yaml
secrets:
  - id: github_keys
    source: secrets/github/
    target:
      - ~/.ssh/id_github                   # From secrets/github/id_github.age
      - ~/.ssh/id_github.pub               # From secrets/github/id_github.pub.age
    permissions: "0600"
```

---

## Packages

Declare system packages to install via native package managers.

### Supported Package Managers

| Manager | Platform | Example |
|---------|----------|---------|
| `apt` | Debian/Ubuntu | `curl`, `git`, `build-essential` |
| `brew` | macOS/Linux | `ripgrep`, `fzf`, `starship` |
| `cargo` | Rust | `ripgrep`, `tokio-cli` |
| `dnf` | Fedora/RHEL | `curl`, `python3-devel` |
| `docker` | Docker | Image pulls + compose |
| `flatpak` | Linux (desktop) | `com.spotify.Client` |
| `nix` | NixOS/standalone | `ripgrep`, `nodejs` |
| `pacman` | Arch Linux | `ripgrep`, `base-devel` |
| `pip` | Python | `black`, `poetry`, `pytest` |
| `snap` | Linux (snap) | `nvim`, `discord` |
| `node` | Node.js (nvm/fnm) | Version files or explicit versions |

### Package Schema

```yaml
packages:
  apt:
    - curl
    - git
    - build-essential
  brew:
    - ripgrep
    - fzf
  docker:
    images:
      - postgres:16
      - redis:alpine
  node:
    version_file: .nvmrc           # Or use 'version: "20.11.0"'
  pip:
    - black
    - poetry
```

### Package Caching

Revive caches package installation status in `~/.config/rv/package-cache.json` (24-hour TTL) to speed up restores. To force reinstalls:

```bash
rv restore base --force-packages
```

---

## Profiles

Profiles are named collections of assets, secrets, and packages that can be restored as a unit.

### Profile Schema

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
      - base                        # Inherits all assets/secrets/packages from base
    assets:
      - work_ssh_config
    secrets:
      - work_vpn_key
    packages:
      - docker
      - node
```

### Field Reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `assets` | `list[str]` | ❌ No | Asset IDs to include (from global `assets` pool). |
| `secrets` | `list[str]` | ❌ No | Secret IDs to include (from global `secrets` pool). |
| `packages` | `list[str]` | ❌ No | Package manager keys to install (from global `packages` dict). |
| `extends` | `list[str]` | ❌ No | Profile names to inherit from. Resolved recursively. |

### Profile Inheritance

Profiles can extend other profiles. Inheritance is resolved recursively:

```yaml
profiles:
  minimal:
    assets: [zshrc]

  base:
    extends: [minimal]
    assets: [gitconfig, ssh_config]

  work:
    extends: [base]
    assets: [work_ssh_key]
    packages: [docker, node]
```

When you run `rv restore work`, you get assets from `minimal`, `base`, and `work` combined.

### Using Profiles

```bash
# Single profile
rv restore base

# Multiple profiles
rv restore base work

# Comma-separated
rv restore base,work
```

---

## Machine Overrides

Apply host-specific configuration overrides without editing the main manifest.

### Machine Overrides Configuration

In your main `manifest.yaml`:

```yaml
machine_overrides:
  enabled: true
  path: "machine/{hostname}.yaml"
```

The `{hostname}` token is replaced at runtime with your system's hostname.

### Override File Format

Create `machine/<your-hostname>.yaml` (same schema as partial manifest.yaml):

```yaml
# machine/my-workstation.yaml
packages:
  apt:
    - libssl-dev
    - docker.io
    - nvidia-driver-535

assets:
  - id: local_zshrc_extension
    type: symlink
    source: assets/zshrc.local
    target: ~/.zshrc.local
```

These overrides are **merged** with the main manifest at restore time. Only that machine sees them.

### Override Search Order

1. Main `manifest.yaml` (global config)
2. `machine/{hostname}.yaml` (host-specific overrides) — **merged on top**

Machine-specific values override global values.

---

## Backup Retention

Control how long transaction backups are retained before automatic cleanup.

### Backup Retention Schema

```yaml
backup_retention:
  max_count: 10         # Maximum number of backup snapshots to keep
  max_age_days: 30      # Maximum age of backups in days
```

Every `rv restore` operation creates a snapshot of pre-existing state in `~/.config/rv/backups/<tx_id>/`. The `BackupPruner` cleans up old snapshots based on these settings.

### Field Reference

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `max_count` | `int` | ❌ No | `10` | Keep at most this many backup snapshots. Older ones are deleted first. |
| `max_age_days` | `int` | ❌ No | `30` | Keep backups at most this many days old. Older ones are deleted. |

### Automatic Pruning

After every successful `rv restore`, backups are automatically pruned based on these limits. To manually prune:

```bash
rv prune                    # Interactive mode
rv prune --dry-run          # Preview what would be deleted
rv prune --yes              # Skip confirmation
```

---

## Complete Example

Here's a realistic `manifest.yaml`:

```yaml
version: 2

assets:
  - id: zshrc
    type: symlink
    source: assets/zshrc
    target: ~/.zshrc
    permissions: "0644"
    conflict_strategy: prompt

  - id: gitconfig
    type: template
    source: assets/gitconfig.j2
    target: ~/.gitconfig
    permissions: "0644"
    template_vars:
      GIT_NAME: John Doe
      GIT_EMAIL: john@example.com

  - id: ssh_config
    type: copy
    source: assets/ssh/config
    target: ~/.ssh/config
    permissions: "0600"
    conflict_strategy: overwrite

secrets:
  - id: github_key
    source: secrets/github_deploy_key
    target: ~/.ssh/id_github
    permissions: "0600"

packages:
  apt:
    - curl
    - git
    - build-essential
  brew:
    - ripgrep
    - fzf
  pip:
    - poetry

profiles:
  base:
    assets:
      - zshrc
      - gitconfig
    packages:
      - apt
      - brew

  dev:
    extends: [base]
    assets:
      - ssh_config
    secrets:
      - github_key
    packages:
      - pip

backup_retention:
  max_count: 20
  max_age_days: 60

machine_overrides:
  enabled: true
  path: "machine/{hostname}.yaml"
```

---

## Validation Errors

If your manifest is invalid, `rv doctor` will report detailed errors:

```bash
rv doctor
```

Common validation errors:

| Error | Cause | Fix |
|-------|-------|-----|
| `permissions must be a 4-digit octal string` | Unquoted number: `permissions: 644` | Quote it: `permissions: "0644"` |
| `secret permissions allow world access` | World-readable secret (e.g. `"0644"`) | Use `"0600"` or `"0400"` |
| `source path contains '..'` | Asset source goes outside repo (e.g. `../../../etc`) | Use relative path inside repo |
| `profile references unknown asset id` | Asset ID doesn't exist in global `assets` | Define it in global `assets` pool first |
| `field required` | Missing required field | Add the field |

---

## Tips & Best Practices

1. **Use symlinks for configs that are frequently read** (dotfiles, vim configs) — faster than copies.
2. **Use copies for binaries and scripts** — allows in-place updates without repo changes.
3. **Use templates for host-specific configs** — interpolate `${HOSTNAME}`, `${USER}`, etc.
4. **Keep secrets encrypted** — never commit plaintext passwords, keys, or tokens.
5. **Use profiles for different use cases** — `base`, `work`, `media`, `server`, etc.
6. **Test with `--dry-run`** — preview changes before applying: `rv restore base --dry-run`
7. **Review diffs** — check what changed before committing: `rv diff -p base | less`
8. **Use machine overrides for one-off configs** — don't pollute the main manifest.

---

## See Also

- [CLI Reference](../README.md#-cli-command-reference)
- [Architecture](../ARCHITECTURE.md)
- [Troubleshooting](../TROUBLESHOOTING.md)
