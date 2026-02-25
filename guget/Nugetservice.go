package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

type serviceIndex struct {
	Resources []struct {
		ID   string `json:"@id"`
		Type string `json:"@type"`
	} `json:"resources"`
}

type searchResponse struct {
	TotalHits IntOrString    `json:"totalHits"`
	Data      []SearchResult `json:"data"`
}

// IntOrString handles feeds (e.g. some Azure DevOps versions) that return
// totalHits as a JSON string ("42") instead of a number (42).
type IntOrString int

func (n *IntOrString) UnmarshalJSON(b []byte) error {
	// Try number first
	var i int
	if err := json.Unmarshal(b, &i); err == nil {
		*n = IntOrString(i)
		return nil
	}
	// Fall back to quoted string
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	parsed, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("IntOrString: cannot parse %q as int", s)
	}
	*n = IntOrString(parsed)
	return nil
}

// SearchResult is what comes back from the NuGet search endpoint.
type SearchResult struct {
	ID             string          `json:"id"`
	Version        string          `json:"version"` // latest stable
	Description    string          `json:"description"`
	Authors        StringOrArray   `json:"authors"`
	Tags           StringOrArray   `json:"tags"`
	TotalDownloads int             `json:"totalDownloads"`
	Verified       bool            `json:"verified"`
	Versions       []searchVersion `json:"versions"`
	Source         string          `json:"-"` // set after search, not from JSON
}

type searchVersion struct {
	Version   string `json:"version"`
	Downloads int    `json:"downloads"`
}

// PackageVersion is an enriched version with semver + framework info.
type PackageVersion struct {
	SemVer           SemVer
	Downloads        int
	Frameworks       []TargetFramework      // target frameworks this version supports
	Vulnerabilities  []PackageVulnerability // CVE advisories for this specific version
	DependencyGroups []dependencyGroup      // declared dependencies, for dep tree overlay
}

// PackageInfo is the full picture of a package.
type PackageInfo struct {
	ID                 string
	LatestVersion      string
	Description        string
	Authors            Set[string]
	Tags               Set[string]
	ProjectURL         string // from catalog entry (e.g. GitHub repo)
	TotalDownloads     int
	Verified           bool
	Versions           []PackageVersion // sorted newest → oldest
	Deprecated         bool
	DeprecationMessage string
	AlternatePackageID string
	NugetOrgURL        string // set when package exists on nuget.org (even if found via another source)
}

// registrationIndex is returned by the RegistrationsBaseUrl endpoint.
type registrationIndex struct {
	Items []registrationPage `json:"items"`
}

type registrationPage struct {
	ID    string                    `json:"@id"`
	Items []registrationLeafWrapper `json:"items"` // nil if not inlined, must fetch page URL
	Lower string                    `json:"lower"`
	Upper string                    `json:"upper"`
}

type registrationLeafWrapper struct {
	CatalogEntry registrationLeaf `json:"catalogEntry"`
}

type registrationLeaf struct {
	ID               string                 `json:"id"`
	Version          string                 `json:"version"`
	Description      string                 `json:"description"`
	Authors          StringOrArray          `json:"authors"`
	Tags             StringOrArray          `json:"tags"`
	ProjectURL       string                 `json:"projectUrl"`
	Repository       *repositoryMeta        `json:"repository"`
	Listed           *bool                  `json:"listed"`
	DependencyGroups []dependencyGroup      `json:"dependencyGroups"`
	Vulnerabilities  []PackageVulnerability `json:"vulnerabilities"`
	Deprecation      *deprecationRaw        `json:"deprecation"`
}

type repositoryMeta struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type dependencyGroup struct {
	TargetFramework string              `json:"targetFramework"` // e.g. ".NETStandard2.0", "net6.0"
	Dependencies    []packageDependency `json:"dependencies"`
}

type packageDependency struct {
	ID    string `json:"id"`
	Range string `json:"range"`
}

// PackageVulnerability holds CVE advisory info for a specific package version.
type PackageVulnerability struct {
	AdvisoryURL string      `json:"advisoryUrl"`
	Severity    IntOrString `json:"severity"` // 0=low 1=moderate 2=high 3=critical
}

