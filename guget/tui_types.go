package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	input "github.com/nulifyer/guget/internal/ui/input"
	style "github.com/nulifyer/guget/internal/ui/style"
	viewport "github.com/nulifyer/guget/internal/ui/viewport"
)

type focusPanel int

const (
	focusProjects focusPanel = iota
	focusPackages
	focusDetail
	focusLog
)

type actionScope int

const (
	scopeSelected actionScope = iota // this project (nil when "All Projects" → becomes all)
	scopeAll                         // always all projects
)

type packageSortMode int

const (
	sortByStatus    packageSortMode = iota // status then name (default)
	sortByName                             // name only
	sortBySource                           // source then name
	sortByCurrent                          // published date of installed version (newest first)
	sortByAvailable                        // published date of best available upgrade (newest first)
)

func (s packageSortMode) label() string {
	switch s {
	case sortByName:
		return "name"
	case sortByCurrent:
		return "current"
	case sortByAvailable:
		return "available"
	case sortBySource:
		return "source"
	default:
		return "status"
	}
}

func (s packageSortMode) defaultDir() bool {
	switch s {
	case sortByCurrent, sortByAvailable:
		return false
	default:
		return true
	}
}

func (s packageSortMode) next() packageSortMode {
	return (s + 1) % 5
}

func parseSortFlag(s string) (packageSortMode, bool) {
	name, dir, _ := strings.Cut(s, ":")
	mode := parseSortMode(name)
	switch strings.ToLower(dir) {
	case "asc":
		return mode, true
	case "desc":
		return mode, false
	default:
		return mode, mode.defaultDir()
	}
}

func parseSortMode(name string) packageSortMode {
	switch strings.ToLower(name) {
	case "name":
		return sortByName
	case "current":
		return sortByCurrent
	case "available":
		return sortByAvailable
	case "source":
		return sortBySource
	default:
		return sortByStatus
	}
}

// --- Message types ---

// packageReadyMsg is sent by the background loader for each package as its
// NuGet metadata resolves, enabling progressive UI updates.
type packageReadyMsg struct {
	generation int
	name       string
	result     nugetResult
}

type packageRowsRefreshMsg struct{}

type reloadRequestedMsg struct {
	reason    string
	paths     []string
	automatic bool
	selection *reloadSelection
	saveError error
}

type workspaceReloadedMsg struct {
	generation int
	snapshot   *workspaceSnapshot
	request    reloadRequestedMsg
	err        error
}

type writeResultMsg struct {
	err       error
	summary   string
	changed   []string // files the transaction changed
	written   int      // number of changed files in the completed transaction
	skipped   int      // number of locked refs skipped during scope=all update
	reload    bool
	selection *reloadSelection
}

type restoreResultMsg struct {
	err error
	// covered lists the pending package edits this restore planned for; they
	// stop affecting later restores once it succeeds.
	covered []string
	// projects counts the restores run; affected, those the planner added
	// for a package edit; regenerated, those run with --force-evaluate;
	// failed, those that failed. only names the project of a single restore.
	projects, affected, regenerated, failed int
	only                                    string
}

type resizeDebounceMsg struct {
	id int
}

type searchDebounceMsg struct {
	requestID int
	id        int
	query     string
}

type searchResultsMsg struct {
	requestID int
	id        int
	results   []SearchResult
	query     string
	err       error
}

type packageFetchedMsg struct {
	requestID int
	info      *PackageInfo
	source    string
	err       error
}

type logBatchMsg struct {
	lines []string
}

type logRefreshMsg struct{}

type openURLRequestedMsg struct {
	url string
}

type openURLResultMsg struct {
	url string
	err error
}

type depTreeReadyMsg struct {
	requestID int
	content   string
	err       error
}

type releaseListReadyMsg struct {
	requestID   int
	releases    []GitHubRelease
	err         error
	owner       string // set when repo was discovered late (e.g. from nuspec)
	repo        string
	nuspecNotes string // piggy-backed nuspec release notes (avoids a second fetch)
	nuspecVer   string // version the nuspec notes belong to
}

type releaseNotesReadyMsg struct {
	requestID int
	tag       string
	body      string
	htmlURL   string
	err       error
}

