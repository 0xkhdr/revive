# Revive (`rv`) — AI Agent Build Plan: A to Z

> **Version**: 1.0  
> **Date**: 2026-05-22  
> **Scope**: Production-grade CLI tool for developer environment lifecycle management.  
> **Constraint**: Unidirectional state engine (`repo → system`). Bidirectional sync deferred post-1.0.

---

## 1. Situational Analysis & Locked Decisions

### 1.1 User Requirements Lock
| Parameter | Decision |
|-----------|----------|
| **Primary Pain Point** | Fast restore on new machines (Q1: **a**) |
| **Distribution Model** | Public/distributed tool (Q2) |
| **Machine Count** | 2–3 personal machines (Q3) |
| **Package Management** | Install packages via native tool orchestration (Q4) |
| **AI Assets** | Static; treated as standard managed assets (Q5) |

### 1.2 Derived Architectural Decisions
1. **Unidirectional Only**: `repo → system`. No `rv sync push/pull`. Git is the sync mechanism.
2. **Package Orchestration, Not Replacement**: Providers wrap `brew`, `apt`, etc. Idempotency delegated to native tools.
3. **Plugin Sandboxing Mandatory**: Because the tool is distributed, the untrusted-plugin security model (subprocess isolation, `ReviveContext` API, permission manifest) is critical path.
4. **Migrations Engine**: Stub-only until schema v3 is needed. No legacy user base to migrate.
5. **Platform Priority**: Unix-first (Linux/macOS). Windows support scoped to post-1.0.
6. **Golden Path UX**: `pip install revive-cli` → `rv clone <repo>` → `rv restore <profile>` → machine is productive.

### 1.3 Risk Register
| Risk | Phase | Mitigation |
|------|-------|------------|
| PyInstaller fails with `pyrage`/`watchdog` C extensions | 5 | Smoke-test PyInstaller in Phase 0 skeleton validation. Maintain `pip install` fallback. |
| Permission model breaks on non-Unix | 5 | Explicitly gate Windows support. Use `os.chmod` abstractions with platform checks. |
| Secret rotation invalidates lockfile checksums | 2 | Lockfile stores checksum of **encrypted** blob, never plaintext. |
| Package provider idempotency gaps | 3 | Providers only verify presence; installation is best-effort with `--dry-run` preview. |
| Plugin subprocess IPC complexity | 4 | Use JSON-RPC over stdin/stdout. No shared memory. |

---

## 2. Tech Stack & Tooling Lock

| Layer | Technology | Justification |
|-------|------------|---------------|
| Language | Python 3.11+ | Match statements, `tomllib`, modern typing |
| CLI | Typer | Type-safe, auto-generated help, Rich integration |
| TUI | Rich | Tables, progress, colored diffs, spinners |
| Validation | Pydantic v2 | Strict manifest schema, fast serialization |
| Encryption | `pyrage` + `age` CLI fallback | `age` is modern, `pyrage` is native Python; CLI fallback for build edge cases |
| Integrity | `hashlib` (SHA-256) | Lockfile diffing, deterministic verification |
| File Watching | `watchdog` | Cross-platform inotify/fsevents abstraction |
| Distribution | PyInstaller | Single-binary target; `pip install` as primary fallback |
| Testing | pytest + pytest-cov | >90% core coverage mandate |
| Static Analysis | mypy (strict), ruff, bandit | Type safety, linting, security scan |
| Formatting | Ruff (format) + Black-compatible | Single formatter config |

---

## 3. Module Layout (Locked)

```text
src/rv/
├── cli/              # Typer command definitions (Phase 1+)
├── core/             # State engine, config loader, path interpolation
├── models/           # Pydantic schemas (Manifest, Profile, TransactionJournal, etc.)
├── services/         # Business logic: Restore, Status, Doctor, Verify
├── providers/        # Package manager orchestration (brew, apt, flatpak, snap, docker, node)
├── plugins/          # Plugin loader, sandbox runner, ReviveContext API
├── security/         # Encryption, permission enforcement, secure tempfile, secret scrubber
├── transactions/     # Atomic execution, rollback journal, process locking
├── logging/          # Structured JSON audit + human console output
├── watchers/         # Daemon mode (watchdog) with coordination lock
├── migrations/       # Schema versioning (stub until v3)
└── utils/            # Platform detection, path canonicalization, cross-device checks
```

---

## 4. Formal State Model & Execution Order

### 4.1 Core Invariant
```
Desired State == Repository State == Local System State
```
- **Desired State**: `manifest.yaml` + profile inheritance + `machine/<hostname>.yaml` overrides.
- **Repository State**: Committed contents in the `revive` repository.
- **Local System State**: Actual filesystem, symlinks, permissions, installed packages.

