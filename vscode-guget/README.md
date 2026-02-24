<div align="center">

<img src="https://raw.githubusercontent.com/nulifyer/guget/main/vscode-guget/icon.png" alt="guget logo" width="128" height="128">

# guget - NuGet Package Manager for VS Code

Manage NuGet packages across .NET projects with an interactive TUI, right inside VS Code.

</div>

![Screenshot placeholder](https://raw.githubusercontent.com/nulifyer/guget/main/docs/screenshot.png)

## Features

- **Browse projects** — scans recursively for `.csproj` / `.fsproj` files
- **Live version status** — fetches latest versions from NuGet v3 API
- **Vulnerability & deprecation tracking** — surfaces CVE advisories and deprecated status per package
- **Update packages** — bump to latest compatible or latest stable version
- **Version picker** — choose any specific version with target-framework and vulnerability indicators
- **Dependency tree** — view declared and full transitive dependency trees
- **Add packages** — search NuGet and add new package references
- **Bulk sync** — apply a compatible version across all projects at once
- **Restore** — run `dotnet restore` without leaving the TUI
- **Multi-source** — respects `NuGet.config` and global NuGet source configuration

## Requirements

- The [guget](https://github.com/nulifyer/guget) CLI binary must be installed
- [.NET SDK](https://dotnet.microsoft.com/download) (for project discovery and `dotnet restore`)

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

1. Open a folder containing `.csproj` or `.fsproj` files
2. Open the command palette (`Ctrl+Shift+P` / `Cmd+Shift+P`)
3. Run **"Guget: Manage NuGet Packages"**

guget opens as a full editor tab with complete keyboard and mouse support.

## Extension Settings

| Setting | Default | Description |
|---------|---------|-------------|
| `guget.binaryPath` | `""` | Absolute path to the `guget` binary. Leave empty to auto-detect from PATH. |
| `guget.verbosity` | `"warn"` | Log verbosity level: `none`, `error`, `warn`, `info`, `debug`, `trace` |
| `guget.additionalArgs` | `[]` | Additional CLI arguments to pass to guget |

## Keybindings

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Cycle panel focus |
| `↑` / `k` / `↓` / `j` | Navigate |
| `u` / `U` | Update to latest compatible / stable version |
| `r` | Open version picker |
| `a` | Sync version across all projects |
| `/` | Search and add a new package |
| `d` | Remove package |
| `t` / `T` | Dependency tree / transitive tree |
| `l` | Toggle log panel |
| `s` | Toggle sources panel |
| `?` | Toggle help overlay |
| `q` / `Ctrl+C` | Quit |
