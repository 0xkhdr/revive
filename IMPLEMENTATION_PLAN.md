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
| 4 | **DONE** | Crypto + scrubbing | `crypto`, `scrub` | round-trip; **INTEROP: Python-encrypted file decrypts and vice versa**; identity comment parsing; scrubber redacts age keys/SSH/PEM and registered secrets longest-first; race-clean; secure temp files `0600`, zeroed and unlinked |
| 5 | **DONE** | Transaction + journal | `transaction` | atomic write via same-dir temp + rename, non-blocking process lock released on every path incl. panic, snapshot/rollback of files, symlinks (`SYMLINK:<target>`) and dirs, failure leaves FS byte-identical; **INTEROP: roll back a Python-written journal**; partial rollback reports `ErrRollbackIncomplete` |
| 6 | **DONE** | Asset handlers | `engine/handler.go` | symlink/copy/template/secret planning, `text/template` with built-ins + `template_vars` and `missingkey=error`, func map, secret copy at `0600`, conflict strategies (`prompt` non-interactive = error), directory fan-out, **planning is side-effect free**, hooks planned in `delete → pre → write → chmod → post` order |
| 7 | **DONE** | Providers | `providers` | 11 providers: unavailable without binary, skip installed packages, dry-run runs nothing, 24 h cache TTL (read failure = empty cache, never error), retry 3× with 2 s/4 s backoff, node version resolution + validation, fixed install order |
| 8 | **DONE** | Restore engine | `engine/restore.go`, `lockfile` | all 14 steps; re-run is a no-op; `--dry-run` mutates nothing and **runs no hook**; `--sequential` and `--parallel` plan identically; hooks execute shell-free with `RV_*` env and 30 s timeout; failure at step 10 or 12 rolls back; **INTEROP: read a Python lockfile without loss** |
| 9 | **DONE** | CLI surface | `cli` | every command from `docs/03` except `gui`/`watch`; exit codes 0/1/2; `--headless` kills decoration and turns prompts into errors; `-p a -p b` ≡ `-p a,b`; completion; `init` refuses over existing workspace; `workspace sync` continues past failures, exits 1 |
| 10 | | Status, diff, doctor | `status`, `doctor` | six status values, `type_mismatch` vs `modified`, permissions checked first, template + encrypted drift, diff scrubbed and skips binaries/symlinks, `doctor --json`, doctor mutates nothing, **Jinja2 `template_syntax` detection incl. `{% … %}`** |
| 11 | | Recovery, prune, backup | `recovery`, `engine/backup.go` | simulated crash recovers to exact pre-state, `recover --auto`, discard, pruning never touches incomplete-journal backups, `max_count` + `max_age_days`, `rv backup` re-encrypts secrets and warns on templates; restore refuses to start with an unrecovered journal **[DIVERGE]** |
| 12 | | Plugins | `plugins` | discovery precedence (first name wins), malformed manifest skipped, JSON result on stdout, timeout clamped `[1,300]` and kills, non-zero exit rolls back, `--no-plugins` / `--dry-run` invoke nothing, `pre-restore` runs **after** snapshot |
| 13 | | Watch daemon | `watcher` | debounced restore, rapid changes collapse to one, `.git` ignored, trigger during held lock is skipped not queued, clean SIGINT/SIGTERM with no goroutine leak |
| 14 | | Release | goreleaser, docs | static binaries for linux/darwin × amd64/arm64, size + startup measured and published, **INTEROP: Python-restored workspace managed by Go `rv` — status in-sync, restore no-op, lockfile preserved**, migration guide (Jinja2 → `text/template`, hook-timing change, removed `self-install`/built-in plugins/Windows) |

### Stage 9 completion notes (2026-08-19)

Stage 9 is done. Coverage on `internal/` is **92.3%**. Packages added: `workspace`; `internal/cli`
now carries the real command tree, with all rendering confined to `render.go` so no
business-logic package imports a terminal library.

Wired to real engines: `init`, `clone`, `restore`, `secret {keygen,encrypt,decrypt,rotate}`,
`workspace {list,add,remove,sync}`, and completion for bash/zsh/fish.

