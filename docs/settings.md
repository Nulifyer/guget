# Settings

Save your TUI preferences in a settings file so you do not have to pass the
same flags every time. You can also set them with environment variables, and a
command-line flag still overrides both for one run.

## Where the file lives

Guget reads the first location that applies:

1. The path in `GUGET_CONFIG`.
2. On Windows, `%APPDATA%\guget\config.toml`.
3. On Linux, macOS, and other Unix systems, `$XDG_CONFIG_HOME/guget/config.toml`
   when `XDG_CONFIG_HOME` is an absolute path, otherwise
   `~/.config/guget/config.toml`.

Print the location Guget uses:

```bash
guget config path
```

The file is optional. `guget config set` creates it with owner-only
permissions, along with its directory.

## File format

Each line sets one key. Quote text values and write `true` or `false` without
quotes. Lines starting with `#` and text after a value's `#` are comments.

```toml
# ~/.config/guget/config.toml
theme = "catppuccin-mocha"
sort = "available:desc"   # newest updates first
mouse = false
update-check = false
```

The file is valid TOML. Text values use TOML string syntax, so write `\"` for a
quote and `\\` for a backslash inside double quotes. Single-quoted strings take
their text literally. Guget does not support tables such as `[tui]`. It ignores
a table and every key after it.

A mistake in the file never stops Guget from starting. An unknown key, a value
that is not allowed, a repeated key, or a file that cannot be read produces a
warning in the TUI log panel and in `guget config show`, and Guget uses the
next source instead. When a key appears twice, the later line wins.

## Keys

| Key | Default | Values | Flag |
| --- | --- | --- | --- |
| `theme` | `"auto"` | `auto`, `auto-light`, `auto-dark`, `dracula`, `catppuccin-mocha`, `catppuccin-macchiato`, `catppuccin-frappe`, `catppuccin-latte`, `nord`, `tokyo-night`, `everforest`, `gruvbox` | `--theme` |
| `sort` | `"status:asc"` | `status`, `name`, `source`, `requested`, or `available`, optionally followed by `:asc` or `:desc`. `current` is accepted as an older name for `requested` | `--sort-by` |
| `mouse` | `true` | `true` or `false` | `--no-mouse` sets `false` |
| `update-check` | `true` | `true` or `false` | `--no-update-check` sets `false` |
| `verbosity` | `"warn"` | `none`, `error`, `warn`, `info`, `debug`, `trace` | `--verbose` |
| `color` | `true` | `true` or `false` | `--no-color` sets `false` |
| `update-target` | `"compatible-stable"` | `compatible-stable`, `compatible`, `stable` | `--update-target` on `list` and `update` |

