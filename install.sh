#!/bin/bash

# mdview build and install script
# Builds and installs the binary after tests and static checks pass.
# If the project has a config.json, it is also deployed to ~/.config/mdview/config.json.
#
# Usage:
#   ./install.sh              # test + vet + build + install to ~/.local/bin + copy config
#   ./install.sh --build-only # build only (skip install and config copy)
#   PREFIX=/usr/local ./install.sh   # change install location (default: $HOME/.local)

set -euo pipefail
cd "$(dirname "$0")"

BIN="mdview"
LDFLAGS="-s -w"
PREFIX="${PREFIX:-$HOME/.local}"
INSTALL_DIR="$PREFIX/bin"
CONFIG_SRC="config.json"
CONFIG_DIR="$HOME/.config/mdview"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: Go is not installed.${NC}"
    echo "To install: brew install go"
    exit 1
fi

echo -e "${GREEN}=== Test ===${NC}"
go test ./...

echo -e "${GREEN}=== Static checks ===${NC}"
go vet ./...

echo -e "${GREEN}=== Build ===${NC}"
CGO_ENABLED=0 go build -ldflags="$LDFLAGS" -o "$BIN" ./cmd/
echo "Built: $(pwd)/$BIN ($(du -h "$BIN" | cut -f1 | tr -d ' '))"

if [[ "${1:-}" == "--build-only" ]]; then
    echo -e "${GREEN}Done (build only)${NC}"
    exit 0
fi

echo -e "${GREEN}=== Install ===${NC}"
mkdir -p "$INSTALL_DIR"
install -m 0755 "$BIN" "$INSTALL_DIR/$BIN"
echo "Installed: $INSTALL_DIR/$BIN"

# Deploy config: project config.json -> ~/.config/mdview/config.json (0600 to protect the API key)
if [[ -f "$CONFIG_SRC" ]]; then
    mkdir -p "$CONFIG_DIR"
    install -m 0600 "$CONFIG_SRC" "$CONFIG_DIR/config.json"
    echo "Config copied: $CONFIG_DIR/config.json"
else
    echo -e "${YELLOW}Note: $CONFIG_SRC not found; skipping config copy (see config.example.json).${NC}"
fi

if ! command -v "$BIN" &> /dev/null; then
    echo -e "${YELLOW}Note: '$INSTALL_DIR' is not on your PATH. Add the following to your shell config.${NC}"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
fi

echo -e "${GREEN}Done${NC}"
