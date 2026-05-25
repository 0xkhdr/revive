# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Added
- `SECURITY.md` documenting the plugin sandbox model, secret handling, CORS policy,
  known limitations, and vulnerability disclosure procedure. _(T-014)_
- `.github/workflows/ci.yml`: GitHub Actions CI pipeline with Ubuntu quality gate
  (pytest ≥90% coverage, mypy, ruff, bandit), 3-distro Docker integration matrix
  (Ubuntu, Alpine, Arch Linux), and binary build smoke test. _(T-013)_
- `--cors-wildcard` flag to `rv gui` for development-only CORS bypass. _(T-002)_
- `--force-packages` and `--no-plugins` flags to `rv workspace sync` to forward
  to `RestoreService.restore()`, enabling CI/CD usage with cache bypass. _(T-018)_
- `pytest-timeout = 60` to CI configuration — all tests now have a 60-second hard
  timeout to prevent hanging test suites from blocking CI runners. _(T-016)_
- `types-PyYAML` added to dev dependencies for full mypy type coverage. _(T-016)_
- Non-loopback host security warning printed to stderr when `rv gui` binds to a
  non-loopback address (e.g. `0.0.0.0`). _(T-011)_

### Changed
- **SECURITY**: CORS `Access-Control-Allow-Origin` in the Web GUI server changed from
  wildcard (`*`) to the loopback origin the server is bound on (`http://127.0.0.1:<port>`).
  This prevents cross-origin attacks from malicious web pages against the GUI API. _(T-002)_
- **BREAKING (internal)**: `start_gui_server()` now accepts a `cors_wildcard: bool`
  parameter (default `False`). Callers that previously relied on the wildcard CORS policy
  must pass `cors_wildcard=True` explicitly for development use. _(T-002)_
- `BackupPruner.prune()` is now automatically invoked after every successful `rv restore`,
  using the `backup_retention.max_count` and `backup_retention.max_age_days` values from
  `manifest.yaml`. This closes the gap where retention was declared but never enforced. _(T-003)_
- `ZeroBuffer.zero_bytes()` now uses the correct CPython struct offset formula
  (`id(data) + getsizeof(b"") - 1`) instead of the incorrect
  `id(data) + getsizeof(b"") - len(data)` arithmetic that risked corrupting adjacent
  memory or silently no-oping on longer strings. _(T-012)_
- `yaml` import in `backup.py` moved from inside-method lazy import to module-level
  import, removing the duplicate `from rv.models.manifest import Asset, Secret` that
  was already present at module scope. _(T-017)_
- Per-asset plugin hook entries (`AssetHookPlugin` in asset-level hooks) now raise
  `AssetHandlerError` immediately instead of logging a silent warning and dropping the
  hook. This ensures transaction rollback is triggered on misconfigured manifests. _(T-004)_

### Removed
- `RestoreService._plan_asset_parallel()`: Dead code removed. The method raised
  `NotImplementedError` (with `# pragma: no cover`) and was never called anywhere in
  the codebase. Asset planning is performed inline via `_plan_one_asset()`. _(T-001)_

### Fixed
- CORS wildcard vulnerability in `server.py` `do_OPTIONS` and `_send_response_json`. _(T-002)_
- `zero_bytes()` memory address arithmetic no longer risks writing past the beginning
  of the bytes object's internal buffer on CPython. _(T-012)_
- Duplicate inner imports (`import yaml`, `from rv.models.manifest import Asset, Secret`)
  removed from `BackupService.backup()` method body. _(T-017)_

### Tests
- `test_recovery.py`: Expanded from ~4 test functions to 16, pushing `recovery.py`
  coverage from 39% to 90%+. New scenarios: rollback/discard with missing files,
  rollback failure propagation, `list_backup_dirs` OSError, `_get_active_tx_ids` with
  no journal dir, `BackupPruner.prune()` age-based, count-based, OSError, dry-run,
  and active-transaction skip paths. _(T-005)_
- `test_transactions.py`: Expanded to cover symlink snapshot, directory snapshot, symlink
  rollback, directory rollback, atomic directory copy execution, `_write_journal` OSError
  silencing, and non-committed cleanup guard. Coverage: 74% → 90%+. _(T-006)_
- `test_security.py`: Added `ZeroBuffer` happy-path, `memoryview`, empty, and type-error
  tests; `AgeEncryptor.is_pyrage_available()` with and without pyrage; `decrypt_file`
  with missing identity; `encrypt_file` with empty recipients. _(T-007, T-008)_
- `test_gui.py`: Added 11 new test scenarios covering 401 auth rejection, CORS header
  correctness, OPTIONS pre-flight, unknown endpoint 404, mocked restore success/failure,
  status drift check, recovery rollback/discard edge cases, non-loopback warning, and
  `cors_wildcard` flag. Coverage: 36% → 70%+. _(T-010)_
- `test_services.py`: Added `test_parallel_planning_faster_than_sequential` benchmark
  verifying parallel planning of 12 assets does not regress vs. sequential. _(T-019)_

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

[Unreleased]: https://github.com/your-org/revive/compare/v0.9.0...HEAD
[0.9.0]: https://github.com/your-org/revive/releases/tag/v0.9.0
