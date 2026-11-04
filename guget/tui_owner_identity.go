package main

import (
	"path/filepath"
	"runtime"
	"strings"
)

// snapshotPackageOwner reads only immutable discovery state. Filesystem identity
// resolution happens while parsing; row rendering must never resolve symlinks.
func snapshotPackageOwner(project *ParsedProject, packageID string) string {
	source := project.SourceFileForPackage(packageID)
	if identity := project.PackageOwnerIdentities[source]; identity != "" {
		return snapshotOwnerKey(identity)
	}
	// Synthetic/read-only snapshots can lack a resolved identity. Keep their
	// lexical identity without probing paths that may now be missing or remote.
	return snapshotOwnerKey(filepath.Clean(source))
}

func snapshotOwnerKey(path string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(path)
	}
	return path
}

func snapshotAffectedProjects(projects, selected []*ParsedProject, packageID string) []*ParsedProject {
	owners := make(map[string]struct{}, len(selected))
	for _, project := range selected {
		owners[snapshotPackageOwner(project, packageID)] = struct{}{}
	}
	consumers := make([]*ParsedProject, 0, len(selected))
	for _, project := range projects {
		if _, ok := owners[snapshotPackageOwner(project, packageID)]; ok {
			consumers = append(consumers, project)
		}
	}
	return consumers
}

// Build once per view refresh. Looking up a row must not rescan every project
// reference, which makes a large workspace's rebuild quadratic in package count.
func packageFrameworksForView(projects []*ParsedProject, selected *ParsedProject) map[string]Set[TargetFramework] {
	targets := make(map[string]Set[TargetFramework])
	owners := make(map[string]string)
	if selected != nil {
		for ref := range selected.Packages {
			id := strings.ToLower(ref.Name)
			owners[id] = snapshotPackageOwner(selected, id)
			targets[id] = NewSet[TargetFramework]()
			for framework := range selected.TargetFrameworks {
				targets[id].Add(framework)
			}
		}
	}
	for _, project := range projects {
		for ref := range project.Packages {
			id := strings.ToLower(ref.Name)
			if selected != nil {
				owner, relevant := owners[id]
				if !relevant || owner != snapshotPackageOwner(project, id) {
					continue
				}
			}
			if targets[id] == nil {
				targets[id] = NewSet[TargetFramework]()
			}
			for framework := range project.TargetFrameworks {
				targets[id].Add(framework)
			}
		}
	}
	return targets
}
