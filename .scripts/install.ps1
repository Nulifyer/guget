#Requires -Version 5.1
$ErrorActionPreference = "Stop"

$Repo       = "nulifyer/guget"
$InstallDir = if ($env:GUGET_INSTALL) { $env:GUGET_INSTALL } else { "$env:LOCALAPPDATA\Programs\guget" }

# ── Arch detection ────────────────────────────────────────────────────────────
$Arch = if (
    [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq
    [System.Runtime.InteropServices.Architecture]::Arm64
) { "arm64" } else { "amd64" }

# ── Fetch latest release tag ──────────────────────────────────────────────────
Write-Host "Fetching latest release..."
$Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
$Tag     = $Release.tag_name                  # e.g. "v0.1.0"  (used in the URL)
$Version = $Tag -replace '^v', ''             # e.g.  "0.1.0"  (used in the filename)

Write-Host "Installing guget $Tag (windows/$Arch)..."

# ── Download and extract ──────────────────────────────────────────────────────
$Filename = "guget_${Version}_windows_${Arch}.zip"
$Url      = "https://github.com/$Repo/releases/download/$Tag/$Filename"

$Tmp = Join-Path $env:TEMP ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $Tmp | Out-Null

try {
    $ZipPath      = Join-Path $Tmp $Filename
    $ChecksumUrl  = "https://github.com/$Repo/releases/download/$Tag/checksums.txt"
    $ChecksumPath = Join-Path $Tmp "checksums.txt"

    Invoke-WebRequest -Uri $Url -OutFile $ZipPath -UseBasicParsing
    Invoke-WebRequest -Uri $ChecksumUrl -OutFile $ChecksumPath -UseBasicParsing

    # ── Verify checksum ──────────────────────────────────────────────────────
    $Expected = (Get-Content $ChecksumPath | Where-Object { $_ -match $Filename }) -replace '\s+.*$', ''
    if (-not $Expected) {
        throw "Checksum not found for $Filename"
    }
    $Actual = (Get-FileHash -Path $ZipPath -Algorithm SHA256).Hash.ToLower()
    if ($Actual -ne $Expected) {
        throw "Checksum mismatch`n  expected: $Expected`n  actual:   $Actual"
    }
    Write-Host "Checksum verified."

    Expand-Archive -Path $ZipPath -DestinationPath $Tmp

    # ── Install binary ────────────────────────────────────────────────────────
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item (Join-Path $Tmp "guget.exe") (Join-Path $InstallDir "guget.exe") -Force

    # ── Add to user PATH if needed ────────────────────────────────────────────
    $UserPath = [Environment]::GetEnvironmentVariable("PATH", "User")
    if ($UserPath -notlike "*$InstallDir*") {
        [Environment]::SetEnvironmentVariable("PATH", "$UserPath;$InstallDir", "User")
        Write-Host "Added $InstallDir to your PATH."
    }

    # Refresh PATH in the current session so guget is usable immediately.
    $env:PATH = [System.Environment]::GetEnvironmentVariable("PATH", "Machine") + ";" +
                [System.Environment]::GetEnvironmentVariable("PATH", "User")

    Write-Host "Installed to $InstallDir\guget.exe"
    Write-Host "Done! $(guget --version)"
}
finally {
    Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}
