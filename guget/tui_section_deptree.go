package main

import (
	"fmt"
	"os/exec"
	"strings"

	bubbles_viewport "charm.land/bubbles/v2/viewport"
	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
)

func runDepTreeCmd(project *ParsedProject) bubble_tea.Cmd {
	return func() bubble_tea.Msg {
		cmd := exec.Command("dotnet", "list", project.FilePath, "package", "--include-transitive")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return depTreeReadyMsg{err: fmt.Errorf("dotnet list: %w\n%s", err, strings.TrimSpace(string(out)))}
		}
		return depTreeReadyMsg{content: string(out)}
	}
}

func newDepTreeOverlay(m *App, title string, loading bool) depTreeOverlay {
	dt := depTreeOverlay{
		sectionBase: sectionBase{app: m, basePct: 80, minWidth: 40, maxMargin: 4, active: true},
		title:       title,
		loading:     loading,
	}
	m.depTree = dt // assign so depTreeOverlaySize() reads the correct Width()
	overlayW, overlayH := m.depTreeOverlaySize()
	dt.vp = bubbles_viewport.New(bubbles_viewport.WithWidth(overlayW-6), bubbles_viewport.WithHeight(overlayH-8))
	return dt
}

func (m *App) openDepTree() bubble_tea.Cmd {
	if m.packages.cursor >= len(m.packages.rows) {
		return nil
	}
	row := m.packages.rows[m.packages.cursor]
	if row.info == nil {
		return nil
	}
	m.ctx.StatusLine = ""
	// Find the installed version's dependency groups.
	var installedVer *PackageVersion
	for i := range row.info.Versions {
		if row.info.Versions[i].SemVer.String() == row.ref.Version.String() {
			installedVer = &row.info.Versions[i]
			break
		}
	}
	dt := newDepTreeOverlay(m, row.ref.Name+" "+row.ref.Version.String(), false)
	dt.content = dt.formatDepGroups(installedVer)
	dt.vp.SetContent(dt.content)
	m.depTree = dt
	return nil
}

func (m *App) openTransitiveDepTree() bubble_tea.Cmd {
	proj := m.selectedProject()
	if proj == nil {
		return m.setStatus("▲ Select a project first", true)
	}
	m.ctx.StatusLine = ""
	m.depTree = newDepTreeOverlay(m, proj.FileName+" (transitive packages)", true)
	return runDepTreeCmd(proj)
}

func (m *App) depTreeOverlaySize() (w, h int) {
	w = m.depTree.Width()
	h = m.overlayHeight() - 4
	return
}

// formatVersionRange converts NuGet version range notation to a readable form.
// e.g. "[8.0.0, )" → ">= 8.0.0",  "[1.0, 2.0)" → ">= 1.0 < 2.0",  "[1.0.0]" → "1.0.0"
func formatVersionRange(r string) string {
	r = strings.TrimSpace(r)
	if r == "" {
		return "any"
	}
	if len(r) < 2 {
		return r
	}
	startInclusive := r[0] == '['
	endInclusive := r[len(r)-1] == ']'

	// Exact version: [1.0.0] with no comma
	if startInclusive && endInclusive && !strings.Contains(r, ",") {
		return strings.Trim(r, "[]")
	}

	inner := r[1 : len(r)-1]
	parts := strings.SplitN(inner, ",", 2)
	if len(parts) != 2 {
		return r // not a recognised range — return as-is
	}
	low := strings.TrimSpace(parts[0])
	high := strings.TrimSpace(parts[1])

	var result strings.Builder
	if low != "" {
		if startInclusive {
			result.WriteString(">= ")
		} else {
			result.WriteString("> ")
		}
		result.WriteString(low)
	}
	if high != "" {
		if result.Len() > 0 {
			result.WriteString(" ")
		}
		if endInclusive {
			result.WriteString("<= ")
		} else {
			result.WriteString("< ")
		}
		result.WriteString(high)
	}
	if result.Len() == 0 {
		return "any"
	}
	return result.String()
}

