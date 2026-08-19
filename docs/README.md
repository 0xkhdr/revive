# Revive documentation

Revive is a declarative, transactional machine-configuration tool. Start with the page that
matches what you are doing.

## Use Revive

- [Operations](Operations.md): install, restore, back up, manage secrets, recover, and automate.
- [Manifest reference](Manifest.md): configure assets, templates, packages, profiles, and machines.

## Develop Revive

- [Architecture](Architecture.md): understand runtime flow, package ownership, state, and security.
- [Development](Development.md): build, test, change, and review the codebase.

## Suggested reading order

New users: **Operations → Manifest**. New contributors: **Architecture → Development**, then use
the Manifest reference when changing configuration behavior.

The CLI remains the authoritative command reference: run `rv --help` or `rv <command> --help`.
