package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nulifyer/guget/internal/ui"
	text "github.com/nulifyer/guget/internal/ui/text"
	"github.com/nulifyer/guget/internal/ui/viewport"
)

// materializedLog is the log view as it was built before the panel drew a
// window: every entry wrapped, styled, and joined into one viewport content.
// The tests hold the windowed view to it.
func materializedLog(lines []string, width int) string {
	var colored []string
	for _, line := range lines {
		lineStyle := logLineStyle(line)
		for _, piece := range wrapLogLine(line, width) {
			colored = append(colored, lineStyle.Render(piece))
		}
	}
	return strings.Join(colored, "\n")
}

func testLogLine(i int) string {
	switch i % 9 {
	case 0:
		return fmt.Sprintf("[INFO] entry %d", i)
	case 1:
		return fmt.Sprintf("[WARN] Save failed: entry %d %s", i, strings.Repeat("a long refusal with /a/deep/path/to/Directory.Packages.props ", 5))
	case 2:
		return fmt.Sprintf("[DEBUG] entry %d %s", i, strings.Repeat("unbroken-", 40))
	case 3:
		return fmt.Sprintf("[ERROR] entry %d – ▲ Wörld 日本語のパッケージ %s", i, strings.Repeat("ünïcödé text ", 20))
	case 4:
		return fmt.Sprintf("[TRACE] entry %d\tcolumn\tvalue", i)
	case 5:
		return fmt.Sprintf("[INFO] entry %d\nsecond row\r\nthird row", i)
	case 6:
		return fmt.Sprintf("restore output %d", i)
	default:
		return fmt.Sprintf("[INFO] GET https://api.nuget.org/v3/registration5-gz-semver2/package%d/index.json 200", i)
	}
}

func plainRows(content string) []string {
	rows := strings.Split(text.Strip(content), "\n")
	for i, row := range rows {
		rows[i] = strings.TrimRight(row, " ")
	}
	return rows
}

func newLogTestApp(t testing.TB, width, height int) *App {
	t.Helper()
	m := &App{ctx: &AppContext{ShowLogs: true}}
	m.log = logPanel{vp: viewport.New(viewport.WithWidth(width), viewport.WithHeight(height))}
	m.log.app = m
	return m
}

func TestLogWindowMatchesTheMaterializedView(t *testing.T) {
	for _, width := range []int{20, 76, 136} {
		const height = 7
		m := newLogTestApp(t, width, height)
		m.updateLogView()
		reference := viewport.New(viewport.WithWidth(width), viewport.WithHeight(height))
		check := func(step string) {
			t.Helper()
			reference.SetContent(materializedLog(m.ctx.LogLines, width))
			if len(m.ctx.LogLines) > 0 && m.log.vp.TotalLineCount() != reference.TotalLineCount() {
				t.Fatalf("width %d, %s: %d rows, want %d", width, step, m.log.vp.TotalLineCount(), reference.TotalLineCount())
			}
			for _, offset := range []int{0, 1, 5, reference.TotalLineCount() / 3, reference.TotalLineCount() / 2, reference.TotalLineCount() - height - 1, reference.TotalLineCount()} {
				m.log.vp.SetYOffset(offset)
				reference.SetYOffset(offset)
				if m.log.vp.YOffset() != reference.YOffset() || m.log.vp.AtBottom() != reference.AtBottom() {
					t.Fatalf("width %d, %s, offset %d: scroll state differs", width, step, offset)
				}
				got, want := plainRows(m.log.vp.View()), plainRows(reference.View())
				if strings.Join(got, "\n") != strings.Join(want, "\n") {
					t.Fatalf("width %d, %s, offset %d:\n got %q\nwant %q", width, step, offset, got, want)
				}
			}
		}
		// Fill past the retention limit in uneven batches so entries are
		// evicted while the row counts are kept incrementally.
		for next := 0; next < maxLogLines+700; {
			batch := 1 + next%613
			lines := make([]string, batch)
			for i := range lines {
				lines[i] = testLogLine(next + i)
			}
			next += batch
			m.HandleEvent(logBatchMsg{lines: lines})
			m.HandleEvent(logRefreshMsg{})
			check(fmt.Sprintf("after %d entries", next))
		}
		if len(m.ctx.LogLines) != maxLogLines {
			t.Fatalf("retained %d entries", len(m.ctx.LogLines))
		}
		// A single-row entry keeps its exact styling, not only its text.
		m.ctx.LogLines = []string{"[WARN] short"}
		m.updateLogView()
		if got, want := strings.Split(m.log.vp.View(), "\n")[0], styleYellow.Render("[WARN] short"); !strings.HasPrefix(got, want) {
			t.Fatalf("styled row %q, want prefix %q", got, want)
		}
	}
}

