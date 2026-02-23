#!/usr/bin/env bash
set -e

REPO="nulifyer/guget"
INSTALL_DIR="${INSTALL_DIR:-}"

# ── OS detection ──────────────────────────────────────────────────────────────
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  linux)  OS="linux"  ;;
  darwin) OS="darwin" ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# ── Arch detection ────────────────────────────────────────────────────────────
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)        ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# ── Fetch latest release tag ──────────────────────────────────────────────────
if command -v curl &>/dev/null; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | sed 's/.*"tag_name": *"\(.*\)".*/\1/')
elif command -v wget &>/dev/null; then
  VERSION=$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | sed 's/.*"tag_name": *"\(.*\)".*/\1/')
else
  echo "Error: curl or wget is required"; exit 1
fi

if [ -z "$VERSION" ]; then
  echo "Error: could not determine latest version"; exit 1
fi

echo "Installing guget $VERSION ($OS/$ARCH)..."

# ── Download and extract ──────────────────────────────────────────────────────
FILENAME="guget_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$VERSION/$FILENAME"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

if command -v curl &>/dev/null; then
  curl -fsSL "$URL" -o "$TMP/$FILENAME"
else
  wget -qO "$TMP/$FILENAME" "$URL"
fi

tar -xzf "$TMP/$FILENAME" -C "$TMP"

# ── Install binary ────────────────────────────────────────────────────────────
if [ -z "$INSTALL_DIR" ]; then
  if [ -w "/usr/local/bin" ]; then
    INSTALL_DIR="/usr/local/bin"
  else
    INSTALL_DIR="$HOME/.local/bin"
    mkdir -p "$INSTALL_DIR"
  fi
fi

install -m 755 "$TMP/guget" "$INSTALL_DIR/guget"

echo "Installed to $INSTALL_DIR/guget"

# ── Add to PATH if needed ─────────────────────────────────────────────────────
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;  # already in PATH, nothing to do
  *)
    # Detect shell profile
    case "${SHELL##*/}" in
      zsh)  PROFILE="$HOME/.zshrc" ;;
      fish) PROFILE="$HOME/.config/fish/config.fish" ;;
      *)    PROFILE="$HOME/.bashrc" ;;
    esac

    LINE="export PATH=\"$INSTALL_DIR:\$PATH\""
    if [ "${SHELL##*/}" = "fish" ]; then
      LINE="fish_add_path $INSTALL_DIR"
    fi

    if ! grep -qF "$INSTALL_DIR" "$PROFILE" 2>/dev/null; then
      printf '\n# Added by guget installer\n%s\n' "$LINE" >> "$PROFILE"
      echo "Added $INSTALL_DIR to PATH in $PROFILE"
      echo "Restart your terminal or run: source $PROFILE"
    fi
    ;;
esac

echo "Done! Run 'guget --version' to verify."
