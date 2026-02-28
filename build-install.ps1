#Requires -Version 5.1
# ──────────────────────────────────────────────────────────────────────────────
# build-install.ps1 — build guget from source and install it locally
#   Usage: .\build-install.ps1
# ──────────────────────────────────────────────────────────────────────────────
$ErrorActionPreference = "Stop"

$RepoRoot  = $PSScriptRoot
$SourceDir = Join-Path $RepoRoot "guget"

# ── Helpers ──────────────────────────────────────────────────────────────────
function Ok($t)   { Write-Host "  ✓  $t" -ForegroundColor Green }
function Fail($t) { Write-Host "  ✗  $t" -ForegroundColor Red; exit 1 }
function Dim($t)  { Write-Host "  $t" -ForegroundColor DarkGray }

Write-Host ""
Write-Host "  guget — build from source" -ForegroundColor Magenta
Write-Host "  ────────────────────────────────────────────────────" -ForegroundColor DarkGray

# ── Pre-flight ───────────────────────────────────────────────────────────────
$null = Get-Command go -ErrorAction SilentlyContinue
if (-not $?) { Fail "Go is not installed or not in PATH" }
Ok "Go found: $(go version)"

if (-not (Test-Path (Join-Path $SourceDir "go.mod"))) {
    Fail "Cannot find $SourceDir\go.mod — run this script from the repo root"
}

# ── Version ──────────────────────────────────────────────────────────────────
$Version = "dev"
try {
    $desc = git -C $RepoRoot describe --tags --always 2>$null
    if ($LASTEXITCODE -eq 0 -and $desc) {
        $Version = $desc -replace '^v', ''
    }
} catch {}
Dim "Version: $Version"

# ── Platform detection ───────────────────────────────────────────────────────
$IsWindows_ = ($env:OS -eq "Windows_NT") -or ($PSVersionTable.PSEdition -eq "Desktop") -or ($IsWindows -eq $true)
$BinaryName = if ($IsWindows_) { "guget.exe" } else { "guget" }

# ── Build ────────────────────────────────────────────────────────────────────
Write-Host ""
Write-Host "  ▸ Building..." -ForegroundColor Cyan

$env:CGO_ENABLED = "0"
$ldflags = "-s -w -X main.version=$Version"
$OutPath = Join-Path $RepoRoot $BinaryName

Push-Location $SourceDir
try {
    go build -ldflags $ldflags -o $OutPath .
    if ($LASTEXITCODE -ne 0) { Fail "Build failed" }
} finally {
    Pop-Location
}
Ok "Built $OutPath"

# ── Install ──────────────────────────────────────────────────────────────────
Write-Host ""
Write-Host "  ▸ Installing..." -ForegroundColor Cyan

if ($IsWindows_) {
    $InstallDir = if ($env:GUGET_INSTALL) { $env:GUGET_INSTALL } else { "$env:LOCALAPPDATA\Programs\guget" }
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item $OutPath (Join-Path $InstallDir $BinaryName) -Force
    Ok "Installed to $InstallDir\$BinaryName"

    # Add to user PATH if needed
    $UserPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    if ($UserPath -notlike "*$InstallDir*") {
        [Environment]::SetEnvironmentVariable("PATH", "$UserPath;$InstallDir", "User")
        Ok "Added $InstallDir to user PATH"
    }

    # Refresh PATH for current session
    $env:PATH = [System.Environment]::GetEnvironmentVariable("PATH", "Machine") + ";" +
                [System.Environment]::GetEnvironmentVariable("PATH", "User")
} else {
    # Linux/macOS via PowerShell
    if ($env:GUGET_INSTALL) {
        $InstallDir = $env:GUGET_INSTALL
    } elseif ((Test-Path "/usr/local/bin") -and ((Get-Item "/usr/local/bin").Mode -match "w")) {
        $InstallDir = "/usr/local/bin"
    } else {
        $InstallDir = "$HOME/.local/bin"
    }
    New-Item -ItemType Directory -Force -Path $InstallDir -ErrorAction SilentlyContinue | Out-Null
    Copy-Item $OutPath (Join-Path $InstallDir $BinaryName) -Force
    & chmod 755 (Join-Path $InstallDir $BinaryName) 2>$null
    Ok "Installed to $InstallDir/$BinaryName"
}

# ── Clean up build artifact from repo root ───────────────────────────────────
Remove-Item $OutPath -Force -ErrorAction SilentlyContinue

# ── Verify ───────────────────────────────────────────────────────────────────
Write-Host ""
$installed = Join-Path $InstallDir $BinaryName
$verOutput = & $installed --version 2>&1
Ok "Verified: $verOutput"

Write-Host ""
Write-Host "  Done!" -ForegroundColor Green
Write-Host ""
