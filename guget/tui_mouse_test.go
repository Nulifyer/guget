package main

import (
	"strings"
	"testing"

	bubbles_textinput "charm.land/bubbles/v2/textinput"
	bubbles_viewport "charm.land/bubbles/v2/viewport"
	bubble_tea "charm.land/bubbletea/v2"
)

func newMouseTestApp() *App {
	return &App{
		mouse: true,
		ctx: &AppContext{
			Width:           120,
			Height:          30,
			Results:         make(map[string]nugetResult),
			PendingPackages: NewSet[string](),
		},
		projects: projectPanel{
			items: []projectItem{{name: "All"}, {name: "One"}, {name: "Two"}},
		},
		packages: packagePanel{
			rows: []packageRow{
				{ref: PackageReference{Name: "A"}},
				{ref: PackageReference{Name: "B"}},
				{ref: PackageReference{Name: "C"}},
				{ref: PackageReference{Name: "D"}},
				{ref: PackageReference{Name: "E"}},
			},
		},
		detail: detailPanel{
			sectionBase: sectionBase{baseWidth: 50, minWidth: 10},
			vp:          bubbles_viewport.New(bubbles_viewport.WithWidth(40), bubbles_viewport.WithHeight(5)),
		},
		log: logPanel{
			vp: bubbles_viewport.New(bubbles_viewport.WithWidth(80), bubbles_viewport.WithHeight(5)),
		},
	}
}

func TestNewAppMouseDefaultsAndOptOut(t *testing.T) {
	snapshot := &workspaceSnapshot{}

	defaultApp := NewApp(".", snapshot, nil, BuiltFlags{})
	if !defaultApp.mouse {
		t.Fatal("mouse navigation is disabled by default")
	}

	optedOutApp := NewApp(".", snapshot, nil, BuiltFlags{NoMouse: true})
	if optedOutApp.mouse {
		t.Fatal("mouse navigation remained enabled with --no-mouse")
	}
}

func TestMouseLayoutMapsPanelsAndRows(t *testing.T) {
	layout := mouseLayout{
		projects:      mouseRect{x: 0, y: 0, w: 30, h: 20},
		packages:      mouseRect{x: 30, y: 0, w: 40, h: 20},
		detail:        mouseRect{x: 70, y: 0, w: 50, h: 20},
		log:           mouseRect{x: 0, y: 20, w: 120, h: 9},
		projectScroll: 2,
		packageScroll: 4,
	}

	for _, test := range []struct {
		x, y  int
		want  focusPanel
		found bool
	}{
		{x: 0, y: 0, want: focusProjects, found: true},
		{x: 30, y: 0, want: focusPackages, found: true},
		{x: 70, y: 0, want: focusDetail, found: true},
		{x: 119, y: 20, want: focusLog, found: true},
		{x: 120, y: 20, found: false},
	} {
		got, found := layout.panelAt(test.x, test.y)
		if found != test.found || found && got != test.want {
			t.Fatalf("panelAt(%d, %d) = (%v, %v), want (%v, %v)", test.x, test.y, got, found, test.want, test.found)
		}
	}

	if got, ok := layout.projectIndexAt(1, 6, 10); !ok || got != 3 {
		t.Fatalf("projectIndexAt() = (%d, %v), want (3, true)", got, ok)
	}
	if got, ok := layout.packageIndexAt(31, 5, 10); !ok || got != 6 {
		t.Fatalf("packageIndexAt() = (%d, %v), want (6, true)", got, ok)
	}
	if _, ok := layout.packageIndexAt(31, 2, 10); ok {
		t.Fatal("package header must not map to a package row")
	}
}

func TestBindMouseCapturesRenderedLayout(t *testing.T) {
	app := newMouseTestApp()
	wantLayout := mouseLayout{packages: mouseRect{x: 30, y: 0, w: 40, h: 20}, packageScroll: 2}
	view := bubble_tea.NewView("")
	app.bindMouse(&view, wantLayout)

	if view.MouseMode != bubble_tea.MouseModeCellMotion {
		t.Fatalf("MouseMode = %v, want MouseModeCellMotion", view.MouseMode)
	}
	if view.OnMouse == nil {
		t.Fatal("OnMouse was not installed")
	}

	cmd := view.OnMouse(bubble_tea.MouseClickMsg(bubble_tea.Mouse{X: 31, Y: 4, Button: bubble_tea.MouseLeft}))
	got, ok := cmd().(mouseInputMsg)
	if !ok {
		t.Fatalf("mouse command returned %T, want mouseInputMsg", cmd())
	}
	if got.layout.packageScroll != wantLayout.packageScroll || got.layout.packages != wantLayout.packages {
		t.Fatalf("captured layout = %+v, want %+v", got.layout, wantLayout)
	}
}

