package main

import (
	"fmt"
	"strings"
	"time"

	bubble_tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func timeAgo(t time.Time) string {
	// NuGet uses 1900-01-01 as a sentinel "no publish date" value.
	// Treat anything before 2005 as unknown rather than showing nonsense like "126 years ago".
	if t.IsZero() || t.Year() < 2005 {
		return ""
	}
	d := time.Since(t)
	days := int(d.Hours() / 24)
	if days < 1 {
		return "today"
	}
	months := days / 30
	years := days / 365
	if years > 0 {
		if years == 1 {
			return "1 year ago"
		}
		return fmt.Sprintf("%d years ago", years)
	}
	if months > 0 {
		if months == 1 {
			return "1 month ago"
		}
		return fmt.Sprintf("%d months ago", months)
	}
	if days == 1 {
		return "1 day ago"
	}
	return fmt.Sprintf("%d days ago", days)
}

// sectionBase holds width configuration and resize state for any TUI section
// (panel or overlay). Centralizes the resize logic that was previously
// scattered across every section file.
type sectionBase struct {
	app *App // back-reference, set once at creation

	// Config (set once at creation)
	baseWidth int // fixed base width in columns; 0 means use basePct
	basePct   int // base as % of availW (e.g. 85 = 85%); 0 means use baseWidth
	minWidth  int // hard floor
	maxMargin int // subtracted from availW for max (e.g. 4 → max = availW-4)

	// State
	active       bool // overlays: whether this overlay is currently shown
	widthOffset  int  // mutated by [ / ] resize
	mouseRegions []mouseRegion
}

// Section is satisfied by any type that embeds sectionBase.
type Section interface {
	Width() int
	Resize(delta int) bool
	ResetOffset()
}

// Overlay is implemented by all overlay sections (search, picker, confirms, etc.).
type Overlay interface {
	IsActive() bool
	HandleKey(msg bubble_tea.KeyMsg) bubble_tea.Cmd
	Render() string
	FooterKeys() []kv
}

// kv is a key-value pair used for footer keybinding display.
type kv struct{ k, v string }

// Base returns the effective base width given available width.
func (s *sectionBase) Base() int {
	if s.basePct > 0 {
		return s.app.ctx.Width * s.basePct / 100
	}
	return s.baseWidth
}

// Width returns the clamped outer width for this section.
func (s *sectionBase) Width() int {
	return clampW(s.Base()+s.widthOffset, s.minWidth, s.app.ctx.Width-s.maxMargin)
}

// Resize adjusts widthOffset by delta, respecting bounds. Returns true if changed.
func (s *sectionBase) Resize(delta int) bool {
	base := s.Base()
	maxW := s.app.ctx.Width - s.maxMargin
	proposed := base + s.widthOffset + delta
	if proposed < s.minWidth || proposed > maxW {
		return false
	}
	s.widthOffset += delta
	return true
}

// IsActive returns whether this section's overlay is currently shown.
func (s *sectionBase) IsActive() bool { return s.active }

// ResetOffset zeroes the resize offset.
func (s *sectionBase) ResetOffset() {
	s.widthOffset = 0
}

// closeOverlay resets the overlay to its default closed state.
func (s *sectionBase) closeOverlay() {
	s.ResetOffset()
	s.active = false
	s.mouseRegions = nil
	s.app.ctx.StatusLine = ""
}

// centerOverlay wraps a rendered box in lipgloss.Place centered in the overlay area.
func (s *sectionBase) centerOverlay(box string) string {
	s.mouseRegions = nil
	return lipgloss.Place(s.app.ctx.Width, s.app.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}

const (
	overlayContentX = 3 // border (1) + horizontal padding (2)
	overlayContentY = 2 // border (1) + vertical padding (1)
)

// centerOverlayInteractive translates box-relative regions into terminal
// coordinates using the same centering rule as lipgloss.Place.
func (s *sectionBase) centerOverlayInteractive(box string, regions []mouseRegion) string {
	boxW := lipgloss.Width(box)
	boxH := lipgloss.Height(box)
	left := imax(0, (s.app.ctx.Width-boxW)/2)
	top := imax(0, (s.app.overlayHeight()-boxH)/2)

	s.mouseRegions = make([]mouseRegion, len(regions))
	copy(s.mouseRegions, regions)
	for i := range s.mouseRegions {
		s.mouseRegions[i].rect.x += left
		s.mouseRegions[i].rect.y += top
	}
	return lipgloss.Place(s.app.ctx.Width, s.app.overlayHeight(), lipgloss.Center, lipgloss.Center, box)
}

func (s *sectionBase) MouseRegions() []mouseRegion {
	regions := make([]mouseRegion, len(s.mouseRegions))
	copy(regions, s.mouseRegions)
	return regions
}

func overlayTitleLine(title string, innerW int) string {
	const closeLabel = "[×]"
	if innerW <= lipgloss.Width(closeLabel) {
		return styleSubtleBold.Render(closeLabel)
	}
	title = truncate(title, innerW-lipgloss.Width(closeLabel)-1)
	gap := innerW - lipgloss.Width(title) - lipgloss.Width(closeLabel)
	return styleAccentBold.Render(title) + strings.Repeat(" ", gap) + styleSubtleBold.Render(closeLabel)
}

func overlayCloseRegion(outerW int, handle func(bubble_tea.KeyMsg) bubble_tea.Cmd) mouseRegion {
	return mouseRegion{
		rect:  mouseRect{x: outerW - 6, y: overlayContentY, w: 3, h: 1},
		click: keyMouseHandler(handle, bubble_tea.KeyEscape),
	}
}

type overlayButton struct {
	label string
	code  rune
}

func renderOverlayButtons(innerW, contentLine int, handle func(bubble_tea.KeyMsg) bubble_tea.Cmd, buttons ...overlayButton) (string, []mouseRegion) {
	plain := make([]string, len(buttons))
	totalW := 0
	for i, button := range buttons {
		plain[i] = "[ " + button.label + " ]"
		totalW += lipgloss.Width(plain[i])
		if i > 0 {
			totalW += 2
		}
	}
	left := imax(0, (innerW-totalW)/2)
	var line strings.Builder
	line.WriteString(strings.Repeat(" ", left))
	regions := make([]mouseRegion, 0, len(buttons))
	x := left
	for i, button := range buttons {
		if i > 0 {
			line.WriteString("  ")
			x += 2
		}
		label := plain[i]
		line.WriteString(styleAccentBold.Render(label))
		width := lipgloss.Width(label)
		regions = append(regions, mouseRegion{
			rect: mouseRect{
				x: overlayContentX + x,
				y: overlayContentY + contentLine,
				w: width,
				h: 1,
			},
			click: keyMouseHandler(handle, button.code),
		})
		x += width
	}
	return line.String(), regions
}

func clampW(w, minW, maxW int) int {
	if w < minW {
		w = minW
	}
	if w > maxW {
		w = maxW
	}
	if w < 1 {
		w = 1
	}
	return w
}

// adjustOffset adds delta to *offset only if the effective width
// (base + *offset + delta) stays within [minW, maxW].
func adjustOffset(offset *int, delta, base, minW, maxW int) {
	proposed := base + *offset + delta
	if proposed < minW || proposed > maxW {
		return
	}
	*offset += delta
}

func padRight(s string, width int) string {
	visible := lipgloss.Width(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}

func truncate(s string, n int) string {
	if n <= 3 {
		return ansi.Truncate(s, n, "")
	}
	return ansi.Truncate(s, n, "...")
}

func truncateStyled(s string, n int) string {
	return ansi.Truncate(s, n, "")
}

// hyperlinkEnabled controls whether OSC 8 escape codes are emitted.
var hyperlinkEnabled = true

// hyperlink wraps text in an OSC 8 terminal hyperlink.
// Unsupported terminals silently ignore the escape codes.
func hyperlink(url, text string) string {
	if !hyperlinkEnabled || url == "" {
		return text
	}
	return hyperlinkBare(url, text) + " ↗"
}

func hyperlinkBare(url, text string) string {
	if !hyperlinkEnabled || url == "" {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// clampListScroll adjusts *scroll so that cursor is visible within a viewport
// of the given height. extraPad adds bottom padding when cursor is at the end.
func clampListScroll(cursor int, scroll *int, visible, total, extraPad int) {
	if cursor < *scroll {
		*scroll = cursor
	}
	pad := 0
	if extraPad > 0 && cursor == total-1 {
		pad = extraPad
	}
	if cursor+pad >= *scroll+visible {
		*scroll = cursor + pad - visible + 1
	}
}

func imax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// renderToPanel finalizes pre-rendered content into a panel at exact outer
// dimensions. Content lines are truncated or padded vertically to fit exactly
// within the style's content area (outerH − vertical frame). This runs BEFORE
// lipgloss Render so that alignTextVertical (which does not truncate) receives
// content that already fits, producing deterministic panel heights.
func renderToPanel(s lipgloss.Style, outerW, outerH int, content string) string {
	contentH := outerH - s.GetVerticalFrameSize()
	if contentH < 1 {
		contentH = 1
	}
	lines := strings.Split(content, "\n")
	if len(lines) > contentH {
		lines = lines[:contentH]
	}
	for len(lines) < contentH {
		lines = append(lines, "")
	}
	return s.Width(outerW).Height(outerH).Render(strings.Join(lines, "\n"))
}
