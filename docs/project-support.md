# Work with project files and shared versions

Guget discovers `.csproj`, `.fsproj`, and `.vbproj` files beneath the selected
workspace. Common build-output directories such as `bin` and `obj` are excluded.
Choose a workspace that contains every project you intend to manage.

Inside a Git repository, Guget also skips paths that Git ignores: rules from
`.gitignore` files, `.git/info/exclude`, and your global excludes file. Files
Git tracks are still found, even under an ignored directory. To search ignored
directories too, pass `--include-ignored` to the TUI or to a command that
reads the workspace: `list`, `show`, `sources`, `search`, `add`, `update`,
`remove`, or `restore`. The built-in exclusions such as `bin` and `obj` still
apply.

```bash
guget --project ./src --include-ignored
```

```bash
guget list --project ./src --format json
```

Inspection and editing have different limits. A displayed package can be
read-only, and its requested expression may differ from the version restored.

## How Guget reads package declarations

Guget reads project files the way MSBuild evaluates them, without starting
MSBuild:

- `Directory.Build.props`, then `Directory.Packages.props`, then the project,
  then `Directory.Build.targets`. Only the nearest file of each name is
  imported automatically; a nested file must import its parent itself.
- The import switches and path overrides `ImportDirectoryBuildProps`,
  `DirectoryBuildPropsPath`, `CustomBeforeDirectoryBuildProps`,
  `CustomAfterDirectoryBuildProps`, `ImportDirectoryPackagesProps`,
  `DirectoryPackagesPropsPath`, `ImportDirectoryBuildTargets`, and
  `DirectoryBuildTargetsPath`, when they are set in `Directory.Build.props`
  or the environment.
- Properties in order, so a later value replaces an earlier one, and
  `$([MSBuild]::GetPathOfFileAbove(...))` imports with or without quotes. The
  one-argument form starts in the directory of the file that contains it.
- `PackageReference Update` changes an earlier reference only; on its own it
  declares nothing. `Remove` removes earlier items.
- When an ID is declared twice, restore uses the first declaration (NU1504,
  NU1506), and so does Guget.

When a condition, import, or switch depends on something Guget cannot see,
such as `$(Configuration)`, edits are checked with MSBuild evaluation first.

## Change a project-owned version

A straightforward project reference looks like this:

```xml
<ItemGroup>
  <PackageReference Include="Newtonsoft.Json" Version="13.0.3" />
</ItemGroup>
```

Preview an update for that project:

```bash
guget update Newtonsoft.Json --version 13.0.4 --project ./src --file App/App.csproj --dry-run --format json
```

