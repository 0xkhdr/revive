# Architecture — Revive (`rv`)

> Comprehensive technical reference for the architecture, module map, data flows,
> and design decisions of Revive v1.x.

---

## Table of Contents

- [System Overview](#system-overview)
- [Module Map](#module-map)
- [Data Flows](#data-flows)
  - [Restore Flow (repo → system)](#restore-flow-repo--system)
  - [Backup Flow (system → repo)](#backup-flow-system--repo)
  - [Transaction Lifecycle](#transaction-lifecycle)
- [Key Design Decisions (ADRs)](#key-design-decisions-adrs)
- [Security Architecture](#security-architecture)
- [Plugin Sandbox Architecture](#plugin-sandbox-architecture)
- [State Model](#state-model)
- [Configuration Files](#configuration-files)
- [Tech Stack](#tech-stack)

---

## System Overview

Revive is a **transaction-safe developer environment lifecycle manager**. It enforces
a unidirectional sync invariant:

```
Desired State ≡ Repository State ≡ Local System State
```

Git commits are the source of truth. `rv restore` applies them. `rv backup` optionally
reverses the flow to capture live system changes back into the repository.

**Core properties:**

| Property | Description |
|----------|-------------|
| Unidirectional primary flow | `repo → system` via `rv restore` |
| Bidirectional capability | `system → repo` via `rv backup` |
| Atomic transactions | All filesystem mutations journaled; full rollback on failure |
| Defense-in-depth security | Age encryption, in-memory zeroing, log scrubbing, path validation |
| Platform | Linux (primary), macOS (supported); Windows deferred post-1.0 |

---

## Module Map

```text
src/rv/
├── __init__.py              # Package entrypoint; defines __version__
├── __main__.py              # PyInstaller entrypoint (python -m rv)
│
├── cli/
│   └── main.py              # Typer CLI app; all user-facing commands
│
├── gui/
│   ├── server.py            # http.server-based Web GUI (rv gui)
│   └── static/              # index.html, styles.css, app.js (cosmic-dark dashboard)
│
├── logging/
│   └── audit.py             # Dual output: structured JSON audit + Rich console
│
├── models/
│   ├── manifest.py          # Pydantic v2 models: Manifest, Asset, Secret, Profile
│   ├── transaction.py       # TransactionJournal, ManifestLock schemas
│   └── workspace.py         # WorkspaceRegistry models (~/.config/rv/workspaces.yaml)
│
├── plugins/
│   ├── context.py           # ReviveContext schema passed to plugin subprocess
│   ├── loader.py            # Plugin discovery (workspace → user-global → builtin)
│   ├── sandbox.py           # Subprocess coordinator + timeout enforcer
│   ├── sandbox_wrapper.py   # In-process builtins/socket/subprocess patcher
│   └── builtin/
│       ├── mcp_config/      # Sync MCP server config to Claude Desktop
│       ├── claude_prompts/  # Sync Claude AI prompt templates
│       └── python_skills/   # Sync AI agent skill files
│
├── providers/
│   ├── base.py              # BaseProvider: retry executor, package cache interface
│   ├── apt.py               # APT (Debian/Ubuntu)
│   ├── brew.py              # Homebrew (macOS/Linux)
│   ├── cargo.py             # Rust Cargo
│   ├── dnf.py               # DNF (Fedora/RHEL)
│   ├── docker.py            # Docker image pull + compose
│   ├── flatpak.py           # Flatpak
│   ├── nix.py               # Nix package manager
│   ├── node.py              # Node.js (nvm/fnm detection + .nvmrc matching)
│   ├── pacman.py            # Pacman (Arch Linux)
│   ├── pip.py               # Python pip
│   └── snap.py              # Snap
│
├── security/
│   ├── encryptor.py         # Age crypto engine (pyrage + age CLI fallback)
│   ├── permissions.py       # POSIX chmod validator + enforcer
│   ├── scrubber.py          # Regex credential scrubber for logs
│   ├── tempfile.py          # Secure temp files (0600 permissions)
│   └── zerobuffer.py        # Explicit in-memory byte zeroing
│
├── services/
│   ├── backup.py            # BackupService: system → repo direction
│   ├── doctor.py            # DoctorService: system health diagnostics
│   ├── handlers.py          # AssetHandler: copy, symlink, template, secret executors
│   ├── recovery.py          # RecoveryService + BackupPruner: journal replay + pruning
│   ├── restore.py           # RestoreService: 14-step unidirectional apply + ManifestLoader
│   ├── status.py            # StatusService: drift detection + colored diff generation
│   └── workspace.py         # WorkspaceService: global workspace registry management
│
├── transactions/
│   ├── atomic.py            # Atomic temp-write + os.replace (prevents partial writes)
│   ├── context.py           # 7-step TransactionContext with journal-based rollback
│   └── lock.py              # flock-based process serialization (~/.config/rv/rv.lock)
│
├── utils/
│   ├── interpolate.py       # ${VAR:-default} env var interpolation + .env loader
│   ├── path.py              # Path canonicalization, traversal checks, symlink loops
│   └── platform.py          # OS/distro detection (Linux distro, macOS version)
│
└── watchers/
    └── daemon.py            # Watchdog daemon: auto-restore on repository file changes
```

---

## Data Flows

### Restore Flow (repo → system)

```
┌──────────────────────────────────────────────────────────────────────┐
│                        rv restore <profile>                          │
└──────────────────────────────────────────────────────────────────────┘
         │
         ▼
   [Step 0] ProcessLock.acquire()          ← flock on ~/.config/rv/rv.lock
         │
         ▼
   [Step 1] ManifestLoader.load()          ← Pydantic v2 strict validation
         │
         ▼
   [Step 2] ProfileResolver.resolve()      ← Recursive inheritance chain
         │
         ▼
   [Step 3] MachineOverrides.merge()       ← machine/{hostname}.yaml overlay
         │
         ▼
   [Step 4] DependencyVerifier.check()     ← tool availability checks
         │
         ▼
   [Step 5] AgeEncryptor.decrypt()         ← secrets → ZeroBuffer (memory only)
         │
         ▼
   [Pre-restore hooks] PluginSandbox.run() ← sandboxed subprocess per plugin
         │
         ▼
   [Step 6] TransactionContext.snapshot()  ← backup existing files to ~/.config/rv/backups/
         │
         ▼
   [Steps 7-9] AssetHandler.*()            ← atomic symlinks, copies, template renders
                                              + per-asset hooks (RV_ASSET_ID env)
         │
         ▼
   [Step 10] Providers.install()           ← apt/brew/cargo/docker/flatpak/nix/node/
                                              pacman/pip/snap package orchestration
         │
         ▼
   [Post-restore hooks] PluginSandbox.run()
         │
         ▼
   [Step 12] PermissionValidator.verify()  ← checksum + chmod comparison
         │
         ▼
   [Step 13] ManifestLock.write()          ← records committed state + rendered_checksums
         │
         ▼
   [Step 14] AuditLogger.log()             ← JSON audit entry + BackupPruner.prune()
         │
         ▼
   TransactionContext.commit()             ← journal marked committed
   TransactionContext.cleanup()            ← wipe backup snapshots + journal
```

### Backup Flow (system → repo)

```
rv backup <profile>
    │
    ▼
ManifestLoader.load()           ← validate manifest
    │
    ▼
ProfileResolver.resolve()       ← expand profile (with inheritance)
    │
    ▼
for each asset/secret:
    copy   → shutil.copy2(target → source)
    symlink → skip if already pointing to repo source; else copy2
    template → SKIP (cannot reverse rendered output)
    secret  → AgeEncryptor.encrypt(target → source.age)
    │
    ▼
(no TransactionContext — writes go directly to repository)
```

### Transaction Lifecycle

```
┌────────────────────────────────────┐
│         TransactionContext         │
│                                    │
│  1. Plan      plan_operation()     │
│  2. Validate  _validate()          │
│  3. Snapshot  _snapshot()  ──────► ~/.config/rv/backups/<tx_id>/
│  4. Execute   _execute()           │ (journal written)
│  5. Verify    _verify()            │
│  6. Commit    _commit()   ──────► manifest.lock updated
│  7. Cleanup   _cleanup()  ──────► backup + journal wiped
│                                    │
│  ← any step failure triggers:      │
│     _rollback() from journal       │
└────────────────────────────────────┘
```

---

## Key Design Decisions (ADRs)

### ADR-001: Pydantic v2 with strict=True

**Decision**: All data models use `model_config = ConfigDict(strict=True)`.

**Rationale**: Eliminates silent type coercion bugs (e.g., `"0644"` → `644` integer).
Manifest parsing is the first point of contact with untrusted user-provided YAML.
Strict validation catches schema drift before any filesystem mutation occurs.

**Tradeoff**: Callers must always pass correctly-typed data; no implicit coercion.

---

### ADR-002: In-Process Sandbox vs. Container Isolation

**Decision**: Plugins run in a Python subprocess with in-process builtins patching,
not in a container or seccomp jail.

**Rationale**: Container isolation would require Docker to always be present and adds
significant startup latency. The target use case (dotfile management on developer
laptops) makes containers impractical. The in-process sandbox provides meaningful
defense-in-depth for honest plugins and misconfigured plugins.

**Known Limitation**: Native extensions (`.so` files) embedded in plugin dependencies
can escape. Documented in `SECURITY.md` as KL-002.

---

### ADR-003: No shell=True in Any Subprocess Call

**Decision**: All `subprocess.Popen` / `subprocess.run` calls pass argument lists,
never shell strings.

**Rationale**: Shell injection is a class of vulnerability, not a bug. Enforced via
`ruff check` (rule S603) and `bandit`. Argument lists also make subprocess invocations
auditable.

---

### ADR-004: Age Encryption with pyrage + CLI Fallback

**Decision**: Use `pyrage` (Rust-backed Python binding) as the primary age implementation
with `age` CLI as a fallback.

**Rationale**: `pyrage` avoids spawning an external subprocess for every encrypt/decrypt
operation and enables in-memory secret handling. CLI fallback ensures compatibility on
systems where `pyrage` native binaries are unavailable.

---

### ADR-005: flock-based Process Lock (not PID file)

**Decision**: `ProcessLock` uses `fcntl.flock()` on `~/.config/rv/rv.lock`.

**Rationale**: `flock` locks are automatically released when the process exits (including
on crash), eliminating the stale PID file problem. This guarantees exactly-once
concurrent execution without cleanup logic.

---

### ADR-006: Dynamic Lockfile Path per Manifest

**Decision**: The lockfile path is derived from the manifest path:
`manifest-custom.yaml` → `manifest-custom.lock`.

**Rationale**: Users can maintain multiple manifests for different environments
(build vs. restore, dev vs. prod). If all manifests shared a single lockfile,
switching manifests would corrupt the lock state.

---

## Security Architecture

```
┌─────────────────────────────────────────────────┐
│                  Secret Lifecycle                │
│                                                 │
│  .age file ──► AgeEncryptor.decrypt()           │
│                     │                           │
│                     ▼                           │
│              ZeroBuffer (bytearray)             │
│                     │                           │
│            write to target path                 │
│                     │                           │
│                     ▼                           │
│          ZeroBuffer.zero_bytes()  ◄── wipe      │
│          (CPython memory zeroing)               │
│                                                 │
│  Logs scrubbed via SecretScrubber (regex)       │
│  Temp files created with 0600 (SecureTempFile)  │
└─────────────────────────────────────────────────┘
```

| Layer | Component | Guarantee |
|-------|-----------|-----------|
| Encryption at rest | `AgeEncryptor` (pyrage) | Only identity holder can decrypt |
| Memory safety | `ZeroBuffer` | Best-effort CPython plaintext zeroing after use |
| Log safety | `SecretScrubber` | Regex strips credentials from all log output |
| Permission safety | `PermissionValidator` | chmod enforced at write + verify time |
| Path safety | `path.py` | `..` traversal and symlink loop detection |
| Concurrency safety | `ProcessLock` (flock) | One rv operation at a time per machine |
| Subprocess safety | No `shell=True` | Argument lists only; auditable by ruff |
| CORS safety | `server.py` | Loopback-restricted by default; `--cors-wildcard` opt-in |

---

## Plugin Sandbox Architecture

```
rv restore (main process)
    │
    ▼
PluginSandbox.run(plugin, context, hook_type)
    │
    ├── serialize context → base64 JSON
    ├── set REVIVE_CONTEXT env var
    │
    ▼
subprocess.Popen(
    ["python", "-m", "rv.plugins.sandbox_wrapper",
     entrypoint, perms_b64, context_b64, hook_type],
    timeout=plugin.timeout   # max 300s
)
    │
    ▼
sandbox_wrapper.py (child process):
    ├── patch builtins.open          (filesystem gate)
    ├── patch os.remove/mkdir/etc.   (filesystem gate)
    ├── patch socket.socket          (network gate if network=false)
    ├── patch subprocess.Popen/run   (shell gate if shell=false)
    ├── patch os.system/popen/spawn* (shell gate)
    ├── intercept ctypes/cffi/gc/importlib imports (via _SandboxedSysModules)
    ├── setrlimit(RLIMIT_AS, 2 GiB)  (memory limit)
    ├── setrlimit(RLIMIT_CPU, 310s)  (CPU limit)
    └── exec plugin entrypoint
```

**Plugin discovery order** (first match wins by name):

1. `<repo_dir>/plugins/` — workspace-local
2. `~/.config/rv/plugins/` — user-global
3. `<rv_package>/plugins/builtin/` — shipped first-party

---

## State Model

### System State Files

| Path | Purpose |
|------|---------|
| `~/.config/rv/rv.lock` | Process lock (flock) |
| `~/.config/rv/workspaces.yaml` | Global workspace registry |
| `~/.config/rv/package-cache.json` | Package status cache (24h TTL) |
| `~/.config/rv/audit.log` | Structured JSON audit log (all operations) |
| `~/.config/rv/backups/<tx_id>/` | Per-transaction pre-mutation backup snapshot |
| `<repo>/<manifest>.lock` | Last committed state + rendered_checksums |

### Manifest Files (per repository)

| File | Purpose |
|------|---------|
| `manifest.yaml` | Default manifest |
| `manifest-build.yaml` | Build/dev environment manifest (generated by `rv init`) |
| `manifest-restore.yaml` | Runtime/production restore manifest (generated by `rv init`) |
| `machine/<hostname>.yaml` | Machine-specific overrides |

---

## Configuration Files

### `manifest.yaml` Schema (v2)

```yaml
version: 2                        # Schema version (must be 2)

assets: []                        # Global asset pool
secrets: []                       # Global secret pool
packages: {}                      # Package manager declarations

profiles: {}                      # Named restore profiles (with optional extends:)

backup_retention:                 # Optional: backup pruning config
  max_count: 10                   # Keep last N backup snapshots
  max_age_days: 30                # Discard snapshots older than N days

machine_overrides:
  enabled: true
  path: "machine/{hostname}.yaml" # {hostname} resolved at runtime
```

### `~/.config/rv/workspaces.yaml` Schema

```yaml
workspaces:
  - name: personal-dotfiles
    path: /home/user/dotfiles
    default_profile: base
  - name: work-configs
    path: /home/user/work/configs
    default_profile: work
```

---

## Tech Stack

| Component | Library | Version | Role |
|-----------|---------|---------|------|
| CLI framework | Typer | ≥ 0.9 | Command parsing + help generation |
| Terminal UI | Rich | ≥ 13 | Panels, tables, colored diffs, progress |
| Data models | Pydantic v2 | ≥ 2.0 | Schema validation (strict mode) |
| Template engine | Jinja2 | ≥ 3.0 | Asset template rendering |
| Encryption | pyrage | ≥ 1.0 | Age encryption (Rust-backed) |
| File watching | watchdog | ≥ 3.0 | `rv watch` daemon |
| YAML parsing | PyYAML | ≥ 6.0 | Manifest + override parsing |
| Testing | pytest | ≥ 7.0 | Test runner |
| Coverage | pytest-cov | ≥ 4.0 | Coverage reporting |
| Linting | ruff | ≥ 0.1 | Fast Python linter + formatter |
| Type checking | mypy | ≥ 1.0 | Strict static analysis |
| Security scan | bandit | ≥ 1.7 | Python security linter |
| Binary packaging | PyInstaller | ≥ 6.0 | Self-contained `rv` binary |
