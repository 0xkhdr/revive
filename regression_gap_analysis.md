# Revive (`rv`) — Regression & Gap Analysis

> **Auditor:** Senior Staff Engineer review  
> **Date:** 2026-05-26  
> **Scope:** Full project — commit history, source, tests, CI, security model  
> **Methodology:** Live code analysis + static tooling + competitive research  
> Evidence cited by file path and commit SHA where available.

---

## § 1 — Project Charter & Vision

**Stated goal** (README.md L3): *"Transaction-safe developer environment manager. Synchronizes dotfiles, configs, encrypted secrets, system packages, and AI agent skills directly from a Git repository."*

**Differentiators claimed:**
1. Strict 7-step transactional rollback model
2. Age-based secret encryption (never plaintext to disk)
3. Sandboxed plugin system
4. AI-asset-first orientation (skills, MCP configs)
5. Multi-profile + machine-override inheritance

**Verdict:** Charter is coherent and internally consistent. The "AI assets" angle (`plugins/builtin/`) is a genuine differentiator vs. all surveyed competitors. No vision drift detected.

---

## § 2 — Regression Scorecard

Milestones identified: **v0.9.0** (pre-release), **v1.0.0** (`e19c0de`), **release/v1.1.0** (`6e2c754`, HEAD).

| Dimension | Score | Evidence |
|-----------|-------|----------|
| **Scope Creep** | 4/5 | TUI → Web GUI pivot mid-development (`426055d`). Adds value but was unplanned. Textual dependency added then stripped. No charter violation; GUI is additive. Minor: `rv prune` exposed as top-level command (README) but PLAN.md only mentions it inside `rv recover --auto`. |
| **Churn vs. Value** | 3/5 | See §3. ~60% of commits are `Feat:` with no `chore:` discipline. High-churn files: `cli/main.py`, `gui/server.py`, `services/restore.py`. TUI replaced entirely in one commit (`426055d`). |
| **Breaking Changes** | 3/5 | `start_gui_server()` signature changed (CHANGELOG: `cors_wildcard: bool` param). Marked as *internal* breaking. `CORS` wildcard → restrictive is correct but silent in semver (no major bump). CHANGELOG references `T-002` task IDs not visible outside internal tracker — reviewer-hostile. |
| **Test Coverage** | 2/5 | **Overall: 74%**. Mandate is ≥90% for `core/`, `security/`, `services/`, `transactions/`. **Actual**: `restore.py` 72%, `backup.py` 75%, `workspace.py` 57%, `encryptor.py` 60%, `base.py` (providers) 73%, `cargo/dnf/nix/pacman/pip` all 24–26%. `test_coverage_booster.py` exists — strong smell of gaming the metric rather than testing intent. |
| **Performance Trend** | 4/5 | Parallel asset planning added (`642a63a`). Benchmark test `test_parallel_planning_faster_than_sequential` exists. No regression tooling (no persistent benchmark history). Single data point, not a trend. |
| **Debt Accumulation** | 4/5 | Only 1 `NotImplementedError` found (`providers/base.py:235` — correct, abstract method). No `TODO`/`FIXME` in production code. Open issues not visible (private repo). CHANGELOG task IDs (`T-001`→`T-019`) reference an external tracker with no public link — dead-end for contributors. |

**Overall project health: 3.3/5** — above average for pre-1.0, below standard for a distributed security-sensitive tool.

---

## § 3 — Chore Philosophy Report

### Explicit Philosophy
AGENTS.md §6: Mandates `ruff format`, `ruff check`, `mypy --strict`, `bandit`. No explicit statement on `chore:` commit semantics. PLAN.md §7 lists AI agent execution rules — no mention of commit conventions.

### Implicit Philosophy (CI/CD)
`.github/workflows/ci.yml` reveals:
- Quality gate runs only on `ubuntu-latest`/Python 3.12 (not the full 3.11/3.12/3.13 matrix claimed in PLAN.md §5.5.4 and `pyproject.toml` classifiers)
- Nightly schedule (`0 2 * * *`) — good
- No release workflow — binary artifacts are never published automatically
- GitHub Actions pinned to **tag only** (`@v4`, `@v5`, `@v3`) — **not SHA-pinned** (supply chain risk, see §5)
- Codecov upload has `fail_ci_if_error: false` — silent coverage upload failures

### Commit Hygiene
All 50 visible commits audited. Results:

| Convention | Count |
|------------|-------|
| Proper conventional (`feat:`, `fix:`, `docs:`) | 3 |
| Capitalised non-conventional (`Feat:`, `Fix:`, `Refactor:`, `Feature:`) | ~20 |
| Free-form sentence | ~27 |
| `chore:` commits | **0** |

