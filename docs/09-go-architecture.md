# 09 — Go Architecture

How the specification maps onto a Go codebase. This document decides the module layout, the
dependency set, and the idiom translations, so the implementer does not have to relitigate them.

---

## 1. Module and layout

```
module github.com/0xkhdr/revive

cmd/
  rv/
    main.go                 # thin: build the root command, run it, map errors to exit codes

internal/
  cli/                      # cobra commands, flag parsing, output rendering
    root.go restore.go backup.go status.go diff.go doctor.go
    watch.go recover.go prune.go secret.go workspace.go clone.go init.go
    render.go               # tables, panels, diff rendering — all output in one place
  manifest/                 # schema, loading, validation
    manifest.go asset.go secret.go packages.go profile.go
    load.go validate.go
  profile/                  # resolution, inheritance, machine overrides
    resolve.go override.go
  engine/                   # the 14-step restore engine + backup
    restore.go backup.go plan.go handler.go
  transaction/              # the 7-phase transaction, journal, rollback, atomic write, lock
    transaction.go journal.go rollback.go atomic.go lock.go
  lockfile/                 # manifest.lock read/write, checksums
    lockfile.go checksum.go
  crypto/                   # age encryption, identity/recipient resolution, secure temp files
    age.go identity.go tempfile.go zero.go
  scrub/                    # secret scrubber
    scrub.go
  permissions/              # chmod/chown enforcement and verification (POSIX build tag)
    permissions_unix.go
  providers/                # package managers
    provider.go base.go cache.go registry.go
    apt.go brew.go pacman.go dnf.go flatpak.go snap.go nix.go cargo.go pip.go docker.go node.go
  plugins/                  # discovery, manifest, sandboxed execution
    loader.go sandbox.go context.go
  status/                   # drift detection, diff content extraction
    status.go diff.go
  doctor/                   # health checks
    doctor.go
  recovery/                 # journal scanning, rollback driver, backup pruning
    recovery.go prune.go
  workspace/                # workspace registry
    workspace.go
  paths/                    # ~/.config/rv layout, canonicalization, interpolation, safety checks
    paths.go interpolate.go env.go
  platform/                 # OS/distro/tool detection
    platform.go
  logging/                  # slog setup, JSON audit handler, scrubbing handler
    logging.go audit.go
  watcher/                  # fsnotify daemon (later phase)
    watcher.go

testdata/                   # fixture manifests, fixture repos, golden files
docs/                       # this specification
reference/                  # the Python implementation (read-only oracle)
```

Everything under `internal/` — nothing here is a public API, and keeping it internal means the
package boundaries can be refactored freely.

**`cmd/rv/main.go` stays tiny:**

```go
func main() {
    if err := cli.Execute(); err != nil {
        os.Exit(cli.ExitCode(err))
    }
}
```

---

## 2. Dependencies

Deliberately small. Each entry is justified; anything not listed should be written rather than
imported.

| Need | Choice | Why |
|------|--------|-----|
| CLI framework | `github.com/spf13/cobra` | Subcommands, flags, generated shell completion. Matches Typer's surface. |
| YAML | `sigs.k8s.io/yaml` or `gopkg.in/yaml.v3` | Prefer `sigs.k8s.io/yaml`: it converts YAML→JSON and uses `encoding/json` tags, so one set of struct tags serves both the manifest (YAML) and the lockfile (JSON). |
| age encryption | `filippo.io/age` | The reference implementation, in-process, by the format's author. |
| File watching | `github.com/fsnotify/fsnotify` | Standard choice; replaces `watchdog`. |
| Terminal output | `github.com/charmbracelet/lipgloss` + `github.com/olekukonko/tablewriter` | Replaces `rich`. Alternatively hand-roll — see below. |
| Diffing | `github.com/sergi/go-diff` or `github.com/hexops/gotextdiff` | Unified and side-by-side diffs. |
| Errgroup | `golang.org/x/sync/errgroup` | Bounded parallel planning with first-error cancellation. |
| UUID | `github.com/google/uuid` | Transaction IDs. |
| flock | `golang.org/x/sys/unix` | `unix.Flock`. |
| Testing | stdlib `testing` + `github.com/stretchr/testify/require` | Keep assertions light. |