### 4.2 Deterministic Apply Order (Mandatory)
All restore operations MUST execute in this exact sequence:
1. Manifest validation (Pydantic strict)
2. Profile resolution (inheritance merge)
3. Machine override merge
4. Dependency validation (target parent dirs exist, cross-device checks)
5. Secret decryption (secure temp, zero buffers post-use)
6. Backup snapshot (to rollback journal)
7. Symlink creation (loop detection, canonical paths)
8. File copy operations (atomic temp + rename)
9. Permission enforcement (`0644` for configs, `0600`/`0700` for secrets)
10. Package installation (provider orchestration)
11. Plugin restore hooks (sandboxed subprocess)
12. Post-apply verification (checksums match lockfile)
13. Lockfile update (`manifest.lock` SHA-256 map)
14. Audit log write (structured JSON)

### 4.3 Transaction Engine (7-Step Boundary)
Every mutating operation runs inside:
1. **Plan**: Precompute execution plan (dry-run compatible).
2. **Validate**: Validate all targets before any write.
3. **Snapshot**: Serialize rollback journal to disk.
4. **Execute**: Atomic temp-file + rename strategy.
5. **Verify**: Integrity check post-apply.
6. **Commit**: Finalize lockfile and journal.
7. **Cleanup**: Remove temporary and backup files.

*Rollback on any failure is mandatory. Incomplete transactions MUST be recoverable via `rv recover` (Phase 5).*

### 4.4 Concurrency Guarantees
- Process file lock at `~/.config/rv/rv.lock` (flock-based).
- Daemon mode (`rv watch`) MUST acquire lock before scanning; CLI commands block or fail if lock held.

---

## 5. Manifest Schema v2 (Formal Definition)

```yaml
version: 2

assets:
  - id: zshrc
    type: symlink                    # symlink | copy | template | secret
    source: assets/zsh/.zshrc        # relative to manifest.yaml
    target: ~/.zshrc                 # supports env var interpolation: ${HOME}
    permissions: "0644"
    owner: null                      # null = current user
    conflict_strategy: prompt        # prompt | overwrite | skip | abort
    encrypted: false
    template_vars: null              # map for template type

  - id: gitconfig
    type: copy
    source: assets/git/.gitconfig
    target: ~/.gitconfig
    permissions: "0644"
    conflict_strategy: overwrite

secrets:
  - id: ssh_key
    type: secret
    source: secrets/id_ed25519.age   # .age extension triggers pyrage/age
    target: ~/.ssh/id_ed25519
    permissions: "0600"
    owner: null

packages:
  brew:
    - git
    - curl
    - ripgrep
  apt:
    - git
    - curl
  flatpak: []
  snap: []
  docker:
    images: []
  node:
    version_file: .nvmrc             # or explicit version: "20.11.0"

profiles:
  base:
    assets:
      - zshrc
      - gitconfig
    secrets:
      - ssh_key
    packages:
      - brew
      - apt
  work:
    extends: [base]                  # simple list merge; DAG deferred
    assets:
      - id: work_ssh
        type: secret
        source: secrets/work_id_ed25519.age
        target: ~/.ssh/work_id_ed25519
        permissions: "0600"

machine_overrides:
  enabled: true
  path: machine/{hostname}.yaml      # merged after profile resolution
```

**Pydantic Rules**:
- Strict path validation (no traversal outside repo without explicit absolute target).
- Safe env var interpolation: `${VAR}` syntax only; fail on missing vars unless `default` provided.
- Future compatibility via `migrations/` stub (ignores unknown fields with warning, never crash).

---

## 6. Phase-by-Phase Build Plan (A to Z)

### Phase 0: State Engine & Foundations (Weeks 1–3)
**Goal**: The tool exists. The transaction model is proven. No CLI commands yet.

#### 6.0.1 Scaffold & Tooling
- Create `src/rv/` layout per module map.
- `pyproject.toml`: dependencies, scripts entrypoint, pytest/cov/ruff/mypy/bandit config.
- Pre-commit hooks (ruff, mypy, bandit).

#### 6.0.2 Pydantic Models (`models/`)
- `Manifest`, `Asset`, `Secret`, `PackageRef`, `Profile`, `MachineOverride`.
- `TransactionJournal`, `RollbackEntry`, `LockfileEntry`.
- Strict validation, exhaustive docstrings, mypy-compatible.

