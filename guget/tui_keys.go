package main

// Main key aliases, help labels, and descriptions share one definition. Overlay
// keys remain beside their modal handlers because their actions depend on mode.
type mainKeyBinding struct {
	key         string
	aliases     []string
	label       string
	description string
	section     string
}

type keyHelpSection struct {
	title string
	rows  [][2]string
}

var mainKeyBindings = []mainKeyBinding{
	{"tab", nil, "tab", "focus next panel", "Navigation"},
	{"shift+tab", nil, "shift+tab", "focus previous panel", "Navigation"},
	{"up", []string{"k"}, "↑ / k", "move up in list", "Navigation"},
	{"down", []string{"j"}, "↓ / j", "move down in list", "Navigation"},
	{"enter", nil, "enter", "switch focus to packages panel", "Navigation"},
	{"u", nil, "u", "update to the update target; keep row (selected project, or all from All Projects)", "Package actions  (packages panel)"},
	{"U", nil, "U", "update selected package to the update target (all projects)", "Package actions  (packages panel)"},
	{"a", nil, "a", "update to latest stable, whatever the frameworks; keep row (selected project, or all from All Projects)", "Package actions  (packages panel)"},
	{"A", nil, "A", "update selected package to latest stable, whatever the frameworks (all projects)", "Package actions  (packages panel)"},
	// A help row only: holding is detected from the timing of the four keys above.
	{"hold update key", nil, "hold u U a A", "update the whole list: each repeat updates the next package the key can update and skips the rest", "Package actions  (packages panel)"},
	{"v", nil, "v", "pick a specific version from the list", "Package actions  (packages panel)"},
	{"d", nil, "d", "remove selected reference (every project from All Projects; a props row must hold it)", "Package actions  (packages panel)"},
	{"t", nil, "t", "show declared dependency tree for package", "Package actions  (packages panel)"},
	{"n", nil, "n", "view release notes (GitHub or NuGet)", "Package actions  (packages panel)"},
	{"o", nil, "o", "cycle sort order", "Package actions  (packages panel)"},
	{"O", nil, "O", "change sort direction", "Package actions  (packages panel)"},
	{"ctrl+r", nil, "ctrl+r", "reload projects from disk", "Project actions"},
	{"r", nil, "r", "run dotnet restore (selected project, or all from All Projects or a props file; plus projects that unrestored edits affect)", "Project actions"},
	{"R", nil, "R", "run dotnet restore (all projects)", "Project actions"},
	{"T", nil, "T", "show full transitive dependency tree", "Project actions"},
	{"/", nil, "/", "search NuGet and add a package", "Project actions"},
	{"[", nil, "[", "shrink focused panel", "View toggles"},
	{"]", nil, "]", "grow focused panel", "View toggles"},
	{"l", nil, "l", "toggle log panel", "View toggles"},
	{"s", nil, "s", "toggle sources panel", "View toggles"},
	{"?", nil, "?", "toggle this help", "View toggles"},
	{"ctrl+x", nil, "ctrl+x", "dismiss Guget update notice for this release", "View toggles"},
	{"ctrl+c", []string{"esc", "q"}, "esc / q / ctrl+c", "quit; ctrl+c also quits from overlays", "View toggles"},
}

func mainKey(key string) string {
	for _, binding := range mainKeyBindings {
		if key == binding.key {
			return binding.key
		}
		for _, alias := range binding.aliases {
			if key == alias {
				return binding.key
			}
		}
	}
	return key
}

func mainKeyHelpSections() []keyHelpSection {
	var sections []keyHelpSection
	for _, binding := range mainKeyBindings {
		if len(sections) == 0 || sections[len(sections)-1].title != binding.section {
			sections = append(sections, keyHelpSection{title: binding.section})
		}
		section := &sections[len(sections)-1]
		section.rows = append(section.rows, [2]string{binding.label, binding.description})
	}
	return sections
}