**Explicitly from the standard library, not a dependency:**

- `text/template` for templating (see §4 — decided; **not** a Jinja2-compatible package).
- `log/slog` for structured logging.
- `os/exec` with `context.Context` for all subprocesses.
- `crypto/sha256`, `encoding/json`, `path/filepath`.

**On terminal output:** `rich` does a great deal (panels, tables, syntax highlighting, progress). The
Go build does not need all of it. Recommendation: use `lipgloss` for styling and one table library,
and keep every rendering call inside `internal/cli/render.go` so the dependency can be swapped
without touching business logic. Business-logic packages MUST NOT import a rendering library — they
return data.

---

## 3. Python → Go idiom mapping

| Python | Go |
|--------|-----|
| Pydantic model + validators | Struct with tags + an explicit `Validate() error` method |
| `Manifest.model_validate(d, strict=True)` | `yaml.UnmarshalStrict` (or `DisallowUnknownFields`) then `m.Validate()` |
| `StrEnum` | A defined string type with declared constants and a `Valid()` method |
| `str \| list[str]` union field | A `StringOrSlice` type with custom `UnmarshalJSON`/`MarshalJSON` |
| `raise ValueError("…")` | A sentinel error wrapped with `fmt.Errorf("context: %w", ErrX)` |
| `try/except Exception` | Explicit error returns; `recover()` only at the top-level goroutine boundary |
| `@contextmanager` | A struct with a `Close() error` used via `defer` |
| `ThreadPoolExecutor` | `errgroup.Group` with `SetLimit(n)` |
| `logging` + custom formatters | `log/slog` with a scrubbing `slog.Handler` wrapper |
| `os.path.expanduser` | `os.UserHomeDir()` + `filepath.Join` (Go has no `expanduser`) |
| `os.path.expandvars` | `os.Expand` with a custom lookup function |
| `shutil.copytree` | Hand-rolled walk with `os.CopyFS` (Go 1.23+) or an explicit recursive copy |
| `fcntl.flock` | `unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)` |
| `typer.confirm` | A prompt helper in `internal/cli`, disabled under `--headless` |
| `subprocess.run(cmd, timeout=n)` | `exec.CommandContext` with a `context.WithTimeout` |

### Strict validation

Pydantic strict mode is doing real work in the Python version: it rejects unknown fields and refuses
type coercion. Reproduce both:

```go
func Load(path string) (*Manifest, error) {
    raw, err := os.ReadFile(path)
    if err != nil { return nil, fmt.Errorf("reading manifest: %w", err) }

    // 1. Raw version pre-check, before struct binding.
    var probe struct{ Version *int `json:"version"` }
    if err := yaml.Unmarshal(raw, &probe); err != nil {
        return nil, fmt.Errorf("parsing manifest: %w", err)
    }
    if probe.Version != nil && !supportedVersion(*probe.Version) {
        return nil, fmt.Errorf("%w: %d", ErrUnsupportedSchemaVersion, *probe.Version)
    }

    // 2. Strict decode — unknown fields are errors, not silent drops.
    var m Manifest
    if err := yaml.UnmarshalStrict(raw, &m); err != nil {
        return nil, fmt.Errorf("parsing manifest: %w", err)
    }

    // 3. Domain validation + defaulting.
    if err := m.Validate(); err != nil { return nil, err }
    return &m, nil
}
```

`Validate()` walks assets and secrets, applies the rules from
[02-domain-model.md](02-domain-model.md) §1.2/§1.5, and forces the derived fields
(`type: secret` ⇒ `encrypted: true`, secret permissions default `0600`).

