# Manage packages from the CLI

Use a command after `guget` to run without opening the TUI. The examples below
assume `./src` is your workspace and `src/App/App.csproj` is a project inside it.
Replace package IDs, versions, and paths with your own.

```bash
guget version
guget help
guget help update
guget update --help
```

`--help` and `-h` print a command's usage on stdout and exit 0 for every
command. The help lists every flag the command accepts, with its default, and
the fields its templates can use.

Workspace flags may come before the command. These two lines are equivalent:

```bash
guget --project ./src list
guget list --project ./src
```

Without a command, `guget` and `guget ./src` start the TUI. `--no-color` is
accepted everywhere but has no effect on CLI output, which is never colored.

## Inspect packages

```bash
guget list --project ./src --format json
guget show Newtonsoft.Json --project ./src --format json
```

`list` returns one row per project, package, and target framework. Rows are
sorted by package ID ignoring case, then project path, then framework. Listing
distinguishes the requested version expression from the restored version.
When restore metadata or MSBuild evaluation is missing, the affected values are
`null`, a warning explains why, and the command exits 7. Inspect `warnings`
before relying on such a result.

`show` of a package that no project references is not an error. It exits 0
with an empty `packages` array and a warning, so test for emptiness.

Filter a listing by framework, or include restored transitive dependencies:

```bash
guget list --project ./src --framework net8.0 --format json
guget list --project ./src --direct --format json
guget list --project ./src --include-transitive --format json
```

The `--outdated`, `--vulnerable`, and `--deprecated` filters are accepted but
not implemented yet. They exit 7 without a listing. Use the TUI to browse
package metadata and notices.

Package rows have these fields:

| Field | Meaning |
| --- | --- |
| `project` | Project file name. |
| `projectPath` | Absolute project path, as the workspace scan found it. |
| `package` | Package ID. |
| `requestedExpression` | Version or range as written, or `null` for transitive rows. |
| `resolvedVersion` | Version chosen by the last restore, or `null` when unknown. |
| `versionOwner` | File that owns the version, or `null` when unknown. |
| `ownerPath` | Alias of `versionOwner`, kept for compatibility. |
| `referenceOwner` | File that declares the reference, or `null`. |
| `targetFramework` | Target framework of this row, or `null` when the project declares none. |
| `targetFrameworks` | The same framework as a one-element array, or `[]`. |
| `direct` | `true` for direct references, `false` for transitive packages. |
| `implicit` | `true` for references added by the SDK. |
| `editSupported` | `true` when Guget can edit this reference. |
| `editReason` | Why the row is read-only, or `null`. |
| `locked` | `true` for an exact pin such as `[1.2.3]`. |
| `global` | `true` for a `GlobalPackageReference`, which applies to every project importing its file. |
| `privateAssets` | The reference's `PrivateAssets`, such as `All` for a global package, or `null`. |
| `download` | `true` for a read-only `PackageDownload` row. Downloads are restore-only and not dependencies. |

## Search configured sources

```bash
guget sources --project ./src --format json
guget search Newtonsoft.Json --project ./src --format json
guget search Newtonsoft.Json --project ./src --source nuget.org --format json --timeout 10s
```

`--source` takes a configured source name and can be repeated. Check names
with `sources` first; an unknown name exits 1. Search stops after 30 seconds
unless you pass `--timeout`. Search returns package results, not a complete
version history or a compatibility guarantee. Local and UNC feeds support
restore but cannot supply search metadata, so search skips them with a warning
and still exits 0. When some sources fail, the results come from the rest and
the command exits 7. When every searched source fails, it exits 3. See
[package sources](sources.md).

Source URLs never show credentials. A URL such as
`https://user:token@feed.example/index.json` is printed as
`https://user:***@feed.example/index.json` in every format and message.

Source rows have these fields:

| Field | Meaning |
| --- | --- |
| `name` | Source name from NuGet.Config. |
| `url` | Source URL with credentials redacted. |
| `capability` | `metadata` for HTTP sources, `restore-only` for local and UNC feeds. |
| `mapped` | `true` when package source mapping assigns patterns to the source. |
| `patterns` | Mapped package patterns, or `[]`. |

Search rows have these fields:

| Field | Meaning |
| --- | --- |
| `package` | Package ID. |
| `version` | Latest version the source reports. |
| `description` | Package description, or an empty string. |
| `authors` | Package authors, or `[]`. |
| `source` | Name of the source that returned the package. |

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

