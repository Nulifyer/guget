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

`<clear />` excludes inherited package-source entries. Without it, sources from
parent directories and user or machine configuration may also appear.
Use `sources` to check the effective Guget view after changes.

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

Replace the example URL with your feed's URL. Configure its credentials using
your organization's NuGet setup. Guget supports credentials stored with sources
in NuGet configuration and discovered credential providers. Keep passwords and
tokens out of committed configuration and command output you share.

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
or `*`. If mapping is configured and nothing matches a package, Guget has no
eligible metadata source for it. Correct the mapping or feed availability rather
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

## Refresh source information

Press `Ctrl+R` in the TUI after changing source configuration, especially files
outside the opened directory. CLI commands reread configuration on each run.

See [project support](project-support.md) for shared configuration and
[troubleshooting](troubleshooting.md) for missing sources or metadata.
