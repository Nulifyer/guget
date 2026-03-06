package main

import (
	"fmt"
	"strings"
	"time"

	bubbles_viewport "charm.land/bubbles/v2/viewport"
	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

const releaseListWidth = 22 // width of the left release list panel

func newReleaseNotesOverlay(m *App, title string) releaseNotesOverlay {
	rn := releaseNotesOverlay{
		sectionBase: sectionBase{app: m, basePct: 85, minWidth: 60, maxMargin: 4, active: true},
		loading:     true,
		title:       title,
	}
	_, rightW := rn.panelWidths()
	_, overlayH := rn.overlaySize()
	vpH := overlayH - 4
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

	// Check for git repository URL (prefer RepositoryURL, fall back to ProjectURL)
	repoURL := row.info.RepositoryURL
	if repoURL == "" {
		repoURL = row.info.ProjectURL
	}
	logTrace("openReleaseNotes: %s repoURL=%q", row.info.ID, repoURL)
	owner, repo := parseGitHubRepo(repoURL)

	if owner != "" && repo != "" {
		logTrace("openReleaseNotes: %s → GitHub %s/%s, fetching release list", row.info.ID, owner, repo)
		rn := newReleaseNotesOverlay(m, title)
		rn.owner = owner
		rn.repo = repo
		rn.vp.SetContent(m.ctx.Spinner.View() + " " + styleSubtle.Render("Loading releases…"))
		m.releaseNotes = rn
		return m.releaseNotes.fetchReleaseListCmd(owner, repo)
	}

	// No GitHub repo in metadata — try nuspec for repo URL or release notes.
	logTrace("openReleaseNotes: %s → no GitHub repo in metadata, trying nuspec", row.info.ID)
	rn := newReleaseNotesOverlay(m, title)
	rn.vp.SetContent(m.ctx.Spinner.View() + " " + styleSubtle.Render("Checking NuGet release notes…"))
	m.releaseNotes = rn
	pkgID := row.info.ID
	version := row.info.LatestVersion
	var svc *NugetService
	for _, s := range m.ctx.NugetServices {
		if strings.EqualFold(s.SourceName(), row.source) {
			svc = s
			break
		}
	}
	if svc == nil {
		return func() bubble_tea.Msg {
			return releaseNotesReadyMsg{err: fmt.Errorf("no release notes available")}
		}
	}
	return func() bubble_tea.Msg {
		// Check if the nuspec has a GitHub repo URL the registration API missed
		nuspecRepo := svc.FetchNuspecRepoURL(pkgID, version)
		if nuspecOwner, nuspecRepoName := parseGitHubRepo(nuspecRepo); nuspecOwner != "" && nuspecRepoName != "" {
			logTrace("openReleaseNotes: %s → nuspec has GitHub repo %s/%s, fetching releases", pkgID, nuspecOwner, nuspecRepoName)
			releases, err := FetchGitHubReleases(nuspecOwner, nuspecRepoName, 20)
			return releaseListReadyMsg{releases: releases, err: err, owner: nuspecOwner, repo: nuspecRepoName}
		}
		// Fall back to inline release notes from nuspec
		notes := svc.FetchNuspecReleaseNotes(pkgID, version)
		if notes == "" {
			return releaseNotesReadyMsg{err: fmt.Errorf("no release notes available")}
		}
		return releaseNotesReadyMsg{body: notes}
	}
}

func (s *releaseNotesOverlay) fetchReleaseListCmd(owner, repo string) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		releases, err := FetchGitHubReleases(owner, repo, 20)
		return releaseListReadyMsg{releases: releases, err: err}
	}
}

func (s *releaseNotesOverlay) fetchReleaseNotesCmd(rel GitHubRelease) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		return releaseNotesReadyMsg{body: rel.Body, htmlURL: rel.HTMLURL}
	}
}

func (s *releaseNotesOverlay) FooterKeys() []kv {
	return []kv{{"tab", "focus"}, {"↑↓", "nav/scroll"}, {"esc", "close"}}
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
	case "tab", "shift+tab":
		s.focusRight = !s.focusRight
		return nil
	case "up", "k":
		if s.focusRight {
			// scroll notes viewport
			var cmd bubble_tea.Cmd
			s.vp, cmd = s.vp.Update(msg)
			return cmd
		}
		// navigate release list
		if len(s.releases) > 0 && s.cursor > 0 {
			s.cursor--
			s.loading = true
			return s.fetchReleaseNotesCmd(s.releases[s.cursor])
		}
		return nil
	case "down", "j":
		if s.focusRight {
			var cmd bubble_tea.Cmd
			s.vp, cmd = s.vp.Update(msg)
			return cmd
		}
		if len(s.releases) > 0 && s.cursor < len(s.releases)-1 {
			s.cursor++
			s.loading = true
			return s.fetchReleaseNotesCmd(s.releases[s.cursor])
		}
		return nil
	default:
		// pass everything else to viewport (pgup/pgdn, home/end, etc.)
		var cmd bubble_tea.Cmd
		s.vp, cmd = s.vp.Update(msg)
		return cmd
	}
}

