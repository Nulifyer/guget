# Troubleshoot Guget

Start by checking the executable and SDK:

```bash
guget version
dotnet --version
```

Use an explicit workspace directory in subsequent commands. The examples assume
`src/App/App.csproj` exists beneath your current directory.

## The command is not found

On Linux or macOS, check the executable selected by your shell:

```bash
command -v guget
```

The default installer uses `/usr/local/bin` or `~/.local/bin`. Open a new terminal
after installation and confirm its installation directory is on PATH.

On Windows PowerShell:

```powershell
Get-Command guget
```

The default Windows installation is
`%LOCALAPPDATA%\Programs\guget\guget.exe`. Restart VS Code if it was running
when PATH changed. A custom `GUGET_INSTALL` destination also needs to be on PATH.

## VS Code opens a different version

Set `guget.binaryPath` to the absolute executable you want. The extension checks
standard installation directories before PATH, so it may find a different copy
from your shell. See the [extension settings](../vscode-guget/README.md#settings).

## No projects or unexpected projects appear

`--project` takes a directory, not a solution or project filename:

```bash
guget list --project ./src --format json
```

Choose a directory above the intended projects. Guget scans its descendants and
excludes common build-output directories. Check that project XML is readable
and complete, especially during an editor save.

For CLI changes, `--file` is relative to `--project`:

```bash
guget update Newtonsoft.Json --version 13.0.4 --project ./src --file App/App.csproj --dry-run --format json
```

Opening a project from the VS Code context menu selects its directory, which can
contain additional projects. Select the intended project inside the TUI.

## The TUI asks for a larger terminal

Resize to at least 80 columns by 12 rows. In VS Code, widen the terminal editor
or hide surrounding panels. Smaller dimensions show a resize message and disable
package actions.

## Updating another package does nothing yet

Wait for the saving or reloading indicator to finish, then retry the action.
Package changes are serialized. Use `Ctrl+R` if an external edit has not appeared,
especially after changes to parent props or configuration files.

For a controlled single-project change, use the CLI preview/apply steps in the
[CLI guide](cli.md#preview-and-apply-an-update).

## Private feed authentication fails

Inspect the source names and URLs:

```bash
guget sources --project ./src --format json
```

Check the feed's V3 service-index URL and the credentials or credential provider
used for that source. A 401/403 response indicates an authentication or access
problem. Search and restore can differ, so check both:

```bash
guget search Newtonsoft.Json --project ./src --source company --format json --timeout 30s
guget restore --project ./src --file App/App.csproj --interactive
```

Replace `company` and the package ID with values from your feed. Run interactive
authentication in a terminal. Keep credentials out of committed files and shared
logs. See [private feeds](sources.md#use-a-private-feed).

## Sources exist but search returns no results

Local and UNC feeds are `restore-only`; they cannot supply search metadata.
For an HTTP(S) feed, check the configured source name and any package-source
mapping patterns. A package with no eligible mapped source will not fall back
to unrelated feeds. See [source mapping](sources.md#restrict-packages-to-sources).

Empty results can also mean that no matching package was found. Check the JSON
`warnings` and exit status to distinguish a successful empty search from feed
failure or incomplete results.

## JSON listing is partial or has no restored version

Exit code 7 indicates a useful partial read result. Inspect `warnings` in full
JSON output:

```bash
guget list --project ./src --format json
```

Confirm that the SDK required by the project is installed. Restore the selected
project and inspect again:

```bash
guget restore --project ./src --file App/App.csproj
guget list --project ./src --format json
```

A missing restored version remains unknown. Do not treat the requested version
expression as proof of the version resolved by restore. JSONL and text formats
do not include the full JSON warnings document.

## Restore failed after an edit

Inspect the package before repeating an add, update, or remove:

```bash
guget show Newtonsoft.Json --project ./src --format json
```

A restore failure does not undo a successful reference change. Correct the feed,
SDK, compatibility, or lock-file problem, then rerun restore. For a shared CPM
change, restore every affected project.

For a write or rollback error, inspect the reported files and your version-control
diff before taking further action.

## A version says today or has no date

Version age uses the metadata feed's publication timestamp. A mirror can report
when it received the package. When public metadata is available, an age labeled
`nuget.org:` identifies that feed's publication date separately. Missing, sentinel,
or future timestamps produce no age. Compare the package's publishing page if
you need to confirm its release history.

## Mouse input interferes with text selection

Use your terminal's mouse-bypass modifier, often Shift, or start Guget with
mouse input disabled:

```bash
guget --project ./src --no-mouse
```

## An interrupted Guget update left files behind

On Windows, a previous executable can remain as `guget.exe.old`. A later update
normally removes it. If `guget.exe` is missing after interruption, restore
`guget.exe.old` to `guget.exe` before retrying.

If a `guget.exe.update-lock` directory remains beside the executable on Windows,
confirm that no self-update is running before removing it. On Linux or macOS,
the corresponding directory is `guget.update-lock`. The installation directory
must be writable. See [updating Guget](installation.md#update-guget).

## Capture diagnostics

Start the TUI with a log file:

```bash
guget --project ./src -v debug --log-file guget.log
```

Press `l` to view logs in the TUI. The panel retains recent entries; use the file
for a longer session. Review logs for sensitive information before sharing them.
Include your Guget version, operating system, .NET SDK version, command, and the
error message when reporting a problem.
