# 07 — Plugins & Hooks

Two extension mechanisms, deliberately different in power:

| | Asset hooks | Plugins |
|---|---|---|
| Declared in | An asset's `hooks.pre` / `hooks.post` | A `plugin.yaml` in a plugin directory |
| Granularity | Per asset, per target | Per restore run |
| Execution | Direct subprocess, no shell | Sandboxed subprocess with declared permissions |
| Use for | `mkdir -p`, `systemctl restart` | Anything with logic |

---

## 1. Asset hooks

```yaml
assets:
  - id: nginx_conf
    type: copy
    source: assets/nginx.conf
    target: /etc/nginx/nginx.conf
    hooks:
      pre:
        - command: "nginx -t"
      post:
        - command: "systemctl reload nginx"
```

**Execution rules:**
- Split the command with shell-style word splitting **at plan time**, then exec **without a shell**
  at execute time. A malformed quote is a validation error naming the asset, raised before any
  mutation.
- Timeout: **30 seconds**. Exceeding it fails the asset.
- Non-zero exit fails the asset → fails the transaction → triggers rollback. Include up to 200
  characters of stderr in the error.
- Environment: inherited, plus `RV_ASSET_ID`, `RV_ASSET_TARGET`, `RV_TX_ID`, `RV_HOOK_STAGE`.

**Plugin references at asset level are rejected.** `- plugin: foo` inside `hooks` MUST return an
error telling the user to use profile-level `pre-restore`/`post-restore` hooks. This is deliberate:
silently ignoring the hook would break the guarantee that a failing hook rolls the transaction back.

### Hooks run inside the transaction **[DECIDED]** **[DIVERGE]**

The Python implementation ran per-asset hooks during *planning*. Three things were wrong with that,
and all three are fixed:

| Python behavior | Go behavior |
|---|---|
| Hooks fired during `--dry-run` | `--dry-run` logs hooks, runs none |
| Every asset's pre-hook ran before any file was written | Each hook runs immediately around its own asset's mutation |
| Pre-hook side effects preceded the snapshot, outside rollback coverage | Hooks execute inside the transaction boundary |

**Mechanism:** a hook is a planned operation of type `hook`, interleaved into the operation list in
its correct position. Planning registers it; phase 4 executes it. Full operation shape and journal
format in [04-restore-engine.md](04-restore-engine.md) §2.

Execution order for one asset becomes:

```
delete old target → pre-hook → write target → chmod → post-hook
```

**A skipped target runs no hooks.** If conflict resolution skips a target, that target contributes
no operations at all, hooks included — there is no mutation for them to bracket.

**What rollback can and cannot do.** Rollback restores files exactly. It cannot un-run a hook;
`systemctl restart` has no inverse, and inventing an undo-command field would be a guess the user
never made. So the contract is: **files restored, executed hooks reported**. Every hook that started
is recorded in the journal's `executed_hooks` and printed in the rollback report, including by
`rv recover` in a later process.

**Therefore: write hooks to be idempotent and side-effect-tolerant.** A hook may run and then have
its file rolled back out from under it. `nginx -t`, `mkdir -p`, and `systemctl reload` are all fine.
A hook that appends a line to a file, or increments a counter, or sends a non-retractable
notification is not — the manifest documentation MUST say so with these examples.

---

## 2. Plugins

### Layout

```
plugins/
  my_plugin/
    plugin.yaml
    main.py | main.sh | any executable
```

### `plugin.yaml`

```yaml
name: my_plugin          # string, required, unique
version: "1.0.0"         # string, required
entrypoint: main.py      # path relative to plugin.yaml, required
timeout: 30              # seconds; clamped to [1, 300]
hooks:                   # which stages this plugin subscribes to
  - pre-restore
  - post-restore
permissions:
  network: false         # allow outbound network
  shell: false           # allow subprocess execution
  allowed_paths: []      # directories the plugin may touch
```

### Discovery

Scanned in **precedence order**, first definition of a given `name` wins:

1. `<workspace>/plugins/` — workspace-local
2. `~/.config/rv/plugins/` — user-global
3. built-in plugins shipped with the binary

Each immediate subdirectory containing a readable `plugin.yaml` becomes a plugin. A malformed
`plugin.yaml` is skipped silently during discovery — one broken plugin MUST NOT prevent the others
from loading. **[DIVERGE]** Silent is too quiet: log the parse failure at warning level with the
path, then skip.

**[DIVERGE]** Built-in plugins in the Python version are Python scripts loaded from the package
directory. A Go binary has no interpreter to offer them. Either embed them with `embed.FS` and
extract to a cache directory before running, or drop built-ins for v1.0 and treat plugins as
strictly user-supplied. **Prefer dropping them** — the three Python built-ins are thin conveniences,
and shipping an interpreter dependency to run them undermines the single-binary goal.

### Hook stages

| Stage | When |
|-------|------|
| `pre-restore` | Start of the execute phase: after the snapshot, before the first mutation |
| `post-restore` | Step 11: after packages, before verification |

