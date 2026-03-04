$ErrorActionPreference = "Stop"

$Repo = "nulifyer/guget"
$BarChar = "#"
$BarMax = 40

# Colors (Gruvbox)
$C_WIN = "`e[38;5;109m"  # blue
$C_LIN = "`e[38;5;142m"  # yellow
$C_MAC = "`e[38;5;175m"  # purple
$C_DIM = "`e[2m"
$C_RST = "`e[0m"

$Raw = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases"

# ── Downloads by Version (stacked by platform) ──────────────────────────────
$Versioned = $Raw | ForEach-Object {
    $rel = $_
    $linux   = ($rel.assets | Where-Object { $_.name -match "linux" }   | Measure-Object -Property download_count -Sum).Sum
    $darwin  = ($rel.assets | Where-Object { $_.name -match "darwin" }  | Measure-Object -Property download_count -Sum).Sum
    $windows = ($rel.assets | Where-Object { $_.name -match "windows" } | Measure-Object -Property download_count -Sum).Sum
    [PSCustomObject]@{
        Tag     = $rel.tag_name
        Linux   = [int]$linux
        Darwin  = [int]$darwin
        Windows = [int]$windows
        Total   = [int]$linux + [int]$darwin + [int]$windows
    }
} | Sort-Object { [version]($_.Tag -replace '^v','') }

if (-not $Versioned) {
    Write-Host "No release data found."; exit 1
}

$Max = ($Versioned | Measure-Object -Property Total -Maximum).Maximum
if ($Max -eq 0) { $Max = 1 }

$GrandTotal = 0
Write-Host ""
Write-Host "  Downloads by Version"
Write-Host "  ──────────────────────────────────────────────────────────"
Write-Host ("  {0,10}   ${C_LIN}## linux${C_RST}  ${C_MAC}## darwin${C_RST}  ${C_WIN}## windows${C_RST}" -f "")
Write-Host ""

foreach ($v in $Versioned) {
    $GrandTotal += $v.Total
    $BarTotal = [math]::Floor($v.Total * $BarMax / $Max)
    if ($v.Total -gt 0 -and $BarTotal -eq 0) { $BarTotal = 1 }

    if ($v.Total -gt 0) {
        $LinLen = [math]::Floor($BarTotal * $v.Linux / $v.Total)
        $MacLen = [math]::Floor($BarTotal * $v.Darwin / $v.Total)
        $WinLen = $BarTotal - $LinLen - $MacLen
    } else {
        $LinLen = 0; $MacLen = 0; $WinLen = 0
    }

    $LinBar = $BarChar * $LinLen
    $MacBar = $BarChar * $MacLen
    $WinBar = $BarChar * $WinLen
    $Pad    = " " * ($BarMax - $BarTotal)

    Write-Host ("  {0,10} | ${C_LIN}${LinBar}${C_MAC}${MacBar}${C_WIN}${WinBar}${C_RST}${Pad} {1}" -f $v.Tag, $v.Total)
}

Write-Host ""
Write-Host "  ──────────────────────────────────────────────────────────"
Write-Host "  Total: $GrandTotal downloads"

# ── Downloads by Platform ────────────────────────────────────────────────────
$PlatformData = $Raw | ForEach-Object { $_.assets } |
    Where-Object { $_.name -match '\.(tar\.gz|zip)$' -and $_.name -notmatch 'checksums|source' } |
    ForEach-Object {
        if ($_.name -match 'guget_[^_]+_(?<os>[^_]+)_(?<arch>[^.]+)') {
            [PSCustomObject]@{
                Platform  = "$($Matches.os)/$($Matches.arch)"
                OS        = $Matches.os
                Downloads = $_.download_count
            }
        }
    } |
    Group-Object -Property Platform |
    ForEach-Object {
        [PSCustomObject]@{
            Platform  = $_.Name
            OS        = $_.Group[0].OS
            Downloads = ($_.Group | Measure-Object -Property Downloads -Sum).Sum
        }
    } |
    Sort-Object -Property Downloads -Descending

$MaxP = ($PlatformData | Measure-Object -Property Downloads -Maximum).Maximum
if ($MaxP -eq 0) { $MaxP = 1 }

Write-Host ""
Write-Host ""
Write-Host "  Downloads by Platform"
Write-Host "  ──────────────────────────────────────────────────────────"
Write-Host ""

foreach ($p in $PlatformData) {
    $BarLen = [math]::Floor($p.Downloads * $BarMax / $MaxP)
    $Bar = $BarChar * $BarLen
    $Color = switch ($p.OS) {
        "windows" { $C_WIN }
        "linux"   { $C_LIN }
        "darwin"  { $C_MAC }
        default   { $C_RST }
    }
    Write-Host ("  {0,16} | ${Color}{1,-${BarMax}}${C_RST} {2}" -f $p.Platform, $Bar, $p.Downloads)
}

Write-Host ""
Write-Host "  ──────────────────────────────────────────────────────────"
Write-Host ""
