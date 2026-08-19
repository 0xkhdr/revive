# Development

Requires the Go version declared in `go.mod` (currently 1.26.4). No runtime dependency is needed
for the built binary.

```console
$ go test ./...
$ go test -race ./...
$ golangci-lint run ./...
$ go build ./cmd/rv
```

Run the narrow package test while editing, then the full suite before handoff. The race run matters
for concurrent planning, watching, and scrubbing. Release builds are defined by `.goreleaser.yaml`
and target Linux/macOS on amd64/arm64 with CGO disabled.

## Change rules

- Keep `cmd/rv` and `internal/cli` thin. Put behavior in the package that owns the domain.
- Reuse `paths.Config`, command runners, clocks, loggers, and confirmation callbacks. Do not read
  the real home directory, execute host commands, or prompt directly from business logic.
- Preserve manifest v1/v2, lockfile, journal, encryption, and XDG layout compatibility unless a
  migration is explicitly designed and documented.
- Decode user configuration strictly. Validate all trust-boundary input before snapshots or
  mutation. Preserve scalar/list shapes where compatibility requires them.
- Use sentinel errors with wrapping and `errors.Is`/`errors.As`; never branch on error text.
- Use atomic write and transaction helpers for managed state. Never add an alternate mutation path
  that bypasses snapshots, verification, or rollback.
- Execute subprocesses with argument arrays and contexts. Do not introduce an implicit shell,
  unbounded process, hidden privilege escalation, or uncancellable I/O.
- Keep hooks and plugins explicit about their non-rollbackable effects. Do not claim plugin
  declarations provide OS-level confinement.
- Add the smallest test that fails before the change and passes after it. Prefer `t.TempDir()` and
  injected fakes; no network, package installation, global config, or user files in tests.
- Update documentation when CLI flags, manifest fields, file formats, defaults, security
  guarantees, or operational recovery behavior changes.

## Compatibility checks

The interoperability gates use fixtures produced by the archived Python implementation:

| Package | Contract |
|---|---|
| `internal/crypto` | Python ciphertext and Go ciphertext remain mutually readable. |
| `internal/transaction` | Go can recover a Python transaction journal. |
| `internal/lockfile` | Python lockfiles load and preserve their data shape. |
| `internal/interop` | A Python-restored workspace remains in sync under Go. |

When old and new behavior differ, establish the public behavior from tests and current product
requirements, add a focused interop/regression test, then document the divergence in
[Migration.md](Migration.md). `reference/` is not built in normal Go CI and is not a source to
modify while implementing Go behavior.

## Review checklist

- `go test ./...` passes; `go test -race ./...` passes for concurrency changes.
- `golangci-lint run ./...` passes.
- Dry-run and diagnostic paths remain read-only.
- Failure after snapshot rolls back; failure before snapshot leaves no journal or mutation.
- Secret values, identities, command output, and errors cannot leak through logs.
- New manifest input is strict, validated, documented, and covered by accept/reject fixtures.
- Generated and runtime files remain ignored; no identity, plaintext secret, or local `.env` is
  staged.
- The smallest responsible package changed; unrelated reference behavior remains intact.
