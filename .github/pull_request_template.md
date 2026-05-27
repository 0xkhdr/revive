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

- [ ] `ruff format src/rv tests` passes
- [ ] `ruff check src/rv tests` passes
- [ ] `mypy src/rv` passes (strict mode)
- [ ] `bandit -r src/rv` passes
- [ ] Tests pass: `pytest --cov=src/rv --cov-fail-under=90`

### Documentation

- [ ] README.md updated (if user-facing changes)
- [ ] CHANGELOG.md updated
- [ ] Code comments added (only for non-obvious logic)
- [ ] Docstrings updated

### Security & Standards

- [ ] No `shell=True` in subprocess calls
- [ ] All secrets registered with `SecretScrubber`
- [ ] No hardcoded credentials or API keys
- [ ] Type annotations are complete (`mypy --strict`)
- [ ] Pydantic models use strict validation

### Breaking Changes

- [ ] If breaking: documented in PR description
- [ ] If breaking: version bump considered (e.g., 1.0.0 → 1.1.0)

---

**Related Issues**: Closes #...

**Reviewers**: @...
