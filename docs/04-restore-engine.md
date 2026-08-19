# 04 — The Restore Engine

This is the heart of `rv`. Everything else is support. The engine is two nested state machines: a
**14-step apply order** at the service level, wrapping a **7-phase transaction** at the filesystem
level.

---

## 1. The 14-step apply order

Deterministic and total-ordered. Steps MUST run in this sequence; the ordering is the correctness
argument, not a stylistic choice.

| Step | Name | What happens |
|------|------|--------------|
| 0 | **Process lock** | Acquire an exclusive `flock` on `~/.config/rv/rv.lock`, **non-blocking**. Held for the whole run. |
| 1 | **Manifest validation** | Load YAML, pre-check the raw `version`, decode and validate the struct. |
| 2 | **Profile resolution** | Flatten `extends` chains with cycle detection into a `ResolvedProfile`. |
| 3 | **Machine override merge** | If enabled and `machine/<hostname>.yaml` exists, merge it over the resolved profile. |
| 4 | **Dependency validation** | Verify source paths exist, targets interpolate, conflicts resolve. Happens inside planning. |
| 5 | **Secret decryption** | Decrypt encrypted assets/secrets into memory. Also inside planning. |
| 6 | **Backup snapshot** | Validate the plan, then snapshot every target that already exists and write the journal. |
| 7 | **Symlinks** | Execute planned symlink operations. |
| 8 | **Copies** | Execute planned copy operations. |
| 9 | **Permissions** | Apply `chmod`/`chown` to each mutated target. Per-asset hooks execute here too, interleaved around their own asset's operations. |
| 10 | **Package orchestration** | Run every provider that has packages, in a fixed order. |
| 11 | **Plugin hooks** | Run `post-restore` plugin hooks. |
| 12 | **Post-apply verification** | Verify every target exists and its mode matches the plan. |
| 13 | **Lockfile update** | Recompute source checksums, target modes and mtimes; write `manifest.lock` atomically. |
| 14 | **Audit commit** | Mark the journal committed, write the audit record, clean up backups, prune per retention. |

Steps 7–9 are a single atomic `execute()` call on the transaction, not three separate passes. They
are numbered separately because they are conceptually distinct and the log messages reference them.

### Ordering rationale (do not reorder)

- **Lock before load.** Two concurrent restores writing the same targets is the failure this
  prevents. Non-blocking is deliberate: a second `rv` should fail fast with "another process holds
  the lock", not queue up behind a 10-minute `apt install`.
- **Resolve before override.** Overrides layer onto a resolved profile, not onto the raw manifest.
- **Decrypt before snapshot.** A missing identity or a corrupt `.age` file must fail while the
  filesystem is still untouched. Decryption failure after snapshotting would mean a pointless
  rollback.
- **Snapshot before execute.** Obviously — no rollback without a pre-state.
- **Packages after files.** Config files must be in place before a package's post-install script or
  a service restart reads them.
- **Verify after packages.** A package installation can overwrite a config file it owns. Verifying
  before package installation would miss that.
- **Lockfile after verify.** The lockfile is a record of a *confirmed* good state.
- **Hooks inside execute, not inside planning.** Planning must be free of side effects so that it
  can run in parallel, run under `--dry-run`, and be abandoned without consequence.

### Dry-run

`--dry-run` runs steps 0–5, then **returns before step 6**. Nothing is snapshotted, nothing is
mutated, no lockfile is written, and **no hook of any kind executes** — per-asset hooks and
`pre-restore`/`post-restore` plugin hooks alike are logged as "would run" and skipped. Providers
receive `dryRun=true` and report what they would install.

### Hook stages

| Stage | When it executes |
|-------|------------------|
| `pre-restore` (plugin) | Start of the execute phase, after snapshot, before the first mutation |
| per-asset `pre` | Inside execute, immediately before that asset's own operations |
| per-asset `post` | Inside execute, immediately after that asset's own operations |
| `post-restore` (plugin) | Step 11, after packages, before verification |

**All four stages run inside the transaction boundary. [DIVERGE]** The Python implementation ran
per-asset hooks during planning and `pre-restore` before the snapshot, which meant those hooks fired
during `--dry-run` and their side effects preceded any rollback coverage. Both are fixed here.

Any hook failure MUST propagate and trigger rollback. See [07-plugins-hooks.md](07-plugins-hooks.md).

### Parallel planning

With `--parallel` (default) and more than one item, assets are planned in a bounded worker pool
(Python caps at 8 workers; Go should use `min(runtime.NumCPU(), 8)` and `errgroup`).

