package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"logger"
)

// ─────────────────────────────────────────────
// NuGet V3 API types
// ─────────────────────────────────────────────

type serviceIndex struct {
	Resources []struct {
		ID   string `json:"@id"`
		Type string `json:"@type"`
	} `json:"resources"`
}

type searchResponse struct {
	TotalHits int            `json:"totalHits"`
	Data      []SearchResult `json:"data"`
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
}

type searchVersion struct {
	Version   string `json:"version"`
	Downloads int    `json:"downloads"`
}

// PackageVersion is an enriched version with semver + framework info.
type PackageVersion struct {
	SemVer     SemVer
	Downloads  int
	Frameworks []TargetFramework // target frameworks this version supports
}

// PackageInfo is the full picture of a package.
type PackageInfo struct {
	ID             string
	LatestVersion  string
	Description    string
	Authors        Set[string]
	Tags           Set[string]
	TotalDownloads int
	Verified       bool
	Versions       []PackageVersion // sorted newest → oldest
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
	ID               string            `json:"id"`
	Version          string            `json:"version"`
	DependencyGroups []dependencyGroup `json:"dependencyGroups"`
}

type dependencyGroup struct {
	TargetFramework string `json:"targetFramework"` // e.g. ".NETStandard2.0", "net6.0"
}

// ─────────────────────────────────────────────
// NugetService
// ─────────────────────────────────────────────

// authTransport injects Basic Auth into every outgoing request and retries on
// HTTP 401 by invoking NuGet credential providers (e.g. Azure Artifacts).
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
		logger.Trace("[%s] sending Basic Auth (username=%q, password=%d chars)", t.sourceName, user, len(pass))
		req.SetBasicAuth(user, pass)
	} else {
		logger.Trace("[%s] no credentials available, sending unauthenticated request", t.sourceName)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}

	// 401 — ask a credential provider (once per transport lifetime).
	logger.Trace("[%s] got 401, invoking credential provider", t.sourceName)
	resp.Body.Close()

	var providerCred *sourceCredential
	t.provOnce.Do(func() {
		cred, provErr := fetchFromCredentialProvider(t.sourceURL, t.sourceName)
		if provErr != nil {
			logger.Debug("[%s] credential provider: %v", t.sourceName, provErr)
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
	sourceURL  string
	sourceName string
	client     *http.Client
	searchBase string // resolved from service index
	regBase    string // RegistrationsBaseUrl
}

func (s *NugetService) SourceName() string { return s.sourceName }

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
	for _, r := range idx.Resources {
		switch r.Type {
		case "SearchQueryService":
			if s.searchBase == "" {
				s.searchBase = r.ID
			}
		case "RegistrationsBaseUrl/3.6.0": // semver2 + gzip, most capable
			s.regBase = r.ID
		case "RegistrationsBaseUrl/3.4.0": // gzip, no semver2
			if s.regBase == "" {
				s.regBase = r.ID
			}
		case "RegistrationsBaseUrl": // plain fallback
			if s.regBase == "" {
				s.regBase = r.ID
			}
		}
	}
	if s.searchBase == "" {
		return fmt.Errorf("SearchQueryService not found in service index")
	}
	if s.regBase == "" {
		return fmt.Errorf("RegistrationsBaseUrl not found in service index")
	}
	logger.Debug("[%s] endpoints resolved: search=%s", s.sourceName, s.searchBase)
	return nil
}

// Search returns up to take results matching the given query string.
func (s *NugetService) Search(query string, take int) ([]SearchResult, error) {
	logger.Debug("[%s] search query=%q take=%d", s.sourceName, query, take)
	params := url.Values{}
	params.Set("q", query)
	params.Set("take", strconv.Itoa(take))
	params.Set("prerelease", "false")
	var resp searchResponse
	if err := s.getJSON(s.searchBase+"?"+params.Encode(), &resp); err != nil {
		return nil, err
	}
	logger.Debug("[%s] search returned %d results", s.sourceName, len(resp.Data))
	return resp.Data, nil
}

// SearchExact finds a package by its exact ID (case-insensitive).
func (s *NugetService) SearchExact(packageID string) (*PackageInfo, error) {
	logger.Debug("[%s] searching for %q", s.sourceName, packageID)
	params := url.Values{}
	params.Set("q", packageID)
	params.Set("take", "10") // small window; exact-ID match is identified by strings.EqualFold below
	params.Set("prerelease", "false")

	var resp searchResponse
	if err := s.getJSON(s.searchBase+"?"+params.Encode(), &resp); err != nil {
		return nil, err
	}

	for _, r := range resp.Data {
		if strings.EqualFold(r.ID, packageID) {
			logger.Debug("[%s] found %q (latest=%s)", s.sourceName, packageID, r.Version)
			return s.enrichResult(r)
		}
	}
	logger.Debug("[%s] %q not found", s.sourceName, packageID)
	return nil, fmt.Errorf("package %q not found", packageID)
}

// enrichResult fetches registration data to build full PackageInfo.
func (s *NugetService) enrichResult(r SearchResult) (*PackageInfo, error) {
	regURL := fmt.Sprintf("%s%s/index.json", s.regBase, strings.ToLower(r.ID))

	var regIdx registrationIndex
	if err := s.getJSON(regURL, &regIdx); err != nil {
		return nil, fmt.Errorf("fetching registration: %w", err)
	}

	// Build a version → downloads lookup from search results
	dlLookup := make(map[string]int, len(r.Versions))
	for _, v := range r.Versions {
		dlLookup[v.Version] = v.Downloads
	}

	var versions []PackageVersion
	for _, page := range regIdx.Items {
		items := page.Items
		if len(items) == 0 {
			// Page not inlined — fetch it separately
			var fullPage registrationPage
			if err := s.getJSON(page.ID, &fullPage); err != nil {
				return nil, fmt.Errorf("fetching page %s: %w", page.ID, err)
			}
			items = fullPage.Items
		}

		for _, leaf := range items {
			ce := leaf.CatalogEntry
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
				SemVer:     ParseSemVer(ce.Version),
				Downloads:  dlLookup[ce.Version],
				Frameworks: frameworks,
			})
		}
	}

	logger.Debug("[%s] enriched %q: %d versions across %d registration page(s)", s.sourceName, r.ID, len(versions), len(regIdx.Items))

	// Sort newest → oldest
	sortVersionsDesc(versions)

	authors := NewSet[string]()
	for _, a := range r.Authors {
		authors.Add(a)
	}
	tags := NewSet[string]()
	for _, t := range r.Tags {
		tags.Add(t)
	}

	return &PackageInfo{
		ID:             r.ID,
		LatestVersion:  r.Version,
		Description:    r.Description,
		Authors:        authors,
		Tags:           tags,
		TotalDownloads: r.TotalDownloads,
		Verified:       r.Verified,
		Versions:       versions,
	}, nil
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

// ─────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────

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

func (s *NugetService) getJSON(u string, dst any) error {
	resp, err := s.client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s for %s", resp.Status, u)
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

func sortVersionsDesc(vs []PackageVersion) {
	for i := 1; i < len(vs); i++ {
		for j := i; j > 0 && vs[j].SemVer.IsNewerThan(vs[j-1].SemVer); j-- {
			vs[j], vs[j-1] = vs[j-1], vs[j]
		}
	}
}