// nuspecVersionListReadyMsg signals that the list of versions for the NuSpec
// tab is available (sourced from PackageInfo.Versions or a fresh fetch).
type nuspecVersionListReadyMsg struct {
	versions []string // version strings, newest first
	svc      *NugetService
	err      error
}

// nuspecVersionNotesReadyMsg delivers the <releaseNotes> for a single version.
type nuspecVersionNotesReadyMsg struct {
	requestID int
	version   string
	notes     string
	err       error
}

// --- Panel state types ---

type projectPanel struct {
	sectionBase // baseWidth=30, minWidth=10
	cursor      int
	scroll      int // list scroll offset
	items       []projectItem
}

type packagePanel struct {
	sectionBase
	cursor   int
	scroll   int
	rows     []packageRow
	sortMode packageSortMode
	sortDir  bool
}

type detailPanel struct {
	sectionBase // baseWidth=50, minWidth=10
	vp          viewport.Model
}

type logPanel struct {
	sectionBase
	vp viewport.Model
	// heights[i] is the number of panel rows entry i of ctx.LogLines takes at
	// wrapWidth and rows is their sum. They are kept in step with the log
	// lines while the panel is shown, and nil when they must be recounted.
	heights   []int
	rows      int
	wrapWidth int
}

// --- Overlay state types ---

type depTreeOverlay struct {
	ctx            context.Context
	cancel         context.CancelFunc
	sectionBase         // basePct=80, minWidth=40, maxMargin=4
	loading        bool // true while dotnet list is running (T key)
	content        string
	err            error
	vp             viewport.Model
	title          string
	packageVersion *PackageVersion
	dotnetProjects []dotnetListProject
}

type releaseNotesTab int

const (
	tabReleases releaseNotesTab = 0
	tabNuSpec   releaseNotesTab = 1
)

type releaseNotesOverlay struct {
	ctx         context.Context
	cancel      context.CancelFunc
	sectionBase      // basePct=85, minWidth=60, maxMargin=4
	focusRight  bool // false = left list, true = notes viewport
	vp          viewport.Model
	title       string
	activeTab   releaseNotesTab

	// GitHub tab state
	ghLoading  bool
	ghReleases []GitHubRelease
	ghCursor   int
	ghNotes    string // body of selected release
	ghNotesURL string // link to the release on GitHub
	ghErr      error
	ghOwner    string
	ghRepo     string

	// NuSpec tab state
	nsLoading    bool
	nsVersions   []string // version strings, newest first
	nsCursor     int
	nsNotes      string // <releaseNotes> for selected version
	nsErr        error
	nsSvc        *NugetService     // service to fetch nuspec notes from
	nsPkgID      string            // package ID for nuspec fetches
	nsNotesCache map[string]string // version → cached release notes (avoids re-fetching)

	// Derived state: which tabs are available (set after fetches complete)
	ghAvailable bool
	nsAvailable bool
}

type sourcesOverlay struct {
	sectionBase // baseWidth=90, minWidth=40, maxMargin=4
	vp          viewport.Model
}

type helpOverlay struct {
	sectionBase // basePct=60, minWidth=56, maxMargin=4
	vp          viewport.Model
}

// --- Data display types ---

type projectItem struct {
	name    string
	project *ParsedProject // nil = "All Projects"
}

func (p projectItem) Title() string {
	if p.project == nil {
		return "◈ All Projects"
	}
	return "◦ " + p.name
}

func (p projectItem) Description() string {
	if p.project == nil {
		return "Combined view"
	}
	var fws []string
	for fw := range p.project.TargetFrameworks {
		fws = append(fws, fw.String())
	}
	if len(fws) > 0 {
		slices.Sort(fws)
		return strings.Join(fws, ", ")
	}
	return fmt.Sprintf("%d packages", p.project.Packages.Len())
}

type packageRow struct {
	installedVersions  Set[string]
	ref                PackageReference
	project            *ParsedProject
	info               *PackageInfo
	source             string
	err                error
	loading            bool
	latestCompatible   *PackageVersion // the update-target policy's choice (PackageAssessment.Candidate), even if not newer
	latestStable       *PackageVersion // the stable policy's choice (the a key): newest listed stable ignoring frameworks, or prerelease for a prerelease request
	updateTarget       *PackageVersion // latestCompatible when it is an update; nil when up to date
	updateRequirements []updateDependencyRequirement
	diverged           bool
	oldest             SemVer
	vulnerable         bool                  // an installed version has ≥1 known vulnerability
	deprecated         bool                  // an installed version is deprecated in the registry
	deprecation        *InstalledDeprecation // the first deprecated installed version
	unusable           []unusableInstall     // installed versions a referencing project's frameworks cannot use
}