func TestBindMouseLeavesViewUnchangedWhenDisabled(t *testing.T) {
	app := newMouseTestApp()
	app.mouse = false
	view := bubble_tea.NewView("")
	app.bindMouse(&view, mouseLayout{})

	if view.MouseMode != bubble_tea.MouseModeNone {
		t.Fatalf("MouseMode = %v, want MouseModeNone", view.MouseMode)
	}
	if view.OnMouse != nil {
		t.Fatal("OnMouse was installed while mouse support was disabled")
	}
}

func TestMouseClickFocusesAndSelectsPackage(t *testing.T) {
	app := newMouseTestApp()
	layout := mouseLayout{packages: mouseRect{x: 30, y: 0, w: 40, h: 20}}
	app.handleMouse(mouseInputMsg{
		event:  bubble_tea.MouseClickMsg(bubble_tea.Mouse{X: 31, Y: 5, Button: bubble_tea.MouseLeft}),
		layout: layout,
	})

	if app.focus != focusPackages {
		t.Fatalf("focus = %v, want focusPackages", app.focus)
	}
	if app.packages.cursor != 2 {
		t.Fatalf("package cursor = %d, want 2", app.packages.cursor)
	}
}

func TestMouseWheelUsesPanelUnderPointerWithoutChangingFocus(t *testing.T) {
	app := newMouseTestApp()
	app.focus = focusProjects
	layout := mouseLayout{packages: mouseRect{x: 30, y: 0, w: 40, h: 20}}
	app.handleMouse(mouseInputMsg{
		event:  bubble_tea.MouseWheelMsg(bubble_tea.Mouse{X: 31, Y: 5, Button: bubble_tea.MouseWheelDown}),
		layout: layout,
	})

	if app.focus != focusProjects {
		t.Fatalf("focus = %v, want unchanged focusProjects", app.focus)
	}
	if app.packages.cursor != 3 {
		t.Fatalf("package cursor = %d, want 3", app.packages.cursor)
	}
}

func TestMouseWheelScrollsDetailViewport(t *testing.T) {
	app := newMouseTestApp()
	app.detail.vp.SetContent(strings.Repeat("line\n", 30))
	layout := mouseLayout{detail: mouseRect{x: 70, y: 0, w: 50, h: 20}}
	app.handleMouse(mouseInputMsg{
		event:  bubble_tea.MouseWheelMsg(bubble_tea.Mouse{X: 71, Y: 5, Button: bubble_tea.MouseWheelDown}),
		layout: layout,
	})

	if app.detail.vp.YOffset() == 0 {
		t.Fatal("detail viewport did not scroll")
	}
}

func TestModalMouseInputDoesNotReachMainPanels(t *testing.T) {
	app := newMouseTestApp()
	app.focus = focusProjects
	app.handleMouse(mouseInputMsg{
		event: bubble_tea.MouseClickMsg(bubble_tea.Mouse{X: 31, Y: 5, Button: bubble_tea.MouseLeft}),
		layout: mouseLayout{
			modal:    true,
			packages: mouseRect{x: 30, y: 0, w: 40, h: 20},
		},
	})

	if app.focus != focusProjects || app.packages.cursor != 0 {
		t.Fatalf("modal click changed main state: focus=%v package=%d", app.focus, app.packages.cursor)
	}
}

func TestModalMouseWheelRoutesToActiveOverlay(t *testing.T) {
	app := newMouseTestApp()
	app.picker = versionPicker{
		sectionBase: sectionBase{app: app, active: true},
		versions:    []PackageVersion{{}, {}},
	}
	app.handleMouse(mouseInputMsg{
		event: bubble_tea.MouseWheelMsg(bubble_tea.Mouse{X: 31, Y: 5, Button: bubble_tea.MouseWheelDown}),
		layout: mouseLayout{modal: true, regions: []mouseRegion{{
			rect:  mouseRect{x: 30, y: 0, w: 40, h: 20},
			wheel: verticalWheelHandler(app.picker.HandleKey),
		}}},
	})

	if app.picker.cursor != 1 {
		t.Fatalf("picker cursor = %d, want 1", app.picker.cursor)
	}
}

