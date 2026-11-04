# Manage packages from the CLI

Use a command after `guget` to run without opening the TUI. The examples below
assume `./src` is your workspace and `src/App/App.csproj` is a project inside it.
Replace package IDs, versions, and paths with your own.

```bash
guget version
guget help
guget help update
```

## Inspect packages

```bash
guget list --project ./src --format json
guget show Newtonsoft.Json --project ./src --format json
```

Listing distinguishes the requested version expression from a restored version
when that information is available. Missing evaluation or restore information
can produce partial results. Prefer JSON and inspect `warnings` before relying
on a result.

Filter a listing by framework, or include restored transitive dependencies:

```bash
guget list --project ./src --framework net8.0 --format json
guget list --project ./src --direct --format json
guget list --project ./src --include-transitive --format json
```

The current CLI does not implement the `--outdated`, `--vulnerable`, or
`--deprecated` listing filters. Use the TUI to browse the available package
metadata and notices.

## Search configured sources

```bash
guget sources --project ./src --format json
guget search Newtonsoft.Json --project ./src --format json --timeout 30s
guget search Newtonsoft.Json --project ./src --source nuget.org --format json --timeout 30s
```

`--source` takes a configured source name. Check the name with `sources` first.
Search returns package results, not a complete version history or a compatibility
guarantee. Local and UNC feeds support restore but cannot supply search metadata.
See [package sources](sources.md).

## Preview and apply an update

Preview an explicit version and project:

```bash
guget update Newtonsoft.Json --version 13.0.4 --project ./src --file App/App.csproj --dry-run --format json
```

Inspect every path in `changes`, especially shared props files. Then apply the
requested update and restore:

```bash
guget update Newtonsoft.Json --version 13.0.4 --project ./src --file App/App.csproj --restore
```

Apply rereads the workspace and creates a fresh plan. A dry run is a preview,
not a saved plan that a later command applies. If files change between commands,
preview again.

Verify the result:

```bash
guget show Newtonsoft.Json --project ./src --format json
```

Use an explicit `--version`. The current CLI refuses `--latest-compatible` and
`--latest-stable`.

## Choose project scope

`--project` is the workspace directory. `--file` names a discovered project,
relative to that directory, and can be repeated:

```bash
guget update Newtonsoft.Json --version 13.0.4 --project ./src --file App/App.csproj --file Worker/Worker.csproj --dry-run --format json
```

For every discovered project, `update` and `remove` accept `--all`:

```bash
guget update Newtonsoft.Json --version 13.0.4 --project ./src --all --dry-run --format json
```

Choose either `--file` or `--all`. `add` requires one or more `--file` values.
Changes to a shared central version may affect additional projects even when
only one reference was selected. See [CPM and shared props](project-support.md).
Ensure all intended projects are readable before a bulk change, and review the
changed files afterward.

## Add a package

```bash
guget add Newtonsoft.Json --version 13.0.4 --project ./src --file App/App.csproj --dry-run --format json
guget add Newtonsoft.Json --version 13.0.4 --project ./src --file App/App.csproj --restore
guget show Newtonsoft.Json --project ./src --format json
```

In a CPM workspace, check the existing central version before adding a reference.
Use that version when the package is already centrally defined, then verify the
effective result. The CLI does not offer the TUI's choice of add locations.

## Remove a package

```bash
guget remove Newtonsoft.Json --project ./src --file App/App.csproj --dry-run --format json
guget remove Newtonsoft.Json --project ./src --file App/App.csproj --restore
```

Removal leaves a shared central version in place. Guget may refuse removal of a
reference inherited from a shared props file.

## Restore projects

```bash
guget restore --project ./src --file App/App.csproj
guget restore --project ./src --all
```

For feed authentication that needs a prompt, run in an interactive terminal:

```bash
guget restore --project ./src --file App/App.csproj --interactive
```

On add/update/remove, `--interactive` requires `--restore` as well.
A failed restore can follow a successful file edit. Check the changed reference
before retrying; restore failure does not roll the edit back.

## Choose output for scripts

Read commands and package mutations accept `table`, `tsv`, `json`, and `jsonl`.
Terminal output defaults to a table, while redirected output defaults to TSV.
JSON includes the surrounding document and warnings. Read commands and dry runs
emit rows in JSONL; mutation apply emits one result document.

A listing document uses these top-level fields:

```json
{
  "schemaVersion": 1,
  "command": "list",
  "workspace": "/path/to/src",
  "packages": [],
  "warnings": []
}
```

Other commands use their own row collections: `sources`, `results` for search,
and `changes` for dry runs. Mutation apply returns `status` (`changed`,
`unchanged`, or `failed`), `changed` paths, and a `restore` outcome. With
`--restore`, the outcome is `succeeded`, `failed`, or `skipped` when no edit was
needed; otherwise it is `not-requested`. A failed edit can also include
`rolledBack`, `conflicting`, and `unrecovered` paths. Inspect unrecovered files
and preserved external edits before retrying.

Save output with `--output`, or capture stdout:

```bash
guget list --project ./src --format json --output packages.json
guget list --project ./src --format json > packages.json
```

Check the exit status and warnings before consuming the file. A failed or
interrupted search preserves an existing output file. A useful partial search
result replaces the output and exits with code 7. Every format sends warnings
to stderr; JSON also includes them in its document.

Output format and file destinations are checked before a package mutation.
If writing the result fails after an edit, the command exits with code 5 and
explains that changes were already applied. Inspect the project before retrying.

For example, consume a JSON listing in Python while retaining partial results:

```python
import json
import subprocess

result = subprocess.run(
    ["guget", "list", "--project", "./src", "--format", "json"],
    capture_output=True,
    text=True,
)
if result.returncode not in (0, 7):
    raise SystemExit(result.stderr or f"guget exited {result.returncode}")

data = json.loads(result.stdout)
for warning in data["warnings"]:
    print(warning)
for package in data["packages"]:
    print(package["package"], package["requestedExpression"])
```

## Read exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success, including no change needed or an empty search. |
| `1` | Invalid command, flags, or project scope. |
| `2` | Workspace discovery or evaluation failure. |
| `3` | Source, authentication, or metadata failure. |
| `4` | Edit refused because ownership, locking, or version selection is unsupported. |
| `5` | File write or rollback failure. Inspect changed files before retrying. |
| `6` | Restore or post-edit validation failure. The edit may already be present. |
| `7` | Partial read result. Inspect warnings before using it. |
| `130` | Interrupted. Check the current state before retrying a mutation. |

To update the Guget executable, use `guget self-update` as described in
[installation and updates](installation.md#update-guget).
