# Revive (`rv`) — Regression & Production Readiness Analysis

**Date:** 2026-05-25  
**Analyst:** Antigravity (AI Agent)  
**Baseline:** IMPROVEMENTS_PLAN.md v1.0 + progress.md (100% complete claim)  
**Test Suite State:** 164/164 passing · mypy strict ✅ · ruff ✅ · bandit -ll ✅

---

## Executive Summary

All 15 planned improvement tasks are implemented and test-green. However, a deep regression
across implementation, test coverage, security posture, and definition-of-done criteria
reveals **17 actionable gaps** across 5 severity tiers.  The codebase is production-quality
in its core paths but has specific hardening, coverage, and completeness gaps that must be
addressed before a 1.0 release tag.

---

## 1. Baseline Quality Metrics

| Check | Result | Notes |
|-------|--------|-------|
| `pytest` | ✅ 164/164 passed | All unit + integration |
| `mypy --strict` | ✅ 0 errors | 56 source files |
| `ruff check` | ✅ 0 errors | |
| `ruff format --check` | ✅ 0 reformats | |
| `bandit -r src/rv -ll` | ✅ 0 High, 1 Medium | 75 Low (noqa-suppressed) |
| **Overall coverage** | ⚠️ **67%** | Below 90% requirement |
| Core modules coverage | ⚠️ Varies (see §3) | `recovery.py` at 39% |

---

## 2. Critical Gaps (Must Fix Before 1.0)

### GAP-001 · Dead Unreachable Method in `RestoreService`
**Severity:** CRITICAL — violates §6 rule 6 (no placeholder logic in production code)  
**File:** [`src/rv/services/restore.py`](src/rv/services/restore.py) — lines 254-281

`_plan_asset_parallel()` is a declared class method that immediately raises
`NotImplementedError  # pragma: no cover`. It is never called — `_plan_one_asset()` is used
instead. The dead method adds confusion, and the comment "Unused — implemented inline"
is itself a code smell that would fail code review.

**Fix:** Remove `_plan_asset_parallel()` entirely. It is dead code and its docstring
wrongly implies it has a distinct purpose.

---

### GAP-002 · CORS Wildcard on GUI API Endangers Token Authentication
**Severity:** CRITICAL — security regression  
**File:** [`src/rv/gui/server.py`](src/rv/gui/server.py) — lines 79, 91

`Access-Control-Allow-Origin: *` is set on every API response including authenticated
endpoints. With token-based authentication, a wildcard CORS policy means any malicious
website loaded in the same browser can call the GUI API with the token extracted from
local storage or a bookmarked URL. This defeats the purpose of `_AUTH_TOKEN`.

**Fix:** Restrict CORS to `http://127.0.0.1:<port>` and `http://localhost:<port>` when
the server is bound to loopback. Only open to `*` if explicitly started with
`--cors-wildcard` for development purposes.

---

### GAP-003 · Backup Retention Pruning NOT Auto-Triggered Post-Commit
**Severity:** HIGH — functional gap vs. acceptance criteria  
**Files:** [`src/rv/transactions/context.py`](src/rv/transactions/context.py),
[`src/rv/services/recovery.py`](src/rv/services/recovery.py)

The IMPROVEMENTS_PLAN (Task 1.3) acceptance criterion states:
> "Backups older than `max_age_days` are **auto-deleted after successful restore**"

The `TransactionContext.cleanup()` method (Step 7) does NOT call `BackupPruner.prune()`.
Pruning only occurs when:
- `rv restore --prune` is explicitly passed by the user, OR
- the user manually runs `rv prune`

This means disk bloat occurs silently unless the user remembers to pass `--prune`.
The `backup_retention` manifest config is never automatically consulted.

**Fix:** In `TransactionContext.cleanup()` (or in `RestoreService` after `tx_context.cleanup()`),
call `BackupPruner.prune(max_count, max_age_days)` using the manifest's retention config.
The manifest must be passed through, or `BackupPruner` must read it from the repo_dir.

---

### GAP-004 · Per-Asset Plugin Hooks Are Silently Dropped (Accepted SPEC Gap)
**Severity:** HIGH — feature incomplete as documented  
**File:** [`src/rv/services/handlers.py`](src/rv/services/handlers.py) — lines 215-227

