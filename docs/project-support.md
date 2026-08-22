# Project and package support

Support snapshot: 2026-08-21

Guget separates inspection support from mutation support. A project can remain
useful in `list` even when an item is read-only.

| Shape | Inspect | Mutate | Notes |
| --- | --- | --- | --- |
| SDK-style C#, F#, or VB project | Yes | Yes | Literal project-owned references are supported. |
| `PackageReference Include` or `Update` | Yes | Yes | Attribute and child-element versions are recognized. |
| `VersionOverride` | Yes | Yes | Treated as a project-owned version. |
| Central Package Management | Yes | Yes | Requires a literal, unconditional `ManagePackageVersionsCentrally=true`; reference and version owners are separate. |
| `Directory.Build.props` or explicit `.props` import | Yes | Limited | Literal, unambiguous owners can update; inherited removal is refused without a local reference. |
| MSBuild property version expression | Yes | Read-only by default | Evaluated value is shown, but rewriting the property owner is not inferred. |
| Conditional references | Per evaluated TFM | Limited | Only XML shapes with a proven literal owner are editable. |
| Transitive or implicit package | Yes with restored graph | No | These are resolved results, not direct declarations. |
| NuGet range or floating expression | Yes | Exact replacement only | The raw request remains distinct from the resolved version. |
| `packages.lock.json` | Restore status only | No direct editing | Restore owns lock-file generation and validation. |
| `packages.config` | Detected as unsupported | No | Full editing is outside the current scope. |
| Local/UNC NuGet source | Restore provenance | No metadata browsing | Listed as `restore-only`. |
| HTTP(S) NuGet V3 source | Yes | Metadata only | Capabilities come from the service index. |

Mutation commands require explicit files or `--all`, refuse ambiguous ownership,
and preflight source hashes. Unsupported files remain byte-for-byte unchanged.
The TUI can inspect projects with only restore-only sources. Metadata search and
release lookup remain unavailable until an HTTP(S) V3 source is configured.
