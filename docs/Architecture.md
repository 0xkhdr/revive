# Architecture

Revive (`rv`) applies a repository-declared developer environment to a Unix machine. The Go
implementation is a single binary. Its supported public surfaces are the CLI, manifest formats
v1/v2, generated lockfiles, transaction journals, encrypted assets, and the runtime layout under
the user's XDG directories. Packages under `internal/` are not a Go library API.

The archived Python implementation under `reference/` is a compatibility oracle. Use its fixtures
and observable behavior when checking interoperability. Do not copy its internal structure or
assume its documentation describes the current Go implementation.

## Runtime flow

`rv restore` follows one ordered transaction:

1. Acquire the process lock and reject unrecovered transactions.
2. Strictly load the manifest, resolve profile inheritance, then apply the machine override.
3. Resolve the age identity, plan assets, decrypt secrets, render templates, and resolve conflicts.
4. Validate commands and targets; snapshot existing targets and persist the journal.
5. Run `pre-restore` plugins.
6. Execute each target: delete, pre-hook, atomic write/symlink, permissions/owner, post-hook.
7. Install missing packages in deterministic provider order.
8. Run `post-restore` plugins and verify target existence and permissions.
9. Atomically write the lockfile, commit the journal, then optionally prune old snapshots.

Planning is read-only and may run concurrently. `--dry-run` stops before snapshots and hooks.
`--preview` uses the drift engine instead of the restore planner.

Rollback restores filesystem state from the journal snapshot. It cannot undo side effects from a
hook, plugin, package manager, service restart, network request, or external process. Such actions
must be idempotent and safe when the surrounding file transaction later rolls back.

## Package ownership

| Package | Responsibility |
|---|---|
| `cmd/rv` | Build metadata and process exit. |
| `internal/cli` | Cobra commands, flags, rendering, prompting, exit codes. |
| `internal/manifest`, `internal/profile` | Strict schema loading, validation, inheritance, host overrides. |
| `internal/engine` | Restore/backup orchestration, asset planning, templates and conflicts. |
| `internal/transaction`, `internal/recovery` | Locking, atomic mutation, journals, rollback and pruning. |
| `internal/status`, `internal/doctor` | Read-only drift, diff and diagnostics. |
| `internal/crypto`, `internal/scrub`, `internal/permissions` | age operations, redaction and POSIX modes. |
| `internal/providers` | Package-manager adapters and installation cache. |
| `internal/plugins` | Plugin discovery, protocol, timeout and process-group cleanup. |
| `internal/paths`, `internal/platform`, `internal/workspace` | Runtime paths, host detection and workspace registry. |
| `internal/lockfile`, `internal/logging`, `internal/watcher` | Confirmed state, audit output and change watching. |

The CLI owns presentation only. Domain behavior belongs in its responsible package and receives
I/O, clocks, paths, commands, or prompts through explicit seams so tests never touch the real
machine.

## Persistent state

XDG variables are honored only when absolute.

| Path | Purpose |
|---|---|
| `~/.config/rv/rv.lock` | Cross-process restore lock. |
| `~/.config/rv/journals/` | Transaction recovery records. |
| `~/.config/rv/backups/` | Pre-mutation snapshots. |
| `~/.config/rv/identity.txt` | Default age identity. |
| `~/.config/rv/package-cache.json` | Package presence cache. |
| `~/.config/rv/workspaces.yaml` | Registered workspaces. |
| `~/.config/rv/plugins/` | User-global plugins. |
| `~/.local/share/rv/audit.log` | Scrubbed JSON audit log, mode `0600`. |

Each manifest gets a sibling lockfile: `manifest.yaml` becomes `manifest.lock`; custom manifest
names receive matching custom lockfiles. Lockfiles and journals remain compatible with the Python
implementation.

## Security and trust boundaries

- Manifest and hook content is trusted, reviewed input. Hook commands are word-split and executed
  directly, without an implicit shell. Use `sh -c` explicitly only when shell syntax is required.
- Asset sources must be relative and remain inside the repository. Machine override paths receive
  the same traversal protection. Missing environment variables are hard errors.
- Secrets are age-encrypted at rest, decrypted in memory, written atomically with restrictive
  permissions, and zeroed on best effort after use. Secret modes may not grant group/world access.
- Logs and errors pass through the credential scrubber. Avoid printing plaintext secrets anyway;
  redaction is defense in depth.
- Plugins are trusted executable code. Timeouts, process-group termination, proxy variables, and
  permission environment variables are guardrails, not kernel confinement. `allowed_paths` is
  advisory and disabling `network` does not block raw sockets. Review plugins like any executable.
- Package providers never add `sudo`. Run with only the privileges required by the selected
  package manager and targets.

For Python-to-Go compatibility changes, see [Migration.md](Migration.md).
