package main

import (
	"fmt"

	"github.com/nulifyer/guget/internal/ui"
)

// Dialogs keep their state, styles, and key behavior beside their implementation.
// The host controls placement and lifecycle without knowing their domain data.
func (p *depTreeOverlay) Draw(ui.Rect) string      { return p.Render() }
func (p *releaseNotesOverlay) Draw(ui.Rect) string { return p.Render() }
func (p *sourcesOverlay) Draw(ui.Rect) string      { return p.Render() }
func (p *helpOverlay) Draw(ui.Rect) string         { return p.Render() }
func (p *packageSearch) Draw(ui.Rect) string       { return p.Render() }
func (p *versionPicker) Draw(ui.Rect) string       { return p.Render() }
func (p *locationPicker) Draw(ui.Rect) string      { return p.Render() }
func (p *projectPicker) Draw(ui.Rect) string       { return p.Render() }
func (p *confirmRemove) Draw(ui.Rect) string       { return p.Render() }
func (p *confirmUpdate) Draw(ui.Rect) string       { return p.Render() }
func (p *packageSearch) Mounted()                  { p.input.Focus() }

// Cancellation belongs to closeOverlay and App.Disconnect. The host may be
// unmounting an older session after the state has been replaced for a new one.
func (p *packageSearch) Unmounted() { p.input.Blur() }

func (m *App) activeOverlay() Overlay {
	for _, o := range m.overlays() {
		if o.IsActive() {
			return o
		}
	}
	return nil
}
func (m *App) componentNodes(footer string, footerH int) []ui.Node {
	w, h := m.ctx.Width, m.ctx.Height
	bodyH := max(0, h-footerH)
	var nodes []ui.Node
	if o := m.activeOverlay(); o != nil {
		// Request identity distinguishes two openings of the same dialog.
		nodes = append(nodes, ui.Node{Key: fmt.Sprintf("dialog:%T:%d", o, o.InstanceID()), Element: o, Bounds: ui.Rect{W: w, H: bodyH}, Focused: true})
	} else if m.compactLogs() {
		nodes = append(nodes, ui.Node{Key: "guget-log", Element: &m.log, Focused: m.focus == focusLog, Bounds: ui.Rect{W: w, H: bodyH}})
	} else {
		left, mid, right := m.panelWidths()
		panelH := min(bodyH, m.bodyOuterHeight())
		nodes = append(nodes,
			ui.Node{Key: "guget-projects", Element: &m.projects, Focused: m.focus == focusProjects, Bounds: ui.Rect{W: left, H: panelH}},
			ui.Node{Key: "guget-packages", Element: &m.packages, Focused: m.focus == focusPackages, Bounds: ui.Rect{X: left, W: mid, H: panelH}},
			ui.Node{Key: "guget-detail", Element: &m.detail, Focused: m.focus == focusDetail, Bounds: ui.Rect{X: left + mid, W: right, H: panelH}},
		)
		if m.ctx.ShowLogs {
			nodes = append(nodes, ui.Node{Key: "guget-log", Element: &m.log, Focused: m.focus == focusLog, Bounds: ui.Rect{Y: panelH, W: w, H: max(0, bodyH-panelH)}})
		}
	}
	nodes = append(nodes, ui.Node{Key: "guget-status", Element: ui.Static(footer), Bounds: ui.Rect{Y: bodyH, W: w, H: footerH}})
	return nodes
}
