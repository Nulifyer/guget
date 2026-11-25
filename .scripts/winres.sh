#!/usr/bin/env bash
# Generate the Windows resources (icon, version information, manifest) that
# `go build` embeds in guget.exe. Writes guget/rsrc_windows_{amd64,arm64}.syso,
# which are build outputs and ignored by git.
#
# Usage: .scripts/winres.sh VERSION   (for example 1.11.0 or 1.11.0-rc.1)
set -euo pipefail

version="${1:?usage: winres.sh VERSION}"
# Windows file versions are four numbers; drop any prerelease or build suffix.
numeric="${version%%[-+]*}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$root/guget"
go run github.com/tc-hib/go-winres@v0.3.3 simply \
  --arch amd64,arm64 \
  --manifest cli \
  --icon "$root/vscode-guget/icon.png" \
  --product-name guget \
  --file-description "guget NuGet package manager" \
  --product-version "$version" \
  --file-version "$numeric.0" \
  --original-filename guget.exe \
  --out rsrc
