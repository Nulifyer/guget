package main

import (
	"crypto/sha256"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nulifyer/guget/internal/msbuildstatic"
)

type ImportElement struct {
	Project   string `xml:"Project,attr"`
	Condition string `xml:"Condition,attr"`
}

// Project is the literal XML shape of one project or props file. Discovery
// uses internal/msbuildstatic; this shape serves single-file views and the
// TUI's literal-edit checks.
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
	Condition               string                `xml:"Condition,attr"`
	PackageReferences       []rawPackageReference `xml:"PackageReference"`
	PackageVersions         []rawPackageReference `xml:"PackageVersion"`
	GlobalPackageReferences []rawPackageReference `xml:"GlobalPackageReference"`
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
	global          bool
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
	// PrivateAssets is the reference's PrivateAssets metadata; NuGet sets
	// "All" for a GlobalPackageReference (NuGet.targets:138-162).
	PrivateAssets string
	Global        bool // declared by a GlobalPackageReference
}

// RequestedRange parses the reference's version expression with NuGet's
// VersionRange rules, so callers can ask for its lower bound, whether that
// bound is a prerelease, or whether it floats.
func (r PackageReference) RequestedRange() (VersionRange, bool) {
	expression := r.Requested
	if expression == "" {
		expression = r.Version.Raw
	}
	return ParseVersionRange(expression)
}

// PackageDeclaration records where a package's reference and version are
// declared, as static evaluation determined them.
type PackageDeclaration struct {
	ReferenceOwner string // file with the PackageReference or GlobalPackageReference
	VersionOwner   string // file that sets the effective version
	VersionSource  msbuildstatic.VersionSource
	Global         bool
	Conditional    bool
	Duplicate      bool
	// Problem is a restore error for this reference, such as NU1008 for a
	// Version on a centrally managed reference. Version edits are refused.
	Problem string
}

// PackageDownload is a restore-only PackageDownload item. It is shown but
// never edited: it is not a dependency and is not centrally managed.
type PackageDownload struct {
	Name      string
	Requested string
	Owner     string
}

// LockSetting is a statically evaluated boolean restore property.
type LockSetting int

const (
	LockSettingFalse LockSetting = iota
	LockSettingTrue
	LockSettingUnknown // set conditionally or from SDK-defined properties
)

// LockFileSettings describe a project's packages.lock.json use
// (NuGet PackagesLockFileUtilities.cs:21-62).
type LockFileSettings struct {
	Path         string // the lock file NuGet would use
	WithLockFile LockSetting
	LockedMode   LockSetting
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
	FileName           string
	RequiresEvaluation bool // conditional declarations/imports cannot establish active ownership from XML alone
	// StaticGaps lists what static evaluation could not decide, such as an
	// unresolved import or an undecidable condition. See msbuildstatic.Gaps.
	StaticGaps             []string
	DiscoveryErrors        []error
	FilePath               string // full path to the .csproj/.fsproj file
	TargetFrameworks       Set[TargetFramework]
	Packages               Set[PackageReference]
	PackageSources         map[string]string            // lowercase pkg name → absolute path of the file declaring its version
	InputRevisions         map[string][sha256.Size]byte // exact bytes consumed by project/import parsing
	PackageOwnerIdentities map[string]string            // recorded owner path → canonical file identity, resolved during discovery
	AddTargets             []AddTarget                  // possible locations for adding new packages

	// Declarations holds reference and version owners by lowercase package ID.
	Declarations map[string]PackageDeclaration
	// CentralVersions holds the effective PackageVersion entries (first
	// wins) by lowercase package ID when central package management is on.
	CentralVersions map[string]msbuildstatic.CentralVersion
	// CentralFloatingVersions is CentralPackageFloatingVersionsEnabled; without
	// it NuGet rejects floating central versions (NU1011).
	CentralFloatingVersions bool
	Downloads               []PackageDownload
	ProjectReferences       []string // absolute paths of ProjectReference items
	LockFile                LockFileSettings

	// MSBuild source overrides, statically evaluated from Directory.Build.props,
	// imports, and the project file. RestoreSources is nil when unset.
	RestoreSources                  *string
	RestoreAdditionalProjectSources string
}

// SourceFileForPackage returns the file path where pkgName is defined.
// Falls back to the project's own FilePath if no source is recorded.
func (pp *ParsedProject) SourceFileForPackage(pkgName string) string {
	if source, ok := pp.PackageSources[strings.ToLower(pkgName)]; ok {
		return source
	}
	return pp.FilePath
}

// ReferenceFileForPackage returns the file declaring pkgName's reference,
// which can differ from its version owner under central package management.
func (pp *ParsedProject) ReferenceFileForPackage(pkgName string) string {
	if declaration, ok := pp.Declarations[strings.ToLower(pkgName)]; ok && declaration.ReferenceOwner != "" {
		return declaration.ReferenceOwner
	}
	return pp.SourceFileForPackage(pkgName)
}

