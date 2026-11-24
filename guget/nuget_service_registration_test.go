package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A registration page with an unlisted newest version, a version NuGet
// cannot parse, and dependency groups in the long framework form nuget.org
// returns.
func TestSearchExactHonoursListedAndParsesLongFrameworkNames(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			fmt.Fprintf(w, `{"resources":[{"@id":%q,"@type":"RegistrationsBaseUrl/3.6.0"}]}`, server.URL+"/registration/")
			return
		}
		fmt.Fprint(w, `{"items":[{"items":[
			{"catalogEntry":{"id":"Example","version":"1.0.0","description":"one","dependencyGroups":[{"targetFramework":".NETFramework4.7.2"},{"targetFramework":".NETStandard2.0"}]}},
			{"catalogEntry":{"id":"Example","version":"2.0.0","listed":true,"description":"two","dependencyGroups":[{"targetFramework":"net8.0-windows7.0"}]}},
			{"catalogEntry":{"id":"Example","version":"3.0.0","listed":false,"description":"three"}},
			{"catalogEntry":{"id":"Example","version":"not-a-version"}}
		]}]}`)
	}))
	defer server.Close()
	svc, err := NewNugetServiceContext(context.Background(), NugetSource{Name: "local", URL: server.URL + "/index.json"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := svc.SearchExactContext(context.Background(), "Example")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Versions) != 3 {
		t.Fatalf("versions = %d, want the three NuGet can parse", len(info.Versions))
	}
	if !info.Versions[0].Unlisted || info.Versions[1].Unlisted || info.Versions[2].Unlisted {
		t.Fatalf("listed flags wrong: %+v", info.Versions)
	}
	if latest := info.LatestStable(); latest == nil || latest.SemVer.String() != "2.0.0" {
		t.Fatalf("LatestStable = %v, want the newest listed version", latest)
	}
	if info.LatestVersion != "2.0.0" || info.Description != "two" {
		t.Fatalf("metadata from %q (%q), want the newest listed version", info.LatestVersion, info.Description)
	}
	if got := info.LatestStableForFramework(targetsOf("net8.0")); got == nil || got.SemVer.String() != "1.0.0" {
		t.Fatalf("net8.0 cannot use the net8.0-windows7.0 build; got %v", got)
	}
	if got := info.LatestStableForFramework(targetsOf("net8.0-windows")); got == nil || got.SemVer.String() != "2.0.0" {
		t.Fatalf("net8.0-windows can use 2.0.0; got %v", got)
	}
	if got := info.LatestStableForFramework(targetsOf("net48")); got == nil || got.SemVer.String() != "1.0.0" {
		t.Fatalf("net48 can use the netstandard2.0 build; got %v", got)
	}
	frameworks := info.Versions[2].Frameworks
	if len(frameworks) != 2 || frameworks[0].String() != "net472" || !sameFramework(frameworks[0], fwNet472) {
		t.Fatalf("frameworks = %+v", frameworks)
	}
}

func TestUnlistedVersionsAreNeverLatest(t *testing.T) {
	info := &PackageInfo{Versions: []PackageVersion{
		{SemVer: ParseSemVer("3.0.0"), Unlisted: true},
		{SemVer: ParseSemVer("2.0.0")},
		{SemVer: ParseSemVer("1.0.0"), Unlisted: true},
	}}
	if v := info.LatestStable(); v == nil || v.SemVer.String() != "2.0.0" {
		t.Fatalf("LatestStable = %v", v)
	}
	if v := info.LatestStableForFramework(targetsOf("net8.0")); v == nil || v.SemVer.String() != "2.0.0" {
		t.Fatalf("LatestStableForFramework = %v", v)
	}
	if cursor := defaultVersionCursor(info.Versions, targetsOf("net8.0")); cursor != 1 {
		t.Fatalf("default cursor on unlisted version %d", cursor)
	}
	only := &PackageInfo{Versions: []PackageVersion{{SemVer: ParseSemVer("1.0.0"), Unlisted: true}}}
	if only.LatestStable() != nil || only.LatestStableForFramework(targetsOf("net8.0")) != nil {
		t.Fatal("an unlisted-only package has no latest version")
	}
}
