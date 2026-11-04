# Contributing to Guget

Use Go 1.26 or newer. Install the .NET SDK to exercise package inspection and
restore against real projects. Extension work also needs Node.js 22 or newer.

## Build and run

From the repository root, build the application:

```bash
cd guget
go build .
```

Run `./guget --project ../test-dotnet` on Linux or macOS, or
`.\guget.exe --project ..\test-dotnet` in Windows PowerShell.

For a local installation, run `.scripts/build-install.sh` or
`.scripts/build-install.ps1` from the repository root. Set `GUGET_INSTALL` to
choose the destination.

## Check Go changes

Run from `guget/`:

```bash
go mod verify
gofmt -l .
go vet ./...
go test ./...
go test -race ./...
```

Format any files reported by `gofmt -l` before submitting the change.

For MSBuild, NuGet feed, restore, or SDK behavior, also run:

```bash
go test -tags=integration -timeout=20m ./...
```

Integration tests contact public services and use pinned external repository
fixtures. Keep unit tests deterministic with temporary projects, injected
runners, and local HTTP servers.

## Check extension changes

Run from `vscode-guget/`:

```bash
npm ci
npm run build
npx tsc --noEmit
npm run package
npm audit
```

## Check shell changes

Run from the repository root:

```bash
shellcheck .scripts/*.sh
```

Exercise PowerShell changes on a host with PowerShell installed.

## Update dependencies

Update Go dependencies with `go get -u -t ./...`, followed by `go mod tidy`
and the checks above. Preserve the effective `github.com/xo/terminfo` replacement
at `v0.0.0-20220910002029-abceb7e1c41e`. Its original rationale was not recorded;
review removing that pin separately from routine dependency updates.

For extension dependencies, update `package.json` and `package-lock.json`
together, then run the extension checks.

## Submit changes

Describe the user-visible change and the checks you ran. Add focused coverage
for behavior changes, especially ownership refusals, cancellation, stale results,
and failure recovery.

Keep README focused on the product. Put end-user instructions and examples in
`docs/`. Keep research, audit reports, and implementation plans outside the
repository.
