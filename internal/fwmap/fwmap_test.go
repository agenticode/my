package fwmap

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/model"
)

// handEntry is a log entry written by hand from a registry: the message
// ID with its registry, arguments of a made-up module, and what it has to
// mean.
type handEntry struct {
	id     string
	args   []string
	origin bool
	want   fwevent.Name
	flags  string
	clears fwevent.Name
	// place the arguments must give: socket/channel/position/rank or labels.
	place string
}

// The entries of the shipped tables, one per line, written from the
// registries the tables cite. A line no entry covers fails the test.
var handEntries = map[string][]handEntry{
	"dmtf": {
		{id: "Platform.1.5.MemoryUncorrectableError", args: []string{"DIMM_A1"}, want: fwevent.UE, place: "DIMM_A1"},
		{id: "Platform.1.3.MemoryContainedError", args: []string{"DIMM_A1"}, want: fwevent.UE, flags: "contained", place: "DIMM_A1"},
		{id: "Platform.1.5.MemoryParityError", args: []string{"DIMM_B2"}, want: fwevent.UE, place: "DIMM_B2"},
		{id: "Platform.1.3.MemoryRowRemappingPending", args: []string{"DIMM_A1"}, want: fwevent.PPRScheduled, place: "DIMM_A1"},
		{id: "Platform.1.3.MemoryRowRemappingError", args: []string{"DIMM_A1"}, want: fwevent.PPRFailed, place: "DIMM_A1"},
		{id: "Platform.1.5.MemoryMirroringDegraded", args: []string{"DIMM_C1"}, want: fwevent.MirrorFailover, place: "DIMM_C1"},
		{id: "Platform.1.5.MemoryMirroringFailed", args: []string{"DIMM_C1"}, want: fwevent.MirrorLost, place: "DIMM_C1"},
		{id: "Platform.1.5.MemorySparingActivated", args: []string{"DIMM_D1"}, want: fwevent.Sparing, place: "DIMM_D1"},
		{id: "Platform.1.5.MemorySparingFailed", args: []string{"DIMM_D1"}, want: fwevent.SparingFailed, place: "DIMM_D1"},
		{id: "Platform.1.5.MemoryTrainingFailed", args: []string{"DIMM_E1"}, want: fwevent.TrainingFailure, place: "DIMM_E1"},
		{id: "LogService.1.2.LogCleared", args: []string{"SEL"}, want: fwevent.LogCleared},
		{id: "ResourceEvent.1.5.ResourceErrorThresholdExceeded", args: []string{"CorrectableECCErrorCount", "100"}, origin: true, want: fwevent.CEThreshold},
		{id: "ResourceEvent.1.5.ResourceErrorThresholdExceeded", args: []string{"UncorrectableECCErrorCount", "1"}, origin: true, want: fwevent.UE},
		{id: "ResourceEvent.1.5.ResourceWarningThresholdExceeded", args: []string{"CorrectableECCErrorCount", "50"}, origin: true, want: fwevent.CEThreshold},
		{id: "ResourceEvent.1.5.ResourceWarningThresholdExceeded", args: []string{"UncorrectableECCErrorCount", "1"}, origin: true, want: fwevent.UE},
		{id: "ResourceEvent.1.5.ResourceErrorThresholdCleared", args: []string{"CorrectableECCErrorCount", "100"}, origin: true, want: fwevent.Cleared, clears: fwevent.CEThreshold},
		{id: "ResourceEvent.1.5.ResourceWarningThresholdCleared", args: []string{"CorrectableECCErrorCount", "50"}, origin: true, want: fwevent.Cleared, clears: fwevent.CEThreshold},
		{id: "ResourceEvent.1.5.ResourceErrorsDetected", args: []string{"Health", "ECC"}, origin: true, want: fwevent.Unclassified},
		{id: "ResourceEvent.1.5.ResourceErrorsCorrected", args: []string{"Health", "ECC"}, origin: true, want: fwevent.Cleared},
	},
	"openbmc": {
		{id: "OpenBMC.0.1.MemoryECCCorrectable", args: []string{"1", "A", "1", "0"}, want: fwevent.CE, place: "1/0/1/0"},
		{id: "OpenBMC.0.1.MemoryECCUncorrectable", args: []string{"2", "C", "0", "1"}, want: fwevent.UE, place: "2/2/0/1"},
		{id: "OpenBMC.0.1.MemoryParityCommandAndAddress", args: []string{"1", "B", "1", "1", "0"}, want: fwevent.UE, place: "1/1/-/-"},
		{id: "OpenBMC.0.1.MemoryParityNotKnown", args: []string{"1", "B", "1", "0", "0"}, want: fwevent.UE, place: "1/-/-/-"},
		{id: "OpenBMC.0.1.ADDDCCorrectable", args: []string{"1", "A", "0", "1"}, want: fwevent.DeviceSparing, flags: "adddc", place: "1/0/0/1"},
		{id: "OpenBMC.0.1.SparingRedundancyDegraded", args: []string{"1", "A", "0", "B", "1"}, want: fwevent.Sparing, flags: "rank", place: "1/0/0/1"},
		{id: "OpenBMC.0.1.SparingRedundancyFull", args: []string{"1", "A", "0", "B", "1"}, want: fwevent.Cleared, clears: fwevent.Sparing, place: "1/0/0/1"},
		{id: "OpenBMC.0.1.MirroringRedundancyDegraded", args: []string{"1", "A", "0", "1", "0"}, want: fwevent.MirrorFailover, place: "1/0/0/0"},
		{id: "OpenBMC.0.1.MirroringRedundancyFull", args: []string{"1", "A", "0", "1", "0"}, want: fwevent.Cleared, clears: fwevent.MirrorFailover, place: "1/0/0/0"},
		{id: "OpenBMC.0.1.MemoryRASConfigurationDisabled", args: []string{"None", "Mirroring"}, want: fwevent.ConfigState},
		{id: "OpenBMC.0.1.MemoryRASConfigurationEnabled", args: []string{"None", "Mirroring"}, want: fwevent.ConfigState},
		{id: "OpenBMC.0.1.MemoryRASModeDisabled", args: []string{"Sparing", "None"}, want: fwevent.ConfigState},
		{id: "OpenBMC.0.1.MemoryRASModeEnabled", args: []string{"None", "Sparing"}, want: fwevent.ConfigState},
		{id: "OpenBMC.0.1.MemoryThermTrip", args: []string{"CPU 1"}, want: fwevent.Thermal},
		{id: "OpenBMC.0.5.EventLogCleared", want: fwevent.LogCleared},
	},
	"ipmi": {
		{id: "memory-00", want: fwevent.CE}, {id: "memory-01", want: fwevent.UE}, {id: "memory-02", want: fwevent.UE},
		{id: "memory-03", want: fwevent.ScrubCE}, {id: "memory-04", want: fwevent.Disabled}, {id: "memory-05", want: fwevent.CELogLimit},
		{id: "memory-06", want: fwevent.Presence}, {id: "memory-07", want: fwevent.ConfigError}, {id: "memory-08", want: fwevent.Sparing},
		{id: "memory-09", want: fwevent.Thermal}, {id: "memory-0a", want: fwevent.Thermal},
	},
}

