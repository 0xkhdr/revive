# Revive (`rv`)

Declarative, transactional, reversible developer-environment management.

A git repository declares files, secrets, and packages in a `manifest.yaml`. `rv restore <profile>`
makes the local machine match that declaration — atomically, with a rollback journal, on a fresh
machine or an existing one.

> **Status: rebuild in progress.**
> `rv` is being rewritten in Go. There is no Go code in this repository yet — only the
> specification for it. The previous Python implementation is archived in
> [`reference/`](reference/) and is no longer developed.

## Repository layout

| Path | Contents |
|------|----------|
| [`docs/`](docs/) | The build specification for the Go rewrite. Start at [`docs/README.md`](docs/README.md). |
| [`reference/`](reference/) | The archived Python implementation — the behavioral oracle for the rewrite, not a porting target. |
| [`reference/examples/`](reference/examples/) | Real-world example manifests. |
| `.github/` | Issue and PR templates, dependabot. Go CI arrives with Phase 0. |

## Building the Go version

Everything an implementer needs is in [`docs/`](docs/), written so that no reading of the Python
source is required:

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

Work proceeds through [the build plan](docs/10-build-plan.md) in order. A phase is done when its
acceptance criteria pass, not before.

## Compatibility contract

The Go build must be able to take over a machine that previously ran the Python `rv`: same
`manifest.yaml` (schema v1 and v2), same `manifest.lock`, same transaction journals, same
age-encrypted files, same `~/.config/rv` layout. Interop tests enforce this at Phases 4, 5, 8, and 14.

## License

[MIT](LICENSE)
