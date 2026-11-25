package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nulifyer/guget/internal/msbuildstatic"
	"github.com/nulifyer/guget/internal/projectedit"
)

// canEditLiteralDeclaration decides whether a TUI version edit or removal can
// use the owner static evaluation found instead of MSBuild evaluation
// (mutationOptions.staticOwnership; the CLI always evaluates). Static
// evaluation is the authority when it has no gaps and every consumed file
// holds only unconditional, literal declarations it models: one reference and
// one version owner. Without CPM the version is the reference's Version; under
// CPM it is the central PackageVersion, or a VersionOverride in the file that
// declares the reference, which wins over the central version (NU1013 is
// refused before this check). Anything else, such as a condition, Choose,
// Update, Remove, or a property-valued version, keeps MSBuild evaluation.
// Generated NuGet build imports can affect evaluation without changing which
// file declares the version, so they do not prevent the edit. Imports outside
// the workspace that static evaluation cannot see are checked separately, once
// per plan (unmodeledMSBuildImports). Callers still check that the consumed
// revisions are current and apply the normal transaction.
func canEditLiteralDeclaration(project *ParsedProject, packageID string) bool {
	if len(project.InputRevisions) == 0 || len(project.DiscoveryErrors) != 0 {
		return false
	}
	declaration, declared := project.Declarations[strings.ToLower(packageID)]
	if !declared || declaration.Conditional || declaration.Duplicate {
		return false
	}
	override := declaration.VersionSource == msbuildstatic.VersionOverride
	// Static evaluation reads Directory.Build.targets and the other implicit
	// imports. When it could not decide an import, switch, or condition, the
	// recorded inputs may be incomplete, so require MSBuild evaluation.
	if len(project.StaticGaps) != 0 {
		return false
	}
	projectPath, err := filepath.Abs(project.FilePath)
	if err != nil {
		return false
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
			resolved, ok := msbuildstatic.ImportPath(imported.Project, path, projectPath)
			if !ok || !inputs[mutationInputKey(resolved)] {
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
					// The override is the effective version in and out of CPM.
					// Require it to be the only version on the reference.
					if !override || (ref.VersionOverride != "") == (ref.OverrideChild != "") || ref.Version != "" || ref.VersionChild != "" {
						return false
					}
					versions++
					if mutationInputKey(path) != owner || projectedit.ValidateVersion(ref.versionExpression()) != nil {
						return false
					}
					continue
				}
				if override {
					return false // the override is on another reference
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
			for _, ref := range group.GlobalPackageReferences {
				if !strings.EqualFold(ref.effectiveName(), packageID) {
					continue
				}
				// A GlobalPackageReference is both the reference and the version.
				references++
				versions++
				if mutationInputKey(path) != owner || projectedit.ValidateVersion(ref.versionExpression()) != nil {
					return false
				}
			}
			for _, ref := range group.PackageVersions {
				if !strings.EqualFold(ref.effectiveName(), packageID) {
					continue
				}
				if override {
					continue // shadowed by the VersionOverride; restore ignores it
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

// literalModelNames are the element and attribute names the declared Project
// model and simpleLiteralUpdateInput read, by lowercase name. The model
// matches them exactly, but MSBuild item types and metadata names ignore case,
// so <packageReference Update="..."> is an Update the model would not see.
var literalModelNames = func() map[string]string {
	names := map[string]string{}
	for _, name := range []string{
		"Project", "PropertyGroup", "ItemGroup", "Import", "ImportGroup", "Choose", "Sdk",
		"PackageReference", "PackageVersion", "GlobalPackageReference",
		"Include", "Update", "Remove", "Condition", "Version", "VersionOverride", "IsImplicitlyDefined",
	} {
		names[strings.ToLower(name)] = name
	}
	return names
}()

// literalModelName reports whether name, spelled as written, is how the
// declared model reads it: a model name in its own spelling, or a name the
// model does not read.
func literalModelName(name string) bool {
	canonical, modeled := literalModelNames[strings.ToLower(name)]
	return !modeled || canonical == name
}

// Reject constructs that the declared Project model does not represent, rather
// than interpreting an omitted Choose, ImportGroup, or item removal as harmless.
// A model name spelled in another case, such as <packageReference>, needs
// MSBuild evaluation too.
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
			if !literalModelName(token.Name.Local) {
				return false
			}
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
				if !literalModelName(attr.Name.Local) {
					return false
				}
				if attr.Name.Local == "IsImplicitlyDefined" {
					return false
				}
				if attr.Name.Local == "Condition" && strings.TrimSpace(attr.Value) != "" {
					return false
				}
				if token.Name.Local == "Import" && attr.Name.Local == "Sdk" {
					return false
				}
				if (token.Name.Local == "PackageReference" || token.Name.Local == "PackageVersion" || token.Name.Local == "GlobalPackageReference") &&
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