func placeText(form string, r Result, args []string) string {
	p := PlaceOf(form, r.Roles, args)
	if len(p.Labels) > 0 {
		return strings.Join(p.Labels, ",")
	}
	if !p.Socket.Valid {
		return ""
	}
	f := func(o model.Opt[int]) string {
		if !o.Valid {
			return "-"
		}
		return fmt.Sprint(o.V)
	}
	return strings.Join([]string{f(p.Socket), f(p.Channel), f(p.Position), f(p.Rank)}, "/")
}

// TestEveryLineOfTheShippedTables reads each hand-written entry and checks
// that every line of every shipped table is met by one.
func TestEveryLineOfTheShippedTables(t *testing.T) {
	c := Shipped()
	if len(c.Notes) > 0 {
		t.Fatalf("the shipped tables load with notes: %v", c.Notes)
	}
	met := map[*Line]bool{}
	for table, entries := range handEntries {
		tb := c.Table(table)
		if tb == nil {
			t.Fatalf("no table %s", table)
		}
		for _, h := range entries {
			e := Entry{MessageID: h.id, Args: h.args, MemoryOrigin: h.origin}
			if table == "ipmi" {
				e = Entry{IPMI: h.id}
			}
			r := c.Classify(e)
			var flags []string
			for _, f := range r.Flags {
				flags = append(flags, string(f))
			}
			if r.Event != h.want || r.Clears != h.clears || strings.Join(flags, ",") != h.flags || r.Line == nil || r.Line.Table() != tb {
				t.Errorf("%s %v: %s clears %q flags %v by %s %s, want %s clears %q flags %q from %s", h.id, h.args, r.Event, r.Clears, flags,
					r.Mapping, r.Ref, h.want, h.clears, h.flags, table)
				continue
			}
			wantMapping := model.MappingTable
			if table == "ipmi" {
				wantMapping = model.MappingIPMI
			}
			if r.Mapping != wantMapping || r.Basis != BasisRegistry || r.Vendor != tb.Vendor || !r.Memory || r.Ref != r.Line.Ref() {
				t.Errorf("%s: mapping %s basis %s vendor %s memory %v ref %s", h.id, r.Mapping, r.Basis, r.Vendor, r.Memory, r.Ref)
			}
			if got := placeText(tb.Location, r, h.args); got != h.place {
				t.Errorf("%s %v: place %q, want %q", h.id, h.args, got, h.place)
			}
			met[r.Line] = true
		}
		for _, l := range tb.Lines {
			// A text line is met by a sentence of the test of its table.
			if !met[l] && l.Text == nil {
				t.Errorf("table %s: line %s (line %d of the file) has no hand-written entry", table, l.ID, l.Line)
			}
			if len(l.Sources) == 0 {
				t.Errorf("table %s: line %s has no source", table, l.ID)
			}
		}
	}
	for _, tb := range c.Tables() {
		if _, ok := handEntries[tb.Name]; !ok {
			t.Errorf("table %s has no hand-written entries", tb.Name)
		}
	}
}

