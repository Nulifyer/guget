package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nulifyer/guget/internal/projectedit"
)

// quickUpdateDependencyConflict detects only known direct-version downgrades.
// It uses loaded declarations and metadata, not a restore graph or a resolver.
// Explicit stable/version-picker actions remain available for deliberate edits.
func (m *App) quickUpdateDependencyConflict(packageID string, candidate *PackageVersion, scope actionScope) string {
	selected := m.ctx.ParsedProjects
	project := m.selectedProject()
	if scope == scopeSelected && project != nil {
		selected = []*ParsedProject{project}
	}
	var changing []*ParsedProject
	for _, p := range selected {
		for ref := range p.Packages {
			if !strings.EqualFold(ref.Name, packageID) || ((scope == scopeAll || project == nil) && ref.Locked) {
				continue
			}
			requested := ref.Requested
			if requested == "" {
				requested = ref.Version.String()
			}
			if requested != candidate.SemVer.String() && requested != "["+candidate.SemVer.String()+"]" {
				changing = append(changing, p)
			}
			break
		}
	}
	consumers := append(changing, snapshotAffectedProjects(m.ctx.ParsedProjects, changing, packageID)...)
	slices.SortFunc(consumers, func(a, b *ParsedProject) int { return strings.Compare(a.FilePath, b.FilePath) })
	for i, consumer := range consumers {
		if i > 0 && consumer.FilePath == consumers[i-1].FilePath {
			continue
		}
		references := consumer.Packages.ToSlice()
		slices.SortFunc(references, func(a, b PackageReference) int {
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
		counts := make(map[string]int, len(references))
		for _, reference := range references {
			counts[strings.ToLower(reference.Name)]++
		}
		for _, reference := range references {
			if strings.EqualFold(reference.Name, packageID) || counts[strings.ToLower(reference.Name)] != 1 {
				continue
			}
			requested := reference.Requested
			if requested == "" {
				requested = reference.Version.String()
			}
			current, known := literalDependencyRequest(requested)
			if !known {
				continue
			}
			frameworks := consumer.TargetFrameworks.ToSlice()
			slices.SortFunc(frameworks, func(a, b TargetFramework) int { return strings.Compare(a.String(), b.String()) })
			for _, target := range frameworks {
				groups := preflightDependencyGroups(candidate.DependencyGroups, target)
				if len(groups) == 0 {
					continue
				}
				var required SemVer
				requiredInclusive, conflict := false, false
				for _, group := range groups {
					minimum, inclusive, found := dependencyGroupMinimum(group, reference.Name)
					if !found || current.IsNewerThan(minimum) || (!minimum.IsNewerThan(current) && inclusive) {
						conflict = false
						break
					}
					if !conflict || required.IsNewerThan(minimum) {
						required, requiredInclusive = minimum, inclusive
					} else if !minimum.IsNewerThan(required) {
						requiredInclusive = requiredInclusive || inclusive
					}
					conflict = true
				}
				if conflict {
					operator := ">="
					if !requiredInclusive {
						operator = ">"
					}
					return fmt.Sprintf("Update %s: %s -> %s%s; v/a override", reference.Name, requested, operator, required.String())
				}
			}
		}
	}
	return ""
}

// Exact framework groups take precedence. Without one, require the conflict in
// every compatible group rather than guessing NuGet's nearest-framework choice.
func preflightDependencyGroups(groups []dependencyGroup, target TargetFramework) []dependencyGroup {
	if target.Family == FamilyUnknown {
		return nil
	}
	var exact, compatible []dependencyGroup
	unknown := false
	for _, group := range groups {
		normalized := normFramework(group.TargetFramework)
		framework := ParseTargetFramework(normalized)
		if normalized == normFramework(target.Raw) {
			exact = append(exact, group)
		}
		if framework.Family == FamilyUnknown && normalized != "any" {
			unknown = true
			continue
		}
		if target.IsCompatibleWith(framework) {
			compatible = append(compatible, group)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	if unknown {
		return nil
	}
	return compatible
}

func literalDependencyRequest(requested string) (SemVer, bool) {
	value := strings.TrimSpace(requested)
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") && !strings.Contains(value, ",") {
		value = value[1 : len(value)-1]
	}
	if strings.ContainsAny(value, "[](),*") || projectedit.ValidateVersion(value) != nil {
		return SemVer{}, false
	}
	return ParseSemVer(value), true
}

func dependencyGroupMinimum(group dependencyGroup, packageID string) (SemVer, bool, bool) {
	var result SemVer
	inclusive, found := false, false
	for _, dependency := range group.Dependencies {
		if !strings.EqualFold(dependency.ID, packageID) {
			continue
		}
		if found { // Duplicate dependency declarations are ambiguous.
			return SemVer{}, false, false
		}
		var known bool
		result, inclusive, known = dependencyMinimum(dependency.Range)
		if !known {
			return SemVer{}, false, false
		}
		found = true
	}
	return result, inclusive, found
}

func dependencyMinimum(expression string) (SemVer, bool, bool) {
	value := strings.TrimSpace(expression)
	if projectedit.ValidateVersion(value) != nil {
		return SemVer{}, false, false
	}
	if minimum, known := literalDependencyRequest(value); known {
		return minimum, true, true
	}
	if len(value) < 3 || !strings.ContainsAny(value[:1], "[(") || !strings.Contains(value, ",") {
		return SemVer{}, false, false
	}
	lower, upper, _ := strings.Cut(value[1:len(value)-1], ",")
	minimum, known := literalDependencyRequest(lower)
	if !known {
		return SemVer{}, false, false
	}
	inclusive := value[0] == '['
	if strings.TrimSpace(upper) != "" {
		maximum, known := literalDependencyRequest(upper)
		if !known || minimum.IsNewerThan(maximum) || (!maximum.IsNewerThan(minimum) && (!inclusive || value[len(value)-1] != ']')) {
			return SemVer{}, false, false
		}
	}
	return minimum, inclusive, true
}
