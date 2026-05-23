# Revive (rv) Improvement & Implementation Plan
## Version: 1.0 | Date: 2026-05-23

---

## Executive Summary

This plan outlines a phased approach to address the 14 identified issues and improvements
for the Revive developer environment lifecycle manager. Tasks are organized by priority,
estimated effort, dependencies, and acceptance criteria.

---

## Phase 1: Security & Stability (Weeks 1-3)

### Task 1.1: Harden Plugin Sandbox
**Priority:** CRITICAL  
**Effort:** 3-4 days  
**Owner:** Security/Core Team  
**Files:** `src/rv/plugins/sandbox_wrapper.py`, `src/rv/plugins/sandbox.py`

**Description:**
The current in-process patch sandbox is vulnerable to escape via ctypes, importlib,
and gc tricks. Implement defense-in-depth layers.

**Implementation Steps:**
1. Add import-time module blocking for ctypes, cffi, importlib, imp, gc
2. Patch __import__ to enforce allowlist
3. Add resource limits (RLIMIT_AS, RLIMIT_CPU) via ctypes (before blocking it)
4. Implement seccomp-bpf filter for Linux (optional, advanced)
5. Add Docker sandbox mode as opt-in for untrusted plugins
6. Update plugin.yaml schema to include `sandbox_mode: process | docker`

**Acceptance Criteria:**
- [x] Malicious plugin using `import ctypes` is blocked
- [x] Malicious plugin using `import importlib` is blocked
- [x] Plugin attempting `os._exit()` is caught and logged
- [x] All existing tests pass with hardened sandbox
- [x] New security tests added to `tests/test_plugins_sandbox.py`

---

### Task 1.2: Native Filesystem Watchers
**Priority:** HIGH  
**Effort:** 2-3 days  
**Owner:** Core Team  
**Files:** `src/rv/watchers/daemon.py`, `pyproject.toml`

**Description:**
Replace polling-based file watching with native OS APIs via the `watchdog` library.

**Implementation Steps:**
1. Add `watchdog>=3.0` to `pyproject.toml` dependencies
2. Refactor `src/rv/watchers/daemon.py`:
   - Use `watchdog.observers.Observer` with platform-specific observer
   - Implement `FileSystemEventHandler` for change detection
   - Maintain debounce logic but trigger on actual events
3. Handle `.git/` ignore at observer level (exclude pattern)
4. Add graceful shutdown on SIGINT/SIGTERM
5. Update tests in `tests/test_watchers.py`

**Acceptance Criteria:**
- [x] CPU usage during watch mode < 1% (was ~5-10% with polling)
- [x] Changes detected within 100ms on local filesystems
- [x] `.git/` directory changes are silently ignored
- [x] Ctrl+C stops cleanly without leaving zombie processes
- [x] Works on Linux (inotify), macOS (fsevents), Windows (ReadDirectoryChangesW)

---

### Task 1.3: Backup Snapshot Pruning
**Priority:** HIGH  
**Effort:** 1-2 days  
**Owner:** Core Team  
**Files:** `src/rv/services/recovery.py`, `src/rv/transactions/context.py`, `src/rv/cli/main.py`

**Description:**
Implement automatic cleanup of old transaction backups to prevent disk bloat.

**Implementation Steps:**
1. Add `backup_retention` section to `manifest.yaml` schema:
   ```yaml
   backup_retention:
     max_count: 10          # Keep last N backups
     max_age_days: 30       # Delete backups older than N days
   ```
2. Implement `BackupPruner` class in `src/rv/services/recovery.py`
3. Hook pruning into `TransactionContext.commit()` (post-cleanup)
4. Add `rv prune` CLI command for manual cleanup
5. Add `--prune` flag to `rv restore` for on-demand pruning

**Acceptance Criteria:**
- [x] Backups older than `max_age_days` are auto-deleted after successful restore
- [x] Backup count never exceeds `max_count` (FIFO eviction)
- [x] `rv prune --dry-run` shows what would be deleted
- [x] `rv prune` interactively confirms deletions
- [x] Active/incomplete transaction journals are never pruned

---

## Phase 2: Platform & Provider Expansion (Weeks 3-5)

### Task 2.1: Add Pacman Provider (Arch Linux)
**Priority:** HIGH  
**Effort:** 1-2 days  
**Owner:** Platform Team  
**Files:** `src/rv/providers/pacman.py`, `src/rv/services/restore.py`, `src/rv/services/doctor.py`

