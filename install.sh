#!/bin/sh
# install.sh — build and install dumpstore from source
# Run as root from the repository root directory.
#
# Usage:
#   sudo ./install.sh              # install or upgrade
#   sudo ./install.sh --uninstall  # remove everything

set -e

INSTALL_DIR="/usr/local/lib/dumpstore"
SYSTEMD_SERVICE="/etc/systemd/system/dumpstore.service"
RC_SERVICE="/usr/local/etc/rc.d/dumpstore"
BINARY="dumpstore"

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------

die() { echo "error: $*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || die "'$1' not found in PATH — please install it first"
}

# ---------------------------------------------------------------------------
# uninstall
# ---------------------------------------------------------------------------

do_uninstall() {
    OS=$(uname -s)
    echo "==> Stopping and removing dumpstore..."
    case "$OS" in
        Linux)
            systemctl disable --now dumpstore 2>/dev/null || true
            rm -f "$SYSTEMD_SERVICE"
            systemctl daemon-reload
            ;;
        FreeBSD)
            service dumpstore stop 2>/dev/null || true
            sysrc -x dumpstore_enable 2>/dev/null || true
            rm -f "$RC_SERVICE"
            ;;
    esac
    rm -rf "$INSTALL_DIR"
    echo "==> dumpstore uninstalled."
}

# ---------------------------------------------------------------------------
# install
# ---------------------------------------------------------------------------

do_install() {
    OS=$(uname -s)

    echo "==> Checking prerequisites..."
    need go
    need ansible-playbook
    need git
    command -v lego >/dev/null 2>&1 || echo "  [warn] lego not found — ACME cert issuance (Let's Encrypt) will not be available"

    # Check we're in the repo root
    [ -f "main.go" ]    || die "run this script from the dumpstore repository root"
    [ -d "playbooks" ]  || die "playbooks/ directory not found"
    [ -d "static" ]     || die "static/ directory not found"

    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
    echo "==> Building dumpstore $VERSION..."
    go build -buildvcs=false -ldflags="-s -w -X main.version=$VERSION" -o "$BINARY" .

    echo "==> Installing to $INSTALL_DIR..."
    install -d "$INSTALL_DIR"
    install -m 0755 "$BINARY" "$INSTALL_DIR/$BINARY"
    rm -f "$BINARY"

    # Copy support directories, replacing any existing content cleanly.
    rm -rf "$INSTALL_DIR/playbooks" "$INSTALL_DIR/static"
    cp -r playbooks "$INSTALL_DIR/"
    cp -r static    "$INSTALL_DIR/"

    echo "==> Configuring authentication..."
    case "$OS" in
        FreeBSD) CONFIG_DIR="/usr/local/etc/dumpstore" ;;
        *)       CONFIG_DIR="/etc/dumpstore" ;;
    esac
    CONFIG_FILE="$CONFIG_DIR/dumpstore.conf"
    install -d -m 0700 "$CONFIG_DIR"
    # Only prompt for a password if none is set yet (safe to re-run on upgrade).
    if ! grep -q '"password_hash"' "$CONFIG_FILE" 2>/dev/null || grep -q '"password_hash": ""' "$CONFIG_FILE" 2>/dev/null; then
        echo "==> Set admin password (used to log in to the web UI):"
        "$INSTALL_DIR/$BINARY" --set-password --config "$CONFIG_FILE"
    else
        echo "==> Password already configured, skipping."
    fi

    echo "==> Setting up service..."
    case "$OS" in
        Linux)
            install -m 0644 contrib/dumpstore.service "$SYSTEMD_SERVICE"
            systemctl daemon-reload
            systemctl enable --now dumpstore
            echo "==> Done. dumpstore is running on http://localhost:8080"
            echo "    Logs: journalctl -u dumpstore -f"
            ;;
        FreeBSD)
            install -m 0555 contrib/dumpstore.rc "$RC_SERVICE"
            sysrc dumpstore_enable=YES
            service dumpstore restart 2>/dev/null || service dumpstore start
            echo "==> Done. dumpstore is running on http://localhost:8080"
            echo "    Logs: service dumpstore status"
            ;;
        *)
            echo "==> Warning: unknown OS '$OS' — binary and assets installed but service not registered."
            echo "    Start manually: $INSTALL_DIR/$BINARY -addr :8080 -dir $INSTALL_DIR -config $CONFIG_FILE"
            ;;
    esac
}

# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

[ "$(id -u)" -eq 0 ] || die "this script must be run as root (sudo ./install.sh)"

case "${1:-}" in
    --uninstall|-u) do_uninstall ;;
    "")             do_install   ;;
    *) echo "usage: sudo ./install.sh [--uninstall]" >&2; exit 1 ;;
esac
