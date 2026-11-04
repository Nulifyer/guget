package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ui "github.com/nulifyer/guget/internal/ui"
)

func writeDeferredFeedIndex(w http.ResponseWriter, base string) {
	fmt.Fprintf(w, `{"resources":[{"@id":%q,"@type":"RegistrationsBaseUrl/3.6.0"},{"@id":%q,"@type":"PackageDetailsUriTemplate"}]}`, base+"/registration/", base+"/packages/{id}/{version}")
}

func writeDeferredPackage(w http.ResponseWriter, id string) {
	fmt.Fprintf(w, `{"items":[{"items":[{"catalogEntry":{"id":%q,"version":"1.0.0"}},{"catalogEntry":{"id":%q,"version":"2.0.0"}}]}]}`, id, id)
}

func TestWorkspacePublishesAndUpdatesReadyPackageWhileOtherFeedIsBlocked(t *testing.T) {
	var fastHits, slowHits atomic.Int32
	slowEntered, release := make(chan struct{}), make(chan struct{})
	var entered, unblock sync.Once
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slowHits.Add(1)
		entered.Do(func() { close(slowEntered) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer unblock.Do(func() { close(release) })
	var fast *httptest.Server
	fast = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fastHits.Add(1)
		if r.URL.Path == "/index.json" {
			writeDeferredFeedIndex(w, fast.URL)
			return
		}
		writeDeferredPackage(w, "Fast.Package")
	}))
	defer fast.Close()
	root := t.TempDir()
	path := filepath.Join(root, "App.csproj")
	mustWriteFile(t, path, `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup><ItemGroup><PackageReference Include="Fast.Package" Version="1.0.0"/><PackageReference Include="Slow.Package" Version="1.0.0"/></ItemGroup></Project>`)
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), fmt.Sprintf(`<configuration><packageSources><clear/><add key="slow" value="%s/index.json"/><add key="fast" value="%s/index.json"/></packageSources><packageSourceMapping><packageSource key="slow"><package pattern="Slow.*"/></packageSource><packageSource key="fast"><package pattern="Fast.*"/></packageSource></packageSourceMapping></configuration>`, slow.URL, fast.URL))
	snapshot, err := loadWorkspaceContext(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ParsedProjects) != 1 || fastHits.Load() != 0 || slowHits.Load() != 0 {
		t.Fatal("local workspace discovery contacted a feed")
	}
	m := NewApp(root, snapshot, nil, BuiltFlags{SortBy: "name:asc"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.lifecycle = ctx
	messages := make(chan ui.Event, 20)
	m.SetSender(func(e ui.Event) { messages <- e })
	m.ctx.Width, m.ctx.Height = 140, 40
	m.focus = focusPackages
	m.startInitialLoad()
	if !strings.Contains(m.Render().Content, "Fast.Package") {
		t.Fatal("local package list is unavailable before source resolution")
	}
	select {
	case <-slowEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("slow mapped source was not exercised")
	}
	select {
	case msg := <-messages:
		ready, ok := msg.(packageReadyMsg)
		if !ok || ready.name != "Fast.Package" || ready.result.err != nil {
			t.Fatalf("first package result = %#v", msg)
		}
		executeActionCmd(t, m, m.HandleEvent(msg))
	case <-time.After(2 * time.Second):
		t.Fatal("useful package waited for unrelated source discovery")
	}
	m.selectPackageByName("Fast.Package")
	effect := m.updatePackage(false, scopeSelected)
	if effect == nil {
		t.Fatal("ready package update was unavailable")
	}
	executeActionCmd(t, m, effect)
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `Include="Fast.Package" Version="2.0.0"`) {
		t.Fatalf("package update did not reach disk: %s, %v", data, err)
	}
	if !m.ctx.PendingPackages.Contains("Slow.Package") || slowHits.Load() == 0 {
		t.Fatal("blocked unrelated source did not remain pending during update")
	}
}

