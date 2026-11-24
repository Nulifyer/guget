package msbuildstatic

import (
	"path/filepath"
	"strings"
)

const (
	typeReference  = "packagereference"
	typeVersion    = "packageversion"
	typeGlobal     = "globalpackagereference"
	typeDownload   = "packagedownload"
	typeProjectRef = "projectreference"
)

// packageItemType returns the lowercase item type when name is one the
// evaluator models. MSBuild item types are case-insensitive.
func packageItemType(name string) string {
	switch lower := strings.ToLower(name); lower {
	case typeReference, typeVersion, typeGlobal, typeDownload, typeProjectRef:
		return lower
	}
	return ""
}

type metadata struct {
	value
	owner       string // file that set this value
	conditional bool
}

type item struct {
	kind        string
	id          string
	file        string // file with the Include
	meta        map[string]*metadata
	conditional bool
	global      bool
}

func (i *item) metadata(name string) *metadata {
	if m, ok := i.meta[strings.ToLower(name)]; ok {
		return m
	}
	return &metadata{value: value{Known: true}}
}

// Attributes that are item operations rather than metadata.
var operationAttributes = map[string]bool{
	"include": true, "exclude": true, "update": true, "remove": true, "condition": true,
	"keepmetadata": true, "removemetadata": true, "keepduplicates": true,
	"matchonmetadata": true, "matchonmetadataoptions": true,
}

// evaluateItems is MSBuild's item pass: item elements in evaluation order,
// with conditions evaluated against the final property values.
func (e *evaluator) evaluateItems() {
	projectDir := filepath.Dir(e.projectPath)
	lists := map[string][]*item{}
	for _, element := range e.items {
		if element.marker {
			e.expandGlobalReferences(lists)
			continue
		}
		s := e.scope(element.file)
		groupCondition := s.evaluateCondition(element.group.attrValue("Condition"), projectDir)
		if groupCondition == isFalse {
			continue
		}
		for _, child := range element.group.children {
			kind := packageItemType(child.name)
			if kind == "" {
				continue
			}
			condition := groupCondition.and(s.evaluateCondition(child.attrValue("Condition"), projectDir))
			if condition == isFalse {
				continue
			}
			conditional := condition == unknown || element.uncertain
			if conditional {
				e.gap("%s %s in %s is conditional", child.name, itemSpec(child), filepath.Base(element.file))
			}
			switch {
			case hasAttr(child, "Include"):
				e.includeItems(lists, kind, child, element.file, conditional)
			case hasAttr(child, "Update"):
				e.reason("%s Update in %s changes earlier items", child.name, filepath.Base(element.file))
				e.updateItems(lists, kind, child, element.file, conditional)
			case hasAttr(child, "Remove"):
				e.removeItems(lists, kind, child, element.file, conditional)
			}
		}
	}
	e.finish(lists)
}

func hasAttr(element *node, name string) bool {
	_, ok := element.attr(name)
	return ok
}

func itemSpec(element *node) string {
	for _, name := range []string{"Include", "Update", "Remove"} {
		if value, ok := element.attr(name); ok {
			return value
		}
	}
	return ""
}

// identities expands an Include, Update, or Remove value into item IDs.
func (e *evaluator) identities(kind string, element *node, file, attribute string) ([]string, bool) {
	expanded := e.scope(file).expand(element.attrValue(attribute))
	if !expanded.Known {
		e.gap("%s %s %q in %s needs MSBuild evaluation", element.name, attribute, element.attrValue(attribute), filepath.Base(file))
		return nil, false
	}
	ids := splitList(expanded.Text)
	for _, id := range ids {
		if strings.ContainsAny(id, "*?") {
			e.gap("wildcard %s %q in %s needs MSBuild evaluation", element.name, id, filepath.Base(file))
			return nil, false
		}
	}
	if kind == typeProjectRef {
		for i, id := range ids {
			// Item paths in imported files are relative to the project.
			ids[i] = absoluteFrom(filepath.Dir(e.projectPath), id)
		}
	}
	return ids, true
}

func (e *evaluator) elementMetadata(element *node, file string, conditional bool) map[string]*metadata {
	s := e.scope(file)
	meta := map[string]*metadata{}
	for _, attr := range element.attrs {
		name := strings.ToLower(attr.Name.Local)
		if attr.Name.Space != "" || operationAttributes[name] {
			continue
		}
		meta[name] = &metadata{value: s.expand(strings.TrimSpace(attr.Value)), owner: file, conditional: conditional}
	}
	for _, child := range element.children {
		condition := s.evaluateCondition(child.attrValue("Condition"), filepath.Dir(e.projectPath))
		if condition == isFalse {
			continue
		}
		meta[strings.ToLower(child.name)] = &metadata{value: s.expand(strings.TrimSpace(child.propertyText())), owner: file, conditional: conditional || condition == unknown}
	}
	return meta
}

