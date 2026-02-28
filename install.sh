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

TAG="$VERSION"           # e.g. "v0.1.0"  (used in the URL)
VERSION="${TAG#v}"       # e.g.  "0.1.0"  (used in the filename)

echo "Installing guget $TAG ($OS/$ARCH)..."

# ── Download and extract ──────────────────────────────────────────────────────
FILENAME="guget_${VERSION}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$TAG/$FILENAME"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

CHECKSUM_URL="https://github.com/$REPO/releases/download/$TAG/checksums.txt"

if command -v curl &>/dev/null; then
  curl -fsSL "$URL" -o "$TMP/$FILENAME"
  curl -fsSL "$CHECKSUM_URL" -o "$TMP/checksums.txt"
else
  wget -qO "$TMP/$FILENAME" "$URL"
  wget -qO "$TMP/checksums.txt" "$CHECKSUM_URL"
fi

# ── Verify checksum ──────────────────────────────────────────────────────────
EXPECTED=$(grep "$FILENAME" "$TMP/checksums.txt" | awk '{print $1}')
if [ -z "$EXPECTED" ]; then
  echo "Error: checksum not found for $FILENAME"; exit 1
fi

if command -v sha256sum &>/dev/null; then
  ACTUAL=$(sha256sum "$TMP/$FILENAME" | awk '{print $1}')
elif command -v shasum &>/dev/null; then
  ACTUAL=$(shasum -a 256 "$TMP/$FILENAME" | awk '{print $1}')
else
  echo "Warning: sha256sum/shasum not found, skipping checksum verification"
  ACTUAL="$EXPECTED"
fi

if [ "$ACTUAL" != "$EXPECTED" ]; then
  echo "Error: checksum mismatch"
  echo "  expected: $EXPECTED"
  echo "  actual:   $ACTUAL"
  exit 1
fi
echo "Checksum verified."

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
    fi
    ;;
esac

# Export into the current shell session.
# When sourced (. <(curl -fsSL ...)) this takes effect immediately.
# When run as a subshell (curl ... | bash) it has no effect on the parent,
# but the profile update above ensures guget is available in new terminals.
export PATH="$INSTALL_DIR:$PATH"

echo "Done! $(guget --version)"