const minimal = `format: 1
table: example
version: 1
vendor: Example
basis: documents
applies:
  message-id-prefix: EXAMPLE
key: message-id
sources:
  doc:
    title: "an example"
    url: https://example.com/doc
`

func TestLoaderChecks(t *testing.T) {
	for _, tt := range []struct {
		name, lines, want string
	}{
		{"missing source", "  - id: A1\n    event: ce\n", "needs the source"},
		{"unknown source", "  - id: A1\n    event: ce\n    source: other\n", "not among the sources"},
		{"duplicate ID", "  - id: A1\n    event: ce\n    source: doc\n  - id: A1\n    event: ue\n    source: doc\n", "given twice"},
		{"bad pattern", "  - id: A1\n    args-like: '(?P<x'\n    event: ce\n    source: doc\n", "does not compile"},
		{"oversized pattern", "  - id: A1\n    args-like: '" + strings.Repeat("a", 300) + "'\n    event: ce\n    source: doc\n", "longer than 256"},
		{"unknown event", "  - id: A1\n    event: ce-ok\n    source: doc\n", "not a common event"},
		{"unknown flag", "  - id: A1\n    event: ue\n    flags: soft-ish\n    source: doc\n", "not a flag"},
		{"unknown role", "  - id: A1\n    event: ue\n    args: dimm\n    source: doc\n", "argument role"},
		{"clears without cleared", "  - id: A1\n    event: ue\n    clears: ce\n    source: doc\n", "clears"},
	} {
		tb, errs, err := Parse([]byte(minimal + "messages:\n" + tt.lines))
		if err != nil {
			t.Errorf("%s: the table failed as a whole: %v", tt.name, err)
			continue
		}
		joined := fmt.Sprint(errs)
		if !strings.Contains(joined, tt.want) {
			t.Errorf("%s: errors %v, want %q", tt.name, errs, tt.want)
		}
		// The good lines of the table hold.
		if tt.name == "duplicate ID" && len(tb.Lines) != 1 {
			t.Errorf("%s: %d lines kept", tt.name, len(tb.Lines))
		}
	}
	for name, text := range map[string]string{
		"no format":     strings.Replace(minimal, "format: 1\n", "", 1),
		"bad basis":     strings.Replace(minimal, "basis: documents", "basis: guess", 1),
		"bad key":       strings.Replace(minimal, "key: message-id", "key: guess", 1),
		"bad version":   strings.Replace(minimal, "version: 1", "version: x", 1),
		"source no url": strings.Replace(minimal, "    url: https://example.com/doc\n", "", 1),
		"unknown key":   minimal + "colour: blue\n",
	} {
		if _, _, err := Parse([]byte(text)); err == nil {
			t.Errorf("%s: read", name)
		}
	}
}

