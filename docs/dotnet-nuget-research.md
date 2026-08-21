# .NET and NuGet packaging research

Research snapshot: 2026-08-21

This document establishes the package-management model Guget should use. It
focuses on consuming NuGet packages because that is Guget's job, then covers
package production far enough to explain the metadata Guget displays.

The central conclusion is simple: project XML is source code for MSBuild, not a
flat package database. Guget needs to distinguish the text a repository declares,
the items MSBuild evaluates, and the dependency graph NuGet restores.

## The three package views

| View | Typical source | What it answers |
| --- | --- | --- |
| Declared | Project and imported MSBuild files | What did the repository author write, and where can it be edited? |
| Evaluated | MSBuild properties and `PackageReference` items | Which references exist for this project and build context after imports, properties, conditions, `Include`, `Update`, and `Remove`? |
| Resolved | Restore output, normally represented by `obj/project.assets.json` or `dotnet package list` | Which exact package version and assets were selected for each target framework? |

A package manager may need all three. The declared view owns safe edits. The
evaluated view explains effective configuration. The resolved view is the only
one that can accurately report the version and transitive graph actually in use.

## What a NuGet package is

A `.nupkg` is a ZIP archive with a `.nuspec` manifest and conventionally named
asset directories. The manifest carries identity, version, dependencies, license,
repository, and other metadata. Assets can be separated by target framework and
purpose, including `ref/` compile assemblies, `lib/` runtime assemblies,
`runtimes/` runtime-specific assets, `build/` MSBuild imports, analyzers, and
content files. NuGet selects compatible assets during restore.

SDK-style projects normally produce packages with `dotnet pack` or the MSBuild
`Pack` target. Package metadata can live in the project file; NuGet generates the
manifest. A hand-authored `.nuspec` remains useful for legacy and advanced
layouts. A package can contain multiple framework-specific implementations and
dependency groups. Symbol packages use `.snupkg` and normally complement Source
Link.

This matters to Guget because package compatibility is not a property of a
version alone. It depends on the consuming target framework and on the assets and
dependency groups in that package version.

Sources: [NuGet creation overview], [.nuspec reference], [multi-targeted package
layout], and [symbol packages].

## Ways a .NET repository can consume packages

### `PackageReference`

Modern projects declare direct dependencies as MSBuild items:

```xml
<ItemGroup>
  <PackageReference Include="Example.Core" Version="3.2.0" />
</ItemGroup>
```

Metadata can be attributes or child elements. Important metadata includes
`Version`, `VersionOverride`, `PrivateAssets`, `IncludeAssets`, `ExcludeAssets`,
`GeneratePathProperty`, `Aliases`, and `IsImplicitlyDefined`. References and
their containing `ItemGroup` can be conditional. Multi-targeted projects restore
one graph per target framework.

`PackageReference` records top-level dependencies; restore computes the transitive
closure. SDKs can add implicit references that tooling should not offer to update.
Starting with recent NuGet/.NET versions, package pruning can also change what
appears in the resolved graph without changing declarations.

### Central Package Management

NuGet Central Package Management (CPM) is enabled by
`ManagePackageVersionsCentrally` and normally places versions in
`Directory.Packages.props`:

```xml
<ItemGroup>
  <PackageVersion Include="Example.Core" Version="3.2.0" />
</ItemGroup>
```

Projects then declare a versionless `PackageReference`. The important rules are:

- Only the nearest `Directory.Packages.props` is discovered automatically. A
  child central file must explicitly import a parent if it wants inheritance.
- A child can modify an imported central item with `PackageVersion Update="..."`.
- Central versions can be conditional, commonly by `$(TargetFramework)`.
- `VersionOverride` on a project reference wins over the central version unless
  overrides are disabled.
- `GlobalPackageReference` adds a package to every project in the centrally
  managed scope and is typically used for build tooling.
- Transitive pinning can promote a transitive dependency to a direct dependency
  and can affect a package produced by `pack`.

CPM therefore requires two distinct ownership concepts: the project owns whether
it references a package, while a central file may own the version. Removing a
reference must not automatically remove a shared `PackageVersion`.

### Imported MSBuild files