**Zero chore commits.** Either maintenance work is hidden in `Feat:` commits or it's not being done. Dependency bumps, gitignore updates, doc cleanups all appear as free-form sentences. Conventional Commits is listed as a project standard in PLAN.md but not enforced.

### Toil Reduction
- No Dependabot config (`.github/dependabot.yml` absent)
- No pre-commit hooks (`.pre-commit-config.yaml` absent)
- No automated release notes generation
- No changelog automation (CHANGELOG.md is hand-written)
- `migrations/` module planned in PLAN.md §3.M but **absent from codebase** — schema v2 → v3 migration path is pure dead air

**Chore Maturity: 2/5** — project relies entirely on manual discipline with no automated enforcement.

---

## § 4 — Competitive Domain Analysis

Revive is a **dotfile/environment lifecycle manager**, not a linter. Correct comparison domain:

| Capability | **Revive** | **chezmoi** | **mackup** | **GNU stow** | **yadm** | Gap Severity |
|------------|-----------|-------------|------------|--------------|----------|--------------|
| Transactional rollback | ✅ 7-step | ❌ | ❌ | ❌ | ❌ | **Revive wins** |
| Encrypted secrets (age) | ✅ native | ✅ native | ❌ | ❌ | ✅ (GPG) | Low |
| Package orchestration | ✅ 11 providers | ⚠️ scripts only | ❌ | ❌ | ❌ | **Revive wins** |
| Plugin sandbox | ✅ subprocess | ❌ | ❌ | ❌ | ❌ | **Revive wins** |
| AI asset management | ✅ builtin | ❌ | ❌ | ❌ | ❌ | **Revive wins** |
| Profile inheritance | ✅ recursive | ✅ (profiles) | ❌ | ❌ | ⚠️ (alt files) | Low |
| Template engine | ✅ Jinja2 | ✅ (Go text/template) | ❌ | ❌ | ⚠️ (Jinja2) | Low |
| Machine overrides | ✅ per-hostname YAML | ✅ | ❌ | ❌ | ✅ | Low |
| Binary distribution | ⚠️ PyInstaller (no published releases) | ✅ Go binary, published | ❌ pip only | ❌ system pkg | ❌ pip | **HIGH** |
| Community/ecosystem | ❌ 0 plugins | ✅ 2k+ users | ✅ popular | ✅ decades old | ✅ | **HIGH** |
| Windows support | ❌ deferred | ✅ | ✅ | ❌ | ❌ | Medium |
| WASM/LSP/IDE integration | ❌ | ❌ | ❌ | ❌ | ❌ | N/A (not in domain) |
| Web GUI | ✅ | ❌ | ❌ | ❌ | ❌ | **Revive wins** |
| Drift detection | ✅ rv status/diff | ✅ chezmoi diff | ❌ | ❌ | ⚠️ | Low |
| Documentation quality | ⚠️ README excellent, plugin API docs absent | ✅ | ⚠️ | ✅ | ✅ | Medium |

### Missing Features Backlog (ranked by impact × effort)

| Rank | Feature | Impact | Effort | Notes |
|------|---------|--------|--------|-------|
| 1 | Published binary releases (GitHub Releases + pip) | HIGH | Low | CI exists but never publishes |
| 2 | `docs/plugin-api.md` + `docs/architecture.md` | HIGH | Low | Referenced in PLAN.md §5.5.5, absent |
| 3 | Dependabot / Renovate automation | HIGH | Low | Zero dependency automation |
| 4 | `rv clone <repo>` golden-path command | HIGH | Med | PLAN.md §1.2 calls it "golden path" but not implemented |
| 5 | Plugin registry + signed plugin verification | Med | High | Post-1.0 backlog; community growth blocker |
| 6 | Windows support | Med | High | Explicitly deferred; limits adoption |
| 7 | Schema migrations engine | Med | Med | `migrations/` module in PLAN, absent in code |
| 8 | `pre-commit` hook enforcement | Med | Low | No gate on contributors' machines |

---

## § 5 — Architecture & Security Gap Register

### 5.1 Architecture Gaps

