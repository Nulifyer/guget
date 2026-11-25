package nugetconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// fixture is an isolated NuGet environment: a repository with a project
// directory, a user home, and a machine-wide configuration directory.
type fixture struct {
	t       *testing.T
	repo    string
	project string
	home    string
	machine string
	vars    map[string]string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{
		t:       t,
		repo:    filepath.Join(base, "repo"),
		project: filepath.Join(base, "repo", "src", "App"),
		home:    filepath.Join(base, "home"),
		machine: filepath.Join(base, "machine"),
	}
	f.vars = map[string]string{"HOME": f.home, "NUGET_COMMON_APPLICATION_DATA": f.machine}
	for _, dir := range []string{f.project, f.home, f.machine} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// An empty user config keeps NuGet's implicit nuget.org default out of
	// tests that are not about it.
	f.write(f.userConfig(), "")
	return f
}

// env simulates Linux, including its case-sensitive file system rule, so tests
// probe the same names on every host.
func (f *fixture) env() Environment {
	return Environment{GOOS: "linux", CaseInsensitive: func() bool { return false }, LookupEnv: func(name string) (string, bool) {
		value, ok := f.vars[name]
		return value, ok
	}}
}

func (f *fixture) userConfig() string {
	return filepath.Join(f.home, ".nuget", "NuGet", "NuGet.Config")
}

func (f *fixture) write(path, body string) string {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	content := `<?xml version="1.0" encoding="utf-8"?>` + "\n<configuration>" + body + "</configuration>\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func (f *fixture) load() *Settings {
	f.t.Helper()
	settings, err := Load(f.project, f.env())
	if err != nil {
		f.t.Fatalf("Load: %v", err)
	}
	return settings
}

func (f *fixture) loadError() error {
	f.t.Helper()
	_, err := Load(f.project, f.env())
	if err == nil {
		f.t.Fatal("Load succeeded; want a configuration error")
	}
	return err
}

func sources(entries ...string) string {
	return "<packageSources>" + strings.Join(entries, "") + "</packageSources>"
}

func add(key, value string) string {
	return `<add key="` + key + `" value="` + value + `" />`
}

func credentials(source string, entries ...string) string {
	return "<packageSourceCredentials><" + source + ">" + strings.Join(entries, "") + "</" + source + "></packageSourceCredentials>"
}

func sourceNamed(t *testing.T, settings *Settings, name string) Source {
	t.Helper()
	for _, source := range settings.Sources {
		if source.Name == name {
			return source
		}
	}
	t.Fatalf("source %q not found in %+v", name, settings.Sources)
	return Source{}
}

func sourceNames(settings *Settings) []string {
	var names []string
	for _, source := range settings.Sources {
		names = append(names, source.Name)
	}
	return names
}

func TestLoadExpandsPercentVariablesOnly(t *testing.T) {
	f := newFixture(t)
	f.vars["FEED_PATH"], f.vars["NUGET_USER"], f.vars["TOKEN"] = "group", "ci-user", "secret"
	f.write(filepath.Join(f.repo, "nuget.config"),
		sources("<clear/>", add("feed", "https://feed.test/%FEED_PATH%/index.json"), add("other", "https://other.test/index.json"))+
			"<packageSourceCredentials>"+
			`<feed><add key="Username" value="%NUGET_USER%"/><add key="ClearTextPassword" value="%TOKEN%"/></feed>`+
			`<other><add key="Username" value="%UNDEFINED_VARIABLE%"/><add key="ClearTextPassword" value="$TOKEN"/></other>`+
			"</packageSourceCredentials>")

	settings := f.load()
	if got, want := sourceNamed(t, settings, "feed"), (Source{Name: "feed", URL: "https://feed.test/group/index.json", Username: "ci-user", Password: "secret"}); got != want {
		t.Fatalf("feed = %+v, want %+v", got, want)
	}
	if got := sourceNamed(t, settings, "other"); got.Username != "%UNDEFINED_VARIABLE%" || got.Password != "$TOKEN" {
		t.Fatalf("undefined and $-style variables must stay literal, got %+v", got)
	}
}