Use an explicit `--version`. An invalid version string exits 1 before Guget
reads the workspace. The `--latest-compatible` and `--latest-stable` selectors
are accepted but refused with exit 4 until they are implemented.

### Exact pins

A reference pinned to one version, such as `Version="[13.0.1]"`, is left alone
by `update --all`. The plan lists it with the operation `skip (pinned)`, and
`skipped` counts such rows. Updating a pinned project named with `--file` is
refused with exit 4. Add `--include-pinned` to update pins in either scope; the
edit keeps the exact-version brackets.

## Choose project scope

`--project` is the workspace directory. `--file` names a discovered project,
relative to that directory, and can be repeated:

```bash
guget update Newtonsoft.Json --version 13.0.4 --project ./src --file App/App.csproj --file Worker/Worker.csproj --dry-run --format json
```

`update` and `remove` also accept `--all`, which selects every discovered
project that references the package. Projects without the package are not
evaluated, so they cannot stop the plan:

```bash
guget update Newtonsoft.Json --version 13.0.4 --project ./src --all --dry-run --format json
```

Choose either `--file` or `--all`; both or neither exits 1. `add` requires one
or more `--file` values. Changes to a shared central version may affect
additional projects even when only one reference was selected. See
[CPM and shared props](project-support.md). Review the changed files afterward.

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

Plan rows, in `changes`, have these fields:

| Field | Meaning |
| --- | --- |
| `operation` | `add reference`, `add central version`, `update version`, `remove reference`, or `skip (pinned)`. |
| `package` | Package ID. |
| `version` | Version written, the pinned version for a skip, or an empty string for a removal. |
| `path` | File the operation edits. |

## Restore projects

```bash
guget restore --project ./src --file App/App.csproj
guget restore --project ./src --all --format json
```

Restore runs `dotnet restore` for each project in turn. A failing project does
not stop the others; the command exits 6 when any project failed. `dotnet`
output goes to stderr, and the result rows go to stdout.

For feed authentication that needs a prompt, run in an interactive terminal:

```bash
guget restore --project ./src --file App/App.csproj --interactive
```

