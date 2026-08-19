# 08 — Drift, Doctor, and Recovery

Three read-mostly subsystems: **drift detection** (`status`, `diff`, `restore --preview`),
**diagnostics** (`doctor`), and **crash recovery** (`recover`, `prune`).

---

## 1. Drift detection

Drift = the machine no longer matches what the manifest declares. Detecting it is what makes `rv`
safe to re-run.

### Status values

| Status | Meaning |
|--------|---------|
| `in_sync` | Target matches the declaration in every checked dimension |
| `missing` | Target does not exist on the filesystem |
| `type_mismatch` | A symlink was declared but a regular file exists (or vice versa) |
| `permissions_drifted` | Mode differs from the declared (or defaulted) mode |
| `modified` | Content differs from the repo source |
| `error` | The check itself failed — bad interpolation, unreadable file |

The overall result is `drifted: true` if **any** asset is not `in_sync`.

### Per-asset algorithm

```
for each target of the asset:
    absTarget := canonicalize(interpolate(target))       # failure → status "error"
    absSource := repoDir/asset.source
    if absSource is a directory:                          # directory fan-out
        absSource = absSource/<basename(absTarget)> if it exists

    if !exists(absTarget) && !isSymlink(absTarget): return "missing"

    if asset.type == symlink:
        if !isSymlink(absTarget): return "type_mismatch"
        resolve the link (relative links resolve against the link's own directory)
        if it does not point at absSource: return "modified"
    else:
        if isSymlink(absTarget): return "type_mismatch"
        expected := asset.permissions, defaulting to 0600 for secrets, 0644 otherwise
        if actual mode != expected: return "permissions_drifted"
        switch asset.type:
            copy (plain):     compare SHA-256 of source and target
            copy (encrypted): see "encrypted comparison" below
            template:         re-render, compare the rendered SHA-256 against the target's
            secret:           encrypted comparison
        if content differs: return "modified"
return "in_sync"
```

Ordering matters: existence, then type, then permissions, then content. Reporting "modified" on a
file that is actually the wrong *type* would mislead the user into looking at a diff.

Directory sources compare by **mtime against the lockfile entry**, not by content hash — hashing a
large directory on every `status` call is the cost this avoids. **[DIVERGE]** mtime comparison is
weak (touch a file, no drift reported after a restore; restore a file with a preserved mtime, false
negative). Since the Go build already computes a deterministic sorted-walk directory hash for the
lockfile, use that hash for directory drift too and drop the mtime path.

### Encrypted comparison

1. If an identity is available: decrypt the source into a secure temp file, hash it, hash the
   target, compare.
2. If not, or decryption fails: fall back to comparing the target's mtime against the lockfile
   entry's recorded mtime, with a 1 ms tolerance.
3. No lockfile entry and no identity → report drift. Failing safe means "assume changed".

### Template comparison

Re-render the template with the current context and compare the rendered hash against the target's
hash. Note that this makes template drift **context-sensitive**: changing an environment variable
that the template reads shows up as drift, which is correct — the file no longer matches what the
declaration would produce today.

The lockfile's `rendered_checksums` records what was last written, which lets the user distinguish
"the template inputs changed" from "someone edited the target by hand".

### `rv diff`

For each `modified` asset, produce expected and actual text:

| Asset type | Expected content |
|------------|------------------|
| `copy` (plain) | The repo source file |
| `copy` (encrypted) | The decrypted source |
| `template` | The freshly rendered output |
| `secret` | The decrypted source |
| `symlink` | Skipped — nothing to diff |

Actual content is the file at the target. Skip when: the target does not exist, the target is a
symlink, or either side is binary. When an identity is missing for an encrypted asset, emit an
explanatory placeholder rather than an empty diff.

Rendering: side-by-side by default, unified with `-u`. Diff output MUST pass through the scrubber —
this is the single most likely place for a secret to reach a terminal.

### `rv restore --preview`

Runs the status engine and prints the drift table with no mutation. Distinct from `--dry-run`:
`--dry-run` exercises the restore *planner* (validating sources, decrypting, resolving conflicts),
while `--preview` reports the *current difference*. Use `--preview` to decide whether to restore,
`--dry-run` to check that a restore would succeed.

---

## 2. Doctor

`rv doctor` answers "is this workspace sane and is this machine capable of applying it".

### Checks, by category

| Category | Severity | Condition |
|----------|----------|-----------|
| `manifest` | critical | Manifest file missing at the expected path |
| `manifest` | critical | Manifest fails to parse or validate (message includes the reason) |
| `lockfile` | warning | Lockfile missing (never restored) or unparseable |
| `system` | warning | No package manager available for a provider the manifest uses |
| `profile` | critical | A named profile is not defined in the manifest |
| `profile_resolution` | critical | Resolution failed — cyclic `extends`, unknown asset/secret ID |
| `asset_source` | critical | An asset's source path does not exist in the repo |
| `asset_target` | warning | A target's parent directory does not exist |
| `asset_target` | warning | A target path fails interpolation (unset variable, no default) |
| `secret_source` | critical | A secret's encrypted source file is missing |
| `template_syntax` | critical | A `type: template` source contains Jinja2 syntax that `text/template` will not execute — see [09-go-architecture.md](09-go-architecture.md) §4.3 |
| `hook_command` | warning | An asset hook's `argv[0]` does not resolve on `PATH`, so the hook will fail mid-transaction |

