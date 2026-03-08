package main

import (
	"fmt"
	"strings"
	"time"

	bubbles_viewport "charm.land/bubbles/v2/viewport"
	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

const releaseListWidth = 22 // width of the left release/version list panel

func newReleaseNotesOverlay(m *App, title string) releaseNotesOverlay {
	rn := releaseNotesOverlay{
		sectionBase: sectionBase{app: m, basePct: 100, minWidth: 60, maxMargin: 0, active: true},
		title:       title,
	}
	_, rightW := rn.panelWidths()
	_, overlayH := rn.overlaySize()
	vpH := overlayH - 5 // title + tabBar + divider + colHeader + colDivider
	if vpH < 4 {
		vpH = 4
	}
	rn.vp = bubbles_viewport.New(bubbles_viewport.WithWidth(rightW), bubbles_viewport.WithHeight(vpH))
	return rn
}

func (m *App) openReleaseNotes() bubble_tea.Cmd {
	if m.packages.cursor >= len(m.packages.rows) {
		return nil
	}
	row := m.packages.rows[m.packages.cursor]
	if row.info == nil {
		return nil
	}
	m.ctx.StatusLine = ""

	title := row.info.ID + " — Release Notes"
	rn := newReleaseNotesOverlay(m, title)
	rn.nsPkgID = row.info.ID

	// Find the NuGet service for this package's source.
	for _, s := range m.ctx.NugetServices {
		if strings.EqualFold(s.SourceName(), row.source) {
			rn.nsSvc = s
			break
		}
	}

	// Build NuSpec version list from the already-fetched PackageInfo.
	nsVersions := make([]string, 0, len(row.info.Versions))
	for _, v := range row.info.Versions {
		nsVersions = append(nsVersions, v.SemVer.String())
	}

	// Check for GitHub repository URL.
	repoURL := row.info.RepositoryURL
	if repoURL == "" {
		repoURL = row.info.ProjectURL
	}
	logTrace("openReleaseNotes: %s repoURL=%q", row.info.ID, repoURL)
	owner, repo := parseGitHubRepo(repoURL)

	var cmds []bubble_tea.Cmd

	// GitHub fetch
	if owner != "" && repo != "" {
		logTrace("openReleaseNotes: %s → GitHub %s/%s", row.info.ID, owner, repo)
		rn.ghOwner = owner
		rn.ghRepo = repo
		rn.ghLoading = true
		cmds = append(cmds, fetchGitHubReleasesCmd(owner, repo))
	}

	// NuSpec: we have the version list already; start fetching notes for the latest version.
	if rn.nsSvc != nil && len(nsVersions) > 0 {
		rn.nsVersions = nsVersions
		rn.nsLoading = true
		svc := rn.nsSvc
		pkgID := rn.nsPkgID
		latestVer := nsVersions[0]

		if owner == "" {
			// No GitHub repo in metadata — fetch nuspec once, extract both repo
			// URL (to discover GitHub releases) and inline release notes.
			rn.ghLoading = true
			cmds = append(cmds, func() bubble_tea.Msg {
				body := svc.FetchNuspec(pkgID, latestVer)
				notes := ExtractNuspecReleaseNotes(body)
				nuspecRepo := ExtractNuspecRepoURL(body)
				if nuspecOwner, nuspecRepoName := parseGitHubRepo(nuspecRepo); nuspecOwner != "" && nuspecRepoName != "" {
					logTrace("openReleaseNotes: %s → nuspec has GitHub repo %s/%s", pkgID, nuspecOwner, nuspecRepoName)
					releases, err := FetchGitHubReleases(nuspecOwner, nuspecRepoName, 20)
					return releaseListReadyMsg{
						releases: releases, err: err,
						owner: nuspecOwner, repo: nuspecRepoName,
						nuspecNotes: notes, nuspecVer: latestVer,
					}
				}
				return releaseListReadyMsg{
					err:         fmt.Errorf("no repository found"),
					nuspecNotes: notes, nuspecVer: latestVer,
				}
			})
		} else {
			cmds = append(cmds, fetchNuspecVersionNotesCmd(svc, pkgID, latestVer))
		}
	} else if owner == "" && rn.nsSvc != nil {
		// No versions available but we can still try to discover GitHub repo from nuspec.
		rn.ghLoading = true
		svc := rn.nsSvc
		pkgID := rn.nsPkgID
		version := row.info.LatestVersion
		cmds = append(cmds, func() bubble_tea.Msg {
			body := svc.FetchNuspec(pkgID, version)
			nuspecRepo := ExtractNuspecRepoURL(body)
			if nuspecOwner, nuspecRepoName := parseGitHubRepo(nuspecRepo); nuspecOwner != "" && nuspecRepoName != "" {
				logTrace("openReleaseNotes: %s → nuspec has GitHub repo %s/%s", pkgID, nuspecOwner, nuspecRepoName)
				releases, err := FetchGitHubReleases(nuspecOwner, nuspecRepoName, 20)
				return releaseListReadyMsg{releases: releases, err: err, owner: nuspecOwner, repo: nuspecRepoName}
			}
			return releaseListReadyMsg{err: fmt.Errorf("no repository found")}
		})
	}

	if len(cmds) == 0 {
		rn.ghErr = fmt.Errorf("no release notes available")
		rn.nsErr = fmt.Errorf("no release notes available")
	}

	// Default to GitHub tab if we're fetching it, otherwise NuSpec.
	if rn.ghLoading {
		rn.activeTab = tabReleases
	} else {
		rn.activeTab = tabNuSpec
	}

	m.releaseNotes = rn
	return bubble_tea.Batch(cmds...)
}

func fetchGitHubReleasesCmd(owner, repo string) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		releases, err := FetchGitHubReleases(owner, repo, 20)
		return releaseListReadyMsg{releases: releases, err: err}
	}
}

