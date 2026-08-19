## Description

Brief description of what this PR does.

## Type of Change

- [ ] Bug fix (non-breaking)
- [ ] New feature (non-breaking)
- [ ] Breaking change (requires version bump)
- [ ] Documentation update
- [ ] Refactoring
- [ ] Dependency update

## Motivation & Context

Why is this change needed? Link any related issues: `Fixes #123`

## Testing

How did you test this change? Describe the test plan:

- [ ] Unit tests added/updated
- [ ] Manual testing completed
- [ ] Tested on: Linux / macOS / [other]
- [ ] Coverage maintained (>90% for core modules)

## Checklist

### Code Quality

- [ ] `gofmt -l .` reports nothing
- [ ] `go vet ./...` passes
- [ ] `golangci-lint run` passes
- [ ] Tests pass: `go test -race -cover ./...` (>90% on `internal/`)

### Documentation

- [ ] README.md updated (if user-facing changes)
- [ ] CHANGELOG.md updated
- [ ] Code comments added (only for non-obvious logic)
- [ ] Doc comments updated on exported identifiers

### Security & Standards

- [ ] No shell invocation in subprocess calls (argv slices only)
- [ ] All secrets registered with the scrubber before any logging
- [ ] No hardcoded credentials or API keys
- [ ] Decrypted plaintext kept in `[]byte` and zeroed in a `defer`
- [ ] Errors wrapped with `%w`; no branching on error message text

### Breaking Changes

- [ ] If breaking: documented in PR description
- [ ] If breaking: version bump considered (e.g., 1.0.0 → 1.1.0)

---

**Related Issues**: Closes #...

**Reviewers**: @...