func (s *releaseNotesOverlay) overlaySize() (w, h int) {
	w = s.Width()
	h = s.app.overlayHeight() - 4
	return
}

func (s *releaseNotesOverlay) buildContent() string {
	var sb strings.Builder

	if len(s.releases) > 0 {
		rel := s.releases[s.cursor]

		// Title
		title := rel.TagName
		if rel.Name != "" && rel.Name != rel.TagName {
			title = rel.Name + " (" + rel.TagName + ")"
		}
		sb.WriteString(styleAccentBold.Render(title))

		// Date
		if rel.PublishedAt != "" {
			if t, err := time.Parse(time.RFC3339, rel.PublishedAt); err == nil {
				sb.WriteString("  " + styleMuted.Render(t.Format("2006-01-02")))
			}
		}

		// Link
		if s.notesURL != "" {
			sb.WriteString("  " + hyperlink(s.notesURL, styleSubtle.Render("view on GitHub")))
		}

		sb.WriteString("\n")
		sb.WriteString(styleBorder.Render(strings.Repeat("─", s.vp.Width())) + "\n")
	}

	body := s.notes
	if body == "" {
		body = styleMuted.Render("(no release notes)")
	}
	// Word-wrap the body to fit within the viewport width
	if s.vp.Width() > 0 {
		body = lipgloss.NewStyle().Width(s.vp.Width()).Render(body)
	}
	sb.WriteString(body)
	return sb.String()
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
	// bodyH = overlayH - 4 (title(1) + titleDivider(1) + tableHeader(1) + headerBorder(1))
	// (overlay padding already subtracted in overlaySize)
	vpH := overlayH - 4
	if vpH < 4 {
		vpH = 4
	}
	s.vp.SetWidth(rightW)
	s.vp.SetHeight(vpH)
	// Re-wrap content at new width
	if !s.loading {
		s.vp.SetContent(s.buildContent())
	}
}

func (s *releaseNotesOverlay) Render() string {
	overlayW, overlayH := s.overlaySize()
	// innerW is the usable content width inside styleOverlay (border 2 + padding 4 = 6)
	innerW := overlayW - 6
	listW, rightW := s.panelWidths()
	focusLeft := !s.focusRight
	div := styleBorder.Render("│")

	// bodyH = overlayH minus title(1) + titleDivider(1) + columnHeaders(1) + headerDivider(1)
	// overlayH already excludes styleOverlay chrome
	bodyH := overlayH - 4
	if bodyH < 1 {
		bodyH = 1
	}

	// ── Title ──
	title := styleAccentBold.Render(s.title)
	titleDivider := styleBorder.Render(strings.Repeat("─", innerW))

	// ── Column headers ──
	leftHdr := " Releases"
	rightHdr := " Notes"
	if focusLeft {
		leftHdr = styleAccentBold.Render(leftHdr)
		rightHdr = lipgloss.NewStyle().Foreground(colorSubtle).Bold(true).Render(rightHdr)
	} else {
		leftHdr = lipgloss.NewStyle().Foreground(colorSubtle).Bold(true).Render(leftHdr)
		rightHdr = styleAccentBold.Render(rightHdr)
	}
	headerLine := padRight(leftHdr, listW) + div + padRight(rightHdr, rightW)
	headerDivider := styleBorder.Render(strings.Repeat("─", listW) + "┼" + strings.Repeat("─", rightW))

	// ── Left panel: release list (scrolled) ──
	maxTagW := listW - 3 // prefix "▶ " (2) + left margin (1)
	if maxTagW < 5 {
		maxTagW = 5
	}
	var allLeft []string
	if s.loading && len(s.releases) == 0 {
		allLeft = append(allLeft, " "+s.app.ctx.Spinner.View()+" "+styleSubtle.Render("Loading…"))
	}
	for i, rel := range s.releases {
		tag := truncate(rel.TagName, maxTagW)
		if i == s.cursor {
			allLeft = append(allLeft, styleAccent.Render(" ▶ "+tag))
		} else {
			allLeft = append(allLeft, styleMuted.Render("   "+tag))
		}
	}
	// Scroll window: keep cursor visible
	scrollStart := 0
	if s.cursor >= bodyH {
		scrollStart = s.cursor - bodyH + 1
	}
	var leftLines []string
	for i := scrollStart; i < len(allLeft) && i < scrollStart+bodyH; i++ {
		leftLines = append(leftLines, padRight(allLeft[i], listW))
	}
	// Pad to bodyH
	for len(leftLines) < bodyH {
		leftLines = append(leftLines, strings.Repeat(" ", listW))
	}

	// ── Right panel: viewport or loading state ──
	var rightLines []string
	if s.loading {
		line := " " + s.app.ctx.Spinner.View() + " " + styleSubtle.Render("Loading release notes…")
		rightLines = append(rightLines, padRight(line, rightW))
		for len(rightLines) < bodyH {
			rightLines = append(rightLines, strings.Repeat(" ", rightW))
		}
	} else if len(s.releases) == 0 && s.nuspecNotes != "" {
		line := " " + styleText.Render(s.nuspecNotes)
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
			right = rightLines[i]
		}
		bodyLines = append(bodyLines, left+div+right)
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		title,
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
