// Package msbuildstatic evaluates the package declarations of an MSBuild
// project without running MSBuild. It models the parts of evaluation that
// decide which PackageReference, PackageVersion, GlobalPackageReference and
// PackageDownload items a project has, and which file declares each value:
//
//   - the implicit imports of Directory.Build.props, Directory.Packages.props
//     and Directory.Build.targets, with their switches and path overrides
//     (msbuild Microsoft.Common.props:19-36, NuGet.props:21-39,
//     Microsoft.Common.targets:45-62);
//   - properties in evaluation order, including self-references, the
//     property functions used to find shared files, and conditions;
//   - items in a second pass that sees the final property values, with
//     Include, Update and Remove applied in order;
//   - NuGet's item rules: GlobalPackageReference expansion at NuGet.targets
//     (NuGet.targets:138-162) and first-wins duplicates (NU1504, NU1506).
//
// Anything it cannot determine is reported in Reasons, so callers can fall
// back to MSBuild evaluation. It never starts a process or uses the network.
package msbuildstatic

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Options struct {
	// LookupEnv reads environment variables, which MSBuild exposes as
	// properties. It defaults to os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// Cache, when set, shares parsed files between evaluations, such as the
	// projects of one workspace scan that import the same props files.
	Cache *ParseCache
}

// ParseCache keeps parsed project files by path and content digest. Every
// evaluation still reads each file and records the bytes it consumed; the
// cache only skips parsing identical bytes again. It is safe for concurrent
// use. Parsed trees are never modified after parsing.
type ParseCache struct {
	mu    sync.Mutex
	files map[string]cachedFile
}

type cachedFile struct {
	digest [sha256.Size]byte
	root   *node
}

// NewParseCache returns an empty cache.
func NewParseCache() *ParseCache {
	return &ParseCache{files: make(map[string]cachedFile)}
}

func (c *ParseCache) parse(path string, data []byte, digest [sha256.Size]byte) (*node, error) {
	if c == nil {
		return parseProjectXML(data)
	}
	c.mu.Lock()
	cached, ok := c.files[path]
	c.mu.Unlock()
	if ok && cached.digest == digest {
		return cached.root, nil
	}
	root, err := parseProjectXML(data)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.files[path] = cachedFile{digest: digest, root: root}
	c.mu.Unlock()
	return root, nil
}

type ImportKind int

const (
	ImportExplicit ImportKind = iota
	ImportDirectoryBuildProps
	ImportDirectoryPackagesProps
	ImportDirectoryBuildTargets
	ImportCustomBuildProps
	ImportCustomBuildTargets
)

// Import is one file imported during evaluation, in evaluation order.
type Import struct {
	Path     string
	Importer string     // file containing the Import element; "" for implicit imports
	Kind     ImportKind // how this file was imported
	// Root is the implicit import whose chain contains this file, or
	// ImportExplicit for files the project imports itself.
	Root ImportKind
	// Uncertain is set when the import condition could not be evaluated.
	Uncertain bool
}

type VersionSource int

const (
	VersionNone      VersionSource = iota
	VersionReference               // Version metadata of the PackageReference
	VersionOverride                // VersionOverride metadata of the PackageReference
	VersionCentral                 // a PackageVersion item
	VersionGlobal                  // the Version of a GlobalPackageReference
)

// Package is one effective PackageReference.
type Package struct {
	ID           string
	Version      string // requested version expression after property expansion
	VersionKnown bool   // false when Version still contains unresolved references
	// ReferenceOwner declares the reference (the GlobalPackageReference for
	// global packages). VersionOwner declares the effective version value; for
	// an item changed by Update it is the file of the last Update that set it.
	ReferenceOwner string
	VersionOwner   string
	VersionSource  VersionSource
	PrivateAssets  string
	Global         bool // from a GlobalPackageReference
	Implicit       bool // IsImplicitlyDefined
	Conditional    bool // depends on a condition the evaluator could not decide
	Duplicate      bool // other PackageReference items share the ID (NU1504; first wins)
	// Problem describes a restore error for this reference, prefixed with its
	// NuGet code, such as NU1008 for a Version on a centrally managed reference.
	Problem string
}

