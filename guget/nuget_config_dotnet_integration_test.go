//go:build integration

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingFeed is a NuGet V3 feed that records which sources dotnet restore
// contacts, the Basic credentials it sends, and which sources it asks for the
// probe package. Unauthenticated requests are challenged so credentials show.
type recordingFeed struct {
	mu          sync.Mutex
	credentials map[string]string // source → "user:password" ("" when none)
	probed      map[string]bool
}

func (f *recordingFeed) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	source := parts[0]
	f.mu.Lock()
	if _, seen := f.credentials[source]; !seen {
		f.credentials[source] = ""
	}
	header := r.Header.Get("Authorization")
	if encoded, ok := strings.CutPrefix(header, "Basic "); ok {
		if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil && string(decoded) != ":" {
			f.credentials[source] = string(decoded)
		}
	}
	if len(parts) >= 3 && parts[1] == "flat" && parts[2] == "oracle.probe" {
		f.probed[source] = true
	}
	f.mu.Unlock()
	if header == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="feed"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if len(parts) == 2 && parts[1] == "index.json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "3.0.0", "resources": []map[string]string{
			{"@id": "http://" + r.Host + "/" + source + "/flat/", "@type": "PackageBaseAddress/3.0.0"},
		}})
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

type nugetOracleCase struct {
	name  string
	files map[string]string // relative path → content; {{F}} is the feed URL
	env   map[string]string
	// Compare which sources restore asks for the probe package. Only cases
	// where every source accepts the sent credentials can be compared.
	mapping bool
	// dotnet commands run in base before restore, and the only system they
	// run on when set.
	setup [][]string
	goos  string
	// Text repo/nuget.config must contain after setup.
	setupWrote string
}

// The probe is the only package restore asks for: some SDK builds would also
// download targeting packs from the feeds. SdkAnalysisLevel 8 makes an HTTP
// source from RestoreSources a warning (NU1803) instead of error NU1302, so
// restore still contacts it.
const oracleProject = `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net10.0</TargetFramework><EnableTargetingPackDownload>false</EnableTargetingPackDownload><NuGetAudit>false</NuGetAudit><SdkAnalysisLevel>8.0.100</SdkAnalysisLevel></PropertyGroup><ItemGroup><PackageReference Include="Oracle.Probe" Version="1.0.0" /></ItemGroup></Project>`

func oracleConfig(body string) string {
	return `<?xml version="1.0" encoding="utf-8"?><configuration>` + body + `</configuration>`
}

func oracleSource(key, path string) string {
	return fmt.Sprintf(`<add key="%s" value="{{F}}/%s/index.json" allowInsecureConnections="true" />`, key, path)
}

func oracleCredentials(source, user, password string) string {
	return fmt.Sprintf(`<packageSourceCredentials><%s><add key="Username" value="%s"/><add key="ClearTextPassword" value="%s"/></%s></packageSourceCredentials>`, source, user, password, source)
}

const userConfigPath = "home/.nuget/NuGet/NuGet.Config"

// requireIsolatedDotnet skips a test that compares guget with dotnet when
// the SDK is missing or NuGet's folders cannot be redirected. macOS fixes the
// machine-wide folder at /Library/Application Support/NuGet.
func requireIsolatedDotnet(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("machine-wide NuGet configuration cannot be isolated on macOS")
	}
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet SDK is not installed")
	}
}

// isolatedNuGetEnv points the NuGet folders dotnet and guget use at base.
// NuGet finds the user and machine-wide folders from DOTNET_CLI_HOME and
// NUGET_COMMON_APPLICATION_DATA on Linux, and from APPDATA and
// ProgramFiles(x86) on Windows. Both put the user configuration at
// userConfigPath and the machine-wide folder at machine/NuGet.
func isolatedNuGetEnv(base string) map[string]string {
	return map[string]string{
		"HOME":                          filepath.Join(base, "home"),
		"DOTNET_CLI_HOME":               filepath.Join(base, "home"),
		"APPDATA":                       filepath.Join(base, "home", ".nuget"),
		"NUGET_COMMON_APPLICATION_DATA": filepath.Join(base, "machine"),
		"ProgramFiles(x86)":             filepath.Join(base, "machine"),
		"NUGET_PACKAGES":                filepath.Join(base, "packages"),
		"NUGET_HTTP_CACHE_PATH":         filepath.Join(base, "http-cache"),
	}
}

