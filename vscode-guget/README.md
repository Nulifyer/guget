<div align="center">

<img src="https://raw.githubusercontent.com/nulifyer/guget/main/vscode-guget/icon.png" alt="guget logo" width="128" height="128">

# guget - NuGet Package Manager for VS Code

Manage NuGet packages across .NET projects with an interactive TUI, right inside VS Code.

[![Visual Studio Marketplace](https://img.shields.io/visual-studio-marketplace/v/nulifyer.guget)](https://marketplace.visualstudio.com/items?itemName=nulifyer.guget)
[![Installs](https://img.shields.io/visual-studio-marketplace/i/nulifyer.guget)](https://marketplace.visualstudio.com/items?itemName=nulifyer.guget)
[![GitHub Release](https://img.shields.io/github/v/release/nulifyer/guget?label=CLI&logo=github&color=brightgreen)](https://github.com/nulifyer/guget/releases)

</div>

![Screenshot placeholder](https://raw.githubusercontent.com/nulifyer/guget/main/docs/screenshot.png)

## Features

- **Browse projects** — scans recursively for `.csproj` / `.fsproj` files
- **Live version status** — fetches latest versions from NuGet v3 API
- **Vulnerability & deprecation tracking** — surfaces CVE advisories and deprecated status per package version, with severity-coloured indicators. Packages from private/Azure feeds are automatically enriched with vulnerability data from nuget.org.
- **Update packages** — bump to latest compatible or latest stable version
- **Version picker** — choose any specific version with target-framework and vulnerability indicators
- **Dependency tree** — view declared and full transitive dependency trees
- **Add packages** — search NuGet and add new package references
- **Bulk operations** — update a package across all projects at once
- **Restore** — run `dotnet restore` without leaving the TUI
- **Multi-source** — respects `NuGet.config` and global NuGet source configuration. Packages found on private feeds are supplemented with metadata from nuget.org.
- **Clickable hyperlinks** — package names, advisory IDs, versions, and source URLs are clickable in terminals that support OSC 8 hyperlinks
- **Themes** — built-in colour themes: `auto`, `dracula`, `nord`, `everforest`, `gruvbox`
- **Responsive layout** — columns hide progressively on narrow terminals to keep the UI usable at any width

## Requirements

- The [guget](https://github.com/nulifyer/guget) CLI binary
- [.NET SDK](https://dotnet.microsoft.com/download) dotnet CLI

## Installation

1. Install this extension from the [VS Code Marketplace](https://marketplace.visualstudio.com/items?itemName=nulifyer.guget)
2. Install the guget CLI:

   **Linux / macOS:**
   ```bash
   curl -fsSL https://raw.githubusercontent.com/nulifyer/guget/main/install.sh | bash
   ```

   **Windows (PowerShell):**
   ```powershell
   irm https://raw.githubusercontent.com/nulifyer/guget/main/install.ps1 | iex
   ```

   Or download from the [Releases page](https://github.com/nulifyer/guget/releases).

## Usage

**Command palette:**

1. Open a folder containing `.csproj` or `.fsproj` files
2. Open the command palette (`Ctrl+Shift+P` / `Cmd+Shift+P`)
3. Run **"Guget: Manage NuGet Packages"**

**Explorer context menu:**

Right-click any `.csproj` or `.fsproj` file in the explorer and select **"Guget: Manage NuGet Packages"** to open guget scoped to that project's directory.

**Editor title button:**

When viewing a `.csproj` or `.fsproj` file, click the guget icon in the editor title bar.

**Status bar:**

When a workspace contains .NET project files, a **guget** item appears in the status bar. Click it to launch guget.

guget opens as a full editor tab with complete keyboard support.

## Extension Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `guget.binaryPath` | `""` | Absolute path to the `guget` binary. Leave empty to auto-detect from PATH. |
| `guget.verbosity` | `"warn"` | Log verbosity level: `none`, `error`, `warn`, `info`, `debug`, `trace` |
| `guget.theme` | `"auto"` | Color theme passed to guget (`auto`, `dracula`, `nord`, `everforest`, `gruvbox`, etc.) |
| `guget.additionalArgs` | `[]` | Additional CLI arguments to pass to guget |

## Keybindings

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Cycle panel focus |
| `↑` / `k` / `↓` / `j` | Navigate |
| `u` / `U` | Update to latest compatible — this / all projects |
| `a` / `A` | Update to latest stable — this / all projects |
| `v` | Open version picker |
| `r` / `R` | Run `dotnet restore` — selected / all projects |
| `/` | Search and add a new package |
| `d` | Remove package |
| `t` / `T` | Dependency tree / transitive tree |
| `l` | Toggle log panel |
| `s` | Toggle sources panel |
| `?` | Toggle help overlay |
| `Esc` / `q` | Quit (main) / Close (overlay) |
