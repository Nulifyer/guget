# Install and update Guget

Guget runs on Linux, macOS, and Windows, on amd64 and arm64. Install the
[.NET SDK](https://dotnet.microsoft.com/download) for the projects you manage.
The TUI needs a terminal of at least 80 columns by 12 rows.

## Install on Linux or macOS

```bash
curl -fsSL https://raw.githubusercontent.com/nulifyer/guget/main/.scripts/install.sh | bash
```

With wget:

```bash
wget -qO- https://raw.githubusercontent.com/nulifyer/guget/main/.scripts/install.sh | bash
```

The installer uses `/usr/local/bin` when writable, otherwise `~/.local/bin`.
Open a new terminal if the command is not available in your current session.

To choose the destination, create the directory and set `GUGET_INSTALL`:

```bash
mkdir -p "$HOME/.local/bin"
curl -fsSL https://raw.githubusercontent.com/nulifyer/guget/main/.scripts/install.sh | GUGET_INSTALL="$HOME/.local/bin" bash
```

## Install on Windows

Run in PowerShell:

```powershell
irm https://raw.githubusercontent.com/nulifyer/guget/main/.scripts/install.ps1 | iex
```

The installer uses `%LOCALAPPDATA%\Programs\guget` and adds that directory to
your user PATH. Restart VS Code or other already-running applications if they
cannot find the new command.

To choose the destination:

```powershell
$env:GUGET_INSTALL = "$env:LOCALAPPDATA\Programs\guget"
irm https://raw.githubusercontent.com/nulifyer/guget/main/.scripts/install.ps1 | iex
```

## Install manually

Download the archive for your operating system and architecture from
[GitHub Releases](https://github.com/nulifyer/guget/releases). Extract `guget`
or `guget.exe` into a directory on PATH. Release installers verify the archive
against the published SHA-256 checksums.

Check the installed command:

```bash
guget version
dotnet --version
```

Then open your workspace:

```bash
guget --project ./src
```

## Install from source

Install Go 1.26 or newer, then clone the repository:

```bash
git clone https://github.com/nulifyer/guget
cd guget
```

On Linux or macOS:

```bash
./.scripts/build-install.sh
```

On Windows PowerShell:

```powershell
.\.scripts\build-install.ps1
```

These scripts build and install the checkout. `GUGET_INSTALL` also selects their
destination. Contributor checks are in [CONTRIBUTING.md](../CONTRIBUTING.md).

## Update Guget

Check for a newer stable release:

```bash
guget self-update --check
```

Quit running TUI sessions, then install the update:

```bash
guget self-update
guget version
```

The installation directory must be writable. Development builds must install a
release first. If a package manager owns your installation, update through that
manager instead. `guget update PACKAGE` updates a NuGet reference, while
`guget self-update` updates the Guget executable.

The TUI shows a release notice when an update is available. Press `Ctrl+X` to
dismiss that release's notice. Automatic checks are cached for 24 hours; an
explicit `self-update --check` checks directly.

To turn off automatic TUI update checks:

```bash
guget --project ./src --no-update-check
```

You can also set `GUGET_NO_UPDATE_CHECK=1` in the environment.

For PATH problems or interrupted updates, see [troubleshooting](troubleshooting.md).
