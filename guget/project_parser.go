package main

import (
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ImportElement struct {
	Project   string `xml:"Project,attr"`
	Condition string `xml:"Condition,attr"`
}

type Project struct {
	SDK         string `xml:"Sdk,attr"`
	SDKElements []struct {
		Name string `xml:"Name,attr"`
	} `xml:"Sdk"`
	XMLName        xml.Name        `xml:"Project"`
	PropertyGroups []PropertyGroup `xml:"PropertyGroup"`
	ItemGroups     []ItemGroup     `xml:"ItemGroup"`
	Imports        []ImportElement `xml:"Import"`
}

// PropertyGroup captures both the well-known TargetFramework fields and any
// arbitrary MSBuild properties defined inline (e.g. OTelLatestStableVer).
// The custom unmarshaller is needed because encoding/xml cannot map arbitrary
// element names to a map with a struct tag alone.
type PropertyGroup struct {
	TargetFramework          string
	TargetFrameworks         string
	Properties               map[string]string // all other child elements
	Condition                string
	HasConditionalProperties bool
}

func (pg *PropertyGroup) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for _, attr := range start.Attr {
		if attr.Name.Local == "Condition" {
			pg.Condition = attr.Value
			break
		}
	}
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			for _, attr := range t.Attr {
				if attr.Name.Local == "Condition" && strings.TrimSpace(attr.Value) != "" {
					pg.HasConditionalProperties = true
				}
			}
			var value string
			if err := d.DecodeElement(&value, &t); err != nil {
				return err
			}
			switch t.Name.Local {
			case "TargetFramework":
				pg.TargetFramework = value
			case "TargetFrameworks":
				pg.TargetFrameworks = value
			default:
				if pg.Properties == nil {
					pg.Properties = make(map[string]string)
				}
				pg.Properties[t.Name.Local] = value
			}
		case xml.EndElement:
			return nil
		}
	}
}

type ItemGroup struct {
	Condition         string                `xml:"Condition,attr"`
	PackageReferences []rawPackageReference `xml:"PackageReference"`
	PackageVersions   []rawPackageReference `xml:"PackageVersion"`
}

// rawPackageReference is used only for XML unmarshalling.
// Both Include (new entry) and Update (modify existing) are captured so that
// unconditional Update elements are not silently dropped.
type rawPackageReference struct {
	Condition       string `xml:"Condition,attr"`
	Include         string `xml:"Include,attr"`
	Update          string `xml:"Update,attr"`
	Version         string `xml:"Version,attr"`
	VersionOverride string `xml:"VersionOverride,attr"`
	VersionChild    string `xml:"Version"`
	OverrideChild   string `xml:"VersionOverride"`
}

func (r rawPackageReference) versionExpression() string {
	for _, value := range []string{r.VersionOverride, r.OverrideChild, r.Version, r.VersionChild} {
		if value != "" {
			return value
		}
	}
	return ""
}

// effectiveName returns the package name from Include, falling back to Update.
func (r rawPackageReference) effectiveName() string {
	if r.Include != "" {
		return r.Include
	}
	return r.Update
}

// buildPropsMap merges all user-defined properties from a slice of PropertyGroups
// into a single flat map for $(PropName) resolution.
func buildPropsMap(groups []PropertyGroup) map[string]string {
	props := make(map[string]string)
	for _, pg := range groups {
		for k, v := range pg.Properties {
			props[k] = v
		}
	}
	return props
}

// resolveProps replaces $(Name) references in s using props.
func resolveProps(s string, props map[string]string) string {
	if !strings.Contains(s, "$(") {
		return s
	}
	for k, v := range props {
		s = strings.ReplaceAll(s, "$("+k+")", v)
	}
	return s
}

// PackageReference is the parsed, usable form with a real SemVer.
type PackageReference struct {
	Name      string
	Version   SemVer
	Requested string // original requested expression before SemVer range normalization
	Locked    bool   // true when the version was specified as [x.y.z] exact pin in the project file
}

