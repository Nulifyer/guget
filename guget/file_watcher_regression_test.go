package main

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestWorkspaceWatchDetectsContentWithPreservedMetadata(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "App.csproj")
	mustWriteFile(t, path, `<Project Version="1.0.0"/>`)
	before, err := scanWatchedWorkspaceFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(0, before[path].ModTime)
	mustWriteFile(t, path, `<Project Version="2.0.0"/>`)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	after, err := scanWatchedWorkspaceFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if before[path].Size != after[path].Size || before[path].ModTime != after[path].ModTime {
		t.Fatal("fixture did not preserve metadata")
	}
	if got := diffWatchedWorkspaceFiles(before, after); !slices.Equal(got, []string{path}) {
		t.Fatalf("changes=%v", got)
	}
}

func TestWorkspaceWatchLoadedExternalInputsAndTargets(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	mustWriteFile(t, filepath.Join(root, "App.csproj"), `<Project/>`)
	external := filepath.Join(base, "shared", "common.targets")
	mustWriteFile(t, external, `<Project/>`)
	local := filepath.Join(root, "local.targets")
	mustWriteFile(t, local, `<Project/>`)
	states, err := scanWatchedWorkspaceFiles(root, external)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := states[external]; !ok {
		t.Fatal("loaded external import not watched")
	}
	if _, ok := states[local]; !ok {
		t.Fatal("targets file not watched")
	}
	if err := os.Remove(external); err != nil {
		t.Fatal(err)
	}
	next, err := scanWatchedWorkspaceFiles(root, external)
	if err != nil {
		t.Fatal(err)
	}
	if got := diffWatchedWorkspaceFiles(states, next); !slices.Equal(got, []string{external}) {
		t.Fatalf("deleted import=%v", got)
	}
}

func TestWorkspaceWatchContinuousSavesHaveBoundedDelay(t *testing.T) {
	var batch workspaceWatchBatch
	start := time.Unix(1000, 0)
	for tick := time.Duration(0); tick < workspaceWatchMaxDelay; tick += 100 * time.Millisecond {
		if paths := batch.update(start.Add(tick), []string{"App.csproj"}); len(paths) > 0 {
			t.Fatalf("unexpected early debounce=%v", paths)
		}
	}
	if got := batch.update(start.Add(workspaceWatchMaxDelay), []string{"App.csproj"}); !slices.Equal(got, []string{"App.csproj"}) {
		t.Fatalf("continuous saves starved delivery: %v", got)
	}
	if got := batch.update(start.Add(workspaceWatchMaxDelay+workspaceWatchDebounce), nil); len(got) != 0 {
		t.Fatalf("repeated batch=%v", got)
	}
}

func TestWorkspaceWatchAcknowledgesBytesWithoutHidingExternalSaves(t *testing.T) {
	registry := &workspaceWatchInputs{}
	own := watchedFileState{Hash: sha256.Sum256([]byte("version=2"))}
	external := watchedFileState{Hash: sha256.Sum256([]byte("version=3"))}
	registry.acknowledge(map[string]watchedFileState{"App.csproj": own})
	if !registry.isAcknowledged("App.csproj", own, true) {
		t.Fatal("own revision was not acknowledged")
	}
	if registry.isAcknowledged("App.csproj", external, true) || registry.isAcknowledged("App.csproj", own, false) {
		t.Fatal("external revision/deletion was hidden")
	}
	registry.setPaths([]string{"App.csproj"})
	if registry.isAcknowledged("App.csproj", own, true) {
		t.Fatal("old acknowledgement survived a new authoritative snapshot")
	}
}

func TestWorkspaceWatchRegistryConcurrentUpdates(t *testing.T) {
	registry := &workspaceWatchInputs{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				registry.setPaths([]string{"App.csproj"})
				registry.acknowledge(map[string]watchedFileState{"App.csproj": {Size: 1}})
				registry.inputPaths()
				registry.changes(nil, map[string]watchedFileState{"App.csproj": {Size: 1}})
				registry.isAcknowledged("App.csproj", watchedFileState{Size: 1}, true)
			}
		}()
	}
	wg.Wait()
}

func TestWorkspaceWatchNewInputsUseLoadedRevision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "NuGet.Config")
	mustWriteFile(t, path, `<configuration/>`)
	loaded, exists, err := readWatchedFileState(path)
	if err != nil || !exists {
		t.Fatalf("read loaded input: exists=%v err=%v", exists, err)
	}
	registry := &workspaceWatchInputs{}
	registry.setPaths([]string{path}, map[string]watchedFileState{path: loaded})
	firstScan := map[string]watchedFileState{path: loaded}
	if got := registry.changes(nil, firstScan); len(got) != 0 {
		t.Fatalf("unchanged newly loaded input triggered reload: %v", got)
	}
	mustWriteFile(t, path, `<configuration><packageSources/></configuration>`)
	edited, exists, err := readWatchedFileState(path)
	if err != nil || !exists {
		t.Fatalf("read edited input: exists=%v err=%v", exists, err)
	}
	for _, prev := range []map[string]watchedFileState{nil, firstScan} {
		if got := registry.changes(prev, map[string]watchedFileState{path: edited}); !slices.Equal(got, []string{path}) {
			t.Fatalf("edit after loading was hidden: %v", got)
		}
	}
	if got := registry.changes(firstScan, nil); !slices.Equal(got, []string{path}) {
		t.Fatalf("deleted loaded input was hidden: %v", got)
	}
	if got := registry.changes(nil, map[string]watchedFileState{"new.props": loaded}); !slices.Equal(got, []string{"new.props"}) {
		t.Fatalf("new workspace file was hidden: %v", got)
	}
}

func TestReloadQueueMergesPathsRefreshAndDiagnostics(t *testing.T) {
	failure := errors.New("save needs inspection")
	previous := reloadRequestedMsg{reason: "manual reload", paths: []string{"NuGet.Config"}, saveError: failure}
	next := reloadRequestedMsg{reason: "disk changes detected", paths: []string{"App.csproj"}, automatic: true}
	for _, pair := range [][2]reloadRequestedMsg{{previous, next}, {next, previous}} {
		merged := mergeReloadRequests(pair[0], pair[1])
		if !reflect.DeepEqual(merged.paths, []string{"App.csproj", "NuGet.Config"}) || merged.automatic || merged.reason != "manual reload" || !errors.Is(merged.saveError, failure) {
			t.Fatalf("merged=%+v", merged)
		}
	}
}

func TestWorkspaceInputPathsIncludeParentOwnersAndAncestorConfiguration(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "nested")
	owner := filepath.Join(base, "common.targets")
	snapshot := &workspaceSnapshot{ParsedProjects: []*ParsedProject{{FilePath: filepath.Join(root, "App.csproj"), AddTargets: []AddTarget{{FilePath: owner}}}}}
	paths := workspaceInputPaths(root, snapshot)
	for _, want := range []string{owner, filepath.Join(base, "Directory.Packages.props"), filepath.Join(base, "NuGet.Config"), filepath.Join(root, "App.csproj")} {
		if !slices.Contains(paths, want) {
			t.Fatalf("missing watched input %s", want)
		}
	}
}