The `template_syntax` check is what makes the templating change ([09](09-go-architecture.md) §4)
survivable: Jinja2 `{% … %}` tags are **not** a `text/template` parse error — they pass through as
literal text and land in the output file. Only the linter catches that, so it is critical severity
and it MUST report file, line, and the `text/template` equivalent.

Report shape:

```json
{
  "healthy": false,
  "checks_run": 12,
  "issues": [
    {"category": "asset_source", "severity": "critical",
     "message": "Asset 'zshrc' source not found: assets/zshrc"}
  ]
}
```

`healthy` is false when any issue is `critical`. **Exit code 1 when unhealthy** — this is what makes
`rv doctor` usable as a CI gate. `--json` prints the structure above verbatim; the default output is
a grouped, colored summary.

Doctor MUST be **read-only and non-destructive**. It is the command a confused user runs first, and
it must be safe to run on a broken machine.

---

## 3. Recovery

### The failure being handled

A restore is interrupted mid-execute: power loss, SIGKILL, an OOM kill. The journal on disk has a
status of `executing` or `verifying`, the backup directory is populated, and the filesystem is in a
partially mutated state.

### `rv recover`

1. Scan `~/.config/rv/journals/*.json`.
2. Parse each; a parse failure is a warning and a skip, not a fatal error.
3. Keep those whose status is neither `committed` nor `rolled_back`.
4. Sort newest first by timestamp.
5. No incomplete journals → print an all-clear, exit 0.
6. `--auto` → roll back the newest and exit. Otherwise, list them and prompt per journal.

**Rollback** replays the journal's entries in reverse (see [04-restore-engine.md](04-restore-engine.md) §3),
then deletes the journal and its backup directory.

**Discard** deletes the journal and backup directory **without** restoring anything. For the case
where the user has already fixed things by hand and just wants the warning to stop.

**[DIVERGE]** The Python version only recovers when the user runs `rv recover`. The Go build SHOULD
detect incomplete journals at the start of every `restore` and refuse to proceed until they are
resolved, with the error naming `rv recover`. Restoring on top of an unrecovered partial transaction
means the new snapshot captures the *broken* state as the "pre-state", which quietly destroys the
ability to get back to the original.

### Journal lifecycle

```
plan → (no journal yet)
snapshot → journal written, status "pending"
execute → status "executing"
verify → status "verifying"
commit → status "committed"
cleanup → journal file and backup directory deleted

on failure at any point:
  rollback → status "rolling_back" → "rolled_back", then journal and backups deleted
```

A journal file that exists at all therefore means "a transaction is in flight or ended badly". Its
presence is the signal.

---

## 4. Backup pruning

Backup snapshots accumulate; a large dotfile repo can produce hundreds of megabytes.

### Policy

From `backup_retention` in the manifest, or `rv prune` flags:
- `max_count` (default 10) — keep at most N snapshots, evicting oldest first.
- `max_age_days` (default 30) — delete anything older.

### Algorithm

```
activeTxIDs := tx IDs of all incomplete journals
entries := backup directories, each with tx_id, path, mtime, age_days, size_bytes,
           sorted OLDEST FIRST

candidates := entries where tx_id not in activeTxIDs and mtime < now - max_age_days
remaining  := entries not already candidates and not active
if len(remaining) > max_count:
    candidates += the oldest (len(remaining) - max_count) of remaining
deduplicate candidates by path
delete each (or report under dry-run)
```

**The active-transaction guard is the critical part.** Deleting the backup directory of an
in-flight or crashed transaction destroys its rollback ability permanently. Age is never a reason to
override that.

Pruning runs automatically after every successful restore, wrapped so that a pruning failure logs a
warning and **never** fails the restore — the restore already succeeded, and disk cleanup is not
worth reverting it for.

`rv prune` lists candidates, confirms (unless `--yes`), then deletes. `--dry-run` lists only.

---

## 5. Backup (system → repo)

`rv backup` is the reverse sync — pulling machine state back into the repo, for when a file was
edited in place and should be committed.

Per item in the resolved profile:

```
skip template assets entirely (a rendered file cannot be un-rendered) — warn
for each target:
    if the target does not exist: warn and skip
    if the target is a symlink pointing at the repo source: "already in sync", skip
    if the target is a symlink elsewhere: follow it and read the real file
    determine the repo destination:
        directory source, or list targets → <source>/<basename(target)> (+ ".age" if encrypted)
        otherwise → <source>
    if encrypted: re-encrypt with the public key derived from the identity
    else: copy into the repo
```

Machine overrides are merged before backup so machine-specific target paths resolve correctly.

`--dry-run` reports the planned items without writing.

**[DIVERGE]** Backup does **not** run inside a transaction in the Python version — it writes into
the repo directly, with no rollback. The repo is git-tracked so the user has a recovery path, but
the asymmetry is worth closing: the Go build SHOULD route backup writes through the same atomic-write
helper at minimum, so an interrupted backup cannot leave a truncated file in the repo.
