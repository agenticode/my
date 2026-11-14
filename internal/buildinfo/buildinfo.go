// Package buildinfo holds the values stamped into the binary at build time.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Version and Commit are set with -ldflags -X by the Makefile. A plain
// "go build" leaves the defaults, in which case Commit falls back to the VCS
// revision recorded by the Go toolchain.
var (
	Version = "dev"
	Commit  = ""
)

// Revision returns the source revision of the build, or "unknown".
func Revision() string {
	if Commit != "" {
		return Commit
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				if len(s.Value) > 12 {
					return s.Value[:12]
				}
				return s.Value
			}
		}
	}
	return "unknown"
}

// String returns the one-line text printed by "my version".
func String() string {
	return fmt.Sprintf("my %s (%s) %s %s/%s", Version, Revision(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
