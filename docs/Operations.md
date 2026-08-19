# Operations

Run commands from the workspace root. `rv --help` and `rv <command> --help` are the authoritative
flag reference.

## Install

```console
$ curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/install.sh | sh
```

The installer verifies the release checksum and writes `rv` to `~/.local/bin`. Ensure that
directory is on `PATH`, or choose another location:

```console
$ curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/install.sh | INSTALL_DIR=/usr/local/bin sh
```

## Safe daily workflow

```console
$ rv doctor
$ rv status -p base
$ rv diff -p base
$ rv restore base --dry-run
$ rv restore base
```

`doctor` checks configuration and host prerequisites without mutation. `status` reports drift;
`diff` shows content changes. `restore --dry-run` validates the real plan but creates no snapshot,
runs no hook, and writes no lockfile. Review before a first restore or any overwrite strategy.

Use `rv backup base --dry-run` before pulling edited machine files into the repository. Template
assets are skipped because rendered output cannot be reversed into source. Review and commit the
result like any other repository change.

## Bootstrap and workspaces

```console
$ rv init
$ rv secret keygen -o ~/.config/rv/identity.txt
$ rv doctor
$ rv restore base --dry-run
$ rv restore base
```

For an existing repository:

```console
$ rv clone <repo-url> <directory> --restore base
```

`rv workspace add`, `list`, `remove`, and `sync` manage the user registry. `workspace remove`
unregisters a path; it never deletes the repository. `workspace sync` pulls then restores each
registered workspace, so use `--dry-run` and explicit profiles in unattended automation.

## Secrets

```console
$ rv secret keygen -o ~/.config/rv/identity.txt
$ rv secret encrypt .env -o secrets/app.env.age -r age1...
$ rv secret decrypt secrets/app.env.age -o /tmp/app.env -i ~/.config/rv/identity.txt
$ rv secret rotate secrets/app.env.age -i ~/.config/rv/identity.txt --new-recipient age1...
```

- Never commit the identity or plaintext.
- Store recipient public keys separately from private identities.
- Prefer normal restore over manual decrypt so plaintext receives manifest permissions and
  transactional handling.
- If manual decrypt is required, choose a restrictive destination, remove it promptly, and avoid
  terminals/logs. `rotate --from-plaintext` requires `--confirm` and wipes/deletes that input on
  success.
- Back up the identity securely. Losing every identity makes ciphertext unrecoverable.

Identity lookup order is the explicit `--identity`, then `~/.config/rv/identity.txt`,
`~/.config/rv/keys/identity.txt`, and `~/.config/rv/identifier.txt`.

## Recovery

A restore refuses to start while an incomplete journal exists. Do not delete journal or backup
files manually.

```console
$ rv recover
$ rv recover --auto
```

Rollback restores snapshotted files. Discard keeps current files and clears the recovery record;
choose it only after inspecting the state. The recovery report lists hooks that ran because their
external effects cannot be reversed.

Prune snapshots safely:

```console
$ rv prune --dry-run
$ rv prune --yes
```

Normal successful restores apply manifest retention automatically.

## Headless and CI use

`--headless` disables color and prompting. It never guesses an answer. Define conflict strategies
that are safe for the job and use structured output where available:

```console
$ rv --headless doctor --json
$ rv --headless status -p base --json
$ rv --headless restore base --dry-run --non-interactive
```

For a CI validation job, `doctor --json` plus `go test ./...` is usually enough. Do not run a real
restore in CI unless the runner is intentionally disposable and owns every declared target.

## Plugins

Plugins live in `<workspace>/plugins/<name>/` or `~/.config/rv/plugins/<name>/`. Workspace-local
names win. The entrypoint must exist inside its plugin directory and be directly executable.

```yaml
# plugins/notify/plugin.yaml
name: notify
version: 1.0.0
entrypoint: run.sh
timeout: 30
hooks: [post-restore]
permissions:
  network: false
  shell: false
  allowed_paths: []
```

The executable reads one JSON object from stdin:

```json
{
  "repo_dir": "/path/to/workspace",
  "profile_name": "base,work",
  "dry_run": false,
  "targets": ["/home/user/.zshrc"],
  "hook_type": "post-restore"
}
```

It may write `{"status":"success","message":"..."}` to stdout. Exit zero with other output is
also success; non-zero exit or timeout fails and rolls back the restore. Plugin permissions are
declarations and environment hints, not a sandbox. Review the executable and its dependencies.

## Troubleshooting order

1. Run `rv doctor -v`.
2. Run `rv status -p <profile>` and `rv diff -p <profile>`.
3. Run `rv restore <profile> --dry-run --sequential -v` to isolate planning failures.
4. If an interrupted transaction is reported, run `rv recover` before anything else.
5. Inspect the scrubbed audit log at `~/.local/share/rv/audit.log`.

Use `--force-packages` only when the package-presence cache is stale. Use `--no-plugins` to isolate
a plugin failure, not as a permanent workaround for an unreviewed plugin.