func TestExpandEnvMatchesDotNet(t *testing.T) {
	env := Environment{LookupEnv: func(name string) (string, bool) {
		value, ok := map[string]string{"A": "1", "EMPTY": ""}[name]
		return value, ok
	}}
	for input, want := range map[string]string{
		"%A%":          "1",
		"x%A%y%A%z":    "x1y1z",
		"%MISSING%%A%": "%MISSING%1",
		"%MISSING%A%":  "%MISSING1",
		"100%":         "100%",
		"%%":           "%%",
		"%EMPTY%x":     "x",
		"$A":           "$A",
	} {
		if got := expandEnv(input, env); got != want {
			t.Errorf("expandEnv(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLoadEnvironmentCredentials(t *testing.T) {
	for _, test := range []struct {
		name, variable, value  string
		wantUser, wantPassword string
	}{
		{"overrides configuration", "NuGetPackageSourceCredentials_feed", "Username=env;Password=envpass", "env", "envpass"},
		{"password keeps semicolons", "NuGetPackageSourceCredentials_feed", "Username=env;Password=p;w=d;ValidAuthenticationTypes=basic", "env", "p;w=d"},
		{"name is case-sensitive", "NuGetPackageSourceCredentials_FEED", "Username=env;Password=envpass", "cfg", "cfgpass"},
		{"malformed value falls back", "NuGetPackageSourceCredentials_feed", "User=env;Pass=envpass", "cfg", "cfgpass"},
		{"basic not allowed", "NuGetPackageSourceCredentials_feed", "Username=env;Password=envpass;ValidAuthenticationTypes=negotiate", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			f.vars[test.variable] = test.value
			f.write(filepath.Join(f.repo, "nuget.config"), sources("<clear/>", add("feed", "https://feed.test/index.json"))+
				credentials("feed", add("Username", "cfg"), add("ClearTextPassword", "cfgpass")))
			got := sourceNamed(t, f.load(), "feed")
			if got.Username != test.wantUser || got.Password != test.wantPassword {
				t.Fatalf("credentials = %q/%q, want %q/%q", got.Username, got.Password, test.wantUser, test.wantPassword)
			}
		})
	}
}

func TestLoadClearOnlyAffectsItsSection(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.repo, "src", "nuget.config"), sources("<clear/>", add("feed", "https://feed.test/index.json"), add("b", "https://b.test/index.json"), add("c", "https://c.test/index.json")))
	f.write(filepath.Join(f.repo, "nuget.config"),
		`<disabledPackageSources><add key="b" value="true"/></disabledPackageSources>`+
			`<packageSourceMapping><packageSource key="feed"><package pattern="Contoso.*"/></packageSource><packageSource key="c"><package pattern="*"/></packageSource></packageSourceMapping>`)
	f.write(f.userConfig(), sources(add("user", "https://user.test/index.json"))+
		credentials("feed", add("Username", "u"), add("ClearTextPassword", "p")))

	settings := f.load()
	if got, want := sourceNames(settings), []string{"feed", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	if got := sourceNamed(t, settings, "feed"); got.Username != "u" || got.Password != "p" {
		t.Fatalf("user-level credentials must apply to a source declared after <clear/>, got %+v", got)
	}
	if got := settings.Mapping.SourcesFor("Contoso.Core"); !reflect.DeepEqual(got, []string{"feed"}) {
		t.Fatalf("parent mapping must survive a source <clear/>, got %v", got)
	}
}

func TestLoadClearInsideSectionDropsLowerPriorityEntries(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.repo, "nuget.config"), sources(add("dropped", "https://dropped.test/index.json"), "<clear/>", add("kept", "https://kept.test/index.json"))+
		`<disabledPackageSources><clear/></disabledPackageSources>`+
		`<packageSourceMapping><clear/></packageSourceMapping>`)
	f.write(f.userConfig(), sources(add("user", "https://user.test/index.json"))+
		`<disabledPackageSources><add key="kept" value="true"/></disabledPackageSources>`+
		`<packageSourceMapping><packageSource key="user"><package pattern="*"/></packageSource></packageSourceMapping>`)

	settings := f.load()
	if got, want := sourceNames(settings), []string{"kept"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	if settings.Mapping != nil {
		t.Fatalf("mapping <clear/> must drop inherited entries, got %+v", settings.Mapping)
	}
}

