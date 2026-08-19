# Compatibility fixtures

`python_manifest.lock` captures an earlier release's lockfile shape with absolute paths replaced
by `{{ROOT}}`. It backs `TestInteropPythonLockfile`.

`tree/` and `tree_sha256.json` pin the earlier directory checksum order: files in each directory
before descending, both in name order. The committed fixtures keep these checks self-contained.