func (e *evaluator) includeItems(lists map[string][]*item, kind string, element *node, file string, conditional bool) {
	if hasAttr(element, "Exclude") {
		e.gap("%s Exclude in %s needs MSBuild evaluation", element.name, filepath.Base(file))
	}
	ids, ok := e.identities(kind, element, file, "Include")
	if !ok {
		return
	}
	for _, id := range ids {
		lists[kind] = append(lists[kind], &item{kind: kind, id: id, file: file, meta: e.elementMetadata(element, file, conditional), conditional: conditional})
	}
}

// updateItems changes metadata of earlier items only; an Update with no
// earlier Include declares nothing.
func (e *evaluator) updateItems(lists map[string][]*item, kind string, element *node, file string, conditional bool) {
	ids, ok := e.identities(kind, element, file, "Update")
	if !ok {
		return
	}
	for _, id := range ids {
		for _, existing := range lists[kind] {
			if !sameID(kind, existing.id, id) {
				continue
			}
			for name, m := range e.elementMetadata(element, file, conditional) {
				existing.meta[name] = m
			}
			if conditional {
				existing.conditional = true
			}
		}
	}
}

func (e *evaluator) removeItems(lists map[string][]*item, kind string, element *node, file string, conditional bool) {
	ids, ok := e.identities(kind, element, file, "Remove")
	if !ok {
		return
	}
	kept := lists[kind][:0]
	for _, existing := range lists[kind] {
		matched := false
		for _, id := range ids {
			if sameID(kind, existing.id, id) {
				matched = true
			}
		}
		switch {
		case matched && conditional:
			existing.conditional = true
			kept = append(kept, existing)
		case !matched:
			kept = append(kept, existing)
		}
	}
	lists[kind] = kept
}

func sameID(kind, a, b string) bool {
	if kind == typeProjectRef {
		return filepath.Clean(a) == filepath.Clean(b) || strings.EqualFold(a, b)
	}
	return strings.EqualFold(a, b)
}

// expandGlobalReferences applies NuGet.targets:138-162: when CPM is on and
// RestoreEnableGlobalPackageReference is not false, each GlobalPackageReference
// becomes a versionless PackageReference with PrivateAssets=All and a
// PackageVersion with its Version. Item conditions see final properties.
func (e *evaluator) expandGlobalReferences(lists map[string][]*item) {
	s := e.scope(e.projectPath)
	enabled := s.evaluateCondition("'$(ManagePackageVersionsCentrally)' == 'true' And '$(RestoreEnableGlobalPackageReference)' != 'false'", filepath.Dir(e.projectPath))
	if enabled == isFalse {
		return
	}
	for _, global := range lists[typeGlobal] {
		conditional := global.conditional || enabled == unknown
		reference := &item{kind: typeReference, id: global.id, file: global.file, meta: map[string]*metadata{}, conditional: conditional, global: true}
		for name, m := range global.meta {
			reference.meta[name] = m
		}
		reference.meta["version"] = &metadata{value: value{Known: true}, owner: global.file}
		reference.meta["includeassets"] = &metadata{value: value{Text: "Runtime;Build;Native;contentFiles;Analyzers", Known: true}, owner: global.file}
		reference.meta["privateassets"] = &metadata{value: value{Text: "All", Known: true}, owner: global.file}
		lists[typeReference] = append(lists[typeReference], reference)
		version := &item{kind: typeVersion, id: global.id, file: global.file, meta: map[string]*metadata{"version": global.metadata("Version")}, conditional: conditional, global: true}
		lists[typeVersion] = append(lists[typeVersion], version)
	}
}

// firstWins picks the effective item for each ID as NuGet's
// CheckForDuplicateNuGetItemsTask does (CheckForDuplicateNuGetItemsTask.cs:75).
// When a conditional item comes first, the result is uncertain: the first
// unconditional item is preferred for display and marked conditional.
func firstWins(items []*item) (winners []*item, duplicates map[*item]bool, uncertain map[*item]bool) {
	duplicates = map[*item]bool{}
	uncertain = map[*item]bool{}
	groups := map[string][]*item{}
	var order []string
	for _, candidate := range items {
		key := strings.ToLower(candidate.id)
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], candidate)
	}
	for _, key := range order {
		group := groups[key]
		winner := group[0]
		if winner.conditional {
			for _, later := range group[1:] {
				if !later.conditional {
					winner = later
					break
				}
			}
			uncertain[winner] = true
		}
		if len(group) > 1 {
			duplicates[winner] = true
		}
		winners = append(winners, winner)
	}
	return winners, duplicates, uncertain
}