func fetchNuspecVersionNotesCmd(svc *NugetService, pkgID, version string) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		body := svc.FetchNuspec(pkgID, version)
		notes := ExtractNuspecReleaseNotes(body)
		return nuspecVersionNotesReadyMsg{version: version, notes: notes}
	}
}

func (s *releaseNotesOverlay) fetchReleaseNotesCmd(rel GitHubRelease) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		return releaseNotesReadyMsg{body: rel.Body, htmlURL: rel.HTMLURL}
	}
}

func (s *releaseNotesOverlay) FooterKeys() []kv {
	return []kv{
		{"1/2", "tab"},
		{"tab", "focus"},
		{"↑↓", "nav/scroll"},
		{"esc", "close"},
	}
}

func (s *releaseNotesOverlay) HandleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		s.Resize(-4)
		s.resizeViewport()
		return nil
	case "]":
		s.Resize(4)
		s.resizeViewport()
		return nil
	case "esc", "q":
		s.closeOverlay()
		return nil
	case "1":
		if s.ghAvailable || s.ghLoading || len(s.ghReleases) > 0 {
			s.activeTab = tabReleases
			s.updateViewportContent()
		}
		return nil
	case "2":
		if s.nsAvailable || s.nsLoading || len(s.nsVersions) > 0 {
			s.activeTab = tabNuSpec
			s.updateViewportContent()
		}
		return nil
	case "tab", "shift+tab":
		s.focusRight = !s.focusRight
		return nil
	case "up", "k":
		if s.focusRight {
			var cmd bubble_tea.Cmd
			s.vp, cmd = s.vp.Update(msg)
			return cmd
		}
		return s.moveCursor(-1)
	case "down", "j":
		if s.focusRight {
			var cmd bubble_tea.Cmd
			s.vp, cmd = s.vp.Update(msg)
			return cmd
		}
		return s.moveCursor(1)
	default:
		var cmd bubble_tea.Cmd
		s.vp, cmd = s.vp.Update(msg)
		return cmd
	}
}

func (s *releaseNotesOverlay) moveCursor(delta int) bubble_tea.Cmd {
	switch s.activeTab {
	case tabReleases:
		next := s.ghCursor + delta
		if next < 0 || next >= len(s.ghReleases) {
			return nil
		}
		s.ghCursor = next
		s.ghLoading = true
		s.ghNotes = ""
		return s.fetchReleaseNotesCmd(s.ghReleases[next])
	case tabNuSpec:
		next := s.nsCursor + delta
		if next < 0 || next >= len(s.nsVersions) {
			return nil
		}
		s.nsCursor = next
		ver := s.nsVersions[next]
		// Return cached notes if we've already fetched this version.
		if cached, ok := s.nsNotesCache[ver]; ok {
			s.nsNotes = cached
			s.updateViewportContent()
			return nil
		}
		s.nsLoading = true
		s.nsNotes = ""
		if s.nsSvc != nil {
			return fetchNuspecVersionNotesCmd(s.nsSvc, s.nsPkgID, ver)
		}
	}
	return nil
}

func (s *releaseNotesOverlay) isLoading() bool {
	switch s.activeTab {
	case tabReleases:
		return s.ghLoading
	case tabNuSpec:
		return s.nsLoading
	}
	return false
}

func (s *releaseNotesOverlay) overlaySize() (w, h int) {
	w = s.Width()
	h = s.app.overlayHeight() - 4
	return
}

