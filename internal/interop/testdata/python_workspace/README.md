# Interop fixture — Phase 14 gate

A complete workspace **restored by the reference Python implementation**, captured immediately
afterwards. `repo/` is the workspace including the `manifest.lock` that restore wrote; `state.json`
records every target it produced, with mode, modification time, and either content or symlink
destination; `identity.txt` is the age key the secret is encrypted to.

It was produced by driving `RestoreService.restore` directly:

```python
from rv.services.restore import RestoreService
RestoreService.restore(repo_dir=repo, profile_name="base", identity_path=identity,
                       interactive=False, dry_run=False, no_plugins=True)
```

Absolute paths were then replaced with the literal token `{{ROOT}}`, which
`TestInteropPythonRestoredWorkspace` substitutes for its own `t.TempDir()`.

**On the modification times.** Git does not preserve them, so the test restores each target's
mtime from `state.json` — the values Python's own restore produced, recorded at capture time, not
copied from the lockfile. That matters: rv's conflict resolution asks whether a target's mtime
matches what the *lockfile* recorded, and the assertion is only meaningful because the two were
captured independently.

The keys here are disposable test keys. Regenerating needs the reference's own dependencies
(`pydantic`, `jinja2`, `typer`, `rich`); the committed fixture means CI does not.