func TestDeferredFeedSharesDiscoveryAndKeepsLinksNonblocking(t *testing.T) {
	var hits atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	var signal, unblock sync.Once
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			hits.Add(1)
			signal.Do(func() { close(entered) })
			<-release
			writeDeferredFeedIndex(w, server.URL)
			return
		}
		writeDeferredPackage(w, "Example")
	}))
	defer server.Close()
	defer unblock.Do(func() { close(release) })
	svc, err := newDeferredNugetService(NugetSource{Name: "test", URL: server.URL + "/index.json"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 16)
	for range 16 {
		go func() { _, err := svc.SearchExactContext(context.Background(), "Example"); done <- err }()
	}
	<-entered
	link := make(chan string, 1)
	go func() { link <- svc.PackageURL("Example", "2.0.0", "https://example.invalid") }()
	select {
	case <-link:
	case <-time.After(time.Second):
		t.Fatal("rendered package URL waited for feed discovery")
	}
	unblock.Do(func() { close(release) })
	for range 16 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 || svc.PackageURL("Example", "2.0.0", "") != server.URL+"/packages/Example/2.0.0" {
		t.Fatal("concurrent lookups did not share initialized endpoints")
	}
}

func TestDeferredFeedRetriesCanceledGeneration(t *testing.T) {
	var hits atomic.Int32
	entered := make(chan struct{})
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			if hits.Add(1) == 1 {
				close(entered)
				<-r.Context().Done()
				return
			}
			writeDeferredFeedIndex(w, server.URL)
			return
		}
		writeDeferredPackage(w, "Example")
	}))
	defer server.Close()
	svc, err := newDeferredNugetService(NugetSource{Name: "test", URL: server.URL + "/index.json"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := svc.SearchExactContext(ctx, "Example"); done <- err }()
	<-entered
	waiters := make(chan error, 16)
	for range 16 {
		go func() {
			_, err := svc.SearchExactContext(context.Background(), "Example")
			waiters <- err
		}()
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled lookup succeeded")
	}
	for range 16 {
		if err := <-waiters; err != nil {
			t.Fatalf("canceling the initiating caller failed an independent caller: %v", err)
		}
	}
	if _, err := svc.SearchExactContext(context.Background(), "Example"); err != nil || hits.Load() != 2 {
		t.Fatalf("reused feed retained canceled initialization: hits=%d, err=%v", hits.Load(), err)
	}
}

func TestDeferredFeedKeepsSuccessfulDiscoveryAfterGenerationCancellation(t *testing.T) {
	var hits atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			hits.Add(1)
			writeDeferredFeedIndex(w, server.URL)
			return
		}
		writeDeferredPackage(w, "Example")
	}))
	defer server.Close()
	svc, err := newDeferredNugetService(NugetSource{Name: "test", URL: server.URL + "/index.json"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := svc.SearchExactContext(ctx, "Example"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := svc.SearchExactContext(context.Background(), "Example"); err != nil || hits.Load() != 1 {
		t.Fatalf("successful discovery was discarded after cancel: hits=%d, err=%v", hits.Load(), err)
	}
}

func TestDeferredFeedCachesFailureUntilReloadReplacesHandle(t *testing.T) {
	var hits atomic.Int32
	var healthy atomic.Bool
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			hits.Add(1)
			if !healthy.Load() {
				http.NotFound(w, r)
				return
			}
			writeDeferredFeedIndex(w, server.URL)
			return
		}
		writeDeferredPackage(w, "Example")
	}))
	defer server.Close()
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "App.csproj"), `<Project/>`)
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), fmt.Sprintf(`<configuration><packageSources><clear/><add key="test" value="%s/index.json"/></packageSources></configuration>`, server.URL))
	first, err := loadWorkspaceContext(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := first.NugetServices[0].SearchExactContext(ctx, "Example"); err == nil {
		t.Fatal("failed feed unexpectedly succeeded")
	}
	// A later save cancels that metadata generation. It must not reclassify
	// the already completed HTTP failure as a canceled initialization.
	cancel()
	for range 3 {
		if _, err := first.NugetServices[0].SearchExactContext(context.Background(), "Example"); err == nil {
			t.Fatal("failed feed unexpectedly succeeded")
		}
	}
	if hits.Load() != 1 {
		t.Fatal("failed discovery was repeated per package")
	}
	healthy.Store(true)
	reused, err := loadWorkspaceReusingServices(context.Background(), root, first)
	if err != nil || reused.NugetServices[0] != first.NugetServices[0] {
		t.Fatal("package-file reload did not reuse feed handle")
	}
	fresh, err := loadWorkspaceContext(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.NugetServices[0].SearchExactContext(context.Background(), "Example"); err != nil || hits.Load() != 2 {
		t.Fatalf("manual reload did not recover failed source: hits=%d, err=%v", hits.Load(), err)
	}
}
