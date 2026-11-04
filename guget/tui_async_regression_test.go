package main

import (
	"fmt"
	ui "github.com/nulifyer/guget/internal/ui"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegressionSearchPaste(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.openSearch()
	m.HandleEvent(ui.PasteEvent{Text: "Newtonsoft.Json"})
	if m.search.input.Value() != "Newtonsoft.Json" {
		t.Fatalf("pasted query was dropped: value=%q", m.search.input.Value())
	}
}

func TestRegressionBackgroundMetadataPreservesDetailScroll(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.ctx.Results["A"].pkg.Description = strings.Repeat("long description words ", 200)
	m.relayout()
	m.refreshDetail()
	m.detail.vp.SetYOffset(10)
	if m.detail.vp.YOffset() != 10 {
		t.Fatal("bad fixture")
	}
	cmd := m.HandleEvent(packageReadyMsg{name: "B", result: m.ctx.Results["B"]})
	executeActionCmd(t, m, cmd)
	if m.detail.vp.YOffset() != 10 {
		t.Fatalf("unrelated B metadata reset A detail scroll to %d", m.detail.vp.YOffset())
	}
}

func TestRegressionDependencyOverlayWidthResize(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.depTree = newDepTreeOverlay(m, "test", false)
	old := m.depTree.vp.Width()
	m.HandleEvent(ui.KeyPress(ui.Key{Code: '['}))
	w, _ := m.depTreeOverlaySize()
	if m.depTree.vp.Width() != w-6 {
		t.Fatalf("resized overlay width %d but viewport remains %d (old %d)", w, m.depTree.vp.Width(), old)
	}
}

func TestRegressionCanceledMouseRemove(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.confirmRemove = newConfirmRemove(m, "A")
	view := m.Render()
	regions := m.confirmRemove.MouseRegions()
	button := regions[2]
	event := ui.MouseClick(ui.Mouse{X: button.rect.x, Y: button.rect.y, Button: ui.MouseLeft})
	m.HandleEvent(ui.KeyPress(ui.Key{Code: ui.KeyEscape}))
	if m.confirmRemove.active {
		t.Fatal("cancel did not close overlay")
	}
	cmd := view.OnMouse(event)
	if cmd != nil || m.ctx.Saving {
		t.Fatalf("canceled confirmation accepted delayed mouse input: saving=%v", m.ctx.Saving)
	}
}

func TestRegressionReopenedProjectPickerRejectsOldMouseRow(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.projectPick = projectPicker{sectionBase: sectionBase{app: m, baseWidth: 80, minWidth: 60, maxMargin: 4, active: true, requestID: m.nextRequestID()}, items: []projectPickItem{{project: &ParsedProject{FileName: "One.csproj"}}, {project: &ParsedProject{FileName: "Two.csproj"}}}}
	view := m.Render()
	row := m.projectPick.MouseRegions()[2]
	event := ui.MouseClick(ui.Mouse{X: row.rect.x, Y: row.rect.y, Button: ui.MouseLeft})
	m.HandleEvent(ui.KeyPress(ui.Key{Code: ui.KeyEscape}))
	m.projectPick = projectPicker{sectionBase: sectionBase{app: m, baseWidth: 80, minWidth: 60, maxMargin: 4, active: true, requestID: m.nextRequestID()}, items: []projectPickItem{{project: &ParsedProject{FileName: "Other.csproj"}}}}
	defer func() {
		if err := recover(); err != nil {
			t.Errorf("old mouse row panicked in reopened picker: %v", err)
		}
	}()
	view.OnMouse(event)
	if m.projectPick.cursor != 0 || m.projectPick.items[0].selected {
		t.Error("old mouse row changed reopened picker")
	}
}

func TestRegressionNuSpecReadsCompleteHTTPBody(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<package><metadata>")
		w.(http.Flusher).Flush()
		<-release
		fmt.Fprint(w, "<releaseNotes>Actual release notes</releaseNotes></metadata></package>")
	}))
	defer server.Close()
	defer unblock()
	svc := &NugetService{client: server.Client(), flatBase: server.URL}
	done := make(chan string, 1)
	go func() { done <- svc.FetchNuspec("Example", "1.0.0") }()
	select {
	case body := <-done:
		if !strings.Contains(body, "Actual release notes") {
			t.Fatalf("fetch returned before complete body: %q", body)
		}
	case <-time.After(50 * time.Millisecond):
		unblock()
		select {
		case body := <-done:
			if !strings.Contains(body, "Actual release notes") {
				t.Fatalf("incomplete body: %q", body)
			}
		case <-time.After(time.Second):
			t.Fatal("fetch did not complete")
		}
	}
}