func isFloating(version string) bool {
	return strings.Contains(version, "*")
}

func (e *evaluator) finish(lists map[string][]*item) {
	result := e.result
	result.CentralPackageManagement = e.cpm == isTrue
	result.CentralPackageManagementKnown = e.cpm != unknown
	if e.cpm == unknown {
		e.gap("central package management needs MSBuild evaluation")
	}
	scope := e.scope(e.projectPath)
	floatingEnabled := e.value("CentralPackageFloatingVersionsEnabled")
	overrideDisabled := scope.evaluateCondition("'$(CentralPackageVersionOverrideEnabled)' == 'false'", filepath.Dir(e.projectPath))

	versions, versionDuplicates, versionUncertain := firstWins(lists[typeVersion])
	central := map[string]CentralVersion{}
	for _, candidate := range versions {
		m := candidate.metadata("Version")
		entry := CentralVersion{
			ID: candidate.id, Version: m.Text, VersionKnown: m.Known, Owner: m.owner, Global: candidate.global,
			Conditional: candidate.conditional || m.conditional || versionUncertain[candidate], Duplicate: versionDuplicates[candidate],
		}
		if entry.Owner == "" {
			entry.Owner = candidate.file
		}
		if entry.Duplicate {
			e.reason("PackageVersion %s is declared more than once (NU1506)", candidate.id)
		}
		central[strings.ToLower(candidate.id)] = entry
		result.CentralVersions = append(result.CentralVersions, entry)
	}

	references, referenceDuplicates, referenceUncertain := firstWins(lists[typeReference])
	for _, reference := range references {
		pkg := Package{
			ID: reference.id, ReferenceOwner: reference.file, Global: reference.global,
			Conditional: reference.conditional || referenceUncertain[reference], Duplicate: referenceDuplicates[reference],
			PrivateAssets: reference.metadata("PrivateAssets").Text,
			Implicit:      strings.EqualFold(reference.metadata("IsImplicitlyDefined").Text, "true"),
		}
		if pkg.Duplicate {
			e.reason("PackageReference %s is declared more than once (NU1504)", reference.id)
		}
		override := reference.metadata("VersionOverride")
		version := reference.metadata("Version")
		// VersionOverride wins over every other version, in and out of CPM
		// (NuGet MSBuildRestoreUtility.cs:741-760).
		switch {
		case override.Text != "":
			pkg.Version, pkg.VersionKnown, pkg.VersionOwner, pkg.VersionSource = override.Text, override.Known, override.owner, VersionOverride
			pkg.Conditional = pkg.Conditional || override.conditional
			if result.CentralPackageManagement && overrideDisabled == isTrue {
				pkg.Problem = "NU1013: VersionOverride is set while CentralPackageVersionOverrideEnabled is false"
			}
		case version.Text != "":
			pkg.Version, pkg.VersionKnown, pkg.VersionOwner, pkg.VersionSource = version.Text, version.Known, version.owner, VersionReference
			pkg.Conditional = pkg.Conditional || version.conditional
			if result.CentralPackageManagement && !pkg.Implicit {
				pkg.Problem = "NU1008: a PackageReference with central package management cannot set Version; use VersionOverride or a PackageVersion"
			}
		case result.CentralPackageManagement && !pkg.Implicit:
			entry, ok := central[strings.ToLower(reference.id)]
			if !ok {
				pkg.VersionKnown = true
				pkg.Problem = "NU1010: no PackageVersion is defined for this centrally managed package"
				break
			}
			pkg.Version, pkg.VersionKnown, pkg.VersionOwner = entry.Version, entry.VersionKnown, entry.Owner
			pkg.VersionSource = VersionCentral
			if entry.Global {
				pkg.VersionSource = VersionGlobal
			}
			pkg.Conditional = pkg.Conditional || entry.Conditional
			if isFloating(entry.Version) && !(floatingEnabled.Known && strings.EqualFold(floatingEnabled.Text, "true")) {
				pkg.Problem = "NU1011: floating central versions need CentralPackageFloatingVersionsEnabled=true"
			}
		default:
			pkg.VersionKnown = true
		}
		result.References = append(result.References, pkg)
	}

	for _, download := range lists[typeDownload] {
		m := download.metadata("Version")
		result.Downloads = append(result.Downloads, Download{ID: download.id, Version: m.Text, Owner: download.file, Conditional: download.conditional || m.conditional})
	}
	for _, reference := range lists[typeProjectRef] {
		result.ProjectReferences = appendUnique(result.ProjectReferences, reference.id)
	}
}
