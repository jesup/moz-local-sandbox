#!/bin/bash
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$HOME/bin"
ln -sf "$REPO/ccode" "$HOME/bin/ccode"
echo "Installed: $HOME/bin/ccode -> $REPO/ccode"