**Description:**
Add support for Arch Linux pacman package manager.

**Implementation Steps:**
1. Create `src/rv/providers/pacman.py` extending `BaseProvider`
2. Implement `is_available()` (check for `pacman` binary)
3. Implement `install()` using `pacman -S --noconfirm <pkgs>`
4. Implement `is_installed(pkg)` for idempotency (check `pacman -Q <pkg>`)
5. Register in `RestoreService.restore()` and `DoctorService`
6. Add tests in `tests/test_providers.py`

**Acceptance Criteria:**
- [x] `pacman` provider installs packages on Arch/Manjaro
- [x] Already-installed packages are skipped (idempotent)
- [x] `rv doctor` detects pacman availability
- [x] Error handling for missing packages (returns clear message)

---

### Task 2.2: Add DNF Provider (Fedora/RHEL)
**Priority:** MEDIUM  
**Effort:** 1-2 days  
**Owner:** Platform Team  
**Files:** `src/rv/providers/dnf.py`, `src/rv/services/restore.py`, `src/rv/services/doctor.py`

**Description:**
Add support for Fedora/RHEL dnf package manager.

**Implementation Steps:**
1. Create `src/rv/providers/dnf.py` extending `BaseProvider`
2. Implement `is_available()` (check for `dnf` binary)
3. Implement `install()` using `dnf install -y <pkgs>`
4. Implement `is_installed(pkg)` (check `dnf list installed <pkg>`)
5. Register in services and doctor
6. Add tests

**Acceptance Criteria:**
- [x] `dnf` provider works on Fedora 40+, RHEL 9+
- [x] Idempotent installs
- [x] Proper error messages for unavailable packages

---

### Task 2.3: Add Nix Provider
**Priority:** MEDIUM  
**Effort:** 2-3 days  
**Owner:** Platform Team  
**Files:** `src/rv/providers/nix.py`, `src/rv/services/restore.py`, `src/rv/services/doctor.py`

**Description:**
Add support for Nix package manager (nix-env / nix profile).

**Implementation Steps:**
1. Create `src/rv/providers/nix.py`
2. Support both `nix-env -iA nixpkgs.<pkg>` and `nix profile install`
3. Detect Nix installation (check for `nix` binary)
4. Handle Nix store paths and garbage collection considerations
5. Register in services and doctor

**Acceptance Criteria:**
- [x] `nix` provider installs packages via `nix-env`
- [x] Works on NixOS and nix-on-other-distro setups
- [x] Idempotent installs (nix handles this naturally)
- [x] `rv doctor` detects nix availability

---

### Task 2.4: Add Cargo & Pip Providers
**Priority:** LOW  
**Effort:** 2 days  
**Owner:** Platform Team  
**Files:** `src/rv/providers/cargo.py`, `src/rv/providers/pip.py`

**Description:**
Add language-specific package managers for developer tools.

**Implementation Steps:**
1. Create `src/rv/providers/cargo.py` (uses `cargo install`)
2. Create `src/rv/providers/pip.py` (uses `pip install --user`)
3. Both should support `is_installed()` checks
4. Register in services and doctor

**Acceptance Criteria:**
- [x] `cargo install ripgrep` works via manifest
- [x] `pip install black` works via manifest
- [x] Both are idempotent

---

## Phase 3: Performance & UX (Weeks 5-7)

### Task 3.1: Package Idempotency Cache
**Priority:** HIGH  
**Effort:** 2-3 days  
**Owner:** Core Team  
**Files:** `src/rv/providers/base.py`, `src/rv/services/restore.py`, `src/rv/models/workspace.py`

**Description:**
Cache installed package state to skip redundant installs and speed up restores.

**Implementation Steps:**
1. Extend `BaseProvider` with abstract `is_installed(pkg) -> bool`
2. Create `PackageCache` class in `src/rv/services/restore.py`:
   - Cache file: `~/.config/rv/package-cache.json`
   - TTL: 24 hours
   - Structure: `{provider: [packages], last_updated: ISO8601}`
3. In `RestoreService`, check cache before calling `provider.install()`
4. Add `--force-packages` flag to `rv restore` to bypass cache
5. Invalidate cache on `--force-packages` or provider errors

**Acceptance Criteria:**
- [x] Second `rv restore base` skips all already-installed packages
- [x] Cache is refreshed every 24 hours or on `--force-packages`
- [x] Cache is provider-specific (brew cache separate from apt cache)
- [x] `rv doctor` can show cache state
- [x] Cache handles package removals (detects drift)