func TestLoadDisabledSources(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.repo, "nuget.config"), sources("<clear/>", add("a", "https://a.test/index.json"), add("B", "https://b.test/index.json"), add("c", "https://c.test/index.json"))+
		`<disabledPackageSources><add key="c" value="false"/><add key="b" value="true"/></disabledPackageSources>`)
	f.write(f.userConfig(), `<disabledPackageSources><add key="a" value="true"/></disabledPackageSources>`)

	// User-level entries apply to repository sources; any value disables; the
	// key is compared case-sensitively.
	if got, want := sourceNames(f.load()), []string{"B"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
}

func TestLoadClosestFileWinsForDuplicateNames(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.repo, "src", "nuget.config"), sources(add("Feed", "https://nested.test/index.json"), add("feed", "https://same-file.test/index.json")))
	f.write(filepath.Join(f.repo, "nuget.config"), sources(add("feed", "https://repo.test/index.json"), add("repo", "https://repo-only.test/index.json")))
	f.write(f.userConfig(), sources(add("user", "https://user.test/index.json")))

	settings := f.load()
	want := []Source{
		{Name: "Feed", URL: "https://nested.test/index.json"},
		{Name: "repo", URL: "https://repo-only.test/index.json"},
		{Name: "user", URL: "https://user.test/index.json"},
	}
	if !reflect.DeepEqual(settings.Sources, want) {
		t.Fatalf("sources = %+v, want %+v", settings.Sources, want)
	}
}

func TestLoadUsesFirstConfigNamePerDirectory(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.repo, ".nuget", "NuGet.Config"), sources(add("solution-folder", "https://ignored.test/index.json")))
	f.write(filepath.Join(f.repo, "NuGet.Config"), sources(add("upper", "https://upper.test/index.json")))
	if got := sourceNames(f.load()); !reflect.DeepEqual(got, []string{"upper"}) {
		t.Fatalf(".nuget/NuGet.Config must not be read, got %v", got)
	}
	if _, err := os.Stat(filepath.Join(f.repo, "NuGet.config")); err == nil {
		return // case-insensitive filesystem: the names are one file
	}
	f.write(filepath.Join(f.repo, "NuGet.config"), sources(add("mixed", "https://mixed.test/index.json")))
	if got := sourceNames(f.load()); !reflect.DeepEqual(got, []string{"mixed"}) {
		t.Fatalf("NuGet.config must win over NuGet.Config, got %v", got)
	}
	f.write(filepath.Join(f.repo, "nuget.config"), sources(add("lower", "https://lower.test/index.json")))
	if got := sourceNames(f.load()); !reflect.DeepEqual(got, []string{"lower"}) {
		t.Fatalf("nuget.config must win over other spellings, got %v", got)
	}
}

func TestLoadUserAndMachineConfigurationLocations(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.home, ".nuget", "NuGet", "config", "b.config"), sources(add("extra-b", "https://b.test/index.json")))
	f.write(filepath.Join(f.home, ".nuget", "NuGet", "config", "a.config"), sources(add("extra-a", "https://a.test/index.json")))
	f.write(filepath.Join(f.machine, "NuGet", "Config", "machine.config"), sources(add("machine", "https://machine.test/index.json")))
	f.write(filepath.Join(f.machine, "NuGet", "Config", "nested", "skip.config"), sources(add("nested", "https://nested.test/index.json")))

	if got, want := sourceNames(f.load()), []string{"extra-a", "extra-b", "machine"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}

	f.vars["DOTNET_CLI_HOME"] = filepath.Join(f.home, "cli")
	f.write(filepath.Join(f.home, "cli", ".nuget", "NuGet", "NuGet.Config"), sources(add("cli-home", "https://cli.test/index.json")))
	if got := sourceNames(f.load()); len(got) == 0 || got[0] != "cli-home" {
		t.Fatalf("DOTNET_CLI_HOME must select the user config, got %v", got)
	}
}

