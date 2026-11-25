package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// restoreStep is one dotnet restore invocation planned by planRestore.
type restoreStep struct {
	Project *ParsedProject
	// Args are the dotnet arguments, without --interactive.
	Args []string
	// ForceEvaluate is set when the project's lock file is in locked mode
	// and an edit invalidated it.
	ForceEvaluate bool
	// Reason says why an edit affects the project; empty when none does.
	Reason string
	// Requested is set for projects the caller asked to restore.
	Requested bool
}

// planRestore is the single restore policy for the CLI and the TUI. It
// restores the requested projects and every workspace project that changed
// invalidates: projects that read a changed file, such as a shared
// Directory.Packages.props or Directory.Build.props, and projects that
// reference those through ProjectReference, whose lock files record their
// dependencies' packages (NuGet PackagesLockFileUtilities.IsLockFileValid).
//
// Restore in locked mode fails with NU1004 when a lock file no longer
// matches, and does not rewrite it (NuGet RestoreCommand.cs:1664-1718). For an
// affected project whose lock file exists and whose RestoreLockedMode is true
// or cannot be determined statically, the step adds --force-evaluate
// (RestoreForceEvaluate), which skips reading the lock file and regenerates
// it. Projects come in dependency order, each once.
func planRestore(workspace, requested []*ParsedProject, changed []string) []restoreStep {
	projects := make(map[string]*ParsedProject)
	var keys []string
	add := func(project *ParsedProject) string {
		key := mutationInputKey(project.FilePath)
		if _, known := projects[key]; !known {
			projects[key] = project
			keys = append(keys, key)
		}
		return key
	}
	for _, project := range workspace {
		if project != nil && project.FilePath != "" {
			add(project)
		}
	}

	selected := make(map[string]bool)
	for _, project := range requested {
		if project != nil && project.FilePath != "" {
			// Prefer the workspace's view of a requested project; a queued TUI
			// request can hold a project from an earlier snapshot.
			selected[add(project)] = true
		}
	}

	reasons := make(map[string]string)
	changedFiles := make(map[string]string, len(changed))
	for _, path := range changed {
		changedFiles[mutationInputKey(path)] = path
	}
	for _, key := range keys {
		for _, input := range projectInputPaths(projects[key]) {
			if path, ok := changedFiles[mutationInputKey(input)]; ok {
				if key == mutationInputKey(path) {
					reasons[key] = "project file changed"
				} else {
					reasons[key] = "uses changed " + path
				}
				break
			}
		}
	}

	references := make(map[string][]string) // project → workspace projects it references
	dependents := make(map[string][]string) // project → workspace projects referencing it
	for _, key := range keys {
		for _, reference := range projects[key].ProjectReferences {
			target := mutationInputKey(reference)
			if _, known := projects[target]; known && target != key {
				references[key] = append(references[key], target)
				dependents[target] = append(dependents[target], key)
			}
		}
	}
	queue := make([]string, 0, len(reasons))
	for _, key := range keys {
		if reasons[key] != "" {
			queue = append(queue, key)
		}
	}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for _, dependent := range dependents[key] {
			if reasons[dependent] == "" {
				reasons[dependent] = "references " + projects[key].FileName + ", whose packages changed"
				queue = append(queue, dependent)
			}
		}
	}

	include := make(map[string]bool)
	for _, key := range keys {
		include[key] = selected[key] || reasons[key] != ""
	}
	sorted := append([]string(nil), keys...)
	slices.Sort(sorted)
	var ordered []string
	visited := make(map[string]bool)
	var visit func(string)
	visit = func(key string) {
		if visited[key] {
			return
		}
		visited[key] = true
		dependencies := append([]string(nil), references[key]...)
		slices.Sort(dependencies)
		for _, dependency := range dependencies {
			visit(dependency)
		}
		if include[key] {
			ordered = append(ordered, key)
		}
	}
	for _, key := range sorted {
		visit(key)
	}

	steps := make([]restoreStep, 0, len(ordered))
	for _, key := range ordered {
		project := projects[key]
		step := restoreStep{Project: project, Args: []string{"restore", project.FilePath}, Reason: reasons[key], Requested: selected[key]}
		if step.Reason != "" && lockedLockFile(project) {
			step.ForceEvaluate = true
			step.Args = append(step.Args, "--force-evaluate")
		}
		steps = append(steps, step)
	}
	return steps
}

// lockedLockFile reports whether restore may be in locked mode with an
// existing lock file, so an invalidated lock file would fail with NU1004.
// Existence comes from when the project was read: the TUI plans restores on
// its event loop, which must not touch the file system. The restore worker
// checks again with withCurrentLockFile.
func lockedLockFile(project *ParsedProject) bool {
	return project.LockFile.LockedMode != LockSettingFalse && project.LockFile.Exists
}

// withCurrentLockFile checks, just before the step runs and off the event
// loop, whether a lock file missing when the project was read exists now, such
// as one an earlier restore created. An affected project in locked mode then
// needs --force-evaluate too, or restore fails with NU1004 instead of
// regenerating the lock file.
func (s restoreStep) withCurrentLockFile() restoreStep {
	lock := s.Project.LockFile
	if s.ForceEvaluate || s.Reason == "" || lock.LockedMode == LockSettingFalse || lock.Path == "" {
		return s
	}
	if info, err := os.Stat(lock.Path); err == nil && info.Mode().IsRegular() {
		s.ForceEvaluate = true
		s.Args = append(append([]string(nil), s.Args...), "--force-evaluate")
	}
	return s
}

// describeRestorePlan explains projects restored beyond the request and the
// lock-file handling, so a wider restore is not a surprise.
func describeRestorePlan(w io.Writer, steps []restoreStep) {
	for _, step := range steps {
		var notes []string
		if step.Reason != "" && !step.Requested {
			notes = append(notes, step.Reason)
		}
		if step.ForceEvaluate {
			notes = append(notes, "locked-mode lock file is regenerated with --force-evaluate")
		}
		if len(notes) > 0 {
			fmt.Fprintf(w, "guget: restore %s: %s\n", step.Project.FilePath, strings.Join(notes, "; "))
		}
	}
}
