# Revive (`rv`)

Declarative, transactional, reversible developer-environment management.

A git repository declares files, secrets, and packages in a `manifest.yaml`. `rv restore <profile>`
makes the local machine match that declaration — atomically, with a rollback journal, on a fresh
machine or an existing one.

## Install

```console
$ go install github.com/0xkhdr/revive/cmd/rv@latest
```

Or download a release binary for `linux/amd64`, `linux/arm64`, `darwin/amd64` or `darwin/arm64`,
`chmod +x` it, and put it on your `PATH`. The Linux builds are fully static, so they run on any
distribution. There are no runtime dependencies.

**Upgrading from the Python `rv`?** Read [docs/MIGRATION.md](docs/MIGRATION.md). Your manifests,
lockfiles, encrypted secrets and journals all carry over unchanged; templates need rewriting, and
`rv doctor` finds every one of them.

## Quick start

```console
$ mkdir dotfiles && cd dotfiles && git init
$ rv init                              # scaffold the workspace
$ rv secret keygen -o ~/.config/rv/identity.txt
$ $EDITOR manifest.yaml                # declare your first asset
$ rv doctor                            # check it over
$ rv restore base --dry-run            # see what would happen
$ rv restore base                      # apply it
$ rv status -p base                    # what has drifted since
```

## Measured

Built with `-trimpath -ldflags "-s -w"`, `CGO_ENABLED=0`:

| Platform | Binary |
|----------|--------|
| linux/amd64 | 6.4 MB |
| linux/arm64 | 5.9 MB |
| darwin/amd64 | 6.5 MB |
| darwin/arm64 | 6.0 MB |

Startup, `rv --help`, median of 20 runs on the same machine: **4.7 ms**, against **665 ms** for
the Python implementation — roughly 140× faster. Shell completion pays that cost on every
keystroke, which is where it is most noticeable.

## Repository layout

| Path | Contents |
|------|----------|
| `cmd/rv/` | The binary. Thin: build the root command, map errors to exit codes. |
| `internal/` | Everything else. Nothing here is a public API. |
| [`docs/`](docs/) | The build specification. Start at [`docs/README.md`](docs/README.md). |
| [`docs/MIGRATION.md`](docs/MIGRATION.md) | Moving from the Python implementation. |
| [`reference/`](reference/) | The archived Python implementation — the behavioral oracle, not a porting target. |
| [`reference/examples/`](reference/examples/) | Real-world example manifests. |

## The specification

The Go build was written from these, so that no reading of the Python source is required:

1. [Overview](docs/01-overview.md) — what `rv` is, scope, and the deliberate divergences from Python
2. [Domain Model](docs/02-domain-model.md) — manifest schema, lockfile, journal formats
3. [CLI Specification](docs/03-cli-spec.md) — commands, flags, exit codes
4. [Restore Engine](docs/04-restore-engine.md) — the 14-step apply order and 7-phase transaction
5. [Security Model](docs/05-security.md) — age encryption, scrubbing, permissions, memory hygiene
6. [Package Providers](docs/06-providers.md) — the provider interface and eleven implementations
7. [Plugins & Hooks](docs/07-plugins-hooks.md) — extension points and sandbox limits
8. [Drift, Doctor, Recovery](docs/08-drift-doctor-recovery.md) — status, diff, doctor, recover, prune
9. [Go Architecture](docs/09-go-architecture.md) — package layout, dependencies, idiom mapping
10. [Build Plan](docs/10-build-plan.md) — 15 phases with acceptance criteria
11. [Testing & Quality](docs/11-testing-quality.md) — test strategy and coverage gates

[`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md) tracks what is built and records the decisions
taken along the way.

## Compatibility contract

The Go build takes over a machine that previously ran the Python `rv`: same `manifest.yaml`
(schema v1 and v2), same `manifest.lock`, same transaction journals, same age-encrypted files
under the same identity, same `~/.config/rv` layout.

Four interop gates enforce it, each against fixtures produced by the Python implementation itself:

| Gate | What it proves |
|------|----------------|
| `internal/crypto` | A Python-encrypted file decrypts here, and a Go-encrypted one decrypts there |
| `internal/transaction` | A journal from a crashed Python restore rolls back here, to the exact pre-state |
| `internal/lockfile` | A Python-written lockfile loads without loss and rewrites equivalently |
| `internal/interop` | A workspace restored by Python reports in-sync, restores as a no-op, and keeps its lockfile |

## Development

```console
$ go test ./...            # unit tests and interop gates
$ go test -race ./...      # the concurrency the watcher and scrubber rely on
$ golangci-lint run ./...
```

Coverage on `internal/` is held above 90% in CI.

## License

[MIT](LICENSE)
