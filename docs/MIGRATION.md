# Migrating from the Python `rv` to the Go build

The Go build is **drop-in compatible with your data**: the same `manifest.yaml`, the same
`manifest.lock`, the same `.age` files under the same identity, the same journals in
`~/.config/rv/`, the same `~/.config/rv/workspaces.yaml`. You do not need to re-encrypt anything,
re-run anything, or move any files.

Four things did change. Only the first needs work.

| Change | Effort |
|--------|--------|
| Templates use Go `text/template`, not Jinja2 | **Rewrite each template.** `rv doctor` finds them all |
| Hooks run inside the transaction | None, unless you relied on the old timing |
| `rv self-install` is gone | Install with `go install` or a release binary |
| Built-in plugins are gone, Windows is unsupported | None, unless you used them |

---

## 1. Templates: Jinja2 → `text/template`

This is the only breaking change that needs editing, and it is mechanical.

**Start here:**

```console
$ rv doctor
```

Every Jinja2 construct is reported as a `template_syntax` issue at critical severity, naming the
file, the line, and the replacement. `rv doctor` exits 1 while any remain, so it works as a
migration checklist and as a CI gate afterwards.

### Why this is a hard error and not a compatibility shim

`text/template` does **not** fail on Jinja2 statement tags. It treats `{% if x %}` as ordinary
text and copies it straight into the file it writes. A silent migration would have produced
config files containing the literal string `{% if x %}` — valid-looking output that breaks
whatever reads it, at a time and place far from the cause. Hence a linter, at critical severity,
rather than a best-effort translator: a partial translation that renders wrong output is worse
than an error telling you to rewrite eight lines.

### The conversion table

| Jinja2 | `text/template` |
|--------|-----------------|
| `{{ email }}` | `{{ .email }}` |
| `{{ _hostname }}` | `{{ ._hostname }}` |
| `{{ HOME }}` | `{{ .HOME }}` |
| `{% if x %}…{% endif %}` | `{{ if .x }}…{{ end }}` |
| `{% if x %}…{% else %}…{% endif %}` | `{{ if .x }}…{{ else }}…{{ end }}` |
| `{% elif y %}` | `{{ else if .y }}` |
| `{% for i in xs %}…{% endfor %}` | `{{ range .xs }}…{{ end }}` |
| `{{ x \| upper }}` | `{{ upper .x }}` or `{{ .x \| upper }}` |
| `{% set n = v %}` | `{{ $n := .v }}` |
| `{{ x if y else z }}` | `{{ if .y }}{{ .x }}{{ else }}{{ .z }}{{ end }}` |

**The leading dot is the rule that catches everyone.** The rendering context is a map, so every
variable is `.name`. A key that is not a valid Go identifier is reachable as
`{{ index . "weird-key" }}`.

### Worked example

Before:

```
[user]
    name = {{ _user }}
    email = {{ email }}

[core]
    editor = {{ EDITOR | default('vim') }}

{% if _platform == 'darwin' %}
[credential]
    helper = osxkeychain
{% endif %}
```

After:

```
[user]
    name = {{ ._user }}
    email = {{ .email }}

[core]
    editor = {{ .EDITOR | default "vim" }}

{{ if eq ._platform "darwin" }}
[credential]
    helper = osxkeychain
{{ end }}
```

### What you get

The built-in variables are unchanged: `._hostname`, `._user`, `._platform`, `._arch`, `._home`,
`._repo_dir`, plus every environment variable and your asset's `template_vars`.

The function map is deliberately small — every function added becomes part of your manifest's
public contract:

```
upper  lower  trim  replace  join  default  env
```

plus the `text/template` built-ins (`if`, `range`, `with`, `eq`, `index`, `printf`, …).

An undefined variable is still a hard error, exactly as Jinja2's `StrictUndefined` was. That
guarantee did not change.

### Checking your work

```console
$ rv doctor                       # no template_syntax issues left
$ rv restore base --dry-run       # renders every template, mutates nothing
$ rv diff -p base                 # shows what the new output would change
```

---

## 2. Hooks now run inside the transaction

Asset hooks used to run during *planning*. They now run during *execution*, interleaved around
their own asset:

```
delete old target → pre-hook → write target → chmod → post-hook
```

Three consequences:

- **`--dry-run` no longer runs hooks.** It lists them. Previously a dry run executed every hook,
  which was a bug.
- **Ordering is what you would expect.** Asset 5's pre-hook runs after asset 4's file is written,
  not before asset 1's.
- **A hook failure rolls back the file changes.** Hooks are inside the transaction boundary now.

The same applies to plugin `pre-restore` hooks, which run after the snapshot rather than before
it.

**What this asks of your hooks:** rollback restores files but cannot un-run a hook —
`systemctl restart` has no inverse. A hook may therefore run and then have its file rolled back
out from under it. Write hooks that are safe to run twice and safe to have run against a
rolled-back file. `nginx -t`, `mkdir -p` and `systemctl reload` are all fine. A hook that appends
a line, increments a counter, or sends a non-retractable notification is not.

Every hook that ran is recorded in the journal and printed in the rollback report, including by
`rv recover` days later.

---

## 3. Installation

`rv self-install` is gone. It existed only to write a shell wrapper around a virtualenv
interpreter, and a single static binary has no such problem.

```console
$ go install github.com/0xkhdr/revive/cmd/rv@latest
```

or download a release binary for your platform, `chmod +x`, and put it on your `PATH`.

`rv self-uninstall` remains, and still takes `--purge-config`.

---

## 4. Smaller removals

- **Built-in plugins are gone.** They were Python scripts loaded from the package directory, and
  shipping an interpreter to run them would defeat the point of a single binary. Your own plugins
  under `plugins/` and `~/.config/rv/plugins/` work unchanged, except that the context now arrives
  as JSON **on stdin** rather than as a base64 argv element:

  ```bash
  ctx=$(cat)                                # was: base64 -d <<< "$2"
  profile=$(echo "$ctx" | jq -r .profile_name)
  ```

- **Windows is not supported.** The Python version mapped POSIX modes onto Windows read-only
  attributes and skipped `chown` with a warning, which made "0600 enforced" untrue. Refusing to
  build is more honest than pretending.

- **`rv gui` is not in this release.** The CLI covers everything it did.

---

## Improvements you get for free

- **Startup is ~140× faster**: 4.7 ms versus 665 ms for `--help` on the same machine. Every
  command pays this, and shell completion pays it on every keystroke.
- **Ctrl-C works.** Every subprocess — package installs, hooks, plugins — takes a context and is
  cancelled properly.
- **Restore refuses to run on top of an unrecovered transaction**, instead of snapshotting the
  broken state as the new "pre-state" and destroying your way back. Run `rv recover` first; it
  tells you so by name.
- **Directory drift is detected by content**, not by modification time, so an edited file inside a
  managed directory is now reported.
- **A second `rv restore` is a no-op** even on the default `prompt` strategy: rv consults the
  lockfile and recognizes targets it wrote itself. A target *you* edited is still protected.
- **`rv status --json`** joins `rv doctor --json` for CI.

---

## Rolling back to the Python version

Nothing stops you. The data formats are unchanged in both directions, so the Python `rv` will read
a workspace the Go build has restored — with one exception: **templates you have converted will no
longer render under Jinja2.** Keep them in version control and you can move between the two
freely until you are ready to commit to the change.
