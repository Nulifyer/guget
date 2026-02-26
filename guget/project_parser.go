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

type PropertyGroup struct {
	TargetFramework  string `xml:"TargetFramework"`
	TargetFrameworks string `xml:"TargetFrameworks"`
}

type ItemGroup struct {
	PackageReferences []rawPackageReference `xml:"PackageReference"`
}

// rawPackageReference is used only for XML unmarshalling.
type rawPackageReference struct {
	Include string `xml:"Include,attr"`
	Version string `xml:"Version,attr"`
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

	for _, ig := range project.ItemGroups {
		for _, raw := range ig.PackageReferences {
			result.Packages.Add(PackageReference{
				Name:    raw.Include,
				Version: ParseSemVer(raw.Version),
			})
			result.PackageSources[strings.ToLower(raw.Include)] = filePath
		}
	}

	projectDir := filepath.Dir(filePath)
	visited := map[string]bool{absFilePath: true}

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
	var refs []rawPackageReference
	for _, ig := range project.ItemGroups {
		refs = append(refs, ig.PackageReferences...)
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
			Name:    raw.Include,
			Version: ParseSemVer(raw.Version),
		}
		result.Packages.Add(ref)
		key := strings.ToLower(raw.Include)
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
			Name:    raw.Include,
			Version: ParseSemVer(raw.Version),
		})
		result.PackageSources[strings.ToLower(raw.Include)] = absPath
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
