# Contributing to Guget

## Verification tiers

Use Go 1.26 or newer to build and test the CLI.

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

Use Node.js 22 or newer for extension builds and packaging. The VS Code
packaging tool requires this runtime.

## Dependency updates

Preserve the effective `github.com/xo/terminfo` version at
`v0.0.0-20220910002029-abceb7e1c41e`. This revision stayed fixed through earlier
dependency upgrades, but the original reason was not recorded. Keep it as a
conservative pin until its removal is reviewed separately.

The `replace` directive in `guget/go.mod` enforces this pin. Newer Ultraviolet
requires terminfo v1, so the version in the indirect `require` entry can differ
from the code used to build Guget. Verify the effective revision with
`go list -m -json github.com/xo/terminfo` and check `Replace.Version`.

Update other Go dependencies with `go get -u -t ./...`, then run `go mod tidy`
and the verification tiers above. For the extension, update the development
dependencies and lockfile together, then run the build, type-check, and
`npm audit` checks.

## Where changes belong

- Package-use domain or dotnet evaluation: `guget/internal/packageops/`
- Atomic replacement: `guget/internal/atomicfile/`
- Logical transactions and stale-file checks: `guget/internal/edit/`
- Source-preserving XML edit planning: `guget/internal/projectedit/`
- Shared mutation scope and ownership policy: `guget/package_mutations.go`
- Project and imported props discovery: `guget/project_parser.go`
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
