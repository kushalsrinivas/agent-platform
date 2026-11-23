// Package buildinfo carries version metadata injected at build time.
//
// The Makefile sets these with -ldflags, for example:
//
//	go build -ldflags "-X .../internal/platform/buildinfo.Version=1.2.3"
package buildinfo

// Values below are defaults for `go run`.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)
