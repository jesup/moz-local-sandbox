#!/bin/bash
set -euo pipefail

REPO="$(cd "$(dirname "$0")" && pwd)"

case "$(uname)" in
    Linux)  BIN="ccode" ;;
    Darwin) BIN="ccode-macos" ;;
    *) echo "Unsupported OS: $(uname)" >&2; exit 1 ;;
esac

# Prefer a writable bin dir already on $PATH; fall back to creating ~/.local/bin.
CANDIDATES=("$HOME/.local/bin" "$HOME/bin")

TARGET_DIR=""
for dir in "${CANDIDATES[@]}"; do
    if [[ ":$PATH:" == *":$dir:"* && -d "$dir" && -w "$dir" ]]; then
        TARGET_DIR="$dir"
        break
    fi
done

if [[ -z "$TARGET_DIR" ]]; then
    for dir in "${CANDIDATES[@]}"; do
        if [[ -d "$dir" && -w "$dir" ]]; then
            TARGET_DIR="$dir"
            break
        fi
    done
fi

if [[ -z "$TARGET_DIR" ]]; then
    TARGET_DIR="$HOME/.local/bin"
    read -r -p "Neither ~/.local/bin nor ~/bin exist. Create $TARGET_DIR? [y/N] " reply
    case "$reply" in
        [yY]|[yY][eE][sS]) mkdir -p "$TARGET_DIR" ;;
        *) echo "Aborted." >&2; exit 1 ;;
    esac
fi

ln -sf "$REPO/$BIN" "$TARGET_DIR/ccode"
echo "Installed: $TARGET_DIR/ccode -> $REPO/$BIN"

if [[ ":$PATH:" != *":$TARGET_DIR:"* ]]; then
    echo "Note: $TARGET_DIR is not on your \$PATH. Add it, e.g.:"
    echo "  export PATH=\"$TARGET_DIR:\$PATH\""
fi
