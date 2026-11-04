package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	ui "github.com/nulifyer/guget/internal/ui"
	style "github.com/nulifyer/guget/internal/ui/style"
	viewport "github.com/nulifyer/guget/internal/ui/viewport"
)

func runDepTreeCmd(ctx context.Context, project *ParsedProject, requestID int) ui.Effect {
	return func() ui.Event {
		cmd := exec.CommandContext(ctx, "dotnet", "list", project.FilePath, "package", "--include-transitive")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return depTreeReadyMsg{requestID: requestID, err: fmt.Errorf("dotnet list: %w\n%s", err, strings.TrimSpace(string(out)))}
		}
		return depTreeReadyMsg{requestID: requestID, content: string(out)}
	}
}

func newDepTreeOverlay(m *App, title string, loading bool) depTreeOverlay {
	m.depTree.closeOverlay()
	parent := m.lifecycle
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	dt := depTreeOverlay{
		ctx: ctx, cancel: cancel,
		sectionBase: sectionBase{app: m, basePct: 80, minWidth: 40, maxMargin: 4, active: true, requestID: m.nextRequestID()},
		title:       title,
		loading:     loading,
	}
	m.depTree = dt // assign so depTreeOverlaySize() reads the correct Width()
	overlayW, overlayH := m.depTreeOverlaySize()
	dt.vp = viewport.New(viewport.WithWidth(overlayW-6), viewport.WithHeight(max(1, overlayH-2)))
	return dt
}

func (s *depTreeOverlay) closeOverlay() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.app != nil {
		s.sectionBase.closeOverlay()
	}
}

func (m *App) openDepTree() ui.Effect {
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
	dt.packageVersion = installedVer
	dt.content = dt.formatDepGroups(installedVer)
	dt.vp.SetContent(dt.content)
	m.depTree = dt
	return nil
}