// TestLookupOrder goes through the order of section 4.4 case by case.
func TestLookupOrder(t *testing.T) {
	user := []byte(minimal + `messages:
  - id: E100
    event: ppr-done
    args: location
    source: doc
`)
	// A user table named like a shipped one replaces a line and adds one.
	userDell := []byte(strings.Replace(strings.Replace(minimal, "table: example", "table: dell", 1),
		"applies:\n  message-id-prefix: EXAMPLE\n", "", 1) + `messages:
  - id: MEM0001
    event: ue
    flags: fatal
    args: location
    source: doc
  - id: MEM0804
    event: ppr-done
    args: location
    source: doc
  - id: MEM0805
    event: ppr-done
    source: nowhere
`)
	c, err := Shipped().WithUser(map[string][]byte{"user.yaml": user, "dell.yaml": userDell})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Notes) != 1 || !strings.Contains(c.Notes[0], "MEM0805") {
		t.Errorf("notes of the bad line: %v", c.Notes)
	}
	for _, tt := range []struct {
		name    string
		e       Entry
		want    fwevent.Name
		mapping string
		ref     string
		memory  bool
	}{
		{"user table", Entry{MessageID: "EXAMPLE.1.0.E100", Args: []string{"DIMM_A1"}}, fwevent.PPRDone, model.MappingTable, "example@1:E100", true},
		{"user line replaces the shipped one", Entry{MessageID: "IDRAC.2.9.MEM0001", Args: []string{"DIMM_A1"}}, fwevent.UE, model.MappingTable, "dell@1:MEM0001", true},
		{"user line of a key the shipped table has too", Entry{MessageID: "IDRAC.2.9.MEM0804", Args: []string{"DIMM_A1"}}, fwevent.PPRDone, model.MappingTable, "dell@1:MEM0804", true},
		{"shipped table", Entry{MessageID: "IDRAC.2.9.MEM0004", Args: []string{"DIMM_A1"}}, fwevent.Disabled, model.MappingTable, "dell@2:MEM0004", true},
		{"registry text", Entry{MessageID: "Contoso.1.0.MemFault7", Message: "x", Registry: &RegistryMessage{Text: "Uncorrectable memory error on DIMM %1", NArgs: 1}}, fwevent.UE, model.MappingRegistry, "wording@1:", true},
		{"entry text", Entry{MessageID: "Contoso.1.0.MemFault7", Message: "Post package repair of DIMM_A1 completed successfully"}, fwevent.PPRDone, model.MappingWording, "wording@1:", true},
		{"unclassified", Entry{MessageID: "Contoso.1.0.MemFault8", Message: "DIMM_A1 reported something"}, fwevent.Unclassified, model.MappingNone, "", true},
		{"a key a table holds as memory", Entry{MessageID: "IDRAC.2.9.MEM9999", Message: "self-test done"}, fwevent.Unclassified, model.MappingNone, "", true},
		{"not about memory", Entry{MessageID: "Contoso.1.0.FanFault", Message: "Fan 3 failed"}, "", model.MappingNone, "", false},
		{"names under another prefix", Entry{MessageID: "Contoso.0.1.MemoryECCCorrectable", Args: []string{"1", "A", "0", "0"}}, fwevent.CE, model.MappingTable, "openbmc@1:MemoryECCCorrectable", true},
		{"origin memory needed", Entry{MessageID: "ResourceEvent.1.5.ResourceErrorThresholdExceeded", Args: []string{"Temperature", "90"}, Message: "x"}, "", model.MappingNone, "", false},
	} {
		r := c.Classify(tt.e)
		if r.Event != tt.want || r.Mapping != tt.mapping || !strings.HasPrefix(r.Ref, tt.ref) || r.Memory != tt.memory {
			t.Errorf("%s: %s by %s %s memory %v; want %s by %s %s memory %v", tt.name, r.Event, r.Mapping, r.Ref, r.Memory, tt.want, tt.mapping, tt.ref, tt.memory)
		}
	}
	if r := c.Classify(Entry{MessageID: "IDRAC.2.9.MEM0001", Args: []string{"DIMM_A1"}}); len(r.Flags) != 1 || r.Flags[0] != fwevent.Fatal {
		t.Errorf("the user line: %+v", r)
	}
	// The shipped classifier is not changed by a user table.
	if r := Shipped().Classify(Entry{MessageID: "EXAMPLE.1.0.E100"}); r.Event != "" {
		t.Errorf("a user table reached the shipped classifier: %+v", r)
	}
	if _, err := Shipped().WithUser(map[string][]byte{"bad.yaml": []byte("format: 2\n")}); err == nil {
		t.Error("a table that cannot be read as a whole was taken")
	}
}

