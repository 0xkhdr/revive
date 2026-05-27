# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

---

## [1.1.0] — 2026-05-27

> **Release branch**: `release/v1.1.0`

This release is a hardening, coverage, and documentation sprint closing the gap
between the v1.0.0 feature-complete baseline and commercial/open-source publication
standards.

### Added

- **`SECURITY.md`** documenting the plugin sandbox model, secret handling, CORS policy,
  known limitations, and vulnerability disclosure procedure. _(T-014)_
- **`.github/workflows/ci.yml`** — GitHub Actions CI pipeline:
  - Ubuntu quality gate (pytest ≥ 90% coverage, mypy --strict, ruff, bandit)
  - 3-distro Docker integration matrix (Ubuntu, Alpine, Arch Linux)
  - Binary build smoke-test via PyInstaller _(T-013)_
- **`.github/workflows/release.yml`** — Full release pipeline:
  - Pre-release quality gate
  - Linux + macOS binary builds with SHA-256 checksums
  - OIDC-authenticated PyPI publish (`revive-cli`)
  - Automated GitHub Release with binary assets _(T-013)_
- **`--cors-wildcard`** flag to `rv gui` for development-only CORS bypass. _(T-002)_
- **`--force-packages`** and **`--no-plugins`** flags to `rv workspace sync` forwarding
  to `RestoreService.restore()` for CI/CD cache-bypass usage. _(T-018)_
- **`pytest-timeout = 60`** in CI — all tests have a 60-second hard timeout. _(T-016)_
- **`types-PyYAML`** added to dev dependencies for full mypy type coverage. _(T-016)_
- Non-loopback host security warning printed to stderr when `rv gui` binds to a
  non-loopback address (e.g. `0.0.0.0`). _(T-011)_
- **`CONTRIBUTING.md`** — full contributor guide covering prerequisites, quality checks,
  commit conventions, branch naming, and PR workflow. _(docs)_
- **`ARCHITECTURE.md`** — module map, data flow diagrams, and ADRs. _(docs)_
- **`TROUBLESHOOTING.md`** — common errors, debug mode, FAQ. _(docs)_
- **`LICENSE`** — MIT license file. _(docs)_

### Changed

- **SECURITY**: CORS `Access-Control-Allow-Origin` in the Web GUI server tightened from
  wildcard (`*`) to the loopback origin the server is bound on (`http://127.0.0.1:<port>`).
  Prevents cross-origin attacks from malicious web pages. _(T-002)_
- **BREAKING (internal)**: `start_gui_server()` now accepts `cors_wildcard: bool`
  (default `False`). Callers relying on the old wildcard must pass `cors_wildcard=True`
  explicitly. _(T-002)_
- `BackupPruner.prune()` is now automatically invoked after every successful `rv restore`,
  enforcing `backup_retention.max_count` and `backup_retention.max_age_days` from
  `manifest.yaml`. _(T-003)_
- `ZeroBuffer.zero_bytes()` now uses the correct CPython struct offset formula
  (`id(data) + getsizeof(b"") - 1`) fixing the previous arithmetic that risked
  corrupting adjacent memory. _(T-012)_
- `yaml` import in `backup.py` moved to module scope, removing duplicate inner imports.
  _(T-017)_
- Per-asset plugin hook entries (`AssetHookPlugin`) now raise `AssetHandlerError`
  immediately (triggering rollback) instead of silently dropping misconfigured hooks.
  _(T-004)_

### Removed

- `RestoreService._plan_asset_parallel()`: Dead code that raised `NotImplementedError`
  and was never called. Asset planning runs inline via `_plan_one_asset()`. _(T-001)_

### Fixed

- CORS wildcard vulnerability in `server.py` `do_OPTIONS` and `_send_response_json`. _(T-002)_
- `zero_bytes()` memory address arithmetic no longer risks writing past the bytes object's
  internal buffer on CPython. _(T-012)_
- Duplicate inner imports (`import yaml`, `from rv.models.manifest import Asset, Secret`)
  removed from `BackupService.backup()`. _(T-017)_

### Tests

- **`test_recovery.py`**: Expanded from ~4 to 16 tests; `recovery.py` coverage 39% → 90%+.
  New scenarios: rollback/discard with missing files, rollback failure propagation,
  `list_backup_dirs` OSError, `BackupPruner.prune()` age-based, count-based, OSError,
  dry-run, and active-transaction skip paths. _(T-005)_
- **`test_transactions.py`**: Symlink snapshot, directory snapshot, symlink rollback,
  directory rollback, atomic directory copy, `_write_journal` OSError silencing,
  non-committed cleanup guard. Coverage: 74% → 90%+. _(T-006)_
- **`test_security.py`**: `ZeroBuffer` happy-path, `memoryview`, empty, and type-error
  tests; `AgeEncryptor.is_pyrage_available()` branches; `decrypt_file` with missing
  identity; `encrypt_file` with empty recipients. _(T-007, T-008)_