func TestLinkMouseRegionsOpenRenderedURL(t *testing.T) {
	old := hyperlinkEnabled
	hyperlinkEnabled = true
	t.Cleanup(func() { hyperlinkEnabled = old })

	regions := linkMouseRegions("x " + hyperlink("https://example.com/package", "package"))
	if len(regions) != 1 {
		t.Fatalf("link regions = %d, want 1", len(regions))
	}
	if regions[0].rect.x != 2 || regions[0].rect.w == 0 {
		t.Fatalf("link rect = %+v, want x=2 and non-zero width", regions[0].rect)
	}
	cmd := regions[0].click(bubble_tea.MouseClickMsg(bubble_tea.Mouse{Button: bubble_tea.MouseLeft}))
	msg, ok := cmd().(openURLRequestedMsg)
	if !ok || msg.url != "https://example.com/package" {
		t.Fatalf("link click = %#v, want openURLRequestedMsg", msg)
	}
}

func TestPackageModeHeaderCyclesAndArrowReverses(t *testing.T) {
	app := newMouseTestApp()
	app.packages.sortMode = sortByStatus
	app.packages.sortDir = sortByStatus.defaultDir()

	layout := app.mainMouseLayout(30, 60, 30)
	modeEvent := bubble_tea.MouseClickMsg(bubble_tea.Mouse{
		X: 34, Y: 1, Button: bubble_tea.MouseLeft,
	})
	app.handleMouse(mouseInputMsg{event: modeEvent, layout: layout})
	if app.packages.sortMode != sortByName || !app.packages.sortDir {
		t.Fatalf("first mode click = (%v, %v), want name ascending", app.packages.sortMode, app.packages.sortDir)
	}

	layout = app.mainMouseLayout(30, 60, 30)
	app.handleMouse(mouseInputMsg{event: modeEvent, layout: layout})
	if app.packages.sortMode != sortBySource || !app.packages.sortDir {
		t.Fatalf("second mode click = (%v, %v), want source ascending", app.packages.sortMode, app.packages.sortDir)
	}

	layout = app.mainMouseLayout(30, 60, 30)
	_, directionOffset := packageSortHeader(app.packages.sortMode, app.packages.sortDir)
	directionEvent := bubble_tea.MouseClickMsg(bubble_tea.Mouse{
		X: 30 + 2 + 2 + directionOffset, Y: 1, Button: bubble_tea.MouseLeft,
	})
	app.handleMouse(mouseInputMsg{event: directionEvent, layout: layout})
	if app.packages.sortMode != sortBySource || app.packages.sortDir {
		t.Fatalf("arrow click = (%v, %v), want source descending", app.packages.sortMode, app.packages.sortDir)
	}
}

func TestNamedPackageHeadersSortDirectlyAndReverse(t *testing.T) {
	app := newMouseTestApp()
	layout := app.mainMouseLayout(30, 60, 30)
	columns := app.packageColumns(60)
	requestedX := 30 + 2 + 2 + columns.nameW
	event := bubble_tea.MouseClickMsg(bubble_tea.Mouse{
		X:      requestedX,
		Y:      1,
		Button: bubble_tea.MouseLeft,
	})
	app.handleMouse(mouseInputMsg{event: event, layout: layout})
	if app.packages.sortMode != sortByCurrent || app.packages.sortDir {
		t.Fatalf("sort = (%v, %v), want current descending", app.packages.sortMode, app.packages.sortDir)
	}
	app.handleMouse(mouseInputMsg{event: event, layout: layout})
	if !app.packages.sortDir {
		t.Fatal("second requested-header click did not reverse the sort")
	}
}

func TestOverlayCloseRegionClosesSources(t *testing.T) {
	app := newMouseTestApp()
	app.sources = sourcesOverlay{sectionBase: sectionBase{
		app: app, baseWidth: 90, minWidth: 40, maxMargin: 4, active: true,
	}}
	_ = app.sources.Render()
	regions := app.sources.MouseRegions()
	if len(regions) == 0 {
		t.Fatal("sources overlay did not render a close region")
	}
	closeRegion := regions[0]
	app.handleMouse(mouseInputMsg{
		event: bubble_tea.MouseClickMsg(bubble_tea.Mouse{
			X: closeRegion.rect.x, Y: closeRegion.rect.y, Button: bubble_tea.MouseLeft,
		}),
		layout: mouseLayout{modal: true, regions: regions},
	})
	if app.sources.active {
		t.Fatal("close region did not close the sources overlay")
	}
}

