package memras

import (
	"github.com/agenticode/my/internal/yamlite"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
)

// outcome writes a check as one line per setting.
func outcome(r *Result) string {
	var b strings.Builder
	for _, it := range r.Items {
		b.WriteString(it.Setting + ": " + it.Status)
		if it.Attribute != "" {
			b.WriteString(" " + it.Attribute)
		}
		if it.Value != "" {
			b.WriteString("=" + it.Value)
		}
		if it.State != "" {
			b.WriteString(" (" + it.State + ")")
		}
		if it.HasPending {
			b.WriteString(" pending " + it.Pending + " (" + it.PendingState + ")")
		}
		if it.Reason != "" {
			b.WriteString(" [" + it.Reason + "]")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestBuiltinProfile(t *testing.T) {
	p := Builtin()
	if p.Name != "built-in" || len(p.Vendors) != 3 {
		t.Fatalf("profile %q with %d vendors", p.Name, len(p.Vendors))
	}
	if !reflect.DeepEqual(p.Expect, map[string]string{PatrolScrub: "on", CELogging: "on"}) {
		t.Errorf("expect = %v", p.Expect)
	}
	for _, v := range p.Vendors {
		if len(v.Manufacturer) == 0 || len(v.Maps) == 0 {
			t.Errorf("vendor %s: manufacturer %v, %d mappings", v.ID, v.Manufacturer, len(v.Maps))
		}
		// Every setting is mapped or listed as unmapped: nothing is left
		// unsaid.
		for _, s := range Settings {
			mapped := false
			for _, m := range v.Maps {
				mapped = mapped || m.Setting == s
			}
			if _, un := v.Unmapped[s]; mapped == un {
				t.Errorf("vendor %s: setting %s is mapped %v and listed as unmapped %v", v.ID, s, mapped, un)
			}
		}
		// Every mapping names a public document by its URL.
		for _, m := range v.Maps {
			src, ok := v.Sources[m.Source]
			if !ok || src.Title == "" {
				t.Errorf("vendor %s, %s: source %q", v.ID, m.Attribute, m.Source)
				continue
			}
			u, err := url.Parse(src.URL)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				t.Errorf("vendor %s, %s: URL %q", v.ID, m.Attribute, src.URL)
			}
		}
	}
	// The catalog has words for every setting and every state, in both
	// languages.
	for _, s := range Settings {
		if !Messages.Has("setting." + s) {
			t.Errorf("no words for the setting %s", s)
		}
		for _, st := range States(s) {
			if !Messages.Has("state." + st) {
				t.Errorf("no words for the state %s", st)
			}
		}
	}
	for code, m := range Messages {
		if m.EN == "" || m.KO == "" {
			t.Errorf("%s lacks a language", code)
		}
	}
	if !Mapped("MemPatrolScrub") || !Mapped("AdvancedMemProtection") || !Mapped("Memory_MemorySpeed") || Mapped("BootMode") {
		t.Errorf("Mapped")
	}
	if !strings.HasPrefix(string(BuiltinText()), "# MemorY memory RAS settings profile.") {
		t.Errorf("BuiltinText")
	}
	if n := len(p.SourceList()); n != 3 {
		t.Errorf("%d sources", n)
	}
}

func set(name, value string, allowed ...string) model.BIOSSetting {
	s := model.BIOSSetting{Name: name, Value: value, HasValue: true, ObservedAt: time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC), Source: "redfish/bios"}
	if len(allowed) > 0 {
		s.Registry, s.Allowed = "R.v1", allowed
	}
	return s
}

func TestCheckOutcomes(t *testing.T) {
	p := Builtin()

	// Not as expected, and this BIOS could be set as expected.
	r := p.Check(Input{Manufacturer: "Dell Inc.", Settings: []model.BIOSSetting{set("MemPatrolScrub", "Disabled", "Standard", "Extended", "Disabled"), set("CorrEccSmi", "Disabled")}})
	if it := r.Item(PatrolScrub); it.Status != StatusMismatch || it.State != "off" {
		t.Errorf("patrol scrub off = %+v", it)
	}
	// Without the registry it is a mismatch too; the result says that the
	// registry was not read.
	if it := r.Item(CELogging); it.Status != StatusMismatch || it.Registry {
		t.Errorf("CE logging off = %+v", it)
	}
	if r.Mismatches != 2 {
		t.Errorf("mismatches = %d", r.Mismatches)
	}

	// The registry of this BIOS version allows no value that means "on":
	// the expectation does not fit, and that is no mismatch.
	r = p.Check(Input{Manufacturer: "Dell Inc.", Settings: []model.BIOSSetting{set("MemPatrolScrub", "Disabled", "Disabled")}})
	if it := r.Item(PatrolScrub); it.Status != StatusNoFit || r.Mismatches != 0 {
		t.Errorf("no fit = %+v, mismatches %d", it, r.Mismatches)
	}

	// A value the profile does not know.
	r = p.Check(Input{Manufacturer: "Dell Inc.", Settings: []model.BIOSSetting{set("MemPatrolScrub", "Turbo"), set("MemFrequency", "MaxPower")}})
	if it := r.Item(PatrolScrub); it.Status != StatusUnknownValue || it.State != "" || it.Value != "Turbo" {
		t.Errorf("unknown value = %+v", it)
	}
	if it := r.Item(MemoryFrequency); it.Status != StatusUnknownValue {
		t.Errorf("MemFrequency=MaxPower = %+v", it)
	}

	// Values with a "*": every frequency in MHz, and an exact value wins.
	for value, state := range map[string]string{"2933MHz": "limited", "800MHz": "limited", "MaxPerf": "auto", "MaxReliability": "limited"} {
		r = p.Check(Input{Manufacturer: "Dell Inc.", Settings: []model.BIOSSetting{set("MemFrequency", value)}})
		if it := r.Item(MemoryFrequency); it.State != state {
			t.Errorf("MemFrequency=%s means %q, want %q", value, it.State, state)
		}
	}
	r = p.Check(Input{Manufacturer: "HPE", Settings: []model.BIOSSetting{set("MaxMemBusFreqMHz", "MaxMemBusFreq2400")}})
	if it := r.Item(MemoryFrequency); it.State != "limited" {
		t.Errorf("HPE frequency = %+v", it)
	}

	// An attribute listed without a value is not a value.
	r = p.Check(Input{Manufacturer: "Dell Inc.", Settings: []model.BIOSSetting{{Name: "MemPatrolScrub", ObservedAt: time.Now()}}})
	if it := r.Item(PatrolScrub); it.Status != StatusAbsent {
		t.Errorf("no value = %+v", it)
	}

	// No settings at all, and a vendor the profile does not know.
	r = p.Check(Input{Manufacturer: "Dell Inc."})
	if r.Settings != 0 || r.Item(PatrolScrub).Status != StatusAbsent || r.Mismatches != 0 || r.Speed != nil {
		t.Errorf("no settings: %s", outcome(r))
	}
	r = p.Check(Input{Manufacturer: "Example Systems", Settings: []model.BIOSSetting{set("PatrolScrub_0042", "Enabled")}})
	if r.Vendor != "" || r.Item(PatrolScrub).Status != StatusUnmapped || r.Item(PatrolScrub).Reason != ReasonNoVendor || len(r.Related) != 1 {
		t.Errorf("unknown vendor: %s", outcome(r))
	}

	// The manufacturer is not on record: the vendor is the one whose
	// attributes the BIOS has.
	r = p.Check(Input{Settings: []model.BIOSSetting{set("MemPatrolScrubbing", "Enabled"), set("AdvancedMemProtection", "AdvancedEcc")}})
	if r.Vendor != "HPE" || r.By != "attributes" || r.Item(PatrolScrub).Status != StatusOK {
		t.Errorf("vendor by attributes: %q by %q", r.Vendor, r.By)
	}
	// An attribute name two vendors share settles nothing.
	r = p.Check(Input{Settings: []model.BIOSSetting{set("MemRefreshRate", "1x")}})
	if r.Vendor != "" {
		t.Errorf("vendor %q from an attribute two vendors have", r.Vendor)
	}
	// The manufacturer wins over the attributes.
	r = p.Check(Input{Manufacturer: "Hewlett Packard Enterprise", Settings: []model.BIOSSetting{set("MemPatrolScrub", "Standard")}})
	if r.Vendor != "HPE" || r.Item(PatrolScrub).Status != StatusAbsent {
		t.Errorf("manufacturer first: %q, %s", r.Vendor, outcome(r))
	}
}

func notes(it *Item) string {
	var codes []string
	for _, n := range it.Notes {
		codes = append(codes, n.Code)
	}
	return strings.Join(codes, " ")
}

func TestWidthRules(t *testing.T) {
	p := Builtin()
	settings := []model.BIOSSetting{set("AdddcSetting", "Enabled"), set("MemOpMode", "OptimizerMode")}
	mod := func(slot string, width, ranks int, channel string) Module {
		return Module{Slot: slot, Width: width, Ranks: ranks, Channel: channel}
	}

	// ADDDC on with x8 DIMMs: on, and without effect on them.
	r := p.Check(Input{Manufacturer: "Dell Inc.", Settings: settings, Modules: []Module{mod("A1", 8, 2, "0/0/0"), mod("A2", 4, 2, "0/0/1"), mod("B1", 8, 2, "1/0/0")}})
	a := r.Item(ADDDC)
	if notes(a) != "note.adddc_x8" {
		t.Fatalf("notes = %s", notes(a))
	}
	text := Text(i18n.EN, a.Notes[0])
	if text != "2 of 3 DIMMs are x8 (A1, B1): ADDDC is on and has no effect on them. ADDDC works with x4 DIMMs only; it is a feature of Intel Xeon Scalable platforms" {
		t.Errorf("note: %s", text)
	}
	if ko := Text(i18n.KO, a.Notes[0]); !strings.Contains(ko, "x8(A1, B1)") || !strings.Contains(ko, "효과 없음") {
		t.Errorf("note in Korean: %s", ko)
	}
	// Device correction on x8: a nibble, not the device.
	if m := r.Item(MemoryMode); notes(m) != "note.sddc_x8" {
		t.Errorf("memory mode notes = %s", notes(m))
	}
	// The rules name their sources.
	urls := []string{}
	for _, s := range r.Sources {
		urls = append(urls, s.URL)
	}
	sort.Strings(urls)
	if len(urls) != 3 || !strings.Contains(strings.Join(urls, " "), SourceWidth.URL) || !strings.Contains(strings.Join(urls, " "), SourceAMDC.URL) {
		t.Errorf("sources = %v", urls)
	}

	// A width that is not known is said, and not taken for x4.
	r = p.Check(Input{Manufacturer: "Dell Inc.", Settings: settings, Modules: []Module{mod("A1", 0, 2, "0/0/0"), mod("A2", 4, 2, "0/0/1")}})
	if a := r.Item(ADDDC); notes(a) != "note.adddc_width_unknown" || !strings.Contains(Text(i18n.EN, a.Notes[0]), "1 of 2 DIMMs is not known") {
		t.Errorf("unknown width: %s", notes(a))
	}
	if m := r.Item(MemoryMode); len(m.Notes) != 0 {
		t.Errorf("memory mode notes = %s", notes(m))
	}

	// One rank on a channel.
	r = p.Check(Input{Manufacturer: "Dell Inc.", Settings: settings, Modules: []Module{mod("A1", 4, 1, "0/0/0"), mod("A2", 4, 2, "0/0/1"), mod("B1", 4, 1, "1/0/0"), mod("B2", 4, 1, "1/0/0")}})
	if a := r.Item(ADDDC); notes(a) != "note.adddc_one_rank" || !strings.Contains(Text(i18n.EN, a.Notes[0]), "1 DIMM is on a channel with one rank (A1)") {
		t.Errorf("one rank: %s %v", notes(a), a.Notes)
	}
	// Without the place of every DIMM nothing is said about ranks.
	r = p.Check(Input{Manufacturer: "Dell Inc.", Settings: settings, Modules: []Module{mod("A1", 4, 1, ""), mod("A2", 4, 1, "0/0/1")}})
	if a := r.Item(ADDDC); len(a.Notes) != 0 {
		t.Errorf("notes without places: %s", notes(a))
	}

	// ADDDC off, or all DIMMs x4: nothing to say.
	r = p.Check(Input{Manufacturer: "Dell Inc.", Settings: []model.BIOSSetting{set("AdddcSetting", "Disabled")}, Modules: []Module{mod("A1", 8, 2, "0/0/0")}})
	if a := r.Item(ADDDC); len(a.Notes) != 0 {
		t.Errorf("ADDDC off: %s", notes(a))
	}
	r = p.Check(Input{Manufacturer: "Dell Inc.", Settings: settings, Modules: []Module{mod("A1", 4, 2, "0/0/0"), mod("A2", 4, 2, "0/0/1")}})
	if a := r.Item(ADDDC); len(a.Notes) != 0 || len(r.Item(MemoryMode).Notes) != 0 {
		t.Errorf("all x4: %s", notes(a))
	}
}

func candidates(sp *Speed) string {
	var codes []string
	for _, c := range sp.Candidates {
		codes = append(codes, c.Code)
	}
	return strings.Join(codes, " ")
}

func TestSpeed(t *testing.T) {
	p := Builtin()
	mod := func(slot string, operating, rated int, channel string) Module {
		return Module{Slot: slot, Operating: operating, Rated: rated, Channel: channel}
	}
	limited := []model.BIOSSetting{set("MemFrequency", "2666MHz")}

	// The BIOS limits the frequency, and every DIMM runs at one speed.
	r := p.Check(Input{Manufacturer: "Dell Inc.", Settings: limited, Modules: []Module{mod("A1", 2666, 2933, "0/0/0"), mod("B1", 2666, 2933, "1/0/0")}})
	if r.Speed == nil || len(r.Speed.Slow) != 2 || r.Speed.Known != 2 || candidates(r.Speed) != "speed.bios speed.platform" {
		t.Fatalf("speed = %+v", r.Speed)
	}
	if got := Text(i18n.EN, r.Speed.Candidates[0]); got != "the BIOS limits the memory frequency (MemFrequency = 2666MHz)" {
		t.Errorf("candidate: %s", got)
	}
	// A speed is written as a number, not with separators.
	if got := Text(i18n.EN, r.Speed.Candidates[1]); !strings.Contains(got, "runs at 2666 MT/s:") {
		t.Errorf("candidate: %s", got)
	}

	// Two DIMMs on a channel, and a module that is rated lower.
	r = p.Check(Input{Manufacturer: "Dell Inc.", Settings: []model.BIOSSetting{set("MemFrequency", "MaxPerf")},
		Modules: []Module{mod("A1", 2400, 2933, "0/0/0"), mod("A2", 2400, 2933, "0/0/0"), mod("B1", 2400, 2400, "1/0/0")}})
	if candidates(r.Speed) != "speed.per_channel speed.mixed speed.platform" || len(r.Speed.Slow) != 2 {
		t.Errorf("speed = %+v", r.Speed)
	}

	// Nothing in the records explains it: no candidate is made up.
	r = p.Check(Input{Manufacturer: "Dell Inc.", Modules: []Module{mod("A1", 2400, 2933, ""), mod("B1", 2933, 2933, "")}})
	if len(r.Speed.Slow) != 1 || len(r.Speed.Candidates) != 0 {
		t.Errorf("speed = %+v", r.Speed)
	}

	// At the rated speed, and speeds that are not known.
	r = p.Check(Input{Manufacturer: "Dell Inc.", Modules: []Module{mod("A1", 2933, 2933, "0/0/0"), mod("B1", 0, 2933, "1/0/0")}})
	if r.Speed == nil || len(r.Speed.Slow) != 0 || r.Speed.Known != 1 {
		t.Errorf("speed = %+v", r.Speed)
	}
	if r = p.Check(Input{Manufacturer: "Dell Inc.", Modules: []Module{mod("A1", 0, 0, "")}}); r.Speed != nil {
		t.Errorf("speed = %+v", r.Speed)
	}
}

func TestLatest(t *testing.T) {
	old := set("MemPatrolScrub", "Disabled")
	old.ObservedAt = old.ObservedAt.Add(-24 * time.Hour)
	got := Latest([]model.BIOSSetting{old, set("MemPatrolScrub", "Standard"), set("MemTest", "Disabled")})
	if len(got) != 2 || got[0].Value != "Standard" {
		t.Errorf("latest = %+v", got)
	}
	if Latest(nil) != nil {
		t.Errorf("Latest(nil)")
	}
}

func TestProfileOfOnesOwn(t *testing.T) {
	text := `# a fleet's own expectations
format: 1
name: "fleet-a"
expect:
  patrol-scrub: off
  ppr: on
  memory-mode: mirroring
why:
  ppr: 'our repair policy'   # in words of our own
vendors:
  - id: acme
    name: "Acme: servers"
    manufacturer:
      - acme
      - ' Acme Systems '
    sources:
      guide:
        title: Acme BIOS guide
        url: https://docs.example.com/acme/bios#memory
    settings:
      - setting: ppr
        attribute: PprMode
        source: guide
        values:
          - value: "Hard: boot"
            means: on
            note: at boot
          - value: 'It''s off'
            means: off
      - setting: patrol-scrub
        attribute: Scrub
        source: guide
        values:
          - value: "*h"
            means: on
          - value: "0h"
            means: off
    unmapped:
      adddc: this BIOS has no such setting
`
	path := filepath.Join(t.TempDir(), "fleet.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "fleet-a" || len(p.Vendors) != 1 || p.Vendors[0].Name != "Acme: servers" || p.Why[PPR] != "our repair policy" ||
		!reflect.DeepEqual(p.Vendors[0].Manufacturer, []string{"acme", "acme systems"}) || p.Vendors[0].Sources["guide"].URL != "https://docs.example.com/acme/bios#memory" {
		t.Fatalf("profile = %+v, vendor %+v", p, p.Vendors[0])
	}
	r := p.Check(Input{Manufacturer: "ACME Corp", Settings: []model.BIOSSetting{set("PprMode", "It's off"), set("Scrub", "24h")}})
	if it := r.Item(PPR); it.Status != StatusMismatch || it.State != "off" || WhyText(i18n.EN, it) != "our repair policy" {
		t.Errorf("ppr = %+v", it)
	}
	// "24h" is on by the pattern, and this profile expects off; the words
	// of the built-in profile for its own expectation do not apply.
	if it := r.Item(PatrolScrub); it.Status != StatusMismatch || it.State != "on" || WhyText(i18n.EN, it) != "" {
		t.Errorf("patrol scrub = %+v, why %q", it, WhyText(i18n.EN, it))
	}
	// An exact value wins over the pattern.
	r = p.Check(Input{Manufacturer: "ACME Corp", Settings: []model.BIOSSetting{set("Scrub", "0h")}})
	if it := r.Item(PatrolScrub); it.State != "off" || it.Status != StatusOK {
		t.Errorf("patrol scrub 0h = %+v", it)
	}
	if it := r.Item(ADDDC); it.Reason != "this BIOS has no such setting" || ReasonText(i18n.KO, it.Reason) != "this BIOS has no such setting" {
		t.Errorf("adddc = %+v", it)
	}
	// An expectation without a mapping stays an open question, not a pass.
	if it := r.Item(MemoryMode); it.Status != StatusUnmapped || it.Expected != "mirroring" {
		t.Errorf("memory mode = %+v", it)
	}

	if _, err := ReadFile(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Errorf("a missing file was read")
	}
	if _, err := ReadFile(t.TempDir()); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("a directory: %v", err)
	}
}

func TestProfileMistakes(t *testing.T) {
	head := "format: 1\nvendors:\n  - id: a\n    name: A\n    sources:\n      s:\n        url: https://example.com/doc\n"
	for name, tt := range map[string]struct{ text, want string }{
		"empty":             {"", "holds nothing"},
		"comments only":     {"# nothing\n\n", "holds nothing"},
		"no format":         {"name: x\n", "no \"format: 1\" line"},
		"later format":      {"format: 2\n", "line 1: format 2: this version reads format 1"},
		"unknown key":       {"format: 1\ncolour: red\n", "line 2: profile: unknown key \"colour\""},
		"unknown setting":   {"format: 1\nexpect:\n  scrubbing: on\n", "line 3: expect: unknown key \"scrubbing\""},
		"unknown state":     {"format: 1\nexpect:\n  patrol-scrub: enabled\n", "line 3: expect patrol-scrub: \"enabled\" is not one of on, off"},
		"tab":               {"format: 1\nexpect:\n\tpatrol-scrub: on\n", "line 3: a tab in the indentation"},
		"flow list":         {"format: 1\nvendors: [a, b]\n", "line 2: flow style ([) is not read"},
		"anchor":            {"format: 1\nname: &x y\n", "line 2: a value that starts with & is not read"},
		"two documents":     {"format: 1\n---\nformat: 1\n", "line 2: document markers are not read"},
		"duplicate key":     {"format: 1\nname: a\nname: b\n", "line 3: the key \"name\" is given twice"},
		"open quote":        {"format: 1\nname: \"abc\n", "line 2: a quoted text without its closing quote"},
		"bad escape":        {"format: 1\nname: \"a\\qb\"\n", "line 2: the escape \\q is not read"},
		"odd indentation":   {"format: 1\nexpect:\n    patrol-scrub: on\n  ppr: on\n", "line 4: the indentation does not fit"},
		"list for a map":    {"format: 1\nexpect:\n  - patrol-scrub\n", "line 3: expect: expected keys with values"},
		"vendor without id": {"format: 1\nvendors:\n  - name: A\n", "vendor: no \"id\""},
		"vendor twice":      {head + "  - id: a\n    name: B\n", "the vendor \"a\" is given twice"},
		"source without url": {"format: 1\nvendors:\n  - id: a\n    name: A\n    sources:\n      s:\n        title: T\n",
			"source s: no \"url\": every mapping names the public document it comes from"},
		"source not a url": {"format: 1\nvendors:\n  - id: a\n    name: A\n    sources:\n      s:\n        url: the manual\n", "is no URL of a public document"},
		"mapping without source": {head + "    settings:\n      - setting: ppr\n        attribute: X\n        values:\n          - value: a\n            means: on\n",
			"a mapping without \"source\""},
		"mapping with an unknown source": {head + "    settings:\n      - setting: ppr\n        attribute: X\n        source: other\n        values:\n          - value: a\n            means: on\n",
			"names the source \"other\", which the vendor does not list"},
		"mapping without values": {head + "    settings:\n      - setting: ppr\n        attribute: X\n        source: s\n", "has no values"},
		"value with a wrong meaning": {head + "    settings:\n      - setting: ppr\n        attribute: X\n        source: s\n        values:\n          - value: a\n            means: hard\n",
			"value \"a\" means \"hard\", which is not one of on, off"},
		"value twice": {head + "    settings:\n      - setting: ppr\n        attribute: X\n        source: s\n        values:\n          - value: a\n            means: on\n          - value: a\n            means: off\n",
			"the value \"a\" is given twice"},
		"setting twice": {head + "    settings:\n      - setting: ppr\n        attribute: X\n        source: s\n        values:\n          - value: a\n            means: on\n      - setting: ppr\n        attribute: Y\n        source: s\n        values:\n          - value: a\n            means: on\n",
			"the setting ppr is mapped twice"},
		"mapped and unmapped": {head + "    settings:\n      - setting: ppr\n        attribute: X\n        source: s\n        values:\n          - value: a\n            means: on\n    unmapped:\n      ppr: no\n",
			"is mapped and listed as unmapped"},
		"too deep": {"format: 1\n" + func() string {
			var b strings.Builder
			for i := 0; i < 20; i++ {
				b.WriteString(strings.Repeat("  ", i) + "why:\n")
			}
			return b.String()
		}(), "nested deeper than"},
	} {
		_, err := Parse([]byte(tt.text))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error %v, want %q", name, err, tt.want)
		}
	}
	if _, err := Parse(make([]byte, yamlite.MaxBytes+1)); err == nil {
		t.Errorf("a file over the size limit was read")
	}
}