// SeverityLabel returns a human-readable severity string.
func (v PackageVulnerability) SeverityLabel() string {
	switch int(v.Severity) {
	case 3:
		return "critical"
	case 2:
		return "high"
	case 1:
		return "moderate"
	default:
		return "low"
	}
}

type deprecationRaw struct {
	Message          string   `json:"message"`
	Reasons          []string `json:"reasons"`
	AlternatePackage struct {
		ID string `json:"id"`
	} `json:"alternatePackage"`
}

// authTransport injects Basic Auth and retries on 401 via credential providers.
type authTransport struct {
	base       http.RoundTripper
	sourceURL  string
	sourceName string
	mu         sync.Mutex
	username   string
	password   string
	provOnce   sync.Once // ensures the credential provider is invoked at most once
}

func newAuthTransport(source NugetSource) *authTransport {
	return &authTransport{
		base:       http.DefaultTransport,
		sourceURL:  source.URL,
		sourceName: source.Name,
		username:   source.Username,
		password:   source.Password,
	}
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	user, pass := t.username, t.password
	t.mu.Unlock()

	// Clone so we never mutate the caller's request.
	req = req.Clone(req.Context())
	if user != "" || pass != "" {
		logTrace("[%s] sending Basic Auth (username=%q, password=%d chars)", t.sourceName, user, len(pass))
		req.SetBasicAuth(user, pass)
	} else {
		logTrace("[%s] no credentials available, sending unauthenticated request", t.sourceName)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}

	// 401 — ask a credential provider (once per transport lifetime).
	logTrace("[%s] got 401, invoking credential provider", t.sourceName)
	resp.Body.Close()

	var providerCred *sourceCredential
	t.provOnce.Do(func() {
		cred, provErr := fetchFromCredentialProvider(t.sourceURL, t.sourceName)
		if provErr != nil {
			logDebug("[%s] credential provider: %v", t.sourceName, provErr)
			return
		}
		t.mu.Lock()
		t.username = cred.Username
		t.password = cred.Password
		t.mu.Unlock()
		providerCred = cred
	})

	if providerCred == nil {
		// Provider not available or already tried and failed — surface the 401.
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Status:     "401 Unauthorized",
			Body:       http.NoBody,
			Header:     make(http.Header),
		}, nil
	}

	// Retry with the provider-supplied credentials.
	req2, err2 := http.NewRequestWithContext(req.Context(), req.Method, req.URL.String(), nil)
	if err2 != nil {
		return nil, err2
	}
	for k, v := range req.Header {
		req2.Header[k] = v
	}
	req2.SetBasicAuth(providerCred.Username, providerCred.Password)
	return t.base.RoundTrip(req2)
}

// NugetService talks to a single NuGet v3 feed.
type NugetService struct {
	sourceURL      string
	sourceName     string
	client         *http.Client
	searchBase     string // resolved from service index
	regBase        string // RegistrationsBaseUrl
	detailTemplate string // PackageDetailsUriTemplate (e.g. "https://…/packages/{id}/{version}")
}

func (s *NugetService) SourceName() string { return s.sourceName }

// PackageURL returns a browsable web URL for the given package, or "" if unknown.
// projectURL is the package's ProjectURL metadata (may be empty).
func (s *NugetService) PackageURL(id, version, projectURL string) string {
	if s.detailTemplate != "" {
		u := strings.NewReplacer("{id}", id, "{version}", version).Replace(s.detailTemplate)
		// Strip query params like ?_src=template
		if i := strings.Index(u, "?"); i >= 0 {
			u = u[:i]
		}
		return u
	}
	return inferPackageURL(s.sourceURL, id, version, projectURL)
}