// CentralVersion is the effective PackageVersion for an ID (first wins).
type CentralVersion struct {
	ID           string
	Version      string
	VersionKnown bool
	Owner        string // file that set the Version value
	Global       bool   // created from a GlobalPackageReference
	Conditional  bool
	Duplicate    bool // other PackageVersion items share the ID (NU1506)
}

// Download is a PackageDownload item. Downloads are not dependencies, are
// never centrally managed, and use exact [version] ranges.
type Download struct {
	ID          string
	Version     string
	Owner       string
	Conditional bool
}

type Evaluation struct {
	ProjectPath string
	// Inputs holds the digest of the exact bytes of every file read.
	Inputs  map[string][sha256.Size]byte
	Imports []Import
	// The implicitly imported files, or "" when not imported.
	DirectoryBuildProps    string
	DirectoryPackagesProps string
	DirectoryBuildTargets  string
	// CentralPackageManagement is NuGet's _CentralPackageVersionsEnabled:
	// ManagePackageVersionsCentrally is true when NuGet.targets is evaluated
	// and the central file was imported (NuGet.targets:65).
	CentralPackageManagement      bool
	CentralPackageManagementKnown bool
	References                    []Package
	CentralVersions               []CentralVersion
	Downloads                     []Download
	ProjectReferences             []string // absolute paths
	UsesSDK                       bool
	// Reasons explains why MSBuild evaluation could differ from this result.
	// It is conservative: using an SDK, a condition, or an import is a reason.
	Reasons []string
	// Gaps is the subset of Reasons where the evaluator could not decide a
	// value: an unresolved import, an undecidable condition or switch, or an
	// unsupported construct. An evaluation without gaps read every declaration
	// MSBuild would read from these files.
	Gaps []string
	// Errors are inputs that could not be read or parsed.
	Errors []error

	props      map[string]*property
	frameworks []string
}

type property struct {
	value
	set bool
}

// Property returns a property's final value. Known is false when the value
// could not be determined statically; set is false when nothing assigned it.
func (e *Evaluation) Property(name string) (text string, known bool, set bool) {
	if p, ok := e.props[strings.ToLower(name)]; ok {
		return p.Text, p.Known, p.set
	}
	return "", true, false
}

// TargetFrameworks returns the project's frameworks: the final
// TargetFrameworks or TargetFramework value, or every value assigned anywhere
// when neither is known.
func (e *Evaluation) TargetFrameworks() []string {
	for _, name := range []string{"TargetFrameworks", "TargetFramework"} {
		if text, known, set := e.Property(name); set && known && strings.TrimSpace(text) != "" {
			return splitList(text)
		}
	}
	return append([]string(nil), e.frameworks...)
}

type itemElement struct {
	group     *node
	file      string
	uncertain bool
	marker    bool // the GlobalPackageReference expansion in NuGet.targets
}

type evaluator struct {
	projectPath string
	lookup      func(string) (string, bool)
	result      *Evaluation
	props       map[string]*property
	files       map[string]*node
	cache       *ParseCache
	imported    map[string]bool
	items       []itemElement
	uncertain   int
	root        ImportKind
	propsDone   bool
	targetsDone bool
	cpm         truth
	strict      bool // only reserved properties are known (ImportPath)
}

// Evaluate statically evaluates the project at projectPath. It returns an
// error only when the project file itself cannot be read or parsed; problems
// with imported files are reported in Errors.
func Evaluate(projectPath string, options Options) (*Evaluation, error) {
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, err
	}
	abs = filepath.Clean(abs)
	e := &evaluator{
		projectPath: abs,
		lookup:      options.LookupEnv,
		result:      &Evaluation{ProjectPath: abs, Inputs: map[string][sha256.Size]byte{}},
		props:       map[string]*property{},
		files:       map[string]*node{},
		cache:       options.Cache,
		imported:    map[string]bool{abs: true},
	}
	if e.lookup == nil {
		e.lookup = os.LookupEnv
	}
	root, err := e.load(abs)
	if err != nil {
		return nil, err
	}
	e.evaluateProject(root)
	e.evaluateItems()
	e.result.props = e.props
	return e.result, nil
}