// ParseCsproj statically evaluates a project's package declarations. See
// internal/msbuildstatic for the MSBuild and NuGet rules it applies.
func ParseCsproj(filePath string) (*ParsedProject, error) {
	evaluation, err := msbuildstatic.Evaluate(filePath, msbuildstatic.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", filePath, err)
	}
	absFilePath := evaluation.ProjectPath
	// Owners inside the project keep the caller's spelling of its path.
	owner := func(path string) string {
		if path == absFilePath {
			return filePath
		}
		return path
	}

	result := &ParsedProject{
		FileName:           filepath.Base(filePath),
		FilePath:           filePath,
		RequiresEvaluation: len(evaluation.Reasons) > 0,
		StaticGaps:         evaluation.Gaps,
		DiscoveryErrors:    evaluation.Errors,
		TargetFrameworks:   NewSet[TargetFramework](),
		Packages:           NewSet[PackageReference](),
		PackageSources:     make(map[string]string),
		InputRevisions:     evaluation.Inputs,
		Declarations:       make(map[string]PackageDeclaration),
		CentralVersions:    make(map[string]msbuildstatic.CentralVersion),
		ProjectReferences:  evaluation.ProjectReferences,
	}
	for _, reason := range evaluation.Reasons {
		logDebug("%s: static evaluation: %s", filePath, reason)
	}
	for _, framework := range evaluation.TargetFrameworks() {
		result.TargetFrameworks.Add(ParseTargetFramework(framework))
	}

	for _, pkg := range evaluation.References {
		if pkg.Implicit {
			continue
		}
		result.Packages.Add(PackageReference{
			Name:          pkg.ID,
			Version:       requestedVersion(pkg.Version),
			Requested:     pkg.Version,
			Locked:        isExactLock(pkg.Version),
			PrivateAssets: pkg.PrivateAssets,
			Global:        pkg.Global,
		})
		key := strings.ToLower(pkg.ID)
		versionOwner := pkg.VersionOwner
		if versionOwner == "" {
			versionOwner = pkg.ReferenceOwner
		}
		result.PackageSources[key] = owner(versionOwner)
		result.Declarations[key] = PackageDeclaration{
			ReferenceOwner: owner(pkg.ReferenceOwner), VersionOwner: owner(pkg.VersionOwner), VersionSource: pkg.VersionSource,
			Global: pkg.Global, Conditional: pkg.Conditional, Duplicate: pkg.Duplicate, Problem: pkg.Problem,
		}
	}
	if evaluation.CentralPackageManagement {
		for _, entry := range evaluation.CentralVersions {
			result.CentralVersions[strings.ToLower(entry.ID)] = entry
		}
	}
	for _, download := range evaluation.Downloads {
		result.Downloads = append(result.Downloads, PackageDownload{Name: download.ID, Requested: download.Version, Owner: owner(download.Owner)})
	}
	if text, known, _ := evaluation.Property("CentralPackageFloatingVersionsEnabled"); known && strings.EqualFold(text, "true") {
		result.CentralFloatingVersions = true
	}
	result.LockFile = lockFileSettings(evaluation)
	result.RestoreSources, result.RestoreAdditionalProjectSources = restoreSourceProperties(filePath, evaluation)
	result.AddTargets = addTargets(evaluation, result.FileName)

	if len(result.DiscoveryErrors) > 0 {
		return nil, fmt.Errorf("incomplete package discovery for %s: %w", filePath, errors.Join(result.DiscoveryErrors...))
	}
	cachePackageOwnerIdentities(result)
	return result, nil
}

// addTargets lists where a new reference can go: the project, the
// Directory.Build.props it imports, its central file under CPM, and .props
// files the project or Directory.Build.props import. Files reached through
// the central file or Directory.Build.targets are not add locations.
func addTargets(evaluation *msbuildstatic.Evaluation, projectName string) []AddTarget {
	targets := []AddTarget{{FilePath: evaluation.ProjectPath, Kind: AddTargetProject, Description: "this project only"}}
	if evaluation.DirectoryBuildProps != "" {
		targets = append(targets, AddTarget{
			FilePath:    evaluation.DirectoryBuildProps,
			Kind:        AddTargetBuildProps,
			Description: "all projects under " + filepath.Base(filepath.Dir(evaluation.DirectoryBuildProps)),
		})
	}
	if evaluation.CentralPackageManagement && evaluation.DirectoryPackagesProps != "" {
		targets = append(targets, AddTarget{FilePath: evaluation.DirectoryPackagesProps, Kind: AddTargetCPM, Description: "central package management"})
	}
	for _, imported := range evaluation.Imports {
		if imported.Kind != msbuildstatic.ImportExplicit && imported.Kind != msbuildstatic.ImportCustomBuildProps ||
			imported.Root != msbuildstatic.ImportExplicit && imported.Root != msbuildstatic.ImportDirectoryBuildProps && imported.Root != msbuildstatic.ImportCustomBuildProps ||
			!strings.EqualFold(filepath.Ext(imported.Path), ".props") {
			continue
		}
		description := "imported props"
		if imported.Importer == evaluation.ProjectPath {
			description = "imported by " + projectName
		}
		targets = append(targets, AddTarget{FilePath: imported.Path, Kind: AddTargetImportedProps, Description: description})
	}
	return targets
}