Three invariants MUST hold:

1. **Each worker plans into its own scratch transaction context.** No shared mutable state.
2. **Results are merged on the calling goroutine in the original item order**, not in completion
   order. The planned-operation list feeds directly into execution order; nondeterministic order
   would make runs non-reproducible.
3. **The first error aborts the whole plan** and is returned wrapped with the offending asset ID.

Planning does I/O (stat, read, decrypt) but never mutates. That is why it is safe to parallelize.
Execution is strictly sequential.

### Package orchestration order (step 10)

Fixed: `brew, apt, flatpak, snap, pacman, dnf, nix, cargo, pip`, then docker images, then node.
Each provider is invoked only if its list is non-empty. A provider failure at this step triggers
**explicit rollback of the file transaction**, then returns an error. `--force-packages` invalidates
the entire package cache before this step.

---

## 2. The transaction (7 phases)

The filesystem-mutation state machine. One transaction per restore run.

```go
type Transaction struct {
    TxID              string
    Timestamp         time.Time
    Status            string
    Entries           []RollbackEntry
    Planned           []Operation
    RenderedChecksums map[string]string
    ExecutedHooks     []ExecutedHook   // side effects rollback cannot undo
    journalPath       string   // ~/.config/rv/journals/<tx>.json
    backupDir         string   // ~/.config/rv/backups/<tx>/
}
```

### Phase 1 — Plan

`Plan(op Operation)` appends an operation. **No I/O and no side effects** beyond making the target
absolute — this is what makes planning safe to parallelize, safe under `--dry-run`, and safe to
abandon.

Operation types: `copy`, `symlink`, `chmod`, `delete`, `hook`.

```go
type Operation struct {
    Type        string   // copy | symlink | chmod | delete | hook
    Target      string   // absolute path; for a hook, the asset target it belongs to
    Source      Source   // nil for chmod, delete, hook
    Permissions string
    Owner       *string
    Hook        *HookOp  // non-nil iff Type == "hook"
}

type HookOp struct {
    AssetID string
    Stage   string   // "pre" | "post"
    Command []string // already word-split at plan time; executed without a shell
}

type Source interface{ isSource() }
type SourcePath  struct{ Path string }
type SourceBytes struct{ Data []byte }   // zero after use — holds decrypted plaintext
```

`Source` is polymorphic: a **source path** for a file/directory copy, or a **byte slice** of literal
content for decrypted secrets and rendered templates. Model it as a sum type, not `any`.

Hook commands are **word-split and validated at plan time** so a malformed quote fails before
anything is snapshotted; only the `exec` happens in phase 4.

### Phase 2 — Validate

Before touching anything:
- Reject unknown operation types.
- If the target exists, it MUST be writable.
- If it does not exist, its parent directory (when it exists) MUST be writable.
- `hook` operations are skipped by the writability checks — they mutate nothing at their target.
  Verify instead that the command is non-empty and that its argv[0] resolves on `PATH`, so a typo
  fails here rather than halfway through the mutations.

Validation failures here mean nothing has been backed up yet, so the run simply aborts.

### Phase 3 — Snapshot

For each planned operation, in order, indexed by position. **`hook` operations are skipped** — there
is no pre-state to capture, and they contribute no `RollbackEntry`.

1. If the target exists: hash it (regular files only, not symlinks) into `checksum`, record its mode
   into `permissions`.
2. Back it up into `~/.config/rv/backups/<tx>/backup_<idx>_<basename>`:
   - **symlink** → write a text file containing `SYMLINK:<readlink result>`
   - **directory** → recursive copy preserving symlinks
   - **regular file** → copy preserving metadata
   A backup failure is fatal — abort before any mutation.
3. Append a `RollbackEntry` with `op` = `create` if the target did not exist, `modify` if it did,
   `delete` if the planned operation is a deletion.
4. Write the journal to disk **before** returning.

### Phase 4 — Execute

Status → `executing`, journal flushed. Then, for each operation in order:

- `mkdir -p` the target's parent.
- **copy, source is a directory:** remove the existing target, create a temp directory as a
  **sibling** of the target, recursive-copy into it, then `rename` it into place. Cleanup on failure.
- **copy, source is a file or bytes:** atomic write (below).
- **symlink:** unlink an existing target if present, then create the link pointing at the absolute
  source path.
