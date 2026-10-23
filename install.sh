#!/bin/sh
set -e

REPO="mugunth13labs/cli-ask"
LATEST_TAG=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST_TAG" ]; then
    LATEST_TAG="v0.1.0"
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
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

BINARY="ask-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/download/${LATEST_TAG}/${BINARY}"
DEST="/usr/local/bin/ask"

echo "Downloading ask (${OS}/${ARCH})..."
if [ -w "/usr/local/bin" ]; then
    curl -fsSL "$URL" -o "$DEST"
    chmod +x "$DEST"
else
    echo "Installing to /usr/local/bin requires sudo permissions:"
    sudo curl -fsSL "$URL" -o "$DEST"
    sudo chmod +x "$DEST"
fi

echo "Successfully installed ask to $DEST"
echo "Run 'ask --help' to get started."
