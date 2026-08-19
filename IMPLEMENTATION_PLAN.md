# Revive v1 — Implementation Plan (Go rebuild)

Agent-facing entrypoint. Start here, then read `docs/` in order. This file says **what to do, in
what order, and when a stage is done**. It does not restate the specification — `docs/` is
normative and wins on every detail.

---

## 0. Mission

Rebuild `rv` from an empty Go module (`github.com/0xkhdr/revive`) so that it is **drop-in
compatible** with a machine previously managed by the Python implementation in `reference/`.

- `reference/` is the **behavioral oracle**: read it to resolve ambiguity. Never transliterate it.
- `docs/01`–`docs/11` are the specification. `MUST` = acceptance criterion.
- `[DIVERGE]` markers are intentional improvements. Do not "fix" them back toward Python.
- When spec and reference disagree, **the spec wins** — but report it, it may be a spec bug.
- **Do not add features.** New ideas go in a list at the end of the stage, not in the code.

### Compatibility contract (non-negotiable)

Same `manifest.yaml` (schema v1 and v2), same `manifest.lock` JSON, same journals in
`~/.config/rv/journals/`, same `.age` files with the same identity file, same
`~/.config/rv/workspaces.yaml`.

Four interop gates enforce it — stages 4, 5, 8, 14. If one fails, the stage is not done.

---

## 1. Required reading before writing code

| Read | Before stage |
|------|--------------|
| `docs/01-overview.md`, `docs/09-go-architecture.md` | 0 |
| `docs/02-domain-model.md` | 2 |
| `docs/04-restore-engine.md`, `docs/05-security.md` | 4 |
| `docs/06-providers.md` | 7 |
| `docs/03-cli-spec.md` | 9 |
| `docs/08-drift-doctor-recovery.md` | 10 |
| `docs/07-plugins-hooks.md` | 12 |
| `docs/11-testing-quality.md` | continuously |
| `docs/10-build-plan.md` | **full acceptance criteria for every stage — the authority** |

---

## 2. Ground rules for every stage

1. **A stage ships with its tests.** Criteria not covered by tests = stage not done.
2. Do not start a stage before its predecessor's criteria pass.
3. Everything under `internal/`. `cmd/rv/main.go` stays tiny (build root command, map error to
   exit code).
4. Inject the four seams from `docs/09` §7 — `Paths`, `Runner`, `Clock`, `Out`. No package calls
   `os.UserHomeDir()` outside `internal/paths`. Tests root everything at `t.TempDir()` and run
   `t.Parallel()`.
5. `go build`, `go vet`, `go test -race`, `golangci-lint` green before moving on.
6. Coverage floor **>90% on `internal/`**. Floor, not goal — the stage criteria are the real spec.
7. Commit per stage. Message names the stage and the criteria that now pass.

---

## 3. Stages

Ordering follows the dependency graph, not user-facing importance. Full acceptance criteria live in
`docs/10-build-plan.md` — the summaries below are the map, that file is the checklist.

Status legend: **DONE** = every acceptance criterion in `docs/10-build-plan.md` passes and is covered
by a test. Anything unmarked has not been started.