| ID | Category | Severity | Evidence | Remediation | Effort |
|----|----------|----------|----------|-------------|--------|
| A-001 | Module missing | Medium | `migrations/` in PLAN.md §3 module layout, absent in `src/rv/`. Schema v2→v3 upgrade path is undocumented and unimplemented. | Scaffold stub with version guard and `PLAN.md` update | Low |
| A-002 | Coverage gaming | High | `test_coverage_booster.py` (310 lines, 19 tests) exists explicitly to boost numbers. Contains real tests but the naming signals intent. `cargo/dnf/nix/pacman/pip` providers at 24–26% coverage — the booster doesn't reach them. | Delete file; integrate its valid tests into proper per-module test files; actually cover providers | Med |
| A-003 | Provider coverage collapse | High | `cargo.py`=24%, `dnf.py`=26%, `nix.py`=24%, `pacman.py`=26%, `pip.py`=24%. Five providers at ~1/4 coverage. These run on user machines. Package install failures would trigger rollback — untested path. | Write integration mocks for all providers; enforce per-provider ≥90% in CI | Med |
| A-004 | restore.py coverage | High | `restore.py` at 72% despite being the core 14-step engine. Lines 202–247, 337–396, 461–487 uncovered — these include profile resolution edge cases and package orchestration error paths. | Targeted unit tests; mock package providers | Med |
| A-005 | workspace.py coverage | High | `services/workspace.py` at 57%. Workspace registry mutations at low coverage — data corruption paths untested. | Write workspace CRUD tests | Low |
| A-006 | Python version matrix | Medium | `pyproject.toml` declares 3.11/3.12/3.13 support; CI only tests 3.12. PLAN.md promised multi-version matrix. | Add 3.11 and 3.13 to CI matrix | Low |
| A-007 | No `rv clone` command | Medium | PLAN.md §1.2: *"Golden Path UX: pip install revive-cli → rv clone <repo> → rv restore <profile>"*. `rv clone` does not exist. The golden path is broken. | Implement `rv clone <repo>` as thin `git clone` + workspace register + optional restore | Low |
| A-008 | No published release | High | `build-binary` job in CI produces a PyInstaller binary on push but does **not** upload to GitHub Releases. `pip install revive-cli` goes to PyPI? No evidence of PyPI publish workflow. No release tag workflow exists. | Add `release.yml` workflow triggered on `v*` tag push | Low |

### 5.2 Security Gaps

| ID | Category | Severity | Evidence | Remediation | Effort |
|----|----------|----------|----------|-------------|--------|
| S-001 | GitHub Actions not SHA-pinned | Medium | `ci.yml`: all `uses:` entries use mutable tags (`@v4`, `@v5`, `@v3`). Tag can be force-pushed by upstream. | Pin all actions to commit SHAs: `actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683` etc. | Low |
| S-002 | `codecov-action` without token | Medium | `ci.yml` L55: no `token:` specified for Codecov. Unauthenticated upload; coverage data is public and can be spoofed. | Add `CODECOV_TOKEN` secret; fail CI on upload error (`fail_ci_if_error: true`) | Low |
| S-003 | GUI auth over plain HTTP | Medium | `SECURITY.md` KL-003: *"X-Auth-Token is transmitted as a plain HTTP header (not HTTPS)."* Token is a 32-char hex string over localhost — acceptable **only** on loopback. Warning is documented but users frequently run `--host 0.0.0.0` without reading docs. | Add hard error (not warning) when binding non-loopback without `--https` or explicit `--i-understand-no-tls` flag | Low |
| S-004 | Plugin sandbox Python-only | High | `SECURITY.md` KL-002: native `.so` extensions can escape sandbox. The sandbox_wrapper imports `ctypes` for resource limits *before* blocking it — but a plugin dependency that bundles native code bypasses all Python-level restrictions. | Document clearly; add `--strict-sandbox` flag that runs plugins in Docker if available; add seccomp profile for Linux | High |
| S-005 | `ZeroBuffer.zero_bytes()` address arithmetic | Low | Fixed in CHANGELOG (T-012) for the correct formula. Remaining risk: CPython internals are private API; will silently no-op on PyPy or future CPython changes. Already documented in SECURITY.md as KL-001. | Consider `mlock()` via `ctypes` for truly sensitive buffers on Linux | High |
| S-006 | No Dependabot | Medium | Zero automated dependency updates. `pyrage>=1.0.0`, `PyYAML>=6.0`, `pydantic>=2.0.0` — all unpinned upper bounds. CVE in any of these propagates silently. | Add `.github/dependabot.yml` for pip and GitHub Actions | Low |
| S-007 | `shell=True` in test | Low | `tests/test_plugins.py:146`: `subprocess.run("echo hello", shell=True)`. Test-only but contradicts AGENTS.md §6.3 mandate. Sets bad precedent. | Convert to `subprocess.run(["echo", "hello"])` | Low |
| S-008 | YAML `safe_load` only — no schema version check before load | Medium | `ManifestLoader.load()` (`restore.py:57`) calls `yaml.safe_load()` then `Manifest.model_validate()`. If future schema v3 adds conflicting fields, v2 parser silently accepts garbage. | Check `raw_data.get("version")` before Pydantic validation; raise `UnsupportedSchemaVersion` for unknown versions | Low |

