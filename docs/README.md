# Revive (Go Rebuild) — Build Specification

This directory is the **complete specification** for rebuilding Revive (`rv`) from scratch in Go.

The original Python implementation lives in [`../reference/`](../reference/). It is the **behavioral
oracle**, not a porting target. Read it to resolve ambiguity; do not transliterate it.

## Who these docs are for

A coding agent (or human) starting from an empty Go module. Every document is written so that
implementing it produces the described behavior without needing to read the Python source.

## Reading order

| # | Document | What it defines |
|---|----------|-----------------|
| 01 | [Overview](01-overview.md) | What `rv` is, the problem, goals, non-goals, rebuild scope |
| 02 | [Domain Model](02-domain-model.md) | Manifest, Asset, Secret, Profile, Lockfile, Journal — the data |
| 03 | [CLI Specification](03-cli-spec.md) | Every command, flag, output, exit code |
| 04 | [Restore Engine](04-restore-engine.md) | The 14-step apply order and the 7-phase transaction |
| 05 | [Security Model](05-security.md) | age encryption, secret scrubbing, permissions, memory hygiene |
| 06 | [Package Providers](06-providers.md) | Provider interface, per-manager behavior, idempotency cache |
| 07 | [Plugins & Hooks](07-plugins-hooks.md) | Plugin manifest, sandbox, hook stages |
| 08 | [Drift, Doctor, Recovery](08-drift-doctor-recovery.md) | status, diff, doctor, recover, prune |
| 09 | [Go Architecture](09-go-architecture.md) | Package layout, dependency choices, Python→Go mapping |
| 10 | [Build Plan](10-build-plan.md) | Ordered phases with acceptance criteria |
| 11 | [Testing & Quality](11-testing-quality.md) | Test strategy, coverage gates, CI |

## Conventions used in these docs

- **MUST / MUST NOT / SHOULD** carry RFC 2119 weight. A MUST is an acceptance criterion.
- Code blocks labeled `go` are **illustrative signatures**, not finished code.
- Paths written `~/.config/rv/...` are literal runtime paths and MUST be preserved — the Go build
  is expected to be drop-in compatible with an existing Python-created `~/.config/rv`.
- Anything marked **[DIVERGE]** is a deliberate improvement over the Python behavior.

## Compatibility contract

The Go rebuild MUST be able to take over a machine that previously ran the Python `rv`:

- Read and write the same `manifest.yaml` (schema v1 and v2).
- Read and write the same `manifest.lock` JSON.
- Read existing transaction journals in `~/.config/rv/journals/` and roll them back.
- Read the same age-encrypted `.age` files with the same identity file.
- Read the same `~/.config/rv/workspaces.yaml`.
