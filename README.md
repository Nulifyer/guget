<div align="center">

# guget

<img src="vscode-guget/icon.svg" alt="guget logo" width="128" height="128">

![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?style=flat&logo=go)
[![Platform](https://img.shields.io/badge/Platform-Linux%20|%20macOS%20|%20Windows-orange.svg)]()
[![GitHub Release](https://img.shields.io/github/v/release/nulifyer/guget?logo=github&color=brightgreen)](https://github.com/nulifyer/guget/releases)
[![VS Code Extension](https://img.shields.io/visual-studio-marketplace/v/nulifyer.guget?label=VS%20Code&logo=visualstudiocode)](https://marketplace.visualstudio.com/items?itemName=nulifyer.guget)

A terminal UI and scriptable CLI for managing NuGet packages across .NET projects.
</div>

## Overview

`guget` lets you browse, update, and add NuGet packages across all `.csproj`, `.fsproj`, and `.vbproj` files in a directory — without leaving the terminal. It fetches live version data from your configured NuGet sources and shows you at a glance what's out of date.

<div align="center">

![Screenshot placeholder](docs/screenshot.png)

</div>



## Features

| | Feature | Description |
|:-:|---------|-------------|
| 📁 | **Browse projects** | Scans recursively for `.csproj` / `.fsproj` / `.vbproj` files, with support for Central Package Management (`Directory.Build.props`) and imported `.props` files |
| 🚀 | **Live version status** | Fetches latest versions from NuGet v3 API |
| 🛡️ | **Vulnerability & deprecation tracking** | Surfaces CVE advisories and deprecated status per package version, with severity-coloured indicators in the list, detail panel, and version picker. Packages from private/Azure feeds are automatically enriched with vulnerability data from nuget.org |
| ⬆️ | **Update packages** | Bump to the latest compatible or latest stable version |
| 📋 | **Version picker** | Choose any specific version with target-framework and vulnerability indicators |
| 🌳 | **Dependency tree** | `t` shows declared dependencies; `T` runs `dotnet list --include-transitive` for the full transitive tree with status icons |
| ➕ | **Add packages** | Search NuGet and add new package references |
| 🔄 | **Bulk operations** | Update a package across all projects at once |
| 🔧 | **Restore** | Run `dotnet restore` without leaving the TUI |
| 🌐 | **Multi-source** | Respects `NuGet.config` and global NuGet source configuration. Private feed packages are supplemented with metadata from nuget.org |
| 🔗 | **Clickable hyperlinks** | Package names, advisory IDs, versions, and source URLs are clickable in terminals that support OSC 8 hyperlinks |
| 🎨 | **Themes** | Built-in colour themes: `auto`, `dracula`, `nord`, `everforest`, `gruvbox`. Select with `--theme` / `-t` |
| ↔️ | **Responsive layout** | Columns hide progressively on narrow terminals to keep the UI usable at any width |
| 📜 | **Log panel** | Real-time internal logs, toggleable with `l` |
| 🔌 | **Sources panel** | View configured NuGet sources, toggleable with `s` |
| ❓ | **Help overlay** | Full keybinding reference, press `?` |
| ⌨️ | **Headless CLI** | Inspect, plan, edit, and restore packages from scripts without starting the TUI |



## Requirements

To run:
- The [guget](https://github.com/nulifyer/guget) CLI binary
- [.NET SDK](https://dotnet.microsoft.com/download) dotnet CLI

To build:
- [Go](https://go.dev/) 1.25+
- [.NET SDK](https://dotnet.microsoft.com/download) dotnet CLI



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

    theme        -t, --theme
                Color theme
                [auto, dracula, nord, everforest, gruvbox, etc]

    sort-by      -o, --sort-by
                Initial sort order: status, name, source, current, available
                Append :asc or :desc for direction (default: status:asc)

    log-file     -lf, --log-file
                Write all log output to this file (in addition to the TUI log panel)

    no-mouse     -nm, --no-mouse
                Disable mouse navigation in the TUI

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

# Use the dracula theme
guget -t dracula

# Leave clicks and wheel events to the terminal
guget --no-mouse

# Sort by available updates, newest first
guget -o available:desc
```

### Headless commands

Supplying a command runs Guget without initializing Bubble Tea. Read commands
support `table`, `tsv`, `json`, and `jsonl`; redirected output defaults to TSV.
Diagnostics stay on stderr, and `--output` replaces its destination atomically.

```bash
# Declared, evaluated, and restore-resolved package state
guget list --project ./src --format json
guget show Newtonsoft.Json --project ./src

# NuGet sources, source mappings, and restore-only local feeds
guget sources --project ./src

# Preview and then apply the same transactional edit plan
guget update Newtonsoft.Json --version 13.0.4 --file App/App.csproj --dry-run
guget update Newtonsoft.Json --version 13.0.4 --file App/App.csproj --restore

# Workspace scope is always explicit
guget remove Old.Package --project ./src --all --dry-run
guget restore --project ./src --all
```

Run `guget help` or `guget help update` for command usage. Mutation commands
require repeatable `--file` targets or an explicit `--all`; `add` always requires
at least one `--file`. A dry run performs the same ownership checks and planning
path as an apply. See [CLI design](docs/cli-design.md) for JSON and exit-code
contracts.



## Keybindings

### Navigation

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Cycle panel focus (Projects → Packages → Detail → Logs) |
| `↑` / `k` | Move up |
| `↓` / `j` | Move down |
| `Enter` | Confirm / move focus from Projects to Packages |
| `Esc` / `q` / `Ctrl+C` | Quit (main screen) / Close (overlay) |

Mouse navigation is enabled by default. A left click selects project and
package rows, sorts package columns, opens links, switches release-note tabs,
and operates visible overlay buttons. Picker rows select or toggle their item;
file-changing actions still require an explicit Add, Apply, Update, or Remove
button. In the package header, click the bracketed sort mode to cycle through
the modes and click its arrow to reverse direction. Requested, Available, and
Source are direct sort shortcuts. The wheel navigates or scrolls the panel
under the pointer without changing keyboard focus. Overlays remain modal, so
clicks cannot reach the main screen underneath them.

Mouse reporting can take over clicks that the terminal would otherwise use for
text selection. Use the terminal's mouse-bypass modifier, commonly Shift, for
native handling. Run `guget --no-mouse` to leave all mouse input with the
terminal.

### Package Actions (packages panel)

| Key | Action |
|-----|--------|
| `u` | Update to latest **compatible** version (this project) |
| `U` | Update to latest **compatible** version (all projects) |
| `a` | Update to latest **stable** version (this project) |
| `A` | Update to latest **stable** version (all projects) |
| `v` | Open version picker overlay |
| `o` | Cycle sort mode (status, name, source, current, available) |
| `O` | Toggle sort direction (asc / desc) |
| `d` | Remove selected package (prompts for confirmation) |
| `t` | Show declared dependency tree for the selected package |

### Project Actions

| Key | Action |
|-----|--------|
| `Ctrl+R` | Reload projects from disk |
| `r` | Run `dotnet restore` (selected project) |
| `R` | Run `dotnet restore` (all projects) |
| `T` | Show full transitive dependency tree |
| `/` | Search NuGet and add a new package |

### General

| Key | Action |
|-----|--------|
| `l` | Toggle log panel |
| `s` | Toggle sources panel |
| `?` | Toggle keybinding help |
| `[` / `]` | Resize focused panel |

### Search Overlay (`/`)

| Key | Action |
|-----|--------|
| `↑` / `Ctrl+P` | Previous result |
| `↓` / `Ctrl+N` | Next result |
| `Enter` | Select package |
| `Esc` | Close |

### Version Picker (`v`)

| Key | Action |
|-----|--------|
| `↑` / `k` | Previous version |
| `↓` / `j` | Next version |
| `u` | Apply version (this project) |
| `U` | Apply version (all projects) |
| `Enter` | Apply version |
| `Esc` / `q` | Close |



## Package Status Icons

| Icon | Meaning |
|------|---------|
| `▲` | Installed version has known **CVE vulnerabilities** |
| `✗` | Error fetching version info |
| `.` | Package metadata is still loading |
| `↑` | Newer **compatible** version available |
| `⬆` | Newer **stable** version available (beyond compatible) |
| `~` | Package is **deprecated** in the registry |
| `✓` | Up to date |



## How It Works

1. On startup, `guget` walks the target directory and parses every `.csproj` / `.fsproj` / `.vbproj` it finds (skipping `bin`, `obj`, `node_modules`, `.git`, etc.).
2. A background goroutine queries your configured NuGet sources for the latest version data for each package.
3. A background watcher polls project files, `.props`, and `nuget.config`, then reloads the workspace when those files change on disk.
4. You can force the same rescan manually at any time with `Ctrl+R`.
5. The UI updates as results arrive — no waiting for a full scan before you can start navigating.
6. Mutations are planned against file hashes, written with same-directory atomic
   replacement, and rolled back best-effort if a multi-file operation fails.



## Built With

- [Bubbletea](https://github.com/charmbracelet/bubbletea) — TUI framework (MVU pattern)
- [Bubbles](https://github.com/charmbracelet/bubbles) — list, spinner, text input, viewport
- [Lipgloss](https://github.com/charmbracelet/lipgloss) — terminal styling and layout
