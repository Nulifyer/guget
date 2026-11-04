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
		snapshot, scanErr = scanWorkspaceWithParser(root, func(file string) (*ParsedProject, error) {
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
	root, _ := sharedCoherenceFixture(t)
	parses := 0
	snapshot, err := scanWorkspaceWithParser(root, func(file string) (*ParsedProject, error) { parses++; return ParseCsproj(file) })
	if err != nil {
		t.Fatal(err)
	}
	if parses != 4 {
		t.Fatalf("new external dependency must receive a guarded second scan, parses=%d", parses)
	}
	assertCoherentVersion(t, snapshot, "1.0.0")
}

func TestWorkspaceScanContinuousExternalChangesReturnUnstableError(t *testing.T) {
	root, owner := sharedCoherenceFixture(t)
	parses := 0
	snapshot, err := scanWorkspaceWithParser(root, func(file string) (*ParsedProject, error) {
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
	snapshot, err := scanWorkspaceWithParser(root, func(file string) (*ParsedProject, error) {
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
