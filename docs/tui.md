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

The picker marks framework compatibility, prereleases, and known vulnerabilities
when the source supplies that metadata. Compatibility indicators are guidance;
restore and your project tests validate the result.

For quick updates from the package panel:

| Key | Scope and version |
| --- | --- |
| `u` | Latest compatible version for the selected project. |
| `U` | Apply the selected package's compatible update across projects. |
| `a` | Latest stable version for the selected project. |
| `A` | Apply the selected package's latest stable version across projects. |

When All Projects is selected, lowercase quick-update keys also operate across
projects. Bulk updates, including version-picker changes from All Projects,
skip pinned references. A selected project's pinned reference can be changed
after confirmation.

Compatible quick updates consider every affected project's frameworks, including
projects that consume the same shared version. Latest stable and explicit picker
choices can require newer frameworks; restore and test each affected project.

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

## Remove a package

Select the project and package, then press `d`. Review the package name and
confirm Remove. Press `Esc` to cancel. Restore the project afterward.

Removing a reference does not automatically remove its shared central version.
For a reference inherited from props, Guget may refuse project-local removal.

## Restore and reload

| Key | Action |
| --- | --- |
| `r` | Restore the selected project; All Projects or a shared props selection restores all projects. |
| `R` | Restore all discovered projects. |
| `Ctrl+R` | Reload project declarations from disk. |

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

## Read package status

| Marker | Meaning |
| --- | --- |
| `.` | Metadata is loading. |
| `✗` | Metadata lookup failed. |
| `▲` | A known vulnerability is reported for an installed version. |
| `~` | The feed marks the package deprecated. |
| `↑` | A compatible update is available. |
| `⇑` | A newer stable version is available beyond the compatible choice. |
| `✓` | No newer version is indicated by the available metadata. |

Missing vulnerability metadata does not establish that a package is safe.
Review the package detail and your restore/audit results when assessing an update.
