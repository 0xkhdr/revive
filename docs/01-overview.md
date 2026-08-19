# 01 — Overview

## What Revive is

`rv` is a CLI that makes a developer machine's configuration **declarative, transactional, and
reversible**. A git repository (the *workspace*) declares files, secrets, and packages in a
`manifest.yaml`. Running `rv restore <profile>` makes the local machine match that declaration —
atomically, with a rollback journal, on a fresh machine or an existing one.

It is a dotfile manager that behaves like a package manager transaction, plus secret management,
plus package orchestration across nine package managers.

## The problem it solves

Setting up or repairing a developer machine is normally a pile of shell scripts that:
- are not idempotent (running twice breaks things),
- have no rollback (a failure halfway leaves a broken machine),
- leak secrets into git or into logs,
- cannot express "this machine is different" without forking the script,
- silently drift from the repo with no way to detect it.

`rv` addresses each of those with a specific mechanism, described in the rest of these docs.

## Core value propositions

| Property | Mechanism |
|----------|-----------|
| **Atomic** | Every mutation goes through a transaction with snapshot → execute → verify → commit. Any failure rolls back the whole run. |
| **Crash-safe** | A journal is written to disk before mutation. A crashed run is recoverable on the next launch via `rv recover`. |
| **Idempotent** | Re-running a restore on a synced machine is a no-op. Packages are checked before install, files are checksum-compared. |
| **Secret-safe** | Secrets live in the repo encrypted with `age`, decrypt to memory, are written with `0600`, and are scrubbed from every log line. |
| **Drift-aware** | `rv status` / `rv diff` report exactly what changed on the machine vs. the repo. |
| **Machine-aware** | `machine/<hostname>.yaml` overrides the manifest per host without forking it. |
| **Extensible** | Sandboxed plugins subscribe to lifecycle hooks. |

## The mental model

```
                 ┌──────────────────────────────────────────┐
   git repo      │  manifest.yaml    (declaration)          │
   (workspace)   │  assets/          (plaintext files)      │
                 │  secrets/         (age-encrypted files)  │
                 │  machine/<host>.yaml (per-host override) │
                 │  plugins/         (lifecycle hooks)      │
                 │  manifest.lock    (sync state, generated)│
                 └──────────────────────────────────────────┘
                          │  rv restore            ▲  rv backup
                          ▼  (repo → system)       │  (system → repo)
                 ┌──────────────────────────────────────────┐
   the machine   │  ~/.zshrc → symlink into repo            │
                 │  ~/.config/app/.env  (0600, decrypted)   │
                 │  apt/brew/… packages installed           │
                 └──────────────────────────────────────────┘
                          │  rv status / rv diff
                          ▼
                     drift report
```

Restore is **repo → system**. Backup is **system → repo**. They are the only two directions, and
they share the same profile resolution and the same path interpolation.

## Non-goals

- Not a configuration management system for fleets (no remote agents, no inventory, no SSH).
- Not a general provisioning tool for servers — it targets a single local machine, owned by one user.
- Not a secret *store* — it encrypts secrets at rest in your repo; it does not run a vault.
- Not a container image builder.

## Scope of the Go rebuild

### In scope (must exist at v1.0 of the Go build)

- All commands in [03-cli-spec.md](03-cli-spec.md) except `gui`.
- Manifest schema v1 and v2, byte-compatible with the Python implementation.
- The full 14-step restore engine and 7-phase transaction with rollback.
- age encryption/decryption, keygen, rotation.
- All nine package providers plus docker and node.
- Plugin discovery and sandboxed execution.
- Drift detection, doctor diagnostics, journal recovery, backup pruning.
- Single static binary, no runtime dependencies beyond optionally the `age` CLI.

### Deferred (post-v1.0)

- `rv gui` — the web dashboard. The Python version serves a `http.server` app with a token-auth
  JSON API. Rebuild it only after the CLI is complete; it is the least-used surface and the most
  code per unit of value.
- The `watch` daemon may ship in a later phase (see [10-build-plan.md](10-build-plan.md)).

### Explicit improvements over Python **[DIVERGE]**

These are intentional changes; do not treat matching Python here as a goal.

1. **Single static binary.** No Python, no venv, no `pip install`. This deletes the entire
   `self-install` / `self-uninstall` wrapper-script machinery — replace with a plain
   `go install` / release binary and keep `self-uninstall --purge-config` only for config cleanup.
2. **Native age.** Use `filippo.io/age` in-process. The Python version juggles a `pyrage` import
   with a fallback to shelling out to the `age` binary; the Go build MUST NOT shell out for crypto.
3. **Typed errors.** Python raises `RuntimeError`/`ValueError` with formatted strings and callers
   catch broadly. Go MUST define sentinel error values and wrap with `%w` so callers branch on
   `errors.Is`/`errors.As`, never on message text.
4. **Context propagation.** Every subprocess call, provider install, and plugin run MUST accept a
   `context.Context` so Ctrl-C cancels cleanly. Python has no equivalent.
5. **No memory-zeroing theater.** The Python `ZeroBuffer.zero_bytes` pokes at CPython internals to
   wipe immutable `bytes`. In Go, keep secret plaintext in `[]byte`, zero it with an explicit loop
   in a `defer`, and never convert it to `string`. Do not attempt anything more exotic.
6. **Structured logging.** Use `log/slog` with a JSON handler for the audit file and a text handler
   for the console, both wrapped by the scrubber. Drop the bespoke formatter classes.
7. **Templates use stdlib `text/template`, not Jinja2.** Syntax becomes `{{ .name }}` with
   `missingkey=error` for the same strict-undefined guarantee. **This breaks every existing user
   template**; the cost is accepted, and `rv doctor` gains a `template_syntax` check that detects
   leftover Jinja2 and names the replacement. Details in
   [09-go-architecture.md](09-go-architecture.md) §4.
8. **Hooks execute inside the transaction.** Python ran per-asset hooks and `pre-restore` during
   planning, so they fired under `--dry-run` and their side effects preceded the snapshot. Hooks are
   now planned operations executed in the mutation phase, interleaved around their own asset.
   `--dry-run` runs none. Rollback restores files exactly and **reports** which hooks already ran,
   since a hook has no inverse. Details in [04-restore-engine.md](04-restore-engine.md) §2–§3.

## Glossary

| Term | Meaning |
|------|---------|
| **Workspace** | A git repo containing `manifest.yaml`; the source of truth. |
| **Profile** | A named subset of assets/secrets/packages to apply (`base`, `work`, …). Profiles can extend other profiles. |
| **Asset** | A file or directory managed by `rv`: symlinked, copied, or rendered from a template. |
| **Secret** | An age-encrypted asset, always decrypted to a `0600` file at the target. |
| **Provider** | An adapter for one package manager (apt, brew, …). |
| **Transaction** | One restore run's set of filesystem mutations, with a journal and rollback. |
| **Journal** | On-disk record of a transaction's pre-mutation state, used for rollback. |
| **Lockfile** | `manifest.lock`; records source checksums and target state after a successful restore. |
| **Drift** | Divergence between what the manifest declares and what is on the machine. |
| **Identity** | An age private key (`AGE-SECRET-KEY-1…`), by default at `~/.config/rv/identity.txt`. |
| **Recipient** | An age public key (`age1…`) that a secret is encrypted to. |