Inspect the changed path, then apply with the same explicit workspace, package,
version, and project scope. See the [CLI guide](cli.md#preview-and-apply-an-update).

Attribute and child-element version forms are recognized. Literal
`VersionOverride` values can also represent a project-specific version.
Conditional or property-based declarations have more limited editing support;
review previews and changed files carefully.

## Central Package Management

CPM normally defines versions in `Directory.Packages.props`:

```xml
<Project>
  <PropertyGroup>
    <ManagePackageVersionsCentrally>true</ManagePackageVersionsCentrally>
  </PropertyGroup>
  <ItemGroup>
    <PackageVersion Include="Newtonsoft.Json" Version="13.0.4" />
  </ItemGroup>
</Project>
```

A consuming project declares the reference without a version:

```xml
<ItemGroup>
  <PackageReference Include="Newtonsoft.Json" />
</ItemGroup>
```

CPM is on when `ManagePackageVersionsCentrally` is `true` after the project
file is evaluated and a central file was imported. A value set in
`Directory.Packages.props` replaces one from `Directory.Build.props`, and the
project replaces both. A value set only in `Directory.Build.targets` comes too
late and does not turn CPM on. Use `show` to inspect the reference and version
locations:

```bash
guget show Newtonsoft.Json --project ./src --format json
```

`referenceOwner` identifies where the reference comes from. `versionOwner`
identifies where the version comes from. In CPM, those can be different files.

A version change in `Directory.Packages.props` affects other projects consuming
that central version, including projects outside your opened workspace. Review
the preview's paths and restore all affected projects. A single `--file` value
selects a project reference; it does not make its central version project-local.

### Nested central files

A nested `Directory.Packages.props` usually imports its parent:

```xml
<Project>
  <Import Project="$([MSBuild]::GetPathOfFileAbove(Directory.Packages.props, $(MSBuildThisFileDirectory)..))" />
  <ItemGroup>
    <PackageVersion Update="Newtonsoft.Json" Version="13.0.4" />
  </ItemGroup>
</Project>
```

Guget edits a version in the file that sets it: here the nested file, while
other packages keep their versions in the parent.

### Add a package under CPM

`guget add` writes a versionless `PackageReference` to the project. A new
`PackageVersion` goes to the central file the project imports
(`DirectoryPackagesPropsPath`).

When the package already has a central version, Guget never writes a second
`PackageVersion`; restore would ignore it. If you request that same version,
Guget adds only the reference. If you request a different version, Guget
refuses with exit 4. The message shows the command that changes the shared
version for every consumer, with the project's full path:

```bash
guget update Newtonsoft.Json --version 13.0.4 --file /path/to/src/App/App.csproj
```

`dotnet add package` behaves differently. When the central version comes from
an imported file, it refuses the add. Otherwise it changes the shared version
for every consumer without asking.

A floating central version such as `13.*` needs
`CentralPackageFloatingVersionsEnabled=true`; otherwise restore fails with
NU1011, and Guget refuses the edit.

### Version on a centrally managed reference

Under CPM, a `PackageReference` with `Version` fails restore with NU1008.
Guget marks it and refuses to update it. Replace `Version` with
`VersionOverride` for a project-specific version, or remove it to use the
central `PackageVersion`.

Guget also refuses updates that keep other restore errors: a reference
without a central version (NU1010), and a `VersionOverride` while
`CentralPackageVersionOverrideEnabled` is `false` (NU1013).

### Global package references

A `GlobalPackageReference` in `Directory.Packages.props` adds the package to
every project that imports that file, with `PrivateAssets="All"`. Restore
reads it only under CPM, and only when it is declared before NuGet's targets:
one in `Directory.Build.targets` has no effect.

Guget lists global packages with `global: true` and `privateAssets: "All"`.
Updating one edits its `Version` in that file. Removing one is a shared-file
edit: `guget remove` refuses it with exit 4, and the TUI removes it from All
Projects. Guget never adds a `PackageReference` or `PackageVersion` for an ID
that is already global.

### Removal compared with dotnet

`guget remove` deletes the `PackageReference` where it is declared and leaves
the central `PackageVersion` in place, as `dotnet remove package` does. Both
refuse a reference that the project inherits from an imported file. Guget
also refuses a conditional reference, or an ID with more than one
`PackageReference` in the same file, which `dotnet remove package` deletes
together. In the TUI, removal from All Projects can also edit an inherited
`.props` file.

## Imported props and inherited references

Packages may be declared in `Directory.Build.props` or imported `.props` files.
These shared files are distinct from the CPM version file.

Use `show` and an update dry run to inspect the shared file that would change.
`guget remove` refuses an inherited reference with exit 4, and so does `d` in
the TUI for a single project. Use the shared-file location in the TUI only when
you intend to change its consumers.

Declarations in `Directory.Build.targets` are listed, including
`PackageReference Update` items that change a project's version. They are
read-only: Guget edits project and `.props` files only.

After editing shared files outside the opened directory, press `Ctrl+R` to
reload the TUI. Prefer a workspace containing the full shared scope when making
bulk changes.

## Version expressions and pinned references

A requested expression can be a single version, a range such as `[1.0,2.0)`, or a
floating expression such as `1.*`. Keep it distinct from the restored version
shown in CLI results. Updating to an explicit version replaces the supported
reference's existing expression; inspect that change before applying it.

Guget compares versions the way NuGet does, so `1.0` and `1.0.0` are the same
version. For a floating or open-ended expression such as `1.*` or `(1.0,)`, the
TUI compares the version restore would pick from the package source.

Guget treats `[1.0.0]` as an exact pin. The TUI requires confirmation to update
a pin in a selected project. Bulk updates, including version-picker changes
from All Projects, skip pinned references. In the CLI, `update --all` skips
pins, and `update --file` refuses them with exit 4. Pass `--include-pinned` to
update them. The edit keeps the brackets. See [exact pins](cli.md#exact-pins).

For projects targeting different frameworks, inspect each framework's result
and restore each affected project after changing versions.

## Restore after an edit

`guget update --restore`, `add --restore`, and `remove --restore` restore more
than the projects you name. They also restore every project in the workspace
that reads a changed file, such as a shared `Directory.Packages.props`, and
every project that references one of those through `ProjectReference`,
directly or through other projects.
Guget prints each added project and the reason. In the TUI, `r` and `R`
include those projects until a restore succeeds.

Restore with a `packages.lock.json` in locked mode (`RestoreLockedMode`) fails
with NU1004 once an edit changes a dependency. For such a project, Guget runs
`dotnet restore --force-evaluate`, which regenerates the lock file. It also
does so when locked mode depends on a condition it cannot evaluate, such as
`$(ContinuousIntegrationBuild)`. Commit the regenerated lock files with the
edit. A restore without edits never adds `--force-evaluate`.

## Editing limits

| Package or project form | What to expect |
| --- | --- |
| SDK-style C#, F#, or VB project | Inspect references and edit supported literal versions. |
| CPM reference or `VersionOverride` | Inspect reference/version locations and review shared effects. |
| Imported props, conditions, properties, or duplicate declarations | Editing is limited and may be refused. Use explicit scope and review the preview. |
| Transitive package | Inspect a restored graph with `--include-transitive`; change the relevant direct dependency. |
| SDK-controlled implicit package | Treat it as SDK-controlled; avoid applying direct package edits to it. |
| `GlobalPackageReference` | Version edits go to its file; removal is a shared-file edit. |
| `PackageDownload` | Listed as read-only (`download: true`); it is restore-only and not a dependency. |
| `Directory.Build.targets` declarations | Listed; read-only. |
| `packages.lock.json` | Restore regenerates it after an edit, with `--force-evaluate` in locked mode; Guget has no direct lock-file editor. |
| `packages.config`, `packages.<ProjectName>.config` | Detected as unsupported; Guget does not edit them. |
| .NET tools | Manage through .NET tool commands; Guget's package commands target project references. |

If ownership cannot be established or a write is refused, inspect the message and
current project files. Do not assume a displayed editable flag proves that a
conditional or inherited reference can be changed as intended.
