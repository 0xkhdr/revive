# Manifest reference

Revive strictly decodes YAML: unknown fields and wrong types fail before any mutation. Schema
versions `1` and `2` are supported; omitted `version` defaults to `2`. Prefer version `2` for new
workspaces.

```yaml
version: 2

assets:
  - id: zshrc
    type: symlink
    source: assets/zshrc
    target: ${HOME}/.zshrc
    permissions: "0644"
    conflict_strategy: prompt

  - id: gitconfig
    type: template
    source: assets/gitconfig.tmpl
    target: ${HOME}/.gitconfig
    permissions: "0644"
    template_vars:
      email: dev@example.com
    hooks:
      post:
        - command: git config --global core.autocrlf input

secrets:
  - id: app_env
    source: secrets/app.env.age
    target: ${APP_DIR}/.env
    permissions: "0600"

packages:
  apt: [git, zsh, ripgrep]
  docker:
    images: [postgres:16]
  node:
    version_file: .nvmrc

profiles:
  base:
    assets: [zshrc, gitconfig]
    packages: [apt]
  work:
    extends: [base]
    secrets: [app_env]
    packages: [docker, node]

machine_overrides:
  enabled: true
  path: machine/{hostname}.yaml

backup_retention:
  max_count: 10
  max_age_days: 30
```

## Assets

| Field | Required | Values/default |
|---|---|---|
| `id` | yes | Unique across assets and secrets. |
| `type` | no | `symlink` (default), `copy`, `template`, or `secret`. |
| `source` | yes | Repository-relative path; absolute paths and `..` escapes are rejected. |
| `target` | yes | String or string list. `${VAR}` and `${VAR:-default}` are supported. |
| `permissions` | no | Quoted four-digit octal, such as `"0644"`. |
| `owner` | no | Unix user name. Applying it may require elevated privileges. |
| `conflict_strategy` | no | `prompt` (default), `overwrite`, `skip`, or `abort`. |
| `encrypted` | no | Boolean; forced to `true` for type `secret`. Prefer the `secrets` section. |
| `template_vars` | no | Template context values; override environment and built-ins. |
| `hooks.pre`, `hooks.post` | no | Lists of `{command: "..."}` entries. |

Conflict behavior applies when a target already exists. With `prompt`, an unchanged target that
the current lockfile proves rv created is safely reapplied without prompting. In headless or
non-interactive use, an unresolved `prompt` conflict is an error. Set an explicit strategy in CI.

A directory source with multiple targets selects a child whose basename matches each target. For
encrypted directory sources, `<target-basename>.age` is tried first. If no child matches, the
directory itself is used.

Per-asset hooks execute around each target inside the transaction and receive `RV_ASSET_ID`,
`RV_ASSET_TARGET`, `RV_TX_ID`, and `RV_HOOK_STAGE`. They time out after 30 seconds. Hooks do not run
for skipped assets or dry runs. Plugin references are not supported at asset level.

## Secrets

Secrets accept `id`, `source`, `target`, `permissions`, and `owner`. Type and encryption are
forced. Permissions default to `"0600"` and must not grant group or world access. Commit only the
age ciphertext; keep identities outside the repository.

Use a source file for one target. For several targets, use a source directory containing one
`<target-basename>.age` file per target:

```yaml
secrets:
  - id: app_files
    source: secrets/app
    target:
      - ${APP_DIR}/.env
      - ${APP_DIR}/credentials.json
```

## Templates

Templates use Go `text/template` with `missingkey=error`. Context precedence, last value winning:
process environment, built-ins, then `template_vars`.

Built-ins: `._hostname`, `._user`, `._platform`, `._arch`, `._home`, `._repo_dir`.

Helpers: `upper`, `lower`, `trim`, `replace`, `join`, `default`, and `env`, plus standard
`text/template` built-ins.

```gotemplate
[user]
name = {{ ._user }}
email = {{ .email }}
editor = {{ default "vim" .EDITOR }}

{{ if eq ._platform "darwin" }}
[credential]
helper = osxkeychain
{{ end }}
```

Run `rv doctor` after editing templates; it reports syntax that Go `text/template` cannot execute.

## Packages and profiles

Flat package groups: `brew`, `apt`, `flatpak`, `snap`, `pacman`, `dnf`, `nix`, `cargo`, and `pip`.
Structured groups are `docker.images` and `node.version`/`node.version_file`; explicit Node
`version` wins. Profiles reference group names, not individual package names.

Profiles may reference global asset/secret IDs or contain inline definitions. Parents resolve
before children; a child replaces an inherited item with the same ID. Multiple requested profiles
merge in command-line order. Package entries are deduplicated while preserving order. Cycles and
unknown references are errors.

## Machine overrides

Overrides default to `machine/{hostname}.yaml`; a missing file is normal. The file may contain only
`assets`, `secrets`, and `packages`. Assets and secrets replace resolved items by ID. Package lists
append, while Node settings overwrite.

Use overrides for genuine host differences. Keep portable values in `.env` and profiles; this
keeps the shared manifest reviewable.

## Retention

`backup_retention.max_count` and `max_age_days` default to `10` and `30`; both must be at least `1`.
They control old snapshot pruning, never active recovery journals.