Projects can import arbitrary `.props` and `.targets` files. MSBuild expands
imports in order, then evaluates properties and items. Properties can be
reassigned. Items can use `Include`, `Update`, and `Remove`, and any of these can
be conditional. `Directory.Build.props` is imported early by the standard build
and, like the central package file, normally uses nearest-file discovery unless
additional imports are authored.

Consequences for Guget:

- An XML tree walk cannot reproduce general MSBuild evaluation.
- The same package may have different effective metadata by target framework,
  configuration, runtime identifier, environment, or command-line property.
- A later `Update` can own the effective version even when an earlier `Include`
  introduced the reference.
- A source-preserving editor needs the exact defining node, not merely a file that
  happened to mention the package.

Modern MSBuild can emit evaluated properties and items as JSON with
`-getProperty` and `-getItem`. Guget should investigate this as its evaluated
view instead of implementing an MSBuild interpreter in Go. Relevant item metadata
must be validated against representative SDK versions before it is treated as an
edit-location contract.

### `packages.config`

Legacy projects can keep a `packages.config` beside the project. It contains a
flat list that includes direct and transitive packages, and uses a project-local
`packages` directory. It has different installation behavior and supports legacy
features that do not map cleanly to `PackageReference`.

NuGet recommends `PackageReference` when a migration is possible, but
`packages.config` is still a real format. Guget should detect it and identify the
project as read-only until explicit support is implemented. It must not silently
report that no packages exist.

### .NET tools

.NET tools are NuGet packages installed through a separate model. Global tools
are user-wide; local tools are recorded in a `dotnet-tools.json` manifest found by
walking parent directories. They are restored and updated with `dotnet tool`
commands, not `PackageReference` edits.

Tools are adjacent to Guget's domain but should be a separately designed feature.
Treating their manifest as a project reference would be incorrect.

### Things that look related but are not package declarations

- `ProjectReference` links projects and participates in the build graph, but is
  not a NuGet package reference.
- Framework references and SDK selection can provide assemblies without an
  editable package version.
- `project.json` is a retired NuGet management format. Current SDK/NuGet releases
  have removed support; detection should produce a migration-oriented message.

## Version syntax and resolution

NuGet versions use SemVer-like syntax with NuGet-specific normalization and range
rules. Guget must preserve the declared expression separately from any resolved
version.

| Declaration | Meaning |
| --- | --- |
| `1.0` or `1.0.0` | Minimum inclusive version, not an exact pin |
| `[1.0]` | Exactly `1.0` |
| `[1.0,2.0)` | At least `1.0` and below `2.0` |
| `1.*` | Floating version; choose the highest matching version |
| `*-*` | Floating syntax that can include prereleases, subject to NuGet rules |

Restore applies rules such as lowest applicable version, floating-version
selection, direct-dependency-wins, and cousin dependencies. A direct reference
can cause a lower transitive version to win and can produce a downgrade warning.
The graph is resolved independently for each target framework.

Guget currently models a parsed declaration as one `SemVer`. That loses range,
floating, property expression, and requested-versus-resolved meaning. A future
domain type should retain at least:

```text
PackageUse
  package ID
  target framework / evaluation context
  declared expression and declaration kind
  evaluated requested expression
  resolved version, if restore data exists
  direct / transitive / implicit status
  reference owner and version owner
  editable support level and reason
```

Sources: [NuGet package versioning] and [Dependency resolution].

## Restore, lock files, and the dependency graph

Restore evaluates the project, resolves the graph, selects compatible assets, and
writes generated files under `obj`, most importantly `project.assets.json` for
`PackageReference` projects. The global packages folder and HTTP cache can satisfy
requests before a feed is contacted.

`packages.lock.json` records resolved dependencies for repeatability. Locked mode
fails when declarations and the lock file disagree unless reevaluation is
explicitly requested. Guget should show lock status before offering an action and
should never label a declared minimum as the installed version when the resolved
version is available.

For a stable machine-readable view, `dotnet package list --format json
--output-version 1` can report requested and resolved top-level packages, with
optional transitive, outdated, deprecated, and vulnerable data. The noun-first
form is .NET 10-era syntax; .NET 9 and earlier use `dotnet list package`. Guget
must detect SDK capabilities rather than infer command spelling from today's SDK.

