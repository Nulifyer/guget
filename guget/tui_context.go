package main

import (
	bubbles_spinner "charm.land/bubbles/v2/spinner"
)

// AppContext holds shared state that all sections can read.
// Passed by pointer so mutations are visible everywhere.
type AppContext struct {
	// Terminal dimensions
	Width  int
	Height int

	// Shared data
	ParsedProjects []*ParsedProject
	PropsProjects  []*ParsedProject
	NugetServices  []*NugetService
	Results        map[string]nugetResult
	Sources        []NugetSource
	SourceMapping  *PackageSourceMapping

	// Loading state
	Loading         bool
	LoadingDone     int
	LoadingTotal    int
	PendingPackages Set[string]
	Spinner         bubbles_spinner.Model
	Restoring       bool
	Reloading       bool

	// Status bar
	StatusLine  string
	StatusIsErr bool

	// Log panel
	LogLines []string
	ShowLogs bool
}