### 5.3 Performance Gaps

| ID | Category | Severity | Evidence | Remediation | Effort |
|----|----------|----------|----------|-------------|--------|
| P-001 | No persistent benchmark baseline | Low | Single benchmark test in `test_services.py`. No historical tracking. Performance regressions between releases are invisible. | Add `pytest-benchmark` and store results in CI artifacts | Med |
| P-002 | Package cache TTL hardcoded | Low | `PackageCache` TTL = 24h hardcoded (AGENTS.md §5.2). Not configurable via manifest. CI users with `--force-packages` bypass it entirely. | Expose `package_cache_ttl_hours` in manifest schema | Low |

### 5.4 Documentation Gaps

| ID | Category | Severity | Evidence | Remediation | Effort |
|----|----------|----------|----------|-------------|--------|
| D-001 | `docs/plugin-api.md` absent | High | PLAN.md §5.5.5 mandates it. Only coverage is `README.md` §Plugin System. Plugin authors cannot add a rule in <30 minutes from docs alone. `ReviveContext` fields not formally documented. | Write `docs/plugin-api.md` with full `ReviveContext` JSON schema, `plugin.yaml` fields, hook lifecycle, example | Low |
| D-002 | `docs/architecture.md` absent | Medium | Same PLAN.md mandate. README covers architecture at high level but component interaction diagram absent. | Write `docs/architecture.md` with component diagram, data flow, extension points | Low |
| D-003 | CHANGELOG task IDs opaque | Low | CHANGELOG references `T-001` through `T-019` with no link to tracker. External contributors cannot trace rationale. | Replace task IDs with PR/issue numbers or remove them; link to GitHub Discussions | Low |
| D-004 | No `CONTRIBUTING.md` | Medium | No contributor guide. Pre-commit hooks absent. New contributor has no path to: setup, run tests, style check, submit PR. | Write `CONTRIBUTING.md` + add `.pre-commit-config.yaml` | Low |

---

## § 6 — Synthesis & Roadmap

### 6.1 Top 5 Critical Gaps (fix in next release / v1.1.0)

| # | Gap | Why Critical |
|---|-----|-------------|
| 1 | **Provider test coverage 24–26%** (A-003) | Five package providers ship untested install paths to user machines. A pip/cargo/nix/dnf failure mid-restore leaves system in rollback state silently. |
| 2 | **No published binary/PyPI release** (A-008) | Tool exists only locally. The "1-second install" script `curl | sh` from README pulls from `main` branch — no pinned release, no checksum verification. Security and reproducibility failure. |
| 3 | **GitHub Actions tag-pinned, not SHA-pinned** (S-001) | Mutable action tags = supply chain attack surface. Any compromised action upstream silently poisons CI. |
| 4 | **`restore.py` at 72% coverage** (A-004) | 14-step core engine. Uncovered paths include profile resolution edge cases (L202–247) and package orchestration rollback (L337–396). Regressions here are catastrophic. |
| 5 | **`workspace.py` at 57%** (A-005) | Workspace registry mutations untested. Workspace corruption = user loses all registered repo paths. |

### 6.2 Top 5 Strategic Improvements (next 2 quarters)

| # | Improvement | Rationale |
|---|------------|-----------|
| 1 | **Automated releases** (GitHub Releases + PyPI publish workflow) | Without distribution, the tool cannot grow. chezmoi has 10k+ GitHub stars partly because `go install` works in 5s. |
| 2 | **`rv clone <repo>` golden-path command** | PLAN.md names it; README omits it. Removes the biggest UX gap vs. chezmoi (`chezmoi init <repo>`). |
| 3 | **Pre-commit hooks + Dependabot** | Chore maturity is 2/5. Automated enforcement stops discipline rot as the contributor base grows. |
| 4 | **`docs/plugin-api.md` + community plugin model** | Plugin sandbox is the key technical differentiator. Without docs, nobody writes plugins. Without plugins, sandbox complexity is pure cost. |
| 5 | **Kernel-level sandbox option** (`--strict-sandbox` via Docker/seccomp) | Python-level sandbox is acknowledged insufficient for production untrusted plugins. The architectural investment is already made; adding a container escape hatch completes it. |

### 6.3 Architecture Recommendation

**Do not pivot.** Core architecture is sound:
- Transaction model is genuinely novel in this space
- Age encryption is the right choice (modern, audited, chezmoi also uses it)
- Pydantic v2 strict validation is correct

