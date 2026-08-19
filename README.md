# Revive (`rv`)

Keep a developer machine reproducible from one Git repository.

Revive applies files, encrypted secrets, templates, and packages declared in `manifest.yaml`.
Every restore is planned, snapshotted, and verified; managed files roll back on failure.

## Why Revive

- Rebuild a workstation from reviewed configuration.
- Preview changes and detect drift before applying them.
- Protect secrets with age encryption and restrictive permissions.
- Recover interrupted or failed restores from transaction journals.
- Pull intentional machine changes back into the repository.

## Install

```console
$ go install github.com/0xkhdr/revive/cmd/rv@latest
```

Release binaries support Linux and macOS on amd64 and arm64. No runtime dependencies.

## Quick start

```console
$ mkdir dotfiles && cd dotfiles && git init
$ rv init
$ rv secret keygen -o ~/.config/rv/identity.txt
$ $EDITOR manifest.yaml
$ rv doctor
$ rv restore base --dry-run
$ rv restore base
```

## Daily use

```console
$ rv status -p base              # report drift
$ rv diff -p base                # inspect changed content
$ rv restore base --dry-run      # validate without mutation
$ rv restore base                # repository -> machine
$ rv backup base --dry-run       # preview machine -> repository
$ rv recover                     # recover an interrupted restore
```

Run `rv --help` for all commands and flags.

## Documentation

Start at the [documentation index](docs/README.md). Upgrading from Python? Read
[Migration](docs/Migration.md).

## License

[MIT](LICENSE)