`theme`, `sort`, `mouse`, `update-check`, `verbosity`, and `color` apply to the
TUI. The CLI commands such as `list` and `update` ignore them. `update-target`
applies to both; see [update target](#update-target).

## Update target

`update-target` decides which version Guget offers as a package's update: the
TUI's Available column, status marker, `u` and `U` keys, and version-picker
default, and the CLI's `list --outdated` and `update --latest`.

| Value | Update target |
| --- | --- |
| `compatible-stable` | The newest stable version that every target framework of the affected projects can use. |
| `compatible` | The newest version, prereleases included, that every target framework of the affected projects can use. |
| `stable` | The newest stable version, whatever frameworks it supports. This is what `dotnet package update` writes. |

Prereleases also count, under every value, when the reference's requested
version, or the lower bound of its range, is itself a prerelease. This is the
rule `dotnet package update` follows. A floating range such as `1.0.0-*` or
`*-*` has a prerelease lower bound too.

For example, with `10.6.0`, `10.7.0`, `11.0.0-preview.6`, and
`11.0.0-preview.7` on the feed:

| Requested | `compatible-stable` | `compatible` | `stable` |
| --- | --- | --- | --- |
| `11.0.0-preview.6` | `11.0.0-preview.7` | `11.0.0-preview.7` | `11.0.0-preview.7` |
| `10.6.0` | `10.7.0` | `11.0.0-preview.7` | `10.7.0` |

A version counts as compatible when restore can use it for each framework,
directly or through AssetTargetFallback, which restores with warning NU1701.
Guget judges this from the frameworks the source lists for each version; restore
remains the final check. Target frameworks Guget cannot evaluate, such as
`$(MyFrameworks)`, are skipped. "Affected projects" are all the projects whose
version one edit changes, for example every project using a shared
`Directory.Packages.props` entry.

Every value uses only listed versions. For each project, Guget merges the
versions from every source that the project's NuGet configuration and package
source mapping allow. See
[package sources](sources.md#how-guget-finds-configuration). An update never
lowers a version: when nothing newer qualifies, the package is up to date. Under `compatible-stable` and
`compatible`, a newer version that some framework cannot use is not an update.

Set it for good, or for one command:

```bash
guget config set update-target compatible
guget list --project ./src --outdated --update-target stable
```

## Environment variables

Every key has a variable named `GUGET_` followed by the key in upper case, with
`-` replaced by `_`:

```bash
GUGET_THEME=nord GUGET_MOUSE=false guget --project ./src
```

| Key | Variable | Older variable that still works |
| --- | --- | --- |
| `theme` | `GUGET_THEME` | |
| `sort` | `GUGET_SORT` | |
| `mouse` | `GUGET_MOUSE` | |
| `update-check` | `GUGET_UPDATE_CHECK` | `GUGET_NO_UPDATE_CHECK=1` sets `false` |
| `verbosity` | `GUGET_VERBOSITY` | `LOG_LEVEL` |
| `color` | `GUGET_COLOR` | `NO_COLOR` set to any non-empty value sets `false` |
| `update-target` | `GUGET_UPDATE_TARGET` | |

True or false variables also accept `1`, `0`, `yes`, `no`, `on`, and `off`. An
empty variable counts as unset. When both a `GUGET_` variable and its older
variable are set, the `GUGET_` variable wins.

## Precedence

From highest to lowest:

1. A command-line flag.
2. An environment variable.
3. The settings file.
4. The default.

For example, with `theme = "nord"` in the file and `GUGET_THEME=dracula` in the
environment, `guget` uses `dracula` and `guget --theme gruvbox` uses `gruvbox`.

The `--no-mouse`, `--no-update-check`, and `--no-color` flags can only turn a
feature off. To turn one back on for a single run, set its variable, such as
`GUGET_MOUSE=true`.

## Manage settings from the command line

```bash
guget config show                 # every key with its value and source
guget config get theme            # the effective value
guget config set theme nord       # validate and save
guget config unset theme          # remove the key from the file
```

`guget config show` reports the source of each value as `env`, `file`, or
`default`. It accepts the same `--format`, `--output`, and `--no-headers` as
the other CLI commands, including Go templates such as
`--format '{{.key}}={{.value}}'`; see [output formats](cli.md#choose-an-output-format).
`--output` refuses to replace the settings file itself. Problems with the file
print to standard error in every format, and JSON also lists them in a
`warnings` array.

```json
{
  "schemaVersion": 1,
  "command": "config",
  "status": "ok",
  "error": null,
  "path": "/home/me/.config/guget/config.toml",
  "settings": [
    {
      "key": "theme",
      "value": "nord",
      "source": "file",
      "default": "auto",
      "description": "TUI color theme (--theme)"
    }
  ],
  "warnings": []
}
```

The real output lists every key; this example shows one. For `mouse`,
`update-check`, and `color`, `value` and `default` are JSON `true` or `false`
rather than strings.

`guget config set` checks the value before it writes and exits with status 1
for an unknown key or a value that is not allowed. It exits with status 5 when
the file cannot be written. It changes only the line for that key, so your
comments and the rest of the file stay as they are. If an environment
variable overrides the key you just set, the command prints a
warning. `guget config unset` removes the key's lines and leaves the rest of
the file alone.

## VS Code extension

The extension passes `--verbose`, `--theme`, and `--sort-by` only for the
`guget.verbosity`, `guget.theme`, `guget.sortField`, and `guget.sortDirection`
settings you set in VS Code. Leave them unset to use your Guget settings file.