func TestLoadMissingUserConfigImpliesNuGetOrg(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(f.userConfig()); err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Join(f.repo, "nuget.config"), sources(add("repo", "https://repo.test/index.json")))

	settings := f.load()
	if got, want := sourceNames(settings), []string{"repo", "nuget.org"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	for _, file := range settings.Files {
		if file.Path == f.userConfig() {
			t.Fatal("the implied user config must not be reported as a consumed file")
		}
	}
	if _, err := os.Stat(f.userConfig()); err == nil {
		t.Fatal("Load must not create the user config")
	}
}

func TestLoadNuGetDefaults(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.machine, "NuGet", "NuGetDefaults.config"), sources(
		add("defa", "https://defa.test/index.json"),
		add("defb", "https://defb.test/index.json"),
		add("defc", "https://defc.test/index.json"),
		add("repo", "https://dup-name.test/index.json"),
		add("dup-url", "https://repo.test/index.json"))+
		`<disabledPackageSources><add key="DEFC" value="true"/></disabledPackageSources>`)
	f.write(filepath.Join(f.machine, "NuGet", "Config", "machine.config"), sources(add("machine", "https://machine.test/index.json")))
	f.write(filepath.Join(f.repo, "nuget.config"), sources(add("repo", "https://repo.test/index.json"))+
		`<disabledPackageSources><add key="defb" value="true"/></disabledPackageSources>`+
		credentials("defa", add("Username", "repo-user"), add("ClearTextPassword", "p")))

	// Defaults survive <clear/>-free merging, go before machine-wide sources,
	// and only the defaults file can disable them or supply credentials.
	settings := f.load()
	if got, want := sourceNames(settings), []string{"repo", "defa", "defb", "machine"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	if got := sourceNamed(t, settings, "defa"); got.Username != "" {
		t.Fatalf("repository credentials must not apply to a defaults source, got %+v", got)
	}

	f.write(filepath.Join(f.repo, "nuget.config"), sources("<clear/>", add("repo", "https://repo.test/index.json")))
	if got, want := sourceNames(f.load()), []string{"repo", "defa", "defb"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after <clear/>: sources = %v, want %v", got, want)
	}
}

func TestLoadAdditionalConfigurationFiles(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.repo, "nuget.config"), sources("<clear/>", add("feed", "https://feed.test/index.json")))
	dir := filepath.Join(f.home, ".nuget", "NuGet", "config")
	f.write(filepath.Join(dir, "b.config"), credentials("feed", add("Username", "from-b"), add("ClearTextPassword", "p")))
	f.write(filepath.Join(dir, "a.config"), credentials("feed", add("Username", "from-a"), add("ClearTextPassword", "p")))
	if got := sourceNamed(t, f.load(), "feed").Username; got != "from-a" {
		t.Fatalf("earlier file name must take priority, got %q", got)
	}

	f.write(filepath.Join(f.repo, "nuget.config"), sources(add("repo", "https://repo.test/index.json")))
	f.write(filepath.Join(dir, "cap.Config"), sources(add("cap", "https://cap.test/index.json")))
	f.write(filepath.Join(dir, "upper.CONFIG"), sources(add("upper", "https://upper.test/index.json")))
	f.write(filepath.Join(dir, "NuGet.Config"), sources(add("excluded", "https://excluded.test/index.json")))
	want := []string{"repo", "cap"}
	// On a case-sensitive filesystem nuget.config is a distinct file that
	// Linux NuGet reads; elsewhere the name is NuGet.Config itself.
	if _, err := os.Stat(filepath.Join(dir, "nuget.config")); err != nil {
		f.write(filepath.Join(dir, "nuget.config"), sources(add("lower", "https://lower.test/index.json")))
		want = append(want, "lower")
	}
	if got := sourceNames(f.load()); !reflect.DeepEqual(got, want) {
		t.Fatalf("Linux reads *.config and *.Config except NuGet.Config: sources = %v, want %v", got, want)
	}
}