**Flags are declared for the commands whose engines arrive later** — `status`, `diff`, `doctor`,
`backup`, `recover`, `prune`, `self-uninstall` — in `pending.go`, with bodies returning
`ErrNotImplemented`. That satisfies "every command's `--help` matches the specification's flags"
and makes profile completion work everywhere now, while leaving each body to the stage that
builds the engine behind it. `restore --preview` is the same: it is the `status` engine, so it
reports not-implemented until stage 10.

Three decisions worth recording:

1. **`--headless` implies non-interactive.** `Env.Confirm` returns a `ErrUsage` naming the prompt
   rather than answering for the user, and `restore` does not install a confirmer under
   `--headless`. A CI run that silently answers a conflict prompt is how unattended data loss
   happens.
2. **Exit codes come only from sentinels.** `ExitCode` matches with `errors.Is` against the
   sentinels each package owns, and a test asserts that an error whose *message* reads
   "usage error" still exits 2 — message text must never decide the code.
3. **`workspace sync` copies the `Env` per workspace** to set `WorkDir`, so each repo's relative
   manifest and its own `.env` resolve against the right directory, and one failure never stops
   the rest. It exits with `ErrOperation` (code 2) when any workspace failed.

`Env.Git` is injectable, which is what lets `clone` and `workspace sync` be tested end to end
with no network — including the case where the middle workspace of three fails and the ones
either side still restore.

Deliberately **not** built: `rv gui` (deferred post-v1.0) and `rv watch` (stage 13), neither of
which appears in `--help`.

---

### Stage 8 completion notes (2026-08-19)

Stage 8 is done, with the third interop gate green. Coverage on `internal/` is **92.8%**.
Packages added: `lockfile`; `engine/restore.go` drives all fourteen steps; `logging/audit.go`
adds the JSON audit handler and a fan-out so one logger reaches console and audit file with
neither able to skip the scrubber.

**Interop gate 8 (lockfile).** `internal/lockfile/testdata/python_manifest.lock` is output from
the reference's own `Lockfile` model. It loads here with scalar targets still scalar and list
targets still index-aligned arrays, and re-serializes JSON-equal to the original. Key order
differs — Go marshals maps sorted, Python preserves insertion order — which is why the criterion's
word is *equivalently* rather than byte-identically.

**A second, unlisted compatibility trap, now pinned by a test.** The lockfile's directory
checksum walks *every file in a directory before descending*, both in name order. That is
Python's `os.walk` with `dirs.sort()`; Go's `filepath.WalkDir` uses a single merged lexical order
that interleaves files and subdirectories differently and produces a **different digest for the
same tree**. Had this been missed, every directory asset in a Python-restored workspace would
report drift under the Go build — and it would have surfaced first as a mysterious stage 14
failure. `TestChecksumMatchesThePythonImplementation` compares against a digest computed by
`RestoreService.calculate_sha256` itself.

**A tension between two criteria, recorded rather than papered over.** Phase 8 requires "re-running
it is a no-op", while phase 6 requires a non-interactive `conflict_strategy: prompt` to be a hard
error. Since `prompt` is the default and conflict resolution reads the filesystem alone
(docs/04 §5), a second `rv restore` over rv's *own* output errors unless the asset sets an
explicit strategy. The Python implementation behaves identically. Idempotency is tested with
explicit strategies, and `TestRerunWithThePromptDefaultErrors` pins the default behavior so it is
visible. **Worth a spec decision before stage 14**, whose interop gate says a Python-restored
workspace must see "restore is a no-op" — that gate will need explicit strategies in its fixture,
or the spec needs conflict resolution to consult the lockfile. Making rv treat a target it
already owns as non-conflicting would be a real improvement, but it is a feature the spec never
asked for, so it is not built.

Two seams added for later stages rather than features: `Restorer.Plugins` (nil until stage 12)
and `Restorer.Prune` (nil until stage 11). Both have their call sites and orderings in place —
`pre-restore` after the snapshot, pruning after success and non-fatal — because those orderings
are stage 8 criteria and testing them with a fake now is cheaper than retrofitting them later.

---

### Stage 6-7 completion notes (2026-08-19)

Stages 6 and 7 are done. Coverage on `internal/` is **92.9%**. Packages added: `engine`
(`handler.go`, `template.go`, `words.go`) and `providers`.

Four design decisions worth recording:

