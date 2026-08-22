package main

import bubble_tea "charm.land/bubbletea/v2"

// mouseRect is a zero-based terminal-cell rectangle. The same layout values
// that size the rendered panels produce these rectangles, keeping pointer hit
// tests aligned with the view the user saw.
type mouseRect struct {
	x, y int
	w, h int
}

func (r mouseRect) contains(x, y int) bool {
	return r.w > 0 && r.h > 0 &&
		x >= r.x && x < r.x+r.w &&
		y >= r.y && y < r.y+r.h
}

type mouseHandler func(event bubble_tea.MouseMsg) bubble_tea.Cmd

// mouseRegion describes one explicit interaction in the rendered frame.
// Regions are checked in order, so links and buttons can take precedence over
// broader row and panel targets.
type mouseRegion struct {
	rect  mouseRect
	click mouseHandler
	wheel mouseHandler
}

// mouseLayout is an immutable snapshot captured by View. Bubble Tea invokes
// OnMouse for that rendered view, so resize or overlay changes cannot make an
// event use geometry from a different frame.
type mouseLayout struct {
	modal bool

	projects mouseRect
	packages mouseRect
	detail   mouseRect
	log      mouseRect

	projectScroll int
	packageScroll int
	regions       []mouseRegion
}

type mouseInputMsg struct {
	event  bubble_tea.MouseMsg
	layout mouseLayout
}

func (m *App) bindMouse(v *bubble_tea.View, layout mouseLayout) {
	if !m.mouse {
		return
	}
	// OSC 8 links are discovered from the final rendered frame, after panel
	// joins, viewport scrolling, centering, and safety trimming. This keeps link
	// hit targets aligned without teaching every renderer about absolute cells.
	layout.regions = append(linkMouseRegions(v.Content), layout.regions...)
	v.MouseMode = bubble_tea.MouseModeCellMotion
	v.OnMouse = func(event bubble_tea.MouseMsg) bubble_tea.Cmd {
		input := mouseInputMsg{event: event, layout: layout}
		return func() bubble_tea.Msg { return input }
	}
}

func (m *App) mainMouseLayout(leftW, midW, rightW int) mouseLayout {
	bodyH := m.bodyOuterHeight()
	layout := mouseLayout{
		projects:      mouseRect{x: 0, y: 0, w: leftW, h: bodyH},
		packages:      mouseRect{x: leftW, y: 0, w: midW, h: bodyH},
		detail:        mouseRect{x: leftW + midW, y: 0, w: rightW, h: bodyH},
		projectScroll: m.projects.scroll,
		packageScroll: m.packages.scroll,
	}
	if m.ctx.ShowLogs {
		layout.log = mouseRect{x: 0, y: bodyH, w: m.layoutWidth(), h: logPanelOuterHeight}
	}

	columns := m.packageColumns(midW)
	headerX := leftW + 2         // panel border + horizontal padding
	panelEnd := leftW + midW - 1 // exclude the right border
	packageHeaderW := 2 + columns.nameW
	_, directionOffset := packageSortHeader(m.packages.sortMode, m.packages.sortDir)
	directionX := headerX + 2 + directionOffset // two leading cells before "Package"
	if directionX < headerX+packageHeaderW && directionX < panelEnd {
		layout.regions = append(layout.regions, mouseRegion{
			rect: mouseRect{x: directionX, y: 1, w: 1, h: 1},
			click: func(bubble_tea.MouseMsg) bubble_tea.Cmd {
				m.focus = focusPackages
				m.reversePackageSort()
				return nil
			},
		})
	}
	visiblePackageW := packageHeaderW
	if headerX+visiblePackageW > panelEnd {
		visiblePackageW = panelEnd - headerX
	}
	if visiblePackageW > 0 {
		layout.regions = append(layout.regions, mouseRegion{
			rect: mouseRect{x: headerX, y: 1, w: visiblePackageW, h: 1},
			click: func(bubble_tea.MouseMsg) bubble_tea.Cmd {
				m.focus = focusPackages
				m.cyclePackageSort()
				return nil
			},
		})
	}
	headerX += packageHeaderW

	addSortRegion := func(width int, mode packageSortMode) {
		visibleW := width
		if headerX+visibleW > panelEnd {
			visibleW = panelEnd - headerX
		}
		if visibleW > 0 {
			layout.regions = append(layout.regions, mouseRegion{
				rect: mouseRect{x: headerX, y: 1, w: visibleW, h: 1},
				click: func(bubble_tea.MouseMsg) bubble_tea.Cmd {
					m.focus = focusPackages
					m.setPackageSort(mode)
					return nil
				},
			})
		}
		headerX += width
	}
	addSortRegion(columns.currentW, sortByCurrent)
	if columns.showAvail {
		addSortRegion(columns.availableW, sortByAvailable)
	}
	if columns.showSource {
		addSortRegion(columns.sourceW, sortBySource)
	}
	return layout
}

func (l mouseLayout) panelAt(x, y int) (focusPanel, bool) {
	switch {
	case l.projects.contains(x, y):
		return focusProjects, true
	case l.packages.contains(x, y):
		return focusPackages, true
	case l.detail.contains(x, y):
		return focusDetail, true
	case l.log.contains(x, y):
		return focusLog, true
	default:
		return 0, false
	}
}