func lockSetting(evaluation *msbuildstatic.Evaluation, name string) LockSetting {
	text, known, set := evaluation.Property(name)
	switch {
	case !known:
		return LockSettingUnknown
	case set && strings.EqualFold(strings.TrimSpace(text), "true"):
		return LockSettingTrue
	}
	return LockSettingFalse
}

// lockFileSettings follows NuGet PackagesLockFileUtilities.GetNuGetLockFilePath:
// NuGetLockFilePath relative to the project, else packages.<project>.lock.json
// when it exists, else packages.lock.json.
func lockFileSettings(evaluation *msbuildstatic.Evaluation) LockFileSettings {
	projectDir := filepath.Dir(evaluation.ProjectPath)
	settings := LockFileSettings{
		WithLockFile: lockSetting(evaluation, "RestorePackagesWithLockFile"),
		LockedMode:   lockSetting(evaluation, "RestoreLockedMode"),
	}
	if text, known, _ := evaluation.Property("NuGetLockFilePath"); known && strings.TrimSpace(text) != "" {
		path := filepath.FromSlash(strings.ReplaceAll(strings.TrimSpace(text), `\`, "/"))
		if !filepath.IsAbs(path) {
			path = filepath.Join(projectDir, path)
		}
		settings.Path = filepath.Clean(path)
		return settings
	}
	name := strings.TrimSuffix(filepath.Base(evaluation.ProjectPath), filepath.Ext(evaluation.ProjectPath))
	named := filepath.Join(projectDir, "packages."+strings.ReplaceAll(name, " ", "_")+".lock.json")
	if _, err := os.Stat(named); err == nil {
		settings.Path = named
	} else {
		settings.Path = filepath.Join(projectDir, "packages.lock.json")
	}
	return settings
}

// restoreSourceProperties returns the final RestoreSources and
// RestoreAdditionalProjectSources values. Entries that still reference
// properties the static evaluator cannot resolve are dropped with a warning.
func restoreSourceProperties(projectPath string, evaluation *msbuildstatic.Evaluation) (*string, string) {
	values := map[string]*string{}
	for _, property := range []string{"RestoreSources", "RestoreAdditionalProjectSources"} {
		text, _, set := evaluation.Property(property)
		if !set {
			continue
		}
		var kept []string
		for _, entry := range strings.Split(text, ";") {
			if strings.Contains(entry, "$(") {
				logWarn("%s: ignoring %s entry %q that needs MSBuild evaluation", projectPath, property, strings.TrimSpace(entry))
				continue
			}
			kept = append(kept, entry)
		}
		joined := strings.Join(kept, ";")
		values[property] = &joined
	}
	additional := ""
	if values["RestoreAdditionalProjectSources"] != nil {
		additional = *values["RestoreAdditionalProjectSources"]
	}
	return values["RestoreSources"], additional
}

// projectNeedsEvaluation reports whether one file's literal XML uses
// constructs whose effect static reading cannot pin to a single declaration.
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
		for _, ref := range append(append(group.PackageReferences, group.PackageVersions...), group.GlobalPackageReferences...) {
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
	return findFileAbove(startDir, "Directory.Build.props")
}

// findDirectoryPackagesProps walks up from startDir looking for Directory.Packages.props,
// the central file used by NuGet Central Package Management (CPM).
// Returns the full path if found, or "" if not found.
func findDirectoryPackagesProps(startDir string) string {
	return findFileAbove(startDir, "Directory.Packages.props")
}

func findFileAbove(startDir, name string) string {
	for dir := startDir; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

// parsePropsFile parses a .props file and returns its PackageReferences, Import
// elements, and PropertyGroups.
func parsePropsFile(filePath string) ([]rawPackageReference, []ImportElement, []PropertyGroup, error) {
	return parsePropsFileRecording(filePath, nil)
}

// parsePropsFileRecording reads the package elements one file declares,
// without following imports. It backs the single-file props views.
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
	elements := func(group ItemGroup) []rawPackageReference {
		var refs []rawPackageReference
		refs = append(refs, group.PackageReferences...)
		refs = append(refs, group.PackageVersions...)
		for _, global := range group.GlobalPackageReferences {
			global.global = true
			refs = append(refs, global)
		}
		return refs
	}

	// First pass: unconditional ItemGroups — these are the canonical versions.
	// Collect conditional groups for a fallback pass below.
	var refs []rawPackageReference
	var conditionalGroups []ItemGroup
	for _, ig := range project.ItemGroups {
		if ig.Condition != "" {
			conditionalGroups = append(conditionalGroups, ig)
			continue
		}
		for _, r := range elements(ig) {
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
		for _, r := range elements(ig) {
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
		ref := PackageReference{
			Name:      raw.effectiveName(),
			Version:   requestedVersion(raw.Version),
			Requested: raw.Version,
			Locked:    isExactLock(raw.Version),
			Global:    raw.global,
		}
		if raw.global {
			ref.PrivateAssets = "All"
		}
		result.Packages.Add(ref)
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
