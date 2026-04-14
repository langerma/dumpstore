#!/bin/sh
# install.sh — build and install dumpstore from source
# Run as root from the repository root directory.
#
# Usage:
#   sudo ./install.sh              # install or upgrade
#   sudo ./install.sh --uninstall  # remove everything

set -e

die() { echo "error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "this script must be run as root (sudo ./install.sh)"
[ -f "main.go" ]     || die "run this script from the dumpstore repository root"

case "${1:-}" in
    --uninstall|-u) make uninstall ;;
    "")             make install   ;;
    *) echo "usage: sudo ./install.sh [--uninstall]" >&2; exit 1 ;;
esac
