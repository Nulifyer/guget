# Guget CLI design

Design snapshot: 2026-08-21

Guget should support two first-class flows in one binary:

- `guget` starts the Bubble Tea interface.
- `guget <verb>` runs headlessly and returns control to the shell.

The CLI is not a wrapper that drives the TUI. Both front ends call the same
package inspection and mutation modules. This keeps MSBuild evaluation, NuGet
resolution, edit ownership, transactions, and restore behavior consistent.

## What we are taking from SQLGo

SQLGo dispatches recognized verbs before it parses TUI flags. Its headless
package accepts argument slices and injected stdin, stdout, and stderr streams.
Each verb owns its flags, and the dispatcher returns a stable exit code. Bare
invocation remains interactive.

That shape fits Guget. We should also carry over these details:

- machine output goes only to stdout;
- diagnostics go only to stderr;
- tests can invoke the dispatcher without replacing process-global streams;
- output-file writes commit only after the command succeeds;
- help, version, and completion are verbs handled without starting the TUI;
- a headless invocation does not initialize a renderer, file watcher, or TUI log
  buffer.

SQLGo's query runner is not reusable here. Guget needs package-specific scope,
ownership, edit planning, source, and restore semantics.

## Module shape

```text
                         +----------------------+
guget with no verb ----> | Bubble Tea adapter   |
                         +----------+-----------+
                                    |
                                    v
                         +----------------------+
guget <verb> ----------> | CLI adapter          |
                         +----------+-----------+
                                    |
                                    v
                         +----------------------+
                         | packageops module    |
                         | inspect, plan, apply |
                         | restore              |
                         +----------+-----------+
                                    |
             +----------------------+----------------------+
             v                      v                      v
       MSBuild/NuGet          edit transaction       process/files
       adapters               implementation         adapters
```

The `packageops` module should be deep. Its interface exposes completed domain
operations, not XML nodes, HTTP calls, Bubble Tea messages, console formatting,
or rollback steps. Both front ends use the same interface and domain results.

A likely concrete interface is:

```go
type Manager struct { /* dependencies */ }

func (m *Manager) Inspect(ctx context.Context, req InspectRequest) (Snapshot, error)
func (m *Manager) Plan(ctx context.Context, snapshot Snapshot, change Change) (Plan, error)
func (m *Manager) Apply(ctx context.Context, plan Plan) (ApplyResult, error)
func (m *Manager) Restore(ctx context.Context, req RestoreRequest) (RestoreResult, error)
```

This is a design target, not a frozen Go signature. Fixture work may show that
inspection and resolved-graph loading need separate methods. The important seam
is that CLI and TUI callers receive the same snapshots, plans, and results.

## Invocation and compatibility

Verb dispatch happens before the current TUI flag parser:

```text
guget                         launch the TUI
guget --project ./repo        launch the TUI for a workspace
guget list --project ./repo   run a headless command
guget help update             show verb help
```

The current `--project` option means workspace directory. Keep that meaning for
backward compatibility. Mutation verbs use repeatable `--file` arguments for
specific project files and `--all` for the entire inspected workspace.

Every verb gets an isolated parser and usage text. The parser must accept common
flag placement on either side of positional package IDs. Tests should cover both
`guget update --file App.csproj Example.Core` and `guget update Example.Core
--file App.csproj`.

Unknown verbs return a usage error. They must not fall through and accidentally
launch the TUI.

## Initial command set

| Command | Purpose | Writes files? |
| --- | --- | --- |
| `guget list` | List package uses, requested/resolved versions, owners, and status | No |
| `guget show PACKAGE` | Show one package across projects and target frameworks | No |
| `guget search QUERY` | Search configured sources and show compatible versions | No |
| `guget sources` | Show configured sources, mapping, capabilities, and audit origin | No |
| `guget add PACKAGE` | Plan or add a reference to explicit project files | Yes |
| `guget update PACKAGE` | Plan or update explicit files, or an explicitly requested workspace scope | Yes |
| `guget remove PACKAGE` | Plan or remove references from explicit files or workspace scope | Yes |
| `guget restore` | Run restore for explicit files or workspace scope | Generated restore output |
| `guget completion SHELL` | Emit shell completion | No |
| `guget version` | Print the version | No |
| `guget help [COMMAND]` | Print command help | No |

`audit` can become a dedicated command after Guget models `auditSources` and
coverage. Until then, `list --vulnerable` can filter known results but must label
partial or unavailable audit data.

## Common flags

Read commands should share:

```text
--project DIR                 workspace root, preserving today's meaning
--framework TFM              repeatable evaluation filter
--source NAME                repeatable metadata-source filter
--format table|tsv|json|jsonl
--output FILE                write data atomically
--no-color
--timeout DURATION
```