func TestRegressionClosedNuSpecOverlayCancelsHTTP(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	m, _, _ := newActionTestApp(t, "")
	m.releaseNotes = newReleaseNotesOverlay(m, "Example")
	m.releaseNotes.activeTab = tabNuSpec
	m.releaseNotes.nsSvc = &NugetService{client: server.Client(), flatBase: server.URL}
	m.releaseNotes.nsPkgID = "Example"
	m.releaseNotes.nsVersions = []string{"2.0.0", "1.0.0"}
	cmd := m.releaseNotes.selectCursor(1)
	done := make(chan ui.Event, 1)
	go func() { done <- cmd() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("fetch did not start")
	}
	m.releaseNotes.HandleKey(ui.KeyPress(ui.Key{Code: ui.KeyEscape}))
	select {
	case <-canceled:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("closed overlay left HTTP request running")
	}
}

func TestRegressionSearchFetchHonorsSelectedResultSource(t *testing.T) {
	makeSource := func(source, version string) (*NugetService, *httptest.Server) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"items":[{"items":[{"catalogEntry":{"id":"Example","version":%q,"projectUrl":"https://example.invalid/"}}]}]}`, version)
		}))
		return &NugetService{client: server.Client(), regBase: server.URL + "/", sourceName: source}, server
	}
	first, firstServer := makeSource("first", "1.0.0")
	defer firstServer.Close()
	selected, selectedServer := makeSource("selected", "2.0.0")
	defer selectedServer.Close()
	m, _, _ := newActionTestApp(t, "")
	m.ctx.NugetServices = []*NugetService{first, selected}
	m.openSearch()
	m.search.results = []SearchResult{{ID: "Example", Version: "2.0.0", Source: "selected"}}
	m.ctx.Results["Example"] = nugetResult{pkg: &PackageInfo{ID: "Example", LatestVersion: "9.0.0"}, source: "first"}
	cmd := m.search.HandleKey(ui.KeyPress(ui.Key{Code: ui.KeyEnter}))
	msg, ok := cmd().(packageFetchedMsg)
	if !ok || msg.err != nil || msg.info == nil {
		t.Fatalf("bad fixture result: %#v", msg)
	}
	if msg.source != "selected" || msg.info.LatestVersion != "2.0.0" {
		t.Fatalf("selected result source=selected version=2.0.0 but fetched source=%s version=%s", msg.source, msg.info.LatestVersion)
	}
}

func TestRegressionAllProjectsCompatibleUpdateRespectsEveryFramework(t *testing.T) {
	m, _, firstPath := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`)
	secondPath := filepath.Join(filepath.Dir(firstPath), "Older.csproj")
	mustWriteFile(t, secondPath, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`)
	second, err := ParseCsproj(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	m.ctx.ParsedProjects = append(m.ctx.ParsedProjects, second)
	m.projects.cursor = 0
	m.rebuildPackageRows()
	cmd := m.updatePackage(false, scopeSelected)
	if cmd == nil {
		return
	}
	executeActionCmd(t, m, cmd)
	updated, err := ParseCsproj(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	for ref := range updated.Packages {
		for _, version := range m.ctx.Results["A"].pkg.Versions {
			if version.SemVer.String() == ref.Version.String() && !versionCompatible(version, updated.TargetFrameworks) {
				t.Fatalf("compatible update wrote A %s supporting %v to net8.0 project", ref.Version.String(), version.Frameworks)
			}
		}
	}
}

func TestRegressionAllProjectsShowsVulnerableMiddleVersion(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.ctx.ParsedProjects = nil
	for _, version := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		refs := NewSet[PackageReference]()
		refs.Add(PackageReference{Name: "A", Version: ParseSemVer(version)})
		m.ctx.ParsedProjects = append(m.ctx.ParsedProjects, &ParsedProject{Packages: refs})
	}
	m.ctx.Results["A"].pkg.Versions[1].Vulnerabilities = []PackageVulnerability{{AdvisoryURL: "https://example.invalid/security", Severity: 2}}
	m.projects.cursor = 0
	m.rebuildPackageRows()
	if len(m.packages.rows) != 1 || !m.packages.rows[0].vulnerable {
		t.Fatal("A 2.0.0 is vulnerable in a project but aggregate warning is missing")
	}
	if detail := m.renderDetailVulnerabilities(m.packages.rows[0]); !strings.Contains(detail, "2.0.0") || !strings.Contains(detail, "security") {
		t.Fatalf("intermediate installed advisory missing from detail: %q", detail)
	}
}

func TestMouseCommandFromBeforeResizeIsIgnored(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.confirmRemove = newConfirmRemove(m, "A")
	view := m.Render()
	button := m.confirmRemove.MouseRegions()[2]
	event := ui.MouseClick(ui.Mouse{X: button.rect.x, Y: button.rect.y, Button: ui.MouseLeft})
	m.HandleEvent(ui.ResizeEvent{Width: 100, Height: 20})
	cmd := view.OnMouse(event)
	if cmd != nil || m.ctx.Saving || !m.confirmRemove.active {
		t.Fatal("pre-resize click accepted")
	}
}

func TestCompatibleUpdateScopeIncludesEveryAffectedFramework(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint("shared=", shared), func(t *testing.T) {
			m, messages, first := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net9.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`)
			second := filepath.Join(filepath.Dir(first), "Older.csproj")
			mustWriteFile(t, second, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`)
			older, err := ParseCsproj(second)
			if err != nil {
				t.Fatal(err)
			}
			m.ctx.ParsedProjects = append(m.ctx.ParsedProjects, older)
			scope := scopeAll
			if shared {
				owner := filepath.Join(filepath.Dir(first), "Directory.Packages.props")
				mustWriteFile(t, owner, `<Project><ItemGroup><PackageVersion Include="A" Version="1.0.0"/></ItemGroup></Project>`)
				// The scope calculation must also cover central consumers outside the selected project.
				for _, p := range m.ctx.ParsedProjects {
					p.PackageSources = map[string]string{"a": owner}
				}
				scope = scopeSelected
			}
			targets := m.packageTargets("A", scope)
			if !targets.Contains(ParseTargetFramework("net8.0")) || !targets.Contains(ParseTargetFramework("net9.0")) {
				t.Fatalf("incomplete framework scope: %v", targets)
			}
			m.rebuildPackageRows()
			if shared {
				if len(m.packages.rows) != 1 || m.packages.rows[0].latestCompatible == nil || m.packages.rows[0].latestCompatible.SemVer.String() != "2.0.0" {
					t.Fatal("selected view omitted another shared owner's framework")
				}
				return // Actual central ownership writes are tested in package_mutations_test.go.
			}
			executeActionCmd(t, m, m.updatePackage(false, scope))
			finishActionReload(t, m, messages)
			for _, path := range []string{first, second} {
				p, err := ParseCsproj(path)
				if err != nil {
					t.Fatal(err)
				}
				for ref := range p.Packages {
					if ref.Name == "A" && ref.Version.String() != "2.0.0" {
						t.Fatalf("compatible update wrote %s to %s", ref.Version.String(), path)
					}
				}
			}
		})
	}
}

func TestDelayedSearchMouseRowKeepsResultIdentity(t *testing.T) {
	first := SearchResult{ID: "Candidate.One", Source: "local", Version: "1.0.0"}
	second := SearchResult{ID: "Candidate.Two", Source: "local", Version: "1.0.0"}
	for _, test := range []struct {
		name         string
		results      []SearchResult
		changeQuery  bool
		wantSelected bool
	}{
		{name: "shorter results", results: []SearchResult{first}},
		{name: "changed query", results: []SearchResult{second}, changeQuery: true},
		{name: "reordered results", results: []SearchResult{second, first}, wantSelected: true},
		{name: "changed source", results: []SearchResult{first, {ID: second.ID, Source: "other", Version: second.Version}}},
		{name: "changed version", results: []SearchResult{first, {ID: second.ID, Source: second.Source, Version: "2.0.0"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, _, _ := newActionTestApp(t, "")
			m.openSearch()
			m.search.input.SetValue("Candidate")
			m.search.lastQuery = "Candidate"
			m.search.results = []SearchResult{first, second}
			m.ctx.Results[second.ID] = nugetResult{source: second.Source, pkg: &PackageInfo{ID: second.ID}}

			view := m.Render()
			row := m.search.MouseRegions()[3] // Close, input, first result, second result.
			event := ui.MouseClick(ui.Mouse{X: row.rect.x, Y: row.rect.y, Button: ui.MouseLeft})
			if test.changeQuery {
				m.HandleEvent(ui.PasteEvent{Text: "Extra"})
			}
			m.HandleEvent(searchResultsMsg{
				requestID: m.search.requestID, id: m.search.debounceID,
				query: m.search.lastQuery, results: test.results,
			})
			cmd := view.OnMouse(event)
			if !test.wantSelected {
				if cmd != nil || m.search.fetchingVersion {
					t.Fatal("delayed mouse row selected a removed or changed search result")
				}
				return
			}
			if cmd == nil || !m.search.fetchingVersion {
				t.Fatal("reordered result lost its captured package identity")
			}
			msg, ok := cmd().(packageFetchedMsg)
			if !ok || msg.info == nil || msg.info.ID != second.ID || msg.source != second.Source {
				t.Fatalf("delayed row fetched %v; expected %s from %s", msg, second.ID, second.Source)
			}
		})
	}
}