#### 6.0.3 Transaction Engine (`transactions/`)
- `TransactionContext`: plan, validate, snapshot, execute, verify, commit, cleanup.
- Atomic file operations: `atomic_write(path, content)` using temp + rename.
- Process lock: `ProcessLock` context manager using `fcntl` (Unix).
- Rollback journal JSON schema: `{tx_id, timestamp, entries: [{op, src_backup, target, checksum}]}`.

#### 6.0.4 Security Foundation (`security/`)
- `PermissionEnforcer`: apply `0600`/`0700`/`0644` with validation.
- `SecureTempFile`: context manager creating files with `O_TMPFILE` / `mkstemp` + immediate `chmod 600`.
- `SecretScrubber`: regex-based redaction for logs and exception traces.
- `ZeroBuffer`: explicit memory overwrite for decrypted plaintext buffers.

#### 6.0.5 Logging (`logging/`)
- `AuditLogger`: structured JSON lines to `~/.local/share/rv/audit.log`.
- `ConsoleLogger`: Rich-powered human output.
- `SecretScrubber` integrated into both.

#### 6.0.6 Utilities (`utils/`)
- `Platform`: detect OS, distro, package manager availability.
- `PathHelper`: canonicalization, cross-device detection, symlink loop detection.
- `Interpolator`: safe `${VAR}` env var substitution.

#### 6.0.7 Phase 0 Acceptance Criteria
- [ ] `pytest` suite passes with >90% coverage on `transactions/`, `security/`, `models/`.
- [ ] `mypy --strict` passes with zero errors.
- [ ] `bandit` passes with no medium/high severity issues.
- [ ] Atomic write proven with simulated power-loss test (rename atomicity).
- [ ] Process lock proven with concurrent process test (one blocks, one fails).

---

### Phase 1: Unidirectional Restore Engine (Weeks 4–6)
**Goal**: `rv restore <profile>` works end-to-end on a fresh machine. No sync. No push.

#### 6.1.1 CLI Scaffold (`cli/`)
- `rv init`: Scaffold repo, detect platform, write `manifest.yaml` template, write `.rvignore`.
- `rv restore <profile>`: Full 14-step deterministic apply order.
- `rv status`: Compare lockfile SHA-256 to current filesystem; report drift.
- `rv diff`: Show colored diff of drift (Rich).

#### 6.1.2 Core Services (`services/`)
- `RestoreService`: orchestrate the 14-step order using `TransactionContext`.
- `StatusService`: compute drift map `{asset_id: {expected, actual, changed}}`.
- `ManifestLoader`: load + interpolate + validate.
- `ProfileResolver`: merge `extends` lists sequentially (base → work → machine).

#### 6.1.3 Asset Handlers
- `SymlinkHandler`: create/update symlinks; detect loops; handle cross-device via copy fallback.
- `CopyHandler`: atomic copy with backup snapshot.
- `TemplateHandler`: Jinja2-like minimal templating (env vars only, no logic).
- `SecretHandler`: decrypt `.age` to secure temp, copy to target, enforce `0600`, zero buffer.

#### 6.1.4 Lockfile (`manifest.lock`)
- JSON map: `{asset_id: {sha256_of_source, target_path, permissions, mtime}}`.
- Updated only on successful commit.

#### 6.1.5 Phase 1 Acceptance Criteria
- [ ] `rv init` generates a valid, loadable manifest.
- [ ] `rv restore base` on a fresh VM creates all symlinks, copies files, decrypts secrets, applies permissions.
- [ ] `rv status` reports zero drift immediately after restore.
- [ ] Manual file modification causes `rv status` to report drift with correct diff.
- [ ] Interrupting restore mid-flight leaves rollback journal; manual journal replay restores previous state.
- [ ] All handlers have dedicated pytest suites with temp directories.

---

### Phase 2: Security & Encryption (Weeks 7–8)
**Goal**: Secrets are cryptographically sound and operationally safe.

#### 6.2.1 Encryption Integration (`security/`)
- `AgeEncryptor`: try `pyrage` first; catch `ImportError`/build failure and fallback to `age` CLI subprocess.
- `rv secret encrypt <path>`: Encrypt file in-place to `<path>.age`; update manifest if run inside repo.
- `rv secret decrypt <path.age>`: Decrypt to stdout or specified output (for debugging; scrubbed in logs).
- `rv secret rotate <id>`: Re-encrypt secret with new age keypair; atomically update source and lockfile.

#### 6.2.2 Secret Handling Audit
- [ ] No plaintext in logs (verified by regex scan of test log output).
- [ ] No plaintext in exception traces (custom exception formatter).
- [ ] No plaintext in temp dirs outside `SecureTempFile` context.
- [ ] No plaintext in process arguments (age CLI uses file paths only, never inline data).

