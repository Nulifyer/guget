package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	ui "github.com/nulifyer/guget/internal/ui"
	style "github.com/nulifyer/guget/internal/ui/style"
)

func TestHeldUpdatesDoNotWaitForUnrelatedMetadata(t *testing.T) {
	m, messages, path := newActionTestApp(t, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="A" Version="1.0.0"/><PackageReference Include="B" Version="1.0.0"/><PackageReference Include="C" Version="1.0.0"/></ItemGroup></Project>`)
	delete(m.ctx.Results, "C")
	m.ctx.Loading = true
	m.ctx.LoadingTotal = 1
	m.ctx.PendingPackages.Add("C")
	m.rebuildPackageRows()
	for _, name := range []string{"A", "B"} {
		if selectedActionPackage(m) != name {
			t.Fatalf("selected=%s, want %s", selectedActionPackage(m), name)
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u', IsRepeat: true}))
		if cmd == nil || !m.ctx.Saving {
			t.Fatal("ready package was blocked by metadata")
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before) {
			t.Fatal("key handler wrote files synchronously")
		}
		executeActionCmd(t, m, cmd)
		finishActionReload(t, m, messages)
		if !m.canMutate() || m.ctx.LoadingDone >= m.ctx.LoadingTotal {
			t.Fatal("reload did not release mutations while metadata remained pending")
		}
	}
	project, err := ParseCsproj(path)
	if err != nil {
		t.Fatal(err)
	}
	for ref := range project.Packages {
		want := "2.0.0"
		if ref.Name == "C" {
			want = "1.0.0"
		}
		if ref.Version.String() != want {
			t.Fatalf("%s=%s, want %s", ref.Name, ref.Version.String(), want)
		}
	}
}

func TestLoadingKeepsPanelsAndHelpVisible(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.ctx.Loading = true
	m.ctx.LoadingTotal = 2
	view := m.Render()
	for _, label := range []string{"Projects", "Package", "loading packages"} {
		if !strings.Contains(view.Content, label) {
			t.Fatalf("loading view omits %q", label)
		}
	}
	cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: '?'}))
	executeActionCmd(t, m, cmd)
	if !m.help.active || !strings.Contains(m.Render().Content, "Navigation") {
		t.Fatal("help inaccessible during loading")
	}
}

func TestSearchCancellationAndSessionIsolation(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.openSearch()
	m.search.results = []SearchResult{{ID: "C"}}
	cmd := m.search.HandleKey(ui.KeyPress(ui.Key{Code: ui.KeyEnter}))
	if cmd == nil || !m.search.fetchingVersion {
		t.Fatal("fetch did not start")
	}
	msg := cmd()
	m.search.HandleKey(ui.KeyPress(ui.Key{Code: ui.KeyEscape}))
	m.HandleEvent(msg)
	if m.picker.active {
		t.Fatal("canceled search opened a version picker")
	}
	m.openSearch()
	m.search.fetchingVersion = true
	m.HandleEvent(msg)
	if m.picker.active || !m.search.active || !m.search.fetchingVersion {
		t.Fatal("old fetch affected reopened search")
	}
	m.HandleEvent(packageFetchedMsg{requestID: m.search.requestID, info: m.ctx.Results["C"].pkg})
	if !m.picker.active || m.search.active {
		t.Fatal("current fetch did not open picker")
	}
}

func TestSearchIgnoresStaleResultsAndPendingEnter(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.openSearch()
	m.search.input.SetValue("C")
	m.search.lastQuery = "C"
	m.search.debounceID = 3
	m.search.loading = true
	m.search.results = []SearchResult{{ID: "A"}}
	if cmd := m.search.HandleKey(ui.KeyPress(ui.Key{Code: ui.KeyEnter})); cmd != nil || m.search.fetchingVersion {
		t.Fatal("enter selected hidden stale results")
	}
	m.HandleEvent(searchResultsMsg{requestID: m.search.requestID, id: 1, query: "C", results: []SearchResult{{ID: "A"}}})
	if !m.search.loading {
		t.Fatal("earlier identical query replaced current request")
	}
	m.HandleEvent(searchResultsMsg{requestID: m.search.requestID, id: 3, query: "C", results: []SearchResult{{ID: "C"}}})
	if m.search.loading || m.search.results[0].ID != "C" {
		t.Fatal("current results not applied")
	}
	id := m.search.requestID
	m.search.closeOverlay()
	m.openSearch()
	m.search.lastQuery = "C"
	cmd := m.HandleEvent(searchDebounceMsg{requestID: id, id: m.search.debounceID, query: "C"})
	if cmd != nil {
		t.Fatal("closed session started a search")
	}
}

func TestReloadCancelsSearchAndRejectsLateFetch(t *testing.T) {
	m, messages, _ := newActionTestApp(t, "")
	m.openSearch()
	m.search.results = []SearchResult{{ID: "C"}}
	cmd := m.search.HandleKey(ui.KeyPress(ui.Key{Code: ui.KeyEnter}))
	m.requestReload(reloadRequestedMsg{reason: "test"})
	m.HandleEvent(cmd())
	if m.search.active || m.picker.active {
		t.Fatal("reload left stale add flow active")
	}
	finishActionReload(t, m, messages)
}

func TestOverlayRepliesCannotCrossOpenings(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.releaseNotes = newReleaseNotesOverlay(m, "A")
	oldID := m.releaseNotes.requestID
	m.releaseNotes = newReleaseNotesOverlay(m, "B")
	m.releaseNotes.nsVersions = []string{"1.0.0"}
	m.releaseNotes.ghReleases = []GitHubRelease{{TagName: "v1"}}
	m.releaseNotes.nsLoading, m.releaseNotes.ghLoading = true, true
	for _, msg := range []ui.Event{
		releaseListReadyMsg{requestID: oldID, owner: "wrong", releases: []GitHubRelease{{TagName: "old"}}},
		releaseNotesReadyMsg{requestID: oldID, tag: "v1", body: "wrong"},
		nuspecVersionNotesReadyMsg{requestID: oldID, version: "1.0.0", notes: "wrong"},
	} {
		m.HandleEvent(msg)
	}
	if m.releaseNotes.ghOwner != "" || m.releaseNotes.ghNotes != "" || len(m.releaseNotes.nsNotesCache) != 0 || !m.releaseNotes.nsLoading || !m.releaseNotes.ghLoading {
		t.Fatal("old release reply changed new package")
	}
	m.depTree = newDepTreeOverlay(m, "A", true)
	oldID = m.depTree.requestID
	m.depTree = newDepTreeOverlay(m, "B", true)
	m.HandleEvent(depTreeReadyMsg{requestID: oldID, err: errors.New("old failure")})
	if !m.depTree.loading || m.depTree.err != nil {
		t.Fatal("old tree reply changed new overlay")
	}
}

func TestCachedNuSpecNotesStopLoading(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.releaseNotes = newReleaseNotesOverlay(m, "A")
	m.releaseNotes.activeTab = tabNuSpec
	m.releaseNotes.nsVersions = []string{"2.0.0", "1.0.0"}
	m.releaseNotes.nsNotesCache = map[string]string{"1.0.0": "cached"}
	m.releaseNotes.nsLoading = true
	if cmd := m.releaseNotes.selectCursor(1); cmd != nil || m.releaseNotes.nsLoading || m.releaseNotes.nsNotes != "cached" {
		t.Fatal("cached notes remained behind spinner")
	}
}

func TestLogUpdatesAreBoundedBatchedAndPreserveScroll(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	lines := make([]string, maxLogLines+100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	cmd := m.HandleEvent(logBatchMsg{lines: lines})
	if cmd != nil || m.log.vp.GetContent() != "" {
		t.Fatal("hidden logs rebuilt viewport")
	}
	if len(m.ctx.LogLines) != maxLogLines || m.ctx.LogLines[0] != "line 100" {
		t.Fatal("log retention is unbounded or dropped wrong lines")
	}
	m.handleKey(ui.KeyPress(ui.Key{Code: 'l'}))
	if !m.log.vp.AtBottom() {
		t.Fatal("opening log did not follow tail")
	}
	m.log.vp.SetYOffset(10)
	cmd = m.HandleEvent(logBatchMsg{lines: []string{"new line"}})
	if cmd == nil {
		t.Fatal("visible logs did not schedule refresh")
	}
	next := m.HandleEvent(logBatchMsg{lines: []string{"another line"}})
	if next != nil {
		t.Fatal("log burst scheduled duplicate refresh")
	}
	executeActionCmd(t, m, cmd)
	if m.log.vp.YOffset() != 10 || m.log.vp.AtBottom() {
		t.Fatal("live logs yanked reader to bottom")
	}
}

func TestLogForwarderDoesNotBlockWriters(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	messages := make(chan logBatchMsg, 2)
	b := &logBuffer{send: func(msg ui.Event) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		messages <- msg.(logBatchMsg)
	}}
	b.Write([]byte("first\n"))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("forwarder did not start")
	}
	for i := range 100 {
		b.Write([]byte(fmt.Sprintf("line %d\n", i)))
	}
	close(release)
	var got []string
	for len(got) < 101 {
		select {
		case msg := <-messages:
			got = append(got, msg.lines...)
		case <-time.After(time.Second):
			t.Fatal("forwarder did not drain")
		}
	}
	if got[0] != "first" || got[100] != "line 99" {
		t.Fatalf("log order changed: %v", got)
	}
}

func TestTinyTerminalDoesNotPanicOrApplyHiddenActions(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	for _, size := range [][2]int{{1, 1}, {20, 2}, {79, 11}, {80, 12}} {
		m.HandleEvent(ui.ResizeEvent{Width: size[0], Height: size[1]})
		for _, overlay := range []string{"main", "search", "help", "notes", "tree"} {
			switch overlay {
			case "search":
				m.openSearch()
			case "help":
				m.help.active = true
			case "notes":
				m.releaseNotes = newReleaseNotesOverlay(m, "A")
			case "tree":
				m.depTree = newDepTreeOverlay(m, "A", true)
			}
			view := m.Render()
			if style.Height(view.Content) > size[1] {
				t.Fatalf("%s height=%d, terminal=%v", overlay, style.Height(view.Content), size)
			}
		}
		if size[0] < 80 {
			cmd := m.HandleEvent(ui.KeyPress(ui.Key{Code: 'u'}))
			if cmd != nil || m.ctx.Saving {
				t.Fatal("small terminal applied hidden action")
			}
		}
	}
}

func TestStatusTruncationPreservesUnicode(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.setStatus(strings.Repeat("界", 100), false)
	if !utf8.ValidString(m.ctx.StatusLine) || style.Width(m.ctx.StatusLine) > m.layoutWidth()-6 {
		t.Fatal("status truncation damaged UTF-8 or exceeded width")
	}
}

func TestCanceledSearchStopsNetworkWork(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	m, _, _ := newActionTestApp(t, "")
	m.ctx.NugetServices = []*NugetService{{client: server.Client(), searchBase: server.URL, sourceName: "test"}}
	m.openSearch()
	cmd := m.search.doSearchCmd("C")
	done := make(chan ui.Event, 1)
	go func() { done <- cmd() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("search did not start")
	}
	m.search.closeOverlay()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("closed search did not cancel HTTP request")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("search command did not exit")
	}
}

func TestSearchExactContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := &NugetService{client: &http.Client{}, regBase: "http://127.0.0.1:1/"}
	_, err := svc.SearchExactContext(ctx, "A")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context cancellation", err)
	}
}

func TestMetadataRepliesCoalesceRowRebuilds(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	first := m.HandleEvent(packageReadyMsg{name: "A", result: m.ctx.Results["A"]})
	second := m.HandleEvent(packageReadyMsg{name: "B", result: m.ctx.Results["B"]})
	if first == nil || second != nil {
		t.Fatal("metadata burst did not coalesce rebuilds")
	}
	m.movePackageCursor(1)
	executeActionCmd(t, m, first)
	if m.packageRefreshPending || selectedActionPackage(m) != "B" {
		t.Fatal("delayed rebuild moved later user selection")
	}
}

func TestFailedReloadResumesPendingMetadata(t *testing.T) {
	m, messages, _ := newActionTestApp(t, "")
	m.ctx.Reloading = true
	m.ctx.PendingPackages.Add("C")
	m.HandleEvent(workspaceReloadedMsg{err: errors.New("injected scan failure")})
	if m.ctx.Reloading || !m.ctx.StatusIsErr {
		t.Fatal("failed reload retained mutation guard")
	}
	select {
	case msg := <-messages:
		ready, ok := msg.(packageReadyMsg)
		if !ok || ready.name != "C" {
			t.Fatalf("unexpected retry: %#v", msg)
		}
		cmd := m.HandleEvent(msg)
		executeActionCmd(t, m, cmd)
	case <-time.After(time.Second):
		t.Fatal("failed reload stranded pending metadata")
	}
	if m.ctx.PendingPackages.Contains("C") {
		t.Fatal("retried metadata remained pending")
	}
}

func TestPackageSavesReuseFeedDiscovery(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"resources":[{"@id":"https://example.invalid/registration/","@type":"RegistrationsBaseUrl/3.6.0"}]}`)
	}))
	defer server.Close()
	m, messages, path := newActionTestApp(t, "")
	config := filepath.Join(filepath.Dir(path), "NuGet.Config")
	mustWriteFile(t, config, fmt.Sprintf(`<configuration><packageSources><clear/><add key="test" value="%s"/></packageSources></configuration>`, server.URL))
	snapshot, err := loadWorkspace(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	m.ctx.Sources, m.ctx.NugetServices = snapshot.Sources, snapshot.NugetServices
	m.sourceSignature = workspaceSourceSignature(snapshot.Sources, snapshot.SourceMapping)
	if hits.Load() != 1 {
		t.Fatalf("initial discovery requests=%d", hits.Load())
	}
	executeActionCmd(t, m, m.updatePackage(false, scopeSelected))
	finishActionReload(t, m, messages)
	if hits.Load() != 1 {
		t.Fatal("package save repeated feed discovery")
	}
	m.requestReload(reloadRequestedMsg{automatic: true, paths: []string{path}})
	finishActionReload(t, m, messages)
	if hits.Load() != 1 {
		t.Fatal("project watcher repeated feed discovery")
	}
	m.requestReload(reloadRequestedMsg{reason: "manual reload"})
	finishActionReload(t, m, messages)
	if hits.Load() != 2 {
		t.Fatal("manual reload did not refresh feeds")
	}
	// Source configuration changes must invalidate cached service credentials.
	mustWriteFile(t, config, fmt.Sprintf(`<configuration><packageSources><clear/><add key="renamed" value="%s"/></packageSources></configuration>`, server.URL))
	m.requestReload(reloadRequestedMsg{automatic: true, paths: []string{config}})
	finishActionReload(t, m, messages)
	if hits.Load() != 3 || m.ctx.NugetServices[0].SourceName() != "renamed" {
		t.Fatal("NuGet.Config change retained old service")
	}
}

