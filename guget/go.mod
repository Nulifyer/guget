module github.com/nulifyer/guget

go 1.25.0

require (
	arger v0.0.0
	logger v0.0.0
)

replace (
	arger => ./arger
	logger => ./logger
)

require (
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/term v0.40.0 // indirect
)