- **chmod:** apply mode and optional owner.
- **delete:** unlink a file/symlink, or recursively remove a directory.
- **hook:** execute the pre-split argv **without a shell**, with a 30 s timeout and the four `RV_*`
  environment variables. Before running it, append the hook to `ExecutedHooks`; append **first**, so
  a hook that starts and then fails or times out is still recorded as having run. A non-zero exit or
  a timeout is an execute-phase error like any other.
- After a `copy` or `symlink`, if the operation carried permissions, apply them immediately.

**Any error in this phase triggers an immediate rollback**, and the returned error MUST say that
the rollback succeeded (or, if the rollback itself failed, say that loudly — see §4).

#### Atomic write

The only way content reaches a target:

```
1. mkdir -p dirname(target)
2. create a temp file in THE SAME DIRECTORY as the target (prefix ".rv_atomic_tmp_")
3. write content
4. flush + fsync
5. rename(temp, target)
6. on any error: unlink the temp file
```

The same-directory requirement is not cosmetic: `rename` is only atomic within a filesystem, and a
temp file in `/tmp` frequently lands on a different device. **[DIVERGE]** The Go build SHOULD also
`fsync` the parent directory after the rename, which the Python version omits; without it the rename
itself can be lost on power failure.

### Phase 5 — Verify

Status → `verifying`. For each planned operation:

- `delete` operations: the target MUST be gone — **unless a later operation in the same transaction
  recreates it** (the delete-then-write pattern). Check for a subsequent `copy`/`symlink` on the
  same target before failing.
- `hook` operations: nothing to verify. A hook that exited 0 is done; its effect is not `rv`'s to
  assert.
- All others: the target MUST exist (a dangling symlink still counts as existing — check the link
  itself, not what it points to).
- If the operation carried permissions, the actual mode MUST equal the expected mode.

Verification failure triggers rollback.

### Phase 6 — Commit

Status → `committed`, journal flushed. The transaction is now durable and irreversible.

### Phase 7 — Cleanup

Remove the backup directory. Remove the journal file (only when status is `committed`). Cleanup
failures are logged at debug level and never escalate — a leftover backup is harmless, and pruning
will collect it.

---

## 3. Rollback

Triggered by a failure in execute or verify, by a failed package/hook step, or manually by
`rv recover`.

```
status = "rolling_back"; flush journal
for entry in reverse(entries):
    switch entry.Op:
      case "create":            # target did not exist before → remove whatever is there now
          remove file/symlink/dir at entry.Target
      case "modify", "delete":  # restore from backup
          remove the current target
          if backup content starts with "SYMLINK:":
              recreate the symlink to the recorded link target
          elif backup is a directory:
              recursive copy back
          else:
              copy the file back
          if entry.Permissions != nil: re-apply the mode
status = "rolled_back"; flush journal
```

**Reverse order is mandatory.** A transaction that deletes `~/.zshrc` then symlinks it must undo the
symlink before restoring the original, or the restore writes into a path the symlink still occupies.

**A single entry's rollback failure MUST NOT abort the loop.** Log it at error level and continue —
partial recovery beats none. Track whether any entry failed; if so, the final status and the
user-facing message MUST say the rollback was incomplete and name the affected paths. This is the
one place where `rv` may leave a machine in a mixed state, and the user has to be told exactly which
files are involved.

### Hooks and rollback

Rollback restores **files**. It cannot un-run a hook — `systemctl restart nginx` has no inverse, and
inventing an "undo command" field would be a guess the user never made.

The contract is therefore: **files are restored exactly; executed hooks are reported, not reversed.**

```
Restore failed at asset "nginx_conf"; the transaction was rolled back.
All 7 files were restored to their previous state.

These hooks already ran and were NOT reversed:
  - nginx_conf (pre):  nginx -t
  - zshrc      (post): chsh -s /bin/zsh
Review them if they had lasting effects.
```

Rules:
- `ExecutedHooks` MUST be persisted in the journal alongside the rollback entries, so
  `rv recover` — which runs in a fresh process, possibly days later — can print the same report.
- The report is printed whenever a rollback occurs and at least one hook executed. It is **not** a
  warning to be suppressed; it is the user's only record of what survived.
- This is also the argument for keeping hooks small and idempotent, which the manifest documentation
  MUST state plainly: a hook should be safe to run twice and safe to have run against a
  rolled-back file.

Journal addition:

```go
type ExecutedHook struct {
    AssetID string   `json:"asset_id"`
    Stage   string   `json:"stage"`     // pre | post
    Command []string `json:"command"`
    Started float64  `json:"started"`   // unix seconds
    Result  string   `json:"result"`    // ok | failed | timeout
}
```