// isExactLock reports whether a raw version string is a NuGet exact-version pin ([x.y.z]).
func isExactLock(s string) bool {
	return len(s) > 2 && s[0] == '[' && s[len(s)-1] == ']' && !strings.ContainsRune(s, ',')
}

type AddTargetKind int

const (
	AddTargetProject       AddTargetKind = iota // .csproj/.fsproj
	AddTargetBuildProps                         // Directory.Build.props
	AddTargetCPM                                // Directory.Packages.props (CPM)
	AddTargetImportedProps                      // Explicitly imported .props
)

type AddTarget struct {
	FilePath    string
	Kind        AddTargetKind
	Description string // e.g., "this project only", "all projects under /path"
}

type ParsedProject struct {
	FileName               string
	RequiresEvaluation     bool // conditional declarations/imports cannot establish active ownership from XML alone
	DiscoveryErrors        []error
	FilePath               string // full path to the .csproj/.fsproj file
	TargetFrameworks       Set[TargetFramework]
	Packages               Set[PackageReference]
	PackageSources         map[string]string            // lowercase pkg name → absolute path of defining file
	InputRevisions         map[string][sha256.Size]byte // exact bytes consumed by project/import parsing
	PackageOwnerIdentities map[string]string            // recorded owner path → canonical file identity, resolved during discovery
	AddTargets             []AddTarget                  // possible locations for adding new packages
}

// SourceFileForPackage returns the file path where pkgName is defined.
// Falls back to the project's own FilePath if no source is recorded.
func (pp *ParsedProject) SourceFileForPackage(pkgName string) string {
	if source, ok := pp.PackageSources[strings.ToLower(pkgName)]; ok {
		return source
	}
	return pp.FilePath
}

