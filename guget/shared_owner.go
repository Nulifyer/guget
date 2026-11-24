package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// sharedOwner is a file that holds one package's version for several
// projects, such as Directory.Packages.props or an imported props file, whose
// projects resolve packages from different NuGet source scopes. A version
// written there must be available from the sources of every one of them.
type sharedOwner struct {
	path      string
	consumers []*ParsedProject
	scopes    []int
}

func (o sharedOwner) consumerNames() string {
	names := make([]string, len(o.consumers))
	for i, project := range o.consumers {
		names[i] = project.FileName
	}
	return strings.Join(names, ", ")
}

func (o sharedOwner) consumerPaths() []string {
	paths := make([]string, len(o.consumers))
	for i, project := range o.consumers {
		paths[i] = project.FilePath
	}
	return paths
}

// sharedOwnersFor returns the version owners an update of packageID in the
// targets writes whose consumers, every project taking the package's version
// from that owner whether targeted or not, span more than one source scope.
// An owner all of whose consumers share one scope is not returned.
func sharedOwnersFor(mapping *PackageSourceMapping, projects, targets []*ParsedProject, packageID string) []sharedOwner {
	var owners []sharedOwner
	seen := make(map[string]bool)
	for _, target := range targets {
		if !referencesPackage(target, packageID) {
			continue
		}
		key := snapshotPackageOwner(target, packageID)
		if seen[key] {
			continue
		}
		seen[key] = true
		consumers := snapshotAffectedProjects(projects, []*ParsedProject{target}, packageID)
		if !slices.Contains(consumers, target) {
			consumers = append(consumers, target)
		}
		owners = appendSharedOwner(owners, mapping, target.SourceFileForPackage(packageID), consumers)
	}
	return owners
}

// sharedAddOwners returns the central package files an add of packageID to
// the targets writes a new PackageVersion to, when the targets using that
// file span more than one source scope. A file that already has the version
// is not written, so it is not returned.
func sharedAddOwners(mapping *PackageSourceMapping, targets []*ParsedProject, packageID string) []sharedOwner {
	groups := make(map[string][]*ParsedProject)
	var order []string
	for _, target := range targets {
		central := projectCPMTarget(target)
		if central == "" || referencesPackage(target, packageID) {
			continue
		}
		if _, defined := target.CentralVersions[strings.ToLower(packageID)]; defined {
			continue
		}
		key := snapshotOwnerKey(filepath.Clean(central))
		if groups[key] == nil {
			order = append(order, central)
		}
		groups[key] = append(groups[key], target)
	}
	var owners []sharedOwner
	for _, central := range order {
		owners = appendSharedOwner(owners, mapping, central, groups[snapshotOwnerKey(filepath.Clean(central))])
	}
	return owners
}

func appendSharedOwner(owners []sharedOwner, mapping *PackageSourceMapping, path string, consumers []*ParsedProject) []sharedOwner {
	owner := sharedOwner{path: path, consumers: consumers}
	owner.scopes = mapping.scopesForPaths(owner.consumerPaths()...)
	if len(owner.scopes) < 2 {
		return owners
	}
	return append(owners, owner)
}

// versionListed reports whether info lists a version the expression names:
// that version by NuGet version equality, or for a range or float, a version
// inside it.
func versionListed(info *PackageInfo, expression string) bool {
	if info == nil {
		return false
	}
	exact := ParseSemVer(expression)
	requested, isRange := ParseVersionRange(expression)
	for _, v := range info.Versions {
		if v.Unlisted {
			continue
		}
		if exact.IsValid() && !strings.ContainsAny(expression, "[]()*,") {
			if v.SemVer.Equal(exact) {
				return true
			}
			continue
		}
		if isRange && requested.Satisfies(v.SemVer) && (!requested.IsFloating() || requested.Float.Satisfies(v.SemVer)) {
			return true
		}
	}
	return false
}

// metadataAnswered reports whether the sources answered: they supplied the
// package, or every one said it does not have it. A failed source leaves
// the answer unknown.
func metadataAnswered(metadata packageMetadata) bool {
	return metadata.Info != nil || len(metadata.Failures) == 0 && errors.Is(metadata.Err, errPackageNotFound)
}

// sharedOwnerVersionProblem explains why version must not be written to a
// shared owner, given each consumer's metadata (metadataFor), or returns "".
// missing reports that a consumer had no metadata, so availability is unknown.
func sharedOwnerVersionProblem(owner sharedOwner, packageID, version string, metadataFor func(*ParsedProject) packageMetadata) (problem string, missing bool) {
	var lacking, unknown []string
	for _, consumer := range owner.consumers {
		metadata := metadataFor(consumer)
		switch {
		case !metadataAnswered(metadata):
			unknown = append(unknown, consumer.FileName)
		case !versionListed(metadata.Info, version):
			lacking = append(lacking, consumer.FileName)
		}
	}
	name := filepath.Base(owner.path)
	switch {
	case len(lacking) > 0:
		return fmt.Sprintf("%s %s is not available from the NuGet sources of %s, which take its version from %s (shared by %s)", packageID, version, strings.Join(lacking, ", "), name, owner.consumerNames()), false
	case len(unknown) > 0:
		return fmt.Sprintf("cannot check that %s %s is available to every project sharing %s: no source supplied metadata for %s", packageID, version, name, strings.Join(unknown, ", ")), true
	}
	return "", false
}