// inferPackageURL constructs a browsable package URL for known hosting services
// based on the source's API URL pattern.
func inferPackageURL(sourceURL, id, version, projectURL string) string {
	lower := strings.ToLower(sourceURL)

	// Azure DevOps Artifacts:
	// https://pkgs.dev.azure.com/{org}[/{project}]/_packaging/{feed}/nuget/v3/index.json
	// → https://dev.azure.com/{org}[/{project}]/_artifacts/feed/{feed}/NuGet/{id}/overview/{version}
	if strings.Contains(lower, "pkgs.dev.azure.com") {
		if idx := strings.Index(lower, "/_packaging/"); idx >= 0 {
			prefix := sourceURL[:idx] // e.g. "https://pkgs.dev.azure.com/org/project"
			rest := sourceURL[idx+len("/_packaging/"):]
			feed := rest
			if sl := strings.Index(feed, "/"); sl >= 0 {
				feed = feed[:sl]
			}
			prefix = strings.Replace(prefix, "pkgs.dev.azure.com", "dev.azure.com", 1)
			return prefix + "/_artifacts/feed/" + feed + "/NuGet/" + id + "/overview/" + version
		}
	}

	// MyGet:
	// https://www.myget.org/F/{feed}/api/v3/index.json
	// → https://www.myget.org/feed/{feed}/package/nuget/{id}/{version}
	if strings.Contains(lower, "myget.org/f/") {
		if idx := strings.Index(lower, "/f/"); idx >= 0 {
			base := sourceURL[:idx] // e.g. "https://www.myget.org"
			rest := sourceURL[idx+len("/F/"):]
			feed := rest
			if sl := strings.Index(feed, "/"); sl >= 0 {
				feed = feed[:sl]
			}
			return base + "/feed/" + feed + "/package/nuget/" + id + "/" + version
		}
	}

	// GitHub Packages:
	// https://nuget.pkg.github.com/{owner}/index.json
	// → https://github.com/{owner}/{repo}/pkgs/nuget/{package}
	if strings.Contains(lower, "nuget.pkg.github.com") {
		owner := extractGitHubOwner(sourceURL)
		if owner == "" {
			return ""
		}
		// Try to derive {owner}/{repo} from ProjectURL for a direct package link.
		if projectURL != "" {
			projLower := strings.ToLower(projectURL)
			if strings.Contains(projLower, "github.com/") {
				idx := strings.Index(projLower, "github.com/")
				ownerRepo := projectURL[idx+len("github.com/"):]
				ownerRepo = strings.TrimRight(ownerRepo, "/")
				parts := strings.SplitN(ownerRepo, "/", 3)
				if len(parts) >= 2 {
					return "https://github.com/" + parts[0] + "/" + parts[1] + "/pkgs/nuget/" + id
				}
			}
		}
		// Fallback: link to the owner's packages filtered by this package name.
		return "https://github.com/" + owner + "?tab=packages&q=" + id + "&type=nuget"
	}

	return ""
}

// extractGitHubOwner returns the owner from a GitHub Packages NuGet source URL,
// e.g. "https://nuget.pkg.github.com/Nulifyer/index.json" → "Nulifyer".
func extractGitHubOwner(sourceURL string) string {
	lower := strings.ToLower(sourceURL)
	idx := strings.Index(lower, "nuget.pkg.github.com")
	if idx < 0 {
		return ""
	}
	after := sourceURL[idx+len("nuget.pkg.github.com"):]
	after = strings.TrimLeft(after, "/")
	if sl := strings.Index(after, "/"); sl > 0 {
		return after[:sl]
	}
	return after
}

type ghPackageResponse struct {
	Repository struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
}

