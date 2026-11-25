//go:build integration

package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// packageFeed serves .nupkg files as NuGet V3 feeds, one per directory under
// root: /NAME/index.json with a flat container and registration resource.
// Versions named in unlisted are served as unlisted.
type packageFeed struct {
	root     string
	unlisted map[string]bool // "feed/id/version", lower case
}

type feedPackage struct {
	id, version, path string
	frameworks        []string
}

func (f *packageFeed) packages(feed string) []feedPackage {
	paths, _ := filepath.Glob(filepath.Join(f.root, feed, "*.nupkg"))
	var result []feedPackage
	for _, path := range paths {
		reader, err := zip.OpenReader(path)
		if err != nil {
			continue
		}
		var nuspec struct {
			Metadata struct {
				ID      string `xml:"id"`
				Version string `xml:"version"`
			} `xml:"metadata"`
		}
		frameworks := map[string]bool{}
		for _, file := range reader.File {
			if strings.HasSuffix(file.Name, ".nuspec") {
				rc, _ := file.Open()
				_ = xml.NewDecoder(rc).Decode(&nuspec)
				rc.Close()
			}
			if parts := strings.Split(file.Name, "/"); len(parts) > 2 && parts[0] == "lib" {
				frameworks[parts[1]] = true
			}
		}
		reader.Close()
		pkg := feedPackage{id: nuspec.Metadata.ID, version: nuspec.Metadata.Version, path: path}
		for framework := range frameworks {
			pkg.frameworks = append(pkg.frameworks, framework)
		}
		result = append(result, pkg)
	}
	return result
}