| # | Status | Stage | Packages built | Gate summary |
|---|--------|-------|----------------|--------------|
| 0 | **DONE** | Skeleton | `cmd/rv`, cobra root, `log/slog`, CI | `--help` lists all commands (stubs OK), `--version` injected at build time, CI green |
| 1 | **DONE** | Paths, env, interpolation | `paths` | `${VAR}` / `${VAR:-default}`, `.env` never overwrites existing env, `~` expanded without resolving symlinks, symlink cycle detected, config rootable anywhere |
| 2 | **DONE** | Manifest schema + validation | `manifest` | v1/v2 load, `version: 3` errors, path traversal rejected, only `0NNN` permissions, secrets forced `encrypted: true`, unknown field = error, `StringOrSlice`; **golden test against `reference/tests/` fixtures** |
| 3 | **DONE** | Profile resolution | `profile` | inheritance, child overrides parent by asset ID, cycle detected with full chain, diamond resolves, multi-profile merge, `machine/<hostname>.yaml` overrides |
| 4 | | Crypto + scrubbing | `crypto`, `scrub` | round-trip; **INTEROP: Python-encrypted file decrypts and vice versa**; identity comment parsing; scrubber redacts age keys/SSH/PEM and registered secrets longest-first; race-clean; secure temp files `0600`, zeroed and unlinked |
| 5 | | Transaction + journal | `transaction` | atomic write via same-dir temp + rename, non-blocking process lock released on every path incl. panic, snapshot/rollback of files, symlinks (`SYMLINK:<target>`) and dirs, failure leaves FS byte-identical; **INTEROP: roll back a Python-written journal**; partial rollback reports `ErrRollbackIncomplete` |
| 6 | | Asset handlers | `engine/handler.go` | symlink/copy/template/secret planning, `text/template` with built-ins + `template_vars` and `missingkey=error`, func map, secret copy at `0600`, conflict strategies (`prompt` non-interactive = error), directory fan-out, **planning is side-effect free**, hooks planned in `delete → pre → write → chmod → post` order |
| 7 | | Providers | `providers` | 11 providers: unavailable without binary, skip installed packages, dry-run runs nothing, 24 h cache TTL (read failure = empty cache, never error), retry 3× with 2 s/4 s backoff, node version resolution + validation, fixed install order |
| 8 | | Restore engine | `engine/restore.go`, `lockfile` | all 14 steps; re-run is a no-op; `--dry-run` mutates nothing and **runs no hook**; `--sequential` and `--parallel` plan identically; hooks execute shell-free with `RV_*` env and 30 s timeout; failure at step 10 or 12 rolls back; **INTEROP: read a Python lockfile without loss** |
| 9 | | CLI surface | `cli` | every command from `docs/03` except `gui`/`watch`; exit codes 0/1/2; `--headless` kills decoration and turns prompts into errors; `-p a -p b` ≡ `-p a,b`; completion; `init` refuses over existing workspace; `workspace sync` continues past failures, exits 1 |
| 10 | | Status, diff, doctor | `status`, `doctor` | six status values, `type_mismatch` vs `modified`, permissions checked first, template + encrypted drift, diff scrubbed and skips binaries/symlinks, `doctor --json`, doctor mutates nothing, **Jinja2 `template_syntax` detection incl. `{% … %}`** |
| 11 | | Recovery, prune, backup | `recovery`, `engine/backup.go` | simulated crash recovers to exact pre-state, `recover --auto`, discard, pruning never touches incomplete-journal backups, `max_count` + `max_age_days`, `rv backup` re-encrypts secrets and warns on templates; restore refuses to start with an unrecovered journal **[DIVERGE]** |
| 12 | | Plugins | `plugins` | discovery precedence (first name wins), malformed manifest skipped, JSON result on stdout, timeout clamped `[1,300]` and kills, non-zero exit rolls back, `--no-plugins` / `--dry-run` invoke nothing, `pre-restore` runs **after** snapshot |
| 13 | | Watch daemon | `watcher` | debounced restore, rapid changes collapse to one, `.git` ignored, trigger during held lock is skipped not queued, clean SIGINT/SIGTERM with no goroutine leak |
| 14 | | Release | goreleaser, docs | static binaries for linux/darwin × amd64/arm64, size + startup measured and published, **INTEROP: Python-restored workspace managed by Go `rv` — status in-sync, restore no-op, lockfile preserved**, migration guide (Jinja2 → `text/template`, hook-timing change, removed `self-install`/built-in plugins/Windows) |

### Stage 0-3 completion notes (2026-08-19)

Stages 0-3 are done: `go build`, `go vet`, `go test -race ./...` and `golangci-lint run ./...` are
all green, and coverage on `internal/` is **95.6%**, above the 90% floor.

Three points where the spec was applied over the reference, as the working rules require:

1. **An omitted `version` defaults to 2.** `docs/02` §1.1 and the `docs/09` §3 loader snippet both
   allow it (`probe.Version != nil && …`). The Python `ManifestLoader.load` rejects a missing
   version outright, because its pre-check reads `raw_data.get("version")` and compares `None`
   against `(1, 2)`. The spec wins; flagging it in case the spec meant to require the field.
2. **Duplicate asset/secret IDs are rejected.** `docs/02` §1.2 says an ID is "unique within the
   pool"; the Python model never enforced it and silently kept the last one.
3. **A `Secret` carries only the fields `docs/02` §1.5 documents**, so `conflict_strategy`,
   `hooks` and `template_vars` on a secret are strict-decode errors. Pydantic silently ignored
   them, which would leave a user's hook quietly never running.

Two guards were added that no criterion names, both on manifest-controlled paths:
`machine_overrides.path` may not escape the repository, and `.env` read errors surface instead of
truncating the file silently.

Ideas raised while building, deliberately **not** implemented (per ground rule: no added features):

- Per-command flag declarations are deferred to their implementing stages, so no flag exists
  without code that reads it. Stage 9 adds them.
- A `platform` seam for the hostname: `profile.ApplyOverrides` takes the hostname as a parameter
  for now, which is enough of a seam for tests. Stage 10 builds `internal/platform`.

---

## 4. Deferred to post-v1.0

`rv gui`; Landlock/seccomp plugin sandboxing; per-provider retry classification. Do not build them.

---

## 5. Definition of done for v1

- Stages 0–14 criteria in `docs/10-build-plan.md` all pass, each covered by a test.
- Four interop gates green (4, 5, 8, 14).
- `internal/` coverage >90%, `go test -race ./...` clean, lint clean, CI green.
- Migration guide published; a workspace with Jinja2 templates fails `rv doctor` with actionable
  `template_syntax` issues rather than rendering `{% if %}` as literal text.