// fetchGitHubPackage calls the GitHub API to get package metadata including
// the linked repository. Returns nil on any error (best-effort).
func (s *NugetService) fetchGitHubPackage(owner, packageName string) *ghPackageResponse {
	// Extract the PAT from the auth transport for Bearer auth to the GitHub REST API.
	at, _ := s.client.Transport.(*authTransport)
	if at == nil {
		return nil
	}
	at.mu.Lock()
	token := at.password
	at.mu.Unlock()
	if token == "" {
		return nil
	}

	// Try user endpoint first, then org endpoint.
	for _, tmpl := range []string{
		"https://api.github.com/users/%s/packages/nuget/%s",
		"https://api.github.com/orgs/%s/packages/nuget/%s",
	} {
		apiURL := fmt.Sprintf(tmpl, owner, packageName)
		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil || resp.StatusCode != http.StatusOK {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
		var ghResp ghPackageResponse
		decErr := json.NewDecoder(resp.Body).Decode(&ghResp)
		resp.Body.Close()
		if decErr == nil && ghResp.Repository.HTMLURL != "" {
			return &ghResp
		}
	}
	return nil
}

// projectOrRepoURL returns projectUrl if set, otherwise falls back to the
// repository URL from the catalog entry (common on GitHub Packages).
func projectOrRepoURL(leaf *registrationLeaf) string {
	if leaf.ProjectURL != "" {
		return leaf.ProjectURL
	}
	if leaf.Repository != nil && leaf.Repository.URL != "" {
		return leaf.Repository.URL
	}
	return ""
}

// NewNugetService creates and initialises a service for the given NugetSource.
func NewNugetService(source NugetSource) (*NugetService, error) {
	svc := &NugetService{
		sourceURL:  source.URL,
		sourceName: source.Name,
		client:     &http.Client{Transport: newAuthTransport(source)},
	}
	if err := svc.resolveEndpoints(); err != nil {
		return nil, err
	}
	return svc, nil
}

func (s *NugetService) resolveEndpoints() error {
	var idx serviceIndex
	if err := s.getJSON(s.sourceURL, &idx); err != nil {
		return fmt.Errorf("fetching service index: %w", err)
	}
	var searchVer, regVer SemVer
	for _, r := range idx.Resources {
		logTrace("[%s] service index resource: type=%q id=%q", s.sourceName, r.Type, r.ID)
		switch {
		case strings.HasPrefix(r.Type, "SearchQueryService"):
			if v := resourceTypeVersion(r.Type); s.searchBase == "" || v.IsNewerThan(searchVer) {
				s.searchBase = r.ID
				searchVer = v
			}
		case strings.HasPrefix(r.Type, "RegistrationsBaseUrl"):
			if v := resourceTypeVersion(r.Type); s.regBase == "" || v.IsNewerThan(regVer) {
				s.regBase = r.ID
				regVer = v
			}
		case strings.HasPrefix(r.Type, "PackageDetailsUriTemplate"):
			s.detailTemplate = r.ID
		}
	}
	if s.searchBase == "" {
		// Not fatal — exact lookups use the registration index directly.
		// Interactive search will be unavailable for this source.
		logWarn("[%s] SearchQueryService not found in service index — search unavailable", s.sourceName)
	}
	if s.regBase == "" {
		return fmt.Errorf("RegistrationsBaseUrl not found in service index")
	}
	// Ensure trailing slash so callers can simply append path segments.
	if !strings.HasSuffix(s.regBase, "/") {
		s.regBase += "/"
	}
	logDebug("[%s] endpoints resolved: search=%s reg=%s", s.sourceName, s.searchBase, s.regBase)
	return nil
}

// Search returns up to take results matching the given query string.
func (s *NugetService) Search(query string, take int) ([]SearchResult, error) {
	logDebug("[%s] search query=%q take=%d", s.sourceName, query, take)
	params := url.Values{}
	params.Set("q", query)
	params.Set("take", strconv.Itoa(take))
	params.Set("prerelease", "false")
	var resp searchResponse
	if err := s.getJSON(s.searchBase+"?"+params.Encode(), &resp); err != nil {
		return nil, err
	}
	logDebug("[%s] search returned %d results", s.sourceName, len(resp.Data))
	return resp.Data, nil
}

// SearchExact looks up a package by its exact ID using the registration index
// directly. This avoids the search API entirely, which is more reliable across
// feed types (e.g. Azure DevOps returns HTTP 500 from its search endpoint for
// packages not in the feed, whereas the registration endpoint returns 404).
func (s *NugetService) SearchExact(packageID string) (*PackageInfo, error) {
	logDebug("[%s] looking up %q via registration index", s.sourceName, packageID)
	regURL := fmt.Sprintf("%s%s/index.json", s.regBase, strings.ToLower(packageID))

	var regIdx registrationIndex
	if err := s.getJSON(regURL, &regIdx); err != nil {
		var he *httpStatusError
		if errors.As(err, &he) && he.Code == http.StatusNotFound {
			logDebug("[%s] %q not found (404)", s.sourceName, packageID)
			return nil, fmt.Errorf("package %q not found", packageID)
		}
		return nil, err
	}

	var versions []PackageVersion
	var latestLeaf *registrationLeaf       // newest version overall (for fallback metadata)
	var latestStableLeaf *registrationLeaf // newest stable version (preferred for metadata)

	for _, page := range regIdx.Items {
		items := page.Items
		if len(items) == 0 {
			// Page not inlined — fetch it separately.
			var fullPage registrationPage
			if err := s.getJSON(page.ID, &fullPage); err != nil {
				return nil, fmt.Errorf("fetching page %s: %w", page.ID, err)
			}
			items = fullPage.Items
		}

		for i := range items {
			ce := &items[i].CatalogEntry
			// Skip explicitly unlisted packages.
			if ce.Listed != nil && !*ce.Listed {
				continue
			}
			sv := ParseSemVer(ce.Version)
			if latestLeaf == nil || sv.IsNewerThan(ParseSemVer(latestLeaf.Version)) {
				latestLeaf = ce
			}
			if !sv.IsPreRelease() {
				if latestStableLeaf == nil || sv.IsNewerThan(ParseSemVer(latestStableLeaf.Version)) {
					latestStableLeaf = ce
				}
			}
			seen := NewSet[string]()
			var frameworks []TargetFramework
			for _, dg := range ce.DependencyGroups {
				raw := normFramework(dg.TargetFramework)
				if raw != "" && !seen.Contains(raw) {
					seen.Add(raw)
					frameworks = append(frameworks, ParseTargetFramework(raw))
				}
			}
			versions = append(versions, PackageVersion{
				SemVer:           sv,
				Frameworks:       frameworks,
				Vulnerabilities:  ce.Vulnerabilities,
				DependencyGroups: ce.DependencyGroups,
			})
		}
	}

	if len(versions) == 0 || latestLeaf == nil {
		logDebug("[%s] %q has no listed versions", s.sourceName, packageID)
		return nil, fmt.Errorf("package %q not found", packageID)
	}

	sortVersionsDesc(versions)

	// Prefer stable-version metadata; fall back to the overall latest.
	meta := latestStableLeaf
	if meta == nil {
		meta = latestLeaf
	}

	authors := NewSet[string]()
	for _, a := range meta.Authors {
		authors.Add(a)
	}
	tags := NewSet[string]()
	for _, t := range meta.Tags {
		tags.Add(t)
	}

	logDebug("[%s] found %q: %d versions, latest stable=%s", s.sourceName, packageID, len(versions), meta.Version)

	pkg := &PackageInfo{
		ID:            meta.ID,
		LatestVersion: meta.Version,
		Description:   meta.Description,
		Authors:       authors,
		Tags:          tags,
		ProjectURL:    projectOrRepoURL(meta),
		Versions:      versions,
	}
	// For GitHub Packages, call the GitHub API to resolve the source repo.
	if pkg.ProjectURL == "" {
		if owner := extractGitHubOwner(s.sourceURL); owner != "" {
			if ghPkg := s.fetchGitHubPackage(owner, packageID); ghPkg != nil {
				if ghPkg.Repository.HTMLURL != "" {
					pkg.ProjectURL = ghPkg.Repository.HTMLURL
				} else {
					pkg.ProjectURL = "https://github.com/" + owner
				}
			}
		}
	}
	if meta.Deprecation != nil {
		pkg.Deprecated = true
		pkg.DeprecationMessage = meta.Deprecation.Message
		pkg.AlternatePackageID = meta.Deprecation.AlternatePackage.ID
	}

	// Augment with download counts from the search endpoint (best-effort;
	// the registration API does not include download statistics).
	if sr := s.fetchSearchResult(packageID); sr != nil {
		pkg.TotalDownloads = sr.TotalDownloads
		pkg.Verified = sr.Verified
		dlMap := make(map[string]int, len(sr.Versions))
		for _, v := range sr.Versions {
			dlMap[v.Version] = v.Downloads
		}
		for i := range pkg.Versions {
			if d, ok := dlMap[pkg.Versions[i].SemVer.Raw]; ok {
				pkg.Versions[i].Downloads = d
			}
		}
	}

	return pkg, nil
}

// fetchSearchResult queries the search endpoint for an exact package ID match
// and returns the first result whose ID matches (case-insensitive). Returns nil
// if the search endpoint is unavailable or the package is not found.
func (s *NugetService) fetchSearchResult(packageID string) *SearchResult {
	if s.searchBase == "" {
		return nil
	}
	params := url.Values{}
	params.Set("q", packageID)
	params.Set("take", "1")
	params.Set("prerelease", "true")
	var resp searchResponse
	if err := s.getJSON(s.searchBase+"?"+params.Encode(), &resp); err != nil {
		logDebug("[%s] download fetch failed for %q: %v", s.sourceName, packageID, err)
		return nil
	}
	for i := range resp.Data {
		if strings.EqualFold(resp.Data[i].ID, packageID) {
			return &resp.Data[i]
		}
	}
	return nil
}

// LatestStable returns the newest non-pre-release version.
func (p *PackageInfo) LatestStable() *PackageVersion {
	for i := range p.Versions {
		if !p.Versions[i].SemVer.IsPreRelease() {
			return &p.Versions[i]
		}
	}
	return nil
}

// LatestStableForFramework returns the newest stable version whose declared
// target frameworks are compatible with all of the project's targets.
// Returns nil if no compatible stable version exists (callers fall back to
// LatestStable themselves for display purposes).
func (p *PackageInfo) LatestStableForFramework(targets Set[TargetFramework]) *PackageVersion {
	for i := range p.Versions {
		v := &p.Versions[i]
		if v.SemVer.IsPreRelease() {
			continue
		}

		// No frameworks declared means the package supports everything
		if len(v.Frameworks) == 0 {
			return v
		}

		// Check if this version is compatible with all project frameworks
		allCompatible := true
		for target := range targets {
			compatibleWithProj := false
			for _, versionFw := range v.Frameworks {
				if target.IsCompatibleWith(versionFw) {
					compatibleWithProj = true
					break
				}
			}
			if !compatibleWithProj {
				allCompatible = false
				break
			}
		}
		if allCompatible {
			return v
		}
	}
	return nil
}

// VersionsSince returns all versions newer than the given semver string.
func (p *PackageInfo) VersionsSince(since string) []PackageVersion {
	floor := ParseSemVer(since)
	var result []PackageVersion
	for _, v := range p.Versions {
		if v.SemVer.IsNewerThan(floor) {
			result = append(result, v)
		}
	}
	return result
}

type StringOrArray []string

func (s *StringOrArray) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = []string{str}
		return nil
	}
	var arr []string
	if err := json.Unmarshal(b, &arr); err != nil {
		return err
	}
	*s = arr
	return nil
}