Sources: [Package restore], [global packages and caches],
[PackageReference lock files], and [`dotnet package list`].

## Sources, configuration, credentials, and trust

NuGet combines configuration from computer, user, and directory scopes. `<clear
/>` stops inherited values for a section. Package sources can be HTTP endpoints,
local directories, or UNC shares. For `PackageReference` restore, source order is
not priority order; sources cooperate, and a package already in a cache can avoid
a source lookup.

Package Source Mapping restricts package ID patterns to source keys. Once enabled,
every top-level and transitive package must match a mapping. An exact package ID
has precedence over a prefix. Falling back to every source when no mapping matches
would violate the feature's trust boundary.

Credentials can come from environment variables, configuration, and credential
providers. Clear-text secrets should be avoided. NuGet's encrypted config
passwords are only usable by the same Windows user on the same machine. Logs and
errors must redact usernames, tokens, passwords, source URLs with embedded
credentials, and credential-provider output.

NuGet audit runs during restore. `auditSources` can be separate from package
sources, and current SDK versions can audit direct and transitive packages. For a
transitive vulnerability, NuGet recommends first upgrading the nearest direct
dependency, then considering a direct pin or CPM transitive pin when necessary.

Current Guget gaps found during repository review:

- It ignores local and UNC package sources.
- Its source-mapping matcher does not give an exact ID precedence over a matching
  prefix, as NuGet does.
- If source mapping has no match, it falls back to all services. That is useful
  for availability but incorrect for enforcement and should become an explicit
  error or clearly labeled metadata-only fallback.
- It implements part of the configuration hierarchy itself, so its result can
  differ from the NuGet client when config sections, environment variables,
  credential providers, or evaluated restore properties are involved.
- It enriches private-feed metadata from nuget.org. That is valuable, but the UI
  must distinguish package origin from metadata/audit origin.

Sources: [`nuget.config` reference], [Package Source Mapping], [NuGet auditing],
and [global packages and caches].

## NuGet V3 feed behavior

A V3 source exposes a service index. Clients discover resource endpoints and
versions from that index rather than constructing every URL from the source URL.
Common resources include search, registrations, flat-container package content,
and vulnerability information. Private feeds do not necessarily implement every
resource, and authentication can be interactive.

Guget should expose capabilities per source and degrade feature by feature:
package restore may work even when search, deprecation metadata, README retrieval,
or vulnerability data is unavailable. One source's missing optional resource
should not make the whole workspace look broken.

Source: [NuGet V3 API overview].

## Adding, updating, and removing packages

The official `dotnet package add` command records a reference and normally runs a
restore-based compatibility check before the project is changed. .NET 9 and older
SDKs use `dotnet add package`. `dotnet package remove` has the same command-order
version distinction.

This makes the CLI a useful mutation adapter for ordinary project-local
references, but not a universal solution. Guget still needs explicit handling for
central version ownership, imported files, bulk edits, conditions, noninteractive
credentials, locked mode, and older project systems. A capability probe should
test representative cases before the plan delegates any edit to the CLI.

Every mutation path should follow these rules:

1. Evaluate and identify the reference owner and version owner.
2. Refuse unsupported or ambiguous ownership with an actionable explanation.
3. Build an edit plan without mutating UI state or disk.
4. Preflight every affected file, including content hashes and permissions.
5. Write same-directory temporary files, flush and close them, then replace target
   files with platform-specific atomic behavior where the platform permits it.
6. Treat a multi-file CPM change as one logical transaction with backups and
   rollback. No filesystem primitive makes multiple file replacements atomic.
7. Reload from disk after success. On failure, retain the last confirmed snapshot
   and report which files were restored or require attention.
8. Offer restore/validation as a cancellable follow-up and honor lock-file mode.

## Recommended Guget support boundary

