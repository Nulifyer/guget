# Manage packages in the TUI

Open the directory containing your projects:

```bash
guget --project ./src
```

You can also run `guget` in that directory. Give the terminal at least 80 columns
and 12 rows. Press `?` for help, and use `Ctrl+C` to quit from any screen.

## Select the project you want to change

The left panel groups projects and shared props files by parent directory, keeping
their existing order within each directory. Select an individual
project, then press `Tab` or `Enter` to focus its packages. Use `Tab` and
`Shift+Tab` to move between panels.

**All Projects includes every discovered project.** With that row selected,
quick updates, version-picker Apply, and removal operate across projects
containing the selected package. For a single-project change, select that
project first. Editing a shared version can still affect other projects; see
[shared package versions](project-support.md#central-package-management).

Use arrow keys to navigate. `j` and `k` also navigate lists. The package detail
panel shows metadata, versions, and notices for the selected package.

## Update a package

1. Select a project and package.
2. Press `v` to open its version picker.
3. Select a version and press `Enter` or click Apply.
4. Confirm if the current version is pinned.
5. Press `r` to restore the selected project.

The picker marks framework compatibility, prereleases, known vulnerabilities
(`▲`), and deprecated versions (`~`) when the source supplies that metadata.
NuGet deprecates individual versions, so each one carries its own `~`. The
`~ deprecated` line under the package name appears when the newest stable
version is deprecated, and names the suggested replacement. Compatibility
indicators are guidance; restore and your project tests validate the result.

Compatibility follows NuGet's framework rules, judged from the frameworks the
source lists for each version. A .NET Framework-only package counts as
compatible with .NET, .NET Core 2.0+, and .NET Standard 2.0+ projects because
restore accepts it through AssetTargetFallback, with warning NU1701. Target
frameworks that come from MSBuild properties guget cannot evaluate are skipped.
Unlisted versions appear in the version list, but guget never chooses one as
the latest or default version.

For quick updates from the package panel:

| Key | Scope and version |
| --- | --- |
| `u` | The update target for the selected project. |
| `U` | Apply the selected package's update target across projects. |
| `a` | Latest stable version for the selected project, whatever its frameworks. |
| `A` | Apply the selected package's latest stable version across projects. |

The update target follows the [`update-target` setting](settings.md#update-target).
The default, `compatible-stable`, is the newest stable version every affected
framework can use. `a` and `A` always choose what `dotnet package update`
would, the `stable` setting. Under every setting, a reference whose requested
version is a prerelease, such as `11.0.0-preview.6`, is offered newer
prereleases. No quick update lowers a version: when nothing newer qualifies,
the status line says the package is already at that version.

To see which setting is active, press `?`: the Settings section of the help
names it and what it chooses. With All Projects selected, the footer also
labels `u/U` as `up compat`, `up compat+pre`, or `up stable`.

When All Projects is selected, lowercase quick-update keys also operate across
projects. Bulk updates, including version-picker changes from All Projects,
skip pinned references. A selected project's pinned reference can be changed
after confirmation.

Update targets consider every affected project's frameworks, including
projects that consume the same shared version. Latest stable and explicit picker
choices can require newer frameworks; restore and test each affected project.
The version picker opens on the update target.

The Available column shows the update target first. When the latest stable
version (the `a` choice) differs, it follows in parentheses:

| Setting | Available shows |
| --- | --- |
| `compatible-stable` | Newest compatible stable version, then the newest stable one if a framework cannot use it. |
| `compatible` | Newest compatible version, prereleases included, then the newest stable one if different. |
| `stable` | Newest stable version only. |

For example, `11.12.0 (12.2.0)` under `compatible-stable` means `u` goes to
11.12.0 and `a` goes to 12.2.0, which a target framework cannot use. When the
package is already at 11.12.0, its marker is `✓` and the detail panel adds
**Newer version not offered** to explain the parenthesized version.
`- (12.2.0)` means no version qualifies as the update target. In a narrow
column the parenthesized version shortens to `(...)`; select the row to read
it in the detail panel.

With All Projects selected, the update target never lowers any project's
version, and prereleases count when the most recent version in use was
requested as a prerelease.

An available version marked `[deps]` needs a dependency changed first. The
detail panel's **Update requirements** lists the affected projects, required
dependency versions, and their current requests. Update those dependencies,
then press `u` again. The marker uses known direct dependency metadata; restore
still validates the full dependency graph. Choosing a version with `v` or `a`
can override this check.

A requested version marked `pin` is an explicit exact-version pin such as
`[13.0.1]`. This is separate from a dependency requirement. All Projects shows
pins beside each affected project's version in the detail panel.

The saved package state refreshes before the next change becomes available.
You can continue navigating while saving, reloading, or fetching package information.

## Add a package

1. Select the destination project and press `/`.
2. Type a package name and wait for results.
3. Select a result and press `Enter`.
4. Choose a version and press `Enter` or click Add.
5. If prompted, choose the project file or shared props location.
6. Restore the affected project with `r`.

When starting from All Projects, a project picker lets you choose destinations.
Use `Space` to toggle a project and `Enter` to confirm. The picker does not offer
projects already using that version or projects marked incompatible.

Search follows the [NuGet sources](sources.md#how-guget-finds-configuration)
of the selected project. From All Projects it offers what any project's sources
have, so an add can still be refused for one destination: `▲ Not added:
App.csproj may not use source nuget.org for Serilog 4.0.0; see its NuGet
configuration`. Nothing is written; choose a version from a source that
project lists, or change its NuGet configuration.

## Remove a package

Select the project and package, then press `d`. Review the package name and
confirm Remove. Press `Esc` to cancel. Restore the project afterward.

Removing a reference does not automatically remove its shared central version.
For a reference inherited from props, Guget may refuse project-local removal.

When Guget refuses or fails to save a change, the status line starts with
`▲ Save failed`, `▲ Not added`, or `▲ Not updated` and gives the reason. If the
line is cut short, it says `(l for logs)`: press `l` to read the whole message,
which wraps in the log panel.

## Global, private-asset, and download-only packages

A package from a [`GlobalPackageReference`](project-support.md#global-package-references)
appears in every project that imports its file. The detail panel shows
**Global reference** for it, and All Projects marks each project's version
`global`. Updating it changes the shared file for all of those projects.
Removing it from one project is refused, because the reference is not that
project's own.

For a reference with `PrivateAssets`, the detail panel shows **Private assets**
and the value.

`PackageDownload` items are not shown. They are restore-only, exact-version
downloads rather than dependencies, and Guget does not edit them. List them
with [`guget list`](cli.md), where they have `download: true`.

## Restore and reload

| Key | Action |
| --- | --- |
| `r` | Restore the selected project; All Projects or a shared props selection restores all projects. |
| `R` | Restore all discovered projects. |
| `Ctrl+R` | Reload project declarations from disk. |

A restore also covers every project that your unrestored package edits affect:
projects that read an edited shared file such as `Directory.Packages.props`,
and projects that reference those. The status line shows how many ran, for
example `✓ Restored 3 projects (2 affected by package edits)`. A lock file in
locked mode that an edit invalidated is regenerated, and the status line counts
it. An edit stays pending until a restore that covers it succeeds, so pressing
`r` again after a failure retries the same projects.

Guget watches project files, loaded imports, and source configuration, including
loaded files outside the opened directory. Use `Ctrl+R` to request a fresh scan.
If a reload fails while a file is being saved, you can keep browsing the previous
view. Finish or repair the save, then reload successfully before making changes.
After a restore failure, open logs with `l` and inspect the message before retrying. See [restore troubleshooting](troubleshooting.md#restore-failed-after-an-edit).

## Browse dependencies and release notes

Select a package and press `t` for its declared dependency tree. To see a full
transitive graph, first select an individual project, then press `T`. This shows
the project's graph, rather than only the selected package's dependencies.
The transitive view uses the .NET SDK and may need a successful restore.

Press `n` for release notes. Use `1` for GitHub releases and `2` for NuSpec notes,
when available. `Tab` switches between the version list and note content. Use
arrow keys to navigate or scroll, and `Esc` to close.

Some feeds or packages do not provide release notes, dependency metadata, or
publication dates. Version ages use feed publication timestamps. When a mirrored
feed and nuget.org report different dates, an age labeled `nuget.org:` refers to
the public feed's date. That label does not establish that the private artifact
is identical to the public one. Missing or future dates are left blank.

## Use the mouse

Left-click a project or package row to select it. Click visible Add, Apply,
Update, Remove, or Cancel buttons to use them. Picker rows select or toggle their
items. Links open in the browser. The wheel navigates or scrolls the panel under
the pointer. Double-clicking is not a separate action.

Click Requested, Available, or Source in the package header to sort. Click the
bracketed sort mode to cycle modes, or its arrow to reverse direction. The
Requested and Available sorts use publication dates.

To leave mouse input to the terminal for text selection and native link handling:

```bash
guget --project ./src --no-mouse
```

Your terminal may also support a mouse-bypass modifier such as Shift.

## Change the view

| Key | Action |
| --- | --- |
| `o` / `O` | Cycle package sort mode or reverse its direction. |
| `[` / `]` | Resize the focused panel or overlay. |
| `l` | Show or hide logs. |
| `s` | Show configured sources. |
| `?` | Show keyboard help. |
| `Esc` / `q` | Quit the main screen or close most overlays; use `Esc` in search. |

Below 20 terminal rows, logs fill the area above the footer. Use the arrow keys
to scroll and press `l` to return to the package panels.

Start with a theme or sort preference:

```bash
guget --project ./src --theme dracula
guget --project ./src --sort-by available:desc
guget --project ./src --no-color
```

Available themes include `auto`, `auto-light`, `auto-dark`, `dracula`,
`catppuccin-mocha`, `catppuccin-macchiato`, `catppuccin-frappe`,
`catppuccin-latte`, `nord`, `tokyo-night`, `everforest`, and `gruvbox`.

The `auto` theme uses the terminal's `COLORFGBG` setting when available and
defaults to dark otherwise. Select `--theme auto-light` for a light terminal
that does not provide that setting.

To keep a theme, sort order, or other preference without passing flags, save
it in your [settings file](settings.md), for example with
`guget config set theme dracula`. The help overlay's Settings section shows
the active update target and the `guget config` and `GUGET_<KEY>` ways to
change a setting.

## Read package status

| Marker | Meaning |
| --- | --- |
| `.` | Metadata is loading. |
| `✗` | Metadata lookup failed, or the projects referencing the package use NuGet sources with no version of it in common. The detail panel says which: **No version for every project** lists those projects. |
| `▲` | A known vulnerability is reported for an installed version. |
| `~` | The feed marks an installed version deprecated. |
| `↑` | An update to the update target is available. |
| `⇑` | An update is available, and a newer stable version exists beyond it that some framework cannot use. |
| `✓` | No update is available under the update target. A newer version that a framework cannot use does not count; Available shows it in parentheses and the detail panel shows **Newer version not offered**. |

The help overlay (`?`) repeats these markers and the Available convention under
**Package list**.

Missing vulnerability metadata does not establish that a package is safe.
Review the package detail and your restore/audit results when assessing an update.
