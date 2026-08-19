# 02 — Domain Model

Everything `rv` does is a function of five data structures: **Manifest**, **ResolvedProfile**,
**TransactionJournal**, **Lockfile**, and **WorkspaceConfig**. This document is the authoritative
schema for all of them.

---

## 1. Manifest (`manifest.yaml`)

The declaration. Lives at the workspace root. A custom path may be given with `--manifest/-m`.

### 1.1 Root

```yaml
version: 2                    # int, required-by-default (defaults to 2)
assets: []                    # []Asset — the global asset pool
secrets: []                   # []Secret — the global secret pool
packages: {}                  # Packages
profiles: {}                  # map[string]Profile
machine_overrides: {}         # MachineOverridesConfig
backup_retention: {}          # BackupRetentionConfig
```

**Version validation MUST happen twice**: once on the raw decoded YAML before struct binding, and
once during struct validation. Supported set is `{1, 2}`. Anything else is a hard error naming the
supported versions and telling the user to upgrade `rv` or downgrade the manifest. The reason for
the double check: a future schema could rename fields in a way a lenient decoder would accept as
"valid garbage", silently corrupting a machine.

```go
var ErrUnsupportedSchemaVersion = errors.New("unsupported manifest schema version")
```

### 1.2 Asset

```yaml
- id: example_zshrc           # string, required, unique within the pool
  type: symlink               # symlink | copy | template | secret   (default: symlink)
  source: assets/example_zshrc # string, required, RELATIVE to the manifest
  target: ${USER_HOME}/.zshrc # string OR []string, required
  permissions: "0644"         # string|null, 4-digit octal starting with 0
  owner: null                 # string|null, username; null = current user
  conflict_strategy: prompt   # prompt | overwrite | skip | abort     (default: prompt)
  encrypted: false            # bool; forced true when type == secret
  template_vars: null         # map[string]any|null, only meaningful for type: template
  hooks:                      # per-asset lifecycle hooks
    pre: []
    post: []
```

**Validation rules (all MUST be enforced at load):**

| Field | Rule |
|-------|------|
| `source` | After path normalization: MUST NOT be absolute, MUST NOT begin with `..`. This is the primary path-traversal guard. |
| `permissions` | If set: exactly 4 characters, first character `0`, parses as octal. Reject `644`, `0o644`, `rwxr-xr-x`. |
| `type: secret` | Setting this forces `encrypted = true` regardless of what was written. |
| `target` | Accepts a scalar or a list. A list means "deploy this one source to each of these targets". |

**Asset types:**

- `symlink` — create a symlink at `target` pointing at the **absolute** path of `source` in the repo.
  Editing the file on the machine edits the repo. This is the default and the common case.
- `copy` — copy `source` to `target`. If `encrypted: true`, decrypt first. A directory source is
  copied recursively and atomically.
- `template` — render `source` as a template, write the result to `target`. See §1.3.
- `secret` — reserved for entries in the `secrets:` block; behaves as an encrypted copy with
  enforced strict permissions.

### 1.3 Templates

**Engine: Go stdlib `text/template` with `Option("missingkey=error")`. [DIVERGE]**

A reference to an undefined variable is a **hard error**, never an empty string. Syntax is
`{{ .name }}` — the leading dot is required, because the context is a map. Full syntax reference,
the function map, and the Jinja2 migration table are in
[09-go-architecture.md](09-go-architecture.md) §4.

The Python implementation used Jinja2 (`{{ name }}`, `{% if %}`). **Templates written for it will
not work unchanged.** `rv doctor` detects Jinja2 syntax in template sources and reports it as a
critical `template_syntax` issue — Jinja2 `{% … %}` tags would otherwise pass through
`text/template` as literal text and land in the output file instead of failing.

The rendering context is built by merging, **in this order** (later wins):

1. All process environment variables.
2. Built-in variables:

   | Name | Value |
   |------|-------|
   | `_hostname` | OS hostname |
   | `_user` | current username |
   | `_platform` | `linux`, `darwin`, … |
   | `_arch` | `amd64`, `arm64`, … |
   | `_home` | user home directory |
   | `_repo_dir` | absolute workspace path |

3. The asset's `template_vars`.

The SHA-256 of the **rendered output** MUST be recorded in the transaction and written to the
lockfile's `rendered_checksums` map, keyed by asset ID. This is what makes template drift detectable.

### 1.4 Asset hooks

