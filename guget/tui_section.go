package main

import (
	bubble_tea "charm.land/bubbletea/v2"
)

// Section is the interface for all interactive components (panels and overlays).
// Following the gh-dash pattern where every component implements the same interface.
type Section interface {
	GetID() int
	IsOverlay() bool
	IsActive() bool
	Update(msg bubble_tea.Msg) (Section, bubble_tea.Cmd)
	View() string
	SetContext(ctx *AppContext)
}

// BaseSection provides common infrastructure that all sections embed.
type BaseSection struct {
	ID      int
	Ctx     *AppContext
	Focused bool
	Active  bool // for overlays: whether currently shown
	Overlay bool // true = renders as centered overlay, false = panel slot
}

func (b *BaseSection) GetID() int              { return b.ID }
func (b *BaseSection) IsOverlay() bool         { return b.Overlay }
func (b *BaseSection) IsActive() bool          { return b.Active }
func (b *BaseSection) SetContext(c *AppContext) { b.Ctx = c }
