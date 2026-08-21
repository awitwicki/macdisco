#!/bin/sh
# macdisco installer
# usage: curl -fsSL https://raw.githubusercontent.com/awitwicki/macdisco/main/install.sh | sh
set -eu

REPO="awitwicki/macdisco"
BIN="macdisco"

if [ "$(uname -s)" != "Darwin" ]; then
    echo "macdisco is macOS-only." >&2
    exit 1
fi

ARCH=$(uname -m) # arm64 | x86_64
case "$ARCH" in
    arm64 | x86_64) ;;
    *)
        echo "unsupported architecture: $ARCH" >&2
        exit 1
        ;;
esac

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# Prefer a prebuilt binary from the latest GitHub release.
ASSET="macdisco-darwin-$ARCH.tar.gz"
URL=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null |
    grep -o "\"browser_download_url\": *\"[^\"]*$ASSET\"" |
    head -1 | sed 's/.*"\(https[^"]*\)"/\1/') || true

if [ -n "${URL:-}" ]; then
    echo "Downloading $ASSET ..."
    curl -fsSL "$URL" -o "$TMP/$ASSET"
    tar -xzf "$TMP/$ASSET" -C "$TMP"
else
    echo "No prebuilt release found; building from source (requires Go)..."
    if ! command -v go >/dev/null 2>&1; then
        echo "Go is not installed. Install it first (brew install go) and re-run." >&2
        exit 1
    fi
    curl -fsSL "https://github.com/$REPO/archive/refs/heads/main.tar.gz" -o "$TMP/src.tar.gz"
    tar -xzf "$TMP/src.tar.gz" -C "$TMP"
    (cd "$TMP/macdisco-main" && go build -trimpath -ldflags "-s -w" -o "$TMP/$BIN" .)
fi

# Install to /usr/local/bin when possible, ~/.local/bin otherwise.
DEST="/usr/local/bin"
if [ -d "$DEST" ] && [ -w "$DEST" ]; then
    install -m 755 "$TMP/$BIN" "$DEST/$BIN"
elif [ -t 1 ] && command -v sudo >/dev/null 2>&1; then
    echo "Installing to $DEST (sudo may ask for your password)..."
    sudo mkdir -p "$DEST"
    sudo install -m 755 "$TMP/$BIN" "$DEST/$BIN"
else
    DEST="$HOME/.local/bin"
    mkdir -p "$DEST"
    install -m 755 "$TMP/$BIN" "$DEST/$BIN"
    case ":$PATH:" in
        *":$DEST:"*) ;;
        *) echo "NOTE: add $DEST to your PATH, e.g.: echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.zshrc" ;;
    esac
fi

echo "✓ installed $("$DEST/$BIN" -v)"
echo "Run:  macdisco ~        # analyze your home folder"
echo "      macdisco /        # analyze the whole disk"
