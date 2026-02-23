package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Project struct {
	XMLName        xml.Name        `xml:"Project"`
	PropertyGroups []PropertyGroup `xml:"PropertyGroup"`
	ItemGroups     []ItemGroup     `xml:"ItemGroup"`
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

	result := &ParsedProject{
		FileName:         filepath.Base(filePath),
		FilePath:         filePath,
		TargetFrameworks: NewSet[TargetFramework](),
		Packages:         NewSet[PackageReference](),
	}

	for _, pg := range project.PropertyGroups {
		for _, fw := range strings.Split(pg.TargetFramework+";"+pg.TargetFrameworks, ";") {
			fw = strings.TrimSpace(fw)
			if fw != "" {
				result.TargetFrameworks.Add(ParseTargetFramework(fw))
			}
		}
	}

	for _, ig := range project.ItemGroups {
		for _, raw := range ig.PackageReferences {
			result.Packages.Add(PackageReference{
				Name:    raw.Include,
				Version: ParseSemVer(raw.Version),
			})
		}
	}

	return result, nil
}

var versionAttrRe = regexp.MustCompile(`(Version\s*=\s*")[^"]*(")`);

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

	return os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0644)
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

	return os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0644)
}
