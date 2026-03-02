package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// writeFileRetry wraps os.WriteFile with retries to handle transient file
// locks on Windows (antivirus, IDE file watchers, indexing services).
func writeFileRetry(path string, data []byte, perm os.FileMode) error {
	const maxAttempts = 5
	var err error
	for i := range maxAttempts {
		err = os.WriteFile(path, data, perm)
		if err == nil {
			return nil
		}
		if i < maxAttempts-1 {
			logDebug("write retry %d/%d for %s: %v", i+1, maxAttempts, path, err)
			time.Sleep(time.Duration(50*(i+1)) * time.Millisecond)
		}
	}
	return err
}

type ImportElement struct {
	Project string `xml:"Project,attr"`
}

type Project struct {
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
	TargetFramework  string
	TargetFrameworks string
	Properties       map[string]string // all other child elements
}

func (pg *PropertyGroup) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
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
	Include         string `xml:"Include,attr"`
	Update          string `xml:"Update,attr"`
	Version         string `xml:"Version,attr"`
	VersionOverride string `xml:"VersionOverride,attr"`
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
	Name    string
	Version SemVer
}

type ParsedProject struct {
	FileName         string
	FilePath         string // full path to the .csproj/.fsproj file
	TargetFrameworks Set[TargetFramework]
	Packages         Set[PackageReference]
	PackageSources   map[string]string // lowercase pkg name → absolute path of defining file
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

	absFilePath, _ := filepath.Abs(filePath)

	result := &ParsedProject{
		FileName:         filepath.Base(filePath),
		FilePath:         filePath,
		TargetFrameworks: NewSet[TargetFramework](),
		Packages:         NewSet[PackageReference](),
		PackageSources:   make(map[string]string),
	}

	mergePropertyGroups(result, project.PropertyGroups)

	projectDir := filepath.Dir(filePath)
	visited := map[string]bool{absFilePath: true}

	// Build CPM version map from Directory.Packages.props if present.
	// CPM projects declare <PackageReference Include="Pkg" /> without a Version;
	// the version is defined centrally as <PackageVersion Include="Pkg" Version="x" />.
	cpmVersions := make(map[string]string) // lowercase name → version string
	var cpmFilePath string
	if dpp := findDirectoryPackagesProps(projectDir); dpp != "" {
		if absDpp, err := filepath.Abs(dpp); err == nil {
			cpmFilePath = absDpp
			if refs, _, _, err := parsePropsFile(absDpp); err == nil {
				for _, r := range refs {
					if r.Version != "" {
						cpmVersions[strings.ToLower(r.Include)] = r.Version
					}
				}
			}
		}
	}

	for _, ig := range project.ItemGroups {
		for _, raw := range ig.PackageReferences {
			version := raw.Version
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
				if cpmVer, ok := cpmVersions[strings.ToLower(raw.effectiveName())]; ok {
					version = cpmVer
					sourceFile = cpmFilePath
				}
			}
			result.Packages.Add(PackageReference{
				Name:    raw.effectiveName(),
				Version: ParseSemVer(version),
			})
			result.PackageSources[strings.ToLower(raw.effectiveName())] = sourceFile
		}
	}

	// Implicit import: Directory.Build.props (walk up from project dir)
	if dbp := findDirectoryBuildProps(projectDir); dbp != "" {
		collectPropsPackages(result, dbp, projectDir, visited)
	}

	// Explicit <Import> elements in the project file
	for _, imp := range project.Imports {
		resolved, err := resolveImportPath(imp.Project, projectDir, projectDir)
		if err != nil {
			logDebug("Skipping import in %s: %v", filePath, err)
			continue
		}
		collectPropsPackages(result, resolved, projectDir, visited)
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
				result.Packages.Add(PackageReference{Name: ref.Name, Version: ParseSemVer(cpmVer)})
				result.PackageSources[name] = cpmFilePath
			}
		}
	}

	return result, nil
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

