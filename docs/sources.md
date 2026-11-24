# Use public and private package sources

Inspect the sources Guget sees for your workspace:

```bash
guget sources --project ./src --format json
```

In the TUI, press `s` to open the sources panel. Source names are the names used
by `search --source`.

## Configure a public feed

Put a `NuGet.Config` in the workspace directory:

```xml
<?xml version="1.0" encoding="utf-8"?>
<configuration>
  <packageSources>
    <clear />
    <add key="nuget.org" value="https://api.nuget.org/v3/index.json" />
  </packageSources>
</configuration>
```

`<clear />` excludes package sources inherited from parent directories and from
user or machine configuration. It only affects `<packageSources>`. Credentials,
disabled sources, and Package Source Mapping from those files still apply, as
they do for `dotnet restore`. Use `sources` to check the effective Guget view
after changes.

Search that feed by its configured name:

```bash
guget search Newtonsoft.Json --project ./src --source nuget.org --format json --timeout 30s
```

HTTP(S) feeds need NuGet V3 metadata endpoints for browsing and search. Some feeds
support restore but omit optional metadata such as release notes or vulnerability
information.

## Use a private feed

Add the feed's NuGet V3 service-index URL using the name you want to select:

```xml
<configuration>
  <packageSources>
    <clear />
    <add key="company" value="https://packages.example.com/nuget/v3/index.json" />
    <add key="nuget.org" value="https://api.nuget.org/v3/index.json" />
  </packageSources>
</configuration>
```

Replace the example URL with your feed's URL. Guget reads the same credentials
as `dotnet restore`, in this order:

1. An environment variable named `NuGetPackageSourceCredentials_<source name>`
   with the value `Username=<user>;Password=<token>`. The name must match the
   source key exactly, including case on Linux.
2. A `<packageSourceCredentials>` entry for the source in any NuGet
   configuration file, including your user configuration.
3. Credential providers, after the feed answers 401 or 403.

Keep tokens out of committed files by referencing environment variables from
the configuration. NuGet expands `%NAME%` in every value on all platforms.
`$NAME` is not expanded, and an undefined `%NAME%` stays as written:

```xml
<packageSourceCredentials>
  <company>
    <add key="Username" value="%FEED_USER%" />
    <add key="ClearTextPassword" value="%FEED_TOKEN%" />
  </company>
</packageSourceCredentials>
```

An entry needs both a username and a password, or restore rejects the whole
file. Guget does the same and reports the file. Encrypted `Password` values
work only on Windows. Keep passwords and tokens out of command output you share.

Check metadata access separately from restore:

```bash
guget search Contoso.Core --project ./src --source company --format json --timeout 30s
guget restore --project ./src --file App/App.csproj --interactive
```

The interactive restore command can prompt for authentication. Successful restore
does not establish that Guget's metadata lookup can access the feed. For 401/403
errors, see [authentication troubleshooting](troubleshooting.md#private-feed-authentication-fails).

## Restrict packages to sources

Package Source Mapping associates package ID patterns with configured source
names. For example, send `Contoso.*` to a company feed and other packages to
nuget.org:

```xml
<configuration>
  <packageSources>
    <clear />
    <add key="company" value="https://packages.example.com/nuget/v3/index.json" />
    <add key="nuget.org" value="https://api.nuget.org/v3/index.json" />
  </packageSources>
  <packageSourceMapping>
    <packageSource key="company">
      <package pattern="Contoso.*" />
    </packageSource>
    <packageSource key="nuget.org">
      <package pattern="*" />
    </packageSource>
  </packageSourceMapping>
</configuration>
```

An exact ID wins over a prefix, and a longer prefix wins over a shorter pattern
or `*`. A `*` ends a prefix wherever it appears, so `Contoso*` and `Contoso.*`
are both prefixes. A mapping entry for a source in a closer configuration file
replaces the entry for that source in files further away. If mapping is
configured and nothing matches a package, Guget has no eligible metadata source
for it. Correct the mapping or feed availability rather
than expecting fallback to an unrelated source.

Inspect `patterns` in `guget sources --format json` output for the workspace.
Public-feed enrichment is available only when the public source is configured
and eligible for the package. A missing vulnerability notice does not establish
complete audit coverage.

## Use a local or UNC feed

A directory feed can coexist with a metadata feed:

```xml
<configuration>
  <packageSources>
    <clear />
    <add key="local" value="./packages" />
    <add key="nuget.org" value="https://api.nuget.org/v3/index.json" />
  </packageSources>
</configuration>
```

Relative directory paths resolve from the configuration file. Local directories
and UNC shares appear as `restore-only`. They can supply restore packages but
cannot supply Guget's search or version metadata. This is expected for a local
feed, even when restore succeeds.

A package that only local feeds supply has no version metadata, so Guget cannot
find its update target. `list --outdated` warns about it and exits 7, or exits 3
when no package in the listing has metadata. `update --latest` exits 3. Use
`update --version` to write a version that only a local feed has.

## How Guget finds configuration

Guget resolves sources for each project the way `dotnet restore` does, so the
sources list can differ from `dotnet nuget list source` run elsewhere:

- It searches from the project's directory up to the filesystem root and reads
  one file per directory: the first of `nuget.config`, `NuGet.config`, and
  `NuGet.Config`. A `.nuget/NuGet.Config` folder file is not read.
- It then reads the user configuration (`~/.nuget/NuGet/NuGet.Config`, or
  `%APPDATA%\NuGet\NuGet.Config` on Windows, with `DOTNET_CLI_HOME` replacing
  the home directory), additional `*.config` files in the `config` folder next
  to it, and machine-wide files in `NuGet/Config`.
- If the user configuration does not exist, nuget.org is included, because
  `dotnet` creates that file with nuget.org on first use.
- For a source name defined in several files, the file closest to the project
  wins.
- `RestoreSources` in the project or `Directory.Build.props` replaces the
  configured sources, and `RestoreAdditionalProjectSources` adds to them. Guget
  reads these properties without running MSBuild, so it skips conditional
  property groups and entries that use other MSBuild properties.

When projects in the workspace resolve different sources, the sources list
shows all of them, with each source's mapped patterns from every project.
Versions and updates still come from each project's own configuration, as
`dotnet restore` resolves each project alone: a project only gets a package from
a source its own configuration lists and, under its own mapping, maps the
package to. `list` and `update --latest` assess each project separately. The
TUI shows a package's versions only when every project referencing it can
use them, offers search results from the selected project's sources (from any
project's sources in All Projects), and refuses an add when the version comes
from a source the target project cannot use. A version written to a shared
file such as `Directory.Packages.props` must be available from the sources of
every project that takes the package's version from it; see
[update](cli.md#preview-and-apply-an-update).

## Refresh source information

Press `Ctrl+R` in the TUI after changing source configuration, especially files
outside the opened directory. CLI commands reread configuration on each run.

See [project support](project-support.md) for shared configuration and
[troubleshooting](troubleshooting.md) for missing sources or metadata.