// unusableInstall is an installed version that the feed lists without
// support for a target framework of the project that references it.
type unusableInstall struct {
	version string
	project string // file name
}

// effectiveVersion returns the version used for status comparisons.
// When diverged (All Projects view), use the oldest version so the icon
// reflects the least-up-to-date project.
func (r packageRow) effectiveVersion() SemVer {
	if r.diverged {
		return r.oldest
	}
	return r.ref.Version
}

// skippedStable returns the stable policy's choice when the update target
// passes it over and no project already uses it: a newer version that some
// target framework cannot use. Both policies share the prerelease rule, so
// framework compatibility is the only reason for the two to differ this way.
// It is nil under the stable policy.
func (r packageRow) skippedStable() *PackageVersion {
	if r.latestStable == nil {
		return nil
	}
	if r.latestCompatible != nil && !r.latestStable.SemVer.IsNewerThan(r.latestCompatible.SemVer) {
		return nil
	}
	if installed := r.effectiveVersion(); installed.IsValid() && !r.latestStable.SemVer.IsNewerThan(installed) {
		return nil
	}
	return r.latestStable
}

func (r packageRow) statusIcon() string {
	if r.loading {
		return "."
	}
	if r.vulnerable {
		return "▲"
	}
	if r.err != nil {
		return "✗"
	}
	if r.updateTarget != nil {
		if r.latestStable != nil && r.latestStable.SemVer.IsNewerThan(r.updateTarget.SemVer) {
			return "⇑"
		}
		return "↑"
	}
	if r.deprecated {
		return "~"
	}
	if len(r.unusable) > 0 {
		return "!"
	}
	return "✓"
}

func (r packageRow) statusStyle() style.Style {
	if r.loading {
		return styleAccent
	}
	if r.vulnerable {
		return styleRed
	}
	if r.err != nil {
		return styleRed
	}
	if r.updateTarget != nil {
		if r.latestStable != nil && r.latestStable.SemVer.IsNewerThan(r.updateTarget.SemVer) {
			return stylePurple
		}
		return styleYellow
	}
	if r.deprecated {
		return styleYellow
	}
	if len(r.unusable) > 0 {
		return styleYellow
	}
	return styleGreen
}

// --- Component state types ---

type versionPicker struct {
	sectionBase   // baseWidth=50, minWidth=40, maxMargin=4
	pkgName       string
	versions      []PackageVersion
	cursor        int
	targets       Set[TargetFramework]
	addMode       bool
	targetProject *ParsedProject
}

func (vp *versionPicker) selectedVersion() *PackageVersion {
	if vp.cursor < len(vp.versions) {
		return &vp.versions[vp.cursor]
	}
	return nil
}

type packageSearch struct {
	sectionBase     // baseWidth=90, minWidth=56, maxMargin=4
	input           input.Model
	debounceID      int
	lastQuery       string
	results         []SearchResult
	cursor          int
	loading         bool
	err             error
	fetchingVersion bool
	fetchedInfo     *PackageInfo
	fetchedSource   string
	cancel          context.CancelFunc
}

type confirmRemove struct {
	sectionBase // baseWidth=48, minWidth=36, maxMargin=4
	pkgName     string
}

type confirmUpdate struct {
	sectionBase // baseWidth=52, minWidth=40, maxMargin=4
	pkgName     string
	newVersion  string
	project     *ParsedProject
	selection   *reloadSelection
}

type locationPicker struct {
	sectionBase   // baseWidth=80, minWidth=60, maxMargin=4
	pkgName       string
	version       string
	targets       []AddTarget
	cursor        int
	targetProject *ParsedProject
}

type projectPickItem struct {
	project        *ParsedProject
	selected       bool
	installed      bool   // already has this exact version
	currentVersion string // non-empty if package exists at a different version
	downgrade      bool   // true when currentVersion is newer than the target version
	incompatible   bool   // true when the package version doesn't support the project's TFMs
}

type projectPicker struct {
	sectionBase
	pkgName string
	version string
	items   []projectPickItem
	cursor  int
}