func TestLoadEmptyCLIHomeIsRelativeToWorkingDirectory(t *testing.T) {
	f := newFixture(t)
	f.vars["DOTNET_CLI_HOME"] = ""
	t.Chdir(f.repo)
	f.write(filepath.Join(f.repo, ".nuget", "NuGet", "NuGet.Config"), sources(add("cwd", "https://cwd.test/index.json")))
	if got := sourceNames(f.load()); !reflect.DeepEqual(got, []string{"cwd"}) {
		t.Fatalf("sources = %v, want the user config under the working directory", got)
	}
}

func TestLoadSourceLocations(t *testing.T) {
	f := newFixture(t)
	f.vars["FEEDS"] = filepath.Join(f.home, "feeds")
	f.write(filepath.Join(f.repo, "nuget.config"), sources("<clear/>",
		add("relative", "./packages"),
		add("variable", "%FEEDS%/local"),
		add("upper", "HTTPS://Feed.Test/index.json")))
	settings := f.load()
	if got := sourceNamed(t, settings, "relative").URL; got != filepath.Join(f.repo, "packages") {
		t.Fatalf("relative feed = %q, want it resolved from the declaring file", got)
	}
	if got := sourceNamed(t, settings, "variable").URL; got != filepath.Join(f.home, "feeds", "local") {
		t.Fatalf("variable feed = %q", got)
	}
	if upper := sourceNamed(t, settings, "upper"); !upper.IsHTTP() || upper.URL != "HTTPS://Feed.Test/index.json" {
		t.Fatalf("upper-case scheme must stay an HTTP source, got %+v", upper)
	}
}

func TestLoadCredentialsMatchDecodedNamesCaseSensitively(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.repo, "nuget.config"), sources("<clear/>", add("My Feed", "https://spaced.test/index.json"), add("Feed", "https://case.test/index.json"))+
		"<packageSourceCredentials>"+
		`<My_x0020_Feed><add key="Username" value="spaced"/><add key="ClearTextPassword" value="p"/></My_x0020_Feed>`+
		`<feed><add key="Username" value="lower"/><add key="ClearTextPassword" value="p"/></feed>`+
		"</packageSourceCredentials>")
	settings := f.load()
	if got := sourceNamed(t, settings, "My Feed").Username; got != "spaced" {
		t.Fatalf("encoded credential name not matched, username %q", got)
	}
	if got := sourceNamed(t, settings, "Feed").Username; got != "" {
		t.Fatalf("credential names are case-sensitive, got username %q", got)
	}
}

func TestLoadCredentialRestrictions(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.repo, "nuget.config"), sources("<clear/>", add("feed", "https://feed.test/index.json"))+
		credentials("feed", add("Username", "u"), add("ClearTextPassword", "p"), add("ValidAuthenticationTypes", "Negotiate, NTLM")))
	settings := f.load()
	if got := sourceNamed(t, settings, "feed"); got.Username != "" || got.Password != "" {
		t.Fatalf("credentials limited to other schemes must not be used for Basic, got %+v", got)
	}
	if len(settings.Warnings) != 1 || strings.Contains(settings.Warnings[0], "p\"") {
		t.Fatalf("warnings = %q", settings.Warnings)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	for name, body := range map[string]string{
		"password without username": credentials("feed", add("ClearTextPassword", "secret-value")),
		"clear inside credentials":  "<packageSourceCredentials><clear/></packageSourceCredentials>",
		"both password kinds":       credentials("feed", add("Username", "u"), add("Password", "AAAA"), add("ClearTextPassword", "secret-value")),
		"source without value":      `<packageSources><add key="feed"/></packageSources>`,
		"mapping without patterns":  `<packageSourceMapping><packageSource key="feed"/></packageSourceMapping>`,
		"duplicate mapping key":     `<packageSourceMapping><packageSource key="a"><package pattern="*"/></packageSource><packageSource key="A"><package pattern="x"/></packageSource></packageSourceMapping>`,
		"malformed XML":             `<packageSources><add key="feed" value="secret-value">`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			path := f.write(filepath.Join(f.repo, "nuget.config"), body)
			err := f.loadError()
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error %q must name %s", err, path)
			}
			if strings.Contains(err.Error(), "secret-value") {
				t.Fatalf("error %q leaks configuration content", err)
			}
		})
	}
}

