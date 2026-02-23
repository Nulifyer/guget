#Requires -Version 5.1
# ──────────────────────────────────────────────────────────────────────────────
# release.ps1 — tag and push a release (GitHub Actions runs goreleaser)
#   Usage: .\release.ps1 [version]   e.g.  .\release.ps1 0.2.0
# ──────────────────────────────────────────────────────────────────────────────
[CmdletBinding()]
param(
    [string]$Version = ""
)
$ErrorActionPreference = "Stop"

# ── Helpers ───────────────────────────────────────────────────────────────────
function Sep  { Write-Host "  ────────────────────────────────────────────────────" -ForegroundColor DarkGray }
function Hdr($t) {
    Write-Host ""
    Write-Host "  ▸ $t" -ForegroundColor Cyan
}
function Ok($t)   {
    Write-Host "  " -NoNewline
    Write-Host "✓" -ForegroundColor Green -NoNewline
    Write-Host "  $t"
}
function Warn($t) {
    Write-Host "  " -NoNewline
    Write-Host "⚠" -ForegroundColor Yellow -NoNewline
    Write-Host "  $t"
}
function Fail($t) {
    Write-Host "  " -NoNewline
    Write-Host "✗  $t" -ForegroundColor Red
    exit 1
}
function Dim($t) { Write-Host "  $t" -ForegroundColor DarkGray }

# ── Banner ────────────────────────────────────────────────────────────────────
Write-Host ""
Write-Host "  GoNugetTui Release" -ForegroundColor Magenta
Sep

# ── Version ───────────────────────────────────────────────────────────────────
if (-not $Version) {
    $RecentTags = git tag --sort=-version:refname 2>$null | Select-Object -First 5
    if ($RecentTags) {
        Write-Host "  Recent tags:" -ForegroundColor DarkGray
        foreach ($t in $RecentTags) {
            Write-Host "    $t" -ForegroundColor Cyan
        }
        Write-Host ""
    }
    Write-Host "  Enter version " -NoNewline
    Write-Host "(e.g. 1.2.3 or v1.2.3)" -ForegroundColor DarkGray -NoNewline
    Write-Host ": " -NoNewline
    $Version = Read-Host
}

$Version = $Version.TrimStart('v')
if ($Version -notmatch '^\d+\.\d+\.\d+(-[a-zA-Z0-9._-]+)?$') {
    Fail "Invalid version '$Version' — expected X.Y.Z or X.Y.Z-pre"
}
$Tag = "v$Version"

# ── Pre-flight ────────────────────────────────────────────────────────────────
Hdr "Pre-flight checks"

$null = git rev-parse --git-dir 2>&1
if ($LASTEXITCODE -ne 0) { Fail "Not inside a git repository" }
Ok "Git repository"

$Branch = git rev-parse --abbrev-ref HEAD 2>&1
Ok "Branch: $Branch"

$Dirty = git status --porcelain 2>&1
if ($Dirty) {
    Warn "Uncommitted changes in working tree:"
    $Dirty | ForEach-Object { Dim "  $_" }
    Write-Host ""
} else {
    Ok "Working tree is clean"
}

$null = git rev-parse $Tag 2>&1
$OverwriteTag = $false
if ($LASTEXITCODE -eq 0) {
    Warn "Tag $Tag already exists"
    Write-Host "  Overwrite it? (deletes local + remote tag) [y/N] " -NoNewline -ForegroundColor Yellow
    $Overwrite = Read-Host
    if ($Overwrite -notmatch '^[yY]$') {
        Write-Host ""
        Warn "Aborted."
        Write-Host ""
        exit 0
    }
    $OverwriteTag = $true
} else {
    Ok "Tag $Tag is available"
}

# ── Git summary ───────────────────────────────────────────────────────────────
Hdr "Commits since last release"

$LastTag = git describe --tags --abbrev=0 2>&1
if ($LASTEXITCODE -ne 0) { $LastTag = "" }

$Range = if ($LastTag) { "$LastTag..HEAD" } else { "" }
if ($LastTag) {
    Dim "From $LastTag → $Tag"
} else {
    Dim "No previous tag found — showing last 20 commits"
}
Write-Host ""
Sep

# Fetch commits: "hash|subject|author|reltime"
$logArgs = @("log", "--no-decorate", "--format=%h|%s|%an|%ar")
if ($Range) { $logArgs += $Range }
$Commits = & git @logArgs 2>$null | Select-Object -First 20

foreach ($line in $Commits) {
    $p = $line -split '\|', 4
    Write-Host "    " -NoNewline
    Write-Host $p[0] -ForegroundColor Cyan -NoNewline
    Write-Host "  $($p[1])" -NoNewline
    Write-Host "  ($($p[2]), $($p[3]))" -ForegroundColor DarkGray
}
Sep

$countArgs = @("rev-list", "--count")
$countArgs += if ($Range) { $Range } else { "HEAD" }
$CommitCount = (& git @countArgs 2>$null) -as [int]
if (-not $LastTag -and $CommitCount -gt 20) {
    Dim "20 of $CommitCount total commits shown"
} else {
    Dim "$CommitCount commit(s) included in this release"
}

# ── Release plan ──────────────────────────────────────────────────────────────
Hdr "Release plan"
Write-Host ""
Write-Host "  Tag         " -NoNewline; Write-Host $Tag -ForegroundColor Green
Write-Host "  Branch      $Branch"
if ($LastTag) { Write-Host "  Previous    $LastTag" -ForegroundColor DarkGray }
Write-Host ""
Write-Host "  Steps:" -ForegroundColor Yellow
Write-Host "    1. git tag $Tag"
Write-Host "    2. " -NoNewline; Write-Host "git push origin $Tag" -NoNewline; Write-Host "  (GitHub Actions handles the rest)" -ForegroundColor DarkGray
Write-Host ""
Sep

# ── Confirm ───────────────────────────────────────────────────────────────────
Write-Host ""
Write-Host "  Proceed with release " -NoNewline
Write-Host $Tag -ForegroundColor Green -NoNewline
Write-Host "? [y/N] " -NoNewline -ForegroundColor Yellow
$Confirm = Read-Host

if ($Confirm -notmatch '^[yY]$') {
    Write-Host ""
    Warn "Aborted."
    Write-Host ""
    exit 0
}

# ── Execute ───────────────────────────────────────────────────────────────────
Hdr "Releasing $Tag"
Write-Host ""

if ($OverwriteTag) {
    Write-Host "  → Deleting existing tag $Tag..." -ForegroundColor Cyan
    git tag -d $Tag
    git push origin --delete $Tag 2>$null
    Ok "Old tag removed"
}

Write-Host "  → Creating tag $Tag..." -ForegroundColor Cyan
git tag -a $Tag -m "Release $Tag"
if ($LASTEXITCODE -ne 0) { Fail "git tag failed" }
Ok "Tag created"

Write-Host "  → Pushing tag to origin..." -ForegroundColor Cyan
git push origin $Tag
if ($LASTEXITCODE -ne 0) { Fail "git push failed" }
Ok "Tag pushed"

Write-Host ""
Write-Host "  ✓  $Tag is live — GitHub Actions will build and publish the release." -ForegroundColor Green
Write-Host ""
