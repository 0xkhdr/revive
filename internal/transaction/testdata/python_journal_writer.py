"""Drives the reference Python TransactionContext to produce a real journal + backups.

Used by TestInteropRollbackPythonJournal. Argv: <workdir>. Leaves the transaction in the
`executing` state, which is what a crashed restore looks like on disk.
"""

import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "..", "reference", "src"))

from rv.transactions.context import TransactionContext  # noqa: E402

work = os.path.abspath(sys.argv[1])
journal_dir = os.path.join(work, "journals")
backup_root = os.path.join(work, "backups")
targets = os.path.join(work, "targets")
sources = os.path.join(work, "sources")
for d in (journal_dir, backup_root, targets, sources):
    os.makedirs(d, exist_ok=True)

# Pre-state: a regular file, a symlink, and a directory — one of each backup shape.
with open(os.path.join(targets, "file.conf"), "w") as f:
    f.write("original file contents\n")
os.chmod(os.path.join(targets, "file.conf"), 0o640)

with open(os.path.join(sources, "linked"), "w") as f:
    f.write("link destination\n")
os.symlink(os.path.join(sources, "linked"), os.path.join(targets, "link"))

os.makedirs(os.path.join(targets, "dir", "nested"), exist_ok=True)
with open(os.path.join(targets, "dir", "nested", "inner.txt"), "w") as f:
    f.write("original nested contents\n")

# New source content the transaction writes over the top.
with open(os.path.join(sources, "new.conf"), "w") as f:
    f.write("REPLACED\n")
os.makedirs(os.path.join(sources, "newdir"), exist_ok=True)
with open(os.path.join(sources, "newdir", "other.txt"), "w") as f:
    f.write("replacement tree\n")

ctx = TransactionContext(tx_id="interop-fixture")
ctx.journal_dir = journal_dir
ctx.backup_dir = os.path.join(backup_root, ctx.tx_id)
ctx.journal_path = os.path.join(journal_dir, f"{ctx.tx_id}.json")

ctx.plan_operation("copy", os.path.join(targets, "file.conf"), os.path.join(sources, "new.conf"), permissions="0644")
ctx.plan_operation("symlink", os.path.join(targets, "link"), os.path.join(sources, "new.conf"))
ctx.plan_operation("copy", os.path.join(targets, "dir"), os.path.join(sources, "newdir"))
ctx.plan_operation("copy", os.path.join(targets, "fresh.conf"), os.path.join(sources, "new.conf"))

ctx.snapshot()
ctx.execute()
# Deliberately no commit(): the journal stays in `executing`, exactly like a killed run.
ctx.status = "executing"
ctx._write_journal()

print(json.dumps({"journal": ctx.journal_path, "targets": targets}))