func (e *evaluator) lookupEnv(name string) (string, bool) {
	value, ok := e.lookup(name)
	return value, ok
}

func (e *evaluator) reason(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	for _, existing := range e.result.Reasons {
		if existing == message {
			return
		}
	}
	e.result.Reasons = append(e.result.Reasons, message)
}

func (e *evaluator) gap(format string, args ...any) {
	e.reason(format, args...)
	message := fmt.Sprintf(format, args...)
	for _, existing := range e.result.Gaps {
		if existing == message {
			return
		}
	}
	e.result.Gaps = append(e.result.Gaps, message)
}

func (e *evaluator) load(path string) (*node, error) {
	if root, ok := e.files[path]; ok {
		return root, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	e.result.Inputs[path] = digest
	root, err := e.cache.parse(path, data, digest)
	if err != nil {
		return nil, err
	}
	e.files[path] = root
	return root, nil
}

func (e *evaluator) scope(file string) scope { return scope{eval: e, thisFile: file} }

func (e *evaluator) pathOfFileAbove(file, start string) string {
	file = nativePath(file)
	for dir := filepath.Clean(start); ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, file)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

func (e *evaluator) evaluateProject(root *node) {
	sdk := strings.TrimSpace(root.attrValue("Sdk")) != ""
	explicitProps, explicitTargets := false, false
	var visit func(*node)
	visit = func(parent *node) {
		for _, child := range parent.children {
			switch child.name {
			case "Sdk":
				sdk = true
			case "ImportGroup":
				visit(child)
			case "Import":
				switch implicitImportPoint(child) {
				case "props":
					explicitProps = true
				case "targets":
					explicitTargets = true
				}
			}
		}
	}
	visit(root)
	e.result.UsesSDK = sdk
	if sdk {
		e.reason("SDK imports are not read statically")
	}
	// A project without an SDK or common imports never imports the Directory
	// files in MSBuild. Earlier guget versions treated such projects as if they
	// did; keep that so plain project files stay usable.
	if sdk || !explicitProps {
		e.implicitProps()
	}
	e.walk(e.projectPath, root, isTrue)
	if sdk || !explicitTargets {
		e.implicitTargets()
	}
}

// implicitImportPoint recognizes the imports that bring in the common props
// and targets: SDK imports and the explicit imports of non-SDK projects.
func implicitImportPoint(element *node) string {
	project := strings.ToLower(strings.TrimSpace(element.attrValue("Project")))
	if strings.TrimSpace(element.attrValue("Sdk")) != "" {
		switch project {
		case "sdk.props":
			return "props"
		case "sdk.targets":
			return "targets"
		}
		return "sdk"
	}
	project = strings.ReplaceAll(project, `\`, "/")
	switch {
	case strings.HasSuffix(project, "/microsoft.common.props"):
		return "props"
	case strings.HasSuffix(project, "/microsoft.common.targets"), strings.HasSuffix(project, "/microsoft.csharp.targets"),
		strings.HasSuffix(project, "/microsoft.visualbasic.targets"), strings.HasSuffix(project, "/microsoft.fsharp.targets"):
		return "targets"
	}
	return ""
}

func (e *evaluator) walk(file string, root *node, context truth) {
	dir := filepath.Dir(file)
	s := e.scope(file)
	for _, child := range root.children {
		if strings.TrimSpace(child.attrValue("Condition")) != "" {
			e.reason("conditions in %s are evaluated statically", filepath.Base(file))
		}
		switch child.name {
		case "PropertyGroup":
			e.propertyGroup(file, child, context.and(s.evaluateCondition(child.attrValue("Condition"), dir)))
		case "ItemGroup":
			if context != isFalse {
				e.items = append(e.items, itemElement{group: child, file: file, uncertain: e.uncertain > 0 || context == unknown})
			}
		case "Import":
			e.importElement(file, child, context)
		case "ImportGroup":
			group := context.and(s.evaluateCondition(child.attrValue("Condition"), dir))
			for _, imported := range child.children {
				if imported.name == "Import" {
					e.importElement(file, imported, group)
				}
			}
		case "Choose":
			e.gap("Choose in %s needs MSBuild evaluation", filepath.Base(file))
			e.choose(file, child, context)
		case "ItemDefinitionGroup":
			for _, definition := range child.children {
				if packageItemType(definition.name) != "" {
					e.gap("ItemDefinitionGroup in %s defines package metadata", filepath.Base(file))
				}
			}
		}
	}
}

// choose takes the first When whose condition is true. Once a condition is
// unknown, the remaining branches are walked as uncertain.
func (e *evaluator) choose(file string, element *node, context truth) {
	s := e.scope(file)
	branch := context
	for _, child := range element.children {
		switch child.name {
		case "When":
			condition := s.evaluateCondition(child.attrValue("Condition"), filepath.Dir(file))
			if branch == isTrue && condition == isTrue {
				e.walk(file, child, isTrue)
				return
			}
			if condition != isFalse {
				branch = unknown
				e.walk(file, child, unknown)
			}
		case "Otherwise":
			e.walk(file, child, branch)
			return
		}
	}
}

func (e *evaluator) propertyGroup(file string, group *node, condition truth) {
	if condition == isFalse {
		return
	}
	s := e.scope(file)
	for _, element := range group.children {
		if element.name == "" {
			continue
		}
		own := condition.and(s.evaluateCondition(element.attrValue("Condition"), filepath.Dir(file)))
		if strings.TrimSpace(element.attrValue("Condition")) != "" {
			e.reason("conditions in %s are evaluated statically", filepath.Base(file))
		}
		e.setProperty(file, element.name, element.propertyText(), own)
	}
}

func (e *evaluator) setProperty(file, name, raw string, condition truth) {
	if condition == isFalse {
		return
	}
	if _, reserved := e.scope(file).reserved(name); reserved {
		return
	}
	expanded := e.scope(file).expand(raw)
	key := strings.ToLower(name)
	if key == "targetframework" || key == "targetframeworks" {
		for _, framework := range splitList(expanded.Text) {
			if !strings.Contains(framework, "$(") {
				e.result.frameworks = appendUnique(e.result.frameworks, framework)
			}
		}
	}
	if condition == unknown {
		// The assignment may or may not happen: keep the previous text, as
		// earlier guget versions skipped conditional groups, but stop
		// treating the value as known.
		e.gap("property %s depends on a condition", name)
		p := e.props[key]
		if p == nil {
			p = &property{value: value{Known: true}}
			e.props[key] = p
		}
		p.Known = false
		return
	}
	known := expanded.Known
	if e.uncertain > 0 {
		known = false
		e.gap("property %s is set in a conditional import", name)
	}
	e.props[key] = &property{value: value{Text: expanded.Text, Known: known}, set: true}
}

func (e *evaluator) importElement(file string, element *node, context truth) {
	switch implicitImportPoint(element) {
	case "props":
		e.implicitProps()
		return
	case "targets":
		e.implicitTargets()
		return
	case "sdk":
		e.gap("SDK import %q in %s is not read statically", element.attrValue("Project"), filepath.Base(file))
		return
	}
	dir := filepath.Dir(file)
	condition := context.and(e.scope(file).evaluateCondition(element.attrValue("Condition"), dir))
	if strings.TrimSpace(element.attrValue("Condition")) != "" {
		e.reason("conditions in %s are evaluated statically", filepath.Base(file))
	}
	e.reason("%s imports other files", filepath.Base(file))
	if condition == isFalse {
		return
	}
	expanded := e.scope(file).expand(element.attrValue("Project"))
	path := strings.TrimSpace(expanded.Text)
	if !expanded.Known {
		e.gap("import %q in %s needs MSBuild evaluation", element.attrValue("Project"), filepath.Base(file))
		return
	}
	if path == "" {
		return
	}
	if strings.ContainsAny(path, "*?") {
		e.gap("wildcard import %q in %s needs MSBuild evaluation", path, filepath.Base(file))
		return
	}
	e.importFile(absoluteFrom(dir, path), file, ImportExplicit, condition)
}

func (e *evaluator) importFile(path, importer string, kind ImportKind, condition truth) {
	if e.imported[path] {
		return // MSBuild skips files that are already imported (MSB4011, MSB4210)
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		if condition == isTrue {
			e.result.Errors = append(e.result.Errors, fmt.Errorf("import %s: file not found", path))
		}
		return
	}
	root, err := e.load(path)
	if err != nil {
		e.result.Errors = append(e.result.Errors, fmt.Errorf("import %s: %w", path, err))
		return
	}
	e.imported[path] = true
	chain := e.root
	if kind != ImportExplicit {
		chain = kind
	}
	e.result.Imports = append(e.result.Imports, Import{Path: path, Importer: importer, Kind: kind, Root: chain, Uncertain: condition == unknown || e.uncertain > 0})
	if condition == unknown {
		e.gap("import of %s depends on a condition", filepath.Base(path))
		e.uncertain++
		defer func() { e.uncertain-- }()
	}
	previous := e.root
	e.root = chain
	e.walk(path, root, isTrue)
	e.root = previous
}

// value reads a property for the implicit imports, which only see global
// and environment properties and what earlier files set.
func (e *evaluator) value(name string) value {
	return e.scope(e.projectPath).property(name)
}

func (e *evaluator) setDefault(name, defaultValue string) {
	if current := e.value(name); current.Known && current.Text == "" {
		e.props[strings.ToLower(name)] = &property{value: value{Text: defaultValue, Known: true}, set: true}
	}
}

func (e *evaluator) setKnown(name, text string) {
	e.props[strings.ToLower(name)] = &property{value: value{Text: text, Known: true}, set: true}
}

// implicitImport imports the file named by a path property of the common
// props or targets. Relative paths resolve against the SDK's own directory,
// which the static evaluator does not know.
func (e *evaluator) implicitImport(name string, kind ImportKind, enabled truth) string {
	path := e.value(name)
	if !path.Known {
		e.gap("%s needs MSBuild evaluation", name)
		return ""
	}
	if enabled == unknown {
		e.gap("the import switch for %s needs MSBuild evaluation", name)
		return ""
	}
	location := nativePath(strings.TrimSpace(path.Text))
	if enabled == isFalse || location == "" {
		return ""
	}
	if !filepath.IsAbs(location) {
		e.gap("%s is relative to SDK files: %s", name, location)
		return ""
	}
	location = filepath.Clean(location)
	// These imports are conditioned on Exists(), so a missing file is skipped.
	if info, err := os.Stat(location); err != nil || info.IsDir() {
		return ""
	}
	before := len(e.result.Imports)
	e.importFile(location, "", kind, isTrue)
	if len(e.result.Imports) > before {
		return location
	}
	return ""
}

func (e *evaluator) switchEnabled(name string) truth {
	switchValue := e.value(name)
	if !switchValue.Known {
		return unknown
	}
	return truthOf(strings.EqualFold(switchValue.Text, "true"))
}

// implicitProps follows msbuild Microsoft.Common.props:13-36 and
// NuGet.props:21-39: Directory.Build.props, then Directory.Packages.props.
func (e *evaluator) implicitProps() {
	if e.propsDone {
		return
	}
	e.propsDone = true
	projectDir := filepath.Dir(e.projectPath)

	e.setDefault("ImportDirectoryBuildProps", "true")
	importBuildProps := e.switchEnabled("ImportDirectoryBuildProps")
	if path := e.value("DirectoryBuildPropsPath"); importBuildProps == isTrue && path.Known && path.Text == "" {
		file := e.value("_DirectoryBuildPropsFile")
		if file.Known && file.Text == "" {
			file.Text = "Directory.Build.props"
		}
		if file.Known {
			if found := e.pathOfFileAbove(file.Text, projectDir); found != "" {
				e.setKnown("_DirectoryBuildPropsBasePath", filepath.Dir(found))
				e.setKnown("DirectoryBuildPropsPath", found)
			}
		}
	}
	e.customImport("CustomBeforeDirectoryBuildProps", ImportCustomBuildProps)
	e.result.DirectoryBuildProps = e.implicitImport("DirectoryBuildPropsPath", ImportDirectoryBuildProps, importBuildProps)
	e.customImport("CustomAfterDirectoryBuildProps", ImportCustomBuildProps)

	e.setDefault("ImportDirectoryPackagesProps", "true")
	importPackagesProps := e.switchEnabled("ImportDirectoryPackagesProps")
	if path := e.value("DirectoryPackagesPropsPath"); importPackagesProps == isTrue && path.Known && path.Text == "" {
		file := e.value("_DirectoryPackagesPropsFile")
		if file.Known && file.Text == "" {
			file.Text = "Directory.Packages.props"
		}
		base := e.value("_DirectoryPackagesPropsBasePath")
		if file.Known && base.Known {
			if base.Text == "" {
				if found := e.pathOfFileAbove(file.Text, projectDir); found != "" {
					base.Text = filepath.Dir(found)
				}
			}
			if base.Text != "" && file.Text != "" {
				e.setKnown("_DirectoryPackagesPropsBasePath", base.Text)
				e.setKnown("DirectoryPackagesPropsPath", absoluteFrom(projectDir, combine(base.Text, file.Text)))
			}
		} else {
			e.gap("Directory.Packages.props location needs MSBuild evaluation")
		}
	}
	if central := e.implicitImport("DirectoryPackagesPropsPath", ImportDirectoryPackagesProps, importPackagesProps); central != "" {
		e.result.DirectoryPackagesProps = central
		e.setKnown("CentralPackageVersionsFileImported", "true")
	}
}

func (e *evaluator) customImport(name string, kind ImportKind) {
	path := e.value(name)
	if !path.Known {
		e.gap("%s needs MSBuild evaluation", name)
		return
	}
	if strings.TrimSpace(path.Text) == "" {
		return
	}
	location := nativePath(strings.TrimSpace(path.Text))
	if !filepath.IsAbs(location) {
		e.gap("%s is relative to SDK files: %s", name, location)
		return
	}
	// The common props import these without an Exists condition.
	e.importFile(filepath.Clean(location), "", kind, isTrue)
}

// implicitTargets marks where NuGet.targets expands GlobalPackageReference
// items, then follows msbuild Microsoft.Common.targets:45-62 for
// Directory.Build.targets, which MSBuild imports after NuGet.targets.
func (e *evaluator) implicitTargets() {
	if e.targetsDone {
		return
	}
	e.targetsDone = true
	e.propsDone = true
	projectDir := filepath.Dir(e.projectPath)

	// NuGet.targets:65 computes _CentralPackageVersionsEnabled here, before
	// Directory.Build.targets can change ManagePackageVersionsCentrally.
	e.cpm = e.switchEnabled("ManagePackageVersionsCentrally").and(e.switchEnabled("CentralPackageVersionsFileImported"))
	if e.cpm == isTrue {
		e.setKnown("_CentralPackageVersionsEnabled", "true")
	}
	e.items = append(e.items, itemElement{marker: true})

	e.setDefault("ImportDirectoryBuildTargets", "true")
	importTargets := e.switchEnabled("ImportDirectoryBuildTargets")
	if path := e.value("DirectoryBuildTargetsPath"); importTargets == isTrue && path.Known && path.Text == "" {
		file := e.value("_DirectoryBuildTargetsFile")
		if file.Known && file.Text == "" {
			file.Text = "Directory.Build.targets"
		}
		if file.Known {
			if found := e.pathOfFileAbove(file.Text, projectDir); found != "" {
				e.setKnown("_DirectoryBuildTargetsBasePath", filepath.Dir(found))
				e.setKnown("DirectoryBuildTargetsPath", found)
			}
		}
	}
	e.customImport("CustomBeforeDirectoryBuildTargets", ImportCustomBuildTargets)
	e.result.DirectoryBuildTargets = e.implicitImport("DirectoryBuildTargetsPath", ImportDirectoryBuildTargets, importTargets)
	e.customImport("CustomAfterDirectoryBuildTargets", ImportCustomBuildTargets)
}

func splitList(text string) []string {
	var values []string
	for _, part := range strings.Split(text, ";") {
		if part = strings.TrimSpace(part); part != "" {
			values = append(values, part)
		}
	}
	return values
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if strings.EqualFold(existing, value) {
			return values
		}
	}
	return append(values, value)
}
