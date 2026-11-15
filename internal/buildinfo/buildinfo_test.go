package buildinfo

import (
	"regexp"
	"runtime/debug"
	"testing"
)

// stamp sets the stamps for one test and restores them afterwards.
func stamp(t *testing.T, version, commit, date string) {
	t.Helper()
	v, c, d := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = v, c, d })
	Version, Commit, Date = version, commit, date
}

func settings(kv ...string) []debug.BuildSetting {
	var out []debug.BuildSetting
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
	}
	return out
}

func TestFill(t *testing.T) {
	// A plain build of a working tree: the toolchain knows the revision.
	stamp(t, "dev", "", "")
	fill(&debug.BuildInfo{Main: debug.Module{Version: "(devel)"},
		Settings: settings("vcs.revision", "0123456789abcdef0123456789abcdef01234567", "vcs.time", "2026-11-15T04:17:53Z", "vcs.modified", "false")})
	if Version != "dev" || Commit != "0123456789ab" || Date != "2026-11-15T04:17:53Z" || Day() != "2026-11-15" {
		t.Errorf("working tree: %q %q %q", Version, Commit, Date)
	}
	if got, want := String(), `^my dev \(0123456789ab, 2026-11-15\) go\S+ \w+/\w+$`; !regexp.MustCompile(want).MatchString(got) {
		t.Errorf("String() = %q, want %s", got, want)
	}

	// Files that differ from the commit are named.
	stamp(t, "dev", "", "")
	fill(&debug.BuildInfo{Settings: settings("vcs.revision", "0123456789abcdef", "vcs.modified", "true")})
	if Commit != "0123456789ab-dirty" || Revision() != "0123456789ab-dirty" || Day() != "" {
		t.Errorf("modified tree: %q, day %q", Commit, Day())
	}

	// "go install module@version": the module version is the version.
	stamp(t, "dev", "", "")
	fill(&debug.BuildInfo{Main: debug.Module{Version: "v0.3.1"}})
	if Version != "v0.3.1" || Revision() != "unknown" {
		t.Errorf("installed module: %q %q", Version, Revision())
	}
	if got := String(); !regexp.MustCompile(`^my v0\.3\.1 \(unknown\) go\S+ \w+/\w+$`).MatchString(got) {
		t.Errorf("String() = %q", got)
	}

	// What a release build stamped stays.
	stamp(t, "v1.2.0", "aaaaaaaaaaaa", "2026-01-02T03:04:05Z")
	fill(&debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}, Settings: settings("vcs.revision", "bbbbbbbbbbbbbbbb", "vcs.time", "2020-01-01T00:00:00Z", "vcs.modified", "true")})
	if Version != "v1.2.0" || Commit != "aaaaaaaaaaaa" || Day() != "2026-01-02" {
		t.Errorf("stamped build: %q %q %q", Version, Commit, Date)
	}

	// A date that is no date is not printed.
	stamp(t, "v1.2.0", "aaaaaaaaaaaa", "yesterday")
	if Day() != "" || String()[:len("my v1.2.0 (aaaaaaaaaaaa) ")] != "my v1.2.0 (aaaaaaaaaaaa) " {
		t.Errorf("bad date: day %q, %q", Day(), String())
	}
}
