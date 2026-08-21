# Building a reliable TUI in Go

Research snapshot: 2026-08-21

This document combines current Bubble Tea v2 behavior, terminal constraints, and
official Go guidance into engineering rules for Guget. It is not a generic style
guide; each recommendation addresses work Guget already performs: network
requests, file watching, project edits, child processes, overlays, and responsive
rendering.

## The architecture Guget already chose

Guget uses Bubble Tea v2.0.7, Bubbles v2.1.0, and Lip Gloss v2.0.3. Bubble Tea is
based on the Elm architecture:

```text
terminal event or completed effect
              |
              v
       Update(message)
         /          \
new model        command (I/O)
    |                |
    v                +---- later message ----+
 View()                                    Update
```

The model owns application state. `Update` processes messages and returns a new
model plus an optional command. A command performs I/O and returns a message when
complete. `View` renders the current model after an update.

Bubble Tea v2 makes terminal modes declarative through `tea.View`: alternate
screen, mouse mode, focus reporting, bracketed paste, cursor, colors, and window
title belong to the rendered view. Guget already returns `tea.View`; new UI code
should keep terminal state there.

Sources: [Bubble Tea README], [Bubble Tea v2 upgrade guide], and [Bubble Tea v2
API].

## One owner for mutable UI state

Only `Update` should mutate the model. Background work should capture immutable
inputs, perform the work, and return a typed result message. It must not retain a
model pointer and mutate it from a goroutine.

```go
type packageLoadedMsg struct {
    generation uint64
    packageID  string
    result     PackageResult
    err        error
}

func loadPackage(ctx context.Context, generation uint64, id string) tea.Cmd {
    return func() tea.Msg {
        result, err := fetchPackage(ctx, id)
        return packageLoadedMsg{generation, id, result, err}
    }
}
```

On receipt, `Update` verifies that the result still belongs to the active request.
This turns concurrency into state-machine transitions that can be tested without
races or timing guesses.

Guget's reload generation checks are a useful start. They should become a shared
pattern for every workflow rather than remain specific to workspace reload.

## Commands, ordering, and cancellation

Bubble Tea commands are the side-effect boundary. `tea.Batch` runs commands
concurrently and gives no result ordering guarantee. `tea.Sequence` is for work
that must start in order. Neither replaces domain-level transactions or
cancellation.

Use one cancellation scope for the program and narrower scopes for replaceable
work:

- workspace scan and metadata generation;
- search query and debounce timer;
- package detail and release notes;
- dependency-tree process;
- restore process;
- file watcher;
- mutation validation.

When a user changes selection, replaces a search, reloads the workspace, closes an
overlay, or quits, cancel work that can no longer produce a useful result. Keep a
generation or request ID as a second defense against clients that finish during
cancellation.

Go's `context` package specifies that contexts are passed explicitly, normally as
the first parameter, and that returned cancellation functions must be called.
`exec.CommandContext` ties child-process lifetime to the same mechanism. Bubble
Tea also accepts a program context with `tea.WithContext`.

Sources: [Go `context`], [Go pipelines and cancellation], [Go `os/exec`], and
[Bubble Tea v2 API].

## Bound concurrency deliberately

One goroutine per discovered package or source is easy to write but creates a
load spike in large repositories. Guget should set explicit limits for network
and process work:

- a worker pool or semaphore for package metadata;
- a smaller per-source limit so one feed cannot occupy every worker;
- request coalescing for the same package/source/version;
- cancellation-aware queue submission;
- bounded retry with jitter only for retryable failures;
- separate concurrency for CPU-light parsing and slow network operations.

The UI should remain usable while work is queued. Progress state should expose
completed, active, queued, failed, and canceled counts instead of only a generic
loading flag.

## Model workflows explicitly

Boolean combinations become invalid states as a TUI grows. Prefer a small status
enum plus operation identity:

```text
idle -> planning -> writing -> validating -> succeeded
                    |             |
                    +-----------> failed
```

For a package edit, model the confirmed disk snapshot separately from an edit
plan. Do not optimistically update the displayed installed version before the
write succeeds. On success, reload from disk; on failure, retain the confirmed
snapshot and show the error and recovery result.

Overlays should behave as a stack or explicit modal state:

- only the topmost modal receives keys;
- global keys are intentionally whitelisted;
- opening captures the selected package/project identity;
- closing cancels work owned only by that overlay;
- a reload closes mutation overlays whose captured identity is stale;
- focus returns to a valid underlying control.

This is the Bubble Tea equivalent of SQLGo's top-layer input ownership and
stale-callback checks, without replacing Bubble Tea's event loop.

## Rendering is measured in terminal cells

Go string length, byte length, rune count, and terminal cell width are different.
ANSI escape sequences occupy bytes but no cells; combining marks and wide glyphs
complicate rune-based calculations. Guget already depends indirectly on
`github.com/charmbracelet/x/ansi`, whose helpers measure, truncate, and wrap ANSI
strings by display width. Layout code should use one cell-width abstraction
throughout.