**[DIVERGE]** Python ran `pre-restore` before the snapshot and before the dry-run exit, so it fired
during `--dry-run` and its effects sat outside rollback coverage — the same defect as per-asset
hooks, fixed the same way. `pre-restore` now runs after the snapshot, and **no hook of any kind runs
under `--dry-run`** — the engine logs what would run and invokes nothing. The `dry_run` field stays
in the plugin context for protocol stability, but in v1.0 a plugin that is running always sees
`false`.

A plugin runs once per matching stage per restore. Execution order among plugins for a stage follows
discovery order. **[DIVERGE]** Discovery order depends on directory listing order, which is not
guaranteed stable — sort plugins by name within each search directory so runs are reproducible.

### The context passed to a plugin

```json
{
  "repo_dir": "/home/user/dotfiles",
  "profile_name": "base",
  "dry_run": false,
  "targets": ["/home/user/.zshrc", "/home/user/.gitconfig"],
  "hook_type": "post-restore"
}
```

`targets` is every absolute target path the transaction plans to mutate.

### Invocation protocol

The context and the permission set are each JSON-marshalled and base64-encoded, then passed as
arguments to the sandbox wrapper along with the entrypoint path and hook type:

```
<wrapper> <entrypoint> <permissions_b64> <context_b64> <hook_type>
```

**[DIVERGE]** Base64 arguments are a Python-shaped choice (it avoids quoting problems in argv).
In Go, pass the context as JSON **on stdin** instead — no length limits, no encoding step, and the
plugin reads it with one `json.NewDecoder(os.Stdin).Decode(&ctx)`. Keep the argv form only if
compatibility with existing user plugins matters, and version the protocol so both can coexist.

### Result protocol

The plugin writes JSON to stdout:

```json
{"status": "success", "message": "reloaded 3 services"}
```

- Exit code 0 with parseable JSON → that object is the result.
- Exit code 0 with unparseable stdout → synthesize `{"status":"success","stdout":"…"}`.
- Non-zero exit → error including exit code, stderr, and stdout.
- Timeout exceeded → a timeout error naming the plugin.

**A plugin failure fails the restore and triggers rollback.** Plugins are not advisory.

---

## 3. The sandbox — what it actually is

Be precise about this, because the word "sandbox" oversells it.

**What it provides:**
- **Process isolation** — the plugin runs in a separate process. It cannot corrupt `rv`'s memory or
  its transaction state.
- **A timeout** — clamped to `[1, 300]` seconds, enforced by killing the process.
- **Network discouragement** — when `permissions.network` is false, proxy environment variables are
  set to a dead loopback address (`http_proxy=http://127.0.0.1:0`, `no_proxy=*`). This stops
  well-behaved HTTP clients. It does **not** stop a raw socket.
- **A declared permission set** — the wrapper receives `network`, `shell`, `allowed_paths` and is
  expected to enforce them on the plugin's behalf.

**What it does not provide:**
- Kernel-enforced filesystem confinement. `allowed_paths` is advisory unless the wrapper enforces it.
- Real network isolation.
- Privilege reduction — the plugin runs as the same user with the same capabilities.

**Do not describe this as untrusted-code-safe.** A plugin is trusted code with a seatbelt.

**[DIVERGE]** For a materially stronger sandbox in the Go build, the honest options are:
1. **Landlock** (Linux 5.13+) — `landlock_restrict_self` in the child before exec, enforcing
   `allowed_paths` in the kernel. This is the right answer on modern Linux.
2. **seccomp** to deny `socket(2)` when `network: false` — real network isolation, not a proxy hint.
3. **A WASM runtime** for plugins — full isolation, but changes the plugin authoring model entirely.

Recommendation: ship v1.0 with process isolation plus timeout plus the documented honest caveat;
add Landlock in a later phase with a graceful fallback on kernels that lack it.

---

## 4. Writing a plugin (user-facing)

```bash
#!/usr/bin/env bash
# plugins/notify/notify.sh
ctx=$(cat)                                    # JSON context on stdin
profile=$(echo "$ctx" | jq -r .profile_name)
notify-send "rv" "restored profile: $profile"
echo '{"status":"success","message":"notification sent"}'
```

```yaml
# plugins/notify/plugin.yaml
name: notify
version: "1.0.0"
entrypoint: notify.sh
timeout: 10
hooks: [post-restore]
permissions:
  network: false
  shell: true
  allowed_paths: []
```

**Rules for plugin authors** (put this in user docs):
- Write only JSON to stdout. Diagnostics go to stderr.
- Exit non-zero to fail the restore. Only do that when the failure genuinely warrants rolling back
  the whole run.
- Respect `dry_run` — do nothing observable when it is true. (The engine does not invoke plugins on
  a dry run in v1.0, so this is forward-compatibility, not a live case.)
- **Be idempotent.** A plugin can run and then have the transaction roll back underneath it. Rollback
  restores files; it cannot un-run your plugin.
- Stay inside the timeout; long work belongs in a service the plugin triggers, not in the plugin.