`AssetHookPlugin` entries in per-asset `hooks.pre` / `hooks.post` are silently skipped
with a `logger.warning()`. The models accept them, the manifest schema documents them,
but execution is deferred to "profile-level hooks." Users who configure:

```yaml
assets:
  - id: ssh_config
    hooks:
      post:
        - plugin: ensure-ssh-dir
```

will get a warning log they may never see, and the hook will silently not execute.
IMPROVEMENTS_PLAN §5.3 acceptance criterion: "Failed hook triggers transaction rollback"
— this criterion passes only for command hooks, not plugin hooks.

**Fix:** Implement full per-asset plugin hook execution inside `_run_asset_hooks()`.
The `repo_dir` must be threaded through as a parameter. Alternatively, explicitly
`raise AssetHandlerError` for plugin hooks until they are fully implemented, rather
than silently dropping them.

---

### GAP-005 · Overall Test Coverage at 67% — Below 90% Mandate
**Severity:** HIGH — violates §6.1 rule 7 (>90% core coverage)  
**Files:** Multiple (see §3)

The AGENTS.md and IMPROVEMENTS_PLAN §Appendix C mandate >90% for
`services/`, `transactions/`, `security/`. Current state:

| Module | Coverage |
|--------|----------|
| `services/recovery.py` | **39%** |
| `services/backup.py` | **76%** |
| `services/restore.py` | **73%** |
| `transactions/context.py` | **74%** |
| `security/encryptor.py` | **59%** |
| `security/zerobuffer.py` | **57%** |
| `cli/main.py` | **50%** |
| `gui/server.py` | **36%** |

---

## 3. Coverage Gaps by Module

### 3.1 `services/recovery.py` — 39% Coverage

**Missing test scenarios:**
- `RecoveryService.rollback_journal()` — the journal file deletion and backup dir deletion paths
- `RecoveryService.discard_journal()` — backup dir removal
- `BackupPruner.list_backup_dirs()` — directory iteration with `OSError` on `os.listdir`
- `BackupPruner.prune()` — the age-based pruning path, the count-based pruning path,
  actual `shutil.rmtree` deletion (not just dry-run), and the OSError failure on deletion

**Required tests:** `tests/test_recovery.py` needs ~8 new scenarios exercising rollback
journal cleanup, full prune execution (not just dry-run), and OSError failure handling.

### 3.2 `transactions/context.py` — 74% Coverage

**Missing test scenarios:**
- `snapshot()` when the target is a symlink (SYMLINK: backup format)
- `snapshot()` when the target is a directory (copytree path)
- `rollback()` for the symlink restoration branch (`content.startswith("SYMLINK:")`)
- `rollback()` for the directory restoration branch
- `execute()` → atomic directory copy path (`os.path.isdir(source_data)`)
- `_write_journal()` OSError silencing on journal write failure

### 3.3 `security/encryptor.py` — 59% Coverage

**Missing test scenarios:**
- The CLI fallback path (`age` binary subprocess) for both encrypt and decrypt
- `get_public_key()` via the `age-keygen` subprocess path
- Error paths for invalid identity files
- `is_pyrage_available()` when pyrage is missing

### 3.4 `security/zerobuffer.py` — 57% Coverage

**Missing test scenarios:**
- `zero_bytes()` execution path (only the error path is tested)
- Verifying memory contents are actually zeroed after `zero()` call
- `zero()` on a `memoryview` (only `bytearray` is tested)
- The read-barrier assertion path (`_barrier = buf[0]`)

### 3.5 `cli/main.py` — 50% Coverage

**Missing test scenarios (high priority):**
- `rv watch` command
- `rv workspace sync` with actual git pull + restore
- `rv recovery` interactive prompt paths (rollback/discard/skip)
- `rv self-uninstall` paths
- `rv secret rotate` (standard mode with identity)
- `rv secret keygen` (full path)
- `rv prune` (actual deletion path with `--yes`)

### 3.6 `gui/server.py` — 36% Coverage

