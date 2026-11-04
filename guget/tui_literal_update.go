package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nulifyer/guget/internal/projectedit"
)

// A TUI version edit or removal uses a unique owner in the recorded declarations
// without starting the SDK. CPM requires one versionless reference and one
// central version. Ambiguous declarations are not eligible for this shortcut.
// Generated NuGet build imports can affect evaluation without preventing an edit to the
// declared version shown in the TUI. This does not guarantee the evaluated version
// or restore result. The caller still checks snapshot revisions before/after
// planning and applies the normal transaction. CLI evaluation is unchanged.
func canEditLiteralDeclaration(project *ParsedProject, packageID string) bool {
	if len(project.InputRevisions) == 0 || len(project.DiscoveryErrors) != 0 {
		return false
	}
	// The declared parser does not consume Directory.Build.targets. Its presence
	// can change ownership after the project declaration, so require evaluation.
	for dir := filepath.Dir(project.FilePath); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "Directory.Build.targets")); !os.IsNotExist(err) {
			return false
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	inputs := make(map[string]bool, len(project.InputRevisions))
	for path := range project.InputRevisions {
		inputs[mutationInputKey(path)] = true
	}
	if !inputs[mutationInputKey(project.FilePath)] {
		return false
	}
	owner := mutationInputKey(project.SourceFileForPackage(packageID))
	central := projectCPMTarget(project)
	centralOwner := central != "" && owner == mutationInputKey(central)
	if !inputs[owner] || !supportedMutationOwner(owner) {
		return false
	}
	versions, references := 0, 0
	for path, revision := range project.InputRevisions {
		data, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(data) != revision || !simpleLiteralUpdateInput(data) {
			return false
		}
		var source Project
		if xml.Unmarshal(data, &source) != nil {
			return false
		}
		switch source.SDK {
		case "", "Microsoft.NET.Sdk", "Microsoft.NET.Sdk.Web", "Microsoft.NET.Sdk.Worker", "Microsoft.NET.Sdk.Razor", "Microsoft.NET.Sdk.WindowsDesktop":
		default:
			return false
		}
		if len(source.SDKElements) != 0 {
			return false
		}
		for _, group := range source.PropertyGroups {
			for name := range group.Properties {
				name = strings.ToLower(name)
				if name == "baseintermediateoutputpath" || strings.HasPrefix(name, "msbuild") ||
					strings.HasPrefix(name, "custom") || strings.HasPrefix(name, "import") ||
					strings.HasPrefix(name, "directorybuild") || strings.HasPrefix(name, "directorypackages") {
					return false // custom SDK/import locations are not in the declared graph
				}
			}
		}
		for _, imported := range source.Imports {
			resolved, err := resolveImportPath(imported.Project, filepath.Dir(path), filepath.Dir(project.FilePath))
			if err != nil || !inputs[mutationInputKey(resolved)] {
				return false
			}
		}
		source.SDK, source.Imports = "", nil
		if projectNeedsEvaluation(source) {
			return false
		}
		for _, group := range source.ItemGroups {
			for _, ref := range group.PackageReferences {
				if !strings.EqualFold(ref.effectiveName(), packageID) {
					continue
				}
				references++
				if ref.VersionOverride != "" || ref.OverrideChild != "" {
					return false
				}
				if centralOwner {
					if ref.versionExpression() != "" {
						return false
					}
				} else {
					versions++
					if mutationInputKey(path) != owner || projectedit.ValidateVersion(ref.versionExpression()) != nil {
						return false
					}
				}
			}
			for _, ref := range group.PackageVersions {
				if !strings.EqualFold(ref.effectiveName(), packageID) {
					continue
				}
				versions++
				if !centralOwner || mutationInputKey(path) != owner || projectedit.ValidateVersion(ref.versionExpression()) != nil {
					return false
				}
			}
		}
	}
	return versions == 1 && references == 1
}

// Reject constructs that the declared Project model does not represent, rather
// than interpreting an omitted Choose, ImportGroup, or item removal as harmless.
func simpleLiteralUpdateInput(data []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		switch token := token.(type) {
		case xml.StartElement:
			if token.Name.Local == "IsImplicitlyDefined" {
				return false
			}
			if depth == 1 {
				switch token.Name.Local {
				case "PropertyGroup", "ItemGroup", "Import":
				default:
					return false
				}
			}
			for _, attr := range token.Attr {
				if attr.Name.Local == "IsImplicitlyDefined" {
					return false
				}
				if attr.Name.Local == "Condition" && strings.TrimSpace(attr.Value) != "" {
					return false
				}
				if token.Name.Local == "Import" && attr.Name.Local == "Sdk" {
					return false
				}
				if (token.Name.Local == "PackageReference" || token.Name.Local == "PackageVersion") &&
					(attr.Name.Local == "Remove" || attr.Name.Local == "Update") {
					return false
				}
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
}
