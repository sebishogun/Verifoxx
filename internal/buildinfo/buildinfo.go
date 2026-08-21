// Package buildinfo holds build-time metadata for the verifoxx binary.
package buildinfo

// version is the build version. It defaults to a deterministic development
// value and can be overridden at link time:
//
//	go build -ldflags "-X github.com/sebishogun/verifoxx/internal/buildinfo.version=v1.2.3"
var version = "devel"

// Version returns the build version.
func Version() string { return version }