**Missing test scenarios:**
- POST `/api/restore` endpoint
- POST `/api/backup` endpoint
- POST `/api/manifest` save endpoint
- GET `/api/status` endpoint
- GET `/api/logs` endpoint
- WebSocket or SSE streaming endpoints (if any)
- 401 response for unauthenticated POST requests

---

## 4. Security Hardening Gaps

### GAP-006 · CORS Wildcard (See GAP-002 above)

### GAP-007 · Sandbox Docker Mode / seccomp-bpf Not Implemented
**Severity:** MEDIUM — planned but not delivered  
**File:** IMPROVEMENTS_PLAN.md Task 1.1, step 4 & 5

The improvement plan explicitly lists:
> 4. Implement seccomp-bpf filter for Linux (optional, advanced)
> 5. Add Docker sandbox mode as opt-in for untrusted plugins
> 6. Update plugin.yaml schema to include `sandbox_mode: process | docker`

None of these are implemented. The `plugin.yaml` schema has no `sandbox_mode` field.
This is fine for 1.0 if explicitly deferred, but must be documented as a known gap.

### GAP-008 · `zero_bytes()` Uses Fragile CPython Memory Layout Hack
**Severity:** MEDIUM — correctness risk across Python versions  
**File:** [`src/rv/security/zerobuffer.py`](src/rv/security/zerobuffer.py) — lines 77-85

The comment says: "the bytes data starts at offset 33 (Python 3.11+)". This is:
1. Undocumented CPython internals
2. Different across Python versions (3.11, 3.12, 3.13)
3. The offset calculation `id(data) + sys.getsizeof(b"") - effective_length` is
   mathematically incorrect — `getsizeof(b"")` returns the empty-bytes overhead size,
   not a fixed offset to the internal buffer of `data` (which depends on `len(data)`)

The code silently `pass`es all failures, which is the right fallback strategy, but the
approach is more likely to corrupt adjacent memory or silently do nothing than to
actually zero the intended bytes object. The implementation should be either corrected
or removed and replaced with a documented "best-effort only" note in the docstring.

### GAP-009 · GUI `start_gui_server` Binds to `0.0.0.0` if User Passes Non-loopback Host
**Severity:** MEDIUM — security config gap  
**File:** [`src/rv/gui/server.py`](src/rv/gui/server.py)

The CLI default for `--host` is `127.0.0.1` (safe), but users can override it to bind
on all interfaces. When binding to `0.0.0.0`, the wildcard CORS (GAP-002) becomes a
remote exploit surface — anyone on the local network can interact with the GUI API.
There is no warning when the user passes a non-loopback host.

**Fix:** Print a bold warning if `host != "127.0.0.1"` and `host != "::1"` and `host != "localhost"`.

---

## 5. Implementation Quality Gaps

### GAP-010 · `_plan_asset_parallel` Dead Code (See GAP-001 above)

### GAP-011 · Workspace Sync Doesn't Respect `--force-packages` / `--no-plugins`
**Severity:** LOW — missing feature completeness  
**File:** [`src/rv/cli/main.py`](src/rv/cli/main.py) — workspace_sync command

`workspace_sync` calls `RestoreService.restore()` but does not expose `force_packages`
or `no_plugins` options. Users cannot pass these flags during bulk workspace sync,
limiting operational flexibility for CI/CD use cases.

### GAP-012 · `rv workspace sync --dry-run` Does Not Validate Profiles Exist
**Severity:** LOW — UX gap  
**File:** [`src/rv/cli/main.py`](src/rv/cli/main.py) — lines 1960-1981

In dry-run mode, `rv workspace sync --dry-run` outputs `"skip"` for both git pull and
restore, but does not verify that a default profile could actually be resolved. A workspace
with a broken manifest.yaml or no profiles defined reports `"no profile"` as a failure
in dry-run mode but doesn't report a validation error clearly.

### GAP-013 · Missing `CHANGELOG.md`
**Severity:** LOW — violates Definition of Done (§Appendix C, criterion 5)  

The IMPROVEMENTS_PLAN explicitly states "CHANGELOG.md entry added" as one of the 8
definition-of-done criteria. No CHANGELOG.md exists in the repository root. All 15 tasks
were delivered without a changelog.