func ParseCsproj(filePath string) (*ParsedProject, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var project Project
	if err := xml.Unmarshal(data, &project); err != nil {
		return nil, fmt.Errorf("failed to parse XML: %w", err)
	}

	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		logWarn("filepath.Abs(%s): %v", filePath, err)
		absFilePath = filePath
	}

	result := &ParsedProject{
		FileName:         filepath.Base(filePath),
		FilePath:         filePath,
		TargetFrameworks: NewSet[TargetFramework](),
		Packages:         NewSet[PackageReference](),
		PackageSources:   make(map[string]string),
	}

	if err := recordProjectInput(result, absFilePath, data); err != nil {
		return nil, err
	}
	result.RequiresEvaluation = projectNeedsEvaluation(project)
	mergePropertyGroups(result, project.PropertyGroups)

	projectDir := filepath.Dir(filePath)
	visited := map[string]bool{absFilePath: true}
	dbp := findDirectoryBuildProps(projectDir)

	// Build CPM version map from Directory.Packages.props if present.
	// CPM projects declare <PackageReference Include="Pkg" /> without a Version;
	// the version is defined centrally as <PackageVersion Include="Pkg" Version="x" />.
	if central := findDirectoryPackagesProps(projectDir); central != "" {
		markProjectEvaluation(result, central)
	}
	cpmVersions := make(map[string]string) // lowercase name → version string
	var cpmFilePath string
	if dpp := activeDirectoryPackagesProps(projectDir, project.PropertyGroups, dbp, result); dpp != "" {
		if absDpp, err := filepath.Abs(dpp); err == nil {
			cpmFilePath = absDpp
			if refs, _, _, err := parsePropsFileRecording(absDpp, result); err == nil {
				markProjectEvaluation(result, absDpp)
				for _, r := range refs {
					if r.Version != "" {
						cpmVersions[strings.ToLower(r.Include)] = r.Version
					}
				}
			} else {
				return nil, fmt.Errorf("central package file %s: %w", absDpp, err)
			}
		}
	}

	for _, ig := range project.ItemGroups {
		for _, raw := range ig.PackageReferences {
			name := raw.effectiveName()
			if name == "" {
				continue
			}
			version := raw.versionExpression()
			sourceFile := filePath
			switch {
			case version != "":
				// Explicit Version in the project file — use as-is.
			case raw.VersionOverride != "":
				// VersionOverride pins a project-specific version in a CPM repo;
				// the override lives in the project file, not the central props.
				version = raw.VersionOverride
			case cpmFilePath != "":
				// No version specified — resolve from Directory.Packages.props.
				if cpmVer, ok := cpmVersions[strings.ToLower(name)]; ok {
					version = cpmVer
					sourceFile = cpmFilePath
				}
			}
			result.Packages.Add(PackageReference{
				Name:      name,
				Version:   ParseSemVer(version),
				Requested: version,
				Locked:    isExactLock(version),
			})
			result.PackageSources[strings.ToLower(name)] = sourceFile
		}
	}

	// Implicit import: Directory.Build.props (walk up from project dir)
	if dbp != "" {
		collectPropsPackages(result, dbp, projectDir, visited)
	}

	// Explicit <Import> elements in the project file
	var resolvedImports []string
	for _, imp := range project.Imports {
		resolved, err := resolveImportPath(imp.Project, projectDir, projectDir)
		if err != nil {
			logDebug("Skipping import in %s: %v", filePath, err)
			continue
		}
		if _, err := os.Stat(resolved); os.IsNotExist(err) && imp.Condition != "" {
			continue
		}
		collectPropsPackages(result, resolved, projectDir, visited)
		resolvedImports = append(resolvedImports, resolved)
	}

	// Post-process: imported props files (e.g. Directory.Build.props) may also
	// reference packages without versions in CPM repos. Fill in any that are
	// still empty using the central version map, and redirect their source to
	// the CPM file so the TUI points edits to the right place.
	if cpmFilePath != "" && len(cpmVersions) > 0 {
		var emptyRefs []PackageReference
		for ref := range result.Packages {
			if ref.Version.Raw == "" {
				emptyRefs = append(emptyRefs, ref)
			}
		}
		for _, ref := range emptyRefs {
			name := strings.ToLower(ref.Name)
			if cpmVer, ok := cpmVersions[name]; ok {
				result.Packages.Remove(ref)
				result.Packages.Add(PackageReference{Name: ref.Name, Version: ParseSemVer(cpmVer), Requested: cpmVer})
				result.PackageSources[name] = cpmFilePath
			}
		}
	}

	// Build AddTargets: possible locations for adding new packages.
	// Use the visited map to include ALL transitively discovered props files.
	absDBP := ""
	if dbp != "" {
		if absDBP, err = filepath.Abs(dbp); err != nil {
			logWarn("filepath.Abs(%s): %v", dbp, err)
			absDBP = dbp
		}
	}
	absCPM := ""
	if cpmFilePath != "" {
		if absCPM, err = filepath.Abs(cpmFilePath); err != nil {
			logWarn("filepath.Abs(%s): %v", cpmFilePath, err)
			absCPM = cpmFilePath
		}
	}
	directImports := make(map[string]bool)
	for _, resolved := range resolvedImports {
		abs, absErr := filepath.Abs(resolved)
		if absErr != nil {
			logWarn("filepath.Abs(%s): %v", resolved, absErr)
			abs = resolved
		}
		directImports[abs] = true
	}

	result.AddTargets = []AddTarget{
		{FilePath: absFilePath, Kind: AddTargetProject, Description: "this project only"},
	}
	if absDBP != "" {
		result.AddTargets = append(result.AddTargets, AddTarget{
			FilePath:    absDBP,
			Kind:        AddTargetBuildProps,
			Description: "all projects under " + filepath.Base(filepath.Dir(absDBP)),
		})
	}
	if absCPM != "" {
		result.AddTargets = append(result.AddTargets, AddTarget{
			FilePath:    absCPM,
			Kind:        AddTargetCPM,
			Description: "central package management",
		})
	}
	// Add all visited props files (includes both direct and transitive imports).
	// Skip files already handled above (Directory.Build.props, CPM file).
	for visitedPath := range visited {
		if visitedPath == absFilePath || visitedPath == absDBP || visitedPath == absCPM {
			continue
		}
		desc := "imported props"
		if directImports[visitedPath] {
			desc = "imported by " + result.FileName
		}
		result.AddTargets = append(result.AddTargets, AddTarget{
			FilePath:    visitedPath,
			Kind:        AddTargetImportedProps,
			Description: desc,
		})
	}

	if len(result.DiscoveryErrors) > 0 {
		return nil, fmt.Errorf("incomplete package discovery for %s: %w", filePath, errors.Join(result.DiscoveryErrors...))
	}
	cachePackageOwnerIdentities(result)
	return result, nil
}

