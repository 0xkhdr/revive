# 06 — Package Providers

A **provider** adapts one package manager to a uniform interface. Step 10 of the restore engine
walks the providers with non-empty package lists, in a fixed order.

---

## 1. The interface

```go
type Provider interface {
    Name() string                                   // the binary probed on PATH
    IsAvailable() bool                              // is this manager usable on this machine
    IsInstalled(ctx context.Context, pkg string) (bool, error)
    Install(ctx context.Context, pkgs []string, opts InstallOptions) error
}

type InstallOptions struct {
    DryRun   bool
    UseCache bool   // false when --force-packages
}
```

`IsInstalled` is **mandatory** for every provider — it is what makes restore idempotent. A provider
that cannot answer it has no business installing anything.

### Shared behavior (embed a base type)

```go
type base struct{ name string }

func (b base) IsAvailable() bool { _, err := exec.LookPath(b.name); return err == nil }

// FilterMissing returns the subset of pkgs that are not installed, consulting the cache first.
func (b base) FilterMissing(ctx, pkgs []string, useCache bool) ([]string, error)

// ExecuteWithRetry runs cmd with exponential backoff.
func (b base) ExecuteWithRetry(ctx context.Context, cmd []string) ([]byte, error)
```

**`FilterMissing`** per package:
1. If `useCache` and the cache says installed → skip.
2. Else call `IsInstalled`. If installed → record it in the cache and skip.
3. Else → add to the missing list.

**`ExecuteWithRetry`**: 3 attempts, initial delay 2 s, backoff factor 2 (2 s, 4 s). Retries on
process failure, missing binary, and permission errors. Logs each failure with the provider name and
stderr. After exhausting attempts, returns a `ProviderError` wrapping the last error.

**[DIVERGE]** Retrying a package install blindly is wrong for some failures — "package not found"
and "permission denied" will never succeed on retry, and burning 6 seconds on them is pure latency.
The Go build SHOULD classify: retry on network/lock-contention failures, fail fast on
not-found/permission. Start with the blind retry to match behavior, then refine per provider.

---

## 2. The idempotency cache

`~/.config/rv/package-cache.json`:

```json
{
  "apt-get": { "installed": ["git", "zsh"], "last_updated": 1716000000.0 },
  "brew":    { "installed": ["fzf"],        "last_updated": 1716000000.0 }
}
```

- **TTL: 86400 seconds (24 h)**, per provider. An expired entry is treated as a complete miss.
- `MarkInstalled(provider, pkgs)` appends (deduplicated) and refreshes `last_updated`.
- `Invalidate(provider)` drops one entry; `InvalidateAll()` deletes the file. `--force-packages`
  calls `InvalidateAll` before step 10.
- Load failures return an empty cache — never fatal. Save failures log a warning — never fatal. The
  cache is an optimization; correctness never depends on it, because `IsInstalled` is the real check.
- Writes go through temp-file-plus-rename.

**[DIVERGE]** The Python cache is read-modify-written from multiple call sites with no locking. In
Go, guard it with a mutex and, since it is written during a run that already holds the process lock,
prefer batching: load once at the start of step 10, mutate in memory, write once at the end.

---

## 3. Provider catalogue

| Provider | Probed binary | Install command | Install check |
|----------|---------------|-----------------|---------------|
| apt | `apt-get` (**and** `dpkg`) | `apt-get install -y <pkgs>` | `dpkg -s <pkg>` exit 0 **and** stdout contains `Status: install ok installed` |
| brew | `brew` | `brew install <pkgs>` | `brew list --versions <pkg>` exit 0 |
| pacman | `pacman` | `pacman -S --noconfirm <pkgs>` | `pacman -Q <pkg>` exit 0 |
| dnf | `dnf` | `dnf install -y <pkgs>` | `rpm -q <pkg>` exit 0 |
| flatpak | `flatpak` | `flatpak install -y <refs>` | `flatpak info <ref>` exit 0 |
| snap | `snap` | `snap install <pkgs>` | `snap list <pkg>` exit 0 |
| nix | `nix-env` | `nix-env -iA nixpkgs.<pkg>` | `nix-env -q <pkg>` exit 0 |
| cargo | `cargo` | `cargo install <pkgs>` | `cargo install --list` contains the crate |
| pip | `pip`/`pip3` | `pip install --user <pkgs>` | `pip show <pkg>` exit 0 |
| docker | `docker` | `docker pull <image>` per image | `docker image inspect <image>` exit 0 |
| node | `node` | see §4 | version comparison |

