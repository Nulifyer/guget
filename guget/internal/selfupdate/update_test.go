package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type archiveEntry struct {
	name, data string
	link       bool
}

func makeArchive(t *testing.T, zipped bool, entries ...archiveEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	if zipped {
		w := zip.NewWriter(&b)
		for _, e := range entries {
			h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
			h.SetMode(0o755)
			if e.link {
				h.SetMode(os.ModeSymlink | 0o777)
			}
			f, err := w.CreateHeader(h)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(f, e.data); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		g := gzip.NewWriter(&b)
		w := tar.NewWriter(g)
		for _, e := range entries {
			h := &tar.Header{Name: e.name, Size: int64(len(e.data)), Mode: 0o755, Typeflag: tar.TypeReg}
			if e.link {
				h.Typeflag = tar.TypeSymlink
				h.Linkname = e.data
				h.Size = 0
			}
			if err := w.WriteHeader(h); err != nil {
				t.Fatal(err)
			}
			if !e.link {
				if _, err := io.WriteString(w, e.data); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := g.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

type releaseFixture struct {
	updater   *Updater
	requests  atomic.Int32
	release   string
	archive   []byte
	checksums string
	status    int
}

func newFixture(t *testing.T, current string) *releaseFixture {
	t.Helper()
	f := &releaseFixture{status: http.StatusOK}
	binary, ext := "guget", ".tar.gz"
	if runtime.GOOS == "windows" {
		binary += ".exe"
		ext = ".zip"
	}
	name := fmt.Sprintf("guget_1.3.0_%s_%s%s", runtime.GOOS, runtime.GOARCH, ext)
	f.archive = makeArchive(t, ext == ".zip", archiveEntry{name: "LICENSE", data: "license"}, archiveEntry{name: binary, data: "new binary"})
	f.checksums = fmt.Sprintf("%x  %s\n", sha256.Sum256(f.archive), name)
	doc := map[string]any{"tag_name": "v1.3.0", "assets": []map[string]string{{"name": name}, {"name": "checksums.txt"}}}
	data, _ := json.Marshal(doc)
	f.release = string(data)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			return
		}
		switch {
		case r.URL.Path == "/latest":
			io.WriteString(w, f.release)
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			io.WriteString(w, f.checksums)
		default:
			w.Write(f.archive)
		}
	}))
	t.Cleanup(server.Close)
	f.updater = New(current, server.Client())
	f.updater.releaseURL = server.URL + "/latest"
	f.updater.downloadBase = server.URL + "/download/"
	return f
}

func TestLatestVersionsAndInvalidReleases(t *testing.T) {
	for _, tc := range []struct {
		current   string
		available bool
	}{
		{"1.2.9", true}, {"v1.2.9", true}, {"1.3.0", false}, {"1.3.0+build", false},
		{"1.3.0-rc.1", true}, {"1.10.0", false}, {"2.0.0", false},
	} {
		t.Run(tc.current, func(t *testing.T) {
			f := newFixture(t, tc.current)
			r, err := f.updater.Latest(context.Background())
			if err != nil || r.Version != "1.3.0" || r.Available != tc.available {
				t.Fatalf("release=%+v err=%v", r, err)
			}
		})
	}
	for _, body := range []string{`{`, `{}`, `{"tag_name":"v1.4.0-beta.1"}`, `{"tag_name":"v1.4.0","prerelease":true}`, `{"tag_name":"v1.4.0","draft":true}`, `{"tag_name":"../../bad"}`, `{"tag_name":"v01.4.0"}`} {
		t.Run(body, func(t *testing.T) {
			f := newFixture(t, "1.2.0")
			f.release = body
			if _, err := f.updater.Latest(context.Background()); err == nil {
				t.Fatal("accepted invalid release")
			}
		})
	}
}

func TestDevelopmentAndNetworkFailures(t *testing.T) {
	for _, current := range []string{"dev", "", "garbage", "1.2", "999999999999999999999999.0.0"} {
		f := newFixture(t, current)
		if _, err := f.updater.Latest(context.Background()); err != ErrDevelopment {
			t.Fatalf("current=%q err=%v", current, err)
		}
		if f.requests.Load() != 0 {
			t.Fatal("development build contacted network")
		}
	}
	f := newFixture(t, "1.2.0")
	f.status = http.StatusForbidden
	if _, err := f.updater.Latest(context.Background()); err == nil {
		t.Fatal("HTTP error ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.updater.Latest(ctx); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestInstallVerifiedArchiveAndPreservePermissions(t *testing.T) {
	f := newFixture(t, "1.2.0")
	r, err := f.updater.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "guget")
	if err := os.WriteFile(target, []byte("original"), 0o751); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(target)
	if err := f.updater.Install(context.Background(), r, target); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	after, _ := os.Stat(target)
	if string(data) != "new binary" || before.Mode().Perm() != after.Mode().Perm() {
		t.Fatalf("data=%q mode=%v", data, after.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Fatalf("temporary files left: %v", entries)
	}
}

func TestInstallFailuresPreserveOriginal(t *testing.T) {
	for _, kind := range []string{"checksum", "missing checksum", "duplicate checksum", "malformed archive", "missing asset", "locked", "missing binary", "symlink binary", "duplicate binary", "traversal"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, "1.2.0")
			r, err := f.updater.Latest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "guget")
			if err := os.WriteFile(target, []byte("original"), 0o755); err != nil {
				t.Fatal(err)
			}
			binary := "guget"
			if runtime.GOOS == "windows" {
				binary += ".exe"
			}
			var entry archiveEntry
			switch kind {
			case "checksum":
				f.archive = append(f.archive, 'x')
			case "missing checksum":
				f.checksums = ""
			case "duplicate checksum":
				f.checksums += f.checksums
			case "malformed archive":
				f.archive = []byte("bad archive")
			case "missing asset":
				r.assets = nil
			case "locked":
				if err := os.Mkdir(target+".update-lock", 0o700); err != nil {
					t.Fatal(err)
				}
			case "missing binary":
				entry = archiveEntry{name: "README.md", data: "no binary"}
			case "symlink binary":
				entry = archiveEntry{name: binary, data: "outside", link: true}
			case "duplicate binary":
				f.archive = makeArchive(t, runtime.GOOS == "windows", archiveEntry{name: binary, data: "one"}, archiveEntry{name: binary, data: "two"})
			case "traversal":
				entry = archiveEntry{name: "../" + binary, data: "outside"}
			}
			if entry.name != "" {
				f.archive = makeArchive(t, runtime.GOOS == "windows", entry)
			}
			if kind == "malformed archive" || entry.name != "" || kind == "duplicate binary" {
				fields := strings.Fields(f.checksums)
				f.checksums = fmt.Sprintf("%x  %s\n", sha256.Sum256(f.archive), fields[1])
			}
			if err := f.updater.Install(context.Background(), r, target); err == nil {
				t.Fatal("expected failure")
			}
			data, _ := os.ReadFile(target)
			if string(data) != "original" {
				t.Fatalf("original changed: %q", data)
			}
			entries, _ := os.ReadDir(filepath.Dir(target))
			want := 1
			if kind == "locked" {
				want++
			}
			if len(entries) != want {
				t.Fatalf("temporary files left: %v", entries)
			}
		})
	}
}

func TestInstallThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires Windows privileges")
	}
	f := newFixture(t, "1.2.0")
	r, err := f.updater.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "actual")
	link := filepath.Join(dir, "guget")
	if err := os.WriteFile(target, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := f.updater.Install(context.Background(), r, link); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("installer replaced symlink")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "new binary" {
		t.Fatalf("target=%q", data)
	}
}

func TestArchiveFormats(t *testing.T) {
	for _, zipped := range []bool{false, true} {
		data := makeArchive(t, zipped, archiveEntry{name: "guget", data: "verified"})
		got, err := extractBinary(data, "guget", zipped)
		if err != nil || string(got) != "verified" {
			t.Fatalf("zipped=%v data=%q err=%v", zipped, got, err)
		}
	}
}

// This test runs on all three CI platforms. A copied test executable remains
// running while the parent replaces it, then exits using its original image.
func TestReplaceRunningExecutable(t *testing.T) {
	if os.Getenv("GUGET_TEST_RUNNING_EXECUTABLE") == "1" {
		fmt.Println("ready")
		bufio.NewReader(os.Stdin).ReadString('\n')
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "running.exe")
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, target, "-test.run=^TestReplaceRunningExecutable$")
	cmd.Env = append(os.Environ(), "GUGET_TEST_RUNNING_EXECUTABLE=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); cmd.Wait(); os.Remove(target + ".old") }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "ready" {
		t.Fatalf("child line=%q err=%v stderr=%s", line, err, stderr.String())
	}
	stage := filepath.Join(filepath.Dir(target), "new.exe")
	if err := os.WriteFile(stage, []byte("replacement"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(stage, target); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "replacement" {
		t.Fatalf("target=%q", got)
	}
	if _, err := io.WriteString(stdin, "finish\n"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child after replacement: %v %s", err, stderr.String())
	}
}
