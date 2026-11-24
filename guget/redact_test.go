package main

import "testing"

func TestRedactURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://user:secret@feed.example/v3/index.json": "https://user:***@feed.example/v3/index.json",
		"https://token@feed.example/index.json":          "https://***@feed.example/index.json",
		"https://u:p@ss@feed.example/index.json":         "https://u:***@feed.example/index.json",
		"https://feed.example/index.json":                "https://feed.example/index.json",
		"https://feed.example/path@v2/index.json":        "https://feed.example/path@v2/index.json",
		`Get "https://a:b@x.test/q": dial failed`:        `Get "https://a:***@x.test/q": dial failed`,
		`/local/feed`: `/local/feed`,
	} {
		if got := redactCredentials(raw); got != want {
			t.Errorf("redact(%q) = %q, want %q", raw, got, want)
		}
	}
}
