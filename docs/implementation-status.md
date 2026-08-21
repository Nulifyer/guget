# Guget improvement plan implementation

Status: active
Repository: /home/nulifyer/Projects/guget
Base: main at 8b03a84

## Goal
Implement the approved Guget improvement plan, including a shared package core,
safe edits, and first-class TUI and headless CLI flows.

## Acceptance checks
- [x] CI verifies Go formatting, modules, vet, unit tests, race tests, and the VS Code build.
- [x] Release and installers enforce verification, version, clean-tree, and checksum safeguards.
- [x] Package state distinguishes declared, evaluated, and resolved versions and ownership.
- [x] Supported mutations use planned atomic writes and rollback; unsupported ownership is read-only.
- [x] Bare `guget` launches the TUI and documented verbs run without a terminal.
- [x] CLI mutation commands use the same plans as the TUI and support dry-run and explicit scope.
- [ ] Background work is cancellable, stale-safe, and bounded. Core processes and metadata fan-out are covered; legacy metadata/release-note paths remain.
- [x] Source mapping and provenance follow documented NuGet trust behavior.
- [x] Documentation and automated checks describe and verify the implemented architecture.

## Scope
In: Go CLI/TUI, project and NuGet modeling, mutation safety, workflows, installers,
VS Code build verification, fixtures, tests, and contributor documentation.
Out: full packages.config mutation, .NET tool management, arbitrary dynamic MSBuild
mutation, signing release binaries, and publishing or committing changes.

## Current state
Phase: first milestone implemented; verification and review in progress
Next action: run final cross-platform, workflow, script, and documentation checks.

## Decisions
| Decision | Reason | Evidence |
| --- | --- | --- |
| Keep bare invocation as the TUI and dispatch recognized verbs first | Preserves current UX and matches the approved CLI design | docs/cli-design.md |
| Put package behavior in shared modules used by both front ends | Prevents CLI and TUI from disagreeing | docs/improvement-plan.md |
| Refuse ambiguous edits | MSBuild evaluation cannot be reproduced by regex-based XML scans | docs/dotnet-nuget-research.md |
| Deliver read-only CLI commands before mutation | Inspection is useful while mutation safety is still under proof | docs/cli-design.md |
| Return exit code 7 when declared/evaluated data is useful but restore data is stale or unavailable | Prevents scripts from mistaking a partial graph for a complete result | guget/cli.go |
| Keep local and UNC feeds visible as restore-only | Preserves source provenance without pretending the V3 browser can search directories | guget/nuget_source_detector.go |
| Apply only the strongest package-source mapping pattern | Matches NuGet trust semantics and prevents wildcard feeds from defeating a specific mapping | guget/nuget_source_mapping.go |

## Findings
| Finding | Evidence | Status |
| --- | --- | --- |
| Current unit, race, vet, module, formatting, and whitespace checks pass | docs/improvement-plan.md, Evidence base | confirmed |
| Previous write helpers edited files in place and covered fewer XML forms than the parser | guget/project_parser.go and regression tests | resolved |
| Previous TUI mutations changed memory before disk completion | guget/tui_actions.go and tui_section_location.go | resolved |
| Previous source mapping fell back to all feeds and lacked strongest-pattern precedence | guget/nuget_source_mapping.go tests | resolved |
| Uppercase `NuGet.Config` could be skipped after a missing lowercase path was cached as seen | CLI search regression test | resolved |
| Not every legacy metadata/release-note request has request-scoped cancellation yet | docs/architecture.md | open, later lifecycle hardening |

## Changes
| Path or artifact | Change | Verification |
| --- | --- | --- |
| docs/*.md | Added research, CLI design, and approved plan | reference and whitespace checks passed |
| .github/workflows/*.yml | Added CI/integration gates and made release depend on verified, embedded version output | local YAML review; hosted execution pending |
| install.sh, release.sh, release.ps1, download-stats.sh | Hardened checksum, clean-tree, tag, and script behavior | ShellCheck and local Go gates |
| guget/internal/atomicfile | Added permission-preserving atomic replacement and directory durability | unit tests and Windows cross-compile |
| guget/internal/edit | Added hash-preflight logical plans and rollback reporting | injected failure/stale tests |
| guget/internal/packageops | Added cancellable MSBuild ownership and versioned restore-graph adapters | injected runner tests and local .NET 10 probe |
| guget/project_parser.go | Added exact XML-node plans for Include/Update, VersionOverride, child versions, CPM, and literal-owner refusal | parser regression tests |
| guget/cli.go | Added early headless dispatch, read/search/mutation/restore verbs, formats, atomic output, dry-run, and stable exit codes | dispatcher tests |
| guget/tui*.go | Removed optimistic writes, reloads disk after commit, added root process context, bounded metadata workers, requested-version labels, and cell-width truncation | unit and race tests |
| guget/nuget_*.go | Enforced mapping trust, context-aware search/startup, restore-only feeds, and secret redaction | unit tests |
| docs/architecture.md, docs/project-support.md, docs/contributing.md | Documented ownership, support, verification, and release flow | Markdown link and whitespace checks pending final pass |

## Risks and unknowns
- Evaluated MSBuild item provenance varies by installed SDK; fixtures and capability probes must settle the editable subset.
- Multi-file replacement cannot be atomic as one filesystem operation; rollback must report incomplete recovery.
- Node/npm was unavailable during the initial review, so local VS Code verification may remain unavailable.
- Multi-file replacement is transactional through rollback, not one filesystem-atomic operation.
- Source metadata, release-note, and credential-provider cancellation still has legacy paths to consolidate after the first milestone.

## Verification

Final local pass on 2026-08-21:

- `go mod verify`, `go vet ./...`, `go test ./...`, and `go test -race ./...` passed.
- Windows amd64 test-binary cross-compilation passed for the root package and atomic replacement package.
- ShellCheck passed for installer, build/install, release, and download-statistics scripts.
- `git diff --check` passed.
- A release-stamped binary returned the embedded version through the headless `version` command.
- A .NET 10 fixture produced versioned JSON with requested/resolved versions and separate reference/version owners; CLI dry-run reported the expected project path without changing it.
- Statement coverage is 24.1% in the root package, 63.0% in `internal/atomicfile`, 75.5% in `internal/edit`, and 69.8% in `internal/packageops`.
- Node/npm is not installed in this environment, so the VS Code extension build is delegated to the new CI job and has not been claimed as a local pass.
- `go mod tidy` could not complete because the managed Go module cache is read-only when it tries to fetch dependency test-only modules. The newly imported ANSI module was promoted from indirect to direct manually; module verification and all builds pass.