func (s *depTreeOverlay) formatDepGroups(v *PackageVersion) string {
	if v == nil || len(v.DependencyGroups) == 0 {
		return styleMuted.Render("(no dependency information available)")
	}
	// Compute max dependency name width for column alignment.
	maxNameW := 20
	for _, dg := range v.DependencyGroups {
		for _, dep := range dg.Dependencies {
			if w := lipgloss.Width(dep.ID); w > maxNameW {
				maxNameW = w
			}
		}
	}
	maxNameW += 2

	var sb strings.Builder
	for _, dg := range v.DependencyGroups {
		fw := dg.TargetFramework
		if fw == "" {
			fw = "any"
		}
		sb.WriteString(styleAccentBold.Render("["+fw+"]") + "\n")
		if len(dg.Dependencies) == 0 {
			sb.WriteString(styleMuted.Render("  (no dependencies)") + "\n")
		} else {
			for _, dep := range dg.Dependencies {
				icon, iconStyle := " ", styleMuted
				if row := s.app.rowByName(dep.ID); row != nil {
					icon, iconStyle = row.statusIcon(), row.statusStyle()
				}
				rangeStr := formatVersionRange(dep.Range)
				sb.WriteString("  " + iconStyle.Render(icon) + " ")
				sb.WriteString(styleText.Render(padRight(dep.ID, maxNameW)) +
					styleSubtle.Render(rangeStr) + "\n")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func (s *depTreeOverlay) buildContent() string {
	if s.err != nil {
		return styleRed.Render("Error: " + s.err.Error())
	}
	if s.loading {
		return "Loading…"
	}
	return s.content
}

type dotnetListPkg struct {
	Name      string
	Requested string // empty for transitive packages
	Resolved  string
}

type dotnetListFramework struct {
	Name       string
	TopLevel   []dotnetListPkg
	Transitive []dotnetListPkg
}

type dotnetListProject struct {
	Name       string
	Frameworks []dotnetListFramework
}

func parseDotnetListOutput(raw string) []dotnetListProject {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	var projects []dotnetListProject
	var curProj *dotnetListProject
	var curFW *dotnetListFramework
	inTransitive := false

	for _, line := range lines {
		stripped := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(stripped, "Project '"):
			name := stripped
			if i := strings.Index(stripped, "'"); i >= 0 {
				rest := stripped[i+1:]
				if j := strings.Index(rest, "'"); j >= 0 {
					name = rest[:j]
				}
			}
			projects = append(projects, dotnetListProject{Name: name})
			curProj = &projects[len(projects)-1]
			curFW = nil
			inTransitive = false

		case strings.HasPrefix(stripped, "[") &&
			(strings.HasSuffix(stripped, "]:") || strings.HasSuffix(stripped, "]")):
			if curProj == nil {
				continue
			}
			fw := strings.TrimSuffix(stripped, ":")
			curProj.Frameworks = append(curProj.Frameworks, dotnetListFramework{Name: fw})
			curFW = &curProj.Frameworks[len(curProj.Frameworks)-1]
			inTransitive = false

		case strings.Contains(stripped, "Top-level"):
			inTransitive = false

		case strings.Contains(stripped, "Transitive"):
			inTransitive = true

		case strings.HasPrefix(stripped, ">"):
			if curFW == nil {
				continue
			}
			// Rejoin interval tokens split by whitespace, e.g. "[2.0.3," ")" → "[2.0.3, )"
			fields := rejoinIntervals(strings.Fields(strings.TrimSpace(strings.TrimPrefix(stripped, ">"))))
			if len(fields) == 0 {
				continue
			}
			pkg := dotnetListPkg{Name: fields[0]}
			rest := fields[1:]
			// Skip the "(A)" auto-referenced marker emitted by dotnet list.
			if len(rest) > 0 && rest[0] == "(A)" {
				rest = rest[1:]
			}
			if inTransitive {
				if len(rest) >= 1 {
					pkg.Resolved = rest[0]
				}
				curFW.Transitive = append(curFW.Transitive, pkg)
			} else {
				if len(rest) >= 1 {
					pkg.Requested = rest[0]
				}
				if len(rest) >= 2 {
					pkg.Resolved = rest[1]
				}
				curFW.TopLevel = append(curFW.TopLevel, pkg)
			}
		}
	}
	return projects
}

// rejoinIntervals merges fields that are parts of a split NuGet interval
// notation, e.g. ["[2.0.3,", ")"] → ["[2.0.3, )"].
func rejoinIntervals(fields []string) []string {
	var result []string
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if (strings.HasPrefix(f, "[") || strings.HasPrefix(f, "(")) &&
			!strings.HasSuffix(f, ")") && !strings.HasSuffix(f, "]") {
			for i+1 < len(fields) {
				i++
				f += " " + fields[i]
				if strings.HasSuffix(f, ")") || strings.HasSuffix(f, "]") {
					break
				}
			}
		}
		result = append(result, f)
	}
	return result
}

func (s *depTreeOverlay) renderParsedDotnetList(projects []dotnetListProject) string {
	// Compute max package name width across all frameworks so the version
	// column starts at the same position regardless of name length.
	maxNameW := 20
	for _, proj := range projects {
		for _, fw := range proj.Frameworks {
			for _, pkg := range fw.TopLevel {
				if w := lipgloss.Width(pkg.Name); w > maxNameW {
					maxNameW = w
				}
			}
			for _, pkg := range fw.Transitive {
				if w := lipgloss.Width(pkg.Name); w > maxNameW {
					maxNameW = w
				}
			}
		}
	}
	maxNameW += 2 // breathing room

	var sb strings.Builder
	for pi, proj := range projects {
		if pi > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(styleAccentBold.Render("◈ "+proj.Name) + "\n")
		for _, fw := range proj.Frameworks {
			sb.WriteString("\n" + styleAccentBold.Render(fw.Name) + "\n")
			if len(fw.TopLevel) > 0 {
				sb.WriteString(styleSubtle.Render("  top-level") + "\n")
				for _, pkg := range fw.TopLevel {
					icon, iconStyle := " ", styleMuted
					if row := s.app.rowByName(pkg.Name); row != nil {
						icon, iconStyle = row.statusIcon(), row.statusStyle()
					}
					sb.WriteString("  " + iconStyle.Render(icon) + " ")
					sb.WriteString(styleText.Render(padRight(pkg.Name, maxNameW)))
					// Only show Requested when it is a specific pinned version
					// (not a range like "[2.0.3, )") that differs from Resolved.
					isRange := strings.ContainsAny(pkg.Requested, "[]()")
					showReq := pkg.Requested != "" && !isRange && pkg.Requested != pkg.Resolved
					if showReq {
						sb.WriteString(styleMuted.Render(padRight(pkg.Requested, 14)))
					} else {
						sb.WriteString(strings.Repeat(" ", 14))
					}
					if pkg.Resolved != "" {
						vs := styleMuted
						if showReq {
							vs = styleYellow
						}
						sb.WriteString(vs.Render(pkg.Resolved))
					}
					sb.WriteString("\n")
				}
			}
			if len(fw.Transitive) > 0 {
				sb.WriteString("\n" + styleSubtle.Render("  transitive") + "\n")
				for _, pkg := range fw.Transitive {
					icon, iconStyle := " ", styleMuted
					if row := s.app.rowByName(pkg.Name); row != nil {
						icon, iconStyle = row.statusIcon(), row.statusStyle()
					}
					sb.WriteString("  " + iconStyle.Render(icon) + " ")
					sb.WriteString(styleSubtle.Render(padRight(pkg.Name, maxNameW)))
					if pkg.Resolved != "" {
						sb.WriteString(styleMuted.Render(formatVersionRange(pkg.Resolved)))
					}
					sb.WriteString("\n")
				}
			}
			if len(fw.TopLevel) == 0 && len(fw.Transitive) == 0 {
				sb.WriteString("  " + styleMuted.Render("(no packages)") + "\n")
			}
		}
	}
	return sb.String()
}

func (s *depTreeOverlay) FooterKeys() []kv {
	return []kv{{"↑↓", "scroll"}, {"esc", "close"}}
}

func (s *depTreeOverlay) HandleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd {
	switch msg.String() {
	case "[":
		s.Resize(-4)
		return nil
	case "]":
		s.Resize(4)
		return nil
	case "esc", "q":
		s.closeOverlay()
		return nil
	default:
		var cmd bubble_tea.Cmd
		s.vp, cmd = s.vp.Update(msg)
		return cmd
	}
}

func (s *depTreeOverlay) Render() string {
	overlayW, overlayH := s.app.depTreeOverlaySize()
	innerW := overlayW - 6

	var lines []string
	lines = append(lines,
		styleAccentBold.Render(s.title),
	)
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", innerW)),
	)

	if s.loading {
		lines = append(lines,
			s.app.ctx.Spinner.View()+" "+
				styleSubtle.Render("Loading dependency tree…"),
		)
		// pad to fill viewport height
		vpH := overlayH - 8
		for i := 1; i < vpH; i++ {
			lines = append(lines, "")
		}
	} else {
		lines = append(lines, s.vp.View())
	}

	box := styleOverlay.
		Width(overlayW).
		Render(strings.Join(lines, "\n"))

	return s.centerOverlay(box)
}
