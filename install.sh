#!/bin/sh
set -e

REPO="mugunth13labs/cli-ask"
LATEST_TAG=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST_TAG" ]; then
    LATEST_TAG="v0.3.1"
fi

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64|amd64)
        ARCH="amd64"
        ;;
    arm64|aarch64)
        ARCH="arm64"
        ;;
    *)
        echo "Error: Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

BINARY="ask-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/download/${LATEST_TAG}/${BINARY}"

# Determine installation directory:
# Prefer /usr/local/bin if root or writable, or if sudo is available.
# Otherwise fallback to ~/.local/bin without breaking system PATH.
if [ -w "/usr/local/bin" ]; then
    INSTALL_DIR="/usr/local/bin"
    USE_SUDO=0
elif command -v sudo >/dev/null 2>&1; then
    INSTALL_DIR="/usr/local/bin"
    USE_SUDO=1
else
    INSTALL_DIR="$HOME/.local/bin"
    USE_SUDO=0
fi

DEST="${INSTALL_DIR}/ask"
TMP_FILE="$(mktemp /tmp/ask-dl.XXXXXX)"

cleanup() {
    rm -f "$TMP_FILE"
}
trap cleanup EXIT

echo "Downloading ask (${OS}/${ARCH}) from ${LATEST_TAG}..."
curl -fsSL "$URL" -o "$TMP_FILE"
chmod +x "$TMP_FILE"

if [ "$USE_SUDO" -eq 1 ]; then
    echo "Installing to ${DEST} (requires sudo permissions):"
    sudo install -m 755 "$TMP_FILE" "$DEST"
else
    mkdir -p "$INSTALL_DIR"
    install -m 755 "$TMP_FILE" "$DEST"
fi

echo "Successfully installed ask to ${DEST}"

# If installed in ~/.local/bin and not yet in PATH, advise cleanly without corrupting rc files
case ":$PATH:" in
    *":${INSTALL_DIR}:"*)
        ;;
    *)
        echo ""
        echo "Notice: ${INSTALL_DIR} is not in your current PATH."
        echo "Add it to your PATH by adding this line to your shell configuration file:"
        echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
        ;;
esac

echo "Run 'ask --help' to get started."
