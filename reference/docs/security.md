# Security Guide

This guide covers Revive's security model, secrets management, identity handling, and CORS configuration.

---

## Age Encryption & Identity Management

Revive uses **age**, a modern encryption format, to protect secrets.

### Generating Your Identity Key

```bash
rv secret keygen --output ~/.config/rv/identity.txt
```

This creates:
- **Private key** (secret): `~/.config/rv/identity.txt`
- **Public key** (shareable): Printed to stdout and in the comments of the identity file

### Identity File Format

```
# public key: age1ql3z7hjy92fpxqqzyt4dxq7q2jvl8n2l9qcqrknzf9tgwrgnqzqqm4yxkl
AGE-SECRET-KEY-1UHGYX5QNFRVQM62HVUAYMKHFPQK0RJ4L7ADLRMRH5DUX8TP7JQQYTNSK5
```

Never commit the identity file to Git. Store it safely:
- Option 1: `~/.config/rv/identity.txt` (default, auto-used)
- Option 2: Password manager (1Password, Bitwarden, LastPass)
- Option 3: Separate secure channel (LUKS, encrypted USB, etc.)

### Using Different Identity Files

If your identity file is not at the default location:

```bash
rv restore base --identity ~/my-keys/revive-identity.txt
rv backup base --identity ~/my-keys/revive-identity.txt
```

---

## Secret Encryption Workflow

### 1. Encrypt a New Secret

```bash
# Generate public key first
PUBKEY=$(grep "^# public key:" ~/.config/rv/identity.txt | cut -d: -f2 | xargs)

# Encrypt a file
rv secret encrypt ~/.aws/credentials \
  --output secrets/aws_creds.age \
  --recipient "$PUBKEY"
```

### 2. Commit to Repository

```bash
git add secrets/aws_creds.age
git commit -m "chore: add encrypted AWS credentials"
git push
```

The `.age` file is safe to commit — it's encrypted and unreadable without the private key.

### 3. Restore on New Machine

```bash
# Copy identity file to new machine (via secure channel)
mkdir -p ~/.config/rv
scp remote:~/.config/rv/identity.txt ~/.config/rv/

# Clone repo and restore
rv clone https://github.com/user/dotfiles ~/dotfiles \
  --restore base \
  --identity ~/.config/rv/identity.txt
```

Secrets are decrypted to memory and written to disk, never logged.

---

## In-Memory Secret Handling

Secrets are decrypted directly to an in-memory buffer (`ZeroBuffer`) and:
1. **Never written to disk** — stays in RAM until written to target
2. **Zeroed after use** — memory is explicitly overwritten with zeros
3. **Not logged** — `SecretScrubber` strips credentials from audit logs

This prevents accidental plaintext leaks in logs, temp files, or debuggers.

---

## Log Scrubbing

All log output (console, audit logs) is passed through `SecretScrubber`, which removes:
- Age secret key material (`AGE-SECRET-KEY-*`)
- Common credential patterns (passwords, tokens, API keys)
- Environment variables containing sensitive data

Example scrubbed log:

```json
{
  "event_type": "restore",
  "profile_name": "base",
  "target": "~/.aws/credentials",
  "status": "success",
  "message": "Secret decrypted and written"  // credentials removed
}
```

---

## Key Rotation

### Rotating Secrets to a New Key

If you need to rotate your age keypair (e.g., to onboard a new team member):

```bash
# Generate new keypair
rv secret keygen --output ~/.config/rv/identity-new.txt

# Extract the new public key
NEW_PUBKEY=$(grep "^# public key:" ~/.config/rv/identity-new.txt | cut -d: -f2 | xargs)

# Rotate existing secret
rv secret rotate secrets/aws_creds.age \
  --identity ~/.config/rv/identity.txt \
  --new-recipient "$NEW_PUBKEY"

# Verify and commit
git add secrets/aws_creds.age
git commit -m "chore: rotate AWS credentials keypair"
```

### Decrypting Without the Original Key

If your private key is lost but you have the plaintext secret:

```bash
rv secret rotate secrets/aws_creds.age \
  --from-plaintext ~/.aws/credentials \
  --new-recipient "$NEW_PUBKEY" \
  --confirm
```

This re-encrypts the plaintext file to the new key and securely wipes the plaintext.

---

## Permission Safety

Revive enforces POSIX permissions on all sensitive files:

| File | Required Permissions | Reason |
|------|---------------------|--------|
| Identity file | `0600` | Private key — world-readable is a security failure |
| Decrypted secrets | `0600` or `0400` | Credentials — must not be group/world-readable |
| Backup snapshots | `0700` | Contain pre-mutation state — restrict access |

The manifest validator rejects secret definitions with unsafe permissions:

```yaml
secrets:
  - id: bad_secret
    source: secrets/creds
    target: ~/.config/app/secret
    permissions: "0644"  # ❌ ERROR: world-readable secret!
```

---

## Path Traversal Prevention

Asset source paths are validated to prevent escaping the repository:

```yaml
assets:
  - id: bad_path
    source: ../../etc/passwd  # ❌ ERROR: '..' not allowed
    target: /tmp/passwd

  - id: good_path
    source: assets/config     # ✅ OK: relative to repo root
    target: ~/.config/app
```

