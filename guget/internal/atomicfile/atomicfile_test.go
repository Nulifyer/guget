package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCheckedWriteRechecksAfterReplacementFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.csproj")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	conflict := errors.New("external revision")
	checks, replacements := 0, 0
	err := writeFile(path, []byte("guget"), 0o600, func() error {
		checks++
		// A temporary file already exists and is fully prepared when checked.
		temps, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".project.csproj.tmp-*"))
		if len(temps) != 1 {
			t.Fatalf("prepared temporary files = %v", temps)
		}
		data, readErr := os.ReadFile(temps[0])
		if readErr != nil || string(data) != "guget" {
			t.Fatalf("prepared contents = %q, error %v", data, readErr)
		}
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if string(current) != "old" {
			return conflict
		}
		return nil
	}, func(source, destination string) error {
		replacements++
		// Model a Windows sharing failure followed by an editor save before retry.
		if err := os.WriteFile(destination, []byte("external"), 0o600); err != nil {
			t.Fatal(err)
		}
		return errors.New("sharing violation")
	})
	if !errors.Is(err, conflict) || checks != 2 || replacements != 1 {
		t.Fatalf("error = %v, checks = %d, replacements = %d", err, checks, replacements)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "external" {
		t.Fatalf("external revision overwritten: %q", data)
	}
	temps, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".project.csproj.tmp-*"))
	if len(temps) != 0 {
		t.Fatalf("temporary files leaked: %v", temps)
	}
}

func TestCheckedWriteRefusesReplacementAfterPreparation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.csproj")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	conflict := errors.New("stale")
	replaced := false
	err := writeFile(path, []byte("new"), 0o600, func() error { return conflict }, func(string, string) error {
		replaced = true
		return nil
	})
	if !errors.Is(err, conflict) || replaced {
		t.Fatalf("error = %v, replaced = %v", err, replaced)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "old" {
		t.Fatalf("contents = %q", data)
	}
}

func TestWriteFileReplacesContentsAndPreservesMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.csproj")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "new" {
		t.Fatalf("contents = %q, want new", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), before.Mode().Perm(); got != want {
		t.Fatalf("mode = %o, want %o", got, want)
	}
}

func TestWriteFileCreatesDestinationWithFallbackMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.props")
	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o600)
	if runtime.GOOS == "windows" {
		// Windows exposes writability, rather than POSIX owner/group bits.
		want = 0o666
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode = %o, want %o", got, want)
	}
}

func TestOnlyFlushedWritesFlush(t *testing.T) {
	files, dirs := 0, 0
	syncFile = func(f *os.File) error { files++; return f.Sync() }
	syncDir = func(string) error { dirs++; return nil }
	t.Cleanup(func() { syncFile, syncDir = (*os.File).Sync, syncDirectory })
	path := filepath.Join(t.TempDir(), "project.csproj")
	if err := WriteFileCheckedWith(path, []byte("buffered"), 0o600, nil, Buffered); err != nil || files != 0 || dirs != 0 {
		t.Fatalf("buffered write: error %v, file syncs %d, directory syncs %d", err, files, dirs)
	}
	if err := WriteFileCheckedWith(path, []byte("flushed"), 0o600, nil, Flushed); err != nil || files != 1 || dirs != 1 {
		t.Fatalf("flushed write: error %v, file syncs %d, directory syncs %d", err, files, dirs)
	}
}

func TestBufferedWriteReplacesAtomicallyAndHonorsTheCheck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project.csproj")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	conflict := errors.New("stale")
	if err := WriteFileCheckedWith(path, []byte("refused"), 0o600, func() error { return conflict }, Buffered); !errors.Is(err, conflict) {
		t.Fatalf("failed check error = %v", err)
	}
	if err := WriteFileCheckedWith(path, []byte("new"), 0o600, func() error { return nil }, Buffered); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatalf("contents = %q, error %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), before.Mode().Perm(); got != want {
		t.Fatalf("mode = %o, want %o", got, want)
	}
	temps, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".project.csproj.tmp-*"))
	if len(temps) != 0 {
		t.Fatalf("temporary files leaked: %v", temps)
	}
}
