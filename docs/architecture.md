# Guget architecture

Architecture snapshot: 2026-08-21

Guget has two front ends in one binary. Bare invocation starts Bubble Tea;
recognized verbs dispatch before TUI flags, rendering, watchers, or log buffers
are initialized. Both flows use the same project parsers and edit-plan builders.

## Data flow

```text
project and props XML --------> declared package references
          |                                  |
          +--> dotnet msbuild JSON ----------+--> package-use view
          |       evaluated items/owners     |    per target framework
          |                                  |
          +--> dotnet package list JSON -----+    requested + resolved
                  restored graph

NuGet.Config hierarchy --> sources + strongest source mapping
                                  |
                                  +--> HTTP V3 metadata adapters
                                  +--> restore-only local/UNC sources
```

Repository text remains the authority for byte-preserving edits. MSBuild is the
authority for effective `PackageReference` and `PackageVersion` items. Versioned
`dotnet package list` output is the authority for the resolved restore graph.
When either external view is unavailable, Guget labels the result partial rather
than turning a range lower bound into an alleged installed version.

## Package boundaries

- `internal/packageops` contains UI-independent evaluated and resolved package
  models plus cancellable MSBuild and dotnet process adapters.
- `internal/edit` validates expected hashes and applies a logical set of file
  changes with rollback reporting.
- `internal/atomicfile` performs same-directory temporary writes, flushes data,
  preserves permissions, and uses platform-specific replacement.
- `cli.go` owns verb parsing, streams, formats, and exit-code mapping.
- `tui*.go` owns Bubble Tea state, input, layout, and presentation.
- `project_parser.go` discovers declared references and produces source-preserving
  changes for the supported XML shapes.
- `nuget_*.go` owns configuration discovery, source trust, credentials, and V3
  metadata.

The remaining `main` package is intentionally being separated incrementally.
Interfaces are introduced only at process, feed, and file seams exercised by
tests; moving files without changing ownership is not an architectural goal.

## Mutation transaction

```text
inspect explicit scope
  -> prove reference/version owner
  -> generate every resulting file in memory
  -> capture path + mode + before hash
  -> preflight every hash
  -> atomic replacement in plan order
  -> on failure, replace completed targets with their original bytes
  -> reload disk state
  -> optional cancellable dotnet restore
```

A CPM add is one logical plan: the central `PackageVersion` and project-local
`PackageReference` either both apply or rollback is reported. Removing a project
reference never implicitly removes its shared central version.

## Process lifetime

The TUI program receives a root context canceled by interrupt or program exit.
Owned restore, dependency-list, startup source discovery, and workspace-reload
processes inherit it. CLI dispatch accepts a caller-provided context; dotnet
evaluation, restore, and output commit paths check or inherit cancellation.
Workspace generations prevent late reload results from replacing newer state.

Network requests have client timeouts. Work remains to move every metadata and
release-note request onto request-scoped contexts and a shared bounded worker
pool; this limitation is tracked in [implementation status].

## Trust and logging

When Package Source Mapping is configured, only sources tied at the strongest
matching pattern are eligible. No match, or a mapped source that is unavailable,
produces no eligible metadata source. Guget does not fall back to every feed.
Local directory and UNC sources remain visible as restore-only capabilities.

The logging boundary redacts registered credentials, URL user information, and
common secret query/header keys before text reaches stderr, a file, or the TUI
log panel.

[implementation status]: implementation-status.md
