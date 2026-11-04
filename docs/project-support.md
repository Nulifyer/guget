# Work with project files and shared versions

Guget discovers `.csproj`, `.fsproj`, and `.vbproj` files beneath the selected
workspace. Common build-output directories such as `bin` and `obj` are excluded.
Choose a workspace that contains every project you intend to manage.

```bash
guget list --project ./src --format json
```

Inspection and editing have different limits. A displayed package can be
read-only, and its requested expression may differ from the version restored.

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

Guget supports CPM enabled through a literal, unconditional
`ManagePackageVersionsCentrally=true` setting. Use `show` to inspect the
reference and version locations:

```bash
guget show Newtonsoft.Json --project ./src --format json
```

`referenceOwner` identifies where the reference comes from. `versionOwner`
identifies where the version comes from. In CPM, those can be different files.

A version change in `Directory.Packages.props` affects other projects consuming
that central version, including projects outside your opened workspace. Review
the preview's paths and restore all affected projects. A single `--file` value
selects a project reference; it does not make its central version project-local.

When adding an already-defined central package, request the existing central
version and verify the result afterward. Removing a project reference leaves
the central `PackageVersion` in place.

## Imported props and inherited references

Packages may be declared in `Directory.Build.props` or imported `.props` files.
These shared files are distinct from the CPM version file.

Use `show` and an update dry run to inspect the shared file that would change.
If a reference is inherited, project-local removal may be refused. Use the
shared-file location in the TUI only when you intend to change its consumers.

After editing shared files outside the opened directory, press `Ctrl+R` to
reload the TUI. Prefer a workspace containing the full shared scope when making
bulk changes.

## Version expressions and pinned references

A requested expression can be a single version, a range such as `[1.0,2.0)`, or a
floating expression such as `1.*`. Keep it distinct from the restored version
shown in CLI results. Updating to an explicit version replaces the supported
reference's existing expression; inspect that change before applying it.

The TUI treats `[1.0.0]` as an exact pin and requires confirmation for selected
project updates. Bulk updates, including version-picker changes from All
Projects, skip pinned references.

For projects targeting different frameworks, inspect each framework's result
and restore each affected project after changing versions.

## Editing limits

| Package or project form | What to expect |
| --- | --- |
| SDK-style C#, F#, or VB project | Inspect references and edit supported literal versions. |
| CPM reference or `VersionOverride` | Inspect reference/version locations and review shared effects. |
| Imported props, conditions, properties, or duplicate declarations | Editing is limited and may be refused. Use explicit scope and review the preview. |
| Transitive package | Inspect a restored graph with `--include-transitive`; change the relevant direct dependency. |
| SDK-controlled implicit package | Treat it as SDK-controlled; avoid applying direct package edits to it. |
| `packages.lock.json` | Let restore manage it; Guget has no direct lock-file editor. |
| `packages.config` | Detected as unsupported; Guget does not edit it. |
| .NET tools | Manage through .NET tool commands; Guget's package commands target project references. |

If ownership cannot be established or a write is refused, inspect the message and
current project files. Do not assume a displayed editable flag proves that a
conditional or inherited reference can be changed as intended.