Responsive behavior should be deterministic at every `WindowSizeMsg`:

1. Reserve fixed rows for header, status, and help.
2. Give focused/primary content a documented minimum.
3. Collapse optional columns and panels in a stable order.
4. Clamp cursors and viewports after content or dimensions change.
5. Render a purposeful minimum-size view instead of negative dimensions or
   corrupted borders.

Use full-window golden tests at representative widths, including narrow screens,
wide Unicode package names, no-color mode, long errors, and nested overlays.

Sources: [`x/ansi` display-width API], [Bubble Tea chat example], and [Bubble Tea
v2 upgrade guide].

## Input and accessibility

Bubble Tea v2 distinguishes `tea.KeyPressMsg` from the broader `tea.KeyMsg`
interface, which can include release events when keyboard enhancements are
enabled. Commands should normally react to presses only. Key handling should use
a central key map so help text and behavior cannot drift.

A usable TUI should not depend on color or mouse input:

- pair color with text, shape, or symbol;
- retain `--no-color` and test it;
- provide keyboard access for every action;
- use consistent confirm/cancel keys;
- keep errors visible until acknowledged or superseded;
- label source, security, and compatibility states in text;
- avoid rapid animation as the only indication of work.

For paste and mouse support, declare the modes in `tea.View` and treat pasted text
as untrusted input. Terminal titles and hyperlinks must sanitize control
characters before emitting OSC sequences.

## Files, processes, network clients, and secrets

### Files

Never rewrite a project with `os.WriteFile` in place. Generate bytes first, create
a temporary file in the same directory, preserve permissions, write, call
`File.Sync`, close, and replace the target with a platform-specific implementation.
The standard `os.Rename` contract is OS-specific when the target already exists,
so Windows replacement behavior needs its own adapter and tests.

Before replacement, compare the file identity or content hash captured during
planning. A file watcher, IDE, or build may have changed it. Multi-file changes
need preflight, backups, and rollback reporting because several replacements
cannot be one filesystem transaction.

### Child processes

Use `exec.CommandContext`, pass arguments directly instead of constructing a shell
command, capture bounded output, and report the exact project being processed.
Cancellation should end the subprocess tree as reliably as each platform allows.
The dotnet SDK version and command capabilities should be probed once and cached.

### Network clients

Accept context on every request path. Configure timeouts, limit response sizes,
close bodies, respect authentication challenges, and classify errors into
canceled, authentication, capability unavailable, rate-limited, temporary, and
permanent. Cache keys must include source and relevant credentials/configuration
identity, not only package ID.

### Secrets

Redaction belongs at the logging boundary. It should cover authorization headers,
tokens, passwords, user-info URLs, credential-provider output, and configured
secret values. Tests should assert that representative errors cannot leak them.

Sources: [Go `os`], [Go `os/exec`], and [Go security best practices].

## Working effectively in Go

Go rewards direct control flow and explicit ownership. For Guget, that means:

- prefer a small concrete type until two real callers need an interface;
- define a narrow interface beside the code that consumes it;
- make the zero value useful when doing so is clear, and otherwise require a
  constructor that validates dependencies;
- return errors to the layer that can add project, package, source, or operation
  context; do not both log and return the same error at every layer;
- use `defer` immediately after acquiring a resource when the lifetime is scoped
  to the function, while checking errors from meaningful flush and close calls;
- copy slices and maps when an immutable snapshot crosses a goroutine boundary;
- keep goroutine creation near its cancellation and completion ownership;
- prefer table-driven tests for behavior matrices and small fakes over elaborate
  mocking frameworks;
- keep module dependencies intentional, verified, and updated in reviewable
  groups rather than automatically accepting every latest release.

These conventions support the more important design objective: packages should
hide complicated implementation decisions behind a small, stable surface. The
workspace model should not expose XML details to the TUI, and the edit package
should not expose filesystem rollback mechanics to package actions.

Sources: [Effective Go], [Go Code Review Comments], and [Go security best
practices].

## Go code organization for Guget

Guget is currently one large `main` package. Splitting by filename helps
navigability but does not create test seams. The next architecture should create
a few deep packages with narrow responsibilities, introduced as feature work
needs them:

```text
cmd/guget             composition, flags, process lifetime
internal/packageops   shared inspection and mutation operations
internal/workspace    immutable snapshots and package-use domain model
internal/msbuild      dotnet/MSBuild evaluation adapter
internal/nuget        source capabilities and V3 metadata clients
internal/edit         edit plans, source-preserving changes, commit/rollback
internal/cli          headless verbs, formatting, and exit-code mapping
internal/tui          Bubble Tea model, messages, views, and key routing
```

