#!/bin/bash
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"

case "$(uname)" in
    Linux)  BIN="ccode" ;;
    Darwin) BIN="ccode-macos" ;;
    *) echo "Unsupported OS: $(uname)" >&2; exit 1 ;;
esac

mkdir -p "$HOME/bin"
ln -sf "$REPO/$BIN" "$HOME/bin/ccode"
echo "Installed: $HOME/bin/ccode -> $REPO/$BIN"
