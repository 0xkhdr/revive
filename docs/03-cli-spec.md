# 03 — CLI Specification

The binary is `rv`. Every command runs against the **current working directory** as the workspace,
unless stated otherwise. There is no `--repo` flag; `cd` to the workspace.

## Global behavior

```
rv [--verbose|-v] [--headless] <command> [args] [flags]
```

| Flag | Effect |
|------|--------|
| `--verbose`, `-v` | Debug-level logging to console. |
| `--headless` | CI mode: plain-text stream logs, no colors, no boxes, no interactive prompts. |

Before any command body runs, the CLI MUST:
1. Initialize logging (console handler + JSON audit file handler, both scrubbed).
2. Load `.env` from the workspace root into the process environment (non-overwriting).

**Exit codes:**

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | User/config error: bad flags, missing manifest, unknown profile, validation failure, unhealthy `doctor` |
| 2 | Operation failure: restore/backup failed, transaction rolled back |

**Shell completion:** `rv` MUST provide completion for bash/zsh/fish. Profile-name arguments MUST
complete from the profiles in the current workspace's manifest, honoring a `-m/--manifest` flag if
it appears earlier on the command line.

**Common flags** shared by many commands:

| Flag | Meaning |
|------|---------|
| `--manifest`, `-m PATH` | Use a manifest other than `./manifest.yaml`. Relative paths resolve against the workspace. |
| `--identity`, `-i PATH` | age identity file. See identity resolution below. |
| `--profile`, `-p NAME` | Profile(s). Repeatable and comma-splittable: `-p base -p work` ≡ `-p base,work`. |
| `--dry-run` | Plan and validate; mutate nothing. |

**Identity resolution order** (used by every command that touches secrets):
1. `--identity` if given — MUST exist, else error.
2. `~/.config/rv/identity.txt`
3. `~/.config/rv/keys/identity.txt`
4. `~/.config/rv/identifier.txt`
5. If none found *and* the resolved profile contains encrypted assets or any secrets: fail with an
   actionable message naming the default path and the `--identity` flag. If nothing is encrypted,
   proceed with no identity.

---

## Command reference

### `rv init`

Scaffold a new workspace in the current directory.

- Fails (exit 1) if any of `manifest.yaml`, `manifest-build.yaml`, `manifest-restore.yaml` already exists.
- Creates `assets/`, `secrets/`, `machine/`, and an agent-skills directory.
- Writes a commented starter `manifest.yaml` (version 2, one example symlink asset, empty
  package lists, a `base` profile).
- Prints next steps.

### `rv clone <repo-url> [dest]`

```
rv clone <repo-url> [dest] [--restore|-r PROFILE] [--identity|-i PATH]
```

`git clone` the URL (default destination is the repo name in the cwd), register it as a workspace,
and optionally run `rv restore <profile>` inside it immediately. This is the fresh-machine
bootstrap path.

### `rv restore <profile...>`

The core command. Repo → system.

```
rv restore base,work
    [--identity|-i PATH]
    [--dry-run]
    [--preview]
    [--interactive | --non-interactive]      (default: interactive)
    [--no-plugins]
    [--prune]
    [--parallel | --sequential]              (default: parallel)
    [--force-packages]
    [--manifest|-m PATH]
```

| Flag | Behavior |
|------|----------|
| `--dry-run` | Runs steps 1–5 (load, resolve, override-merge, validate, plan, decrypt) then exits **before** any mutation. Prints the plan. **No hook of any kind executes** — asset hooks and plugin hooks are listed, not run. |
| `--preview` | Does **not** restore at all — runs drift analysis (the `status` engine) and prints what would change. Mutually exclusive with the actual run. |
| `--non-interactive` | Never prompt. A `conflict_strategy: prompt` asset in this mode is a **hard error**, not a silent skip — silent data loss is unacceptable. |
| `--no-plugins` | Skip all plugin hooks. |
| `--prune` | Prune old backup snapshots after a successful restore (this also happens automatically per `backup_retention`). |
| `--sequential` | Plan assets one at a time instead of in a worker pool. For debugging races. |
| `--force-packages` | Invalidate the whole package cache and re-query every provider before installing. |

Positional profiles accept repetition and commas: `rv restore base work` ≡ `rv restore base,work`.

On success prints the transaction ID, the profile(s), counts of assets/secrets/packages applied, and
the lockfile path. On failure prints the error and states that the transaction was rolled back;
exit 2.

### `rv backup <profile...>`

System → repo. The inverse of restore.

```
rv backup base [--identity|-i PATH] [--dry-run] [--manifest|-m PATH]
```

For each asset/secret in the resolved profile:
- Skips `template` assets entirely with a warning — a rendered file cannot be un-rendered.
- If the target is already a symlink into the repo, reports "already in sync" and skips.
- If the target is a symlink elsewhere, follows it and copies the real contents.
- For encrypted assets/secrets, re-encrypts the system file to the repo `.age` path using the
  public key derived from the identity.
- Multi-target assets with a directory source write back to `<source>/<basename of target>`
  (plus `.age` for encrypted).

Prints the list of items backed up (or planned, under `--dry-run`).

### `rv status --profile <name>`

```
rv status -p base [-i PATH] [-m PATH]
```

Compares the resolved profile against the filesystem and prints a table of per-asset status. Status
values and their meanings are specified in [08-drift-doctor-recovery.md](08-drift-doctor-recovery.md):
`in_sync`, `missing`, `modified`, `type_mismatch`, `permissions_drifted`, `error`.

### `rv diff --profile <name>`

