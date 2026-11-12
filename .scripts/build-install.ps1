#Requires -Version 5.1
# ──────────────────────────────────────────────────────────────────────────────
# build-install.ps1 — build guget from source and install it locally
#   Usage: .\.scripts\build-install.ps1
# ──────────────────────────────────────────────────────────────────────────────
$ErrorActionPreference = "Stop"

$RepoRoot  = Split-Path -Parent $PSScriptRoot
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
    Fail "Cannot find $SourceDir\go.mod — check the repository checkout"
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

$BuildDir = Join-Path ([System.IO.Path]::GetTempPath()) ("guget-build-" + [System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $BuildDir | Out-Null
$PreviousCgoEnabled = $env:CGO_ENABLED

try {
    $env:CGO_ENABLED = "0"
    $ldflags = "-s -w -X main.version=$Version"
    $OutPath = Join-Path $BuildDir $BinaryName

    Push-Location -LiteralPath $SourceDir
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

        # Add to user PATH if needed. Works on the raw registry value so entries
        # such as %USERPROFILE%\bin stay unexpanded, and keeps its type
        # (REG_EXPAND_SZ); [Environment]::SetEnvironmentVariable would expand
        # every entry and save the result as REG_SZ.
        $EnvKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
        try {
            $RawPath = [string]$EnvKey.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
            $Entries = @($RawPath -split ';' | Where-Object { $_ })
            $Present = $Entries | Where-Object { [Environment]::ExpandEnvironmentVariables($_).TrimEnd('\') -eq $InstallDir.TrimEnd('\') }
            if (-not $Present) {
                $Kind = if ($EnvKey.GetValueNames() -contains 'Path') { $EnvKey.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
                $EnvKey.SetValue('Path', (($Entries + $InstallDir) -join ';'), $Kind)
                # Setting and clearing a variable through .NET broadcasts WM_SETTINGCHANGE.
                [Environment]::SetEnvironmentVariable('GUGET_PATH_REFRESH', '1', 'User')
                [Environment]::SetEnvironmentVariable('GUGET_PATH_REFRESH', $null, 'User')
                Ok "Added $InstallDir to user PATH"
            }
        } finally {
            $EnvKey.Close()
        }

        # Make guget usable in the current session too, keeping its other PATH entries
        if (($env:PATH -split ';') -notcontains $InstallDir) {
            $env:PATH = "$env:PATH;$InstallDir"
        }
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

    # ── Verify ───────────────────────────────────────────────────────────────────
    Write-Host ""
    $installed = Join-Path $InstallDir $BinaryName
    $verOutput = & $installed --version 2>&1
    if ($LASTEXITCODE -ne 0) { Fail "Installed binary failed verification" }
    Ok "Verified: $verOutput"

    Write-Host ""
    Write-Host "  Done!" -ForegroundColor Green
    Write-Host ""
} finally {
    Remove-Item -LiteralPath $BuildDir -Recurse -Force -ErrorAction SilentlyContinue
    if ($null -eq $PreviousCgoEnabled) {
        Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    } else {
        $env:CGO_ENABLED = $PreviousCgoEnabled
    }
}
