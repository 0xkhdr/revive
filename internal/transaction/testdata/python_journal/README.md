# Interop fixture — Phase 5 gate

`journal.json` and everything under `backups/` were produced by the **reference Python
implementation**'s `TransactionContext`, driven by `../python_journal_writer.py`:

```sh
python3 internal/transaction/testdata/python_journal_writer.py /some/workdir
```

The transaction is left in the `executing` state, which is exactly what a killed restore leaves
behind. Absolute paths were then replaced with the literal token `{{ROOT}}`, which
`TestInteropRollbackPythonJournal` substitutes for its own `t.TempDir()`. Nothing else was
touched: the field names, the explicit `null`s, the `0o640` permission spelling and the
`SYMLINK:<target>` backup format are all Python's own output.

Regenerating needs `pydantic` (the reference's own dependency); the committed fixture means CI
does not.
