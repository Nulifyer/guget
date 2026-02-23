#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────────────────
# release.sh — tag and push a release (GitHub Actions runs goreleaser)
#   Usage: ./release.sh [version]   e.g.  ./release.sh 0.2.0
# ──────────────────────────────────────────────────────────────────────────────
set -euo pipefail

# ── ANSI helpers ──────────────────────────────────────────────────────────────
BOLD='\033[1m'; DIM='\033[2m'; RESET='\033[0m'
RED='\033[31m'; GREEN='\033[32m'; YELLOW='\033[33m'
CYAN='\033[36m'; MAGENTA='\033[35m'; WHITE='\033[97m'

sep()  { echo -e "${DIM}  ────────────────────────────────────────────────────${RESET}"; }
ok()   { echo -e "  ${GREEN}✓${RESET}  $*"; }
warn() { echo -e "  ${YELLOW}⚠${RESET}  $*"; }
fail() { echo -e "  ${RED}✗  $*${RESET}" >&2; exit 1; }
hdr()  { echo -e "\n${BOLD}${CYAN}  ▸ $*${RESET}"; }
dim()  { echo -e "  ${DIM}$*${RESET}"; }

# ── Banner ────────────────────────────────────────────────────────────────────
echo -e "\n${BOLD}${MAGENTA}  GoNugetTui Release${RESET}"
sep

# ── Version ───────────────────────────────────────────────────────────────────
VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
    RECENT_TAGS=$(git tag --sort=-version:refname 2>/dev/null | head -5)
    if [[ -n "$RECENT_TAGS" ]]; then
        echo -e "  ${DIM}Recent tags:${RESET}"
        while IFS= read -r t; do
            echo -e "    ${CYAN}$t${RESET}"
        done <<< "$RECENT_TAGS"
        echo ""
    fi
    printf "  Enter version ${DIM}(e.g. 1.2.3 or v1.2.3)${RESET}: "
    read -r VERSION
fi

VERSION="${VERSION#v}"   # strip leading v; tag will re-add it
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9._-]+)?$ ]]; then
    fail "Invalid version '$VERSION' — expected X.Y.Z or X.Y.Z-pre"
fi
TAG="v$VERSION"

# ── Pre-flight ────────────────────────────────────────────────────────────────
hdr "Pre-flight checks"

git rev-parse --git-dir &>/dev/null || fail "Not inside a git repository"
ok "Git repository"

BRANCH=$(git rev-parse --abbrev-ref HEAD)
ok "Branch: ${BOLD}$BRANCH${RESET}"

DIRTY=$(git status --porcelain 2>/dev/null || true)
if [[ -n "$DIRTY" ]]; then
    warn "Uncommitted changes in working tree:"
    echo "$DIRTY" | while IFS= read -r line; do dim "  $line"; done
    echo ""
else
    ok "Working tree is clean"
fi

if git rev-parse "$TAG" >/dev/null 2>&1; then
    warn "Tag ${BOLD}$TAG${RESET} already exists"
    printf "  ${YELLOW}Overwrite it? (deletes local + remote tag) [y/N]${RESET} "
    read -r OVERWRITE
    if [[ ! "$OVERWRITE" =~ ^[yY]$ ]]; then
        echo -e "\n  ${YELLOW}Aborted.${RESET}\n"
        exit 0
    fi
    OVERWRITE_TAG=true
else
    OVERWRITE_TAG=false
    ok "Tag $TAG is available"
fi

# ── Git summary ───────────────────────────────────────────────────────────────
hdr "Commits since last release"

LAST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || true)
if [[ -n "$LAST_TAG" ]]; then
    RANGE="$LAST_TAG..HEAD"
    dim "From ${BOLD}$LAST_TAG${RESET}${DIM} → ${BOLD}$TAG"
else
    RANGE=""
    dim "No previous tag found — showing last 20 commits"
fi

echo ""
sep
git -c color.ui=always log ${RANGE:-} --no-decorate \
    --pretty=tformat:"    %C(cyan)%h%C(reset)  %s  %C(brightblack)(%an, %ar)%C(reset)" \
    | head -20
sep

COUNT=$(git rev-list --count ${RANGE:-HEAD} 2>/dev/null || echo "?")
if [[ -z "$RANGE" && "$COUNT" -gt 20 ]]; then COUNT="20 (of $COUNT total)"; fi
dim "$COUNT commit(s) included in this release"

# ── Release plan ──────────────────────────────────────────────────────────────
hdr "Release plan"
echo -e ""
echo -e "  ${DIM}Tag${RESET}         ${BOLD}${GREEN}$TAG${RESET}"
echo -e "  ${DIM}Branch${RESET}      ${BOLD}$BRANCH${RESET}"
[[ -n "$LAST_TAG" ]] && echo -e "  ${DIM}Previous${RESET}    ${DIM}$LAST_TAG${RESET}"
echo -e ""
echo -e "  ${YELLOW}Steps:${RESET}"
echo -e "    ${DIM}1.${RESET} git tag ${BOLD}$TAG${RESET}"
echo -e "    ${DIM}2.${RESET} git push origin ${BOLD}$TAG${RESET}  ${DIM}(GitHub Actions handles the rest)${RESET}"
echo ""
sep

# ── Confirm ───────────────────────────────────────────────────────────────────
printf "\n  ${BOLD}Proceed with release ${GREEN}$TAG${RESET}${BOLD}? [y/N]${RESET} "
read -r CONFIRM
if [[ ! "$CONFIRM" =~ ^[yY]$ ]]; then
    echo -e "\n  ${YELLOW}Aborted.${RESET}\n"
    exit 0
fi

# ── Execute ───────────────────────────────────────────────────────────────────
hdr "Releasing $TAG"
echo ""

if [[ "$OVERWRITE_TAG" == true ]]; then
    echo -e "  ${CYAN}→${RESET}  Deleting existing tag ${BOLD}$TAG${RESET}..."
    git tag -d "$TAG"
    git push origin --delete "$TAG" 2>/dev/null || true
    ok "Old tag removed"
fi

echo -e "  ${CYAN}→${RESET}  Creating tag ${BOLD}$TAG${RESET}..."
git tag -a "$TAG" -m "Release $TAG"
ok "Tag created"

echo -e "  ${CYAN}→${RESET}  Pushing tag to origin..."
git push origin "$TAG"
ok "Tag pushed"

echo -e "\n  ${BOLD}${GREEN}✓  $TAG is live — GitHub Actions will build and publish the release.${RESET}\n"