// TestNumberOfArgs: the registry of the BMC says how many arguments a
// message has; a line for another count holds without its roles.
func TestNumberOfArgs(t *testing.T) {
	text := minimal + `messages:
  - id: E1
    nargs: 2
    event: ue
    args: serial, location
    source: doc
  - id: E1
    nargs: 1
    event: ue
    args: location
    source: doc
  - id: E2
    nargs: 3
    event: ce
    args: location, -, -
    source: doc
`
	c, err := Shipped().WithUser(map[string][]byte{"t.yaml": []byte(text)})
	if err != nil {
		t.Fatal(err)
	}
	r := c.Classify(Entry{MessageID: "EXAMPLE.1.E1", Args: []string{"S123", "DIMM_A1"}})
	if p := PlaceOf("plain", r.Roles, []string{"S123", "DIMM_A1"}); r.Event != fwevent.UE || p.Serial != "S123" || p.Labels[0] != "DIMM_A1" {
		t.Errorf("two arguments: %+v %+v", r, p)
	}
	r = c.Classify(Entry{MessageID: "EXAMPLE.1.E1", Args: []string{"S123", "DIMM_A1"}, Registry: &RegistryMessage{NArgs: 1}})
	if p := PlaceOf("plain", r.Roles, []string{"S123"}); p.Labels[0] != "S123" || r.Line.NArgs != 1 {
		t.Errorf("the registry's count chooses the variant: %+v", r)
	}
	r = c.Classify(Entry{MessageID: "EXAMPLE.1.E2", Args: []string{"DIMM_A1"}})
	if r.Event != fwevent.CE || r.Roles != nil {
		t.Errorf("no variant for the count: %+v", r)
	}
}