func (s *releaseNotesOverlay) panelWidths() (listW, rightW int) {
	overlayW, _ := s.overlaySize()
	innerW := overlayW - 6 // subtract styleOverlay border(2) + padding(4)
	listW = releaseListWidth
	if listW > innerW/3 {
		listW = innerW / 3
	}
	rightW = innerW - listW - 3 // 3 = column border(1) + right column padding(2)
	if rightW < 20 {
		rightW = 20
	}
	return
}

func (s *releaseNotesOverlay) resizeViewport() {
	_, rightW := s.panelWidths()
	_, overlayH := s.overlaySize()
	vpH := overlayH - 5 // title + tabBar + divider + colHeader + colDivider
	if vpH < 4 {
		vpH = 4
	}
	s.vp.SetWidth(rightW)
	s.vp.SetHeight(vpH)
	if !s.isLoading() {
		s.updateViewportContent()
	}
}

func (s *releaseNotesOverlay) updateViewportContent() {
	s.vp.SetContent(s.buildContent())
}

func (s *releaseNotesOverlay) buildContent() string {
	switch s.activeTab {
	case tabReleases:
		return s.buildGitHubContent()
	case tabNuSpec:
		return s.buildNuSpecContent()
	}
	return ""
}

func (s *releaseNotesOverlay) buildGitHubContent() string {
	if s.ghErr != nil && len(s.ghReleases) == 0 {
		return " " + styleRed.Render("Error: "+s.ghErr.Error())
	}
	if len(s.ghReleases) == 0 {
		return " " + styleMuted.Render("(no releases found)")
	}

	var sb strings.Builder
	rel := s.ghReleases[s.ghCursor]

	title := rel.TagName
	if rel.Name != "" && rel.Name != rel.TagName {
		title = rel.Name + " (" + rel.TagName + ")"
	}
	sb.WriteString(styleAccentBold.Render(title))

	if rel.PublishedAt != "" {
		if t, err := time.Parse(time.RFC3339, rel.PublishedAt); err == nil {
			sb.WriteString("  " + styleMuted.Render(t.Format("2006-01-02")))
		}
	}

	if s.ghNotesURL != "" {
		sb.WriteString("  " + hyperlink(s.ghNotesURL, styleSubtle.Render("view on GitHub")))
	}

	sb.WriteString("\n")
	sb.WriteString(styleBorder.Render(strings.Repeat("─", s.vp.Width())) + "\n")

	body := s.ghNotes
	if body == "" {
		body = styleMuted.Render("(no release notes)")
	}
	if s.vp.Width() > 0 {
		body = lipgloss.NewStyle().Width(s.vp.Width()).Render(body)
	}
	sb.WriteString(body)
	return sb.String()
}

func (s *releaseNotesOverlay) buildNuSpecContent() string {
	if s.nsErr != nil && len(s.nsVersions) == 0 {
		return styleRed.Render("Error: " + s.nsErr.Error())
	}
	if len(s.nsVersions) == 0 {
		return styleMuted.Render("(no versions found)")
	}

	var sb strings.Builder
	ver := s.nsVersions[s.nsCursor]
	sb.WriteString(styleAccentBold.Render(ver))
	sb.WriteString("\n")
	sb.WriteString(styleBorder.Render(strings.Repeat("─", s.vp.Width())) + "\n")

	body := s.nsNotes
	if body == "" {
		body = styleMuted.Render("(no release notes for this version)")
	}
	if s.vp.Width() > 0 {
		body = lipgloss.NewStyle().Width(s.vp.Width()).Render(body)
	}
	sb.WriteString(body)
	return sb.String()
}

func (s *releaseNotesOverlay) tabLabel(tab releaseNotesTab) string {
	switch tab {
	case tabReleases:
		label := "Releases"
		if s.ghLoading && len(s.ghReleases) == 0 {
			return label + " " + s.app.ctx.Spinner.View()
		}
		if s.ghErr != nil && !s.ghAvailable {
			return label + " ✗"
		}
		return label
	case tabNuSpec:
		label := "NuSpec"
		if s.nsLoading && len(s.nsVersions) == 0 {
			return label + " " + s.app.ctx.Spinner.View()
		}
		if s.nsErr != nil && !s.nsAvailable {
			return label + " ✗"
		}
		return label
	}
	return ""
}

