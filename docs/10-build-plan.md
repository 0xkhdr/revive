# 10 — Build Plan

Ordered phases. Each phase ends with **acceptance criteria** that are objectively checkable. Do not
start a phase before its predecessor's criteria pass.

The ordering follows the dependency graph, not the user-facing importance: paths and manifest come
first because everything reads them; the transaction comes before the engine because the engine is
a driver for it.

---

## Phase 0 — Skeleton

**Build:** Go module, directory layout from [09-go-architecture.md](09-go-architecture.md),
`cmd/rv/main.go`, cobra root command with `--verbose` and `--headless`, `log/slog` wiring, CI
running `go build`, `go vet`, `go test`, `golangci-lint`.

**Acceptance:**
- `rv --help` lists all commands (stubs returning "not implemented" are fine).
- `rv --version` prints a version injected at build time.
- CI is green on a push.

---

## Phase 1 — Paths, environment, interpolation

**Build:** `internal/paths` — the `paths.Config` struct with every runtime path, canonicalization,
`IsSafeSubpath`, symlink loop detection, `.env` loading, `${VAR}` / `${VAR:-default}` interpolation.

**Acceptance:**
- `${HOME}/x` interpolates; `${UNSET}/x` returns an error; `${UNSET:-/tmp}/x` yields `/tmp/x`.
- `.env` loading does not overwrite pre-existing environment variables and strips one layer of
  matching quotes.
- Canonicalization expands `~` and makes paths absolute **without** resolving symlinks.
- A symlink cycle `a → b → a` is detected.
- `paths.Config` can be rooted anywhere; no package calls `os.UserHomeDir()` outside it.

---

## Phase 2 — Manifest schema and validation

**Build:** `internal/manifest` — all structs, strict loading, the raw version pre-check, `Validate()`,
the `StringOrSlice` type.

**Acceptance:**
- The example manifest in [02-domain-model.md](02-domain-model.md) §8 loads and validates.
- `version: 3` returns `ErrUnsupportedSchemaVersion` naming versions 1 and 2.
- `source: ../../etc/passwd` and `source: /etc/passwd` are both rejected.
- `permissions: "644"`, `"0o644"`, `"rwx"` are all rejected; `"0644"` is accepted.
- A secret with `permissions: "0644"` is rejected; `"0600"` is accepted.
- `type: secret` forces `encrypted: true` even when the YAML says false.
- An unknown top-level field is an error, not a silent drop.
- A scalar `target` and a list `target` both load into `StringOrSlice`.
- **Golden test:** every YAML file in `reference/tests/` fixtures that the Python suite accepts is
  accepted, and every one it rejects is rejected.

---

## Phase 3 — Profile resolution

**Build:** `internal/profile` — recursive resolution, cycle detection, multi-profile merge, machine
override merging.

**Acceptance:**
- `work extends base` yields base's assets plus work's.
- A child's asset with the same ID as a parent's overrides it.
- `a extends b`, `b extends a` errors with the full chain in the message.
- A diamond (`c extends a,b`; both extend `base`) resolves without a false cycle error.
- `base,work` merges both, last-write-wins on assets, deduplicated append on packages.
- An unknown asset ID referenced by a profile errors, naming both the ID and the profile.
- A `machine/<hostname>.yaml` override replaces an asset by ID and appends to package lists.
- A missing override file is silent; a malformed one errors.

---

## Phase 4 — Crypto and scrubbing

**Build:** `internal/crypto` (age wrappers, identity/recipient resolution, secure temp files,
zeroing), `internal/scrub`.

**Acceptance:**
- `Encrypt` then `Decrypt` round-trips.
- **A file encrypted by the Python implementation decrypts correctly, and vice versa.** This is the
  interop gate; do not skip it.
- An identity file with a `# public key:` comment line parses correctly.
- A recipient given as a file path and as a literal `age1…` both work.
- `PublicKeyFromIdentity` returns the same key from the comment path and the derivation path.
- The scrubber redacts an `AGE-SECRET-KEY-1…` literal, an SSH public key, and a PEM block.
- A registered secret is redacted; two registered secrets where one is a prefix of the other are
  both fully redacted (length-descending ordering).
- Concurrent `RegisterSecret` and `Scrub` calls do not race (`go test -race`).
- A secure temp file is created mode `0600` and is zeroed and unlinked on close.

---

## Phase 5 — Transaction and journal

**Build:** `internal/transaction` — atomic write, process lock, the 7 phases, journal
serialization, rollback.

**Acceptance:**
- Atomic write creates its temp file in the target's directory and renames into place; an
  interrupted write leaves no partial target.
- Two processes cannot hold the lock simultaneously; the second gets `ErrLockHeld` immediately
  (non-blocking).
- The lock is released on every exit path including panic.
- Snapshot creates a backup for an existing target, records its mode and checksum, and creates no
  backup for a nonexistent target.
- A backed-up symlink is stored as `SYMLINK:<target>` and restores as a symlink, not a file.
- A backed-up directory restores as a directory.
- Rollback replays in reverse order.
- A failure in execute leaves the filesystem byte-identical to its pre-transaction state.
- A failure in verify (mode mismatch) triggers rollback.
- Journal JSON is byte-compatible with a Python-written journal — **read a Python journal from
  `reference/` fixtures and roll it back successfully.**