func TestVersionPickerRowClickSelectsWithoutApplying(t *testing.T) {
	app := newMouseTestApp()
	app.picker = newVersionPicker(app, "Example", []PackageVersion{
		{SemVer: ParseSemVer("2.0.0")},
		{SemVer: ParseSemVer("1.0.0")},
	}, NewSet[TargetFramework](), nil, false)
	_ = app.picker.Render()
	regions := app.picker.MouseRegions()
	row := regions[2] // close, first row, second row
	cmd := app.handleMouse(mouseInputMsg{
		event:  bubble_tea.MouseClickMsg(bubble_tea.Mouse{X: row.rect.x, Y: row.rect.y, Button: bubble_tea.MouseLeft}),
		layout: mouseLayout{modal: true, regions: regions},
	})
	if cmd != nil {
		t.Fatal("version row click started an apply command")
	}
	if app.picker.cursor != 1 || !app.picker.active {
		t.Fatalf("picker state = cursor %d active %v, want cursor 1 active", app.picker.cursor, app.picker.active)
	}
}

func TestProjectPickerRowClickTogglesSelection(t *testing.T) {
	app := newMouseTestApp()
	app.projectPick = projectPicker{
		sectionBase: sectionBase{app: app, baseWidth: 80, minWidth: 60, maxMargin: 4, active: true},
		items: []projectPickItem{
			{project: &ParsedProject{FileName: "One.csproj"}},
			{project: &ParsedProject{FileName: "Two.csproj"}},
		},
	}
	_ = app.projectPick.Render()
	regions := app.projectPick.MouseRegions()
	row := regions[2]
	app.handleMouse(mouseInputMsg{
		event:  bubble_tea.MouseClickMsg(bubble_tea.Mouse{X: row.rect.x, Y: row.rect.y, Button: bubble_tea.MouseLeft}),
		layout: mouseLayout{modal: true, regions: regions},
	})
	if app.projectPick.cursor != 1 || !app.projectPick.items[1].selected {
		t.Fatalf("project click = cursor %d selected %v", app.projectPick.cursor, app.projectPick.items[1].selected)
	}
}

func TestSearchResultClickContinuesToVersionFetch(t *testing.T) {
	app := newMouseTestApp()
	app.search = packageSearch{
		sectionBase: sectionBase{app: app, baseWidth: 90, minWidth: 56, maxMargin: 4, active: true},
		input:       bubbles_textinput.New(),
		results:     []SearchResult{{ID: "Example", Version: "1.0.0"}},
		lastQuery:   "Example",
	}
	app.ctx.Results["Example"] = nugetResult{pkg: &PackageInfo{ID: "Example"}, source: "test"}
	_ = app.search.Render()
	regions := app.search.MouseRegions()
	row := regions[2] // close, input, first result
	cmd := app.handleMouse(mouseInputMsg{
		event:  bubble_tea.MouseClickMsg(bubble_tea.Mouse{X: row.rect.x, Y: row.rect.y, Button: bubble_tea.MouseLeft}),
		layout: mouseLayout{modal: true, regions: regions},
	})
	if cmd == nil {
		t.Fatal("search result click did not continue the add flow")
	}
	msg, ok := cmd().(packageFetchedMsg)
	if !ok || msg.info == nil || msg.info.ID != "Example" {
		t.Fatalf("search click returned %#v, want cached packageFetchedMsg", msg)
	}
}

