#!/usr/bin/env bash
# Exercise the download installer without network access or a global install.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/guget-install-test.XXXXXX")"
trap 'rm -rf "$TEST_ROOT"' EXIT
mkdir -p "$TEST_ROOT/tools" "$TEST_ROOT/release" "$TEST_ROOT/archive"

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) echo "This test requires Linux or macOS" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) echo "Unsupported test architecture" >&2; exit 1 ;;
esac

cat > "$TEST_ROOT/archive/guget" <<'EOF'
#!/bin/sh
printf 'guget installer-test\n'
EOF
chmod +x "$TEST_ROOT/archive/guget"
ARCHIVE="guget_installer-test_${OS}_${ARCH}.tar.gz"
tar -czf "$TEST_ROOT/release/$ARCHIVE" -C "$TEST_ROOT/archive" guget
if command -v sha256sum >/dev/null; then
  (cd "$TEST_ROOT/release" && sha256sum "$ARCHIVE") > "$TEST_ROOT/release/checksums.txt"
else
  (cd "$TEST_ROOT/release" && shasum -a 256 "$ARCHIVE") > "$TEST_ROOT/release/checksums.txt"
fi

cat > "$TEST_ROOT/tools/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "$2" == */releases/latest ]]; then
  printf '{"tag_name": "vinstaller-test"}\n'
else
  cp "$GUGET_TEST_RELEASE/${2##*/}" "$4"
fi
EOF
chmod +x "$TEST_ROOT/tools/curl"

# The destination intentionally does not exist and contains a space.
INSTALL_PATH="$TEST_ROOT/new install/bin"
env GUGET_TEST_RELEASE="$TEST_ROOT/release" GUGET_INSTALL="$INSTALL_PATH" \
  PATH="$INSTALL_PATH:$TEST_ROOT/tools:$PATH" \
  bash "$REPO_ROOT/.scripts/install.sh"
test -x "$INSTALL_PATH/guget"
test "$("$INSTALL_PATH/guget" --version)" = 'guget installer-test'

# A checksum failure must preserve an already installed executable.
printf 'invalid  %s\n' "$ARCHIVE" > "$TEST_ROOT/release/checksums.txt"
if env GUGET_TEST_RELEASE="$TEST_ROOT/release" GUGET_INSTALL="$INSTALL_PATH" \
    PATH="$INSTALL_PATH:$TEST_ROOT/tools:$PATH" \
    bash "$REPO_ROOT/.scripts/install.sh" > "$TEST_ROOT/rejected.log" 2>&1; then
  echo "Installer accepted an invalid checksum" >&2
  exit 1
fi
grep -q 'checksum mismatch' "$TEST_ROOT/rejected.log"
test "$("$INSTALL_PATH/guget" --version)" = 'guget installer-test'
echo 'Installer destination and checksum regression checks passed.'
