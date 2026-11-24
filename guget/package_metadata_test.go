package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// testLeaf is one registration catalog entry served by testFeeds.
type testLeaf struct {
	Version     string
	Listed      *bool
	Frameworks  []string
	Description string
	Vulnerable  bool
	Deprecated  []string // deprecation reasons
	Alternate   string
}

// testFeeds serves several NuGet V3 feeds from one local server. Each feed
// lives at /NAME/index.json with a registration resource; a feed named in
// failing answers 500, and one named in blocking waits for the request to end.
type testFeeds struct {
	server   *httptest.Server
	mu       sync.Mutex
	packages map[string]map[string][]testLeaf // feed → lower-case id → leaves
	failing  map[string]bool
	blocking map[string]bool
	requests map[string]int // feed → registration requests
}

func newTestFeeds(t *testing.T) *testFeeds {
	t.Helper()
	feeds := &testFeeds{packages: map[string]map[string][]testLeaf{}, failing: map[string]bool{}, blocking: map[string]bool{}, requests: map[string]int{}}
	feeds.server = httptest.NewServer(http.HandlerFunc(feeds.serve))
	t.Cleanup(feeds.server.Close)
	return feeds
}

func (f *testFeeds) add(feed, id string, leaves ...testLeaf) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.packages[feed] == nil {
		f.packages[feed] = map[string][]testLeaf{}
	}
	f.packages[feed][strings.ToLower(id)] = append(f.packages[feed][strings.ToLower(id)], leaves...)
}

func (f *testFeeds) url(feed string) string { return f.server.URL + "/" + feed + "/index.json" }

func (f *testFeeds) source(feed string) NugetSource { return NugetSource{Name: feed, URL: f.url(feed)} }

func (f *testFeeds) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	feed := parts[0]
	if len(parts) == 2 && parts[1] == "index.json" {
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "3.0.0", "resources": []map[string]string{
			{"@id": f.server.URL + "/" + feed + "/registration/", "@type": "RegistrationsBaseUrl/3.6.0"},
		}})
		return
	}
	f.mu.Lock()
	f.requests[feed]++
	failing, blocking := f.failing[feed], f.blocking[feed]
	leaves, ok := f.packages[feed][strings.ToLower(parts[len(parts)-2])]
	f.mu.Unlock()
	switch {
	case blocking:
		<-r.Context().Done()
		return
	case failing:
		w.WriteHeader(http.StatusInternalServerError)
		return
	case len(parts) != 4 || parts[1] != "registration" || !ok:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var items []map[string]any
	for _, leaf := range leaves {
		entry := map[string]any{"id": parts[2], "version": leaf.Version, "description": leaf.Description}
		if leaf.Listed != nil {
			entry["listed"] = *leaf.Listed
		}
		var groups []map[string]any
		for _, framework := range leaf.Frameworks {
			groups = append(groups, map[string]any{"targetFramework": framework})
		}
		entry["dependencyGroups"] = groups
		if leaf.Vulnerable {
			entry["vulnerabilities"] = []map[string]any{{"advisoryUrl": "https://advisory.example/" + leaf.Version, "severity": "3"}}
		}
		if len(leaf.Deprecated) > 0 {
			entry["deprecation"] = map[string]any{"reasons": leaf.Deprecated, "message": "deprecated " + leaf.Version, "alternatePackage": map[string]string{"id": leaf.Alternate}}
		}
		items = append(items, map[string]any{"catalogEntry": entry})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"items": items}}})
}

func (f *testFeeds) services(t *testing.T, feeds ...string) []*NugetService {
	t.Helper()
	var services []*NugetService
	for _, feed := range feeds {
		service, err := NewNugetServiceContext(context.Background(), f.source(feed))
		if err != nil {
			t.Fatal(err)
		}
		services = append(services, service)
	}
	return services
}

func listed(value bool) *bool { return &value }

// config returns a NuGet.Config with the given feeds, in order, and a package
// source mapping when patterns is not empty.
func (f *testFeeds) config(feeds []string, patterns map[string][]string) string {
	var b strings.Builder
	b.WriteString(`<configuration><packageSources><clear/>`)
	for _, feed := range feeds {
		b.WriteString(`<add key="` + feed + `" value="` + f.url(feed) + `" allowInsecureConnections="true"/>`)
	}
	b.WriteString(`</packageSources>`)
	if len(patterns) > 0 {
		b.WriteString(`<packageSourceMapping>`)
		for _, feed := range feeds {
			if len(patterns[feed]) == 0 {
				continue
			}
			b.WriteString(`<packageSource key="` + feed + `">`)
			for _, pattern := range patterns[feed] {
				b.WriteString(`<package pattern="` + pattern + `"/>`)
			}
			b.WriteString(`</packageSource>`)
		}
		b.WriteString(`</packageSourceMapping>`)
	}
	b.WriteString(`</configuration>`)
	return b.String()
}