**[DIVERGE]** This field does not exist in the Python journal format. It is additive, so a
Python-written journal still parses (the field is simply absent), and a Go-written journal is
ignored by the Python reader. The compatibility contract holds in both directions.

---

## 4. Process lock

```go
// ~/.config/rv/rv.lock, exclusive, non-blocking by default
type ProcessLock struct{ path string; blocking bool }
```

- `flock(LOCK_EX | LOCK_NB)`. On `EWOULDBLOCK`, return a distinct `ErrLockHeld` whose message names
  the lock path and says another `rv` process holds it.
- Write the PID into the file after acquiring, for auditability.
- Release with `LOCK_UN` and close, always, on every exit path.
- The lock is advisory and process-scoped, which is exactly the scope needed: one machine, one user.

**Testing note:** tests MUST run serially (Python's suite deadlocks under `pytest -n`). In Go, mark
lock tests as non-parallel, or better, make the lock path injectable so each test gets its own file
in `t.TempDir()`. Prefer the injectable path — it removes the constraint instead of documenting it.

---

## 5. Asset planning

`AssetHandler` turns one asset into planned operations. Per asset:

```
absSource := join(repoDir, asset.Source)
if !exists(absSource) && !asset.Encrypted: error "source file not found"

for each target in asset.Targets():
    absTarget := canonicalize(interpolate(target))

    # directory-source fan-out: match the source file whose basename matches the target's
    targetSource := absSource
    if isDir(absSource):
        base := basename(absTarget)
        try absSource/<base>.age first when encrypted, then absSource/<base>

    # conflict resolution
    if exists(absTarget) || isSymlink(absTarget):
        switch asset.ConflictStrategy:
          skip:      continue to next target
          abort:     error
          prompt:    if interactive → ask; declining skips this target
                     if NOT interactive → ERROR (never silently skip)
          overwrite: fall through

    PLAN (do not run) each pre-hook as a "hook" operation, stage="pre"
    switch asset.Type:
      symlink:  detect symlink loops on the source; plan delete-if-exists; plan symlink
      copy:     plan delete-if-exists; decrypt if encrypted; plan copy
      template: plan delete-if-exists; render; record rendered SHA-256; plan copy of the bytes
      secret:   plan delete-if-exists; decrypt (identity required); plan copy with mode 0600
    PLAN (do not run) each post-hook as a "hook" operation, stage="post"
```

Hooks are **planned here and executed in phase 4**, never run during planning. Planning stays
side-effect free, which is what lets it run in parallel and lets `--dry-run` stop safely. Word-split
each hook command at plan time so a quoting error fails before the snapshot.

A skipped target contributes no operations at all — **including its hooks**. A `pre` hook attached
to a target that conflict resolution skipped MUST NOT run; the asset was not applied, so its hooks
have nothing to bracket.

Returns whether *any* target was planned; assets where every target was skipped are reported as
skipped and excluded from the lockfile update.

**Symlink loop detection**: walk the link chain from the source, tracking visited canonical paths;
a repeat is a loop and a hard error.

---

## 6. Lockfile update (step 13)

For each non-skipped asset and secret:
- Hash the repo source (file: streamed; directory: sorted walk).
- For each target that exists on disk: record the absolute path, the effective permissions (the
  asset's declared mode, or the actual mode if none was declared; `0600` for secrets), and the mtime.
- Write scalars for scalar-target assets and index-aligned arrays for list-target assets.
- Merge the transaction's `RenderedChecksums` into the lockfile.
- Write the whole lockfile with the atomic-write routine.

An existing unreadable lockfile is logged as a warning and replaced, never a fatal error.

---

## 7. Error handling contract

```go
var (
    ErrLockHeld              = errors.New("another rv process holds the lock")
    ErrUnsupportedSchemaVersion = errors.New("unsupported manifest schema version")
    ErrProfileNotFound       = errors.New("profile not defined in manifest")
    ErrCyclicInheritance     = errors.New("cyclic profile inheritance")
    ErrTargetConflict        = errors.New("target exists and conflict strategy forbids overwrite")
    ErrIdentityRequired      = errors.New("age identity required to decrypt")
    ErrRollbackIncomplete    = errors.New("rollback did not fully restore prior state")
)
```

Rules:
- Wrap with `%w` and add context at each layer (`"planning asset %q: %w"`).
- **Never branch on an error's message text.** Python does this in places; do not carry it over.
- Every error that reaches the user MUST answer: what failed, at which step, and what state the
  machine is in now.