func (s *releaseNotesOverlay) Render() string {
	overlayW, overlayH := s.overlaySize()
	innerW := overlayW - 6
	listW, rightW := s.panelWidths()
	focusLeft := !s.focusRight
	div := styleBorder.Render("│")

	// bodyH = overlayH minus title(1) + tabBar(1) + divider(1) + colHeaders(1) + colDivider(1)
	bodyH := overlayH - 5
	if bodyH < 1 {
		bodyH = 1
	}

	// ── Title ──
	title := styleAccentBold.Render(s.title)

	// ── Tab bar ──
	ghLabel := s.tabLabel(tabReleases)
	nsLabel := s.tabLabel(tabNuSpec)
	if s.activeTab == tabReleases {
		ghLabel = styleMuted.Render("[1] ") + styleAccentBold.Render(ghLabel)
		nsLabel = styleMuted.Render("[2] " + nsLabel)
	} else {
		ghLabel = styleMuted.Render("[1] " + ghLabel)
		nsLabel = styleMuted.Render("[2] ") + styleAccentBold.Render(nsLabel)
	}
	tabBar := ghLabel + styleBorder.Render(" │ ") + nsLabel

	titleDivider := styleBorder.Render(strings.Repeat("─", innerW))

	// ── Column headers ──
	var leftHdr, rightHdr string
	if s.activeTab == tabReleases {
		leftHdr = "Releases"
	} else {
		leftHdr = "Versions"
	}
	rightHdr = " Notes"
	if focusLeft {
		leftHdr = styleAccentBold.Render(leftHdr)
		rightHdr = lipgloss.NewStyle().Foreground(colorSubtle).Bold(true).Render(rightHdr)
	} else {
		leftHdr = lipgloss.NewStyle().Foreground(colorSubtle).Bold(true).Render(leftHdr)
		rightHdr = styleAccentBold.Render(rightHdr)
	}
	headerLine := padRight(leftHdr, listW) + div + padRight(rightHdr, rightW+2)
	headerDivider := styleBorder.Render(strings.Repeat("─", listW) + "┼" + strings.Repeat("─", rightW+2))

	// ── Left panel ──
	maxTagW := listW - 3 // prefix "▶ " (2) + left margin (1)
	if maxTagW < 5 {
		maxTagW = 5
	}
	var allLeft []string
	loading := s.isLoading()

	switch s.activeTab {
	case tabReleases:
		if s.ghLoading && len(s.ghReleases) == 0 {
			allLeft = append(allLeft, s.app.ctx.Spinner.View()+" "+styleSubtle.Render("Loading..."))
		}
		for i, rel := range s.ghReleases {
			tag := truncate(rel.TagName, maxTagW)
			if i == s.ghCursor {
				allLeft = append(allLeft, styleAccent.Render("▶ "+tag))
			} else {
				allLeft = append(allLeft, styleMuted.Render("  "+tag))
			}
		}
	case tabNuSpec:
		if s.nsLoading && len(s.nsVersions) == 0 {
			allLeft = append(allLeft, s.app.ctx.Spinner.View()+" "+styleSubtle.Render("Loading..."))
		}
		for i, ver := range s.nsVersions {
			tag := truncate(ver, maxTagW)
			if i == s.nsCursor {
				allLeft = append(allLeft, styleAccent.Render("▶ "+tag))
			} else {
				allLeft = append(allLeft, styleMuted.Render("  "+tag))
			}
		}
	}

	// Scroll window: keep cursor visible
	cursor := 0
	if s.activeTab == tabReleases {
		cursor = s.ghCursor
	} else {
		cursor = s.nsCursor
	}
	scrollStart := 0
	if cursor >= bodyH {
		scrollStart = cursor - bodyH + 1
	}
	var leftLines []string
	for i := scrollStart; i < len(allLeft) && i < scrollStart+bodyH; i++ {
		leftLines = append(leftLines, padRight(allLeft[i], listW))
	}
	for len(leftLines) < bodyH {
		leftLines = append(leftLines, strings.Repeat(" ", listW))
	}

	// ── Right panel ──
	var rightLines []string
	if loading {
		line := " " + s.app.ctx.Spinner.View() + " " + styleSubtle.Render("Loading...")
		rightLines = append(rightLines, padRight(line, rightW))
		for len(rightLines) < bodyH {
			rightLines = append(rightLines, strings.Repeat(" ", rightW))
		}
	} else {
		vpView := s.vp.View()
		rightLines = strings.Split(vpView, "\n")
	}

	// ── Join left + divider + right line by line ──
	var bodyLines []string
	for i := 0; i < bodyH; i++ {
		left := ""
		if i < len(leftLines) {
			left = leftLines[i]
		}
		right := ""
		if i < len(rightLines) {
			right = " " + rightLines[i]
		}
		bodyLines = append(bodyLines, left+div+right)
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
		tabBar,
		titleDivider,
		headerLine,
		headerDivider,
		strings.Join(bodyLines, "\n"),
	)

	box := styleOverlay.
		Width(overlayW).
		Render(content)

	return s.centerOverlay(box)
}
