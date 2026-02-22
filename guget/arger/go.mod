module arger

go 1.25.0

require (
	golang.org/x/term v0.40.0
	logger v0.0.0
)

require golang.org/x/sys v0.41.0 // indirect

replace logger => ../logger
