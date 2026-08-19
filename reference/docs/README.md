# Revive Documentation Hub

Welcome to Revive's comprehensive documentation. Choose a guide based on what you want to do.

---

## For End Users

### New to Revive?

**[Getting Started Guide](../README.md#-quick-start)** — Install Revive and restore your first profile (5 minutes).

### Bootstrapping a New Machine

**[New Machine Setup](new-machine.md)** — Clone your dotfiles repository and auto-restore everything in one command. This is the golden path for setting up a fresh machine.

### Manifest Configuration

**[Manifest Reference](manifest-reference.md)** — Complete guide to `manifest.yaml` schema, including assets, secrets, packages, profiles, and machine-specific overrides.

### Common Tasks

- **View drift**: `rv status -p <profile>`
- **Preview changes**: `rv restore <profile> --dry-run`
- **Capture system changes back to repo**: `rv backup <profile>`
- **Watch for changes**: `rv watch -p <profile>`
- **Manage secrets**: See [Security Guide](security.md)

### Having Issues?

See the **[Troubleshooting Guide](../TROUBLESHOOTING.md)** for common errors and solutions.

---

## For Developers & Contributors

### Understanding the Architecture

**[Architecture Guide](../ARCHITECTURE.md)** — Module map, data flows, transaction engine, and design decisions.

### Extending Revive

**[Extending Revive](extending.md)** — Add custom package providers, new asset types, plugins, and other capabilities.

### Security Model

**[Security Guide](security.md)** — Plugin sandbox, age encryption, secret handling, CORS, and security best practices.

### Contributing

**[Contributing Guide](../CONTRIBUTING.md)** — Setup, quality checks, code standards, tests, and PR workflow.

---

## For Advanced Users

### Custom Plugins

**[Plugin Authoring](plugins.md)** — Write custom plugins that run before and after `rv restore` with filesystem, network, and shell sandboxing.

### Multiple Manifests

Use `--manifest` / `-m` flag to switch between different configurations:

```bash
# Development environment
rv restore base -m manifest-build.yaml

# Production environment
rv restore base -m manifest-restore.yaml
```

Each manifest gets its own `.lock` file for separate state tracking.

### Machine Overrides

Create `machine/<hostname>.yaml` to override manifest values on specific hosts (see [Manifest Reference](manifest-reference.md#machine-overrides)).

### Audit Logs

All operations are logged to `~/.config/rv/audit.log` in JSON format:

```bash
cat ~/.config/rv/audit.log | jq '.'
tail -f ~/.config/rv/audit.log | jq '.operation'
```

---

## Quick Links

| Document | For | Time |
|----------|-----|------|
| [README](../README.md) | Everyone | 15 min overview |
| [New Machine Setup](new-machine.md) | Fresh system setup | 5 min |
| [Manifest Reference](manifest-reference.md) | Config writers | Reference |
| [Troubleshooting](../TROUBLESHOOTING.md) | Problem solving | As-needed |
| [Security Guide](security.md) | Security-conscious users | 10 min |
| [Architecture](../ARCHITECTURE.md) | Code readers | 20 min |
| [Plugin Authoring](plugins.md) | Plugin writers | 15 min |
| [Extending](extending.md) | Contributors | 20 min |
| [Contributing](../CONTRIBUTING.md) | Developers | 10 min |

---

## API & Reference

- **CLI Reference**: See [README CLI Command Reference](../README.md#-cli-command-reference)
- **Pydantic Models**: See source code in `src/rv/models/`
- **Security Model**: [Security Guide](security.md)
- **Transaction Engine**: [Architecture Guide](../ARCHITECTURE.md#-transaction--rollback-engine)
