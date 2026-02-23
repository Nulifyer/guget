<div align="center">

# guget

![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat&logo=go)
[![Platform](https://img.shields.io/badge/Platform-Linux%20|%20macOS%20|%20Windows-orange.svg)]()

A terminal UI for managing NuGet packages across .NET projects.
</div>

## Overview

`guget` lets you browse, update, and add NuGet packages across all `.csproj` and `.fsproj` files in a directory — without leaving the terminal. It fetches live version data from your configured NuGet sources and shows you at a glance what's out of date.

<div align="center">

![Screenshot placeholder](docs/screenshot.png)

</div>

---

## Features

- **Browse projects** — scans recursively for `.csproj` / `.fsproj` files
- **Live version status** — fetches latest versions from NuGet v3 API
- **Update packages** — bump to the latest compatible or latest stable version
- **Version picker** — choose any specific version with target-framework indicators
- **Add packages** — search NuGet and add new package references
- **Bulk sync** — apply a compatible version across all projects at once
- **Restore** — run `dotnet restore` without leaving the TUI
- **Log panel** — real-time internal logs, toggleable with `l`
- **Multi-source** — respects `NuGet.config` and global NuGet source configuration

---

## Requirements

- [Go](https://go.dev/) 1.25+
- [.NET SDK](https://dotnet.microsoft.com/download) (for project discovery and `dotnet restore`)
- A terminal with ANSI color support

---

## Installation

**Linux / macOS**

Installs to `/usr/local/bin` if writable, otherwise `~/.local/bin`. Override the install location with `GUGET_INSTALL=/your/path`.

```bash
curl -fsSL https://raw.githubusercontent.com/nulifyer/guget/main/install.sh | bash
```
```bash
wget -qO- https://raw.githubusercontent.com/nulifyer/guget/main/install.sh | bash
```

**Windows (PowerShell)**

```powershell
irm https://raw.githubusercontent.com/nulifyer/guget/main/install.ps1 | iex
```

Fetches the latest release from GitHub, installs to `%LOCALAPPDATA%\Programs\guget`, and adds it to your user `PATH` automatically. Override the install location with `$env:GUGET_INSTALL`.

> **Windows note:** The binary is not yet code-signed, so Windows SmartScreen may warn on first run. Running from a terminal (PowerShell or cmd) bypasses this.

**Manual download**

Grab the archive for your platform from the [Releases page](https://github.com/nulifyer/guget/releases) and place the binary somewhere on your `PATH`.

**Build from source**

```bash
git clone https://github.com/nulifyer/guget
cd guget/guget
go build -o guget        # Linux / macOS
go build -o guget.exe    # Windows
```

---

## Usage

```
guget [options] [project]

Usage:
    no-color     -nc, --no-color
                Disable colored output in the terminal

    verbosity    -v, --verbose
                Set the logging verbosity level
                [<empty>, none, error, err, warn, warning, info, debug, dbg, trace, trc]

    project      -p, --project
                Set the target project directory (defaults to current working directory)

    version      -V, --version
                Print the version and exit
```

**Examples:**

```bash
# Scan the current directory
guget

# Scan a specific solution folder
guget ~/src/MyApp

# Enable verbose logging
guget -v debug
```

---

## Keybindings

### Navigation

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Cycle panel focus (Projects → Packages → Detail → Logs) |
| `↑` / `k` | Move up |
| `↓` / `j` | Move down |
| `Enter` | Confirm / move focus from Projects to Packages |
| `q` / `Ctrl+C` | Quit |

### Package Actions

| Key | Action |
|-----|--------|
| `u` | Update selected package to latest **compatible** version |
| `U` | Update selected package to latest **stable** version |
| `r` | Open version picker overlay |
| `a` | Sync compatible version across **all** projects |
| `R` | Run `dotnet restore` |
| `/` | Search NuGet and add a new package |
| `d` | Remove selected package (prompts for confirmation) |
| `l` | Toggle log panel |

### Search Overlay (`/`)

| Key | Action |
|-----|--------|
| `↑` / `Ctrl+P` | Previous result |
| `↓` / `Ctrl+N` | Next result |
| `Enter` | Select package |
| `Esc` | Cancel |

### Version Picker (`r`)

| Key | Action |
|-----|--------|
| `↑` / `k` | Previous version |
| `↓` / `j` | Next version |
| `Enter` | Select version |
| `Esc` / `q` | Cancel |

---

## Package Status Icons

| Icon | Meaning |
|------|---------|
| `✓` | Up to date |
| `↑` | Newer **compatible** version available |
| `⬆` | Newer **stable** version available |
| `✗` | Error fetching version info |

---

## Project Structure

```
GoNugetTui/
├── guget/
│   ├── main.go               # Entry point, CLI flags, background fetching
│   ├── Tui.go                # Bubbletea MVU — all UI state and rendering
│   ├── ProjectParser.go      # XML parsing of .csproj / .fsproj files
│   ├── Nugetservice.go       # NuGet v3 API client
│   ├── Nugetsourcedetector.go# Reads NuGet source configuration
│   ├── TargetFramework.go    # Target framework compatibility logic
│   ├── Semver.go             # Semantic version parsing and comparison
│   ├── Set.go                # Generic set helper
│   ├── logger/               # Leveled, colored logger with TUI integration
│   └── arger/                # Minimal CLI argument parser
└── test-dotnet/              # Sample .NET projects for development
```

---

## How It Works

1. On startup, `guget` walks the target directory and parses every `.csproj` / `.fsproj` it finds (skipping `bin`, `obj`, `node_modules`, `.git`, etc.).
2. A background goroutine queries your configured NuGet sources for the latest version data for each package.
3. The UI updates as results arrive — no waiting for a full scan before you can start navigating.
4. When you update a package, `guget` rewrites the relevant project file(s) in place.

---

## Built With

- [Bubbletea](https://github.com/charmbracelet/bubbletea) — TUI framework (MVU pattern)
- [Bubbles](https://github.com/charmbracelet/bubbles) — list, spinner, text input, viewport
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — terminal styling and layout