func TestWordingRules(t *testing.T) {
	w := Shipped().Wording()
	for _, tt := range []struct {
		text   string
		want   fwevent.Name
		clears fwevent.Name
	}{
		{"Correctable ECC logging limit reached on DIMM_A1", fwevent.CELogLimit, ""},
		{"DIMM_A1: Correctable ECC", fwevent.CE, ""},
		{"Memory Scrub Failed on DIMM_A1", fwevent.ScrubCE, ""},
		{"Memory Device Disabled: DIMM_B1", fwevent.Disabled, ""},
		{"Uncorrectable memory error at DIMM_C1", fwevent.UE, ""},
		{"Multi-bit ECC error on DIMM_C1", fwevent.UE, ""},
		{"Post package repair failed on DIMM_A1", fwevent.PPRFailed, ""},
		{"PPR completed successfully on DIMM_A1", fwevent.PPRDone, ""},
		{"PPR scheduled for the next boot on DIMM_A1", fwevent.PPRScheduled, ""},
		{"Corrected memory error threshold exceeded on DIMM_A1", fwevent.CEThreshold, ""},
		{"Predictive failure on DIMM_A1", fwevent.PredictedFailure, ""},
		{"DIMM_A1 was mapped out", fwevent.Disabled, ""},
		{"ADDDC engaged on rank 1 of DIMM_A1", fwevent.DeviceSparing, ""},
		{"Memory mirror failover from DIMM_A1", fwevent.MirrorFailover, ""},
		{"Memory training failed on DIMM_A1", fwevent.TrainingFailure, ""},
		{"Predictive failure on DIMM_A1 cleared", fwevent.Cleared, fwevent.PredictedFailure},
		{"DIMM_A1 is enabled again, no longer disabled", fwevent.Cleared, fwevent.Disabled},
		// Two families: an uncorrected error and a repair that failed.
		{"Uncorrectable memory error; post package repair failed", fwevent.Unclassified, ""},
	} {
		ev, clears, _, _, ok := w.Read(tt.text)
		if !ok || ev != tt.want || clears != tt.clears {
			t.Errorf("%q: %s clears %q (%v), want %s clears %q", tt.text, ev, clears, ok, tt.want, tt.clears)
		}
	}
	// Texts that say none of it.
	for _, text := range []string{
		"Correctable memory error logging disabled",
		"Memory self-test completed",
		"Uncorrectable", // no word of memory
		"PPR", "Memory mirroring enabled", "ADDDCX", "training took longer than expected",
	} {
		if ev, _, _, _, ok := w.Read(text); ok && ev != fwevent.CELogLimit {
			t.Errorf("%q: read as %s", text, ev)
		}
	}
}

func TestShippedText(t *testing.T) {
	for _, tb := range Shipped().Tables() {
		b, err := ShippedText(tb.Name)
		if err != nil || !strings.Contains(string(b), "table: "+tb.Name) {
			t.Errorf("%s: %v", tb.Name, err)
		}
	}
	for _, bad := range []string{"../classify.go", "Dell", ""} {
		if _, err := ShippedText(bad); err == nil {
			t.Errorf("%q gave a file", bad)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, tb := range Shipped().Tables() {
		b, _ := ShippedText(tb.Name)
		f.Add(b)
	}
	f.Add([]byte(minimal + "messages:\n  - id: A\n    event: ce\n    source: doc\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		tb, _, err := Parse(data)
		if err != nil {
			return
		}
		for _, l := range tb.Lines {
			if !l.Event.Valid() || len(l.Sources) == 0 {
				t.Fatalf("a line that does not hold was kept: %+v", l)
			}
		}
	})
}

func FuzzClassify(f *testing.F) {
	f.Add("OpenBMC.0.1.MemoryECCCorrectable", "Memory ECC correctable error.", "1", "A", true)
	f.Add("IDRAC.2.9.MEM0001", "Multi-bit memory errors at DIMM_A1", "DIMM_A1", "", false)
	f.Add("", "PPR completed successfully on DIMM_A1", "", "", false)
	f.Add("A.B.C.D.E.F", "\xff\xfe uncorrect memory", "\x00", "", true)
	c := Shipped()
	f.Fuzz(func(t *testing.T, id, msg, a1, a2 string, origin bool) {
		r := c.Classify(Entry{MessageID: id, Message: msg, Args: []string{a1, a2}, MemoryOrigin: origin,
			Registry: &RegistryMessage{Text: msg, NArgs: len(a1) % 4}})
		if r.Memory && !r.Event.Valid() {
			t.Fatalf("about memory without a common event: %+v", r)
		}
		if r.Line != nil {
			_ = PlaceOf(r.Line.Table().Location, r.Roles, []string{a1, a2})
		}
	})
}
