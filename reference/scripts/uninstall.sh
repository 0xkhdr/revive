#!/usr/bin/env sh
set -eu

INSTALL_ROOT="${REVIVE_INSTALL_DIR:-"$HOME/.local/share/rv"}"
BIN_DIR="${REVIVE_BIN_DIR:-"$HOME/.local/bin"}"
WRAPPER_PATH="$BIN_DIR/rv"
FORCE=0

die() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

log() {
    printf '%s\n' "$*"
}

usage() {
    cat <<'EOF'
Usage: scripts/uninstall.sh [options]

Uninstall the user-local Revive (rv) package installed by scripts/install.sh.

Options:
  --force       Remove ~/.local/bin/rv even if it does not look installer-managed.
  -h, --help    Show this help text.

Environment:
  REVIVE_INSTALL_DIR    Install root. Default: ~/.local/share/rv
  REVIVE_BIN_DIR        Wrapper directory. Default: ~/.local/bin
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --force)
            FORCE=1
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "Unknown option: $1"
            ;;
    esac
    shift
done

if [ -f "$WRAPPER_PATH" ]; then
    if grep -q "Revive CLI Installer Wrapper" "$WRAPPER_PATH" || [ "$FORCE" = "1" ]; then
        rm -f "$WRAPPER_PATH"
        log "Removed $WRAPPER_PATH"
    else
        log "Skipped $WRAPPER_PATH because it was not created by scripts/install.sh. Use --force to remove it."
    fi
else
    log "No rv wrapper found at $WRAPPER_PATH"
fi

if [ -d "$INSTALL_ROOT" ]; then
    rm -rf "$INSTALL_ROOT"
    log "Removed $INSTALL_ROOT"
else
    log "No install root found at $INSTALL_ROOT"
fi

log "Revive uninstall complete."