**Two structural additions warranted:**
1. **Schema migrations module** — `migrations/` was planned, omitted. As schema evolves, users with old `manifest.yaml` files will silently fail Pydantic validation. Add version-gated migration shims now before user base grows.
2. **Provider abstraction needs a test harness** — the `BaseProvider` abstract class has no integration test fixture. Add a `FakeProvider` in conftest that all provider tests inherit from. This will make adding providers (e.g., `winget`, `homebrew cask`) trivial and well-tested.

**WASM plugins**: Interesting post-2.0 idea. Python WASM sandbox (via `wasmer-python` or `wasmtime`) would close KL-002 without Docker dependency. Not urgent; seccomp + Docker flag is the near-term answer.

### 6.4 Chore Philosophy Recommendation

Current state: **reactive manual maintenance**. Target state: **automated proactive hygiene**.

Concrete steps:
1. Add `.pre-commit-config.yaml` with `ruff`, `mypy`, `bandit` hooks — enforces AGENTS.md §6 on every local commit, zero CI surprises
2. Add `.github/dependabot.yml` — weekly PRs for pip and Actions updates
3. Adopt Conventional Commits **with a linter** (`commitlint` or `cz-conventional-changelog`) — `chore:` discipline then becomes enforceable
4. Replace internal `T-XXX` task ID references in CHANGELOG with GitHub issue links
5. Add `release.yml` workflow: on `v*` tag → run full CI → publish to PyPI → upload PyInstaller binaries to GitHub Releases

---

<details>
<summary>Appendix A — Raw Coverage Numbers (from live test run)</summary>

```
src/rv/providers/cargo.py      24%  (32-45, 55-80 uncovered)
src/rv/providers/dnf.py        26%  (32-42, 52-77 uncovered)
src/rv/providers/nix.py        24%  (37-47, 57-83 uncovered)
src/rv/providers/pacman.py     26%  (32-42, 52-77 uncovered)
src/rv/providers/pip.py        24%  (25-29, 40-50, 60-86 uncovered)
src/rv/providers/brew.py       68%
src/rv/providers/base.py       73%
src/rv/services/restore.py     72%  (202-247, 337-396, 461-487 uncovered)
src/rv/services/backup.py      75%
src/rv/services/workspace.py   57%
src/rv/security/encryptor.py   60%
src/rv/gui/server.py           49%  ← CHANGELOG T-010 claims "70%+"; actual is 49%
src/rv/transactions/context.py 93%
src/rv/transactions/lock.py    94%
src/rv/services/doctor.py      93%
src/rv/services/recovery.py    91%
TOTAL                          74%   (CI threshold: 90% — FAILING if enforced per-module)
```

> **CHANGELOG DISCREPANCY**: T-010 entry states `test_gui.py` pushed coverage from 36% → 70%+. Live measurement shows **49%**. Either tests were deleted after the changelog entry or the measurement methodology differed. `server.py` is 439 lines; 221 uncovered statements. The GUI REST API endpoints (L400–L487) are almost entirely untested.

Note: The 90% CI threshold applies to the **aggregate** (`--cov-fail-under=90` in `ci.yml`), not per-module. The aggregate passes only because high-coverage modules compensate for the provider failures. **The mandate in AGENTS.md is per-module for `core/`, `security/`, `services/`, `transactions/`.**

</details>

<details>
<summary>Appendix B — Commit Type Distribution</summary>

```
Free-form sentence (no prefix)     ~27 commits   (54%)
Feat: / Feature: (capitalised)     ~15 commits   (30%)
Refactor: / Fix:  (capitalised)    ~5 commits    (10%)
feat: / fix: / docs: (correct CC)  3 commits    (6%)
chore:                              0 commits    (0%)
```

The 94% non-compliant commit rate is the leading indicator for future contributor onboarding friction.

</details>

<details>
<summary>Appendix C — Supply Chain Risk Detail</summary>

Actions currently pinned by **mutable tag** (not SHA):
- `actions/checkout@v4` — should be `@11bd71901bbe5b1630ceea73d27597364c9af683`
- `actions/setup-python@v5` — should be `@a26af69be951a213d495a4c3e4e4022e16d87065`
- `codecov/codecov-action@v4` — should be `@13bc3af76a898c7be47ac7a9f8a4b4d53be2f5dd`
- `docker/setup-buildx-action@v3` — should be `@b5ca514318bd8c58d41b4776b9c5fa8754e9b5ef`

Use `pinact` or GitHub's own dependency review to automate SHA pinning.

</details>