### GAP-014 · Missing `SECURITY.md`
**Severity:** LOW — professional production practice  

There is no `SECURITY.md` documenting the security model, vulnerability disclosure
policy, or known limitations (e.g., `zero_bytes()` best-effort behavior, sandbox
limitations, network-bound plugins). This is critical for a tool that manages secrets
and executes arbitrary plugins.

### GAP-015 · `BackupService` Override YAML Re-imported Locally
**Severity:** LOW — code quality  
**File:** [`src/rv/services/backup.py`](src/rv/services/backup.py) — lines 90-93

```python
import yaml
from rv.models.manifest import Asset, Secret
```

These imports appear inside a method body. `Asset` and `Secret` are already imported
at the top of the file (line 9). The inner `from rv.models.manifest import Asset, Secret`
is a redundant re-import that was not caught by `ruff` because `F401` is suppressed.

### GAP-016 · `pyproject.toml` Missing `pytest-timeout` Dependency
**Severity:** LOW — test reliability risk  

There is no timeout configuration in `[tool.pytest.ini_options]`. The `addopts` only has
`--strict-markers`. Per-asset hooks run `subprocess.run(..., timeout=30)` but the test
suite itself has no global timeout. A hanging test (e.g., due to a blocked subprocess
in a provider) will cause CI to time out at the runner level rather than reporting a
clean test failure.

**Fix:** Add `pytest-timeout` to `[project.optional-dependencies] dev` and add
`timeout = 60` to `[tool.pytest.ini_options]`.

### GAP-017 · Docker CI Dockerfiles Are Not Connected to Any CI Pipeline
**Severity:** MEDIUM — acceptance criteria partially unmet  
**Files:** `tests/integration/Dockerfile.ubuntu`, `Dockerfile.alpine`, `Dockerfile.arch`

Task 6.1 acceptance criterion:
> "Add GitHub Actions matrix: ubuntu-latest, alpine, archlinux"  
> "Run on every PR and nightly"

There is no `.github/` directory, no CI workflow files, and no CI automation at all.
The integration Dockerfiles exist but are only run manually per the progress.md note:
> "Run in Docker: `docker build -f tests/integration/Dockerfile.ubuntu -t rv-test .`"

This is a significant gap if the tool is intended for open-source collaboration.

---

## 6. Improvement Tasks Checklist (Not Fully Implemented)

Reviewing the IMPROVEMENTS_PLAN checkboxes vs. actual code:

| Task | Plan Status | Actual Status |
|------|-------------|---------------|
| 1.1 Harden Plugin Sandbox | ✅ all checks | ✅ Done |
| 1.2 Native Filesystem Watchers | ✅ all checks | ✅ Done |
| 1.3 Backup Snapshot Pruning | ✅ all checks | ⚠️ Auto-prune on restore missing (GAP-003) |
| 2.x All Providers | ✅ all checks | ✅ Done |
| 3.1 Package Cache | ✅ all checks | ✅ Done |
| 3.2 Parallel Asset Processing | **`[ ]` 10+ asset < 20% time** | ⚠️ No perf benchmark test |
| 3.3 Template Context Enhancement | ✅ all checks | ✅ Done |
| 4.1 Secret Rotation | **`[ ]` Tests** | ✅ Integration test exists |
| 4.2 GUI Authentication | ✅ all checks | ⚠️ CORS wildcard gap (GAP-002) |
| 4.3 ZeroBuffer | ✅ all checks | ⚠️ `zero_bytes` fragile (GAP-008) |
| 5.1 Profile Delta Preview | ✅ all checks | ✅ Done |
| 5.2 Workspace Sync | **`[ ]` dry-run shows per-workspace** | ⚠️ Partial (GAP-012) |
| 5.3 Per-Asset Hooks | **`[ ]` Pre/post hook runs** | ⚠️ Plugin hooks silently dropped (GAP-004) |
| 6.1 Docker Integration Tests | **`[ ]` CI passes on 3 distros** | ⚠️ No CI pipeline (GAP-017) |
| 6.2 Target Array Tests | ✅ all checks | ✅ Done |
| 6.3 Lockfile Checksums | ✅ all checks | ✅ Done |