// resolveImportPath resolves MSBuild-style import paths with basic variable substitution.
// referringFileDir is the directory containing the file with the <Import> element.
// projectDir is the directory of the .csproj/.fsproj being parsed.
func resolveImportPath(rawPath, referringFileDir, projectDir string) (string, error) {
	resolved := rawPath
	resolved = strings.ReplaceAll(resolved, "$(MSBuildThisFileDirectory)", referringFileDir+string(os.PathSeparator))
	resolved = strings.ReplaceAll(resolved, "$(ProjectDir)", projectDir+string(os.PathSeparator))

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
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to read props file: %w", err)
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
			r.Version = resolveProps(r.Version, props)
			refs = append(refs, r)
		}
		for _, r := range ig.PackageVersions {
			r.Version = resolveProps(r.Version, props)
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
			r.Version = resolveProps(r.Version, props)
			refs = append(refs, r)
			seen[name] = true
		}
		for _, r := range ig.PackageVersions {
			name := strings.ToLower(r.effectiveName())
			if name == "" || seen[name] {
				continue
			}
			r.Version = resolveProps(r.Version, props)
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

	refs, imports, propertyGroups, err := parsePropsFile(absPath)
	if err != nil {
		logDebug("Failed to parse props file %s: %v", absPath, err)
		return
	}

	for _, raw := range refs {
		ref := PackageReference{
			Name:    raw.effectiveName(),
			Version: ParseSemVer(raw.Version),
		}
		result.Packages.Add(ref)
		key := strings.ToLower(raw.effectiveName())
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

	refs, _, propertyGroups, err := parsePropsFile(absPath)
	if err != nil {
		return nil, err
	}

	result := &ParsedProject{
		FileName:         filepath.Base(absPath),
		FilePath:         absPath,
		TargetFrameworks: NewSet[TargetFramework](),
		Packages:         NewSet[PackageReference](),
		PackageSources:   make(map[string]string),
	}

	mergePropertyGroups(result, propertyGroups)

	for _, raw := range refs {
		result.Packages.Add(PackageReference{
			Name:    raw.effectiveName(),
			Version: ParseSemVer(raw.Version),
		})
		result.PackageSources[strings.ToLower(raw.effectiveName())] = absPath
	}

	return result, nil
}

var versionAttrRe = regexp.MustCompile(`(Version\s*=\s*")[^"]*(")`)
// RemovePackageReference removes a <PackageReference> line for pkgName from a
// .csproj/.fsproj file without altering any other formatting.
func RemovePackageReference(filePath, pkgName string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	pkgNameRe := regexp.MustCompile(`(?i)Include\s*=\s*"` + regexp.QuoteMeta(pkgName) + `"`)

	lines := strings.Split(string(data), "\n")
	changed := false
	out := lines[:0] // reuse the backing array in-place to avoid an extra allocation
	for _, line := range lines {
		if pkgNameRe.MatchString(line) {
			changed = true
			continue
		}
		out = append(out, line)
	}

	if !changed {
		return nil
	}

	return writeFileRetry(filePath, []byte(strings.Join(out, "\n")), 0644)
}


// UpdatePackageVersion rewrites the Version attribute for a specific
// PackageReference in a .csproj/.fsproj file without altering any other
// formatting.
func UpdatePackageVersion(filePath, pkgName, newVersion string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	pkgNameRe := regexp.MustCompile(`(?i)Include\s*=\s*"` + regexp.QuoteMeta(pkgName) + `"`)

	lines := strings.Split(string(data), "\n")
	changed := false
	for i, line := range lines {
		if pkgNameRe.MatchString(line) {
			updated := versionAttrRe.ReplaceAllString(line, "${1}"+newVersion+"${2}")
			if updated != line {
				lines[i] = updated
				changed = true
			}
		}
	}

	if !changed {
		return nil
	}

	return writeFileRetry(filePath, []byte(strings.Join(lines, "\n")), 0644)
}

// AddPackageReference inserts a new <PackageReference Include="pkgName" Version="version" />
// into a .csproj/.fsproj file without altering any other formatting.
func AddPackageReference(filePath, pkgName, version string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", filePath, err)
	}

	lines := strings.Split(string(data), "\n")

	pkgRefRe        := regexp.MustCompile(`(?i)<PackageReference`)
	itemGroupOpenRe := regexp.MustCompile(`(?i)<ItemGroup`)
	itemGroupCloseRe := regexp.MustCompile(`(?i)</ItemGroup>`)
	projectCloseRe  := regexp.MustCompile(`(?i)</Project>`)

	// Detect indentation from the first existing PackageReference line.
	indent := "  "
	for _, line := range lines {
		if pkgRefRe.MatchString(line) {
			trimmed := strings.TrimLeft(line, " \t")
			indent = line[:len(line)-len(trimmed)]
			break
		}
	}

	newLine := indent + fmt.Sprintf(`<PackageReference Include="%s" Version="%s" />`, pkgName, version)

	// Stack-scan to find an ItemGroup that already contains PackageReferences.
	type igState struct {
		openLine  int
		hasPkgRef bool
	}
	var stack []igState
	insertAt := -1
	for i, line := range lines {
		if itemGroupOpenRe.MatchString(line) {
			stack = append(stack, igState{openLine: i})
		} else if pkgRefRe.MatchString(line) && len(stack) > 0 {
			stack[len(stack)-1].hasPkgRef = true
		} else if itemGroupCloseRe.MatchString(line) && len(stack) > 0 {
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top.hasPkgRef {
				insertAt = i
				break
			}
		}
	}

	if insertAt >= 0 {
		// Insert before the closing </ItemGroup>.
		lines = append(lines[:insertAt], append([]string{newLine}, lines[insertAt:]...)...)
	} else {
		// No PackageReference ItemGroup found — create a new one before </Project>.
		outerIndent := ""
		if len(indent) >= 2 {
			outerIndent = indent[:len(indent)-2]
		}
		newBlock := []string{
			outerIndent + "<ItemGroup>",
			newLine,
			outerIndent + "</ItemGroup>",
		}
		inserted := false
		for i, line := range lines {
			if projectCloseRe.MatchString(line) {
				lines = append(lines[:i], append(newBlock, lines[i:]...)...)
				inserted = true
				break
			}
		}
		if !inserted {
			return fmt.Errorf("could not find insertion point in %s", filePath)
		}
	}

	return writeFileRetry(filePath, []byte(strings.Join(lines, "\n")), 0644)
}
