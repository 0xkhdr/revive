# Extending Revive

This guide explains how to extend Revive with new capabilities: custom package providers, asset handlers, and plugins.

---

## Adding a New Package Provider

### Use Case

You want Revive to install packages from a package manager not currently supported (e.g., Pacman, Nix, Flatpak).

### Step 1: Create the Provider Class

Create `src/rv/providers/myprovider.py` extending `BaseProvider`:

```python
from rv.providers.base import BaseProvider, ProviderError


class MyProviderProvider(BaseProvider):
    """Orchestrates package installations via MyProvider."""

    def __init__(self) -> None:
        super().__init__("myprovider")

    def install(self, packages: list[str], dry_run: bool = False) -> None:
        """Install packages. Handles retries and caching."""
        if not packages:
            return

        # Check if provider is available
        if not self.is_available():
            raise ProviderError("myprovider command-line tool is not installed on this system.")

        if dry_run:
            # Log what would be installed, don't actually install
            logger.info(f"[Dry Run] Would install MyProvider packages: {', '.join(packages)}")
            return

        # Filter already-installed packages (uses cache if available)
        missing = self.filter_missing(packages)
        if not missing:
            logger.info("All packages already installed.")
            return

        # Build command and execute with exponential backoff retry
        cmd = ["myprovider", "install", "-y"] + missing
        try:
            self.execute_with_retry(cmd)
        except Exception as e:
            raise ProviderError(f"MyProvider installation failed: {e}") from e
```

**Key Methods**:
- `self.is_available()` — Check if the provider command exists in PATH
- `self.filter_missing(packages)` — Filter out already-installed packages (uses cache)
- `self.execute_with_retry(cmd)` — Execute with exponential backoff retry logic

### Step 2: Register the Provider

Add the provider to two places:

**1. `src/rv/services/restore.py`** (RestoreService):

```python
from rv.providers.myprovider import MyProviderProvider

# In RestoreService.restore():
if "myprovider" in packages:
    MyProviderProvider().install(packages["myprovider"], dry_run=dry_run)
```

**2. `src/rv/services/doctor.py`** (DoctorService):

```python
# In DoctorService._check_provider_availability():
MyProviderProvider(),  # Add to provider list
```

### Step 3: Write Tests

Create `tests/test_providers.py` tests:

```python
def test_myprovider_install_success(tmp_path):
    """Test successful package installation."""
    provider = MyProviderProvider()
    # Mock and verify install was called
    assert provider.is_available()

def test_myprovider_install_not_found():
    """Test when myprovider command is not available."""
    provider = MyProviderProvider()
    with pytest.raises(ProviderError):
        provider.install(["package1"])
```

### Step 4: Document in manifest.yaml

Users can now use the provider in `manifest.yaml`:

```yaml
packages:
  myprovider:
    - package1
    - package2
```

---

## Adding a New Asset Type

### Use Case

You want a new asset type beyond `symlink`, `copy`, and `template` (e.g., `download`, `git-clone`).

### Step 1: Add the Enum

Edit `src/rv/models/manifest.py`:

```python
from enum import Enum

class AssetType(str, Enum):
    """Asset deployment type."""
    SYMLINK = "symlink"
    COPY = "copy"
    TEMPLATE = "template"
    DOWNLOAD = "download"  # NEW
```

### Step 2: Implement the Handler

Edit `src/rv/services/handlers.py`:

```python
from rv.transactions.context import TransactionContext


class AssetHandler:
    """Handles asset type-specific deployment logic."""

    @classmethod
    def handle_asset(
        cls,
        asset: Asset,
        abs_source: str,
        abs_target: str,
        tx_context: TransactionContext
    ) -> None:
        """Route asset to appropriate handler based on type."""
        if asset.type == AssetType.DOWNLOAD:
            cls._handle_download(asset, abs_source, abs_target, tx_context)
        # ... other types ...

    @classmethod
    def _handle_download(
        cls,
        asset: Asset,
        abs_source: str,
        abs_target: str,
        tx_context: TransactionContext
    ) -> None:
        """Handle download asset type.
        
        The 'source' field contains a URL to download.
        The 'target' field is where to save it.
        """
        # Validate target
        if os.path.exists(abs_target):
            # Check for conflicts
            if asset.conflict_strategy == "skip":
                return
            elif asset.conflict_strategy == "abort":
                raise AssetHandlerError(f"Target exists: {abs_target}")

        # Download the file
        import urllib.request
        try:
            downloaded_data = urllib.request.urlopen(abs_source).read()
        except Exception as e:
            raise AssetHandlerError(f"Failed to download {abs_source}: {e}") from e

        # Plan the operation in the transaction
        tx_context.plan_operation(
            "copy",
            abs_target,
            source_data=downloaded_data,
            permissions=asset.permissions,
            owner=asset.owner
        )
```

### Step 3: Update Backup Logic

Edit `src/rv/services/backup.py` to skip new asset types that are non-reversible:

```python
def _backup_item(self, asset: Asset, ...) -> None:
    """Backup an asset. Skip non-reversible types."""
    if asset.type == AssetType.DOWNLOAD:
        # Can't reverse-engineer the original URL from the downloaded file
        logger.info(f"Skipping backup of {asset.type} asset: {asset.id}")
        return

    # ... continue with other types ...
```

### Step 4: Document It

Add to manifest schema documentation (README or docs/manifest-reference.md).

---

## Writing a Custom Plugin

See the full [Plugin Authoring Guide](plugins.md).

Quick example:

**`plugins/my-hook/plugin.yaml`:**

```yaml
name: "my-hook"
version: "1.0.0"
entrypoint: "run.py"
hooks:
  - post-restore
```

**`plugins/my-hook/run.py`:**

```python
import json
import os
import sys

def main() -> None:
    context = json.loads(os.environ.get("REVIVE_CONTEXT", "{}"))
    # Your logic here
    print(json.dumps({"status": "success"}))
    sys.exit(0)

if __name__ == "__main__":
    main()
```

---

## Code Standards

All contributions must adhere to:

1. **Strict Type Safety**: `mypy --strict src/rv` must pass
   ```bash
   def install(self, packages: list[str], dry_run: bool = False) -> None:
       ...
   ```

2. **No `shell=True`**: Always use argument lists
   ```python
   # ❌ Wrong
   subprocess.run("brew install " + " ".join(packages), shell=True)

   # ✅ Correct
   subprocess.run(["brew", "install"] + packages)
   ```

3. **Pydantic Strict Mode**: Never bypass validation
   ```python
   # ❌ Wrong
   manifest = Manifest(**raw_dict)

   # ✅ Correct
   manifest = Manifest.model_validate(raw_dict, strict=True)
   ```

4. **No Secret Leaks**: Register with `SecretScrubber`
   ```python
   from rv.security.scrubber import SecretScrubber
   
   scrubber = SecretScrubber()
   scrubber.register_pattern(r"API_KEY=\S+")
   ```

5. **Test Coverage**: Maintain >90% for core modules
   ```bash
   pytest --cov=src/rv --cov-fail-under=90
   ```

---

## Testing Your Extension

### Unit Tests

```python
import pytest
from rv.providers.myprovider import MyProviderProvider


def test_myprovider_install():
    """Test provider installation."""
    provider = MyProviderProvider()
    # Mock subprocess and verify
    assert provider.name == "myprovider"

def test_myprovider_not_available(monkeypatch):
    """Test when provider is not available."""
    import shutil
    monkeypatch.setattr(shutil, "which", lambda x: None)
    
    provider = MyProviderProvider()
    assert not provider.is_available()
```

### Integration Tests

```bash
# Run full test suite
pytest --cov=src/rv tests/

# Run specific test
pytest tests/test_providers.py::test_myprovider_install -v

# Check type safety
mypy src/rv

# Check code quality
ruff check src/rv
```

---

## Project Layout

Understanding the module structure helps when adding features:

```
src/rv/
├── cli/main.py              # User-facing commands
├── models/
│   ├── manifest.py          # Pydantic schemas
│   ├── transaction.py       # Transaction models
│   └── workspace.py         # Workspace registry
├── providers/
│   ├── base.py              # BaseProvider
│   ├── apt.py, brew.py, ... # Implementation
│   └── YOUR_PROVIDER.py     # Your new provider
├── services/
│   ├── restore.py           # Main restore orchestrator
│   ├── backup.py            # Backup service
│   ├── handlers.py          # Asset type handlers
│   ├── status.py            # Drift detection
│   ├── doctor.py            # Diagnostics
│   ├── recovery.py          # Rollback engine
│   └── workspace.py         # Workspace mgmt
├── transactions/
│   ├── context.py           # 7-step transaction
│   ├── atomic.py            # Atomic writes
│   └── lock.py              # Process lock
├── security/
│   ├── encryptor.py         # Age encryption
│   ├── permissions.py       # chmod enforcement
│   ├── scrubber.py          # Log scrubbing
│   ├── tempfile.py          # Secure temp files
│   └── zerobuffer.py        # In-memory zeroing
└── plugins/
    ├── loader.py            # Plugin discovery
    ├── sandbox.py           # Sandbox executor
    └── builtin/             # First-party plugins
```

---

## Contributing Back

If your extension is useful to others, consider contributing it back:

1. **Fork the repository**
2. **Create a feature branch**: `git checkout -b feat/add-myprovider`
3. **Make your changes** (follow code standards)
4. **Add tests** (maintain >90% coverage)
5. **Update documentation** (README, CONTRIBUTING, AGENTS.md)
6. **Open a pull request** with a clear description

See [Contributing Guide](../CONTRIBUTING.md) for the full workflow.

---

## Related Documentation

- [Plugin Authoring](plugins.md)
- [Architecture Guide](../ARCHITECTURE.md)
- [Contributing Guide](../CONTRIBUTING.md)
- [Security Guide](security.md)