Verify each command against the corresponding file in `reference/src/rv/providers/` when
implementing; the table is the contract, the reference is the tiebreaker.

### Install flow, uniform across providers

```
if len(pkgs) == 0: return
if !dryRun && !IsAvailable(): return ProviderError("<name> is not available on this platform")
missing := FilterMissing(ctx, pkgs, useCache)
if len(missing) == 0: log "all packages already installed"; return
if dryRun: log "[Dry Run] would install: <missing>"; return
ExecuteWithRetry(ctx, installCmd(missing))
MarkInstalled(name, missing)
```

Note that availability is **not** checked under `--dry-run` — a dry run on machine A should be able
to preview a manifest destined for machine B.

Notes per provider:
- **apt** probes both `apt-get` and `dpkg`; either missing means unavailable.
- **apt** may need root. Do not silently prepend `sudo`; let the command fail and surface the
  permission error. **[DIVERGE]** Consider detecting non-root plus a missing write permission on the
  dpkg lock and emitting a clear "run with sudo or configure a privileged helper" message instead of
  a raw exit-100 dump.
- **nix** package names are written bare in the manifest (`ripgrep`) and prefixed with `nixpkgs.`
  at the command boundary.
- **pip** always uses `--user`; never touch a system site-packages.

---

## 4. Node provider

Node is special: it manages a **version**, not a package list.

```yaml
packages:
  node:
    version_file: .nvmrc    # relative to the workspace
    version: null           # explicit; wins over version_file
```

```
target := version if set, else the trimmed contents of <repoDir>/<version_file>
strip a leading "v" from the target
if no target: log "no Node.js version target defined"; return

current := `node -v`, "v" stripped, or empty if node is absent
if current has prefix target: log match; return          # "20" matches "20.11.0"
if dryRun: log what would be installed; return

if fnm is on PATH:  fnm install <target>;  return on success
if $NVM_DIR/nvm.sh exists (default ~/.nvm):
    bash -c ". <nvm.sh> && nvm install <target>"; return on success
return ProviderError naming current, target, and the absence of fnm/nvm
```

`IsInstalled(pkg)` for this provider means "is this version currently active", using the same
prefix comparison.

**Security note:** this is the single place a shell is invoked, because `nvm` is a shell function
and cannot be exec'd. The target version MUST be validated against `^v?\d+(\.\d+)*$` before it is
interpolated into that command string. See [05-security.md](05-security.md) §6.

A missing version file is a warning plus skip, not an error.

---

## 5. Docker provider

`packages.docker.images` is a list of image references, each `docker pull`ed. `IsInstalled` is
`docker image inspect <ref>` exiting 0. Available iff `docker` is on PATH — note that the daemon
may still be unreachable, so an install failure must surface the docker error verbatim rather than
claiming the image does not exist.

---

## 6. Platform detection

```go
func OS() string                             // linux, darwin, …
func IsLinux() bool
func IsMacOS() bool
func Distro() string                          // from /etc/os-release ID=, lowercased; "" on non-Linux
func FindTool(name string) (string, bool)     // cached PATH lookup
func HasTool(name string) bool
func AvailablePackageManagers() map[string]bool
```

Results are cached for the process lifetime — `doctor` and every provider probe the same binaries,
and repeated `exec.LookPath` calls in a loop are wasted syscalls. In Go use `sync.Once` per key or a
mutex-guarded map.

`Distro()` reads `/etc/os-release` and takes the `ID=` value, stripping quotes. Used by `doctor` to
tell the user which package managers make sense on their system.

---

## 7. Adding a provider

1. Implement `Provider`, embedding the base type.
2. Register it in the restore engine's step-10 ordering **and** in `doctor`'s availability report.
3. Add its list field to the `Packages` struct and to `ResolvedProfile.Packages`.
4. Handle it in profile resolution's package-group switch and in the machine-override merge.
5. Tests: available/unavailable, already-installed (no command run), install path, dry-run path,
   retry-then-fail path.

Step 4 is the one that gets forgotten — a provider registered everywhere except the override merge
silently ignores machine-specific packages.

**[DIVERGE]** The Python version hardcodes each provider in four separate `if`/`elif` chains. The Go
build SHOULD use a registry (`map[string]Provider` plus an explicit ordered slice) so adding a
provider touches one table instead of four switch statements. Keep the order slice explicit — do not
iterate a map, or install order becomes random.