### The polymorphic target field

```go
type StringOrSlice []string

func (s *StringOrSlice) UnmarshalJSON(b []byte) error {
    var one string
    if err := json.Unmarshal(b, &one); err == nil { *s = []string{one}; return nil }
    var many []string
    if err := json.Unmarshal(b, &many); err != nil { return err }
    *s = many
    return nil
}
```

For the **lockfile** the round trip must preserve shape (a scalar target writes a scalar), so track
whether the source was scalar and implement `MarshalJSON` accordingly:

```go
type StringOrSlice struct { Values []string; wasScalar bool }
func (s StringOrSlice) MarshalJSON() ([]byte, error) {
    if s.wasScalar && len(s.Values) == 1 { return json.Marshal(s.Values[0]) }
    return json.Marshal(s.Values)
}
```

---

## 4. The template engine — `text/template` **[DECIDED]** **[DIVERGE]**

Python uses Jinja2. **The Go build uses stdlib `text/template`.** This is settled; pongo2 and other
Jinja2-compatible packages were considered and rejected. Zero dependencies and strict-undefined
semantics out of the box beat syntax compatibility for a user base this size, and the migration is
mechanical.

This **breaks every existing user template**. That cost is accepted and is paid down by the tooling
in §4.3.

### 4.1 Construction

```go
tmpl, err := template.New(asset.ID).
    Option("missingkey=error").
    Parse(string(src))
if err != nil {
    return fmt.Errorf("parsing template for asset %q: %w", asset.ID, err)
}

var buf bytes.Buffer
if err := tmpl.Execute(&buf, ctx); err != nil {
    return fmt.Errorf("rendering template for asset %q: %w", asset.ID, err)
}
```

`Option("missingkey=error")` is **mandatory**, never `missingkey=zero` or the default
`missingkey=invalid`. An unset variable MUST be a hard error, never an empty string — this is the
same guarantee the interpolator makes for `${VAR}`, and for the same reason: a silently empty value
writes a broken config file that fails much later and much more confusingly.

### 4.2 Context shape

The merged context from [02-domain-model.md](02-domain-model.md) §1.3 is passed as a
`map[string]any`, so every variable is referenced with a leading dot:

| Jinja2 (old) | `text/template` (new) |
|--------------|----------------------|
| `{{ email }}` | `{{ .email }}` |
| `{{ _hostname }}` | `{{ ._hostname }}` |
| `{{ HOME }}` | `{{ .HOME }}` |
| `{% if x %}…{% endif %}` | `{{ if .x }}…{{ end }}` |
| `{% for i in xs %}…{% endfor %}` | `{{ range .xs }}…{{ end }}` |
| `{{ x \| upper }}` | `{{ upper .x }}` (see below) |

Environment-variable keys reach the template unchanged, so `{{ .HOME }}` and `{{ .CARD_EXPRESS_DIR }}`
work. Keys that are not valid Go identifiers (rare in practice) are reachable with
`{{ index . "weird-key" }}`.

**Function map.** Register a small, fixed set of helpers so common Jinja2 filters have an
equivalent. Keep it minimal — a template language is not a scripting language:

```go
template.FuncMap{
    "upper":   strings.ToUpper,
    "lower":   strings.ToLower,
    "trim":    strings.TrimSpace,
    "replace": func(old, new, s string) string { return strings.ReplaceAll(s, old, new) },
    "join":    func(sep string, xs []string) string { return strings.Join(xs, sep) },
    "default": func(fallback, v any) any { if v == nil || v == "" { return fallback }; return v },
    "env":     os.Getenv,
}
```

Do not add a function because it *might* be useful. Additions are one-way; every function becomes
part of the manifest's public contract.

### 4.3 Migration support (required, not optional)

Because this break is deliberate, the tooling that softens it is part of the deliverable:

