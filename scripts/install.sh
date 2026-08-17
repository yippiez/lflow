#!/usr/bin/env bash
#
# scripts/install.sh — build lflow from source and install it to
# ~/.local/bin (no sudo required). Run from the repo root:
# $ ./scripts/install.sh
#

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

BLACK='\033[30;1m'
GREEN='\033[32;1m'
RESET='\033[0m'

print_step() {
  printf "${BLACK}%s${RESET}\n" "$1"
}

print_success() {
  printf "${GREEN}%s${RESET}\n" "$1"
}

if ! command -v go >/dev/null 2>&1; then
  echo "go is required to install lflow. Install Go and try again." >&2
  exit 1
fi

bindir=${bindir:-"$HOME/.local/bin"}
mkdir -p "${bindir}"

print_step "Building lflow..."
go build --tags fts5 -o "${bindir}/lflow" ./tui/cmd/lflow

print_success "lflow was successfully installed to ${bindir}/lflow."

case ":${PATH}:" in
  *":${bindir}:"*) ;;
  *) echo "Note: ${bindir} is not on your PATH. Add it to your shell profile." ;;
esac
