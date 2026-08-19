# 05 — Security Model

`rv` handles private keys, API tokens, and `.env` files in plaintext form on their way to disk.
The security model has five parts: **encryption at rest**, **log scrubbing**, **permission
enforcement**, **memory hygiene**, and **path containment**. Each has a hard requirement.

---

## 1. Encryption at rest — age

Secrets live in the repo encrypted with [age](https://age-encryption.org) (X25519), so the repo can
be pushed to a private remote without exposing them.

- **Identity** (private key): `AGE-SECRET-KEY-1…`, default `~/.config/rv/identity.txt`, mode `0600`.
- **Recipient** (public key): `age1…`, safe to commit.

**[DIVERGE]** Use `filippo.io/age` in-process. The Go build MUST NOT shell out to an `age` binary
for encryption or decryption — no plaintext through argv, no plaintext through a temp file handed to
another process, no dependency on a system package. The Python version's `pyrage`-with-CLI-fallback
dance disappears entirely.

### Required operations

```go
func GenerateKeypair() (pub string, identity string, err error)
func Encrypt(plaintext []byte, recipients []string) ([]byte, error)
func Decrypt(ciphertext []byte, identity string) ([]byte, error)
func PublicKeyFromIdentity(identity string) (string, error)
```

### Identity and recipient parsing

Both accept either a literal key string or a path to a file containing one — resolve by inspecting
the value:

- Starts with `age1` → it is a recipient literal.
- Starts with `AGE-SECRET-KEY-1` → it is an identity literal.
- Otherwise, if it looks like a path (contains a separator, starts with `.`, or is absolute) → read
  the file and scan **line by line** for the first line with the right prefix. Identity files written
  by `rv secret keygen` carry a `# public key: age1…` comment on the first line, so a naive
  whole-file read would break.
- A path that does not exist is an error, not a fallthrough to "treat as literal".

`PublicKeyFromIdentity` MUST try, in order: parse the `# public key:` comment from the file, then
derive the public key from the private key. Deriving is cheap; the comment is just a fast path.

### Where decryption happens

- Restore, planning phase (step 5), into memory only.
- `rv status` / `rv diff`, to compare content — via a **secure temp file** when a file path is
  unavoidable (see §4).
- `rv secret decrypt`, explicitly, to a user-named path.
- `rv secret rotate`, decrypt-then-re-encrypt.

Decrypted bytes MUST NOT be written anywhere except the declared target with its enforced mode.

### Key rotation

`rv secret rotate <file> --new-recipient <key>`:
1. Decrypt with the current identity (or read `--from-plaintext` directly).
2. Re-encrypt to the new recipient set.
3. Atomically replace the `.age` file.
4. If `--from-plaintext` was used, **securely wipe and delete the plaintext source** — overwrite
   with zeros, `fsync`, then unlink. Requires `--confirm`.

---

## 2. Log scrubbing

**Every** log line, on every channel, passes through the scrubber before it is emitted. Console,
JSON audit file, error messages, panics. There is no unscrubbed path.

### Static patterns

Compiled once, applied to every line, replacing matches with `[REDACTED]`:

| Pattern | Catches |
|---------|---------|
| `AGE-SECRET-KEY-1[a-zA-Z0-9]+` (case-insensitive) | age private keys |
| `(?:ssh-ed25519\|ssh-rsa\|ecdsa-sha2-nistp256)\s+[a-zA-Z0-9+/=]+` | SSH keys |
| `-----BEGIN\s+(?:RSA\|OPENSSH\|PRIVATE)\s+KEY-----[^-]+-----END\s+(?:RSA\|OPENSSH\|PRIVATE)\s+KEY-----` (dotall) | PEM private keys |

### Dynamic registry

Secrets discovered at runtime are registered and thereafter redacted by literal substring match:

```go
func RegisterSecret(s string)   // ignore strings shorter than 5 chars — false positives
func Scrub(s string) string
```

Two rules:
- **Sort registered secrets by length descending before substituting.** Otherwise a short secret
  that is a prefix of a longer one produces `[REDACTED]<tail>` — a partial leak.
- The identity private key MUST be registered as soon as it is read, before anything else can log.

**[DIVERGE]** The Python registry is a mutable package-level `set` with no locking. In Go it MUST be
a struct guarded by a `sync.RWMutex`, since parallel asset planning logs concurrently.

### Audit log

`~/.local/share/rv/audit.log` — one JSON object per line, appended:

```json
{"timestamp":"…","level":"INFO","logger":"rv.services.restore","message":"…",
 "tx_id":"…","asset_id":"…","op":"restore"}
```

The audit logger MUST NOT propagate to the console handler (no double printing). The whole
serialized line is scrubbed after marshalling, so a secret in any field is caught.

---

## 3. Permission enforcement

```go
func Enforce(path, permissions string, owner *string) error
func Verify(path, permissions string) (bool, error)
```

- `permissions` is a 4-digit octal string starting with `0`. Parse strictly; reject anything else.
- `chmod` is applied to every asset that declares permissions, immediately after the write.
- `chown` is applied only when `owner` is set; it resolves the username to uid/gid. A nonexistent
  user is a validation error; a permission failure (chown usually needs root) is a permission error.
  Both are distinct error types.
- **Secrets are checked twice**: at manifest validation (`mode & 0o077 == 0`) and at transaction
  verification (actual mode equals expected). A secret that lands world-readable MUST fail the
  transaction and roll it back.

**[DIVERGE]** Drop the Windows permission mapping entirely. The Python version maps POSIX modes to
Windows read-only attributes and skips `chown` with a warning, which is a security-relevant lie —
"0600 enforced" is not true on Windows. The Go build targets Linux and macOS; a Windows build should
refuse to run rather than pretend. Use build tags so the POSIX implementation is the only one that
compiles.

---

## 4. Memory hygiene

Decrypted plaintext is the highest-value data in the process.

**Rules:**

1. Keep plaintext in `[]byte`, never `string`. Go strings are immutable and cannot be zeroed.
2. Zero the slice in a `defer` as soon as the bytes are consumed:
   ```go
   defer func() { for i := range plaintext { plaintext[i] = 0 } }()
   ```
3. Never log plaintext, never include it in an error message, never put it in a struct that gets
   marshalled for debugging.
4. Do not attempt to defeat the garbage collector or the optimizer with anything clever. The
   Python version pokes at CPython's `PyBytesObject` internals to overwrite immutable `bytes`; that
   is best-effort theater and MUST NOT be reproduced. A simple zeroing loop on a `[]byte` you still
   own is the correct measure. **[DIVERGE]**

### Secure temp files

Where a plaintext file on disk is unavoidable (status/diff comparison, or the `age` CLI path if it
ever returns):

- Create with mode `0600` from the start — never create-then-chmod, which leaves a race window.
- On close: overwrite the entire file with zeros, `fsync`, then unlink.
- Directory variant: mode `0700`, zero every file inside before removing the tree.
- In Go, wrap this in a type with a `Close() error` used via `defer`.

---

## 5. Path containment

The mechanism that stops a malicious or careless manifest from writing outside its intended area.

| Guard | Where |
|-------|-------|
| `source` MUST be relative and MUST NOT contain `..` after normalization | Manifest validation, both Asset and Secret |
| Target interpolation errors on unset variables | Interpolator — prevents `${UNSET}/.config` becoming `/.config` |
| `IsSafeSubpath(base, target)` — resolve both and require containment | Used when validating that repo-relative writes stay in the repo |
| Symlink loop detection on the source | Symlink asset planning |
| Canonicalization does **not** resolve symlinks | Path helper — writing through an attacker-planted symlink is the bug this avoids |

Note the asymmetry: **sources are confined to the repo, targets are deliberately not.** Targets are
supposed to reach into `~`, `/etc`, and elsewhere — that is the tool's job. Target safety comes from
the manifest being a reviewed, committed artifact, plus conflict strategy, plus the snapshot and
rollback.

---

## 6. Subprocess execution

- **Never invoke a shell.** No `sh -c`, no `shell: true` equivalent. Always pass an argument slice.
  The one legitimate exception is `nvm`, which only exists as a shell function and requires
  `bash -c ". nvm.sh && nvm install <version>"`; the interpolated value there MUST be a version
  string validated against `^v?\d+(\.\d+)*$` before it goes anywhere near that string.
- Asset hook commands are split with shell-style word splitting but executed without a shell.
- Every subprocess gets a timeout: **30 s** for asset hooks, **30 s default / 300 s max** for
  plugins, provider-defined for package managers.
- Every subprocess MUST take a `context.Context` so Ctrl-C cancels it. **[DIVERGE]**

---

## 7. GUI security (when it is rebuilt)

Deferred to post-v1.0, but the constraints are recorded here so they are not lost:

- Bind to `127.0.0.1` by default. Binding to any other address requires an explicit
  `--i-understand-no-tls` flag, because the bearer token would otherwise cross the network in clear.
- Bearer token required on every `/api/*` request; auto-generated when not supplied. Compare tokens
  with a constant-time comparison.
- CORS restricted to loopback origins unless a hidden development flag is passed.
- Static file serving MUST resolve and contain paths — no `..` escapes out of the static directory.

---

## 8. Threat model

**Defended:**
- Secrets committed to a git repo (encrypted at rest).
- Secrets leaking into logs, terminal scrollback, CI output (scrubbing).
- Secrets landing on disk world-readable (double-checked permission enforcement).
- A half-applied restore leaving a broken machine (transaction + journal + rollback).
- Two `rv` processes racing (process lock).
- A manifest writing outside the repo via a crafted `source` (path validation).

**Not defended:**
- An attacker with root, or with the user's own uid, on the machine. `rv` runs as the user and can
  do anything the user can.
- Memory forensics against the live process. Zeroing narrows the window; it does not close it.
- A malicious manifest in a repo the user chose to clone. The manifest is trusted input — review it
  before running `rv restore`, exactly as with any install script.
- Plugin escape. The sandbox is process isolation plus a permission declaration, not a jail. See
  [07-plugins-hooks.md](07-plugins-hooks.md) for what it does and does not do.
