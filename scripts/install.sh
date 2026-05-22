#!/usr/bin/env sh
set -eu

APP_NAME="rv"
PACKAGE_NAME="revive-cli"
REQUIRED_PYTHON="3.11"

REPO_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
INSTALL_ROOT="${REVIVE_INSTALL_DIR:-"$HOME/.local/share/rv"}"
VENV_DIR="$INSTALL_ROOT/venv"
BIN_DIR="${REVIVE_BIN_DIR:-"$HOME/.local/bin"}"
WRAPPER_PATH="$BIN_DIR/$APP_NAME"
FORCE=0
INSTALL_SYSTEM_DEPS="${REVIVE_INSTALL_SYSTEM_DEPS:-0}"

log() {
    printf '%s\n' "$*"
}

die() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

usage() {
    cat <<'EOF'
Usage: scripts/install.sh [options]

Install Revive (rv) for the current Linux user.

Options:
  --force               Recreate the installation venv and overwrite ~/.local/bin/rv.
  --system-deps         Best-effort install of Python/venv/pip/age with the system package manager.
  -h, --help            Show this help text.

Environment:
  REVIVE_INSTALL_DIR    Install root. Default: ~/.local/share/rv
  REVIVE_BIN_DIR        Wrapper directory. Default: ~/.local/bin
  PYTHON                Python executable to use. Must be Python 3.11+.
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --force)
            FORCE=1
            ;;
        --system-deps)
            INSTALL_SYSTEM_DEPS=1
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

command_exists() {
    command -v "$1" >/dev/null 2>&1
}

run_sudo() {
    if [ "$(id -u)" -eq 0 ]; then
        "$@"
    elif command_exists sudo; then
        sudo "$@"
    else
        die "sudo is required to install system dependencies as a non-root user"
    fi
}

install_system_deps() {
    log "Installing best-effort system dependencies..."

    if command_exists apt-get; then
        run_sudo apt-get update
        run_sudo apt-get install -y python3 python3-venv python3-pip age git
    elif command_exists dnf; then
        run_sudo dnf install -y python3 python3-pip age git
    elif command_exists yum; then
        run_sudo yum install -y python3 python3-pip age git
    elif command_exists zypper; then
        run_sudo zypper install -y python3 python3-pip age git
    elif command_exists pacman; then
        run_sudo pacman -Sy --needed --noconfirm python python-pip age git
    elif command_exists apk; then
        run_sudo apk add --no-cache python3 py3-pip age git
    else
        die "No supported package manager found. Install Python $REQUIRED_PYTHON+, venv, pip, age, and git manually."
    fi
}

python_ok() {
    "$1" - "$REQUIRED_PYTHON" <<'PY'
import sys

required = tuple(int(part) for part in sys.argv[1].split("."))
raise SystemExit(0 if sys.version_info[:2] >= required else 1)
PY
}

find_python() {
    if [ "${PYTHON:-}" ]; then
        python_ok "$PYTHON" || die "PYTHON must point to Python $REQUIRED_PYTHON+"
        printf '%s\n' "$PYTHON"
        return
    fi

    for candidate in python3.13 python3.12 python3.11 python3; do
        if command_exists "$candidate" && python_ok "$candidate"; then
            command -v "$candidate"
            return
        fi
    done

    return 1
}

if [ "$(uname -s)" != "Linux" ]; then
    die "This installer targets Linux. Use pip install -e . or PyInstaller on other platforms."
fi

if [ "$INSTALL_SYSTEM_DEPS" = "1" ]; then
    install_system_deps
fi

PYTHON_BIN=$(find_python || true)
if [ -z "$PYTHON_BIN" ]; then
    die "Python $REQUIRED_PYTHON+ was not found. Re-run with --system-deps or install Python manually."
fi

if [ -e "$WRAPPER_PATH" ] && [ "$FORCE" != "1" ]; then
    die "$WRAPPER_PATH already exists. Re-run with --force to overwrite it."
fi

if [ -d "$VENV_DIR" ] && [ "$FORCE" = "1" ]; then
    rm -rf "$VENV_DIR"
fi

mkdir -p "$INSTALL_ROOT" "$BIN_DIR"

log "Creating virtual environment at $VENV_DIR..."
"$PYTHON_BIN" -m venv "$VENV_DIR" || die "Failed to create venv. Install the Python venv package and retry."

log "Installing $PACKAGE_NAME from $REPO_DIR..."
"$VENV_DIR/bin/python" -m pip install --upgrade pip
"$VENV_DIR/bin/python" -m pip install --upgrade "$REPO_DIR"

cat > "$WRAPPER_PATH" <<EOF
#!/usr/bin/env sh
# Revive CLI Installer Wrapper
# Managed by $REPO_DIR/scripts/install.sh
exec "$VENV_DIR/bin/python" -m rv "\$@"
EOF
chmod 0755 "$WRAPPER_PATH"

log "Installed rv at $WRAPPER_PATH"
if ! printf '%s' ":$PATH:" | grep -q ":$BIN_DIR:"; then
    log "Add this to your shell profile if rv is not found:"
    log "  export PATH=\"$BIN_DIR:\$PATH\""
fi
