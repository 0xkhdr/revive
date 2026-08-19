# Revive documentation

Use this index to enter the documentation at the right level.

## Using Revive

- [Operations](Operations.md) — installation, daily workflows, secrets, recovery, plugins, and CI.
- [Manifest](Manifest.md) — schema, assets, templates, secrets, packages, profiles, and overrides.
- [Migration](Migration.md) — move from the Python implementation to the Go binary.

## Developing Revive

- [Architecture](Architecture.md) — runtime flow, package boundaries, persistent state, and trust model.
- [Development](Development.md) — local checks, compatibility rules, change conventions, and review checklist.

## Source of truth

The Go code and tests define current behavior. The archived implementation under `reference/` is
used only for compatibility evidence. If documentation and executable behavior differ, update the
documentation with the code change that establishes the intended behavior.