1. **`rv doctor` detects Jinja2 syntax** in any `type: template` source and reports it as a
   `template_syntax` issue at **critical** severity, naming the file, the line, and the
   `text/template` equivalent. Detection patterns:
   - `{%` … `%}` — Jinja2 statement tags, which `text/template` will pass through as literal text
     rather than failing, so this MUST be caught by the linter rather than at render time.
   - `{{` followed by an identifier with no leading `.` and no registered function name.
   - `|` inside `{{ }}` — Jinja2 filter syntax.
2. **The migration guide** ([10-build-plan.md](10-build-plan.md) Phase 14) carries the table above
   plus worked examples.
3. **A parse error names the asset and the file**, never just a byte offset.

Detection is a linter, not a translator. Do **not** write an automatic Jinja2→`text/template`
converter: partial translation of a template that then silently renders wrong output is worse than
an error telling the user to rewrite eight lines by hand.

---

## 5. Concurrency

Concurrency exists in exactly two places. Everything else is sequential, deliberately.

**Parallel asset planning:**

```go
g, ctx := errgroup.WithContext(ctx)
g.SetLimit(min(runtime.NumCPU(), 8))
results := make([]planResult, len(items))       // indexed — preserves order
for i, item := range items {
    i, item := i, item
    g.Go(func() error {
        r, err := planOne(ctx, item, repoDir, identity, interactive)
        if err != nil { return fmt.Errorf("planning asset %q: %w", item.ID, err) }
        results[i] = r
        return nil
    })
}
if err := g.Wait(); err != nil { return err }
// merge results in index order — deterministic
```

**The watch daemon's debounce timer**, which is a single goroutine plus a `time.Timer`.

Shared mutable state that MUST be guarded:
- The scrubber's dynamic secret registry (`sync.RWMutex`).
- The package cache (mutex, or load-once/write-once within step 10).
- The platform tool-lookup cache (`sync.Map` or a mutex-guarded map).

Everything else — transaction execution, rollback, lockfile writing, provider installs — is
single-goroutine by design. Do not parallelize execution; the ordering guarantees depend on it.

---

## 6. Error handling and exit codes

```go
// internal/cli/errors.go
func ExitCode(err error) int {
    switch {
    case err == nil:                        return 0
    case errors.Is(err, ErrUsage),
         errors.Is(err, manifest.ErrValidation),
         errors.Is(err, profile.ErrNotFound),
         errors.Is(err, doctor.ErrUnhealthy): return 1
    default:                                 return 2
    }
}
```

Rules:
- Sentinel errors live in the package that owns the concept, not in a shared `errors` package.
- Wrap with `%w` and add context at every layer boundary.
- The CLI layer is the only place that formats an error for a human.
- **Never compare error strings.**

---

## 7. Testing hooks to build in from the start

Retrofitting testability is the expensive path. Design these in now:

1. **Injectable paths.** No package may call `os.UserHomeDir()` inline. A single `paths.Config`
   struct carries the config dir, journal dir, backup dir, audit log path, and lock path; tests
   construct one rooted at `t.TempDir()`. This alone removes the Python suite's serial-execution
   requirement.
2. **Injectable clock** for retention and cache TTL tests.
3. **Injectable command runner.** Providers depend on an interface, not on `os/exec` directly:
   ```go
   type Runner interface {
       Run(ctx context.Context, cmd []string) ([]byte, error)
       LookPath(name string) (string, bool)
   }
   ```
   A fake runner makes every provider testable without touching the system.
4. **`io.Writer` for all output.** Never `fmt.Println` from business logic.

---

## 8. Build and release

```bash
go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags)" ./cmd/rv
```

- Targets: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`.
- `CGO_ENABLED=0` for fully static Linux binaries.
- Use `goreleaser` for the release matrix, checksums, and archives.
- Windows: **not supported**. Guard POSIX-only files with `//go:build unix` and let the build fail
  loudly on Windows rather than shipping the permission-mapping lie described in
  [05-security.md](05-security.md) §3.
