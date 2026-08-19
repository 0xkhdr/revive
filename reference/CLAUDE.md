# Claude Code & AI Agent Reference

This document provides guidance for Claude Code users and AI agents working on the Revive codebase.

---

## Quick Start: Local Development

```bash
# 1. Clone and setup
git clone https://github.com/0xkhdr/revive.git
cd revive
python3 -m venv .venv
source .venv/bin/activate   # Windows: .venv\Scripts\activate
pip install -e ".[dev]"

# 2. Run quality checks
ruff format src/rv tests       # Format code
ruff check src/rv tests         # Lint
mypy src/rv                     # Type checking
bandit -r src/rv -ll           # Security scan

# 3. Run tests
pytest --cov=src/rv --cov-fail-under=90

# 4. Pre-commit hooks
pip install pre-commit
pre-commit install              # Auto-runs checks before commit
```

---

## Code Standards (Non-Negotiable)

All code in `src/rv/` must meet these standards before merging:

### 1. Strict Type Annotations

**Requirement**: `mypy --strict src/rv` must pass with zero warnings.

```python
# ❌ Wrong
def restore(profile, dry_run=False):
    return manifest

# ✅ Correct
def restore(profile: str, dry_run: bool = False) -> Manifest:
    return manifest
```

### 2. No `shell=True`

**Requirement**: Never execute subprocesses with shell=True. Always use argument lists.

```python
# ❌ Wrong
subprocess.run(f"pip install {package}", shell=True)

# ✅ Correct
subprocess.run(["pip", "install", package])
```

### 3. Pydantic Strict Mode

**Requirement**: All Pydantic models use `strict=True`. Never bypass validation.

```python
# ❌ Wrong
manifest = Manifest(**raw_dict)  # May coerce types

# ✅ Correct
manifest = Manifest.model_validate(raw_dict, strict=True)
```

### 4. Secret Registration

**Requirement**: All secrets must be registered with `SecretScrubber` before any logging.

```python
from rv.security.scrubber import SecretScrubber

# Register patterns
scrubber = SecretScrubber()
scrubber.register_pattern(r"AGE-SECRET-KEY-\S+")
scrubber.register_pattern(r"aws_secret_access_key=\S+")

# Log safely
logger.info(scrubber.scrub(f"Decrypted secret: {secret_value}"))
```

### 5. No Placeholder Logic

**Requirement**: Transaction/recovery engines must have complete implementations. No stubs, `TODO` comments, or mock operations.

```python
# ❌ Wrong (in transaction context)
def rollback(self) -> None:
    # TODO: implement rollback
    pass

# ✅ Correct
def rollback(self) -> None:
    for operation in reversed(self.journal):
        if operation["type"] == "copy":
            self._rollback_copy(operation)
        elif operation["type"] == "symlink":
            self._rollback_symlink(operation)
```

### 6. Test Coverage >90%

**Requirement**: Core modules (`core/`, `security/`, `services/`, `transactions/`) maintain >90% coverage.

```bash
# Check coverage before committing
pytest --cov=src/rv --cov-fail-under=90 --cov-report=term-missing -q
```

---

## Module Responsibilities

| Module | Responsibility | Key Files |
|--------|-----------------|-----------|
| `cli/` | User-facing commands, Rich formatting | `cli/main.py` |
| `models/` | Pydantic validation schemas | `manifest.py`, `transaction.py`, `workspace.py` |
| `services/` | Business logic orchestration | `restore.py`, `backup.py`, `handlers.py` |
| `transactions/` | Atomic operations, rollback | `context.py`, `atomic.py`, `lock.py` |
| `security/` | Crypto, permissions, secret handling | `encryptor.py`, `scrubber.py`, `zerobuffer.py` |
| `providers/` | Package manager integrations | `base.py`, `apt.py`, `brew.py`, etc. |
| `plugins/` | Plugin discovery, sandbox execution | `loader.py`, `sandbox.py`, `sandbox_wrapper.py` |
| `gui/` | Web dashboard server | `server.py` (uses `http.server`) |
| `logging/` | Dual JSON/Rich logger | `audit.py` |
| `utils/` | Path handling, env interpolation | `path.py`, `interpolate.py` |
| `watchers/` | File watching daemon | `daemon.py` |

---

## Key Data Structures

### Manifest

```python
from rv.models.manifest import Manifest, Asset, Secret, Profile

manifest = Manifest.model_validate(yaml_dict, strict=True)

# Access nested structures
for profile_name, profile in manifest.profiles.items():
    print(f"Profile {profile_name} has assets: {profile.assets}")
```

### Transaction Journal

Each restore creates a journal at `~/.config/rv/backups/<tx_id>/journal.json`:

```json
{
  "tx_id": "uuid-here",
  "profile_name": "base",
  "status": "committed",
  "operations": [
    {"type": "backup", "target": "~/.zshrc"},
    {"type": "symlink", "target": "~/.zshrc"},
    {"type": "chmod", "target": "~/.zshrc", "permissions": "0644"}
  ]
}
```

---

## Common Patterns

### Adding a Service Method

All service methods should:
1. Validate input (Pydantic models)
2. Lock if mutating (`ProcessLock`)
3. Log operations
4. Return typed result

```python
from rv.transactions.lock import ProcessLock
from rv.security.scrubber import SecretScrubber

class MyService:
    def do_something(self, profile_name: str, dry_run: bool = False) -> dict:
        """Do something safely."""
        # Validation
        if not profile_name:
            raise ValueError("profile_name required")

        # Lock for mutations
        if not dry_run:
            with ProcessLock():
                return self._mutate(profile_name)
        else:
            return self._plan(profile_name)

    def _mutate(self, profile_name: str) -> dict:
        # Scrub secrets
        scrubber = SecretScrubber()
        # ... mutation logic ...
        return {"status": "success"}
```

