#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────────────────
# build-install.sh — build guget from source and install it locally
#   Usage: ./build-install.sh
# ──────────────────────────────────────────────────────────────────────────────
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"
SOURCE_DIR="$REPO_ROOT/guget"

# ── Helpers ──────────────────────────────────────────────────────────────────
GREEN='\033[32m'; RED='\033[31m'; CYAN='\033[36m'
MAGENTA='\033[35m'; DIM='\033[2m'; BOLD='\033[1m'; RESET='\033[0m'

ok()   { echo -e "  ${GREEN}✓${RESET}  $*"; }
fail() { echo -e "  ${RED}✗  $*${RESET}" >&2; exit 1; }
dim()  { echo -e "  ${DIM}$*${RESET}"; }

echo -e "\n${BOLD}${MAGENTA}  guget — build from source${RESET}"
echo -e "${DIM}  ────────────────────────────────────────────────────${RESET}"

# ── Pre-flight ───────────────────────────────────────────────────────────────
command -v go &>/dev/null || fail "Go is not installed or not in PATH"
ok "Go found: $(go version)"

[[ -f "$SOURCE_DIR/go.mod" ]] || fail "Cannot find $SOURCE_DIR/go.mod — run this script from the repo root"

# ── Version ──────────────────────────────────────────────────────────────────
VERSION="dev"
if DESC=$(git -C "$REPO_ROOT" describe --tags --always 2>/dev/null); then
    VERSION="${DESC#v}"
fi
dim "Version: $VERSION"

# ── Build ────────────────────────────────────────────────────────────────────
echo ""
echo -e "  ${BOLD}${CYAN}▸ Building...${RESET}"

export CGO_ENABLED=0
LDFLAGS="-s -w -X main.version=$VERSION"
OUT_PATH="$REPO_ROOT/guget-build"

( cd "$SOURCE_DIR" && go build -ldflags "$LDFLAGS" -o "$OUT_PATH" . )
ok "Built $OUT_PATH"

# ── Install ──────────────────────────────────────────────────────────────────
echo ""
echo -e "  ${BOLD}${CYAN}▸ Installing...${RESET}"

if [[ -n "${GUGET_INSTALL:-}" ]]; then
    INSTALL_DIR="$GUGET_INSTALL"
elif [[ -w "/usr/local/bin" ]]; then
    INSTALL_DIR="/usr/local/bin"
else
    INSTALL_DIR="$HOME/.local/bin"
fi

mkdir -p "$INSTALL_DIR"
install -m 755 "$OUT_PATH" "$INSTALL_DIR/guget"
ok "Installed to $INSTALL_DIR/guget"

# ── Clean up build artifact ──────────────────────────────────────────────────
rm -f "$OUT_PATH"

# ── Add to PATH if needed ────────────────────────────────────────────────────
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    case "${SHELL##*/}" in
      zsh)  PROFILE="$HOME/.zshrc" ;;
      fish) PROFILE="$HOME/.config/fish/config.fish" ;;
      *)    PROFILE="$HOME/.bashrc" ;;
    esac

    LINE="export PATH=\"$INSTALL_DIR:\$PATH\""
    if [[ "${SHELL##*/}" == "fish" ]]; then
      LINE="fish_add_path $INSTALL_DIR"
    fi

    if ! grep -qF "$INSTALL_DIR" "$PROFILE" 2>/dev/null; then
      printf '\n# Added by guget build-install\n%s\n' "$LINE" >> "$PROFILE"
      ok "Added $INSTALL_DIR to PATH in $PROFILE"
    fi

    export PATH="$INSTALL_DIR:$PATH"
    ;;
esac

# ── Verify ───────────────────────────────────────────────────────────────────
echo ""
VER_OUTPUT=$("$INSTALL_DIR/guget" --version 2>&1 || true)
ok "Verified: $VER_OUTPUT"

echo -e "\n  ${BOLD}${GREEN}Done!${RESET}\n"