```
rv diff -p base [-i PATH] [--unified|-u] [-m PATH]
```

For every asset reported `modified`, print a colored diff between the expected content (repo source,
decrypted or rendered as appropriate) and the actual file. Default rendering is side-by-side;
`--unified` switches to standard unified diff. Binary files and symlink assets are skipped.

### `rv doctor`

```
rv doctor [-p NAME] [--json] [-m PATH]
```

Runs health checks and prints a report grouped by category, each issue carrying a severity
(`critical`, `warning`, `info`). `--json` emits the structured report for CI. **Exits 1 if the
report is unhealthy** (any critical issue).

Check categories: `manifest`, `lockfile`, `system`, `profile`, `profile_resolution`,
`asset_source`, `asset_target`, `secret_source`. Details in
[08-drift-doctor-recovery.md](08-drift-doctor-recovery.md).

### `rv watch --profile <name>`

```
rv watch -p base [-i PATH] [--debounce|-d SECONDS] (default 5.0) [-m PATH]
```

Watch the workspace for filesystem changes and auto-run a restore after a debounce window. Ignores
`.git`. If the process lock is held when the timer fires, skip that trigger rather than queueing.
Handles SIGINT/SIGTERM for a clean shutdown.

### `rv recover`

```
rv recover [--auto]
```

Scan `~/.config/rv/journals/` for journals whose status is not `committed` and not `rolled_back`
(i.e. interrupted transactions), newest first.

- Interactive: list them and prompt per journal for **rollback** or **discard**.
- `--auto`: roll back the newest incomplete journal and exit. For CI and boot scripts.
- No incomplete journals: print an all-clear and exit 0.

**Rollback** restores every entry in reverse order from its backup. **Discard** deletes the journal
and its backup directory, leaving the mutated files in place.

### `rv prune`

```
rv prune [--max-count N] (10) [--max-age-days N] (30) [--dry-run] [--yes|-y]
```

Delete old transaction backup snapshots. Lists candidates first, then confirms unless `--yes`.
**MUST NOT delete a backup directory belonging to an incomplete journal**, regardless of age.

### `rv secret …`

```
rv secret keygen  [--output|-o PATH]
rv secret encrypt <file> --output|-o PATH --recipient|-r KEY [--recipient KEY …]
rv secret decrypt <file> --output|-o PATH --identity|-i PATH
rv secret rotate  <file> [--identity|-i PATH] --new-recipient|-nr KEY [--new-recipient KEY …]
                         [--from-plaintext PATH] [--confirm]
```

- **keygen** — generate an age keypair. With `--output`, write the identity file with a
  `# public key: age1…` comment line, the private key, and mode `0600`. Without `--output`, print
  both keys and warn that the private key is not stored.
- **encrypt** — encrypt a plaintext file to one or more age recipients. A recipient may be an
  `age1…` string or a path to a file containing one.
- **decrypt** — decrypt with an identity.
- **rotate** — re-encrypt an existing secret to new recipients. Normally decrypt-then-re-encrypt.
  With `--from-plaintext`, encrypt the given plaintext directly and then **securely wipe and delete
  that plaintext file** — this path requires `--confirm`.

### `rv workspace …`

```
rv workspace list
rv workspace add <path> [--name|-n NAME]
rv workspace remove <name>
rv workspace sync [-p PROFILE] [--dry-run] [-i PATH] [--force-packages] [--no-plugins] [-m PATH]
```

`sync` iterates every registered workspace, runs `git pull` then `rv restore` in each. **A failure
in one workspace MUST NOT stop the others.** Print a summary table (workspace, path, git result,
restore result, details) and exit 1 if any failed.

### `rv gui` — deferred

```
rv gui [--port|-p 8080] [--host|-h 127.0.0.1] [--no-browser] [--auth-token TOKEN] [-m PATH]
```

Web dashboard. **Not in the Go v1.0 scope.** If rebuilt later, preserve these security properties
from the Python version, which are non-negotiable:
- Binds to loopback by default; binding elsewhere without TLS requires an explicit
  `--i-understand-no-tls` acknowledgement flag.
- A bearer token is required on every `/api/*` request; auto-generated if not supplied, and
  disabling it requires passing an explicit empty value.
- CORS restricted to loopback unless a hidden dev flag is passed.

API surface, for reference when it is rebuilt: `GET/PUT/DELETE /api/workspace`,
`POST /api/workspace/register|switch`, `GET/POST /api/manifest`, `POST /api/asset/import`,
`POST /api/action/{status,diff,doctor,restore,keygen}`,
`POST /api/action/recovery/{list,rollback,discard}`.

### `rv self-uninstall`

```
rv self-uninstall [--force|-f] [--purge-config]
```

Remove the installed binary; with `--purge-config`, also remove `~/.config/rv`. **[DIVERGE]** The
Python `self-install` command exists only to write a shell wrapper around a venv interpreter — a
Go binary has no such problem. Do not port `self-install`; document `go install` and the release
binary instead.

---

## Output principles

The Python CLI leans heavily on boxed panels and tables. Keep the information, keep it terse:

1. **Machine-readable when asked.** `--json` on `doctor` today; extend the same treatment to
   `status` in the Go build so CI can consume it.
2. **`--headless` suppresses all decoration** and all prompts. Anything that would prompt becomes an
   error in headless mode.
3. **Never print a secret.** Every output path — logs, panels, error messages, diffs — goes through
   the scrubber ([05-security.md](05-security.md)).
4. **Errors state what failed and what state the system is in.** "Restore failed at step 10; the
   transaction was rolled back; your files are unchanged" is the required shape.