// mapping resolves a package source mapping for the patterns' feeds.
func (f *testFeeds) mapping(t *testing.T, patterns map[string][]string) *PackageSourceMapping {
	t.Helper()
	root := t.TempDir()
	var feeds []string
	for feed := range patterns {
		feeds = append(feeds, feed)
	}
	sort.Strings(feeds)
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), f.config(feeds, patterns))
	project := writeCLIProject(t, root, "App", `<Project><PropertyGroup><TargetFramework>net8.0</TargetFramework></PropertyGroup></Project>`)
	resolved, err := resolveWorkspaceSources(parseTestProjects(t, project))
	if err != nil || resolved.Mapping == nil {
		t.Fatalf("mapping: %v", err)
	}
	return resolved.Mapping
}

func TestFetchPackageMetadataMergesEverySource(t *testing.T) {
	feeds := newTestFeeds(t)
	feeds.add("private", "Example",
		testLeaf{Version: "1.0.0", Description: "private one"},
		testLeaf{Version: "1.1.0", Description: "private", Listed: listed(false)},
		testLeaf{Version: "2.0.0-beta", Description: "private beta"})
	feeds.add("public", "Example",
		testLeaf{Version: "1.0.0", Description: "public one", Vulnerable: true},
		testLeaf{Version: "1.1.0", Description: "public listed"},
		testLeaf{Version: "1.2.0", Description: "public newest", Deprecated: []string{"Legacy"}})
	metadata := fetchPackageMetadata(context.Background(), feeds.services(t, "private", "public"), nil, "Example")
	if metadata.Err != nil || len(metadata.Failures) != 0 {
		t.Fatalf("metadata = %+v", metadata)
	}
	var got []string
	for _, v := range metadata.Info.Versions {
		entry := v.SemVer.String() + "@" + v.Source
		if v.Unlisted {
			entry += "!"
		}
		got = append(got, entry)
	}
	// 1.0.0 comes from the first source; 1.1.0 is listed on public.
	if strings.Join(got, " ") != "2.0.0-beta@private 1.2.0@public 1.1.0@public 1.0.0@private" {
		t.Fatalf("versions = %v", got)
	}
	if metadata.Source != "public" || metadata.Info.Description != "public newest" || metadata.Info.LatestVersion != "1.2.0" {
		t.Fatalf("package metadata from %s: %+v", metadata.Source, metadata.Info)
	}
	if metadata.Info.Versions[1].Deprecation == nil || metadata.Info.Versions[1].Deprecation.Reasons[0] != "Legacy" {
		t.Fatalf("per-version deprecation lost: %+v", metadata.Info.Versions[1])
	}
	if latest := assessPackage(metadata.Info, testRefs("1.0.0"), nil, UpdateTargetStable).Target; versionText(latest) != "1.2.0" {
		t.Fatalf("merged target = %s", versionText(latest))
	}
}

func TestFetchPackageMetadataHonoursSourceMapping(t *testing.T) {
	feeds := newTestFeeds(t)
	feeds.add("private", "Contoso.Core", testLeaf{Version: "1.0.0"})
	feeds.add("public", "Contoso.Core", testLeaf{Version: "9.0.0"})
	mapping := feeds.mapping(t, map[string][]string{"private": {"Contoso.*"}, "public": {"*"}})
	metadata := fetchPackageMetadata(context.Background(), feeds.services(t, "private", "public"), mapping, "Contoso.Core")
	if metadata.Err != nil || len(metadata.Info.Versions) != 1 || metadata.Info.Versions[0].SemVer.String() != "1.0.0" {
		t.Fatalf("mapping ignored: %+v", metadata)
	}
	feeds.mu.Lock()
	defer feeds.mu.Unlock()
	if feeds.requests["public"] != 0 {
		t.Fatal("a source the mapping excludes was contacted")
	}
}

func TestFetchPackageMetadataToleratesFailingSources(t *testing.T) {
	feeds := newTestFeeds(t)
	feeds.add("good", "Example", testLeaf{Version: "1.0.0"})
	feeds.failing["bad"] = true
	metadata := fetchPackageMetadata(context.Background(), feeds.services(t, "bad", "good"), nil, "Example")
	if metadata.Err != nil || metadata.Info == nil || len(metadata.Failures) != 1 || metadata.Failures[0].Source != "bad" {
		t.Fatalf("metadata = %+v", metadata)
	}
	metadata = fetchPackageMetadata(context.Background(), feeds.services(t, "bad"), nil, "Example")
	if metadata.Err == nil || metadata.Info != nil || len(metadata.Failures) != 1 {
		t.Fatalf("metadata = %+v", metadata)
	}
	// Not found everywhere is an answer, not a source failure.
	metadata = fetchPackageMetadata(context.Background(), feeds.services(t, "good"), nil, "Missing")
	if metadata.Err == nil || len(metadata.Failures) != 0 || !strings.Contains(metadata.Err.Error(), "not found") {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestFetchPackageMetadataStopsOnCancel(t *testing.T) {
	feeds := newTestFeeds(t)
	feeds.add("good", "Example", testLeaf{Version: "1.0.0"})
	feeds.blocking["slow"] = true
	services := feeds.services(t, "good", "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan packageMetadata, 1)
	go func() { done <- fetchPackageMetadata(ctx, services, nil, "Example") }()
	select {
	case metadata := <-done:
		if metadata.Err == nil {
			t.Fatalf("canceled fetch returned %+v", metadata)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fetch ignored cancellation")
	}
}