#### 6.2.3 Legacy Migration Shim
- Read-only `git-crypt` detection: if repo has `.git-crypt` dir, print warning and skip; user must migrate manually.

#### 6.2.4 Phase 2 Acceptance Criteria
- [ ] `rv secret encrypt/decrypt` roundtrip verified with test key.
- [ ] `rv secret rotate` updates lockfile without invalidating other entries.
- [ ] Secret scrubber audit: grep all test artifacts for plaintext test secret → zero hits.
- [ ] Fallback to `age` CLI proven by uninstalling `pyrage` and running restore.

---

### Phase 3: Profiles & Package Orchestration (Weeks 9–11)
**Goal**: `rv restore` installs the full environment, not just files.

#### 6.3.1 Profile Inheritance (`core/`)
- Sequential list merge: `extends: [base, work]` merges base assets/secrets/packages, then work overlays.
- Conflict resolution: last-write-wins for same `id`; explicit `conflict_strategy` on assets.
- Machine overrides: auto-load `machine/{hostname}.yaml` after profile merge.

#### 6.3.2 Package Providers (`providers/`)
**Design Principle**: `rv` generates native tool input and executes native commands. No `shell=True`. No state caching.

| Provider | Mechanism | Dry-Run |
|----------|-----------|---------|
| `brew` | Generate `Brewfile` in temp dir; run `brew bundle --file={tmp}` | `--dry-run` prints Brewfile, skips execution |
| `apt` | Run `apt-get install -y {pkg_list}` if any missing | `--dry-run` runs `dpkg -l` check, prints would-install list |
| `flatpak` | Run `flatpak install -y {ref}` | `--dry-run` checks `flatpak list` |
| `snap` | Run `snap install {pkg}` | `--dry-run` checks `snap list` |
| `docker` | Run `docker pull {image}` or `docker compose -f {file} up -d` | `--dry-run` prints pull list |
| `node` | Verify `node -v` matches `.nvmrc` or explicit version; run `nvm install` or `fnm install` if available | `--dry-run` prints version mismatch |

- **Retry logic**: 3 attempts with exponential backoff (2s, 4s, 8s) on network errors.
- **Idempotency**: Rely on native tool idempotency. `rv` only verifies presence before calling.

#### 6.3.3 `rv doctor`
- Check manifest validity, lockfile consistency, missing packages, broken symlinks, wrong permissions.
- Emit structured report (JSON with `--json` flag).

#### 6.3.4 Phase 3 Acceptance Criteria
- [ ] `rv restore` with packages installs all listed packages on fresh Ubuntu and macOS VMs.
- [ ] `--dry-run` produces accurate preview without system mutation.
- [ ] Network failure simulation triggers retry logic; final failure aborts transaction and rolls back file changes.
- [ ] `rv doctor` detects intentionally broken symlink and wrong permission.

---

### Phase 4: Plugin System & AI Assets (Weeks 12–14)
**Goal**: Extensible, sandboxed architecture for user and first-party plugins.

#### 6.4.1 Plugin Loader (`plugins/`)
- Discovery: scan `plugins/` directory in repo and `~/.config/rv/plugins/`.
- `plugin.yaml` manifest: name, version, permissions (`network: bool`, `shell: bool`, `allowed_paths: list[str]`), entrypoint.

#### 6.4.2 Plugin Security Model
- **Untrusted by default**.
- Execution: isolated subprocess with `subprocess.run` (NO `shell=True`).
- Timeout: mandatory 30s default, overridable in `plugin.yaml` (max 300s).
- API: `ReviveContext` serialized as JSON env var + stdin; plugin writes JSON to stdout.
- Restrictions:
  - No network unless `network: true`.
  - No shell commands unless `shell: true`.
  - Filesystem access restricted to `allowed_paths` + transaction targets.

#### 6.4.3 Hook System
- `pre-restore`: runs after validation, before snapshot.
- `post-restore`: runs after verification, before lockfile update.
- Hooks execute in sandboxed subprocess; failure aborts transaction.

#### 6.4.4 AI Asset Plugins (First-Party)
- `mcp-config`: symlink/copy MCP server configs to target IDE directories.
- `claude-prompts`: deploy system prompts to Claude Code / Claude Desktop paths.
- `python-skills`: manage `skills/` directory mappings.
- These are thin wrappers over the asset system with known target paths per platform.

#### 6.4.5 Phase 4 Acceptance Criteria
- [ ] Malicious plugin attempting `shell=True` is blocked by loader (permission manifest mismatch).
- [ ] Plugin timeout proven: infinite-loop plugin killed at 30s; transaction rolls back.
- [ ] AI asset plugin restores MCP config to correct macOS and Linux paths.
- [ ] `rv restore` with `--no-plugins` flag skips plugin execution (escape hatch).