func projectNeedsEvaluation(project Project) bool {
	if project.SDK != "" || len(project.SDKElements) > 0 || len(project.Imports) > 0 {
		return true
	}
	seen := make(map[string]bool)
	for _, group := range project.PropertyGroups {
		if group.Condition != "" || group.HasConditionalProperties {
			return true
		}
		for name, value := range group.Properties {
			if strings.EqualFold(name, "ManagePackageVersionsCentrally") && !strings.EqualFold(strings.TrimSpace(value), "true") && !strings.EqualFold(strings.TrimSpace(value), "false") {
				return true
			}
		}
	}
	for _, group := range project.ItemGroups {
		if group.Condition != "" {
			return true
		}
		for _, ref := range append(group.PackageReferences, group.PackageVersions...) {
			key := strings.ToLower(ref.effectiveName())
			if seen[key] {
				return true
			}
			seen[key] = true
			if ref.Condition != "" || ref.Update != "" || strings.ContainsAny(ref.effectiveName(), "$*;?") {
				return true
			}
		}
	}
	return false
}

func markProjectEvaluation(result *ParsedProject, path string) {
	data, err := os.ReadFile(path)
	var project Project
	if err == nil {
		err = recordProjectInput(result, path, data)
	}
	if err == nil {
		err = xml.Unmarshal(data, &project)
	}
	if err != nil {
		result.DiscoveryErrors = append(result.DiscoveryErrors, fmt.Errorf("%s: %w", path, err))
		return
	}
	result.RequiresEvaluation = result.RequiresEvaluation || projectNeedsEvaluation(project)
}

// mergePropertyGroups extracts target frameworks from PropertyGroup elements.
func mergePropertyGroups(result *ParsedProject, groups []PropertyGroup) {
	for _, pg := range groups {
		for _, fw := range strings.Split(pg.TargetFramework+";"+pg.TargetFrameworks, ";") {
			fw = strings.TrimSpace(fw)
			if fw != "" {
				result.TargetFrameworks.Add(ParseTargetFramework(fw))
			}
		}
	}
}