---

### Task 3.2: Parallel Asset Processing
**Priority:** MEDIUM  
**Effort:** 2-3 days  
**Owner:** Core Team  
**Files:** `src/rv/services/handlers.py`, `src/rv/services/restore.py`

**Description:**
Process independent assets in parallel to reduce restore time.

**Implementation Steps:**
1. Analyze asset dependency graph (assets are mostly independent)
2. Use `asyncio` or `concurrent.futures.ThreadPoolExecutor` for I/O-bound ops
3. Keep package installs sequential (order matters)
4. Maintain transaction atomicity (collect all results before commit)
5. Add `--parallel` flag to `rv restore` (default: enabled)
6. Add `--sequential` flag to disable parallelization for debugging

**Acceptance Criteria:**
- [ ] 10+ asset restore completes in < 20% of sequential time
- [ ] Failed asset still triggers full rollback
- [ ] Order of operations is deterministic (sorted by asset ID)
- [ ] `--sequential` flag forces single-threaded execution
- [ ] No race conditions on shared targets

---

### Task 3.3: Template Context Enhancement
**Priority:** MEDIUM  
**Effort:** 1-2 days  
**Owner:** Core Team  
**Files:** `src/rv/services/handlers.py`, `src/rv/models/manifest.py`

**Description:**
Auto-inject built-in context variables into Jinja2 templates.

**Implementation Steps:**
1. In `AssetHandler._handle_template()`, build context dict:
   ```python
   builtin_context = {
       "_hostname": socket.gethostname(),
       "_user": getpass.getuser(),
       "_platform": sys.platform,  # linux, darwin, win32
       "_arch": platform.machine(),  # x86_64, arm64
       "_home": str(Path.home()),
       "_repo_dir": repo_dir,
   }
   ```
2. Merge with user-defined `template_vars` (user vars take precedence)
3. Update manifest schema docs
4. Add tests for builtin variable injection

**Acceptance Criteria:**
- [x] `{{ _hostname }}` renders correctly in templates
- [x] `{{ _platform }}` returns linux/darwin/win32
- [x] User-defined vars override builtins (no collision issues)
- [x] Backward compatible with existing templates

---

## Phase 4: Security & Secrets (Weeks 7-8)

### Task 4.1: Secret Rotation Without Old Identity
**Priority:** HIGH  
**Effort:** 2 days  
**Owner:** Security Team  
**Files:** `src/rv/cli/main.py` (secret subcommands), `src/rv/security/encryptor.py`

**Description:**
Support re-encrypting secrets from plaintext when old identity is unavailable.

**Implementation Steps:**
1. Add `rv secret rotate --from-plaintext <file>` command
2. Flow: plaintext -> encrypt with new recipient -> overwrite .age file
3. Add warning: "This will delete the plaintext source after encryption"
4. Require `--confirm` flag for safety
5. Update docs and help text

**Acceptance Criteria:**
- [ ] `rv secret rotate --from-plaintext ~/.aws/credentials --recipient age1new...` works
- [ ] Requires explicit `--confirm` flag
- [ ] Plaintext file is securely wiped after encryption (shred-like)
- [ ] Old .age file is backed up before overwrite

---

### Task 4.2: GUI Authentication
**Priority:** MEDIUM  
**Effort:** 1-2 days  
**Owner:** Security/Frontend Team  
**Files:** `src/rv/gui/server.py`

**Description:**
Add basic authentication to the web GUI to prevent unauthorized access.

**Implementation Steps:**
1. Add `--auth-token` flag to `rv gui`
2. If no token provided, auto-generate and print to console
3. Implement middleware to check `?token=` query param or `X-Auth-Token` header
4. Return 401 for unauthenticated requests
5. Store token in memory only (never disk)

**Acceptance Criteria:**
- [x] `rv gui --auth-token mytoken123` requires token for access
- [x] Auto-generated token is printed on startup
- [x] All API endpoints require authentication
- [x] Token is not persisted to disk

---

### Task 4.3: ZeroBuffer Compiler Optimization Resistance
**Priority:** LOW  
**Effort:** 1 day  
**Owner:** Security Team  
**Files:** `src/rv/security/zerobuffer.py`

**Description:**
Ensure `ctypes.memset` zeroing is not optimized away by aggressive compilers.