| Shape | Discovery | Display | Mutation |
| --- | --- | --- | --- |
| Project-local, unconditional `PackageReference Include` with literal version | Yes | Declared + evaluated + resolved | Supported after transactional editor work |
| Versionless project reference with one unambiguous central `PackageVersion` | Yes | Show separate reference/version owners | Supported as a planned multi-file operation |
| `VersionOverride` | Yes | Show override and central baseline | Supported only at the override node |
| Conditional reference or central version | Per evaluation context | Per target framework | Require an explicit target/condition; otherwise read-only |
| `Update`, `Remove`, property expression, or arbitrary imported ownership | Evaluated | Yes, with provenance | Read-only until an exact source-node strategy is proven |
| Implicit SDK package | Evaluated | Yes, labeled SDK-controlled | Never offer a direct version edit |
| Transitive package | Resolved | Yes, per target framework | Guide to nearest direct dependency; do not edit as if direct |
| `packages.config` | Detect | Read-only summary | Out of scope initially |
| Local/global .NET tool | Detect separately | Future feature | Out of scope initially |

This boundary favors an honest read-only result over a plausible but unsafe edit.

## Primary sources

- [What is NuGet?]
- [PackageReference in project files]
- [Central Package Management]
- [NuGet package versioning]
- [Dependency resolution]
- [Package restore]
- [`nuget.config` reference]
- [Package Source Mapping]
- [NuGet auditing]
- [NuGet V3 API overview]
- [Evaluate MSBuild items and properties]
- [MSBuild build process]
- [Customize a build by directory]
- [`dotnet package add`]
- [`dotnet package remove`]
- [`dotnet package list`]
- [`packages.config` reference]
- [Install a .NET tool]
- [NuGet creation overview]
- [.nuspec reference]
- [multi-targeted package layout]
- [symbol packages]

[What is NuGet?]: https://learn.microsoft.com/en-us/nuget/what-is-nuget
[PackageReference in project files]: https://learn.microsoft.com/en-us/nuget/consume-packages/package-references-in-project-files
[Central Package Management]: https://learn.microsoft.com/en-us/nuget/consume-packages/central-package-management
[NuGet package versioning]: https://learn.microsoft.com/en-us/nuget/concepts/package-versioning
[Dependency resolution]: https://learn.microsoft.com/en-us/nuget/concepts/dependency-resolution
[Package restore]: https://learn.microsoft.com/en-us/nuget/consume-packages/package-restore
[global packages and caches]: https://learn.microsoft.com/en-us/nuget/consume-packages/managing-the-global-packages-and-cache-folders
[PackageReference lock files]: https://learn.microsoft.com/en-us/nuget/consume-packages/package-references-in-project-files#locking-dependencies
[`nuget.config` reference]: https://learn.microsoft.com/en-us/nuget/reference/nuget-config-file
[Package Source Mapping]: https://learn.microsoft.com/en-us/nuget/consume-packages/package-source-mapping
[NuGet auditing]: https://learn.microsoft.com/en-us/nuget/consume-packages/auditing-packages
[NuGet V3 API overview]: https://learn.microsoft.com/en-us/nuget/api/overview
[Evaluate MSBuild items and properties]: https://learn.microsoft.com/en-us/visualstudio/msbuild/evaluate-items-and-properties
[MSBuild build process]: https://learn.microsoft.com/en-us/visualstudio/msbuild/build-process-overview
[Customize a build by directory]: https://learn.microsoft.com/en-us/visualstudio/msbuild/customize-by-directory
[`dotnet package add`]: https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-package-add
[`dotnet package remove`]: https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-package-remove
[`dotnet package list`]: https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-package-list
[`packages.config` reference]: https://learn.microsoft.com/en-us/nuget/reference/packages-config
[Install a .NET tool]: https://learn.microsoft.com/en-us/dotnet/core/tools/dotnet-tool-install
[NuGet creation overview]: https://learn.microsoft.com/en-us/nuget/create-packages/overview-and-workflow
[.nuspec reference]: https://learn.microsoft.com/en-us/nuget/reference/nuspec
[multi-targeted package layout]: https://learn.microsoft.com/en-us/nuget/create-packages/supporting-multiple-target-frameworks
[symbol packages]: https://learn.microsoft.com/en-us/nuget/create-packages/symbol-packages-snupkg
