package ui

import (
	"strings"

	text "github.com/nulifyer/guget/internal/ui/text"
)

// Rect is an allocation in terminal cells, with a zero-based origin.
type Rect struct{ X, Y, W, H int }

// Element is a stateful, locally rendered component. The host supplies bounds;
// the element supplies its content. Input goes only to the focused element.
type Element interface {
	Draw(Rect) string
	HandleKey(KeyEvent) Effect
}

// Lifecycle is optional, like connected/disconnected callbacks on web elements.
type Lifecycle interface {
	Mounted()
	Unmounted()
}

// Node declares one element in a frame. Key identifies an instance across
// renders. Replacing a modal's session key unmounts its previous instance.
type Node struct {
	Key     string
	Element Element
	Bounds  Rect
	Focused bool
}
type Host struct{ nodes []Node }

func (h *Host) Reconcile(nodes []Node) {
	previous := make(map[string]Node, len(h.nodes))
	for _, n := range h.nodes {
		previous[n.Key] = n
	}
	current := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		current[n.Key] = n
	}
	for _, n := range h.nodes {
		if _, ok := current[n.Key]; !ok {
			if l, ok := n.Element.(Lifecycle); ok {
				l.Unmounted()
			}
		}
	}
	for _, n := range nodes {
		if _, ok := previous[n.Key]; !ok {
			if l, ok := n.Element.(Lifecycle); ok {
				l.Mounted()
			}
		}
	}
	h.nodes = append(h.nodes[:0], nodes...)
}
func (h *Host) Disconnect() { h.Reconcile(nil) }
func (h *Host) HandleKey(k KeyEvent) Effect {
	for i := len(h.nodes) - 1; i >= 0; i-- {
		if h.nodes[i].Focused {
			return h.nodes[i].Element.HandleKey(k)
		}
	}
	return nil
}
func (h *Host) Draw(w, hgt int) string {
	// Each row is composed from non-overlapping allocations. Clip at the host
	// seam, so one component cannot wrap into a neighbor's cells.
	rows := make([][]string, max(0, hgt))
	for i := range rows {
		rows[i] = make([]string, max(0, w))
		for j := range rows[i] {
			rows[i][j] = " "
		}
	}
	for _, node := range h.nodes {
		r := node.Bounds
		if r.W <= 0 || r.H <= 0 {
			continue
		}
		lines := strings.Split(node.Element.Draw(r), "\n")
		for y := 0; y < r.H && y < len(lines); y++ {
			targetY := r.Y + y
			if targetY < 0 || targetY >= hgt {
				continue
			}
			// Store a whole styled fragment at its first cell, reserving the rest.
			start := max(0, r.X)
			end := min(w, r.X+r.W)
			if start >= end {
				continue
			}
			line := text.Cut(lines[y], max(0, -r.X), max(0, -r.X)+end-start)
			line += strings.Repeat(" ", max(0, end-start-text.StringWidth(line)))
			for x := start; x < end; x++ {
				rows[targetY][x] = ""
			}
			rows[targetY][start] = line
		}
	}
	out := make([]string, len(rows))
	for i := range rows {
		out[i] = strings.Join(rows[i], "")
	}
	return strings.Join(out, "\n")
}

// Static is useful for a status bar or heading whose state belongs to its parent.
type Static string

func (s Static) Draw(Rect) string          { return string(s) }
func (s Static) HandleKey(KeyEvent) Effect { return nil }