### Adding Provider Support

New providers extend `BaseProvider`:

```python
from rv.providers.base import BaseProvider, ProviderError

class MyProvider(BaseProvider):
    def __init__(self) -> None:
        super().__init__("myprovider")

    def install(self, packages: list[str], dry_run: bool = False) -> None:
        if not packages:
            return
        if not self.is_available():
            raise ProviderError("myprovider not installed")
        if dry_run:
            return
        self.execute_with_retry(["myprovider", "install"] + packages)

    def is_available(self) -> bool:
        import shutil
        return shutil.which(self.name) is not None
```

Then register in `RestoreService.restore()` and `DoctorService`.

### Testing with Mocking

Use `unittest.mock` for isolated tests:

```python
from unittest.mock import patch, MagicMock

@patch("shutil.which")
def test_provider_not_available(mock_which):
    mock_which.return_value = None
    provider = MyProvider()
    assert not provider.is_available()
```

---

## Running Tests

```bash
# All tests with coverage
pytest --cov=src/rv --cov-fail-under=90 -q

# Specific test file
pytest tests/test_restore.py -v

# Specific test
pytest tests/test_restore.py::test_restore_happy_path -v

# Match pattern
pytest -k "test_restore" -v

# Show print statements
pytest -s tests/test_restore.py::test_restore_happy_path

# Fail fast (stop at first failure)
pytest -x

# Last 10 failed tests
pytest --lf
```

---

## Debugging Tips

### Enable Verbose Logging

```bash
rv --verbose restore base 2>&1 | head -100
```

### Inspect Audit Log

```bash
cat ~/.config/rv/audit.log | jq '.[-5:]'  # Last 5 entries
jq '.[] | select(.status=="error")' ~/.config/rv/audit.log  # Errors only
```

### Dry-Run Preview

Always preview before applying:

```bash
rv restore base --dry-run
rv restore base --preview
```

### Debug a Specific Service

```python
# In Python REPL or test:
from rv.services.restore import RestoreService
from rv.models.manifest import Manifest

manifest = Manifest.model_validate(yaml.safe_load(open("manifest.yaml")))
service = RestoreService()
# ... call methods and inspect state ...
```

### Check Transaction Journals

```bash
# List all transaction backups
ls -la ~/.config/rv/backups/

# Inspect a specific journal
cat ~/.config/rv/backups/<tx_id>/journal.json | jq '.'

# Find failed transactions
jq '.[] | select(.status!="committed")' ~/.config/rv/audit.log
```

---

## Known Gotchas

### 1. Tests Run with flock

The `ProcessLock` uses `flock` on `~/.config/rv/rv.lock`. Running parallel tests will deadlock. Tests are serialized by default.

**Fix**: `pytest` runs tests serially. If you run tests with `pytest -n`, you'll hit deadlocks. Don't do that.

### 2. test_coverage_booster.py

There's a `test_coverage_booster.py` file in `tests/` that is **not** a standard test — it's used to artificially boost coverage for tested-but-uncovered paths. Don't delete it.

### 3. Pydantic Strict Mode

The `Manifest` model is **strictly validated**. If you accidentally pass a dict instead of a Pydantic model, it will fail. Always use `Manifest.model_validate(dict, strict=True)`.

```python
# ❌ Wrong
manifest = Manifest(**my_dict)

# ✅ Correct
manifest = Manifest.model_validate(my_dict, strict=True)
```

### 4. Secret Leaks in Tests

If a test creates an age-encrypted secret, make sure to clean it up:

```python
def test_secret_encrypt(tmp_path):
    # Create temp secret
    secret_file = tmp_path / "secret.age"
    # ... encrypt ...
    
    # Clean up!
    secret_file.unlink()
```

---

## Pre-Commit Hooks

The `.pre-commit-config.yaml` runs:
- `ruff format` (formatting)
- `ruff check` (linting)
- `mypy` (type checking)
- `bandit` (security)

Commit will fail if any check fails. Fix and re-commit:

```bash
git add src/rv/file.py
git commit -m "fix: improve performance"
# Checks fail? ruff format runs auto-fix, then:
git add src/rv/file.py
git commit -m "fix: improve performance"  # Try again
```

---

## Adding Documentation

Keep these rules in mind when writing docs:

1. **Code examples**: Keep them short and runnable
2. **Links**: Use relative links within the repo (`[link](../README.md)`)
3. **Consistency**: Follow existing doc structure
4. **No comments in code examples**: Let the code speak for itself
5. **Keep it current**: Update docs when code changes

---

## Common CI Failures

| Error | Cause | Fix |
|-------|-------|-----|
| `mypy: error: Missing return type` | Function missing return type annotation | Add `-> ReturnType` |
| `ruff: Line too long` | Line > 120 chars | Wrap or shorten |
| `bandit: Possible SQL injection` | Dynamic SQL without parameterization | Use prepared statements |
| `pytest: AssertionError: assert X == Y` | Test assertion failed | Debug test logic |
| `coverage: coverage is <90%` | Test coverage below threshold | Add more tests |

---

## Questions or Issues?

- **Code questions**: Check `CONTRIBUTING.md` and `AGENTS.md`
- **Architecture questions**: See `ARCHITECTURE.md`
- **Security questions**: See `docs/security.md`
- **Plugin questions**: See `docs/plugins.md`
- **General troubleshooting**: See `TROUBLESHOOTING.md`

---

## See Also

- [Contributing Guide](CONTRIBUTING.md)
- [Architecture Guide](ARCHITECTURE.md)
- [AGENTS.md](AGENTS.md) — detailed developer reference
- [docs/](docs/) — user documentation
