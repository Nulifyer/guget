package main

import (
	"strings"
	"testing"
)

func TestRedactURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://user:secret@feed.example/v3/index.json": "https://user:***@feed.example/v3/index.json",
		"https://token@feed.example/index.json":          "https://***@feed.example/index.json",
		"https://u:p@ss@feed.example/index.json":         "https://u:***@feed.example/index.json",
		"https://feed.example/index.json":                "https://feed.example/index.json",
		"https://feed.example/path@v2/index.json":        "https://feed.example/path@v2/index.json",
		`Get "https://a:b@x.test/q": dial failed`:        `Get "https://a:***@x.test/q": dial failed`,
		`/local/feed`:                    `/local/feed`,
		"https://user:pa'ss@host/":       "https://user:***@host/",
		"https://tok'secret@host/":       "https://***@host/",
		"https://:secret@host/":          "https://***@host/",
		"https://@host/":                 "https://***@host/",
		"https://user:secret@host":       "https://user:***@host",
		"https://user:secret@host?x=1":   "https://user:***@host?x=1",
		"HTTPS://user:secret@host/":      "HTTPS://user:***@host/",
		"git+ssh://user:secret@host/":    "git+ssh://user:***@host/",
		"https://user%3Asecret@host/":    "https://***@host/",
		"https://host/?next=https://a@b": "https://host/?next=https://***@b",
		"no scheme ://user:secret@host":  "no scheme ://user:secret@host",
	} {
		if got := redactCredentials(raw); got != want {
			t.Errorf("redactCredentials(%q) = %q, want %q", raw, got, want)
		}
		if strings.Contains(raw, " ") {
			continue
		}
		if got := redactURL(raw); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestRedactURLHidesEveryUserinfoCharacter covers the RFC 3986 userinfo
// characters: unreserved, percent-encoded, sub-delims, and ':' and '@' in
// the password, plus characters a hand-written URL may carry anyway.
func TestRedactURLHidesEveryUserinfoCharacter(t *testing.T) {
	characters := []string{"-", ".", "_", "~", "%20", "%40", "!", "$", "&", "'", "(", ")", "*", "+", ",", ";", "=", `"`, "<", ">", "[", "]", "{", "}", "|", "\\", "^", "`", "é"}
	for _, c := range characters {
		secret := "pa" + c + "ss"
		for _, test := range []struct{ raw, want string }{
			{"https://us" + c + "er:" + secret + "@host.test/index.json", "https://us" + c + "er:***@host.test/index.json"},
			{"https://us" + c + "er:" + secret + ":" + secret + "@host.test/index.json", "https://us" + c + "er:***@host.test/index.json"},
			{"https://us" + c + "er:" + secret + "@" + secret + "@host.test/index.json", "https://us" + c + "er:***@host.test/index.json"},
			{"https://to" + c + secret + "@host.test/index.json", "https://***@host.test/index.json"},
		} {
			if got := redactURL(test.raw); got != test.want {
				t.Errorf("redactURL(%q) = %q, want %q", test.raw, got, test.want)
			}
			for _, text := range []string{
				`Get "` + test.raw + `": dial tcp: refused`,
				`source '` + test.raw + `' failed`,
				"(" + test.raw + ")\nnext line",
				test.raw + "\tcolumn",
			} {
				got := redactCredentials(text)
				if strings.Contains(got, secret) || !strings.Contains(got, test.want) {
					t.Errorf("redactCredentials(%q) = %q, want it to contain %q", text, got, test.want)
				}
			}
		}
	}
}

func TestSourcesOutputRedactsQuotedCredentials(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, root+"/NuGet.Config", `<configuration><packageSources><clear/>
		<add key="quoted" value="https://user:pa&apos;ss@feed.test/index.json"/>
		<add key="token" value="https://tok&apos;secret@other.test/index.json"/></packageSources></configuration>`)
	writeCLIProject(t, root, "App", minimalProject)
	for _, format := range []string{"json", "jsonl", "tsv", "table", "{{.url}}"} {
		code, stdout, stderr := runHermetic(t, "sources", "--project", root, "--format", format)
		if code != ExitSuccess || strings.Contains(stdout, "pa'ss") || strings.Contains(stdout, "secret") || strings.Contains(stderr, "secret") {
			t.Fatalf("%s: code=%d stdout=%s stderr=%s", format, code, stdout, stderr)
		}
		if !strings.Contains(stdout, "https://user:***@feed.test") || !strings.Contains(stdout, "https://***@other.test") {
			t.Fatalf("%s: redacted URLs missing:\n%s", format, stdout)
		}
	}
}