**Implementation Steps:**
1. Add `ctypes.pythonapi.PyMem_RawFree` with explicit overwrite
2. Use `volatile` pointer semantics via `ctypes.c_void_p`
3. Add `sys.intern()` or `gc.collect()` barrier to prevent reordering
4. Add unit test verifying buffer is zeroed (inspect memory)

**Acceptance Criteria:**
- [x] Buffer contents are verifiably zeroed after release
- [x] Test passes even with `-O3` / aggressive optimization
- [x] No performance regression

---

## Phase 5: CLI & Workflow (Weeks 8-9)

### Task 5.1: Profile Delta Preview
**Priority:** MEDIUM  
**Effort:** 2 days  
**Owner:** Core Team  
**Files:** `src/rv/services/restore.py`, `src/rv/cli/main.py`

**Description:**
Show what changes from the current live system state, not from empty.

**Implementation Steps:**
1. Add `rv restore <profile> --preview` flag
2. Compute delta: current live state vs proposed new state
3. Show: added, modified, removed, unchanged assets
4. Show package install delta (what would actually change)
5. Reuse existing diff logic where possible

**Acceptance Criteria:**
- [x] `--preview` shows only assets that would change
- [x] Unchanged assets are listed as "skipped"
- [x] Works with `--dry-run`
- [x] Color-coded output (green=add, yellow=modify, red=remove)

---

### Task 5.2: Workspace Sync Command
**Priority:** LOW  
**Effort:** 1-2 days  
**Owner:** Core Team  
**Files:** `src/rv/services/workspace.py`, `src/rv/cli/main.py`

**Description:**
One-command sync across all registered workspaces.

**Implementation Steps:**
1. Add `rv workspace sync` command
2. Iterate over `~/.config/rv/workspaces.yaml`
3. For each workspace: `git pull` then `rv restore <default_profile>`
4. Add `--profile` override to sync specific profiles
5. Add `--dry-run` to preview across all workspaces

**Acceptance Criteria:**
- [ ] `rv workspace sync` updates and restores all workspaces
- [ ] Failed workspace is reported but doesn't block others
- [ ] Summary report at end: X succeeded, Y failed
- [ ] `--dry-run` shows what would happen per workspace

---

### Task 5.3: Per-Asset Hooks
**Priority:** LOW  
**Effort:** 2-3 days  
**Owner:** Core Team  
**Files:** `src/rv/models/manifest.py`, `src/rv/services/handlers.py`

**Description:**
Allow pre/post hooks on individual assets for fine-grained control.

**Implementation Steps:**
1. Extend Asset model with `hooks` field:
   ```yaml
   assets:
     - id: ssh_config
       type: copy
       hooks:
         pre:
           - plugin: ensure-ssh-dir
         post:
           - command: "chmod 600 ~/.ssh/config"
   ```
2. Implement hook execution in `AssetHandler`
3. Support both plugin references and inline commands
4. Inherit sandbox permissions from parent profile

**Acceptance Criteria:**
- [ ] Pre-hook runs before asset mutation
- [ ] Post-hook runs after successful asset write
- [ ] Failed hook triggers transaction rollback
- [ ] Works with all asset types (symlink, copy, template)

---

## Phase 6: Testing & Quality (Weeks 9-10)

### Task 6.1: Docker Integration Tests
**Priority:** HIGH  
**Effort:** 3-4 days  
**Owner:** QA Team  
**Files:** `tests/integration/`, `.github/workflows/ci.yml`

**Description:**
Run full restore/backup/rollback cycles in clean Docker containers.

**Implementation Steps:**
1. Create `tests/integration/Dockerfile.ubuntu`, `Dockerfile.alpine`, `Dockerfile.arch`
2. Write `test_full_lifecycle.py`:
   - Init repo -> Add assets -> Restore -> Verify -> Backup -> Modify -> Restore -> Rollback -> Verify
3. Add GitHub Actions matrix: ubuntu-latest, alpine, archlinux
4. Run on every PR and nightly
5. Test package providers in their native environments

**Acceptance Criteria:**
- [ ] CI passes on Ubuntu, Alpine, Arch Linux
- [ ] Full lifecycle test covers all asset types
- [ ] Rollback test verifies exact pre-restore state
- [ ] Secret encryption/decryption tested end-to-end
- [ ] Package installation tested for available providers

---

### Task 6.2: Target Array Resolution Tests
**Priority:** MEDIUM  
**Effort:** 1-2 days  
**Owner:** QA Team  
**Files:** `tests/test_handlers.py`