// TestNuGetConfigurationMatchesDotnetRestore runs each configuration through
// dotnet restore against a recording feed and through guget's resolver, and
// requires both to agree on contacted sources, credentials, and mapping.
func TestNuGetConfigurationMatchesDotnetRestore(t *testing.T) {
	requireIsolatedDotnet(t)
	empty := oracleConfig("")
	cases := []nugetOracleCase{
		{name: "percent variables expand in URLs and credentials", env: map[string]string{"FEED_PATH": "a", "FEED_USER": "user1", "FEED_TOKEN": "pass1"}, files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("feed", "%FEED_PATH%") + `</packageSources>` + oracleCredentials("feed", "%FEED_USER%", "%FEED_TOKEN%")),
		}},
		{name: "undefined and dollar variables stay literal", env: map[string]string{"FEED_TOKEN": "pass1"}, files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("feed", "x") + `</packageSources>` + oracleCredentials("feed", "%UNDEFINED_GUGET_VARIABLE%", "$FEED_TOKEN")),
		}},
		{name: "environment credentials override configuration", env: map[string]string{"NuGetPackageSourceCredentials_feed": "Username=envu;Password=p;w=d"}, files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("feed", "x") + `</packageSources>` + oracleCredentials("feed", "cfgu", "cfgp")),
		}},
		{name: "environment credential names are case-sensitive", env: map[string]string{"NuGetPackageSourceCredentials_FEED": "Username=envu;Password=envp"}, files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("feed", "x") + `</packageSources>` + oracleCredentials("feed", "cfgu", "cfgp")),
		}},
		{name: "malformed environment credentials fall back", env: map[string]string{"NuGetPackageSourceCredentials_feed": "User=envu;Pass=envp"}, files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("feed", "x") + `</packageSources>` + oracleCredentials("feed", "cfgu", "cfgp")),
		}},
		{name: "user credentials apply beneath a repository clear", files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("feed", "x") + `</packageSources>`),
			userConfigPath:      oracleConfig(`<packageSources>` + oracleSource("user", "user") + `</packageSources>` + oracleCredentials("feed", "useru", "userp")),
		}},
		{name: "user disabled sources apply to repository sources", files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("a", "a") + oracleSource("B", "b") + oracleSource("c", "c") + `</packageSources><disabledPackageSources><add key="c" value="false"/><add key="b" value="true"/></disabledPackageSources>`),
			userConfigPath:      oracleConfig(`<disabledPackageSources><add key="a" value="true"/></disabledPackageSources>`),
		}},
		{name: "closest file wins for duplicate names", files: map[string]string{
			"repo/src/nuget.config": oracleConfig(`<packageSources>` + oracleSource("feed", "nested") + `</packageSources>`),
			"repo/nuget.config":     oracleConfig(`<packageSources>` + oracleSource("FEED", "repo") + oracleSource("r", "r") + `</packageSources>`),
			userConfigPath:          oracleConfig(`<packageSources>` + oracleSource("feed", "user") + `</packageSources>`),
		}},
		{name: "one config file per directory", files: map[string]string{
			"repo/nuget.config":        oracleConfig(`<packageSources>` + oracleSource("lower", "lower") + `</packageSources>`),
			"repo/NuGet.config":        oracleConfig(`<packageSources>` + oracleSource("mixed", "mixed") + `</packageSources>`),
			"repo/NuGet.Config":        oracleConfig(`<packageSources>` + oracleSource("upper", "upper") + `</packageSources>`),
			"repo/.nuget/NuGet.Config": oracleConfig(`<packageSources>` + oracleSource("dotnuget", "dotnuget") + `</packageSources>`),
			"repo/src/NuGet.config":    oracleConfig(`<packageSources>` + oracleSource("nestedmixed", "nestedmixed") + `</packageSources>`),
			"repo/src/NuGet.Config":    oracleConfig(`<packageSources>` + oracleSource("nestedupper", "nestedupper") + `</packageSources>`),
		}},
		{name: "additional user configuration directory", files: map[string]string{
			"repo/nuget.config":                     oracleConfig(`<packageSources>` + oracleSource("repo", "repo") + `</packageSources>`),
			"home/.nuget/NuGet/config/extra.config": oracleConfig(`<packageSources>` + oracleSource("extra", "extra") + `</packageSources>`),
		}},
		{name: "machine-wide configuration directory", files: map[string]string{
			"repo/nuget.config":                    oracleConfig(`<packageSources>` + oracleSource("repo", "repo") + `</packageSources>`),
			"machine/NuGet/Config/machine.config":  oracleConfig(`<packageSources>` + oracleSource("machine", "machine") + `</packageSources>`),
			"machine/NuGet/Config/nested/x.config": oracleConfig(`<packageSources>` + oracleSource("nested", "nested") + `</packageSources>`),
		}},
		{name: "nested clear keeps parent credentials and disabled sources", files: map[string]string{
			"repo/nuget.config":     oracleConfig(oracleCredentials("feed", "parentu", "parentp") + `<disabledPackageSources><add key="b" value="true"/></disabledPackageSources>`),
			"repo/src/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("feed", "x") + oracleSource("b", "b") + `</packageSources>`),
		}},
		{name: "encoded and case-sensitive credential names", files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("My Feed", "spaced") + oracleSource("Feed", "cased") + `</packageSources>` +
				`<packageSourceCredentials><My_x0020_Feed><add key="Username" value="spaced"/><add key="ClearTextPassword" value="p"/></My_x0020_Feed><feed><add key="Username" value="lower"/><add key="ClearTextPassword" value="p"/></feed></packageSourceCredentials>`),
		}},
		{name: "credentials limited to other authentication types", files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("feed", "x") + `</packageSources><packageSourceCredentials><feed><add key="Username" value="u"/><add key="ClearTextPassword" value="p"/><add key="ValidAuthenticationTypes" value="negotiate"/></feed></packageSourceCredentials>`),
		}},
		{name: "prefix patterns without a dot", mapping: true, files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("a", "a") + oracleSource("b", "b") + `</packageSources><packageSourceMapping><packageSource key="a"><package pattern="Oracle*"/></packageSource><packageSource key="b"><package pattern="*"/></packageSource></packageSourceMapping>`),
		}},
		{name: "closer mapping entry replaces the whole entry", mapping: true, files: map[string]string{
			"repo/src/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("a", "a") + oracleSource("b", "b") + `</packageSources><packageSourceMapping><packageSource key="a"><package pattern="Other.*"/></packageSource><packageSource key="b"><package pattern="*"/></packageSource></packageSourceMapping>`),
			"repo/nuget.config":     oracleConfig(`<packageSourceMapping><packageSource key="a"><package pattern="Oracle.*"/></packageSource></packageSourceMapping>`),
		}},
		{name: "user mapping applies beneath a repository clear", mapping: true, files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("a", "a") + oracleSource("b", "b") + `</packageSources><packageSourceMapping><packageSource key="b"><package pattern="*"/></packageSource></packageSourceMapping>`),
			userConfigPath:      oracleConfig(`<packageSourceMapping><packageSource key="a"><package pattern="Oracle.Probe"/></packageSource></packageSourceMapping>`),
		}},
		{name: "RestoreSources replaces configured sources", files: map[string]string{
			"repo/nuget.config":          oracleConfig(`<packageSources><clear/>` + oracleSource("cfg", "cfg") + oracleSource("other", "other") + `</packageSources>` + oracleCredentials("cfg", "cu", "cp")),
			"repo/Directory.Build.props": `<Project><PropertyGroup><RestoreSources>{{F}}/props/index.json;{{F}}/cfg/index.json</RestoreSources></PropertyGroup></Project>`,
		}},
		{name: "RestoreSources Clear empties sources", files: map[string]string{
			"repo/nuget.config":          oracleConfig(`<packageSources><clear/>` + oracleSource("cfg", "cfg") + `</packageSources>`),
			"repo/Directory.Build.props": `<Project><PropertyGroup><RestoreSources>Clear</RestoreSources></PropertyGroup></Project>`,
		}},
		{name: "DOTNET_CLI_HOME selects the user configuration", env: map[string]string{"DOTNET_CLI_HOME": "{{BASE}}/cli"}, files: map[string]string{
			"repo/nuget.config":             oracleConfig(`<packageSources>` + oracleSource("repo", "repo") + `</packageSources>`),
			userConfigPath:                  oracleConfig(`<packageSources>` + oracleSource("fromhome", "fromhome") + `</packageSources>`),
			"cli/.nuget/NuGet/NuGet.Config": oracleConfig(`<packageSources>` + oracleSource("fromcli", "fromcli") + `</packageSources>`),
		}},
		{name: "NuGetDefaults sources keep their own settings", files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("repo", "repo") + `</packageSources><disabledPackageSources><add key="defb" value="true"/></disabledPackageSources>` + oracleCredentials("defa", "repou", "repop")),
			"machine/NuGet/NuGetDefaults.config": oracleConfig(`<packageSources>` + oracleSource("defa", "defa") + oracleSource("defb", "defb") + oracleSource("defc", "defc") + oracleSource("repo", "dupname") +
				`</packageSources><disabledPackageSources><add key="DEFC" value="true"/></disabledPackageSources>` + oracleCredentials("defb", "defu", "defp")),
		}},
		{name: "additional user configuration names and priority", files: map[string]string{
			"repo/nuget.config":                     oracleConfig(`<packageSources>` + oracleSource("feed", "x") + `</packageSources>`),
			"home/.nuget/NuGet/config/b.config":     oracleConfig(oracleCredentials("feed", "fromB", "p")),
			"home/.nuget/NuGet/config/a.config":     oracleConfig(oracleCredentials("feed", "fromA", "p")),
			"home/.nuget/NuGet/config/nuget.config": oracleConfig(`<packageSources>` + oracleSource("lowername", "lowername") + `</packageSources>`),
			"home/.nuget/NuGet/config/NuGet.Config": oracleConfig(`<packageSources>` + oracleSource("exactname", "exactname") + `</packageSources>`),
			"home/.nuget/NuGet/config/upper.CONFIG": oracleConfig(`<packageSources>` + oracleSource("upperext", "upperext") + `</packageSources>`),
			"home/.nuget/NuGet/config/cap.Config":   oracleConfig(`<packageSources>` + oracleSource("capext", "capext") + `</packageSources>`),
		}},
		{name: "empty RestoreSources keeps configured sources", files: map[string]string{
			"repo/nuget.config":          oracleConfig(`<packageSources><clear/>` + oracleSource("cfg", "cfg") + `</packageSources>`),
			"repo/Directory.Build.props": `<Project><PropertyGroup><RestoreSources> ; </RestoreSources></PropertyGroup></Project>`,
		}},
		{name: "additional project sources join configured sources", files: map[string]string{
			"repo/nuget.config":          oracleConfig(`<packageSources><clear/>` + oracleSource("cfg", "cfg") + oracleSource("off", "off") + `</packageSources><disabledPackageSources><add key="off" value="true"/></disabledPackageSources>` + oracleCredentials("off", "offu", "offp")),
			"repo/Directory.Build.props": `<Project><PropertyGroup><RestoreAdditionalProjectSources>{{F}}/zeta/index.json;{{F}}/off/index.json;http:/127.0.0.1:{{PORT}}/collapsed/index.json</RestoreAdditionalProjectSources></PropertyGroup></Project>`,
		}},
		{name: "credentials without a username are invalid", files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("feed", "x") + `</packageSources><packageSourceCredentials><feed><add key="ClearTextPassword" value="p"/></feed></packageSourceCredentials>`),
		}},
		{name: "clear inside credentials is invalid", files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("feed", "x") + `</packageSources><packageSourceCredentials><clear/></packageSourceCredentials>`),
		}},
		// dotnet encrypts the password with DPAPI for the current user.
		{name: "encrypted password", goos: "windows", files: map[string]string{
			"repo/nuget.config": oracleConfig(`<packageSources><clear/>` + oracleSource("feed", "x") + `</packageSources>`),
		}, setup: [][]string{{"nuget", "update", "source", "feed", "--username", "encu", "--password", "enc;p=w", "--configfile", "repo/nuget.config"}}, setupWrote: `key="Password"`},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if test.goos != "" && test.goos != runtime.GOOS {
				t.Skipf("runs on %s only", test.goos)
			}
			base := t.TempDir()
			feed := &recordingFeed{credentials: map[string]string{}, probed: map[string]bool{}}
			server := httptest.NewServer(feed)
			defer server.Close()
			files := map[string]string{"repo/src/App/App.csproj": oracleProject, userConfigPath: empty}
			for path, content := range test.files {
				files[path] = content
			}
			for path, content := range files {
				content = strings.NewReplacer("{{F}}", server.URL, "{{PORT}}", serverPort(server.URL)).Replace(content)
				mustWriteFile(t, filepath.Join(base, filepath.FromSlash(path)), content)
			}
			env := isolatedNuGetEnv(base)
			for name, value := range test.env {
				env[name] = strings.ReplaceAll(value, "{{BASE}}", base)
			}
			project := filepath.Join(base, "repo", "src", "App", "App.csproj")
			for _, args := range test.setup {
				if output, err := runDotnet(t, env, base, args...); err != nil {
					t.Fatalf("dotnet %s: %v\n%s", strings.Join(args, " "), err, output)
				}
			}
			if test.setupWrote != "" {
				if data, err := os.ReadFile(filepath.Join(base, "repo", "nuget.config")); err != nil || !strings.Contains(string(data), test.setupWrote) {
					t.Fatalf("setup did not write %s: %v\n%s", test.setupWrote, err, data)
				}
			}

			restoreOutput := runOracleRestore(t, project, env)
			restoreRejected := strings.Contains(restoreOutput, "Unable to parse config file")

			for name, value := range env {
				t.Setenv(name, value)
			}
			resolved, err := resolveWorkspaceSources(parseTestProjects(t, project))
			if restoreRejected || err != nil {
				if restoreRejected != (err != nil) {
					t.Fatalf("dotnet rejected configuration: %v; guget error: %v\n%s", restoreRejected, err, restoreOutput)
				}
				return
			}

			feed.mu.Lock()
			defer feed.mu.Unlock()
			wantCredentials := map[string]string{}
			wantProbed := map[string]bool{}
			for _, source := range resolved.Sources {
				path, onFeed := strings.CutPrefix(source.URL, server.URL+"/")
				if !onFeed {
					t.Fatalf("guget resolved a source restore cannot reach in this test: %+v", source)
				}
				name := strings.Split(path, "/")[0]
				// With package source mapping, restore contacts only the
				// sources mapped for the probe.
				if !resolved.Mapping.Allows(source.Name, source.URL, "Oracle.Probe") {
					continue
				}
				wantProbed[name] = true
				wantCredentials[name] = ""
				if source.Username != "" || source.Password != "" {
					wantCredentials[name] = source.Username + ":" + source.Password
				}
			}
			if !reflect.DeepEqual(feed.credentials, wantCredentials) {
				t.Fatalf("restore contacted %v; guget resolved %v\n%s", feed.credentials, wantCredentials, restoreOutput)
			}
			if test.mapping && !reflect.DeepEqual(feed.probed, wantProbed) {
				t.Fatalf("restore probed %v; guget mapping allows %v", sortedKeys(feed.probed), sortedKeys(wantProbed))
			}
		})
	}
}

func runOracleRestore(t *testing.T, project string, env map[string]string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "dotnet", "restore", project, "--disable-parallel", "-nologo")
	command.Dir = filepath.Dir(project)
	command.Env = append(os.Environ(), "DOTNET_NOLOGO=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1")
	for name, value := range env {
		command.Env = append(command.Env, name+"="+value)
	}
	// Restore is expected to fail: the feed has no packages. Only the requests
	// and configuration errors matter.
	output, _ := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("dotnet restore timed out:\n%s", output)
	}
	return string(output)
}

func serverPort(url string) string {
	return url[strings.LastIndex(url, ":")+1:]
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