```yaml
hooks:
  pre:
    - command: "mkdir -p /opt/app/conf"
  post:
    - command: "systemctl --user restart app"
    - plugin: "notify"       # NOT SUPPORTED at asset level — see below
```

- `command` hooks are split with shell-style word splitting and executed **without a shell**
  (`shell=True` equivalent is forbidden). Timeout: **30 seconds**. A non-zero exit MUST fail the
  asset, which fails the transaction, which triggers rollback.
- Hook processes receive these environment variables in addition to the inherited environment:
  `RV_ASSET_ID`, `RV_ASSET_TARGET`, `RV_TX_ID`, `RV_HOOK_STAGE` (`pre` or `post`).
- `plugin` references at the **asset** level MUST be rejected with an error directing the user to
  profile-level `pre-restore`/`post-restore` hooks. Failing loudly is required — silently dropping
  the hook would violate the rollback guarantee.

**Hooks run inside the transaction. [DIVERGE]** A hook is a planned operation, not a planning-time
side effect. It is registered during planning and **executed in the execute phase**, immediately
around its own asset's mutation:

```
… delete old ~/.gitconfig … → pre-hook → write ~/.gitconfig → chmod → post-hook → next asset …
```

Three consequences, all of them the point of the change:

1. **`--dry-run` never runs a hook.** It logs what would run. (The Python implementation ran
   per-asset hooks during planning, so `--dry-run` executed them — a genuine bug.)
2. **Ordering is what the user expects.** The pre-hook for asset 5 runs after asset 4's file is
   written, not before asset 1's.
3. **A hook failure rolls back the file mutations** already applied in this transaction.

What rollback **cannot** do is un-run a hook. `systemctl restart` has no inverse. Rollback therefore
restores every file and **reports which hooks already executed** so the user knows what side effects
survive. See [04-restore-engine.md](04-restore-engine.md) §3.

### 1.5 Secret

```yaml
secrets:
  - id: card_express_env
    source: secrets/card_express_env   # relative; typically a .age file or a dir of them
    target:
      - ${CARD_EXPRESS_DIR}/.env
      - ${CARD_EXPRESS_DIR}/.env.deploy
    permissions: "0600"                # default "0600"
    owner: null
```

Secrets are Assets with three differences, all forced at load time:

1. `type` is always `secret`; `encrypted` is always `true`.
2. `permissions` defaults to `0600` and MUST satisfy `mode & 0o077 == 0` — no group or world bits.
   A secret declared `0644` is a hard validation error, not a warning.
3. Same relative-path/no-traversal rule on `source`.

### 1.6 Packages

```yaml
packages:
  brew: []          # []string
  apt: []           # []string
  flatpak: []
  snap: []
  pacman: []
  dnf: []
  nix: []            # names are passed to nix-env as nixpkgs.<pkg>
  cargo: []
  pip: []            # installed with --user
  docker:
    images: []       # []string, pulled
  node:
    version_file: .nvmrc   # string|null, relative to the workspace
    version: null          # string|null, explicit version; wins over version_file
```

### 1.7 Profile

```yaml
profiles:
  base:
    extends: []       # []string — parent profile names
    assets: []        # []string (IDs from the global pool) OR inline Asset objects
    secrets: []       # []string (IDs) OR inline Secret objects
    packages: []      # []string — names of package groups to pull in: apt, brew, docker, node, …
```

A profile references assets by ID (resolved against the global pool) or declares them inline.
`packages` is a list of **group names**, not package names — listing `apt` pulls in the entire
top-level `packages.apt` list.

### 1.8 Machine overrides

```yaml
machine_overrides:
  enabled: true
  path: "machine/{hostname}.yaml"   # {hostname} is substituted with the OS hostname
```

If the resolved file exists, its `assets`, `secrets`, and `packages` blocks are merged over the
resolved profile. Assets and secrets merge **by ID, last-write-wins**. Package lists **append**.
`node.version` / `node.version_file` overwrite. A missing override file is normal and silent; a
malformed one is a hard error.

### 1.9 Backup retention

```yaml
backup_retention:
  max_count: 10      # int >= 1 — keep at most N transaction backup snapshots
  max_age_days: 30   # int >= 1 — delete snapshots older than N days
```

Applied automatically after every successful restore, and manually via `rv prune`.

---

## 2. Path interpolation

Every `target` value passes through interpolation before use, in both restore and backup.

