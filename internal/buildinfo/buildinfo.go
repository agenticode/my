// Package buildinfo holds the values stamped into the binary at build time.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// Version, Commit and Date say what the binary was built from. A release
// build sets them with -ldflags -X (see the Makefile and tools/dist): the
// version of the release, the commit and the time of that commit. A build
// without them takes what the Go toolchain recorded: the module version
// of a "go install module@version", and the revision and commit time of
// the working tree.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func init() {
	if bi, ok := debug.ReadBuildInfo(); ok {
		fill(bi)
	}
}

// fill completes the stamps from the build information of the toolchain.
// A value that was stamped is kept.
func fill(bi *debug.BuildInfo) {
	if Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		Version = bi.Main.Version
	}
	var revision, when string
	modified := false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if Commit == "" && revision != "" {
		if len(revision) > 12 {
			revision = revision[:12]
		}
		// Files that differ from the commit make the build something
		// else than the commit.
		if modified {
			revision += "-dirty"
		}
		Commit = revision
	}
	if Date == "" {
		Date = when
	}
}

// Revision returns the source revision of the build, or "unknown".
func Revision() string {
	if Commit != "" {
		return Commit
	}
	return "unknown"
}

// Day returns the day of the commit the binary was built from, or "" when
// it is not known.
func Day() string {
	t, err := time.Parse(time.RFC3339, Date)
	if err != nil {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// String returns the one-line text printed by "my version".
func String() string {
	source := Revision()
	if d := Day(); d != "" {
		source += ", " + d
	}
	return fmt.Sprintf("my %s (%s) %s %s/%s", strings.TrimSpace(Version), source, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
