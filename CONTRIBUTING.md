# Contributing to Revive (`rv`)

Thank you for contributing to Revive. This guide gets you from zero to a passing CI in under 10 minutes.

---

## Prerequisites

- Python 3.11, 3.12, or 3.13
- Git

---

## Local Setup

```bash
# Clone the repository
git clone <your-fork-url>
cd revive

# Create and activate a virtual environment
python -m venv .venv
source .venv/bin/activate  # On Windows: .venv\Scripts\activate

# Install all dependencies (including dev tools)
pip install -e ".[dev]"

# Install pre-commit hooks (runs ruff, mypy, bandit on every commit)
pip install pre-commit
pre-commit install
```

---

## Running the Quality Checks

All checks below must pass before submitting a PR. They mirror the CI pipeline exactly.

```bash
# Format code
ruff format src/rv tests

# Lint
ruff check src/rv tests

# Type check (strict)
mypy src/rv

# Security scan
bandit -r src/rv -ll

# Full test suite with coverage
pytest --cov=src/rv --cov-fail-under=90 --cov-report=term-missing -q
```

---

## Code Standards

All contributions must comply with **AGENTS.md §6**:

| Rule | Enforcement |
|------|------------|
| Strict type annotations | `mypy --strict` |
| Line length ≤ 120 chars | `ruff format` |
| No `shell=True` in subprocess calls | `ruff check` (S603 rule) |
| No plaintext secrets in logs | `bandit -ll` |
| No stub/TODO in transaction/security code | Code review |
| Test coverage ≥ 90% for `services/`, `security/`, `transactions/` | `pytest --cov-fail-under=90` |

---

## Writing Tests

- Tests live in `tests/` and follow the `test_<module>.py` naming convention
- Use `unittest.mock.patch` for subprocess calls — never invoke real package managers in unit tests
- Use `tmp_path` (pytest fixture) for filesystem operations
- Mirror the pattern in `tests/test_providers.py` for any new provider tests

---

## Submitting a Pull Request

1. **Create a branch** off `main`: `git checkout -b feat/your-feature`
2. **Make your changes** with proper type annotations and docstrings
3. **Run all quality checks** (see above) — pre-commit will catch most issues
4. **Push and open a PR** against `main`
5. **Link any related issues** in the PR description

### Commit Message Format

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add rv clone command
fix: resolve workspace path deduplication edge case
chore(deps): bump pydantic to 2.7.0
docs: add plugin-api.md
refactor(providers): extract filter_missing into BaseProvider
test: add cargo/dnf/nix/pacman/pip provider coverage
```

### Branch Naming

| Type | Pattern |
|------|---------|
| Feature | `feat/<short-description>` |
| Bug fix | `fix/<short-description>` |
| Chore/deps | `chore/<short-description>` |
| Documentation | `docs/<short-description>` |

---

## Architecture Quick Reference

See `AGENTS.md` for the full module layout, 14-step restore process, and plugin sandbox model.

Key directories:
- `src/rv/services/` — Core business logic (restore, backup, status, workspace)
- `src/rv/providers/` — Package manager orchestrators
- `src/rv/security/` — Encryption, scrubbers, permission enforcement
- `src/rv/transactions/` — Atomic writes, transaction context, lock
- `src/rv/plugins/` — Plugin loader and subprocess sandbox

---

## Getting Help

- Open a [GitHub Discussion](../../discussions) for design questions
- Open a [GitHub Issue](../../issues) for bugs with a minimal reproduction case