**Syntax:** `${VAR}` and `${VAR:-default}`. Matching regex, verbatim:

```
\$\{([a-zA-Z_][a-zA-Z0-9_]*)(?::-([^}]+))?\}
```

Rules:

- `${VAR}` where `VAR` is unset and no default is given is a **hard error**, never an empty string.
  Silently expanding to `""` would write files to `/` — the strictness is the point.
- `${VAR:-fallback}` uses `fallback` when `VAR` is unset.
- Before interpolation, a `.env` file at the workspace root is loaded into the environment if
  present. Parsing: skip blanks and `#` comments, split on the **first** `=`, trim whitespace, strip
  one layer of matching surrounding quotes, and **do not overwrite** a variable already set in the
  real environment.

After interpolation the path is canonicalized: expand `~`, expand `$VAR` shell-style, then make
absolute. **Symlinks MUST NOT be resolved** during canonicalization — the target of a symlink asset
is the link itself, and resolving would write through it.

---

## 3. ResolvedProfile (in-memory)

The output of profile resolution — the flattened thing the engine actually applies.

```go
type ResolvedProfile struct {
    Assets      map[string]Asset   // keyed by ID
    Secrets     map[string]Secret  // keyed by ID
    Packages    map[string][]string // provider name -> package names
    DockerImages []string
    NodeConfig  struct{ Version, VersionFile string }
}
```

### Resolution algorithm

```
resolve(manifest, profileName):
    names := split(profileName, ",") and trim; error if empty
    if len(names) == 1:
        error if name not in manifest.profiles
        resolveRecursive(manifest, name, out, visited=[])
        return out
    // multiple profiles: resolve each independently, then merge
    for each name: r := resolve(manifest, name)
        out.Assets  = merge(out.Assets, r.Assets)    // last-write-wins by ID
        out.Secrets = merge(out.Secrets, r.Secrets)  // last-write-wins by ID
        packages / dockerImages: append, deduplicated, order preserved
        node: a non-empty value overwrites

resolveRecursive(manifest, name, out, visited):
    if name in visited: error "Cyclic profile inheritance detected: a -> b -> a"
    visited = visited + [name]
    for each parent in profile.extends:
        resolveRecursive(manifest, parent, out, copy(visited))   // parents FIRST
    merge this profile's assets   (string IDs looked up in the global pool; unknown ID = error)
    merge this profile's secrets  (same)
    for each group in profile.packages: append the manifest's top-level list for that group
```

Two properties are load-bearing and MUST be preserved:

- **Parents resolve before children**, so a child's asset with the same ID overrides the parent's.
- **`visited` is copied per branch**, so a diamond (`c extends a, b`; `a extends base`;
  `b extends base`) is legal, while a true cycle is detected. The error message MUST include the
  full chain.

Multiple profiles are accepted as a comma-separated string or as repeated arguments; both are
normalized to the same comma-joined form.

---

## 4. TransactionJournal

Written to `~/.config/rv/journals/<tx_id>.json`. Its whole job is to make a crashed restore
recoverable.

```go
type RollbackEntry struct {
    Op          string  `json:"op"`           // create | modify | delete | symlink | chmod
    SrcBackup   *string `json:"src_backup"`   // path to the pre-state backup, nil if target was absent
    Target      string  `json:"target"`       // absolute path that was mutated
    Checksum    *string `json:"checksum"`     // SHA-256 of the target BEFORE mutation
    Permissions *string `json:"permissions"`  // octal mode of the target BEFORE mutation
}

type TransactionJournal struct {
    TxID      string          `json:"tx_id"`      // UUID
    Timestamp float64         `json:"timestamp"`  // unix seconds, float
    Status    string          `json:"status"`     // pending|executing|verifying|committed|rolling_back|rolled_back|aborted
    Entries   []RollbackEntry `json:"entries"`    // in execution order; rollback walks it in reverse
}
```

Backups for transaction `T` live in `~/.config/rv/backups/<T>/`, one file per entry, named
`backup_<index>_<basename>`. A backed-up **symlink** is stored as a text file whose contents are
the literal string `SYMLINK:<link target>` — the rollback path detects this prefix and recreates a
link instead of a file. Keep this format; it is part of the compatibility contract.

Journal and backup directory are deleted only after a successful commit.

---

## 5. Lockfile (`manifest.lock`)

Sits next to the manifest, named by replacing the manifest's extension with `.lock`. **Content is
JSON**, despite the manifest being YAML. Written atomically after a successful restore.

