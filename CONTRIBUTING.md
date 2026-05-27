# Contributing to Revive (`rv`)

Thank you for contributing to Revive. This guide gets you from zero to a passing CI in under 15 minutes.

---

## Table of Contents

- [Prerequisites](#prerequisites)
- [Local Setup](#local-setup)
- [Quality Checks](#quality-checks)
- [Code Standards](#code-standards)
- [Writing Tests](#writing-tests)
- [Submitting a Pull Request](#submitting-a-pull-request)
- [Branch Naming](#branch-naming)
- [Adding New Features](#adding-new-features)
- [Getting Help](#getting-help)

---

## Prerequisites

| Tool | Minimum Version | Notes |
|------|----------------|-------|
| Python | 3.11 | 3.12 recommended |
| Git | any | Required |
| age | any | Required for secret-related tests |

---

## Local Setup

```bash
# 1. Clone your fork
git clone https://github.com/<your-username>/revive.git
cd revive

# 2. Create and activate a virtual environment
python3 -m venv .venv
source .venv/bin/activate   # Windows: .venv\Scripts\activate

# 3. Install all dependencies (including dev tools)
pip install -e ".[dev]"

# 4. Install pre-commit hooks
pip install pre-commit
pre-commit install
```

Pre-commit runs `ruff`, `mypy`, and `bandit` automatically on every `git commit`.

---

## Quality Checks

All checks must pass before submitting a PR. They mirror the CI pipeline exactly.

```bash
# Format code (line length: 120)
ruff format src/rv tests

# Lint
ruff check src/rv tests

# Static type checking (strict mode)
mypy src/rv

# Security scan (medium + high severity)
bandit -r src/rv -ll

# Full test suite with coverage gate
pytest --cov=src/rv --cov-fail-under=90 --cov-report=term-missing -q
```

All commands are also available through the `.venv/bin/` prefix when the venv is not activated.

---

## Code Standards

All contributions must comply with **[AGENTS.md §6](AGENTS.md)**:

| Rule | Enforcement |
|------|------------|
| Strict type annotations throughout `src/rv/` | `mypy --strict` |
| Line length ≤ 120 characters | `ruff format` |
| No `shell=True` in any subprocess call | `ruff check` (S603) |
| No plaintext secrets in logs or traces | `bandit -ll` + code review |
| No stub/TODO/`NotImplementedError` in `transactions/`, `security/`, `services/` | Code review |
| Test coverage ≥ 90% for `services/`, `security/`, `transactions/` | `pytest --cov-fail-under=90` |
| All secrets registered with `SecretScrubber` before logging | Code review |
| Pydantic strict mode — never bypass with raw dicts | Code review |

---

## Writing Tests

- Tests live in `tests/` and follow the `test_<module>.py` naming convention.
- **Unit tests**: Use `unittest.mock.patch` for all subprocess calls. Never invoke real
  package managers, real age CLI, or real filesystem side effects in unit tests.
- **Fixtures**: Use `tmp_path` (pytest fixture) for all temporary filesystem operations.
- **Integration tests**: Live in `tests/integration/`. Run inside Docker via CI.
  See `tests/integration/Dockerfile.*` for the matrix.
- **Pattern**: Mirror `tests/test_providers.py` for new provider tests;
  mirror `tests/test_transactions.py` for transaction context tests.

### Test Coverage Requirements

| Module | Minimum Coverage |
|--------|-----------------|
| `src/rv/services/` | 90% |
| `src/rv/security/` | 90% |
| `src/rv/transactions/` | 90% |
| `src/rv/providers/` | 85% |
| Overall | 76%+ (improving) |

---

## Submitting a Pull Request

1. **Branch** off `main`: `git checkout -b feat/your-feature`
2. **Make changes** with proper type annotations and docstrings
3. **Run all quality checks** (pre-commit catches most issues automatically)
4. **Push** and open a PR against `main`
5. **Describe** what the change does and why in the PR body
6. **Link** any related issues

### Commit Message Format

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add rv clone command
fix: resolve workspace path deduplication edge case
chore(deps): bump pydantic to 2.7.0
docs: add plugin-api.md
refactor(providers): extract filter_missing into BaseProvider
test: add cargo/dnf/nix/pacman/pip provider coverage
security: tighten CORS policy in GUI server
```

**Scopes** (optional but encouraged): `cli`, `gui`, `services`, `providers`, `security`,
`transactions`, `plugins`, `models`, `utils`, `ci`, `docs`, `deps`.

---

## Branch Naming

| Type | Pattern | Example |
|------|---------|---------|
| Feature | `feat/<short-description>` | `feat/dnf-provider` |
| Bug fix | `fix/<short-description>` | `fix/zero-buffer-offset` |
| Security | `security/<description>` | `security/cors-tighten` |
| Chore/deps | `chore/<description>` | `chore/bump-pydantic` |
| Documentation | `docs/<description>` | `docs/architecture-md` |
| Release | `release/v<semver>` | `release/v1.1.0` |

> [!IMPORTANT]
> Direct pushes to `main` and `master` are blocked by pre-commit hooks.
> All changes go through a PR.

---

## Adding New Features

### New Package Provider

See [AGENTS.md §5.1](AGENTS.md) for the full extension pattern.

1. Create `src/rv/providers/<name>.py` extending `BaseProvider`
2. Register in `RestoreService.restore()` (`src/rv/services/restore.py`)
3. Register in `DoctorService` (`src/rv/services/doctor.py`)
4. Add tests in `tests/test_providers.py` (mirror existing provider tests)

### New Asset Handler

See [AGENTS.md §5.4](AGENTS.md) for the full extension pattern.

1. Add enum value to `AssetType` in `src/rv/models/manifest.py`
2. Add `_handle_<type>()` classmethod to `AssetHandler` in `src/rv/services/handlers.py`
3. Add tests in `tests/test_services.py`

### New CLI Command

1. Add a decorated function in `src/rv/cli/main.py` using `@app.command()`
2. Follow the existing pattern: accept optional `manifest` path, call a service, print
   via `console` (Rich)
3. Add tests in `tests/test_cli.py`

---

## Architecture Quick Reference

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full module map, data flow, and ADRs.

Key directories:

| Path | Responsibility |
|------|---------------|
| `src/rv/services/` | Core business logic (restore, backup, status, workspace) |
| `src/rv/providers/` | Package manager orchestrators |
| `src/rv/security/` | Encryption, scrubbers, permission enforcement |
| `src/rv/transactions/` | Atomic writes, transaction context, flock |
| `src/rv/plugins/` | Plugin loader and subprocess sandbox |
| `src/rv/models/` | Pydantic v2 schema definitions |
| `src/rv/cli/` | Typer CLI application |
| `src/rv/gui/` | HTTP-based web dashboard |

---

## Getting Help

- Open a [GitHub Discussion](../../discussions) for design questions or proposals.
- Open a [GitHub Issue](../../issues) for bugs — include a minimal reproduction case.
- See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) for common errors and fixes.