func (f *packageFeed) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	base := "http://" + r.Host + "/" + parts[0]
	w.Header().Set("Content-Type", "application/json")
	if len(parts) == 2 && parts[1] == "index.json" {
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "3.0.0", "resources": []map[string]string{
			{"@id": base + "/flat/", "@type": "PackageBaseAddress/3.0.0"},
			{"@id": base + "/registration/", "@type": "RegistrationsBaseUrl/3.6.0"},
		}})
		return
	}
	if len(parts) < 4 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	id := strings.ToLower(parts[2])
	var matching []feedPackage
	for _, pkg := range f.packages(parts[0]) {
		if strings.EqualFold(pkg.id, id) {
			matching = append(matching, pkg)
		}
	}
	if len(matching) == 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	sort.Slice(matching, func(i, j int) bool {
		return ParseSemVer(matching[i].version).Compare(ParseSemVer(matching[j].version)) < 0
	})
	switch {
	case parts[1] == "flat" && parts[3] == "index.json":
		var versions []string
		for _, pkg := range matching {
			versions = append(versions, strings.ToLower(pkg.version))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"versions": versions})
	case parts[1] == "flat" && len(parts) == 5:
		for _, pkg := range matching {
			if strings.EqualFold(pkg.version, parts[3]) {
				w.Header().Set("Content-Type", "application/octet-stream")
				data, _ := os.ReadFile(pkg.path)
				_, _ = w.Write(data)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	case parts[1] == "registration" && parts[3] == "index.json":
		var items []map[string]any
		for _, pkg := range matching {
			version := strings.ToLower(pkg.version)
			listed := !f.unlisted[strings.ToLower(parts[0]+"/"+id+"/"+version)]
			published := "2024-01-01T00:00:00+00:00"
			if !listed {
				published = "1900-01-01T00:00:00+00:00"
			}
			var groups []map[string]any
			for _, framework := range pkg.frameworks {
				groups = append(groups, map[string]any{"@id": base + "/dg/" + framework, "targetFramework": framework})
			}
			leaf := base + "/registration/" + id + "/" + version + ".json"
			items = append(items, map[string]any{
				"@id": leaf, "packageContent": base + "/flat/" + id + "/" + version + "/" + id + "." + version + ".nupkg",
				"catalogEntry": map[string]any{
					"@id": leaf, "id": pkg.id, "version": pkg.version, "listed": listed, "published": published,
					"dependencyGroups": groups, "description": pkg.id,
				},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "items": []map[string]any{{
			"@id": base + "/registration/" + id + "/index.json#page", "count": len(items),
			"lower": matching[0].version, "upper": matching[len(matching)-1].version, "items": items,
		}}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func runDotnet(t *testing.T, env map[string]string, dir string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "dotnet", args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "DOTNET_NOLOGO=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1", "MSBUILDDISABLENODEREUSE=1")
	for name, value := range env {
		command.Env = append(command.Env, name+"="+value)
	}
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("dotnet %s timed out:\n%s", strings.Join(args, " "), output)
	}
	return string(output), err
}

type dotnetOutdated struct {
	Projects []struct {
		Frameworks []struct {
			TopLevelPackages []struct {
				ID              string `json:"id"`
				ResolvedVersion string `json:"resolvedVersion"`
				LatestVersion   string `json:"latestVersion"`
			} `json:"topLevelPackages"`
		} `json:"frameworks"`
	} `json:"projects"`
}

// TestUpdateTargetStableMatchesDotnet builds packages with dotnet pack,
// serves them from two local HTTP feeds, and requires guget's stable update
// target to match `dotnet list package --outdated` and `dotnet package
// update` for a stable and a prerelease reference.
func TestUpdateTargetStableMatchesDotnet(t *testing.T) {
	requireIsolatedDotnet(t)
	base := t.TempDir()
	env := isolatedNuGetEnv(base)
	env["GUGET_CONFIG"] = filepath.Join(base, "guget.toml")
	mustWriteFile(t, filepath.Join(base, userConfigPath), oracleConfig(""))

	// Feed a: Oracle.Stable 1.0.0, 2.0.0-beta.1, unlisted 3.0.0; Oracle.Pre
	// 1.0.0 and 2.0.0-preview.1. Feed b: Oracle.Stable 1.1.0, Oracle.Pre
	// 2.0.0-preview.2. Only merging both feeds finds 1.1.0 and preview.2.
	library := filepath.Join(base, "lib", "Lib.csproj")
	mustWriteFile(t, filepath.Join(base, "lib", "nuget.config"), oracleConfig(`<packageSources><clear /></packageSources>`))
	mustWriteFile(t, filepath.Join(base, "lib", "readme.txt"), "oracle")
	mustWriteFile(t, library, `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework>`+
		`<EnableTargetingPackDownload>false</EnableTargetingPackDownload><IncludeBuildOutput>false</IncludeBuildOutput><NoWarn>$(NoWarn);NU5128</NoWarn>`+
		`<Authors>guget</Authors><Description>oracle</Description></PropertyGroup>`+
		`<ItemGroup><None Include="readme.txt" Pack="true" PackagePath="docs/" /></ItemGroup></Project>`)
	if output, err := runDotnet(t, env, filepath.Dir(library), "restore", library, "-nologo"); err != nil {
		t.Fatalf("dotnet restore: %v\n%s", err, output)
	}
	packs := []struct{ feed, id, version string }{
		{"a", "Oracle.Stable", "1.0.0"}, {"a", "Oracle.Stable", "2.0.0-beta.1"}, {"a", "Oracle.Stable", "3.0.0"},
		{"b", "Oracle.Stable", "1.1.0"},
		{"a", "Oracle.Pre", "1.0.0"}, {"a", "Oracle.Pre", "2.0.0-preview.1"},
		{"b", "Oracle.Pre", "2.0.0-preview.2"},
	}
	for _, pack := range packs {
		output, err := runDotnet(t, env, filepath.Dir(library), "pack", library, "-nologo", "--no-build", "--no-restore", "-o", filepath.Join(base, "feeds", pack.feed), "-p:PackageId="+pack.id, "-p:Version="+pack.version)
		if err != nil {
			t.Fatalf("dotnet pack %s %s: %v\n%s", pack.id, pack.version, err, output)
		}
	}
	feed := &packageFeed{root: filepath.Join(base, "feeds"), unlisted: map[string]bool{"a/oracle.stable/3.0.0": true}}
	server := httptest.NewServer(feed)
	defer server.Close()

	repo := filepath.Join(base, "repo")
	config := oracleConfig(`<packageSources><clear/>` +
		fmt.Sprintf(`<add key="a" value="%s/a/index.json" allowInsecureConnections="true" />`, server.URL) +
		fmt.Sprintf(`<add key="b" value="%s/b/index.json" allowInsecureConnections="true" />`, server.URL) +
		`</packageSources>`)
	mustWriteFile(t, filepath.Join(repo, "nuget.config"), config)
	project := filepath.Join(repo, "App", "App.csproj")
	const appProject = `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><EnableTargetingPackDownload>false</EnableTargetingPackDownload><NuGetAudit>false</NuGetAudit></PropertyGroup><ItemGroup><PackageReference Include="Oracle.Stable" Version="1.0.0" /><PackageReference Include="Oracle.Pre" Version="2.0.0-preview.1" /></ItemGroup></Project>`
	mustWriteFile(t, project, appProject)
	if output, err := runDotnet(t, env, repo, "restore", project, "-nologo"); err != nil {
		t.Fatalf("dotnet restore: %v\n%s", err, output)
	}

	dotnetLatest := func(includePrerelease bool) map[string]string {
		args := []string{"list", project, "package", "--outdated", "--format", "json", "--output-version", "1"}
		if includePrerelease {
			args = append(args, "--include-prerelease")
		}
		output, err := runDotnet(t, env, repo, args...)
		if err != nil {
			t.Fatalf("dotnet list --outdated: %v\n%s", err, output)
		}
		var document dotnetOutdated
		if err := json.Unmarshal([]byte(output[strings.Index(output, "{"):]), &document); err != nil {
			t.Fatalf("dotnet list output: %v\n%s", err, output)
		}
		latest := map[string]string{}
		for _, p := range document.Projects {
			for _, framework := range p.Frameworks {
				for _, pkg := range framework.TopLevelPackages {
					latest[pkg.ID] = pkg.LatestVersion
				}
			}
		}
		return latest
	}
	plain, prerelease := dotnetLatest(false), dotnetLatest(true)

	for name, value := range env {
		t.Setenv(name, value)
	}
	var stdout, stderr bytes.Buffer
	cli := &cliRuntime{stdout: &stdout, stderr: &stderr}
	code := cli.dispatch(context.Background(), []string{"list", "--project", repo, "--outdated", "--update-target", "stable", "--format", "json"})
	if code != ExitSuccess {
		t.Fatalf("guget list: code=%d stderr=%s", code, stderr.String())
	}
	var document listDocument
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	guget := map[string]string{}
	for _, row := range document.Packages {
		if row.UpdateTarget != nil {
			guget[row.Package] = *row.UpdateTarget
		}
	}
	// dotnet list ignores prereleases unless asked; guget's stable target
	// counts them for a prerelease reference, like dotnet package update.
	if guget["Oracle.Stable"] != plain["Oracle.Stable"] || guget["Oracle.Stable"] != "1.1.0" {
		t.Fatalf("stable reference: guget %q, dotnet list --outdated %q", guget["Oracle.Stable"], plain["Oracle.Stable"])
	}
	if guget["Oracle.Pre"] != prerelease["Oracle.Pre"] || guget["Oracle.Pre"] != "2.0.0-preview.2" {
		t.Fatalf("prerelease reference: guget %q, dotnet list --outdated --include-prerelease %q", guget["Oracle.Pre"], prerelease["Oracle.Pre"])
	}

	if output, _ := runDotnet(t, env, repo, "package", "update", "--help"); !strings.Contains(output, "Update referenced packages") {
		t.Log("dotnet package update is not available in this SDK; skipping the update comparison")
		return
	}
	for _, id := range []string{"Oracle.Stable", "Oracle.Pre"} {
		mustWriteFile(t, project, appProject)
		if output, err := runDotnet(t, env, repo, "package", "update", id, "--project", project); err != nil {
			t.Fatalf("dotnet package update %s: %v\n%s", id, err, output)
		}
		want := declaredVersion(t, project, id)
		mustWriteFile(t, project, appProject)
		stdout.Reset()
		stderr.Reset()
		code := cli.dispatch(context.Background(), []string{"update", id, "--latest", "--update-target", "stable", "--project", repo, "--file", project, "--format", "json"})
		if code != ExitSuccess {
			t.Fatalf("guget update %s: code=%d stderr=%s", id, code, stderr.String())
		}
		if got := declaredVersion(t, project, id); got != want {
			t.Fatalf("%s: guget update --latest wrote %q, dotnet package update wrote %q", id, got, want)
		}
	}
}

// declaredVersion reads the Version attribute of one PackageReference.
func declaredVersion(t *testing.T, project, id string) string {
	t.Helper()
	file, err := os.Open(project)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if element, ok := token.(xml.StartElement); ok && element.Name.Local == "PackageReference" {
			var include, version string
			for _, attr := range element.Attr {
				switch attr.Name.Local {
				case "Include":
					include = attr.Value
				case "Version":
					version = attr.Value
				}
			}
			if strings.EqualFold(include, id) {
				return version
			}
		}
	}
	t.Fatalf("%s has no reference to %s", project, id)
	return ""
}