func (l mouseLayout) projectIndexAt(x, y, total int) (int, bool) {
	if !l.projects.contains(x, y) {
		return 0, false
	}
	// Border, title, and divider occupy rows 0, 1, and 2. Project items
	// then occupy three rows each: title, description, and spacing.
	localY := y - l.projects.y
	if localY < 3 || localY >= l.projects.h-1 {
		return 0, false
	}
	index := l.projectScroll + (localY-3)/3
	return index, index >= 0 && index < total
}

func (l mouseLayout) packageIndexAt(x, y, total int) (int, bool) {
	if !l.packages.contains(x, y) {
		return 0, false
	}
	// Border, header, and divider occupy rows 0, 1, and 2. Every package
	// after that is one terminal row.
	localY := y - l.packages.y
	if localY < 3 || localY >= l.packages.h-1 {
		return 0, false
	}
	index := l.packageScroll + localY - 3
	return index, index >= 0 && index < total
}

func (m *App) handleMouse(input mouseInputMsg) bubble_tea.Cmd {
	for _, region := range input.layout.regions {
		event := input.event.Mouse()
		if !region.rect.contains(event.X, event.Y) {
			continue
		}
		switch input.event.(type) {
		case bubble_tea.MouseClickMsg:
			if event.Button == bubble_tea.MouseLeft && region.click != nil {
				return region.click(input.event)
			}
		case bubble_tea.MouseWheelMsg:
			if region.wheel != nil {
				return region.wheel(input.event)
			}
		}
	}

	// Modal overlays own input. Main-screen clicks must never pass through an
	// overlay and trigger a package or project selection underneath it.
	if input.layout.modal {
		return nil
	}

	event := input.event.Mouse()
	target, ok := input.layout.panelAt(event.X, event.Y)
	if !ok {
		return nil
	}

	switch input.event.(type) {
	case bubble_tea.MouseClickMsg:
		if event.Button != bubble_tea.MouseLeft {
			return nil
		}
		m.focus = target
		switch target {
		case focusProjects:
			if index, ok := input.layout.projectIndexAt(event.X, event.Y, len(m.projects.items)); ok {
				m.selectProjectIndex(index)
			}
		case focusPackages:
			if index, ok := input.layout.packageIndexAt(event.X, event.Y, len(m.packages.rows)); ok {
				m.selectPackageIndex(index)
			}
		}

	case bubble_tea.MouseWheelMsg:
		switch target {
		case focusProjects:
			switch event.Button {
			case bubble_tea.MouseWheelUp:
				m.moveProjectCursor(-1)
			case bubble_tea.MouseWheelDown:
				m.moveProjectCursor(1)
			}
		case focusPackages:
			switch event.Button {
			case bubble_tea.MouseWheelUp:
				m.movePackageCursor(-3)
			case bubble_tea.MouseWheelDown:
				m.movePackageCursor(3)
			}
		case focusDetail:
			var cmd bubble_tea.Cmd
			m.detail.vp, cmd = m.detail.vp.Update(input.event)
			return cmd
		case focusLog:
			if m.ctx.ShowLogs {
				var cmd bubble_tea.Cmd
				m.log.vp, cmd = m.log.vp.Update(input.event)
				return cmd
			}
		}
	}
	return nil
}

func keyMouseHandler(handle func(bubble_tea.KeyMsg) bubble_tea.Cmd, code rune) mouseHandler {
	return func(bubble_tea.MouseMsg) bubble_tea.Cmd {
		return handle(bubble_tea.KeyPressMsg(bubble_tea.Key{Code: code}))
	}
}

func verticalWheelHandler(handle func(bubble_tea.KeyMsg) bubble_tea.Cmd) mouseHandler {
	return func(event bubble_tea.MouseMsg) bubble_tea.Cmd {
		var code rune
		switch event.Mouse().Button {
		case bubble_tea.MouseWheelUp:
			code = bubble_tea.KeyUp
		case bubble_tea.MouseWheelDown:
			code = bubble_tea.KeyDown
		default:
			return nil
		}
		return handle(bubble_tea.KeyPressMsg(bubble_tea.Key{Code: code}))
	}
}

func (m *App) selectProjectIndex(index int) {
	if index < 0 || index >= len(m.projects.items) {
		return
	}
	changed := index != m.projects.cursor
	m.projects.cursor = index
	m.clampProjectOffset()
	if !changed {
		return
	}
	m.packages.cursor = 0
	m.packages.scroll = 0
	m.rebuildPackageRows()
	m.refreshDetail()
}

func (m *App) moveProjectCursor(delta int) {
	if len(m.projects.items) == 0 {
		return
	}
	index := m.projects.cursor + delta
	if index < 0 {
		index = 0
	}
	if index >= len(m.projects.items) {
		index = len(m.projects.items) - 1
	}
	m.selectProjectIndex(index)
}

func (m *App) selectPackageIndex(index int) {
	if index < 0 || index >= len(m.packages.rows) {
		return
	}
	changed := index != m.packages.cursor
	m.packages.cursor = index
	m.clampOffset()
	if changed {
		m.refreshDetail()
	}
}

func (m *App) movePackageCursor(delta int) {
	if len(m.packages.rows) == 0 {
		return
	}
	index := m.packages.cursor + delta
	if index < 0 {
		index = 0
	}
	if index >= len(m.packages.rows) {
		index = len(m.packages.rows) - 1
	}
	m.selectPackageIndex(index)
}
