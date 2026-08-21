# Guget improvement plan

Plan snapshot: 2026-08-21

This plan follows the repository review, the [.NET and NuGet research], the
[Go TUI research], the [CLI design], and a comparison with the 2026-08-21 SQLGo
hardening work. It is a proposal for approval, not a record of completed
implementation.

## Goal

Make Guget a trustworthy NuGet workspace manager: it should accurately explain
declared, evaluated, and resolved package state; edit only configurations it can
prove it owns; recover cleanly from failure; remain responsive under large
workspaces and slow feeds; provide consistent interactive and scriptable flows;
and release only artifacts that passed repeatable checks.

## What changed after research

The initial review suggested strengthening the existing parser/editor first. The
NuGet and MSBuild research changes that order.

The current parser treats project XML as the package model. That approach cannot
be made generally correct by adding more regular expressions because MSBuild
evaluates imports, properties, item operations, and conditions, while NuGet
resolves a separate graph per target framework. The revised plan first establishes
an evaluated ownership model and explicit support levels. Mutation work then
operates only on unambiguous owners.

This plan also separates a declared version expression from a resolved version.
For example, bare `1.0` is a minimum constraint in NuGet, not an exact installed
version. That distinction should reach the model and UI before Guget expands its
update logic.

## Evidence base

The Guget review used the clean `main` worktree at commit `8b03a84`. On
2026-08-21, `go test ./...`, `go test -race ./...`, `go vet ./...`, `go mod
verify`, formatting, and whitespace checks passed. Statement coverage was 15.5%.
The live NuGet and cloned-repository tests are behind the `integration` build tag,
so they are not part of the normal test run. Node/npm was unavailable in the
review environment, so the VS Code extension was inspected but not built locally.
ShellCheck also reported actionable issues in the Unix install/release/statistics
scripts.

The SQLGo comparison covered seven commits from 2026-08-21 (`311ca9e` through
`5f049e5`). The relevant changes were resilient connection and query lifecycles,
cross-platform atomic output, cancellation/stale-result regression tests, CI and
PostgreSQL integration workflows, installer and release guards, and verification
that packaged binaries contain the release tag. Those are patterns to adapt. Its
custom TUI event loop and database-specific architecture are not Guget templates;
Bubble Tea remains Guget's event-loop owner.

## Design principles

1. The repository text owns edits; MSBuild owns effective items; NuGet restore
   owns the resolved graph.
2. A read-only explanation is better than a silent, lossy, or ambiguous edit.
3. Disk is authoritative after a mutation. The UI never commits optimistic state.
4. Every asynchronous workflow has cancellation, an identity, and bounded work.
5. Tests cover failure and stale-result paths, not only success.
6. Release is the final consumer of CI, never the first place verification runs.
7. Package boundaries are introduced around ownership, not as a cosmetic rewrite.
8. The TUI and CLI are adapters over the same package-operation modules. Neither
   owns a second implementation of discovery, version choice, edits, or restore.

## Delivery sequence

Each phase is intended to be a reviewable pull request or a small series of pull
requests. A phase does not need to wait for unrelated later UI polish.

### Phase 1: Establish gates and executable specifications

Deliverables:

- Add CI for `gofmt`, `go mod verify`, `go vet`, unit tests, and race tests.
- Add a VS Code extension job that runs `npm ci` and `npm run build`.
- Keep live NuGet and cloned .NET fixture tests in a separate integration workflow.
- Make the release workflow depend on verification and test that the release tag
  is embedded in a built binary.
- Add fixture directories that encode the supported and unsupported MSBuild/NuGet
  shapes from the research documents.
- Add tests exposing the current unsafe cases: `Update`, child-element versions,
  `VersionOverride`, multiple XML elements on one line, CPM remove ownership,
  failed writes, and partial dual-file adds.
- Fix installer variable-name drift and require checksum verification instead of
  silently skipping it.

Borrow from SQLGo: the verification workflow shape, release-version assertion,
integration-workflow separation, and shell/PowerShell installer hardening.

Exit criteria:

- Pull requests cannot merge with format, vet, unit, or race failures.
- Releases cannot publish an unverified Go binary.
- Every known unsafe edit shape has a failing regression test or an explicit
  read-only expectation before mutation code changes.

### Phase 2: Introduce an authoritative package-use model

Deliverables:

- Create a workspace snapshot that separates declaration, evaluated item, and
  resolved package use.
- Preserve raw version expressions; stop converting every declaration directly
  into one semantic version.
- Represent reference owner and version owner separately.
- Represent target framework, direct/transitive/implicit status, lock state,
  source provenance, and edit support with a reason.
