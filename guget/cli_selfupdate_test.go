package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/selfupdate"
)

func TestSelfUpdateCLI(t *testing.T) {
	for _, kind := range []string{"check", "current", "install", "bad checksum", "lookup failure", "network failure", "dev", "help", "bad flags", "positional"} {
		t.Run(kind, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			r := cliRuntime{stdout: &stdout, stderr: &stderr}
			var archive bytes.Buffer
			ext, binary := ".tar.gz", "guget"
			if runtime.GOOS == "windows" {
				ext, binary = ".zip", "guget.exe"
				w := zip.NewWriter(&archive)
				f, err := w.Create(binary)
				if err != nil {
					t.Fatal(err)
				}
				io.WriteString(f, "new executable")
				w.Close()
			} else {
				g := gzip.NewWriter(&archive)
				w := tar.NewWriter(g)
				w.WriteHeader(&tar.Header{Name: binary, Mode: 0o755, Size: int64(len("new executable"))})
				io.WriteString(w, "new executable")
				w.Close()
				g.Close()
			}
			name := fmt.Sprintf("guget_1.3.0_%s_%s%s", runtime.GOOS, runtime.GOARCH, ext)
			current := "1.2.0"
			if kind == "current" {
				current = "1.3.0"
			}
			if kind == "dev" {
				current = "dev"
			}
			requests := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				if kind == "network failure" {
					return nil, errors.New("offline")
				}
				body := fmt.Sprintf(`{"tag_name":"v1.3.0","assets":[{"name":%q},{"name":"checksums.txt"}]}`, name)
				switch {
				case strings.HasSuffix(req.URL.Path, "/checksums.txt"):
					sum := sha256.Sum256(archive.Bytes())
					if kind == "bad checksum" {
						sum[0]++
					}
					body = fmt.Sprintf("%x  %s\n", sum, name)
				case strings.HasSuffix(req.URL.Path, ext):
					body = archive.String()
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			target := filepath.Join(t.TempDir(), binary)
			if err := os.WriteFile(target, []byte("original"), 0o755); err != nil {
				t.Fatal(err)
			}
			lookedUp := false
			executable := func() (string, error) {
				lookedUp = true
				if kind == "lookup failure" {
					return "", errors.New("no executable")
				}
				return target, nil
			}
			args := []string{}
			wantCode := ExitSuccess
			switch kind {
			case "check":
				args = []string{"--check"}
			case "help":
				args = []string{"--help"}
			case "bad flags":
				args = []string{"--force"}
				wantCode = ExitUsage
			case "positional":
				args = []string{"guget"}
				wantCode = ExitUsage
			case "dev":
				wantCode = ExitRefused
			case "network failure":
				wantCode = ExitSource
			case "bad checksum", "lookup failure":
				wantCode = ExitWrite
			}
			code := r.runSelfUpdate(context.Background(), args, selfupdate.New(current, client), executable)
			if code != wantCode {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			data, _ := os.ReadFile(target)
			want := "original"
			if kind == "install" {
				want = "new executable"
			}
			if string(data) != want {
				t.Fatalf("binary=%q", data)
			}
			switch kind {
			case "check", "current", "help", "bad flags", "positional", "dev", "network failure":
				if lookedUp {
					t.Fatal("unnecessary executable lookup")
				}
			}
			if kind == "check" && !strings.Contains(stdout.String(), "1.3.0 available") {
				t.Fatalf("stdout=%q", stdout.String())
			}
			if kind == "current" && !strings.Contains(stdout.String(), "already current") {
				t.Fatalf("stdout=%q", stdout.String())
			}
			if kind == "install" && !strings.Contains(stdout.String(), "Updated guget to 1.3.0") {
				t.Fatalf("stdout=%q", stdout.String())
			}
			if kind == "dev" || kind == "help" || kind == "bad flags" || kind == "positional" {
				if requests != 0 {
					t.Fatal("unexpected network request")
				}
			}
			if code == ExitSuccess && stderr.Len() != 0 {
				t.Fatalf("success diagnostics=%q", stderr.String())
			}
		})
	}
}

func TestSelfUpdateDispatchHelpCompletionAndDevelopmentBuild(t *testing.T) {
	old := version
	version = "dev"
	t.Cleanup(func() { version = old })
	code, stdout, stderr := runCLIForTest(t, "self-update", "--check")
	if code != ExitRefused || stdout != "" || !strings.Contains(stderr, "release build") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, args := range [][]string{{"help"}, {"help", "self-update"}, {"self-update", "--help"}, {"completion", "bash"}, {"completion", "zsh"}, {"completion", "fish"}} {
		code, stdout, stderr := runCLIForTest(t, args...)
		if code != ExitSuccess || !strings.Contains(stdout, "self-update") || stderr != "" {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}