**Description:**
Add comprehensive tests for the sub-item resolution logic to prevent ambiguity bugs.

**Implementation Steps:**
1. Test case: directory source with matching basenames
2. Test case: directory source with missing matches (should error)
3. Test case: directory source with extra files (should ignore extras)
4. Test case: nested directory structures
5. Test case: symlink targets in arrays

**Acceptance Criteria:**
- [x] All resolution scenarios have explicit test coverage
- [x] Mismatches produce clear error messages
- [x] Edge cases (empty dir, single file) handled

---

### Task 6.3: Manifest Lockfile Checksums for Rendered Templates
**Priority:** LOW  
**Effort:** 1-2 days  
**Owner:** Core Team  
**Files:** `src/rv/models/transaction.py`, `src/rv/services/restore.py`

**Description:**
Include checksums of rendered template output in `manifest.lock`.

**Implementation Steps:**
1. After template rendering, compute SHA256 of rendered content
2. Store in `manifest.lock` under `rendered_checksums`
3. On `rv status`, compare rendered checksum against live file
4. Detect template engine version changes

**Acceptance Criteria:**
- [x] `manifest.lock` contains `rendered_checksums` for templates
- [x] Template engine update is detected as drift
- [x] Backward compatible with old lockfiles

---

## Appendix A: Dependency Graph

```
Task 1.1 (Sandbox)     ─┐
Task 1.2 (Watchers)    ─┤
Task 1.3 (Pruning)     ─┤
                        │
Task 2.1 (Pacman)      ─┤
Task 2.2 (DNF)         ─┤──> Phase 2 (independent of Phase 1)
Task 2.3 (Nix)         ─┤
Task 2.4 (Cargo/Pip)   ─┘

Task 3.1 (Idempotency) ─┐
Task 3.2 (Parallel)    ─┤──> Phase 3 (depends on Phase 2 providers)
Task 3.3 (Templates)   ─┘

Task 4.1 (Rotation)    ─┐
Task 4.2 (GUI Auth)    ─┤──> Phase 4 (independent)
Task 4.3 (ZeroBuffer)  ─┘

Task 5.1 (Preview)     ─┐
Task 5.2 (Sync)        ─┤──> Phase 5 (depends on Phase 3)
Task 5.3 (Hooks)       ─┘

Task 6.1 (Docker CI)   ─┐
Task 6.2 (Array Tests) ─┤──> Phase 6 (depends on all prior phases)
Task 6.3 (Lockfile)    ─┘
```

---

## Appendix B: Risk Register

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| Plugin sandbox hardening breaks legitimate plugins | Medium | High | Extensive test suite, gradual rollout, opt-in flags |
| Parallel asset processing introduces race conditions | Medium | High | Strict ordering, mutex on shared targets, thorough testing |
| Native watchers fail on network filesystems | High | Low | Fallback to polling mode, clear documentation |
| Nix provider complexity exceeds scope | Low | Medium | Start with `nix-env`, defer `nix profile` and flakes |
| Package cache becomes stale | Medium | Medium | TTL-based invalidation, `--force-packages` escape hatch |
| GUI auth breaks existing workflows | Low | Medium | Auto-generated tokens, `--no-auth` for local dev (dangerous) |

---

## Appendix C: Definition of Done

For each task to be considered complete:
1. Implementation code merged to `main`
2. Unit tests with >90% coverage for modified modules
3. Integration tests pass in Docker CI
4. Documentation updated (README, CLI help, docstrings)
5. `CHANGELOG.md` entry added
6. Code review approved by 2 maintainers
7. `ruff format`, `ruff check`, `mypy --strict`, `bandit -r` all pass
8. Manual QA sign-off for user-facing features

---

## Appendix D: Estimated Timeline

| Phase | Duration | Cumulative |
|-------|----------|------------|
| Phase 1: Security & Stability | 3 weeks | Week 3 |
| Phase 2: Platform Expansion | 2 weeks | Week 5 |
| Phase 3: Performance & UX | 2 weeks | Week 7 |
| Phase 4: Security & Secrets | 1 week | Week 8 |
| Phase 5: CLI & Workflow | 1 week | Week 9 |
| Phase 6: Testing & Quality | 1 week | Week 10 |
| **Buffer / Polish** | 1 week | Week 11 |
| **Total** | **11 weeks** | |

---

*End of Plan*