// httpStatusError is returned by getJSON for non-200 responses so callers can
// inspect the status code and decide whether to treat it as a hard failure.
type httpStatusError struct {
	Code int
	URL  string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP %d for %s", e.Code, e.URL)
}

func (s *NugetService) getJSON(u string, dst any) error {
	resp, err := s.client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &httpStatusError{Code: resp.StatusCode, URL: u}
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// normFramework normalises a raw targetFramework string from the NuGet
// registration API into the short form expected by ParseTargetFramework
// (e.g. ".NETFramework4.6.2" → "net462", ".NETStandard2.0" → "netstandard2.0").
// An empty string returns "any", which ParseTargetFramework maps to FamilyUnknown
// with Raw=="any" — IsCompatibleWith treats that as compatible with everything.
func normFramework(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "any"
	}
	low := strings.ToLower(strings.ReplaceAll(raw, " ", ""))

	// Handle explicit .NET prefixes from the NuGet API
	switch {
	case strings.HasPrefix(low, ".netstandard"):
		return strings.TrimPrefix(low, ".")
	case strings.HasPrefix(low, ".netframework"):
		// .NETFramework4.6.2 → net462
		ver := strings.TrimPrefix(low, ".netframework")
		ver = strings.ReplaceAll(ver, ".", "")
		return "net" + ver
	case strings.HasPrefix(low, ".netcoreapp"):
		return strings.TrimPrefix(low, ".")
	case strings.HasPrefix(low, ".net"):
		return strings.TrimPrefix(low, ".")
	}
	return low
}

// resourceTypeVersion parses the version suffix from a NuGet service index resource type,
// e.g. "SearchQueryService/3.0.0-beta" → SemVer{3,0,0,"beta"}.
// Unversioned types (e.g. "SearchQueryService") return a zero SemVer.
func resourceTypeVersion(resourceType string) SemVer {
	if idx := strings.IndexByte(resourceType, '/'); idx >= 0 {
		return ParseSemVer(resourceType[idx+1:])
	}
	return SemVer{}
}

func sortVersionsDesc(vs []PackageVersion) {
	for i := 1; i < len(vs); i++ {
		for j := i; j > 0 && vs[j].SemVer.IsNewerThan(vs[j-1].SemVer); j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}