On add, update, and remove, `--restore` also restores every project an edit
affects: projects that read a changed shared file, such as
`Directory.Packages.props`, and projects that reference those. Guget prints
each added project and why on stderr. A project whose lock file is in locked
mode is restored with `--force-evaluate`, so NuGet regenerates its
`packages.lock.json`. See [project support](project-support.md#restore-after-an-edit).

On add, update, and remove, `--interactive` requires `--restore` as well.
A failed restore can follow a successful file edit. Check the changed reference
before retrying; restore failure does not roll the edit back.

Restore rows, in `projects`, have these fields:

| Field | Meaning |
| --- | --- |
| `project` | Project file name. |
| `projectPath` | Absolute project path. |
| `status` | `succeeded` or `failed`. |
| `error` | Why the restore failed, or `null`. |

## Manage settings

`guget config` reads and changes the user settings file described in
[settings](settings.md):

```bash
guget config path
guget config show --format json
guget config get theme
guget config set theme nord
guget config unset theme
```

`show` is the only subcommand that takes `--format`, `--output`, and
`--no-headers`. Its document has `path`, `settings`, and `warnings` after the
common header. `--output` refuses to replace the settings file. Completion
offers the subcommands, and the setting keys after `get`, `set`, and `unset`.

Setting rows, in `settings`, have these fields:

| Field | Meaning |
| --- | --- |
| `key` | Setting name, such as `theme`. |
| `value` | Effective value: a JSON boolean for `mouse`, `update-check`, and `color`, otherwise a string. |
| `source` | `env`, `file`, or `default`. |
| `default` | Built-in default, typed like `value`. |
| `description` | What the setting controls. |

`set` and `unset` exit 1 for an unknown key or a value that is not allowed,
before touching the file, and exit 5 when the file cannot be written.

## Choose an output format

Every command that produces rows accepts `--format`, `--output`, and
`--no-headers`. That covers `list`, `show`, `sources`, `search`, `add`,
`update`, `remove`, `restore`, and `config show`. `--format` takes one of these values:

| Format | Output |
| --- | --- |
| `table` | Aligned columns with a header row. |
| `tsv` | Tab-separated columns with a header row. |
| `json` | One document with the rows, warnings, and status. |
| `jsonl` | One JSON row per line. |
| A Go template | One line per row, rendered from the template. |

The default is `table` only when stdout is a terminal and no `--output` file is
named (`--output -` means stdout). Otherwise the default is `tsv`, so redirected
output and files are tab-separated. Pass `--format` explicitly in scripts.

`--no-headers` omits the header row of `table`, `tsv`, and table templates.

### Templates

A template uses Go [text/template](https://pkg.go.dev/text/template) syntax.
Its fields are the JSON keys of one row, exactly as in `json` and `jsonl`
output, so `{{.package}}` reads the `package` key. Type `\t` for a tab.

```bash
guget list --project ./src --format '{{.package}} {{.requestedExpression}}'
guget list --project ./src --format '{{.package}}@{{.resolvedVersion}}'
guget search Serilog --project ./src --format '{{.package}}{{"\t"}}{{.version}}'
guget restore --project ./src --all --format '{{.project}} {{.status}}'
```

A `table ` prefix aligns the columns and adds a header row of the field names
in upper case:

```bash
guget list --project ./src --format 'table {{.project}}\t{{.package}}\t{{.resolvedVersion}}'
```

```text
PROJECT     PACKAGE          RESOLVEDVERSION
App.csproj  Newtonsoft.Json  13.0.4
```

A `null` value prints as empty text and is false in `{{if}}`. Templates can
call four functions: `json` (encode a value as JSON, so `null` stays `null`),
`join` (join a list with a separator), `lower`, and `upper`:

```bash
guget list --project ./src --format '{{.package}} {{join .targetFrameworks ","}} {{json .resolvedVersion}}'
```

Guget checks the template and its field names before it reads the workspace,
runs MSBuild, or contacts a feed. A syntax error or an unknown field exits 1 and
lists the valid fields. For add, update, and remove, a dry run renders the plan
rows; applying renders the one result document, so its fields are the
document's top-level keys such as `status` and `changed`.

### Output files

Save output with `--output`, or capture stdout:

```bash
guget list --project ./src --format json --output packages.json
guget list --project ./src --format json > packages.json
```

`--output` never overwrites a workspace input such as a project, a props file,
or a NuGet.Config. Guget checks the destination before any evaluation, network
request, or edit, and exits 5 if it is unusable. The file is replaced
atomically after the result renders.

Every format sends warnings to stderr; JSON also includes them in `warnings`.
If writing the result fails after an edit, the command exits 5 and explains
that changes were already applied. Inspect the project before retrying.

## Read JSON documents

Every JSON document starts with the same keys:

```json
{
  "schemaVersion": 1,
  "command": "list",
  "status": "ok",
  "error": null,
  "workspace": "/path/to/src",
  "packages": [],
  "warnings": []
}
```

Every key is always present. Arrays are `[]` when empty, and unknown values are
`null`. `schemaVersion` stays 1 while changes only add keys.

The rows sit under a key named for the command:

| Command | Rows | Other keys |
| --- | --- | --- |
| `list`, `show` | `packages` | `workspace`, `warnings` |
| `sources` | `sources` | `workspace`, `warnings` |
| `search` | `results` | `workspace`, `query`, `warnings` |
| `add`, `update`, `remove` | `changes` | `workspace`, `dryRun`, `skipped`, `changed`, `restore`, `rolledBack`, `conflicting`, `unrecovered`, `warnings` |
| `restore` | `projects` | `workspace`, `warnings` |
| `config show` | `settings` | `path`, `warnings` |

`status` is one of these values:

| Status | Meaning |
| --- | --- |
| `ok` | The read finished with complete data. |
| `partial` | Some requested data could not be produced; see `warnings`. |
| `dry-run` | A mutation plan was previewed. |
| `changed` | A mutation edited files, listed in `changed`. |
| `unchanged` | A mutation found nothing to edit. |
| `refused` | The edit policy refused the change. |
| `invalid` | Input such as a `--source` name or `--file` path was not valid for the workspace. |
| `failed` | Workspace, source, write, or restore failure. |
| `timed-out` | `--timeout` expired. |
| `interrupted` | The command was interrupted. |

For a mutation, `restore` is `not-requested`, `skipped-dry-run` for a dry run
with `--restore`, `skipped` when no edit was needed, `succeeded`, or `failed`.
A failed edit can list `rolledBack`, `conflicting`, and `unrecovered` paths.
Inspect unrecovered files and preserved external edits before retrying.

Paths in every command use the absolute form of the workspace scan. Guget does
not resolve symbolic links in output, so `list`, `show`, plans, and `restore`
report one file the same way.

### Errors

With `--format json`, a command that fails after its arguments are parsed still
writes one document to stdout, or to `--output`. The document has the failing
`status`, a message in `error`, and whatever data the command had gathered. If
the `--output` file itself was refused, the document goes to stdout. The exit
code is the same as without JSON.

Usage errors, such as an unknown flag or a missing argument, print only to
stderr. With `jsonl` and templates, failures also print only to stderr.

```bash
guget update Example --project ./src --file App/App.csproj --version 2.0.0 --format json
```

```json
{
  "schemaVersion": 1,
  "command": "update",
  "status": "refused",
  "error": "edit refused: package \"Example\" is not installed in the selected scope",
  "workspace": "/path/to/src",
  "dryRun": false,
  "changes": [],
  "skipped": 0,
  "changed": [],
  "restore": "not-requested",
  "rolledBack": [],
  "conflicting": [],
  "unrecovered": [],
  "warnings": []
}
```

## Read exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success, including no change needed, an empty search, or an unreferenced `show` package. |
| `1` | Usage or invalid input: unknown flag, conflicting flags, invalid version string or template, unknown `--source` name, or a `--file` that is not a discovered project. |
| `2` | Workspace or evaluation failure: bad `--project`, no projects, or `dotnet` and MSBuild unavailable or failing where an edit needs evaluation. |
| `3` | Source, authentication, or metadata failure. |
| `4` | Edit refused by ownership, pin, or version-selection policy. |
| `5` | File write or rollback failure, or an unusable `--output`. Inspect changed files before retrying. |
| `6` | Restore failure. An edit may already be present. |
| `7` | Partial result: some requested data could not be produced. Inspect warnings before using it. |
| `124` | `--timeout` expired. Guget prints `guget: timed out after` and the duration. |
| `130` | Interrupted. Check the current state before retrying a mutation. |

Warnings about permanent conditions do not change the exit code. Examples are a
restore-only source skipped by search and an unsupported package artifact such
as `packages.config`.

Codes each command can return besides 0, 1, 124, and 130:

| Command | Other codes |
| --- | --- |
| `list` | 2, 5, 7 |
| `show` | 2, 5, 7 |
| `sources` | 2, 5 |
| `search` | 2, 3, 5, 7 |
| `add`, `remove` | 2, 4, 5, 6 |
| `update` | 2, 4, 5, 6 |
| `restore` | 2, 5, 6 |
| `config` | 5 |
| `self-update` | 3, 4, 5 |
| `completion`, `version`, `help` | none |

## Use the CLI in CI and agents

Fail a pipeline when an edit is refused, and keep the refusal for the log:

```bash
if ! guget update Newtonsoft.Json --version 13.0.4 --project ./src --all --format json --output update.json; then
  jq -r '.status + ": " + (.error // "")' update.json
  exit 1
fi
```

Preview, check the plan, then apply:

```bash
guget update Newtonsoft.Json --version 13.0.4 --project ./src --all --dry-run --format json > plan.json
jq -r '.changes[] | "\(.operation)\t\(.path)"' plan.json
guget update Newtonsoft.Json --version 13.0.4 --project ./src --all --format json
```

List references whose restored version is unknown:

```bash
guget list --project ./src --format json | jq -r '.packages[] | select(.resolvedVersion == null) | "\(.project) \(.package)"'
```

Stream rows for line-oriented tools:

```bash
guget list --project ./src --format jsonl | jq -c 'select(.locked)'
guget list --project ./src --format '{{.package}}' --no-headers | sort -u
```

Treat exit code 7 as usable but incomplete. In Python:

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

Set `--timeout` on commands that read the workspace or contact feeds, so a slow
SDK or feed cannot stall a job. A timeout exits 124, which is distinct from an
interrupt.

## Complete commands in your shell

```bash
source <(guget completion bash)
source <(guget completion zsh)
guget completion fish | source
```

Completion offers commands, each command's flags, and fixed values such as
`--format` choices. In bash, flags are offered once you type `-`; otherwise
bash completes file and directory names, for example after `--project` or
`--output`.

To update the Guget executable, use `guget self-update` as described in
[installation and updates](installation.md#update-guget).