- A rollback whose individual entry fails continues with the remaining entries and reports
  `ErrRollbackIncomplete` naming the affected paths.

---

## Phase 6 — Asset handlers

**Build:** `internal/engine/handler.go` — planning for symlink, copy, template, secret; conflict
resolution; directory fan-out; per-asset hooks.

**Acceptance:**
- A symlink asset plans delete-if-exists then symlink to the absolute source.
- A copy asset with a directory source plans an atomic directory copy.
- A `text/template` source renders with env vars, built-ins (`_hostname`, `_user`, `_platform`,
  `_arch`, `_home`, `_repo_dir`), and `template_vars`, with `template_vars` winning. Syntax is
  `{{ .name }}`.
- An undefined template variable is an error, not an empty string (`missingkey=error` is set).
- Every function in the registered func map (`upper`, `lower`, `trim`, `replace`, `join`, `default`,
  `env`) works, and an unregistered function is a parse error naming the asset.
- The rendered SHA-256 is recorded on the transaction.
- A secret decrypts and plans a copy with mode `0600`; a missing identity errors.
- `conflict_strategy: skip` skips an existing target; `abort` errors; `overwrite` proceeds;
  `prompt` in non-interactive mode **errors** rather than skipping.
- A directory source with list targets matches each target's basename inside the source (trying
  `<basename>.age` first for encrypted assets).
- **Planning is side-effect free**: planning an asset with hooks executes nothing. Assert by giving
  a hook a command that creates a marker file and checking the marker is absent after planning.
- Hooks are planned as `hook` operations in the correct interleaved position:
  `delete → pre → write → chmod → post`.
- A hook command with a malformed quote fails at plan time, before any snapshot.
- A skipped target contributes **no operations, hooks included**.
- A `plugin:` reference inside asset hooks errors.

---

## Phase 7 — Providers

**Build:** `internal/providers` — the interface, the base type, the cache, the registry, all eleven
providers.

**Acceptance:**
- Each provider reports unavailable when its binary is absent (fake `Runner`).
- Each provider skips already-installed packages without running an install command.
- Each provider's dry-run path runs no command and logs the plan.
- The cache honors the 24 h TTL and is invalidated by `InvalidateAll`.
- A cache read failure yields an empty cache, never an error.
- `ExecuteWithRetry` retries three times with 2 s/4 s backoff, then returns a `ProviderError`
  wrapping the last failure.
- The node provider resolves a version from `version_file` and from `version` (explicit wins), treats
  `20` as matching `20.11.0`, and rejects a version string failing `^v?\d+(\.\d+)*$` before touching
  the `nvm` shell path.
- The registry's install order is the fixed sequence, not map iteration order.

---

## Phase 8 — The restore engine

**Build:** `internal/engine/restore.go` — all 14 steps, parallel planning, hooks, package
orchestration, lockfile update, audit commit, auto-prune.

**Acceptance:**
- A full restore against a fixture workspace produces the expected symlinks, files, and modes.
- Re-running it is a no-op: no mutations, and an unchanged lockfile except mtimes.
- `--dry-run` mutates nothing, writes no journal and no lockfile, and **runs no hook** — asset hooks
  and `pre-restore`/`post-restore` plugin hooks alike. Assert with marker-file hooks.
- `--sequential` and `--parallel` produce **identical** planned-operation ordering.
- A per-asset `command` hook executes in the execute phase, without a shell, with the four `RV_*`
  env vars, timing out at 30 s; a non-zero exit fails the transaction and rolls it back.
- A hook that runs and is then rolled back appears in `executed_hooks` in the journal and in the
  rollback report; the files are still restored exactly.
- A failure at step 10 (a provider error) rolls back the file changes from steps 7–9.
- A failure at step 12 (verification) rolls back.
- The lockfile round-trips: scalar targets write scalars, list targets write index-aligned arrays.
- **A lockfile written by the Python implementation is read without loss and rewritten equivalently.**
- The process lock is held for the entire run and released at the end.
- Backup pruning runs after success and its failure does not fail the restore.

---

## Phase 9 — CLI surface

**Build:** `internal/cli` — every command from [03-cli-spec.md](03-cli-spec.md) except `gui` and
`watch`, plus output rendering and shell completion.

**Acceptance:**
- Every command's `--help` matches the specification's flags.
- Exit codes follow the table: 0 success, 1 user error, 2 operation failure.
- `--headless` suppresses all decoration and turns every prompt into an error.
- Profile arguments accept `-p base -p work` and `-p base,work` identically.
- Profile-name completion works in bash and zsh, honoring an earlier `-m`.
- `rv init` scaffolds and refuses to run over an existing workspace.
- `rv clone` clones, registers a workspace, and optionally restores.
- `rv workspace sync` continues past a failing workspace and exits 1 if any failed.

---

## Phase 10 — Status, diff, doctor

**Build:** `internal/status`, `internal/doctor`.