Package listing should also support filters such as `--outdated`, `--vulnerable`,
`--deprecated`, `--direct`, and `--include-transitive`. Filters operate on the
shared package-use model, not a second CLI-only interpretation of project XML.

Mutation commands add:

```text
--file PROJECT               repeatable explicit target
--all                        explicit workspace-wide target
--dry-run                    print the edit plan without writing
--restore                    restore affected projects after a successful edit
--interactive                allow an authentication provider to prompt
```

`restore` also accepts `--interactive` and passes it to `dotnet restore`.

`add` requires at least one `--file`. `update` and `remove` require one or more
`--file` values or `--all`. They never guess a target from a TUI selection or use
the current package row. `--file` and `--all` are mutually exclusive.

Version selection should be explicit and shared with the TUI:

```text
--version EXPRESSION
--latest-compatible
--latest-stable
```

These selectors are mutually exclusive. The eventual default must be chosen only
after the evaluated-model fixtures prove how Guget validates compatibility across
multiple target frameworks.

## Noninteractive behavior

The CLI must be useful in scripts and CI:

- it never prompts unless `--interactive` was supplied;
- `--all` itself is explicit consent to workspace scope;
- ambiguous ownership, unsupported MSBuild shapes, stale file hashes, and lock
  conflicts fail before a write;
- `--dry-run` runs the same inspection and planning path as an actual edit;
- cancellation from the parent context or interrupt stops network and dotnet
  processes and does not commit an incomplete output or edit plan;
- repeated no-op operations return success and report that nothing changed;
- bulk mutations use one logical edit transaction rather than a loop of
  independent CLI writes.

The CLI should not print spinners, cursor controls, status bars, or decorative
symbols when stdout or stderr is not a terminal.

## Output contract

Human output defaults to a table on a terminal. Piped output defaults to TSV.
Callers that need a stable contract should request JSON explicitly.

JSON documents should include a schema version from the first release:

```json
{
  "schemaVersion": 1,
  "command": "list",
  "workspace": "/repo",
  "projects": [],
  "warnings": []
}
```

Rules:

- stdout contains only the selected data format;
- warnings, progress, and errors go to stderr;
- JSON field meanings remain stable within a schema version;
- paths and package IDs are data, never preformatted display strings;
- requested expressions and resolved versions have separate fields;
- per-target-framework results stay separate;
- warnings identify stale restore data and incomplete source or audit coverage;
- `--output` uses atomic replacement and leaves an existing destination unchanged
  when the command fails.

## Exit codes

Reserve a small stable set and test it through the dispatcher:

| Code | Meaning |
| --- | --- |
| `0` | Success, including a valid no-op or empty search result |
| `1` | Invalid command, flags, or target scope |
| `2` | Workspace discovery or MSBuild evaluation failed |
| `3` | NuGet source, authentication, or metadata operation failed |
| `4` | Edit refused because ownership is unsupported, ambiguous, locked, or stale |
| `5` | Write failed or rollback needs attention |
| `6` | Restore or post-edit validation failed |
| `7` | Read command returned useful but explicitly partial results |
| `130` | Interrupted by the user |

The CLI should also return typed errors internally. Exit-code mapping belongs in
the CLI adapter, not the package modules.

## Delivery slices

1. Add verb dispatch, help, version, completion, stable exit codes, and injected
   streams. Keep bare invocation unchanged.
2. Add read-only `list`, `show`, and `sources` on the evaluated workspace model,
   with table, TSV, and versioned JSON output.
3. Add `search` through the shared NuGet module with cancellation and source
   capability reporting.
4. Add `--dry-run` for add, update, and remove once transactional edit plans exist.
5. Enable mutation by applying those exact plans. Add explicit scope and restore
   options.
6. Add integration tests across SDK versions, CPM fixtures, local/private source
   behavior, redirected streams, interrupts, and output-file failures.

Read-only commands should ship before mutation commands. That gives scripts useful
inspection while the edit safety contract is still being proven.

## Acceptance criteria

- TUI and CLI tests receive identical snapshots and edit plans for the same
  fixture and request.
- Headless commands do not initialize Bubble Tea or require a terminal.
- Every mutation supports `--dry-run`, and applying the plan changes only the
  files and nodes shown by that dry run.
- JSON output parses with no stderr text mixed into stdout.
- All documented exit codes have dispatcher-level tests.
- Redirected stdin/stdout/stderr, interrupt cancellation, and atomic `--output`
  behavior have regression tests.
- Bare `guget` and current TUI flags keep their existing behavior.
