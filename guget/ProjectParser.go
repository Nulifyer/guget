package main

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
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