- Add a cancellable MSBuild adapter that probes `dotnet msbuild -getProperty` and
  `-getItem` JSON capabilities.
- Add a cancellable NuGet graph adapter using versioned `dotnet package list
  --format json` output where available, with SDK-version-aware verb ordering.
- Treat generated restore data as stale or unavailable rather than inventing a
  resolved result when restore has not run.
- Detect `packages.config`, tool manifests, implicit packages, and unsupported
  project shapes so the UI can explain them.
- Introduce a UI-independent `packageops` module for inspection, edit planning,
  application, and restore. Both front ends consume its domain results.

Research spike within this phase:

- Compare source provenance from evaluated item metadata across .NET SDK versions
  and the fixture matrix.
- Compare official `dotnet package add/remove` behavior for project-local and CPM
  cases. Use it as a mutation adapter only where its behavior is stable,
  noninteractive, and source-preserving enough for Guget's contract.

Exit criteria:

- Fixture snapshots identify requested and resolved versions per target framework.
- The UI can show an unsupported package without offering an unsafe action.
- Guget does not claim a bare range expression is the installed exact version.

### Phase 3: Replace direct writes with planned transactions

Deliverables:

- Add an `EditPlan` API that records intended node changes, expected file hashes,
  generated bytes, backups, validation, and rollback status.
- Port SQLGo's platform-specific atomic-output pattern, adapted for preserving the
  original permissions, BOM, newline convention, and surrounding XML.
- Generate and validate all affected files before replacing any target.
- Implement best-effort rollback with precise reporting for logical multi-file
  operations such as a CPM add.
- Apply a successful plan, then reload from disk; do not mutate the confirmed
  workspace snapshot beforehand.
- Split add, update, and remove by semantic operation:
  reference addition/removal is distinct from version-owner addition/removal.
- Support only the approved shapes in the research support table. Return typed,
  actionable errors for ambiguous ownership and concurrent external edits.
- Add restore validation as a cancellable follow-up with lock-file awareness.

Exit criteria:

- Injected failures at every write/replace step do not leave the UI claiming
  uncommitted state.
- Supported single-file changes are atomic on Linux, macOS, and Windows.
- Multi-file failure tests prove rollback behavior and report any incomplete
  recovery.
- Removing a CPM reference cannot accidentally delete a shared central version.
- Unsupported XML remains byte-for-byte unchanged.

### Phase 4: Add the headless CLI flow

Deliverables:

- Dispatch recognized verbs before TUI flag parsing while preserving bare
  `guget` and existing TUI flags.
- Keep the CLI in a package that does not initialize Bubble Tea or terminal
  rendering.
- Inject stdin, stdout, and stderr into one dispatcher and return documented,
  stable exit codes.
- Add read-only `list`, `show`, `search`, and `sources` commands first.
- Support terminal tables, piped TSV, versioned JSON/JSONL, and atomic
  `--output` files. Keep diagnostics on stderr.
- Add `add`, `update`, and `remove` through the same `EditPlan` path as the TUI.
  Every mutation supports `--dry-run`.
- Require explicit mutation scope through repeatable `--file` or `--all`; never
  infer a headless target from interactive state.
- Add a headless `restore` command plus help, version, and shell completion.
- Propagate cancellation to evaluation, feed requests, edits, and dotnet
  processes. Never prompt unless `--interactive` was supplied.
- Test that CLI and TUI requests produce the same snapshots and edit plans for
  the same fixtures.

Borrow from SQLGo: early verb dispatch, isolated verb parsing, injected streams,
stable exit codes, TTY-aware output defaults, and atomic output files. Guget adds
package ownership, dry-run plans, and explicit project scope.

Exit criteria:

- Bare `guget` still launches the TUI, while `guget <verb>` runs without a
  terminal.
- Versioned JSON contains no logs or progress text and preserves per-framework
  requested/resolved data.
- Every write shown by `--dry-run` matches the subsequently applied plan.
- Mutation and output failures return documented exit codes and preserve prior
  files whenever rollback succeeds.
- Dispatcher tests cover redirected streams, flag placement, empty results,
  partial results, interrupts, and each exit-code category.

The detailed command and output contract lives in [CLI design].

### Phase 5: Give asynchronous work a common lifecycle

Deliverables:

- Root Bubble Tea program context with cancellation on quit.
- A request identity and cancel function for reload, search, metadata, release
  notes, dependency tree, restore, and mutation validation.