// findDirectoryBuildProps walks up from startDir looking for Directory.Build.props.
// Returns the full path if found, or "" if not found.
func findDirectoryBuildProps(startDir string) string {
	dir := startDir
	for {
		candidate := filepath.Join(dir, "Directory.Build.props")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// findDirectoryPackagesProps walks up from startDir looking for Directory.Packages.props,
// the central file used by NuGet Central Package Management (CPM).
// Returns the full path if found, or "" if not found.
func findDirectoryPackagesProps(startDir string) string {
	dir := startDir
	for {
		candidate := filepath.Join(dir, "Directory.Packages.props")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// activeDirectoryPackagesProps returns the nearest central package file only
// when CPM is enabled by a literal, unconditional property. NuGet imports the
// central file before Directory.Build.props and the project, so later values
// override earlier ones here as they do during evaluation.
func activeDirectoryPackagesProps(startDir string, projectGroups []PropertyGroup, buildPropsPath string, recording ...*ParsedProject) string {
	dpp := findDirectoryPackagesProps(startDir)
	if dpp == "" {
		return ""
	}

	enabled := false
	known := false
	applyGroups := func(groups []PropertyGroup) {
		if value, ok := literalBoolProperty(groups, "ManagePackageVersionsCentrally"); ok {
			enabled = value
			known = true
		}
	}
	if value, ok := literalBoolPropertyFromFile(dpp, startDir, "ManagePackageVersionsCentrally", make(map[string]bool), recording...); ok {
		enabled, known = value, true
	}
	if buildPropsPath != "" {
		if value, ok := literalBoolPropertyFromFile(buildPropsPath, startDir, "ManagePackageVersionsCentrally", make(map[string]bool), recording...); ok {
			enabled, known = value, true
		}
	}
	applyGroups(projectGroups)
	if !known || !enabled {
		return ""
	}
	return dpp
}

func literalBoolPropertyFromFile(path, projectDir, name string, visited map[string]bool, recording ...*ParsedProject) (bool, bool) {
	absPath, err := filepath.Abs(path)
	if err != nil || visited[absPath] {
		return false, false
	}
	visited[absPath] = true

	var result *ParsedProject
	if len(recording) > 0 {
		result = recording[0]
	}
	_, imports, groups, err := parsePropsFileRecording(absPath, result)
	if err != nil {
		if result != nil {
			result.DiscoveryErrors = append(result.DiscoveryErrors, fmt.Errorf("property input %s: %w", absPath, err))
		}
		return false, false
	}
	value := false
	found := false
	for _, imported := range imports {
		if strings.TrimSpace(imported.Condition) != "" {
			continue
		}
		resolved, resolveErr := resolveImportPath(imported.Project, filepath.Dir(absPath), projectDir)
		if resolveErr != nil {
			resolved = resolveGetPathOfFileAboveImport(imported.Project, filepath.Dir(absPath))
		}
		if resolved == "" {
			continue
		}
		if importedValue, ok := literalBoolPropertyFromFile(resolved, projectDir, name, visited, recording...); ok {
			value, found = importedValue, true
		}
	}
	if localValue, ok := literalBoolProperty(groups, name); ok {
		value, found = localValue, true
	}
	return value, found
}

func resolveGetPathOfFileAboveImport(raw, referringFileDir string) string {
	lower := strings.ToLower(raw)
	const buildPropsCall = "getpathoffileabove('directory.build.props'"
	const packagesPropsCall = "getpathoffileabove('directory.packages.props'"
	startDir := filepath.Dir(referringFileDir)
	switch {
	case strings.Contains(lower, buildPropsCall):
		return findDirectoryBuildProps(startDir)
	case strings.Contains(lower, packagesPropsCall):
		return findDirectoryPackagesProps(startDir)
	default:
		return ""
	}
}

func literalBoolProperty(groups []PropertyGroup, name string) (bool, bool) {
	var value bool
	found := false
	for _, group := range groups {
		for property, raw := range group.Properties {
			if !strings.EqualFold(property, name) {
				continue
			}
			if strings.TrimSpace(group.Condition) != "" || group.HasConditionalProperties {
				value, found = false, true
				continue
			}
			switch strings.ToLower(strings.TrimSpace(raw)) {
			case "true":
				value, found = true, true
			case "false":
				value, found = false, true
			default:
				value, found = false, true
			}
		}
	}
	return value, found
}

// resolveImportPath resolves MSBuild-style import paths with basic variable substitution.
// referringFileDir is the directory containing the file with the <Import> element.
// projectDir is the directory of the .csproj/.fsproj being parsed.
func resolveImportPath(rawPath, referringFileDir, projectDir string) (string, error) {
	resolved := rawPath
	resolved = strings.ReplaceAll(resolved, "$(MSBuildThisFileDirectory)", referringFileDir+string(os.PathSeparator))
	resolved = strings.ReplaceAll(resolved, "$(ProjectDir)", projectDir+string(os.PathSeparator))
	if strings.Contains(resolved, "$(MSBuildProjectDirectory)") {
		absoluteProjectDir, err := filepath.Abs(projectDir)
		if err != nil {
			return "", fmt.Errorf("resolve project directory: %w", err)
		}
		resolved = strings.ReplaceAll(resolved, "$(MSBuildProjectDirectory)", absoluteProjectDir)
	}

	if strings.Contains(resolved, "$(") {
		return "", fmt.Errorf("unresolved MSBuild variable in import path: %s", rawPath)
	}

	// MSBuild paths often use Windows-style backslashes; normalize them so
	// import resolution works on Linux/macOS as well.
	resolved = strings.ReplaceAll(resolved, `\`, "/")
	resolved = filepath.FromSlash(resolved)
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(referringFileDir, resolved)
	}
	return filepath.Clean(resolved), nil
}

// parsePropsFile parses a .props file and returns its PackageReferences, Import
// elements, and PropertyGroups.
func parsePropsFile(filePath string) ([]rawPackageReference, []ImportElement, []PropertyGroup, error) {
	return parsePropsFileRecording(filePath, nil)
}

func parsePropsFileRecording(filePath string, result *ParsedProject) ([]rawPackageReference, []ImportElement, []PropertyGroup, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to read props file: %w", err)
	}
	if err := recordProjectInput(result, filePath, data); err != nil {
		return nil, nil, nil, err
	}
	var project Project
	if err := xml.Unmarshal(data, &project); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to parse props XML: %w", err)
	}
	// Build a property map so $(PropName) in version strings can be resolved.
	// Properties must be gathered before iterating ItemGroups because they may
	// be declared above or below the PackageVersion elements in the file.
	props := buildPropsMap(project.PropertyGroups)

	// First pass: unconditional ItemGroups — these are the canonical versions.
	// Collect conditional groups for a fallback pass below.
	var refs []rawPackageReference
	var conditionalGroups []ItemGroup
	for _, ig := range project.ItemGroups {
		if ig.Condition != "" {
			conditionalGroups = append(conditionalGroups, ig)
			continue
		}
		for _, r := range ig.PackageReferences {
			if r.effectiveName() == "" {
				continue
			}
			r.Version = resolveProps(r.versionExpression(), props)
			refs = append(refs, r)
		}
		for _, r := range ig.PackageVersions {
			if r.effectiveName() == "" {
				continue
			}
			r.Version = resolveProps(r.versionExpression(), props)
			refs = append(refs, r)
		}
	}

	// Second pass: conditional ItemGroups as a fallback for packages that have
	// no unconditional definition (e.g. target-framework-specific packages like
	// Microsoft.AspNetCore.TestHost). We cannot evaluate MSBuild conditions so
	// we use the first conditional match as a conservative version estimate.
	seen := make(map[string]bool, len(refs))
	for _, r := range refs {
		seen[strings.ToLower(r.effectiveName())] = true
	}
	for _, ig := range conditionalGroups {
		for _, r := range ig.PackageReferences {
			name := strings.ToLower(r.effectiveName())
			if name == "" || seen[name] {
				continue
			}
			r.Version = resolveProps(r.versionExpression(), props)
			refs = append(refs, r)
			seen[name] = true
		}
		for _, r := range ig.PackageVersions {
			name := strings.ToLower(r.effectiveName())
			if name == "" || seen[name] {
				continue
			}
			r.Version = resolveProps(r.versionExpression(), props)
			refs = append(refs, r)
			seen[name] = true
		}
	}

	return refs, project.Imports, project.PropertyGroups, nil
}

// collectPropsPackages parses a .props file and merges its PackageReferences
// into the result. Recurses into nested <Import> elements. Uses visited to
// prevent cycles.
func collectPropsPackages(result *ParsedProject, propsPath, projectDir string, visited map[string]bool) {
	absPath, err := filepath.Abs(propsPath)
	if err != nil {
		logWarn("Could not resolve absolute path for %s: %v", propsPath, err)
		return
	}
	if visited[absPath] {
		return
	}
	visited[absPath] = true

	markProjectEvaluation(result, absPath)
	refs, imports, propertyGroups, err := parsePropsFileRecording(absPath, result)
	if err != nil {
		result.DiscoveryErrors = append(result.DiscoveryErrors, fmt.Errorf("import %s: %w", absPath, err))
		logDebug("Failed to parse props file %s: %v", absPath, err)
		return
	}

	for _, raw := range refs {
		ref := PackageReference{
			Name:      raw.effectiveName(),
			Version:   ParseSemVer(raw.Version),
			Requested: raw.Version,
			Locked:    isExactLock(raw.Version),
		}
		result.Packages.Add(ref)
		key := strings.ToLower(raw.effectiveName())
		if _, exists := result.PackageSources[key]; exists {
			result.RequiresEvaluation = true
		}
		// Only set source if not already defined (.csproj takes precedence)
		if _, exists := result.PackageSources[key]; !exists {
			result.PackageSources[key] = absPath
		}
	}

	mergePropertyGroups(result, propertyGroups)

	// Recurse into nested imports
	propsDir := filepath.Dir(absPath)
	for _, imp := range imports {
		resolved, err := resolveImportPath(imp.Project, propsDir, projectDir)
		if err != nil {
			logDebug("Skipping nested import in %s: %v", absPath, err)
			continue
		}
		if _, err := os.Stat(resolved); os.IsNotExist(err) && imp.Condition != "" {
			continue
		}
		collectPropsPackages(result, resolved, projectDir, visited)
	}
}

// ParsePropsAsProject parses a .props file and returns a ParsedProject
// containing only the packages directly defined in that file.
func ParsePropsAsProject(filePath string) (*ParsedProject, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	result := &ParsedProject{
		FileName:         filepath.Base(absPath),
		FilePath:         absPath,
		TargetFrameworks: NewSet[TargetFramework](),
		Packages:         NewSet[PackageReference](),
		PackageSources:   make(map[string]string),
	}

	refs, _, propertyGroups, err := parsePropsFileRecording(absPath, result)
	if err != nil {
		return nil, err
	}
	mergePropertyGroups(result, propertyGroups)

	for _, raw := range refs {
		result.Packages.Add(PackageReference{
			Name:      raw.effectiveName(),
			Version:   ParseSemVer(raw.Version),
			Requested: raw.Version,
			Locked:    isExactLock(raw.Version),
		})
		result.PackageSources[strings.ToLower(raw.effectiveName())] = absPath
	}

	cachePackageOwnerIdentities(result)
	return result, nil
}

// Resolve ownership once during discovery. Render and event handlers can compare
// these identities without probing potentially slow network-mounted filesystems.
func cachePackageOwnerIdentities(project *ParsedProject) {
	project.PackageOwnerIdentities = make(map[string]string)
	for _, source := range project.PackageSources {
		if _, known := project.PackageOwnerIdentities[source]; known {
			continue
		}
		identity, err := canonicalMutationPath(source)
		if err != nil {
			identity = filepath.Clean(source)
		}
		project.PackageOwnerIdentities[source] = identity
	}
}

// Record the bytes actually consumed, not a later reread of the same path. This
// distinguishes a B-then-A editor save from parsing the stable A revision.
func recordProjectInput(project *ParsedProject, path string, data []byte) error {
	if project == nil {
		return nil
	}
	if project.InputRevisions == nil {
		project.InputRevisions = make(map[string][sha256.Size]byte)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if prior, known := project.InputRevisions[absolute]; known && prior != digest {
		return fmt.Errorf("%w: %s was read at multiple revisions", errWorkspaceInputsChanged, absolute)
	}
	project.InputRevisions[absolute] = digest
	return nil
}
