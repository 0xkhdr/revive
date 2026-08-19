# Security Policy

## Supported Versions

| Version | Supported |
| ------- | --------- |
| 1.x     | ✅ Active  |
| < 1.0   | ❌ None    |

---

## Security Model

Revive applies defense-in-depth across four domains:

### 1. Secret Handling

- All secrets are encrypted at rest using [age](https://age-encryption.org/) (`pyrage` native binding with CLI fallback).
- During restore, secrets are decrypted directly into a `ZeroBuffer`-backed in-memory `bytearray`. Intermediate Python objects (`bytes`) are zeroed via `ZeroBuffer.zero_bytes()` immediately after use.
- `ZeroBuffer.zero_bytes()` is a **best-effort** memory zeroing mechanism for CPython `bytes` objects. Because Python's garbage collector does not guarantee immediate collection, this is defense-in-depth only — rely on OS-level protected memory regions and encrypted swap for stronger guarantees.
- Age identity files must exist at the path specified via `--identity` or in `~/.config/rv/identity.txt`. They are never written to disk by Revive.
- Log/audit output is scrubbed of secrets via `SecretScrubber` before being persisted.

### 2. Plugin Sandbox

Plugins are executed inside a heavily restricted Python subprocess:

| Protection | Detail |
|---|---|
| **Import Interception** | `ctypes`, `cffi`, `gc`, `importlib` blocked via stack-frame inspection |
| **Filesystem Restrictions** | `builtins.open`, `os.remove`, `os.mkdir`, etc. gated to allow-listed paths only |
| **Network Restrictions** | `socket.socket` patched to raise `PermissionError` unless `permissions.network: true` |
| **Shell Restrictions** | `subprocess.Popen`, `os.system`, `os.popen`, `os.spawn*` patched unless `permissions.shell: true` |
| **Resource Limits** | POSIX `setrlimit`: 2 GiB memory, 310s CPU |
| **Timeout** | Default 30s, configurable up to 300s |

**Known limitation**: The sandbox does not use Docker or seccomp profiles. A sufficiently motivated native extension (`.so`) embedded in a plugin dependency could escape the sandbox. For production deployments, run Revive inside a container or dedicated VM when executing untrusted plugins.

### 3. CORS Restrictions

The Web GUI server (`rv gui`) restricts CORS to the loopback address it is bound on (`http://127.0.0.1:<port>` or `http://localhost:<port>`) by default. The `--cors-wildcard` flag enables wildcard CORS for local development; **it must never be used in production or when the GUI server is bound to a non-loopback address.**

A security warning is printed to stderr whenever the server binds to a non-loopback host.

### 4. Atomic Writes & Transaction Integrity

All filesystem mutations occur inside a 7-step `TransactionContext`:
1. Backup snapshots are written before any mutation.
2. Writes use `AtomicWrite` (temp file + `os.replace`) to prevent partial writes.
3. Any step failure triggers a journal-based rollback that restores the pre-restore state.
4. The process lock (`~/.config/rv/rv.lock`) prevents concurrent `rv` operations on the same machine.

---

## Known Limitations

| ID | Component | Limitation |
|---|---|---|
| KL-001 | `ZeroBuffer.zero_bytes()` | Best-effort CPython memory zeroing. Not guaranteed on PyPy, CPython < 3.11, or future interpreter changes. |
| KL-002 | Plugin Sandbox | No kernel-level sandboxing (seccomp/Docker). Native extensions can escape. Deferred to post-1.0. |
| KL-003 | GUI Auth | `X-Auth-Token` is transmitted as a plain HTTP header (not HTTPS). Bind to loopback only in production. |

---

## Reporting a Vulnerability

**Please do not report security vulnerabilities in public GitHub issues.**

To report a vulnerability:

1. **Preferred**: Use [GitHub Private Vulnerability Reporting](https://github.com/0xkhdr/revive/security/advisories/new)
   for confidential, coordinated disclosure.
2. **Alternatively**: Open a GitHub Security Advisory draft — this keeps the report private
   until a fix is released.
3. Include:
   - A description of the vulnerability and its impact.
   - Steps to reproduce.
   - Affected versions.
   - Suggested fix or mitigations, if any.

We aim to acknowledge reports within **48 hours** and provide a fix or workaround within **7 days** for critical issues.

---

## Security-Relevant Dependencies

| Package | Role | Version Constraint |
|---|---|---|
| `pyrage` | Age encryption | `>=1.0.0` |
| `pydantic` | Strict schema validation | `>=2.0.0,<3` |
| `cryptography` | Underlying crypto for pyrage | Managed by pyrage |
| `PyYAML` | YAML parsing | `>=6.0` |

Run `bandit -r src/rv -ll` to check for security issues in the codebase.