func TestLocationPickerRequiresAddButton(t *testing.T) {
	app := newMouseTestApp()
	app.locationPick = locationPicker{
		sectionBase: sectionBase{app: app, baseWidth: 80, minWidth: 60, maxMargin: 4, active: true},
		pkgName:     "Example",
		version:     "1.0.0",
		targetProject: &ParsedProject{
			FilePath: "App.csproj",
		},
		targets: []AddTarget{
			{FilePath: "App.csproj", Kind: AddTargetProject},
			{FilePath: "Directory.Build.props", Kind: AddTargetBuildProps},
		},
	}
	_ = app.locationPick.Render()
	regions := app.locationPick.MouseRegions()
	secondRow := regions[2]
	cmd := app.handleMouse(mouseInputMsg{
		event:  bubble_tea.MouseClickMsg(bubble_tea.Mouse{X: secondRow.rect.x, Y: secondRow.rect.y, Button: bubble_tea.MouseLeft}),
		layout: mouseLayout{modal: true, regions: regions},
	})
	if cmd != nil || app.locationPick.cursor != 1 || !app.locationPick.active {
		t.Fatalf("row click = cmd %v cursor %d active %v", cmd != nil, app.locationPick.cursor, app.locationPick.active)
	}

	addButton := regions[4] // close, two rows, cancel, add
	cmd = app.handleMouse(mouseInputMsg{
		event:  bubble_tea.MouseClickMsg(bubble_tea.Mouse{X: addButton.rect.x, Y: addButton.rect.y, Button: bubble_tea.MouseLeft}),
		layout: mouseLayout{modal: true, regions: regions},
	})
	if cmd == nil || app.locationPick.active {
		t.Fatalf("add click = cmd %v active %v, want command and closed overlay", cmd != nil, app.locationPick.active)
	}
}

func TestConfirmationButtonRequiresExplicitClick(t *testing.T) {
	app := newMouseTestApp()
	packages := NewSet[PackageReference]()
	packages.Add(PackageReference{Name: "Example"})
	app.ctx.ParsedProjects = []*ParsedProject{{FilePath: "App.csproj", Packages: packages}}
	app.confirmRemove = newConfirmRemove(app, "Example")
	_ = app.confirmRemove.Render()
	regions := app.confirmRemove.MouseRegions()
	removeButton := regions[2] // close, cancel, remove
	cmd := app.handleMouse(mouseInputMsg{
		event:  bubble_tea.MouseClickMsg(bubble_tea.Mouse{X: removeButton.rect.x, Y: removeButton.rect.y, Button: bubble_tea.MouseLeft}),
		layout: mouseLayout{modal: true, regions: regions},
	})
	if cmd == nil || app.confirmRemove.active {
		t.Fatalf("remove click = cmd %v active %v, want command and closed overlay", cmd != nil, app.confirmRemove.active)
	}
}

func TestReleaseNotesWheelUsesPanelUnderPointer(t *testing.T) {
	app := newMouseTestApp()
	app.releaseNotes = newReleaseNotesOverlay(app, "Example release notes")
	app.releaseNotes.ghAvailable = true
	app.releaseNotes.ghReleases = []GitHubRelease{{TagName: "v2"}, {TagName: "v1"}}
	app.releaseNotes.ghNotes = strings.Repeat("line\n", 100)
	app.releaseNotes.updateViewportContent()
	_ = app.releaseNotes.Render()
	regions := app.releaseNotes.MouseRegions()

	var wheelRegions []mouseRegion
	for _, region := range regions {
		if region.wheel != nil {
			wheelRegions = append(wheelRegions, region)
		}
	}
	if len(wheelRegions) != 2 {
		t.Fatalf("release-note wheel regions = %d, want 2", len(wheelRegions))
	}
	left, right := wheelRegions[0], wheelRegions[1]
	if left.rect.x > right.rect.x {
		left, right = right, left
	}

	app.handleMouse(mouseInputMsg{
		event:  bubble_tea.MouseWheelMsg(bubble_tea.Mouse{X: left.rect.x, Y: left.rect.y, Button: bubble_tea.MouseWheelDown}),
		layout: mouseLayout{modal: true, regions: regions},
	})
	if app.releaseNotes.ghCursor != 1 {
		t.Fatalf("left wheel cursor = %d, want 1", app.releaseNotes.ghCursor)
	}

	app.releaseNotes.ghLoading = false
	app.releaseNotes.vp.SetContent(strings.Repeat("line\n", 100))
	app.handleMouse(mouseInputMsg{
		event:  bubble_tea.MouseWheelMsg(bubble_tea.Mouse{X: right.rect.x, Y: right.rect.y, Button: bubble_tea.MouseWheelDown}),
		layout: mouseLayout{modal: true, regions: regions},
	})
	if !app.releaseNotes.focusRight || app.releaseNotes.vp.YOffset() == 0 {
		t.Fatalf("right wheel = focusRight %v offset %d", app.releaseNotes.focusRight, app.releaseNotes.vp.YOffset())
	}
}