All paths are canonicalized and checked for symbolic link loops.

---

## Process Isolation & Locking

### Process Lock

Only one Revive restore operation can run at a time. The `ProcessLock` uses filesystem-level `flock` on `~/.config/rv/rv.lock` to prevent concurrent mutations.

```bash
# This will wait if another restore is in progress
rv restore base
# (waits until first restore completes)
```

### Plugin Sandbox

Plugins run in isolated subprocesses with:
- **Filesystem restrictions** — only allowed paths
- **Network blocking** — unless explicitly allowed
- **Shell blocking** — unless explicitly allowed
- **Import interception** — blocks dangerous modules
- **Resource limits** — 2 GiB memory, 310s CPU
- **Execution timeout** — 30s default (configurable, max 300s)

See [Plugin Authoring Guide](plugins.md#plugin-execution--sandboxing).

---

## Web GUI Security

### Loopback-Only Binding

By default, `rv gui` binds to `127.0.0.1`:

```bash
rv gui              # Binds to 127.0.0.1:8080
                    # Accessible only on localhost
```

If you bind to a non-loopback address (e.g. LAN access), Revive prints a warning to stderr:

```bash
rv gui --host 0.0.0.0
# WARNING: The GUI server is binding to a non-loopback address (0.0.0.0).
# This exposes the API to your network. Use with caution.
```

### CORS Policy

The Web GUI has a strict CORS policy:

**Default (loopback-only)**:
```
Access-Control-Allow-Origin: http://127.0.0.1:8080
```

This allows the dashboard to communicate with the API on the same loopback address.

**Development (wildcard)**:
```bash
rv gui --cors-wildcard
# CORS: Access-Control-Allow-Origin: *
```

⚠️ **Warning**: `--cors-wildcard` allows any website to access the API. Only use in development on isolated networks.

### Authentication Token

The Web GUI requires an authentication token on every API request:

```bash
rv gui --auth-token my-secure-token
```

If not provided, a random 32-character hex token is generated and printed to the console.

### HTTPS (TLS) Disabled

The current Web GUI does **not** support HTTPS. If you expose the GUI beyond loopback:
- Use a reverse proxy (nginx, Caddy) with TLS
- Or bind only to loopback and SSH tunnel: `ssh -L 8080:127.0.0.1:8080 user@remote`

---

## Audit Logging

All operations are logged to `~/.config/rv/audit.log` (JSON format):

```json
{
  "timestamp": "2026-05-27T15:30:45.123456Z",
  "event_type": "restore",
  "profile_name": "base",
  "status": "success",
  "transaction_id": "uuid-here",
  "targets": ["~/.zshrc", "~/.gitconfig"]
}
```

View audit log:

```bash
cat ~/.config/rv/audit.log | jq '.'           # Pretty-print all events
jq '.[] | select(.status=="error")' ~/.config/rv/audit.log  # Errors only
tail -f ~/.config/rv/audit.log | jq '.'       # Stream new entries
```

---

## Security Best Practices

1. **Keep identity keys private** — treat as passwords. Store in password manager if needed.
2. **Rotate keys regularly** — every 6-12 months using `rv secret rotate`
3. **Back up identity keys securely** — not in the repo, use encrypted backup
4. **Use `--dry-run` first** — preview changes before applying: `rv restore base --dry-run`
5. **Review diffs before commit** — check what changed: `rv diff -p base`
6. **Enable audit logging** — log location is `~/.config/rv/audit.log`, reviewed periodically
7. **Restrict Web GUI access** — use loopback binding or reverse proxy with TLS
8. **Never commit secrets in plaintext** — always encrypt with `rv secret encrypt`
9. **Use machine overrides for host-specific secrets** — not global manifest
10. **Test restore on non-production first** — verify config on a test machine

---

## Known Security Limitations

| Limitation | Impact | Mitigation |
|-----------|--------|-----------|
| **Web GUI over HTTP** | No encryption in transit | Use SSH tunnel or reverse proxy with TLS |
| **No TLS support** | CORS wildcard on non-loopback is risky | Bind only to loopback, use reverse proxy |
| **Plugin sandbox is in-process** | Not a container/OS jail | Only run trusted plugins, review plugin code |
| **No fine-grained file permissions on backups** | Backup snapshots readable by user account | Restrict access to `~/.config/rv/backups/` |
| **Audit logs readable by user** | Logs contain operation details | Restrict access to `~/.config/rv/audit.log` |

---

## Vulnerability Reporting

If you discover a security vulnerability, please report it responsibly:

**Do not open a public issue.** Instead:

1. Email: security@revive-cli.dev (if available)
2. Or use GitHub's Security Advisory feature (if enabled)

Include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if available)

---

## Related Documentation

- [Plugin Security](plugins.md#plugin-execution--sandboxing)
- [Secret Encryption](../README.md#secrets)
- [Audit Logging](../ARCHITECTURE.md#state-model)
- [Security Model (Architecture)](../ARCHITECTURE.md#-security-architecture)
