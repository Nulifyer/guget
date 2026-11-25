package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func sharedCoherenceFixture(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	owner := filepath.Join(base, "shared.props")
	mustWriteFile(t, owner, `<Project><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`)
	for _, name := range []string{"First.csproj", "Second.csproj"} {
		mustWriteFile(t, filepath.Join(root, name), `<Project><Import Project="../shared.props"/></Project>`)
	}
	return root, owner
}

func assertCoherentVersion(t *testing.T, snapshot *workspaceSnapshot, want string) {
	t.Helper()
	for _, project := range append(append([]*ParsedProject(nil), snapshot.ParsedProjects...), snapshot.PropsProjects...) {
		for ref := range project.Packages {
			if ref.Name == "A" && ref.Version.String() != want {
				t.Fatalf("mixed snapshot: %s has %s, want %s", project.FilePath, ref.Version.String(), want)
			}
		}
	}
}

func TestWorkspaceScanRetriesSharedOwnerChangeBetweenProjectParses(t *testing.T) {
	root, owner := sharedCoherenceFixture(t)
	firstParsed := make(chan struct{})
	resume := make(chan struct{})
	done := make(chan struct{})
	var snapshot *workspaceSnapshot
	var scanErr error
	parses := 0
	go func() {
		defer close(done)
		snapshot, scanErr = scanWorkspaceWithParser(root, workspaceScanOptions{}, func(file string) (*ParsedProject, error) {
			parsed, err := ParseCsproj(file)
			parses++
			if parses == 1 {
				close(firstParsed)
				<-resume
			}
			return parsed, err
		})
	}()
	<-firstParsed
	if err := UpdatePackageVersion(owner, "A", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	close(resume)
	<-done
	if scanErr != nil {
		t.Fatal(scanErr)
	}
	if parses < 4 {
		t.Fatalf("changed external owner was not retried: parses=%d", parses)
	}
	assertCoherentVersion(t, snapshot, "2.0.0")
}

func TestWorkspaceScanTracksNewExternalInputBeforePublishing(t *testing.T) {
	root, owner := sharedCoherenceFixture(t)
	parses := 0
	snapshot, err := scanWorkspaceWithParser(root, workspaceScanOptions{}, func(file string) (*ParsedProject, error) { parses++; return ParseCsproj(file) })
	if err != nil {
		t.Fatal(err)
	}
	// Parsing discovers the import. Its consumed revision proves the disk
	// view, so the scan must not parse the workspace a second time.
	if parses != 2 {
		t.Fatalf("discovered import cost another scan: parses=%d, want 2", parses)
	}
	assertCoherentVersion(t, snapshot, "1.0.0")
	state, exists, err := readWatchedFileState(owner)
	if err != nil || !exists {
		t.Fatalf("read owner: exists=%v err=%v", exists, err)
	}
	if tracked, ok := snapshot.FileRevisions[owner]; !ok || tracked.Hash != state.Hash {
		t.Fatalf("discovered import is not tracked at its current revision: %v", snapshot.FileRevisions)
	}
}

func TestWorkspaceScanRetriesDiscoveredImportEditedAfterParsing(t *testing.T) {
	root, owner := sharedCoherenceFixture(t)
	parses := 0
	snapshot, err := scanWorkspaceWithParser(root, workspaceScanOptions{}, func(file string) (*ParsedProject, error) {
		parsed, err := ParseCsproj(file)
		parses++
		if parses == 2 && err == nil {
			// Both projects consumed 1.0.0. The import changes before the
			// scan's closing read, so the consumed revision is stale.
			err = UpdatePackageVersion(owner, "A", "2.0.0")
		}
		return parsed, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if parses != 4 {
		t.Fatalf("edit of a discovered import after parsing was not retried: parses=%d, want 4", parses)
	}
	assertCoherentVersion(t, snapshot, "2.0.0")
}

func TestConsistentWorkspaceInputRevisions(t *testing.T) {
	state := func(b byte) watchedFileState { return watchedFileState{Hash: [32]byte{b}, Owner: "o"} }
	known := map[string]watchedFileState{"p": state(1)}
	cases := []struct {
		name     string
		after    map[string]watchedFileState
		consumed workspaceRevisionLedger
		want     bool
	}{
		{"unchanged", map[string]watchedFileState{"p": state(1)}, nil, true},
		{"known input changed", map[string]watchedFileState{"p": state(2)}, nil, false},
		{"known input removed", map[string]watchedFileState{}, nil, false},
		{"discovered at consumed revision", map[string]watchedFileState{"p": state(1), "i": state(3)}, workspaceRevisionLedger{"i": {3}}, true},
		{"discovered then changed", map[string]watchedFileState{"p": state(1), "i": state(4)}, workspaceRevisionLedger{"i": {3}}, false},
		{"discovered then removed", map[string]watchedFileState{"p": state(1)}, workspaceRevisionLedger{"i": {3}}, false},
		{"new input nothing consumed", map[string]watchedFileState{"p": state(1), "n": state(5)}, nil, false},
	}
	for _, c := range cases {
		if got := consistentWorkspaceInputRevisions(known, c.after, c.consumed); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWorkspaceScanContinuousExternalChangesReturnUnstableError(t *testing.T) {
	root, owner := sharedCoherenceFixture(t)
	parses := 0
	snapshot, err := scanWorkspaceWithParser(root, workspaceScanOptions{}, func(file string) (*ParsedProject, error) {
		parsed, err := ParseCsproj(file)
		parses++
		next := `<Project><ItemGroup><PackageReference Include="A" Version="2.0.0"/></ItemGroup></Project>`
		if parses%2 == 0 {
			next = `<Project><ItemGroup><PackageReference Include="A" Version="1.0.0"/></ItemGroup></Project>`
		}
		if err == nil {
			err = os.WriteFile(owner, []byte(next), 0o644)
		}
		return parsed, err
	})
	// The owner returns to its original bytes repeatedly. Per-project revision
	// validation must reject these intermediate changes, not trust final bytes.
	if err == nil || snapshot != nil || !strings.Contains(err.Error(), "changed during") {
		t.Fatalf("unstable snapshot=%v err=%v", snapshot, err)
	}
}

func TestWorkspaceScanRejectsConsumedOwnerRevisionEvenIfDiskRestored(t *testing.T) {
	root, owner := sharedCoherenceFixture(t)
	stable, err := os.ReadFile(owner)
	if err != nil {
		t.Fatal(err)
	}
	intermediate := strings.Replace(string(stable), "1.0.0", "2.0.0", 1)
	snapshot, err := scanWorkspaceWithParser(root, workspaceScanOptions{}, func(file string) (*ParsedProject, error) {
		if err := os.WriteFile(owner, []byte(intermediate), 0o644); err != nil {
			return nil, err
		}
		parsed, parseErr := ParseCsproj(file)
		restoreErr := os.WriteFile(owner, stable, 0o644)
		if parseErr != nil {
			return nil, parseErr
		}
		return parsed, restoreErr
	})
	if err == nil || snapshot != nil {
		t.Fatalf("consumed intermediate owner was published: snapshot=%v err=%v", snapshot, err)
	}
	final, readErr := os.ReadFile(owner)
	if readErr != nil || string(final) != string(stable) {
		t.Fatal("fixture failed to restore stable owner")
	}
}

func TestProjectInputRevisionsRejectDuplicateDifferentReads(t *testing.T) {
	project := &ParsedProject{}
	path := filepath.Join(t.TempDir(), "shared.props")
	if err := recordProjectInput(project, path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := recordProjectInput(project, path, []byte("second")); err == nil {
		t.Fatal("different bytes from a repeated input were accepted")
	}
}

func TestWorkspaceSnapshotPublicationRejectsChangesAfterDiscovery(t *testing.T) {
	root, owner := sharedCoherenceFixture(t)
	snapshot, err := scanWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdatePackageVersion(owner, "A", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkspaceSnapshot(snapshot); err == nil {
		t.Fatal("snapshot changed while source loading was still publishable")
	}
}

func TestDeferredSourceDiscoveryCannotMakeChangedWorkspaceCurrent(t *testing.T) {
	root, owner := sharedCoherenceFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var signal, unblock sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signal.Do(func() { close(entered) })
		<-release
		fmt.Fprint(w, `{"version":"3.0.0","resources":[{"@id":"https://example.invalid/registration/","@type":"RegistrationsBaseUrl/3.6.0"}]}`)
	}))
	defer server.Close()
	defer unblock.Do(func() { close(release) })
	mustWriteFile(t, filepath.Join(root, "NuGet.Config"), `<configuration><packageSources><clear/><add key="test" value="`+server.URL+`"/></packageSources></configuration>`)
	snapshot, err := loadWorkspaceContext(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := snapshot.NugetServices[0].deferred.resolve(context.Background())
		done <- err
	}()
	<-entered
	if err := UpdatePackageVersion(owner, "A", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	unblock.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := validateWorkspaceSnapshot(snapshot); err == nil {
		t.Fatal("completed source discovery made changed workspace declarations current")
	}
}