**Acceptance:**
- Each of the six status values is produced by a targeted fixture.
- A hand-edited symlink target reports `type_mismatch`, not `modified`.
- A `chmod` on a target reports `permissions_drifted` before any content check runs.
- Template drift is detected when a template input variable changes.
- Encrypted drift detection works with an identity, and falls back to the lockfile mtime without one.
- `rv diff` renders side-by-side and unified output, skips symlinks and binaries, and its output
  passes through the scrubber.
- `doctor` produces every category in the table and exits 1 when a critical issue exists.
- `doctor --json` emits the documented structure.
- `doctor` mutates nothing.
- **`template_syntax` detection**: a source containing `{% if x %}`, `{{ bare }}`, or `{{ x | upper }}`
  is reported critical with file, line, and the `text/template` equivalent. A valid
  `{{ .x }}` / `{{ if .x }}` source produces no issue. The `{% … %}` case is the important one —
  `text/template` would pass it through as literal text rather than failing.
- `hook_command` detection: a hook whose `argv[0]` is not on `PATH` is reported as a warning.

---

## Phase 11 — Recovery, prune, backup

**Build:** `internal/recovery`, `internal/engine/backup.go`.

**Acceptance:**
- A simulated crash (kill during execute) leaves a journal that `rv recover` finds and rolls back
  to the exact pre-state.
- `rv recover --auto` rolls back the newest journal and exits 0.
- Discard removes the journal and backups without restoring files.
- Pruning never deletes a backup belonging to an incomplete journal, regardless of age.
- `max_count` and `max_age_days` are both enforced, and their candidate sets are deduplicated.
- `rv backup` copies a modified system file into the repo, re-encrypts secrets, skips templates with
  a warning, and reports symlinks already pointing at the repo as in-sync.
- A restore that starts with an unrecovered incomplete journal refuses to proceed **[DIVERGE]**.

---

## Phase 12 — Plugins

**Build:** `internal/plugins` — discovery, manifest parsing, sandboxed execution, hook dispatch.

**Acceptance:**
- Plugins are discovered in precedence order, first name wins, sorted by name within a directory.
- A malformed `plugin.yaml` is logged and skipped without affecting other plugins.
- A plugin receives the context, returns JSON on stdout, and its result is logged.
- A plugin exceeding its timeout is killed and fails the restore.
- A non-zero exit fails the restore and triggers rollback.
- `--no-plugins` skips all plugin execution.
- `--dry-run` invokes no plugin at any stage.
- `pre-restore` runs **after** the snapshot, so a plugin failure at that stage rolls back cleanly.
- The timeout is clamped to `[1, 300]`.
- With `network: false`, proxy environment variables are set (and the documentation states plainly
  that this is not real network isolation).

---

## Phase 13 — Watch daemon

**Build:** `internal/watcher` — fsnotify recursive watch, debounce, restore trigger, signal handling.

**Acceptance:**
- A file change triggers a restore after the debounce window.
- Rapid successive changes collapse into one restore.
- `.git` changes are ignored.
- A trigger while the process lock is held is skipped, not queued.
- SIGINT/SIGTERM shut down cleanly with no goroutine leak (`go test -race`, leak check).

---

## Phase 14 — Release

**Build:** goreleaser config, install documentation, migration guide from the Python version.

**Acceptance:**
- Static binaries build for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64.
- Binary size and startup time recorded (expect a large improvement over Python's import time —
  measure it, publish it).
- **End-to-end interop test:** a workspace restored by the Python `rv` is then managed by the Go
  `rv` — status reports in-sync, restore is a no-op, and the lockfile is preserved.
- Migration guide covers: **the Jinja2 → `text/template` syntax change** (with the conversion table
  from [09](09-go-architecture.md) §4.2 and a `rv doctor` walkthrough), the hook-timing change
  (hooks no longer run under `--dry-run`, and now execute inside the transaction), removal of
  `self-install`, removal of built-in plugins, Windows removal.
- A workspace containing Jinja2 templates fails `rv doctor` with actionable `template_syntax`
  issues rather than silently rendering `{% if %}` into the output file.

---

## Deferred to post-v1.0

- `rv gui` — the web dashboard ([03-cli-spec.md](03-cli-spec.md) records its API surface and the
  security constraints for whoever rebuilds it).
- Landlock/seccomp plugin sandboxing ([07-plugins-hooks.md](07-plugins-hooks.md) §3).
- Retry classification per provider ([06-providers.md](06-providers.md) §1).

---

## Working rules for the implementing agent

1. **Never transliterate.** The Python source is the behavioral oracle for *what*, never the model
   for *how*. If a Go idiom is clearer, use it.
2. **Every phase ships with tests.** A phase whose criteria are not covered by tests is not done.
3. **The interop tests are non-negotiable.** Phases 4, 5, 8 and 14 each contain one. They are the
   entire reason the compatibility contract is credible.
4. **When the spec and the reference disagree, the spec wins** — but say so, because it may be a
   spec bug.
5. **`[DIVERGE]` items are intentional.** Do not "fix" them back toward Python.
6. **Do not add features.** The scope is this specification. New ideas go in a list at the end of
   the phase, not in the code.
