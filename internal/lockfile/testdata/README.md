# Interop fixtures

`python_manifest.lock` was produced by the **reference Python implementation**'s own `Lockfile`
model (`model_dump_json(indent=2)`), with absolute paths replaced by the literal token `{{ROOT}}`.
It backs `TestInteropPythonLockfile`, the phase 8 gate.

`tree/` plus `tree_sha256.json` back `TestChecksumMatchesThePythonImplementation`. The digests
were computed by `reference/`'s `RestoreService.calculate_sha256`, so the directory walk order —
every file in a directory before descending, both in name order — is pinned to Python's `os.walk`
behavior rather than to Go's `filepath.WalkDir`, which interleaves differently and would produce
a different digest for the same tree.

Regenerating either needs the reference's own dependencies (`pydantic`, `jinja2`); the committed
fixtures mean CI does not.