---

## 7. Prioritized Implementation Tasks

### Priority 1 — Critical (Block Release)

| ID | Task | File(s) | Effort |
|----|------|---------|--------|
| T-001 | Remove dead `_plan_asset_parallel` method | `restore.py` | 15 min |
| T-002 | Fix CORS wildcard to loopback-only origin | `server.py` | 1 hour |
| T-003 | Auto-trigger `BackupPruner.prune()` in `RestoreService` post-commit | `restore.py`, `context.py` | 2 hours |
| T-004 | Raise error (not warning) for per-asset `AssetHookPlugin` entries | `handlers.py` | 30 min |

### Priority 2 — High (Required for 90% Coverage Mandate)

| ID | Task | File(s) | Effort |
|----|------|---------|--------|
| T-005 | Boost `recovery.py` coverage from 39% → 90%+ | `test_recovery.py` | 1 day |
| T-006 | Boost `transactions/context.py` coverage from 74% → 90%+ | `test_transactions.py` | 4 hours |
| T-007 | Boost `security/encryptor.py` coverage from 59% → 90%+ | `test_security.py` | 4 hours |
| T-008 | Boost `security/zerobuffer.py` coverage from 57% → 90%+ | `test_security.py` | 2 hours |
| T-009 | Boost `cli/main.py` coverage from 50% → 70%+ | `test_cli.py` | 1 day |
| T-010 | Boost `gui/server.py` coverage from 36% → 70%+ | `test_gui.py` | 1 day |

### Priority 3 — Medium (Security & Quality)

| ID | Task | File(s) | Effort |
|----|------|---------|--------|
| T-011 | Add non-loopback host warning to GUI server startup | `server.py`, `main.py` | 1 hour |
| T-012 | Fix or remove fragile `zero_bytes()` CPython hack; add docstring | `zerobuffer.py` | 2 hours |
| T-013 | Create `.github/workflows/ci.yml` for Docker matrix CI | `.github/` | 4 hours |
| T-014 | Add `SECURITY.md` documenting security model and known limits | project root | 2 hours |

### Priority 4 — Low (Polish & Definition of Done)

| ID | Task | File(s) | Effort |
|----|------|---------|--------|
| T-015 | Create `CHANGELOG.md` with entries for all 15 improvement tasks | project root | 1 hour |
| T-016 | Add `pytest-timeout` dep + `timeout = 60` to `pyproject.toml` | `pyproject.toml` | 15 min |
| T-017 | Remove duplicate inner imports in `backup.py` | `backup.py` | 5 min |
| T-018 | Add `--force-packages` / `--no-plugins` flags to `workspace sync` | `main.py` | 1 hour |
| T-019 | Performance benchmark test: 10+ assets parallel vs sequential | `test_services.py` | 2 hours |

---

## 8. Verification Commands

After fixes are applied, run:

```bash
# Full quality gate
.venv/bin/pytest --cov=src/rv --cov-fail-under=90 -q

# Type safety
.venv/bin/mypy src/rv

# Code quality
.venv/bin/ruff check src/rv tests
.venv/bin/ruff format --check src/rv tests

# Security scan (target 0 Medium/High)
.venv/bin/bandit -r src/rv -ll

# Build binary
.venv/bin/pyinstaller rv.spec --clean
```

---

## 9. Summary

The Revive codebase is well-structured with a strong security-first foundation,
comprehensive provider ecosystem, and solid transaction/rollback machinery. The
core is production-worthy, but the following must be resolved before a 1.0 tag:

- **2 critical security issues** (CORS wildcard, missing auto-pruning trigger)
- **1 dead code violation** (prohibited by AGENTS.md rules)
- **1 silently dropped feature** (per-asset plugin hooks)
- **Coverage at 67%** — well below the mandated 90% for core modules
- **No CI pipeline** — integration Dockerfiles exist but are not wired to automation
- **No CHANGELOG.md or SECURITY.md** — required by Definition of Done

Estimated remediation effort: **6–8 developer-days** to reach full production readiness.

---

*Generated by regression analysis of commit state as of 2026-05-25.*