func TestLoadReportsConsumedFiles(t *testing.T) {
	f := newFixture(t)
	repoConfig := f.write(filepath.Join(f.repo, "nuget.config"), sources(add("repo", "https://repo.test/index.json")))
	settings := f.load()
	var paths []string
	for _, file := range settings.Files {
		paths = append(paths, file.Path)
	}
	if want := []string{repoConfig, f.userConfig()}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("files = %v, want %v", paths, want)
	}
	candidates := CandidatePaths(f.project, f.env())
	for _, want := range []string{filepath.Join(f.project, "NuGet.config"), filepath.Join(f.repo, "nuget.config"), f.userConfig()} {
		found := false
		for _, candidate := range candidates {
			found = found || candidate == want
		}
		if !found {
			t.Errorf("candidate paths are missing %s", want)
		}
	}
}

func TestMappingPatterns(t *testing.T) {
	mapping := &Mapping{}
	mapping.add("prefix", []string{"Contoso*"})
	mapping.add("dotted", []string{"Contoso.Core.*"})
	mapping.add("exact", []string{"Contoso.Core.Tools"})
	mapping.add("all", []string{"*"})
	for id, want := range map[string][]string{
		"Contoso.Core.Tools": {"exact"},
		"Contoso.Core.Other": {"dotted"},
		"ContosoX":           {"prefix"},
		"Fabrikam":           {"all"},
		" contoso.core.x ":   {"dotted"},
	} {
		if got := mapping.SourcesFor(id); !reflect.DeepEqual(got, want) {
			t.Errorf("SourcesFor(%q) = %v, want %v", id, got, want)
		}
	}
	if got := mapping.Patterns("PREFIX"); !reflect.DeepEqual(got, []string{"contoso*"}) {
		t.Fatalf("Patterns = %v", got)
	}

	tied := &Mapping{}
	tied.add("b", []string{"Contoso.*"})
	tied.add("a", []string{"contoso.*", "Other"})
	if got := tied.SourcesFor("Contoso.X"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("equally specific sources must all be returned, got %v", got)
	}
	if got := tied.SourcesFor("Unmapped"); got != nil {
		t.Fatalf("unmatched package must have no source, got %v", got)
	}
}

func TestLoadMappingCloserEntryReplacesWholeEntry(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.repo, "src", "nuget.config"), sources("<clear/>", add("a", "https://a.test/index.json"), add("b", "https://b.test/index.json"))+
		`<packageSourceMapping><packageSource key="a"><package pattern="Other.*"/></packageSource><packageSource key="b"><package pattern="*"/></packageSource></packageSourceMapping>`)
	f.write(filepath.Join(f.repo, "nuget.config"), `<packageSourceMapping><packageSource key="A"><package pattern="Oracle.*"/></packageSource></packageSourceMapping>`)
	if got := f.load().Mapping.SourcesFor("Oracle.Probe"); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("SourcesFor = %v, want [b]", got)
	}
}