```go
type LockfileEntry struct {
    SHA256OfSource string `json:"sha256_of_source"`
    TargetPath     any    `json:"target_path"`   // string, or []string for multi-target assets
    Permissions    any    `json:"permissions"`   // string, or []string, index-aligned with TargetPath
    MTime          any    `json:"mtime"`         // float64, or []float64, index-aligned
}

type Lockfile struct {
    Entries           map[string]LockfileEntry `json:"entries"`             // keyed by asset/secret ID
    RenderedChecksums map[string]string        `json:"rendered_checksums"`  // asset ID -> SHA-256 of rendered template
}
```

The scalar-or-array polymorphism is inherited from the Python schema and MUST be preserved on both
read and write: an asset whose `target` was a scalar writes scalars; one whose `target` was a list
writes index-aligned arrays. In Go, model this with a custom `UnmarshalJSON`/`MarshalJSON` on a
`StringOrSlice` type rather than `any`.

**Source checksums:** for a file, stream-hash it in 4 KiB chunks. For a directory, walk it with
directories and filenames **sorted**, feeding the relative path of each file into the hash before
its contents — determinism across machines depends on that sort. A missing path hashes to the empty
string.

---

## 6. WorkspaceConfig (`~/.config/rv/workspaces.yaml`)

The global registry of known workspaces, so `rv workspace sync` can update all of them.

```yaml
workspaces:
  - name: dotfiles
    path: /home/user/dotfiles
    last_accessed: 2026-08-19T10:00:00
default_workspace: dotfiles
```

---

## 7. Runtime paths

All of these are part of the compatibility contract. The Go build MUST use the same locations.

| Path | Contents |
|------|----------|
| `~/.config/rv/rv.lock` | Process lock file (advisory `flock`) |
| `~/.config/rv/journals/<tx>.json` | Transaction journals |
| `~/.config/rv/backups/<tx>/` | Pre-mutation backup snapshots |
| `~/.config/rv/identity.txt` | Default age identity. Also probed: `~/.config/rv/keys/identity.txt`, `~/.config/rv/identifier.txt` |
| `~/.config/rv/package-cache.json` | Package idempotency cache |
| `~/.config/rv/workspaces.yaml` | Workspace registry |
| `~/.config/rv/plugins/` | User-global plugins |
| `~/.local/share/rv/audit.log` | Structured JSON audit log (append-only) |

---

## 8. Worked example

```yaml
version: 2

assets:
  - id: zshrc
    type: symlink
    source: assets/zshrc
    target: ${USER_HOME}/.zshrc
    permissions: "0644"
    conflict_strategy: prompt

  - id: app_compose
    type: symlink
    source: assets/app
    target:
      - ${APP_DIR}/compose
      - ${APP_DIR}/docker-compose.yml
    permissions: "0644"
    conflict_strategy: prompt

  - id: gitconfig
    type: template
    source: assets/gitconfig.tmpl
    target: ${USER_HOME}/.gitconfig
    permissions: "0644"
    template_vars:
      email: dev@example.com

secrets:
  - id: app_env
    source: secrets/app_env
    target:
      - ${APP_DIR}/.env
      - ${APP_DIR}/.env.deploy
    permissions: "0600"

packages:
  apt: [git, zsh, ripgrep, jq, docker-ce]
  docker: { images: [] }
  node: { version_file: .nvmrc }

profiles:
  base:
    assets: [zshrc, gitconfig]
    packages: [apt]
  work:
    extends: [base]
    assets: [app_compose]
    secrets: [app_env]
    packages: [apt, node]
```

Note `app_compose` and `app_env`: one source, several targets. When the source is a **directory**,
each target is matched to the file inside the source directory whose basename equals the target's
basename (for encrypted sources, `<basename>.age` is tried first). This is how one `secrets/app_env/`
directory populates both `.env` and `.env.deploy`.

The matching `assets/gitconfig.tmpl`, in `text/template` syntax:

```
[user]
    name = {{ ._user }}
    email = {{ .email }}

[core]
    editor = {{ .EDITOR | default "vim" }}

{{ if eq ._platform "darwin" }}
[credential]
    helper = osxkeychain
{{ end }}
```

`._user` and `._platform` are built-ins, `.email` comes from `template_vars`, `.EDITOR` comes from
the environment. The leading dot is required on every one of them.