func (m *App) openTransitiveDepTree() ui.Effect {
	proj := m.selectedProject()
	if proj == nil {
		return m.setStatus("▲ Select a project first", true)
	}
	m.ctx.StatusLine = ""
	m.depTree = newDepTreeOverlay(m, proj.FileName+" (transitive packages)", true)
	return runDepTreeCmd(m.depTree.ctx, proj, m.depTree.requestID)
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
			if w := style.Width(dep.ID); w > maxNameW {
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
				rangeW := min(style.Width(rangeStr), max(1, s.vp.Width()/2))
				nameW := max(1, min(maxNameW, s.vp.Width()-4-rangeW))
				sb.WriteString(styleText.Render(padRight(truncate(dep.ID, max(1, nameW-1)), nameW)) +
					styleSubtle.Render(truncate(rangeStr, rangeW)) + "\n")
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func (s *depTreeOverlay) refreshContent() {
	if s.packageVersion != nil {
		s.content = s.formatDepGroups(s.packageVersion)
	} else if len(s.dotnetProjects) > 0 {
		s.content = s.renderParsedDotnetList(s.dotnetProjects)
	}
	s.vp.SetContent(s.buildContent())
}

func (s *depTreeOverlay) buildContent() string {
	if s.err != nil {
		return styleRed.Render("Error: " + s.err.Error())
	}
	if s.loading {
		return "Loading..."
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
	maxNameW, maxResolvedW, maxRequestedW := 20, 10, 0
	for _, proj := range projects {
		for _, fw := range proj.Frameworks {
			for _, group := range [][]dotnetListPkg{fw.TopLevel, fw.Transitive} {
				for _, pkg := range group {
					maxNameW = max(maxNameW, style.Width(pkg.Name)+2)
					maxResolvedW = max(maxResolvedW, style.Width(pkg.Resolved))
					if pkg.Requested != "" && !strings.ContainsAny(pkg.Requested, "[]()") && pkg.Requested != pkg.Resolved {
						maxRequestedW = max(maxRequestedW, style.Width(pkg.Requested)+2)
					}
				}
			}
		}
	}
	versionBudget := max(1, (s.vp.Width()-4)/3)
	resolvedW := min(maxResolvedW, versionBudget)
	requestedW := min(maxRequestedW, versionBudget)
	nameW := max(1, min(maxNameW, s.vp.Width()-4-resolvedW-requestedW))

	var sb strings.Builder
	for pi, proj := range projects {
		if pi > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(styleAccentBold.Render(truncate("◈ "+proj.Name, s.vp.Width())) + "\n")
		for _, fw := range proj.Frameworks {
			sb.WriteString("\n" + styleAccentBold.Render(truncate(fw.Name, s.vp.Width())) + "\n")
			if len(fw.TopLevel) > 0 {
				sb.WriteString(styleSubtle.Render("  top-level") + "\n")
				for _, pkg := range fw.TopLevel {
					icon, iconStyle := " ", styleMuted
					if row := s.app.rowByName(pkg.Name); row != nil {
						icon, iconStyle = row.statusIcon(), row.statusStyle()
					}
					sb.WriteString("  " + iconStyle.Render(icon) + " ")
					sb.WriteString(styleText.Render(padRight(truncate(pkg.Name, max(1, nameW-1)), nameW)))
					// Only show Requested when it is a specific pinned version
					// (not a range like "[2.0.3, )") that differs from Resolved.
					isRange := strings.ContainsAny(pkg.Requested, "[]()")
					showReq := pkg.Requested != "" && !isRange && pkg.Requested != pkg.Resolved
					if showReq {
						sb.WriteString(styleSubtle.Render(padRight(truncate(pkg.Requested, max(1, requestedW-1)), requestedW)))
					} else {
						sb.WriteString(strings.Repeat(" ", requestedW))
					}
					if pkg.Resolved != "" {
						vs := styleSubtle
						if showReq {
							vs = styleYellow
						}
						sb.WriteString(vs.Render(truncate(pkg.Resolved, resolvedW)))
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
					sb.WriteString(styleSubtle.Render(padRight(truncate(pkg.Name, max(1, nameW-1)), nameW)))
					if pkg.Resolved != "" {
						sb.WriteString(strings.Repeat(" ", requestedW) + styleSubtle.Render(truncate(formatVersionRange(pkg.Resolved), resolvedW)))
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

func (s *depTreeOverlay) HandleKey(msg ui.KeyEvent) ui.Effect {
	switch msg.String() {
	case "[":
		if s.Resize(-4) {
			s.app.relayout()
			s.refreshContent()
		}
		return nil
	case "]":
		if s.Resize(4) {
			s.app.relayout()
			s.refreshContent()
		}
		return nil
	case "esc", "q":
		s.closeOverlay()
		return nil
	default:
		var cmd ui.Effect
		s.vp, cmd = s.vp.Update(msg)
		return cmd
	}
}

func (s *depTreeOverlay) Render() string {
	overlayW, overlayH := s.app.depTreeOverlaySize()
	innerW := overlayW - 6

	var lines []string
	lines = append(lines,
		overlayTitleLine(s.title, innerW),
	)
	lines = append(lines,
		styleBorder.Render(strings.Repeat("─", innerW)),
	)

	if s.loading {
		lines = append(lines,
			s.app.ctx.Spinner.View()+" "+
				styleSubtle.Render("Loading dependency tree..."),
		)
		// pad to fill viewport height
		vpH := max(1, overlayH-2)
		for i := 1; i < vpH; i++ {
			lines = append(lines, "")
		}
	} else {
		lines = append(lines, s.vp.View())
	}

	box := styleOverlay.
		Width(overlayW).
		Render(strings.Join(lines, "\n"))
	regions := []mouseRegion{
		overlayCloseRegion(overlayW, s.HandleKey),
		{
			rect:  mouseRect{x: 1, y: 1, w: overlayW - 2, h: style.Height(box) - 2},
			wheel: verticalWheelHandler(s.HandleKey),
		},
	}

	return s.centerOverlayInteractive(box, regions)
}