func TestProjectSources(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.repo, "nuget.config"), sources("<clear/>", add("cfg", "https://cfg.test/index.json"), add("off", "https://off.test/index.json"))+
		credentials("cfg", add("Username", "u"), add("ClearTextPassword", "p"))+
		credentials("off", add("Username", "u2"), add("ClearTextPassword", "p2"))+
		`<disabledPackageSources><add key="off" value="true"/></disabledPackageSources>`)
	settings := f.load()
	text := func(value string) *string { return &value }

	got, err := settings.ProjectSources(f.project, RestoreProperties{})
	if err != nil || !reflect.DeepEqual(got, settings.Sources) {
		t.Fatalf("without properties: %+v, %v", got, err)
	}

	got, err = settings.ProjectSources(f.project, RestoreProperties{
		Sources:           text(" https:/props.test/index.json ; https://cfg.test/index.json;../feed"),
		AdditionalSources: "https://off.test/index.json;https://add.test/index.json;https://cfg.test/index.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Source{
		{Name: "https://props.test/index.json", URL: "https://props.test/index.json"},
		{Name: "cfg", URL: "https://cfg.test/index.json", Username: "u", Password: "p"},
		{Name: filepath.Join(f.repo, "src", "feed"), URL: filepath.Join(f.repo, "src", "feed")},
		{Name: "https://add.test/index.json", URL: "https://add.test/index.json"},
		{Name: "https://off.test/index.json", URL: "https://off.test/index.json"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sources =\n%+v\nwant\n%+v", got, want)
	}

	for _, empty := range []string{"", " ; "} {
		if got, err := settings.ProjectSources(f.project, RestoreProperties{Sources: text(empty)}); err != nil || !reflect.DeepEqual(got, settings.Sources) {
			t.Fatalf("empty RestoreSources %q must leave configured sources: %+v, %v", empty, got, err)
		}
	}
	feed := filepath.Join(f.home, "feed")
	if got, err := settings.ProjectSources(f.project, RestoreProperties{Sources: text("file:" + filepath.ToSlash(feed))}); err != nil || len(got) != 1 || got[0].URL != feed {
		if runtime.GOOS != "windows" {
			t.Fatalf("file URI: %+v, %v", got, err)
		}
	}
	if got, err := settings.ProjectSources(f.project, RestoreProperties{Sources: text("Clear")}); err != nil || len(got) != 0 {
		t.Fatalf("Clear: %+v, %v", got, err)
	}
	if _, err := settings.ProjectSources(f.project, RestoreProperties{Sources: text("Clear;https://x.test/index.json")}); err == nil {
		t.Fatal("Clear combined with sources must be rejected")
	}
}

func TestLoadCachedKeepsPerRootChainsAndOneRevision(t *testing.T) {
	f := newFixture(t)
	repoConfig := f.write(filepath.Join(f.repo, "NuGet.Config"), sources(add("repo", "https://repo.test/index.json")))
	other := filepath.Join(f.repo, "src", "Other")
	f.write(filepath.Join(other, "NuGet.Config"), sources(add("other", "https://other.test/index.json")))
	cache := NewCache()
	first, err := LoadCached(f.project, f.env(), cache)
	if err != nil {
		t.Fatal(err)
	}
	if uncached := f.load(); !reflect.DeepEqual(first, uncached) {
		t.Fatalf("cached settings differ:\n%+v\n%+v", first, uncached)
	}
	// A change after the first read must not give later loads of the same
	// resolution a second revision; the caller validates the consumed one.
	f.write(repoConfig, sources(add("changed", "https://changed.test/index.json")))
	second, err := LoadCached(other, f.env(), cache)
	if err != nil {
		t.Fatal(err)
	}
	if got := sourceNames(second); !reflect.DeepEqual(got, []string{"other", "repo"}) {
		t.Fatalf("other root sources = %v, want its own chain", got)
	}
	// A case-insensitive filesystem reports the first name NuGet probes,
	// which can differ in case from the name written.
	want, err := os.Stat(repoConfig)
	if err != nil {
		t.Fatal(err)
	}
	digest := func(settings *Settings) [32]byte {
		for _, file := range settings.Files {
			if info, err := os.Stat(file.Path); err == nil && os.SameFile(info, want) {
				return file.Digest
			}
		}
		t.Fatalf("%s not consumed: %+v", repoConfig, settings.Files)
		return [32]byte{}
	}
	if digest(first) != digest(second) {
		t.Fatal("loads sharing a cache consumed different revisions")
	}
	if fresh := f.load(); sourceNamed(t, fresh, "changed").URL == "" {
		t.Fatal("a new load did not see the change")
	}
}
