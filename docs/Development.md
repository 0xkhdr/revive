# Development

Revive is a single Go binary. The CLI and generated file formats are public; packages under
`internal/` are not a library API.

```console
$ go test ./...
$ go test -race ./...
$ golangci-lint run ./...
$ go build ./cmd/rv
```

Use the Go version declared in `go.mod`. Run the narrow package test while editing, then the full
suite before handoff. Release builds are defined by `.goreleaser.yaml`.

## Runtime

`rv restore` loads and validates the manifest, resolves profiles and the machine override, plans
assets, snapshots targets, runs hooks and mutations, installs packages, verifies results, and
commits the journal and lockfile. Planning and dry-run are read-only. A failure after the snapshot
restores managed filesystem state; external process effects cannot be rolled back.

The main ownership boundaries are:

- `internal/cli`: commands, flags, prompts, and rendering.
- `internal/manifest` and `internal/profile`: schema, validation, inheritance, and overrides.
- `internal/engine`: restore and backup orchestration.
- `internal/transaction` and `internal/recovery`: atomic mutation, journals, and rollback.
- `internal/status` and `internal/doctor`: read-only drift and diagnostics.
- `internal/crypto`, `internal/scrub`, and `internal/permissions`: secrets and filesystem safety.
- `internal/providers` and `internal/plugins`: external package managers and plugin processes.

## Change rules

- Keep `cmd/rv` and `internal/cli` thin. Put behavior in the package that owns the domain.
- Reuse `paths.Config`, command runners, clocks, loggers, and confirmation callbacks. Do not read
  the real home directory, execute host commands, or prompt directly from business logic.
- Preserve supported manifest versions, lockfiles, journals, encryption, and XDG paths unless the
  format change is explicitly designed and documented.
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

## Review checklist

- `go test ./...` passes; `go test -race ./...` passes for concurrency changes.
- `golangci-lint run ./...` passes.
- Dry-run and diagnostic paths remain read-only.
- Failure after snapshot rolls back; failure before snapshot leaves no journal or mutation.
- Secret values, identities, command output, and errors cannot leak through logs.
- New manifest input is strict, validated, documented, and covered by accept/reject fixtures.
- Generated and runtime files remain ignored; no identity, plaintext secret, or local `.env` is
  staged.
- The smallest responsible package changed; unrelated behavior remains intact.
