package main

import (
	"fmt"
	"strings"

	bubbles_textinpute "charm.land/bubbles/v2/textinput"
	bubbles_viewport "charm.land/bubbles/v2/viewport"
	lipgloss "charm.land/lipgloss/v2"
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
	sortByCurrent                          // published date of installed version (newest first)
	sortByAvailable                        // published date of best available upgrade (newest first)
	sortBySource                           // source then name
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
	name   string
	result nugetResult
}

type writeResultMsg struct {
	err     error
	written int // number of files written (0 = unknown / not an applyVersion call)
	skipped int // number of locked refs skipped during scope=all update
}

type restoreResultMsg struct {
	err error
}

type resizeDebounceMsg struct {
	id int
}

type searchDebounceMsg struct {
	id    int
	query string
}

type searchResultsMsg struct {
	results []SearchResult
	query   string
	err     error
}

type packageFetchedMsg struct {
	info   *PackageInfo
	source string
	err    error
}

type logLineMsg struct {
	line string
}

type depTreeReadyMsg struct {
	content string
	err     error
}

type releaseListReadyMsg struct {
	releases []GitHubRelease
	err      error
	owner    string // set when repo was discovered late (e.g. from nuspec)
	repo     string
}

type releaseNotesReadyMsg struct {
	body    string
	htmlURL string
	err     error
}

// --- Panel state types ---

type projectPanel struct {
	sectionBase // baseWidth=30, minWidth=10
	cursor      int
	scroll      int // list scroll offset
	items       []projectItem
}

type packagePanel struct {
	cursor   int
	scroll   int
	rows     []packageRow
	sortMode packageSortMode
	sortDir  bool
}

type detailPanel struct {
	sectionBase // baseWidth=50, minWidth=10
	vp          bubbles_viewport.Model
}

type logPanel struct {
	vp bubbles_viewport.Model
}

// --- Overlay state types ---

type depTreeOverlay struct {
	sectionBase // basePct=80, minWidth=40, maxMargin=4
	loading     bool // true while dotnet list is running (T key)
	content     string
	err         error
	vp          bubbles_viewport.Model
	title       string
}

type releaseNotesOverlay struct {
	sectionBase // basePct=85, minWidth=60, maxMargin=4
	loading     bool
	focusRight  bool             // false = release list, true = notes viewport
	releases    []GitHubRelease  // list of release tags
	cursor      int              // selected release index
	notes       string           // rendered release notes body
	notesURL    string           // link to the release on GitHub
	err         error
	vp          bubbles_viewport.Model
	title       string
	owner       string // GitHub owner
	repo        string // GitHub repo name
	// fallback: nuspec release notes (non-git or GitHub fetch failed)
	nuspecNotes string
}

type sourcesOverlay struct {
	sectionBase // baseWidth=90, minWidth=40, maxMargin=4
}

type helpOverlay struct {
	sectionBase // basePct=60, minWidth=56, maxMargin=4
	vp          bubbles_viewport.Model
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
		return strings.Join(fws, ", ")
	}
	return fmt.Sprintf("%d packages", p.project.Packages.Len())
}

type packageRow struct {
	ref              PackageReference
	project          *ParsedProject
	info             *PackageInfo
	source           string
	err              error
	latestCompatible *PackageVersion
	latestStable     *PackageVersion
	diverged         bool
	oldest           SemVer
	vulnerable       bool // installed version has ≥1 known vulnerability
	deprecated       bool // package is deprecated in the registry
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

func (r packageRow) statusIcon() string {
	if r.vulnerable {
		return "▲"
	}
	if r.err != nil {
		return "✗"
	}
	ver := r.effectiveVersion()
	check := r.latestCompatible
	if check == nil {
		check = r.latestStable
	}
	if check != nil && check.SemVer.IsNewerThan(ver) {
		if r.latestStable != nil && r.latestCompatible != nil &&
			r.latestStable.SemVer.IsNewerThan(r.latestCompatible.SemVer) {
			return "⬆"
		}
		return "↑"
	}
	if r.deprecated {
		return "~"
	}
	return "✓"
}

func (r packageRow) statusStyle() lipgloss.Style {
	if r.vulnerable {
		return styleRed
	}
	if r.err != nil {
		return styleRed
	}
	ver := r.effectiveVersion()
	check := r.latestCompatible
	if check == nil {
		check = r.latestStable
	}
	if check != nil && check.SemVer.IsNewerThan(ver) {
		if r.latestStable != nil && r.latestCompatible != nil &&
			r.latestStable.SemVer.IsNewerThan(r.latestCompatible.SemVer) {
			return stylePurple
		}
		return styleYellow
	}
	if r.deprecated {
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
	input           bubbles_textinpute.Model
	debounceID      int
	lastQuery       string
	results         []SearchResult
	cursor          int
	loading         bool
	err             error
	fetchingVersion bool
	fetchedInfo     *PackageInfo
	fetchedSource   string
}

type confirmRemove struct {
	sectionBase // baseWidth=48, minWidth=36, maxMargin=4
	pkgName string
}

type confirmUpdate struct {
	sectionBase // baseWidth=52, minWidth=40, maxMargin=4
	pkgName    string
	newVersion string
	project    *ParsedProject
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
	project   *ParsedProject
	selected  bool
	installed bool // already has this package
}

type projectPicker struct {
	sectionBase
	pkgName string
	version     string
	items       []projectPickItem
	cursor      int
}
