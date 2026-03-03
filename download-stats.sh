#!/usr/bin/env bash
set -euo pipefail

REPO="nulifyer/guget"
BAR_CHAR="#"
BAR_MAX=40

# Colors (Gruvbox)
C_WIN='\033[38;5;109m'  # blue
C_LIN='\033[38;5;142m'  # yellow
C_MAC='\033[38;5;175m'  # purple
C_DIM='\033[2m'
C_RST='\033[0m'

RAW=$(curl -fsSL "https://api.github.com/repos/$REPO/releases")

# ── Downloads by Version (stacked by platform) ──────────────────────────────
# Output: tag \t linux_count \t darwin_count \t windows_count \t total
VERSIONED=$(echo "$RAW" | jq -r '
  [.[] | . as $rel | {
    tag: .tag_name,
    linux: ([.assets[] | select(.name | test("linux")) | .download_count] | add // 0),
    darwin: ([.assets[] | select(.name | test("darwin")) | .download_count] | add // 0),
    windows: ([.assets[] | select(.name | test("windows")) | .download_count] | add // 0)
  } | .total = (.linux + .darwin + .windows)
  ] | sort_by(.tag | ltrimstr("v") | split(".") | map(tonumber))
  | .[] | "\(.tag)\t\(.linux)\t\(.darwin)\t\(.windows)\t\(.total)"')

if [[ -z "$VERSIONED" ]]; then
  echo "No release data found."; exit 1
fi

MAX=$(echo "$VERSIONED" | awk -F'\t' '{if($5>m)m=$5} END{print m}')
if [[ "$MAX" -eq 0 ]]; then MAX=1; fi

TOTAL=0
echo ""
echo "  Downloads by Version"
echo "  ──────────────────────────────────────────────────────────"
printf "  %10s   ${C_LIN}## linux${C_RST}  ${C_MAC}## darwin${C_RST}  ${C_WIN}## windows${C_RST}\n" ""
echo ""

while IFS=$'\t' read -r tag linux darwin windows total; do
  TOTAL=$((TOTAL + total))
  BAR_TOTAL=$(( total * BAR_MAX / MAX ))
  if [[ "$total" -gt 0 && "$BAR_TOTAL" -eq 0 ]]; then BAR_TOTAL=1; fi

  # Split bar proportionally among platforms
  if [[ "$total" -gt 0 ]]; then
    LIN_LEN=$(( BAR_TOTAL * linux / total ))
    MAC_LEN=$(( BAR_TOTAL * darwin / total ))
    WIN_LEN=$(( BAR_TOTAL - LIN_LEN - MAC_LEN ))
  else
    LIN_LEN=0; MAC_LEN=0; WIN_LEN=0
  fi

  LIN_BAR=$(printf "%${LIN_LEN}s" | tr ' ' "$BAR_CHAR")
  MAC_BAR=$(printf "%${MAC_LEN}s" | tr ' ' "$BAR_CHAR")
  WIN_BAR=$(printf "%${WIN_LEN}s" | tr ' ' "$BAR_CHAR")

  BAR="${C_LIN}${LIN_BAR}${C_MAC}${MAC_BAR}${C_WIN}${WIN_BAR}${C_RST}"
  PAD_LEN=$(( BAR_MAX - BAR_TOTAL ))
  PAD=$(printf "%${PAD_LEN}s" "")

  printf "  %10s | ${BAR}${PAD} %d\n" "$tag" "$total"
done <<< "$VERSIONED"

echo ""
echo "  ──────────────────────────────────────────────────────────"
echo "  Total: $TOTAL downloads"

# ── Downloads by Platform ────────────────────────────────────────────────────
PLATFORM_DATA=$(echo "$RAW" | jq -r '
  [.[] .assets[]
    | select(.name | test("\\.(tar\\.gz|zip)$"))
    | select(.name | test("checksums|source") | not)
    | .name |= (capture("guget_[^_]+_(?<os>[^_]+)_(?<arch>[^.]+)") | "\(.os)/\(.arch)")
  ] | group_by(.name) | map({
    platform: .[0].name,
    os: (.[0].name | split("/")[0]),
    downloads: ([.[].download_count] | add)
  }) | sort_by(.downloads) | reverse | .[] | "\(.platform)\t\(.os)\t\(.downloads)"')

MAX_P=$(echo "$PLATFORM_DATA" | awk -F'\t' '{if($3>m)m=$3} END{print m}')
if [[ "$MAX_P" -eq 0 ]]; then MAX_P=1; fi

echo ""
echo ""
echo "  Downloads by Platform"
echo "  ──────────────────────────────────────────────────────────"
echo ""

while IFS=$'\t' read -r platform os count; do
  BAR_LEN=$(( count * BAR_MAX / MAX_P ))
  BAR=$(printf "%${BAR_LEN}s" | tr ' ' "$BAR_CHAR")
  case "$os" in
    windows) COLOR="$C_WIN" ;;
    linux)   COLOR="$C_LIN" ;;
    darwin)  COLOR="$C_MAC" ;;
    *)       COLOR="$C_RST" ;;
  esac
  printf "  %16s | ${COLOR}%-${BAR_MAX}s${C_RST} %d\n" "$platform" "$BAR" "$count"
done <<< "$PLATFORM_DATA"

echo ""
echo "  ──────────────────────────────────────────────────────────"
echo ""
