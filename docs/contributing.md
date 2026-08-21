# Contributing to Guget

## Verification tiers

Run the local gate from `guget/`:

```bash
go mod verify
gofmt -w .
git diff --exit-code
go vet ./...
go test ./...
go test -race ./...
```

Run integration tests when changing MSBuild, NuGet feeds, restore graphs, or SDK
compatibility:

```bash
go test -tags=integration -timeout=20m ./...
```

The scheduled integration workflow installs the .NET SDK and runs this tier.
The normal CI workflow also builds `vscode-guget` with `npm ci` and `npm run
build`, and runs ShellCheck over release and installer scripts.

## Where changes belong

- Package-use domain or dotnet evaluation: `guget/internal/packageops/`
- Atomic replacement: `guget/internal/atomicfile/`
- Logical transactions and stale-file checks: `guget/internal/edit/`
- Source-preserving XML plan generation: `guget/project_parser.go`
- Headless behavior and output contracts: `guget/cli.go`
- Bubble Tea behavior and layout: `guget/tui*.go`
- NuGet source, mapping, and credentials: `guget/nuget_*.go`

Add failure-path tests for stale results, cancellation, partial writes, rollback,
and unsupported ownership. Network-dependent cases belong behind the
`integration` build tag; unit tests should use injected runners or local HTTP
servers.

## Release checks

Release tags run the same Go verification before GoReleaser. The workflow builds
a binary with the tag-derived version and executes `guget version` before any
artifact is published. Release scripts refuse dirty worktrees and existing tags;
installers require published checksum verification.

See [architecture](architecture.md), [project support](project-support.md), and
the [.NET/NuGet research](dotnet-nuget-research.md) before expanding mutation
support.