- Bounded metadata workers with per-source limits and request coalescing.
- Context-aware HTTP and `exec.CommandContext` paths.
- Typed error categories and persistent, actionable status messages.
- Secret redaction at the logging boundary, with tests for URLs, credentials,
  headers, and provider output.
- Progress that distinguishes active, queued, failed, and canceled work.

Borrow from SQLGo: snapshot inputs before background work, deliver results only to
the UI owner, and reject stale callbacks after selection/session changes.

Exit criteria:

- Repeated search/reload/selection changes do not grow goroutines without bound.
- Quitting terminates owned HTTP, watcher, and child-process work.
- Race tests actively cover late completions and cancellation.
- Logs from representative failures contain no configured secrets.

### Phase 6: Make package state understandable in the TUI

Deliverables:

- Show requested and resolved versions separately where they differ.
- Show results per target framework instead of collapsing incompatible graphs.
- Label direct, transitive, implicit, centrally managed, overridden, locked,
  unsupported, and stale/unrestored states.
- Show reference owner, version owner, package source, and metadata/audit source
  without conflating them.
- Centralize key maps so help and behavior share one definition.
- Use ANSI-aware cell width for truncation and layout.
- Add deterministic narrow, medium, and wide layouts with a minimum-size view.
- Add golden tests for Unicode, no-color mode, long errors, focus, and overlays.
- Correct documentation that still says manual reload is `g`; the implementation
  and key table use `Ctrl+R`.

Exit criteria:

- Every visible action states its scope and the file or owner it will affect.
- No action is enabled for a package whose edit ownership is ambiguous.
- Golden layouts remain within the configured terminal width.
- Help text is generated from the same key definitions used by input handling.

### Phase 7: Align source behavior with NuGet trust rules

Deliverables:

- Treat an unmatched Package Source Mapping as an enforcement error, not a reason
  to query every source.
- Add local-directory and UNC source discovery or clearly mark them as restore-only
  until metadata browsing is implemented.
- Model source capabilities discovered from the V3 service index and degrade
  optional features independently.
- Separate package-source provenance from nuget.org metadata enrichment and audit
  provenance.
- Incorporate `auditSources` and make vulnerability coverage state visible.
- Test configuration hierarchy, `<clear />`, disabled sources, exact-vs-prefix
  mapping precedence, caches, and authentication failures.

Exit criteria:

- Guget never broadens a configured source trust boundary silently.
- A source missing search or registration capabilities cannot break unrelated
  sources or restore information.
- The UI explains whether vulnerability information is complete, partial, stale,
  or unavailable.

### Phase 8: Consolidate package boundaries and contributor documentation

Deliverables:

- Complete the incremental move from the monolithic `main` package into
  `packageops`, `workspace`, `msbuild`, `nuget`, `edit`, `cli`, and `tui`
  ownership boundaries.
- Keep interfaces at callers and limited to evaluator, feed client, writer,
  process runner, and clock seams that tests actually replace.
- Add an architecture document showing process lifetime, data flow, edit
  transaction flow, and extension points.
- Document the supported-project matrix, troubleshooting commands, integration
  test setup, and release checklist.
- Add bounded fuzz jobs for package expressions and source-preserving XML edit
  planning, plus a scheduled `govulncheck` policy.

Exit criteria:

- Core package discovery and edit planning can be tested without a terminal or
  live network.
- A contributor can locate ownership and run the right test tier from the docs.
- The release checklist is represented by automation rather than memory.

## Intended first milestone

The first milestone is phases 1 through 4. It produces a safer Guget with both
interactive and headless flows before adding new package-management breadth:

```text
fixtures and CI
      -> evaluated ownership model
      -> transactional edits for proven-safe shapes
      -> read-only CLI, dry-run, then shared mutations
```

Phases 5 through 8 build responsiveness, clearer UX, stricter source semantics,
and maintainable boundaries on top of that trustworthy core.

## Explicit non-goals for the first milestone

- Reimplementing MSBuild evaluation in Go.
- Mutating arbitrary conditional or dynamically imported MSBuild items.
- Full `packages.config` editing.
- Managing global or local .NET tools.
- Treating every transitive vulnerability as a direct package Guget should pin.
- A wholesale package move before tests establish the behavioral contract.
- A CLI-only parser, resolver, or editor that can disagree with the TUI.

## Approval outcome

Approval of this plan authorizes the first milestone to begin with Phase 1. Later
phases remain the agreed direction, but their detailed UI and compatibility
choices should be checked against what the evaluated-model spike proves.

[.NET and NuGet research]: dotnet-nuget-research.md
[Go TUI research]: go-tui-research.md
[CLI design]: cli-design.md