func TestLogRefreshFollowsTheTailOnlyWhenTheReaderIsThere(t *testing.T) {
	m := newLogTestApp(t, 60, 5)
	m.updateLogView()
	for i := range 40 {
		m.HandleEvent(logBatchMsg{lines: []string{testLogLine(i)}})
	}
	m.HandleEvent(logRefreshMsg{})
	if !m.log.vp.AtBottom() {
		t.Fatal("log did not follow new entries")
	}
	m.log.vp.SetYOffset(3)
	before := m.log.vp.View()
	m.HandleEvent(logBatchMsg{lines: []string{"[INFO] later"}})
	m.HandleEvent(logRefreshMsg{})
	if m.log.vp.YOffset() != 3 || m.log.vp.View() != before {
		t.Fatal("a new entry moved a reader who had scrolled up")
	}
	// Hidden logs keep no row counts; reopening recounts them.
	m.ctx.ShowLogs = false
	m.HandleEvent(logBatchMsg{lines: []string{"[INFO] while hidden"}})
	if m.log.heights != nil {
		t.Fatal("hidden log kept row counts")
	}
	m.ctx.ShowLogs = true
	m.updateLogView()
	m.log.vp.GotoBottom()
	if rows := plainRows(m.log.vp.View()); rows[len(rows)-1] != "[INFO] while hidden" {
		t.Fatalf("reopened log ends with %q", rows[len(rows)-1])
	}
}

// logRefreshAllocs measures one log update as the event loop performs it: a
// new entry arrives, the view refreshes, and the panel draws.
func logRefreshAllocs(t *testing.T, entries int) float64 {
	t.Helper()
	m := newLogTestApp(t, 136, logPanelLines)
	for i := range entries {
		m.ctx.LogLines = append(m.ctx.LogLines, testLogLine(i))
	}
	m.updateLogView()
	entry := testLogLine(1)
	return testing.AllocsPerRun(50, func() {
		m.appendLog(entry)
		m.refreshLogView()
		_ = m.log.vp.View()
	})
}

// A log update must cost what the panel shows, not what the buffer holds:
// the work is the same for a small buffer and a full one.
func TestLogRefreshCostDoesNotGrowWithTheBuffer(t *testing.T) {
	small, full := logRefreshAllocs(t, 50), logRefreshAllocs(t, maxLogLines)
	if full > small+2 {
		t.Fatalf("a refresh allocates %.0f times with a full buffer and %.0f with 50 entries", full, small)
	}

	// Count the entries a refresh and a draw wrap: the new entry and the
	// visible ones, never the buffer.
	m := newLogTestApp(t, 136, logPanelLines)
	for i := range maxLogLines {
		m.ctx.LogLines = append(m.ctx.LogLines, testLogLine(i))
	}
	m.updateLogView()
	m.appendLog("[INFO] one more")
	m.refreshLogView()
	rows := m.log.vp.TotalLineCount()
	visited := 0
	original := m.log.vp
	m.log.vp.SetWindow(rows, func(start, n int) []string {
		visited += n
		return m.log.window(start, n)
	})
	m.log.vp.GotoBottom()
	_ = m.log.vp.View()
	m.log.vp = original
	if visited != logPanelLines {
		t.Fatalf("a draw asked for %d rows of %d; the panel shows %d", visited, rows, logPanelLines)
	}
}

func fullLogApp(b *testing.B) *App {
	m := newLogTestApp(b, 136, logPanelLines)
	for i := range maxLogLines {
		m.ctx.LogLines = append(m.ctx.LogLines, testLogLine(i))
	}
	m.updateLogView()
	return m
}

// BenchmarkLogRefreshFullBuffer is one log update with a full buffer: append
// an entry, refresh, draw.
func BenchmarkLogRefreshFullBuffer(b *testing.B) {
	m := fullLogApp(b)
	entry := testLogLine(1)
	b.ReportAllocs()
	for b.Loop() {
		m.appendLog(entry)
		m.refreshLogView()
		_ = m.log.vp.View()
	}
}

// BenchmarkLogRefreshFullBufferMaterialized is the same update the way it was
// done before: the whole buffer wrapped, styled, and split again.
func BenchmarkLogRefreshFullBufferMaterialized(b *testing.B) {
	m := fullLogApp(b)
	entry := testLogLine(1)
	vp := viewport.New(viewport.WithWidth(136), viewport.WithHeight(logPanelLines))
	b.ReportAllocs()
	for b.Loop() {
		m.ctx.LogLines = appendLogLine(m.ctx.LogLines, entry)
		vp.SetContent(materializedLog(m.ctx.LogLines, 136))
		vp.GotoBottom()
		_ = vp.View()
	}
}

// BenchmarkLogRecountFullBuffer is opening the panel or changing its width:
// every entry's rows are counted, none is styled.
func BenchmarkLogRecountFullBuffer(b *testing.B) {
	m := fullLogApp(b)
	b.ReportAllocs()
	for b.Loop() {
		m.updateLogView()
		_ = m.log.vp.View()
	}
}

// BenchmarkLogScrollFullBuffer is a draw far from both ends of the buffer.
func BenchmarkLogScrollFullBuffer(b *testing.B) {
	m := fullLogApp(b)
	m.log.vp.SetYOffset(m.log.vp.TotalLineCount() / 2)
	b.ReportAllocs()
	for b.Loop() {
		m.log.vp, _ = m.log.vp.Update(ui.KeyPress(ui.Key{Code: 'k'}))
		_ = m.log.vp.View()
	}
}