1. **`PlanAsset` returns operations rather than appending to a shared transaction.** docs/04 §5
   requires each parallel worker to plan into its own scratch context and the caller to merge in
   item order; returning a `Plan` value makes that structural instead of a convention stage 8 has
   to remember.
2. **The write operation carries the mode *and* a separate `chmod` operation is planned.** The
   mode on the write is what stops a secret from existing world-readable for even an instant
   (`AtomicWrite` sets it before the content lands); the `chmod` operation is what applies `owner`
   and what makes the planned sequence the `delete → pre → write → chmod → post` the criteria fix.
3. **Shell word splitting is written, not imported.** docs/09 §2 says anything not on the
   dependency list is written; `splitWords` handles single quotes, double quotes with the four
   escapes, bare backslashes, and errors on an unbalanced quote — which is the behavior the
   "malformed quote fails at plan time" criterion actually needs.
4. **One `base` type parameterized by binaries, an installed-check and install commands.** Adding a
   provider is one entry in `catalogue.go` plus one line in the registry, rather than the
   reference's four parallel if/elif chains where forgetting the fourth silently drops packages.

Two notes on behavior:

- **The retry is still blind**, matching the reference: three attempts, 2 s then 4 s. docs/06 §1
  flags classification (fail fast on not-found and permission-denied) as the follow-up, and the
  code says so at the call site.
- **`internal/platform` is not built yet.** docs/06 §6 specifies it, but nothing in stage 7 needs
  it — providers take a `Runner`, which is the better seam. `doctor` builds it in stage 10.

Deliberately **not** built (no added features): the engine's 14-step driver and parallel planning
(stage 8), plugin hook dispatch (stage 12), and `brew bundle` support, which the reference has but
neither `docs/06` nor the manifest schema mentions.

---

### Stage 4-5 completion notes (2026-08-19)

Stages 4 and 5 are done, with both interop gates green. Coverage on `internal/` is **91.1%**.
Packages added: `crypto`, `scrub`, `permissions`, `transaction`; `logging` now wraps every writer
in the scrubber, so there is no unscrubbed output path.

**Interop gate 4 (crypto), both directions, against the real Python implementation.**
`internal/crypto/testdata/interop/` holds fixtures produced by `reference/`'s own `AgeEncryptor`;
`TestInteropGoToPython` drives that same class in a subprocess to decrypt a Go-encrypted file. It
fails rather than skips when the reference cannot be imported — a skipped gate is an unmet
criterion wearing a green tick.

**Interop gate 5 (journal).** `internal/transaction/testdata/python_journal/` is a real journal and
backup tree written by `reference/`'s `TransactionContext`, left in the `executing` state like a
killed run. Only the absolute path prefix was replaced with `{{ROOT}}` so the test can relocate it;
the field names, explicit nulls, `0o640` permission spelling and `SYMLINK:<target>` backup format
are Python's own output. Regenerating it needs `pydantic`; the committed fixture means CI does not.

Three notes where the spec was applied with judgement:

1. **The PEM scrubbing pattern in `docs/05` §2 is wrong and was corrected.** As written —
   `-----BEGIN\s+(?:RSA|OPENSSH|PRIVATE)\s+KEY-----` — it cannot match `BEGIN RSA PRIVATE KEY` or
   `BEGIN OPENSSH PRIVATE KEY`, the two commonest real headers, because it requires `KEY` to follow
   the algorithm directly. The acceptance criterion ("the scrubber redacts a PEM block") outranks
   the pattern listing, so the algorithm segment is optional in the Go pattern. **This is a spec
   bug worth fixing in `docs/05`.**
2. **Journal permissions are `0o644`, manifest permissions are `0644`.** Both spellings are part of
   the compatibility contract — Python's `oct()` produces the former — so `permissions.Parse`
   accepts both while `manifest` keeps rejecting `0o644` in a manifest.
3. **`permissions` never chmods or chowns through a symlink**, using `Lstat` and `Lchown`. The
   reference used plain `chmod`/`chown`, which change the mode of whatever the link points at
   rather than the link rv just created.

Deliberately **not** built (no added features): plugin hook dispatch (stage 12), the JSON audit
file handler (stage 8 wires it; `logging` already scrubs the console), and `rv secret`'s command
bodies (stage 9). Hook *execution* is in the transaction because `docs/04` §2 phase 4 places it
there, not because stage 6 needed it early.

---

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
