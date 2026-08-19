# Revive (`rv`)

Revive keeps a Linux or macOS development machine reproducible from one Git repository. It
applies files, templates, age-encrypted secrets, and packages declared in `manifest.yaml`; every
restore is planned, snapshotted, and verified, with managed files rolled back on failure.

```console
$ curl -fsSL https://raw.githubusercontent.com/0xkhdr/revive/main/install.sh | sh
$ mkdir dotfiles && cd dotfiles && git init
$ rv init
$ $EDITOR manifest.yaml
$ rv doctor
$ rv restore base --dry-run
$ rv restore base
```

This installs `rv` to `~/.local/bin`. Set `INSTALL_DIR` to choose another location, or install
from source with `go install github.com/0xkhdr/revive/cmd/rv@latest`.

Use the [documentation index](docs/README.md) to find operations, manifest, architecture, and
development guides. The CLI is the authoritative command reference: run `rv --help` or
`rv <command> --help`.

## License

[MIT](LICENSE)