func FuzzParse(f *testing.F) {
	f.Add(BuiltinText())
	f.Add([]byte("format: 1\nexpect:\n  ppr: on\nvendors:\n  - id: a\n    name: A\n    sources:\n      s:\n        url: https://example.com\n    settings:\n      - setting: ppr\n        attribute: X\n        source: s\n        values:\n          - value: \"*\"\n            means: on\n"))
	f.Add([]byte("a:\n  - b: 'c'\n    d:\n      - e\n\"f\": \"g\\n\"\n"))
	f.Add([]byte("- - -\n-\n  -:\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Parse(data)
		if err != nil {
			return
		}
		// A profile that parses can be checked against anything.
		r := p.Check(Input{Manufacturer: "x", Settings: []model.BIOSSetting{set("X", "a"), set("MemPatrolScrub", "Standard")},
			Modules: []Module{{Slot: "A1", Width: 8, Ranks: 1, Channel: "0/0/0", Operating: 1, Rated: 2}}})
		if len(r.Items) != len(Settings) {
			t.Fatalf("%d items", len(r.Items))
		}
		for _, it := range r.Items {
			StatusText(i18n.KO, it.Status)
			WhyText(i18n.EN, &it)
		}
	})
}

// A value pattern never matches by accident.
func TestValuePatterns(t *testing.T) {
	for _, tt := range []struct {
		pattern, value string
		want           bool
	}{
		{"Enabled", "Enabled", true}, {"Enabled", "enabled", false}, {"*MHz", "2666MHz", true}, {"*MHz", "MHz", true}, {"*MHz", "2666", false},
		{"MaxMemBusFreq*", "MaxMemBusFreq2933", true}, {"MaxMemBusFreq*", "Auto", false}, {"*", "", true}, {"*", "anything", true},
		{"a*b*c", "aXbYc", true}, {"a*b*c", "abc", true}, {"a*b*c", "acb", false}, {"a*b", "ab", true}, {"a*b", "a", false}, {"", "", true}, {"", "x", false},
	} {
		v := Value{Match: tt.pattern}
		if got := v.matches(tt.value); got != tt.want {
			t.Errorf("%q matches %q = %v, want %v", tt.pattern, tt.value, got, tt.want)
		}
	}
}