---

### Phase 5: Production Polish & Distribution (Weeks 15–17)
**Goal**: Distributable, resilient, observable tool.

#### 6.5.1 Daemon Mode (`watchers/`)
- `rv watch`: watchdog monitors repo for changes; triggers restore on change.
- Respects process lock; sleeps if lock held by another `rv` process.
- Configurable debounce (default 5s).

#### 6.5.2 Disaster Recovery
- `rv recover`: scan `~/.config/rv/journals/` for incomplete transactions; interactive replay or abort.
- `rv recover --auto`: non-interactive replay of latest incomplete journal (for CI/headless).
- Safe mode: if lockfile is corrupt, skip verification and warn; do not auto-mutate.

#### 6.5.3 CI / Headless Mode
- `rv ci`: structured JSON telemetry to stdout; no Rich styling.
- Exit codes: `0` (success), `1` (validation failure), `2` (transaction failure), `3` (partial/needs recover).

#### 6.5.4 Build & Distribution
- PyInstaller smoke test: verify `pyrage` and `watchdog` bundle correctly.
- GitHub Actions CI: pytest, mypy, ruff, bandit on Python 3.11/3.12/3.13.
- Release workflow: build PyInstaller binaries for `linux-x64`, `macos-x64`, `macos-arm64`.
- `pip install revive-cli` wheel as primary distribution; binary as convenience.

#### 6.5.5 Documentation
- `README.md`: quickstart, installation, manifest reference.
- `docs/architecture.md`: state engine, transaction model, security model.
- `docs/plugin-api.md`: `ReviveContext` schema, `plugin.yaml` reference.

#### 6.5.6 Phase 5 Acceptance Criteria
- [ ] `rv watch` detects file change and triggers restore within debounce window.
- [ ] `rv recover` successfully rolls back interrupted transaction from journal.
- [ ] PyInstaller binary runs `rv restore` on clean machine without Python installed.
- [ ] CI pipeline passes on all target Python versions.
- [ ] `bandit` + `ruff` + `mypy --strict` pass on entire codebase.

---

## 7. AI Agent Execution Rules (Mandatory)

The AI agent implementing this plan MUST adhere to the following non-negotiable constraints:

1. **Never skip tests**. Every module delivered must have accompanying pytest tests before moving to the next module.
2. **Never stub critical functionality**. Rollback logic, permission enforcement, and secret scrubbing must be fully implemented, never `pass` or `TODO`.
3. **Never use `shell=True`**. All subprocess calls must use explicit argument lists.
4. **Never bypass type validation**. All Pydantic models use `strict=True` where applicable; no silent exception suppression.
5. **Never mutate files outside transaction targets**. All filesystem changes go through `TransactionContext`.
6. **Write typed code only**. Every function has type hints; mypy strict mode must pass.
7. **Maintain >90% test coverage** for `core/`, `transactions/`, `security/`, `models/`, `services/`.
8. **Use Ruff + Black-compatible formatting**. No manual style debates.
9. **Write exhaustive docstrings** for all public APIs (Google style).
10. **No placeholder logic in Phases 0–2**. These phases are the foundation; they must be production-grade before Phase 3 begins.

---

## 8. Definition of Done (v1.0)

- [ ] `rv init`, `rv restore`, `rv status`, `rv diff`, `rv doctor`, `rv recover`, `rv watch`, `rv secret encrypt/decrypt/rotate` are implemented and tested.
- [ ] Transaction engine guarantees atomicity and rollback on all mutating commands.
- [ ] Secrets never appear in logs, diffs, traces, or temp dirs outside secure contexts.
- [ ] Package providers orchestrate `brew`, `apt`, `flatpak`, `snap`, `docker`, `node` with `--dry-run` support.
- [ ] Plugin system loads sandboxed subprocesses with timeout and permission enforcement.
- [ ] PyInstaller binary builds and runs on Linux x64, macOS x64, and macOS ARM64.
- [ ] CI passes: pytest (>90% core coverage), mypy strict, ruff, bandit.
- [ ] Documentation covers quickstart, manifest schema, security model, and plugin API.

---

## 9. Post-1.0 Backlog (Deferred)

- Bidirectional sync (`rv sync push` / `rv sync pull`) with merge engine.
- Full DAG profile inheritance with cycle detection.
- Schema migrations engine (when v3 is introduced).
- Windows platform support with NTFS permission mapping.
- Remote orchestration hooks (SSH-based multi-machine restore).
- Community plugin registry and signed plugin verification.