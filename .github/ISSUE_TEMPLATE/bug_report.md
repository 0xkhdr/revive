---
name: Bug report
about: Report a bug or unexpected behavior
title: "[BUG] "
labels: bug
assignees: ''

---

## Description

Brief description of the bug.

## Reproduction Steps

Steps to reproduce the behavior:

1. ...
2. ...
3. ...

## Expected Behavior

What you expected to happen.

## Actual Behavior

What actually happened instead.

## Environment

Please provide:

```bash
rv --version
python3 --version
uname -a
```

Or run this and paste the output:

```bash
rv doctor --json
```

## Relevant Log Output

If applicable, enable verbose logging and share relevant excerpts:

```bash
rv --verbose restore base
# [paste output here]
```

Audit log (redacted):

```bash
cat ~/.config/rv/audit.log | jq '.[-1]'
# [paste last entry here, redacting any secrets]
```

## Additional Context

Any other context that might help (OS, installed tools, custom plugins, etc.).

---

## Checklist

- [ ] I've searched existing issues for duplicates
- [ ] I've read the [Troubleshooting Guide](https://github.com/0xkhdr/revive/blob/main/TROUBLESHOOTING.md)
- [ ] I've enabled `--verbose` to provide detailed logs
- [ ] My manifest.yaml is valid (checked with `rv doctor`)
