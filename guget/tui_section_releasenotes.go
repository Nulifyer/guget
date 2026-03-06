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

func (m *Model) openReleaseNotes() bubble_tea.Cmd {
	if m.packageCursor >= len(m.packageRows) {
		return nil
	}
	row := m.packageRows[m.packageCursor]
	if row.info == nil {
		return nil
	}
	m.ctx.StatusLine = ""

	_, rightW := m.releaseNotesPanelWidths()
	_, overlayH := m.releaseNotesOverlaySize()
	vpH := overlayH - 4 // title(1) + titleDivider(1) + colHeaders(1) + headerBorder(1)
	if vpH < 4 {
		vpH = 4
	}
	vp := bubbles_viewport.New(bubbles_viewport.WithWidth(rightW), bubbles_viewport.WithHeight(vpH))

	// Check for git repository URL (prefer RepositoryURL, fall back to ProjectURL)
	repoURL := row.info.RepositoryURL
	if repoURL == "" {
		repoURL = row.info.ProjectURL
	}
	logTrace("openReleaseNotes: %s repoURL=%q", row.info.ID, repoURL)
	owner, repo := parseGitHubRepo(repoURL)

	if owner != "" && repo != "" {
		logTrace("openReleaseNotes: %s → GitHub %s/%s, fetching release list", row.info.ID, owner, repo)
		vp.SetContent(m.ctx.Spinner.View() + " " + styleSubtle.Render("Loading releases…"))
		m.releaseNotes = releaseNotesOverlay{
			active:  true,
			loading: true,
			title:   row.info.ID + " — Release Notes",
			vp:      vp,
			owner:   owner,
			repo:    repo,
		}
		return m.fetchReleaseListCmd(owner, repo)
	}

	// No GitHub repo in metadata — try nuspec for repo URL or release notes.
	logTrace("openReleaseNotes: %s → no GitHub repo in metadata, trying nuspec", row.info.ID)
	vp.SetContent(m.ctx.Spinner.View() + " " + styleSubtle.Render("Checking NuGet release notes…"))
	m.releaseNotes = releaseNotesOverlay{
		active:  true,
		loading: true,
		title:   row.info.ID + " — Release Notes",
		vp:      vp,
	}
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

func (m *Model) fetchReleaseListCmd(owner, repo string) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		releases, err := FetchGitHubReleases(owner, repo, 20)
		return releaseListReadyMsg{releases: releases, err: err}
	}
}

func (m *Model) fetchReleaseNotesCmd(rel GitHubRelease) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		return releaseNotesReadyMsg{body: rel.Body, htmlURL: rel.HTMLURL}
	}
}

func (m *Model) handleReleaseNotesKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		m.overlayWidthOffset -= 4
		m.resizeReleaseNotesViewport()
		return nil
	case "]":
		m.overlayWidthOffset += 4
		m.resizeReleaseNotesViewport()
		return nil
	case "esc", "q":
		m.overlayWidthOffset = 0
		m.releaseNotes.active = false
		m.ctx.StatusLine = ""
		return nil
	case "tab", "shift+tab":
		m.releaseNotes.focusRight = !m.releaseNotes.focusRight
		return nil
	case "up", "k":
		if m.releaseNotes.focusRight {
			// scroll notes viewport
			var cmd bubble_tea.Cmd
			m.releaseNotes.vp, cmd = m.releaseNotes.vp.Update(msg)
			return cmd
		}
		// navigate release list
		if len(m.releaseNotes.releases) > 0 && m.releaseNotes.cursor > 0 {
			m.releaseNotes.cursor--
			m.releaseNotes.loading = true
			return m.fetchReleaseNotesCmd(m.releaseNotes.releases[m.releaseNotes.cursor])
		}
		return nil
	case "down", "j":
		if m.releaseNotes.focusRight {
			var cmd bubble_tea.Cmd
			m.releaseNotes.vp, cmd = m.releaseNotes.vp.Update(msg)
			return cmd
		}
		if len(m.releaseNotes.releases) > 0 && m.releaseNotes.cursor < len(m.releaseNotes.releases)-1 {
			m.releaseNotes.cursor++
			m.releaseNotes.loading = true
			return m.fetchReleaseNotesCmd(m.releaseNotes.releases[m.releaseNotes.cursor])
		}
		return nil
	default:
		// pass everything else to viewport (pgup/pgdn, home/end, etc.)
		var cmd bubble_tea.Cmd
		m.releaseNotes.vp, cmd = m.releaseNotes.vp.Update(msg)
		return cmd
	}
}

func (m Model) releaseNotesOverlaySize() (w, h int) {
	w = clampW(m.ctx.Width*85/100+m.overlayWidthOffset, 60, m.ctx.Width-4)
	h = m.overlayHeight() - 4
	return
}