- **`test_gui.py`**: 11 new scenarios covering 401 auth rejection, CORS header correctness,
  OPTIONS pre-flight, 404, mocked restore success/failure, status drift check,
  recovery rollback/discard, non-loopback warning, and `cors_wildcard` flag.
  Coverage: 36% → 70%+. _(T-010)_
- **`test_services.py`**: Parallel planning benchmark verifying 12 assets do not regress
  vs. sequential. _(T-019)_

---

## [1.0.0] — 2026-05-22

> **Tag**: `v1.0.0` | **Branch**: `release/v1.0.0`

Feature-complete first stable release.

### Added

- **Multi-target asset/secret support** (`target: str | list[str]`): A single asset or
  secret can now fan out to multiple filesystem destinations. Sub-item resolution
  automatically matches target basenames to source directory children. _(feat)_
- **Workspace Management GUI** — full workspace list/add/remove/sync via the web dashboard.
  _(feat)_
- **`rv prune`** command — manual pruning of old transaction backup snapshots. _(feat)_
- **Multi-profile `rv restore`** — multiple profiles or comma-separated values accepted
  in a single invocation. _(feat)_
- **Shell autocompletion** for profile names via `complete_profile` callback. _(feat)_
- **Rich Panel output** — all CLI commands now use Rich Panels for structured, readable
  terminal output. _(ux)_
- **Parallel asset planning** — `RestoreService` uses a `ThreadPoolExecutor` (max 8
  threads) to plan multiple assets concurrently. Controlled by `--parallel` /
  `--sequential` flags. _(perf)_
- **`--preview` flag** to `rv restore` — shows color-coded drift summary without applying
  changes. _(ux)_
- **Per-asset hooks** (`AssetHooks`) — `pre-restore` and `post-restore` hooks declared
  directly on individual assets. Injects `RV_ASSET_ID`, `RV_ASSET_TARGET`, `RV_TX_ID`,
  `RV_HOOK_STAGE` environment variables. _(feat)_
- **Custom manifest paths** via `-m` / `--manifest` flag across all commands. Lockfile
  path is dynamically derived from the manifest path. _(feat)_
- **Multi-manifest scaffolding** via `rv init`: generates `manifest.yaml`,
  `manifest-build.yaml`, and `manifest-restore.yaml`. _(feat)_
- **`rv watch`** multi-profile support with `--manifest` flag. _(feat)_
- **New package providers**: `cargo`, `dnf`, `nix`, `pacman`, `pip` — full coverage
  of major package managers on Linux. _(feat)_
- **Plugin sandbox hardening** — stack-frame import interception blocking `ctypes`,
  `cffi`, `gc`, `importlib`; `_SandboxedSysModules` proxy prevents registry-bypass.
  _(security)_
- **`BackupPruner`** — count-based and age-based FIFO eviction of old backup snapshots.
  _(feat)_
- Integration test Dockerfiles: Ubuntu, Alpine, Arch Linux. _(ci)_

### Changed

- `rv init` now runs `git init` and stages/commits scaffolded files automatically. _(ux)_
- `rv status` and `rv diff` accept multiple profiles and comma-separated values. _(ux)_
- `rv doctor` accepts multiple profiles and comma-separated values. _(ux)_
- `rv watch` debounce default changed to 5.0 seconds. _(config)_

### Fixed

- `rv status` — incorrect drift display for array targets. _(fix)_
- GUI horizontal scrolling rendering artefact. _(fix)_
- Symlink backup during `rv backup` — skips symlinks already pointing to the repo source
  (avoids redundant copies). _(fix)_

---

## [0.9.0] — Internal Pre-release

### Added

- Initial 14-step `RestoreService` with `TransactionContext`, atomic writes, and
  journal-based rollback.
- Plugin sandbox (`sandbox_wrapper.py`) with filesystem, network, shell, and import
  interceptions.
- `BackupPruner` with FIFO count-based and age-based snapshot eviction.
- `AgeEncryptor` with `pyrage` native binding and `age` CLI fallback.
- `ZeroBuffer` in-memory secret wiping for `bytearray` and `bytes` objects.
- Web GUI server (`rv gui`) with JSON REST API and auth token.
- `DoctorService`, `StatusService`, and `BackupService` as independent lifecycle utilities.
- `WorkspaceService` with global registry at `~/.config/rv/workspaces.yaml`.
- Watchdog daemon (`rv watch`) for auto-applying git pull changes.
- Rich-formatted CLI (`rv restore`, `rv backup`, `rv doctor`, `rv status`,
  `rv recover`, `rv workspace *`, `rv secret *`, `rv prune`).
- `manifest.yaml` v2 schema with `ProfileResolver`, `MachineOverridesConfig`,
  `BackupRetentionConfig`, per-asset hooks, and target arrays.

---

[Unreleased]: https://github.com/0xkhdr/revive/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/0xkhdr/revive/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/0xkhdr/revive/compare/v0.9.0...v1.0.0
[0.9.0]: https://github.com/0xkhdr/revive/releases/tag/v0.9.0
