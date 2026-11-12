#Requires -Version 5.1
# ASCII without a BOM on purpose: `irm ... | iex` reads a BOM as part of the
# first command ("The term '#Requires' is not recognized").
$ErrorActionPreference = "Stop"

$Repo       = "nulifyer/guget"
$InstallDir = if ($env:GUGET_INSTALL) { $env:GUGET_INSTALL } else { "$env:LOCALAPPDATA\Programs\guget" }

# Adds $Dir to the user PATH unless an entry already points there. Works on
# the raw registry value so entries such as %USERPROFILE%\bin stay unexpanded,
# and keeps the value's type (REG_EXPAND_SZ). [Environment]::SetEnvironmentVariable
# would expand every entry and save the result as REG_SZ.
function Add-GugetUserPath([string]$Dir) {
    $Key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    try {
        $Raw = [string]$Key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $Entries = @($Raw -split ';' | Where-Object { $_ })
        foreach ($Entry in $Entries) {
            if ([Environment]::ExpandEnvironmentVariables($Entry).TrimEnd('\') -eq $Dir.TrimEnd('\')) { return $false }
        }
        $Kind = if ($Key.GetValueNames() -contains 'Path') { $Key.GetValueKind('Path') } else { [Microsoft.Win32.RegistryValueKind]::ExpandString }
        $Key.SetValue('Path', (($Entries + $Dir) -join ';'), $Kind)
    } finally {
        $Key.Close()
    }
    # Setting and clearing a variable through .NET broadcasts WM_SETTINGCHANGE,
    # so Explorer and programs started from it pick up the new PATH.
    [Environment]::SetEnvironmentVariable('GUGET_PATH_REFRESH', '1', 'User')
    [Environment]::SetEnvironmentVariable('GUGET_PATH_REFRESH', $null, 'User')
    return $true
}

# --- Arch detection ---
$Arch = if (
    [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq
    [System.Runtime.InteropServices.Architecture]::Arm64
) { "arm64" } else { "amd64" }

# --- Fetch latest release tag ---
Write-Host "Fetching latest release..."
$Release = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
$Tag     = $Release.tag_name                  # e.g. "v0.1.0"  (used in the URL)
$Version = $Tag -replace '^v', ''             # e.g.  "0.1.0"  (used in the filename)

Write-Host "Installing guget $Tag (windows/$Arch)..."

# --- Download and extract ---
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

    # --- Verify checksum ---
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

    # --- Install binary ---
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item (Join-Path $Tmp "guget.exe") (Join-Path $InstallDir "guget.exe") -Force

    # --- Add to user PATH if needed ---
    if (Add-GugetUserPath $InstallDir) {
        Write-Host "Added $InstallDir to your PATH."
    }

    # Make guget usable in the current session too, keeping its other PATH entries.
    if (($env:PATH -split ';') -notcontains $InstallDir) {
        $env:PATH = "$env:PATH;$InstallDir"
    }

    Write-Host "Installed to $InstallDir\guget.exe"
    Write-Host "Done! $(guget --version)"
}
finally {
    Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}