func (m Model) buildReleaseNotesContent() string {
	rn := m.releaseNotes
	var s strings.Builder

	if len(rn.releases) > 0 {
		rel := rn.releases[rn.cursor]

		// Title
		title := rel.TagName
		if rel.Name != "" && rel.Name != rel.TagName {
			title = rel.Name + " (" + rel.TagName + ")"
		}
		s.WriteString(styleAccentBold.Render(title))

		// Date
		if rel.PublishedAt != "" {
			if t, err := time.Parse(time.RFC3339, rel.PublishedAt); err == nil {
				s.WriteString("  " + styleMuted.Render(t.Format("2006-01-02")))
			}
		}

		// Link
		if rn.notesURL != "" {
			s.WriteString("  " + hyperlink(rn.notesURL, styleSubtle.Render("view on GitHub")))
		}

		s.WriteString("\n")
		s.WriteString(styleBorder.Render(strings.Repeat("─", m.releaseNotes.vp.Width())) + "\n")
	}

	body := rn.notes
	if body == "" {
		body = styleMuted.Render("(no release notes)")
	}
	// Word-wrap the body to fit within the viewport width
	if m.releaseNotes.vp.Width() > 0 {
		body = lipgloss.NewStyle().Width(m.releaseNotes.vp.Width()).Render(body)
	}
	s.WriteString(body)
	return s.String()
}

func (m *Model) releaseNotesPanelWidths() (listW, rightW int) {
	overlayW, _ := m.releaseNotesOverlaySize()
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

func (m *Model) resizeReleaseNotesViewport() {
	_, rightW := m.releaseNotesPanelWidths()
	_, overlayH := m.releaseNotesOverlaySize()
	// bodyH = overlayH - 4 (title(1) + titleDivider(1) + tableHeader(1) + headerBorder(1))
	// (overlay padding already subtracted in overlaySize)
	vpH := overlayH - 4
	if vpH < 4 {
		vpH = 4
	}
	m.releaseNotes.vp.SetWidth(rightW)
	m.releaseNotes.vp.SetHeight(vpH)
	// Re-wrap content at new width
	if !m.releaseNotes.loading {
		m.releaseNotes.vp.SetContent(m.buildReleaseNotesContent())
	}
}

func (m Model) renderReleaseNotesOverlay() string {
	overlayW, overlayH := m.releaseNotesOverlaySize()
	// innerW is the usable content width inside styleOverlay (border 2 + padding 4 = 6)
	innerW := overlayW - 6
	listW, rightW := m.releaseNotesPanelWidths()
	focusLeft := !m.releaseNotes.focusRight
	div := styleBorder.Render("│")

	// bodyH = overlayH minus title(1) + titleDivider(1) + columnHeaders(1) + headerDivider(1)
	// overlayH already excludes styleOverlay chrome
	bodyH := overlayH - 4
	if bodyH < 1 {
		bodyH = 1
	}

	// ── Title ──
	title := styleAccentBold.Render(m.releaseNotes.title)
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
	if m.releaseNotes.loading && len(m.releaseNotes.releases) == 0 {
		allLeft = append(allLeft, " "+m.ctx.Spinner.View()+" "+styleSubtle.Render("Loading…"))
	}
	for i, rel := range m.releaseNotes.releases {
		tag := truncate(rel.TagName, maxTagW)
		if i == m.releaseNotes.cursor {
			allLeft = append(allLeft, styleAccent.Render(" ▶ "+tag))
		} else {
			allLeft = append(allLeft, styleMuted.Render("   "+tag))
		}
	}
	// Scroll window: keep cursor visible
	scrollStart := 0
	if m.releaseNotes.cursor >= bodyH {
		scrollStart = m.releaseNotes.cursor - bodyH + 1
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
	if m.releaseNotes.loading {
		line := " " + m.ctx.Spinner.View() + " " + styleSubtle.Render("Loading release notes…")
		rightLines = append(rightLines, padRight(line, rightW))
		for len(rightLines) < bodyH {
			rightLines = append(rightLines, strings.Repeat(" ", rightW))
		}
	} else if len(m.releaseNotes.releases) == 0 && m.releaseNotes.nuspecNotes != "" {
		line := " " + styleText.Render(m.releaseNotes.nuspecNotes)
		rightLines = append(rightLines, padRight(line, rightW))
		for len(rightLines) < bodyH {
			rightLines = append(rightLines, strings.Repeat(" ", rightW))
		}
	} else {
		vpView := m.releaseNotes.vp.View()
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

	return lipgloss.Place(m.ctx.Width, m.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}