These are ownership boundaries, not a demand for many interfaces. Put interfaces
at the caller side where tests need to replace an evaluator, feed client, writer,
clock, or process runner. Keep data types independent of terminal widgets so
workspace and edit behavior can be tested without rendering a screen. The CLI and
TUI should be adapters over `packageops`; neither should implement package
ownership or mutation rules itself.

Use wrapped errors with operational context, and inspect causes with `errors.Is`
or `errors.As`. Avoid package-global mutable state for services, logging, and send
callbacks; construct dependencies in `main` and pass them inward.

## Testing strategy

### Pure model and reducer tests

Call `Update` with typed messages and assert the next state and returned command.
Cover stale generations, cancellation, selection changes, overlay routing, error
retention, cursor clamping, and reload during an edit.

### Rendering tests

Render fixed model states at fixed dimensions. Compare ANSI-normalized golden
output and separately assert cell widths. Bubble Tea's `WithWindowSize`, custom
input, and custom output options can drive a smaller number of runtime-level
tests. Do not make the whole suite depend on terminal timing.

### Domain fixture tests

Maintain .NET repository fixtures for:

- literal and child-element versions;
- `Include`, `Update`, and `Remove`;
- imported props and nested central files;
- conditions and multiple target frameworks;
- version ranges, floating versions, and property expressions;
- `VersionOverride`, global references, and transitive pinning;
- lock files, local feeds, source mappings, and legacy `packages.config`;
- whitespace, comments, BOMs, CRLF, multiple elements per line, and concurrent
  external edits.

Each supported mutation needs round-trip, failure, rollback, and idempotence tests.
Unsupported shapes need tests proving that Guget refuses the edit without changing
bytes.

### Tooling gates

Run `gofmt`, `go mod verify`, `go vet`, unit tests, and `go test -race` in CI.
Add bounded fuzz targets for XML/package-expression parsing and edit planning.
Run `govulncheck` as a separate security gate with an explicit update policy.
Keep live NuGet and .NET fixture tests in an integration workflow so the unit suite
remains deterministic.

The race detector only observes executed paths, so concurrency regression tests
must actively exercise cancellation and late results. Go fuzzing should use fast,
deterministic targets with no persistent global state.

Sources: [Go race detector], [Go fuzzing], [Go security best practices], and [Go
vulnerability management].

## Current Guget findings that drive the plan

The repository review found these high-value gaps:

- file writes retry `os.WriteFile` but are not atomic;
- update and removal flows mutate the in-memory model before disk writes finish;
- a CPM add performs two independent writes without rollback;
- write regexes do not cover the same XML forms the parser recognizes;
- CPM removal can target the central version owner instead of the project's
  reference owner;
- metadata loading starts an unbounded goroutine per package;
- several long-running HTTP/process workflows lack shared cancellation;
- test coverage is 15.5%, and integration-tagged tests do not protect the normal
  unit path;
- release automation publishes without first running the full verification suite;
- installer and documentation environment-variable names differ.

The recent SQLGo hardening work demonstrates reusable patterns: platform-specific
atomic replacement behind one API, cancellation and generation checks, regression
tests around asynchronous ownership, separate deterministic and live integration
workflows, release verification, and a test that proves packaged binaries contain
the release tag.

## Primary sources

- [Bubble Tea README]
- [Bubble Tea v2 upgrade guide]
- [Bubble Tea v2 API]
- [Bubble Tea examples]
- [`x/ansi` display-width API]
- [Go `context`]
- [Go pipelines and cancellation]
- [Go `os`]
- [Go `os/exec`]
- [Effective Go]
- [Go Code Review Comments]
- [Go race detector]
- [Go fuzzing]
- [Go security best practices]
- [Go vulnerability management]

[Bubble Tea README]: https://github.com/charmbracelet/bubbletea/blob/main/README.md
[Bubble Tea v2 upgrade guide]: https://github.com/charmbracelet/bubbletea/blob/main/UPGRADE_GUIDE_V2.md
[Bubble Tea v2 API]: https://pkg.go.dev/charm.land/bubbletea/v2
[Bubble Tea examples]: https://github.com/charmbracelet/bubbletea/tree/main/examples
[Bubble Tea chat example]: https://github.com/charmbracelet/bubbletea/blob/main/examples/chat/main.go
[`x/ansi` display-width API]: https://pkg.go.dev/github.com/charmbracelet/x/ansi
[Go `context`]: https://pkg.go.dev/context
[Go pipelines and cancellation]: https://go.dev/blog/pipelines
[Go `os`]: https://pkg.go.dev/os
[Go `os/exec`]: https://pkg.go.dev/os/exec
[Effective Go]: https://go.dev/doc/effective_go
[Go Code Review Comments]: https://go.dev/wiki/CodeReviewComments
[Go race detector]: https://go.dev/doc/articles/race_detector
[Go fuzzing]: https://go.dev/doc/security/fuzz
[Go security best practices]: https://go.dev/doc/security/best-practices
[Go vulnerability management]: https://go.dev/doc/security/vuln