func TestSourcesOverlayScrollsLongLists(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.ctx.Height = 20
	for i := range 20 {
		m.ctx.Sources = append(m.ctx.Sources, NugetSource{Name: fmt.Sprintf("source-%d", i), URL: "https://example.invalid/feed"})
	}
	m.sources.active = true
	m.sources.Render()
	if m.sources.vp.TotalLineCount() <= m.sources.vp.Height() {
		t.Fatal("fixture does not require scrolling")
	}
	for range 100 {
		m.sources.HandleKey(ui.KeyPress(ui.Key{Code: ui.KeyDown}))
	}
	if !m.sources.vp.AtBottom() || !strings.Contains(m.sources.Render(), "source-19") {
		t.Fatal("last source is inaccessible")
	}
}

func TestShortPickerKeepsSelectedRowAndButtonsVisible(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.ctx.Height = 16
	versions := make([]PackageVersion, 30)
	for i := range versions {
		versions[i].SemVer = ParseSemVer(fmt.Sprintf("1.0.%d", i))
	}
	m.picker = newVersionPicker(m, "A", versions, nil, m.selectedProject(), false)
	m.picker.cursor = 29
	view := m.Render()
	if !strings.Contains(view.Content, "1.0.29") || !strings.Contains(view.Content, "Apply") {
		t.Fatal("short picker hides selection or action")
	}
	for _, region := range m.picker.MouseRegions() {
		if region.click != nil && region.rect.y >= m.overlayHeight() {
			t.Fatal("picker control is below visible overlay")
		}
	}
}

func TestNarrowLayoutKeepsPackageNamesReadable(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	for _, width := range []int{80, 100, 120, 140} {
		m.ctx.Width = width
		left, mid, right := m.panelWidths()
		if mid < 40 || left < 10 || right < 10 || left+mid+right != width {
			t.Fatalf("terminal=%d, panels=%d/%d/%d", width, left, mid, right)
		}
	}
}

func TestHelpDescriptionsWrapInsideViewport(t *testing.T) {
	m, _, _ := newActionTestApp(t, "")
	m.ctx.Width = 80
	m.help.active = true
	m.help.refreshView()
	for _, line := range strings.Split(m.help.vp.GetContent(), "\n") {
		if style.Width(line) > m.help.vp.Width() {
			t.Fatalf("help line is clipped: %q", line)
		}
	}
}
