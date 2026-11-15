package sel

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

var ctx = context.Background()

func collect(t *testing.T, name string, opts Options) (*adapter.Collected, adapter.Report) {
	t.Helper()
	var c adapter.Collected
	rep := NewFile(testutil.Testdata(t, "sel", name), opts).Collect(ctx, &c)
	return &c, rep
}

// got is one ledger record in a form that is easy to compare.
type got struct {
	Ref   string // the part of the raw reference after '#'
	What  string // severity and error type of an event, kind and detail of an action
	Label string
	At    string
	Extra string // time quality when not exact, scrub type, method
}

func stamp(t time.Time, q model.TimeQuality) (string, string) {
	if q == model.TimeRelative {
		return "boot+" + t.Sub(time.Unix(0, 0)).String(), "relative"
	}
	return t.UTC().Format("2006-01-02 15:04:05"), ""
}

func summary(c *adapter.Collected) []got {
	var out []got
	short := func(ref string) string { return ref[strings.LastIndex(ref, "#")+1:] }
	for _, e := range c.Events {
		at, extra := stamp(e.ErrorDT, e.TimeQuality)
		if e.ErrorScrubType != "" {
			extra = strings.TrimSpace(extra + " " + string(e.ErrorScrubType))
		}
		out = append(out, got{short(e.RawRef), string(e.ErrorSeverity) + ": " + e.ErrorType, e.Label, at, extra})
	}
	for _, a := range c.Actions {
		at, extra := stamp(a.At, a.TimeQuality)
		if a.Method != "" {
			extra = strings.TrimSpace(extra + " by " + a.Method)
		}
		out = append(out, got{short(a.RawRef), "action " + string(a.Kind) + "/" + string(a.Result) + ": " + a.Detail, a.Label, at, extra})
	}
	return out
}

func check(t *testing.T, c *adapter.Collected, want []got) {
	t.Helper()
	have := summary(c)
	if !reflect.DeepEqual(have, want) {
		t.Errorf("records:")
		for _, g := range have {
			t.Logf("  have %+v", g)
		}
		for _, g := range want {
			t.Logf("  want %+v", g)
		}
	}
	for _, e := range c.Events {
		if e.Grade != model.GradeC || e.Source == "" || e.Fingerprint == "" || e.Raw == "" || e.Decoder != "ipmi/sel" || e.Count != 1 {
			t.Errorf("event %s: grade %q, source %q, decoder %q, count %d", e.RawRef, e.Grade, e.Source, e.Decoder, e.Count)
		}
	}
	for _, a := range c.Actions {
		if a.Source == "" || a.Fingerprint == "" || a.Raw == "" {
			t.Errorf("action %s lacks source, fingerprint or raw text", a.RawRef)
		}
	}
}

func hasNote(rep adapter.Report, part string) bool {
	return strings.Contains(strings.Join(rep.Notes, "\n"), part)
}

func wantNotes(t *testing.T, rep adapter.Report, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !hasNote(rep, p) {
			t.Errorf("notes lack %q:\n  %s", p, strings.Join(rep.Notes, "\n  "))
		}
	}
}

func TestElist(t *testing.T) {
	c, rep := collect(t, "ipmitool-elist.txt", Options{})
	check(t, c, []got{
		{"L2", "CE: correctable ECC", "DIMM_C1", "2026-10-28 01:31:40", ""},
		{"L3", "CE: correctable ECC", "DIMM_C1", "2026-11-02 14:07:55", ""},
		{"L4", "Informational: correctable ECC logging limit reached", "DIMM_C1", "2026-11-09 22:41:03", ""},
		{"L6", "UE: uncorrectable ECC", "DIMM_C1", "2026-11-12 03:14:07", ""},
		{"L10", "Informational: configuration error", "DIMM_E1", "2026-11-12 03:19:45", ""},
		{"L13", "CE: correctable ECC", "", "2026-11-13 11:00:00", ""}, // a shared sensor names no module
		{"L15", "CE: correctable ECC", "DIMM_A1", "boot+42s", "relative"},
		{"L16", "Informational: Predictive Failure Asserted", "DIMM_B1", "2026-11-13 12:00:00", ""},
		// DIMM_G1 was taken out of use and came back: no disabled module remains.
		{"L19", "Informational: memory device disabled (enabled again later)", "DIMM_G1", "2026-11-14 02:00:00", ""},
		{"L20", "Informational: memory device disabled (deasserted: in use again)", "DIMM_G1", "2026-11-14 02:45:00", ""},
		{"L8", "action disable/ok: memory device disabled", "DIMM_C1", "2026-11-12 03:19:44", ""},
		{"L11", "action sparing/ok: spare", "DIMM_F1", "2026-11-13 10:02:31", ""},
	})
	if rep.Status != model.SourceOK || rep.Records != 20 || rep.Skipped != 0 || rep.Kind != Kind {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep,
		"20 records read (ipmitool sel list)",
		"3 records of other sensors were left out",
		`1 records of kind "OEM record c1" have a vendor format`,
		`1 records of kind "OEM record e0" have a vendor format`,
		`1 records of kind "Linux kernel panic"`,
		"1 presence records were left out",
		"1 records only say that a state ended",
		"1 disable records were followed by the module coming back into use",
		`1 memory sensor records say "Predictive Failure Asserted"`,
		"1 memory records do not name a module",
		"the firmware stopped logging corrected errors 1 time(s), first on 2026-11-09 22:41 UTC",
		"the log was cleared, last on 2026-10-27 09:00 UTC",
		"16 times carry no zone and were read as UTC",
	)
	// The dates of this dump have days above 12: their order is not in doubt.
	if hasNote(rep, "month/day from day/month") {
		t.Error("the report doubts dates that are not ambiguous")
	}
	for _, e := range c.Events {
		if e.Source != "sel/list" {
			t.Errorf("source = %q", e.Source)
		}
	}
}

func TestListWithoutSensorNames(t *testing.T) {
	c, rep := collect(t, "ipmitool-list.txt", Options{})
	check(t, c, []got{
		{"L1", "CE: correctable ECC", "", "2026-11-10 06:20:11", ""},
		{"L2", "UE: uncorrectable ECC", "CPU 1 DIMM 3", "2026-11-10 06:20:12", ""},
		{"L4", "action disable/ok: memory device disabled", "CPU 1 DIMM 3", "2026-11-10 06:22:30", ""},
	})
	if rep.Status != model.SourceOK || rep.Records != 5 {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep, "2 records of other sensors", "1 records say that logging of corrected memory errors was switched off", "1 memory records do not name a module")
}

func TestVendorTextInDescription(t *testing.T) {
	c, rep := collect(t, "ipmitool-elist-supermicro.txt", Options{})
	check(t, c, []got{
		{"L1", "CE: correctable ECC", "P1-DIMMB1", "2026-11-08 20:42:15", ""},
		{"L2", "CE: correctable ECC", "P1-DIMMB1", "2026-11-08 23:10:02", ""},
		{"L3", "UE: uncorrectable ECC", "P2-DIMME2", "2026-11-09 04:55:37", ""},
	})
	if rep.Status != model.SourceOK || rep.Records != 4 {
		t.Errorf("report = %+v", rep)
	}

	c, rep = collect(t, "ipmitool-elist-dell.txt", Options{})
	check(t, c, []got{
		{"L1", "CE: correctable ECC", "DIMMA1", "2026-11-05 13:02:44", ""},
		// One record, two modules: one error, tied to neither.
		{"L2", "UE: uncorrectable ECC on one of DIMMA1, DIMMA2", "", "2026-11-05 13:40:10", ""},
		// Both modules were taken out of use.
		{"L3", "action disable/ok: memory device disabled", "DIMMA1", "2026-11-05 13:41:00", ""},
		{"L3", "action disable/ok: memory device disabled", "DIMMA2", "2026-11-05 13:41:00", ""},
	})
	wantNotes(t, rep, "1 error records name more than one module")
	if c.Actions[0].Fingerprint == c.Actions[1].Fingerprint {
		t.Error("the two actions of one record share a fingerprint")
	}
}

func TestVerbose(t *testing.T) {
	c, rep := collect(t, "ipmitool-list-v.txt", Options{})
	check(t, c, []got{
		{"L1-L11", "CE: correctable ECC", "sensor 0x53 module 3", "2026-11-12 03:14:07", ""},
		{"L13-L23", "UE: uncorrectable ECC", "", "2026-11-12 03:14:09", ""},
		{"L55-L65", "Informational: Redundancy Lost", "", "2026-11-12 03:30:00", ""},
		{"L25-L35", "action disable/ok: memory device disabled", "sensor 0x53 module 2", "2026-11-12 03:20:00", "by bios"},
		{"L67-L77", "action sparing/ok: spare", "sensor 0x53 module 5", "boot+2m3s", "relative by bios"},
	})
	if rep.Status != model.SourceOK || rep.Records != 7 {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep, "7 records read (ipmitool sel list -v)", "1 records of other sensors", `"OEM record c1"`, `"Redundancy Lost"`)
	if c.Events[0].Source != "sel/verbose" || !strings.Contains(c.Events[0].Raw, "Event Data            : 30ff03") {
		t.Errorf("first event: source %q, raw %q", c.Events[0].Source, c.Events[0].Raw)
	}
}

func TestZones(t *testing.T) {
	// The dump names a zone with one meaning: the times are converted.
	c, rep := collect(t, "ipmitool-elist-zone.txt", Options{})
	check(t, c, []got{
		{"L1", "CE: correctable ECC", "DIMM_A1", "2026-11-12 03:14:07", ""},
		{"L2", "UE: uncorrectable ECC", "DIMM_A1", "2026-11-12 03:14:09", ""},
	})
	if hasNote(rep, "read as UTC") || rep.Status != model.SourceOK {
		t.Errorf("report = %+v", rep)
	}
	fp := c.Events[0].Fingerprint

	// The caller knows better: the machine the dump was made on was set to
	// New York time whatever the dump says.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone database:", err)
	}
	c, rep = collect(t, "ipmitool-elist-zone.txt", Options{Location: ny})
	check(t, c, []got{
		{"L1", "CE: correctable ECC", "DIMM_A1", "2026-11-12 17:14:07", ""},
		{"L2", "UE: uncorrectable ECC", "DIMM_A1", "2026-11-12 17:14:09", ""},
	})
	wantNotes(t, rep, "times were read in the zone America/New_York as requested")
	// The record is the same record whatever zone its time is read in.
	if c.Events[0].Fingerprint != fp {
		t.Error("the fingerprint of a record depends on the zone option")
	}
}

func TestLocaleForms(t *testing.T) {
	// Day first, twelve-hour clock, and a zone name with several meanings.
	c, rep := collect(t, "ipmitool-elist-ampm.txt", Options{})
	check(t, c, []got{
		{"L1", "CE: correctable ECC", "DIMM_A1", "2026-11-13 15:14:07", ""},
		{"L2", "CE: correctable ECC", "DIMM_A1", "2026-11-14 00:00:01", ""},
	})
	wantNotes(t, rep, "the dates of the dump are day/month/year", `2 times are marked "CST"`)
}

func TestCSV(t *testing.T) {
	c, rep := collect(t, "ipmitool-elist.csv", Options{})
	check(t, c, []got{
		{"L1", "CE: correctable ECC", "DIMM_C1", "2026-11-12 03:14:07", ""},
		{"L2", "UE: uncorrectable ECC on one of DIMMA1, DIMMA2", "", "2026-11-12 03:14:09", ""},
		{"L4", "CE: correctable ECC", "DIMM_C1", "boot+42s", "relative"},
	})
	if rep.Status != model.SourceOK || rep.Records != 4 {
		t.Errorf("report = %+v", rep)
	}
}

func TestFreeIPMI(t *testing.T) {
	c, rep := collect(t, "ipmi-sel.txt", Options{})
	check(t, c, []got{
		{"L2", "CE: correctable ECC", "DIMM_A1", "2026-11-10 06:20:11", ""},
		{"L3", "UE: uncorrectable ECC", "DIMM_B2", "2026-11-10 06:20:12", ""},
		{"L7", "Informational: correctable ECC logging limit reached", "DIMM_C1", "2026-11-10 06:30:00", ""},
		{"L5", "action disable/ok: memory device disabled", "DIMM_B2", "2026-11-10 06:22:30", ""},
	})
	if rep.Status != model.SourcePartial || rep.Records != 6 || rep.Skipped != 1 {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep, "6 records read (ipmi-sel)", "#L6: memory sensor record has no readable time")
	if c.Events[0].Source != "sel/ipmi-sel" {
		t.Errorf("source = %q", c.Events[0].Source)
	}
}

func TestJSON(t *testing.T) {
	c, rep := collect(t, "sel.json", Options{})
	check(t, c, []got{
		{"entry 1", "CE: correctable ECC", "DIMM_C1", "2026-11-12 03:14:07", ""},
		{"entry 2", "UE: uncorrectable ECC", "sensor 0x53 module 4", "2026-11-12 03:14:09", ""},
		{"entry 3", "action disable/ok: memory device disabled", "DIMM_C1", "2026-11-12 03:20:00", ""},
		{"entry 4", "action sparing/ok: spare", "", "2026-11-13 10:02:31", "by bmc"},
	})
	if rep.Status != model.SourcePartial || rep.Records != 7 || rep.Skipped != 1 {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep, "7 records read (JSON)", "entry 6: memory sensor record has no readable time", "1 records of other sensors", `"entry that is not an object"`)
}

// rawFixture builds the binary file "ipmitool sel writeraw" writes from the
// hexadecimal fixture.
func rawFixture(t testing.TB) []byte {
	t.Helper()
	var out []byte
	for _, line := range strings.Split(string(testutil.ReadTestdata(t, "sel", "sel-writeraw.hex")), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		b, err := hex.DecodeString(strings.ReplaceAll(line, " ", ""))
		if err != nil || len(b) != 16 {
			t.Fatalf("fixture line %q: %d bytes, %v", line, len(b), err)
		}
		out = append(out, b...)
	}
	return out
}

func TestRaw(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sel.bin")
	if err := os.WriteFile(path, rawFixture(t), 0o600); err != nil {
		t.Fatal(err)
	}
	var c adapter.Collected
	rep := NewFile(path, Options{}).Collect(ctx, &c)
	check(t, &c, []got{
		{"record 1", "CE: correctable ECC", "sensor 0x53 module 3", "2026-11-12 03:14:07", ""},
		{"record 2", "UE: uncorrectable ECC", "", "2026-11-12 03:14:09", ""},
		// Record 9 says the module of record 3 is in use again.
		{"record 3", "Informational: memory device disabled (enabled again later)", "sensor 0x53 module 2", "2026-11-12 03:20:00", ""},
		{"record 5", "CE: correctable ECC", "sensor 0x53 module 3", "boot+42s", "relative"},
		{"record 9", "Informational: memory device disabled (deasserted: in use again)", "sensor 0x53 module 2", "2026-11-12 03:40:00", ""},
	})
	if rep.Status != model.SourcePartial || rep.Records != 9 || rep.Skipped != 1 {
		t.Errorf("report = %+v", rep)
	}
	wantNotes(t, rep, "9 records read (ipmitool sel writeraw)", "record 6: memory sensor record has no readable time",
		`"OEM record c1"`, `"OEM record e0"`, "1 records of other sensors")
	if c.Events[0].Raw != "01 00 02 ff 2e f5 6a 33 00 04 0c 53 6f 30 ff 03" || c.Events[0].Source != "sel/raw" {
		t.Errorf("first event: raw %q, source %q", c.Events[0].Raw, c.Events[0].Source)
	}
}

func TestReadTwiceGivesTheSameRecords(t *testing.T) {
	for _, name := range []string{"ipmitool-elist.txt", "ipmitool-list-v.txt", "sel.json", "ipmi-sel.txt"} {
		a, _ := collect(t, name, Options{})
		b, _ := collect(t, name, Options{})
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: two readings differ", name)
		}
		seen := map[string]string{}
		for _, e := range a.Events {
			if prev, dup := seen[e.Fingerprint]; dup {
				t.Errorf("%s: %s and %s share a fingerprint", name, prev, e.RawRef)
			}
			seen[e.Fingerprint] = e.RawRef
		}
		for _, x := range a.Actions {
			if prev, dup := seen[x.Fingerprint]; dup {
				t.Errorf("%s: %s and %s share a fingerprint", name, prev, x.RawRef)
			}
			seen[x.Fingerprint] = x.RawRef
		}
	}
}

func TestInputsThatAreNotADump(t *testing.T) {
	dir := t.TempDir()
	read := func(name, content string) adapter.Report {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		var c adapter.Collected
		rep := NewFile(p, Options{}).Collect(ctx, &c)
		if n := len(c.Events) + len(c.Actions); n != 0 && rep.Status != model.SourceOK && rep.Status != model.SourcePartial {
			t.Errorf("%s: %d records from a source with status %s", name, n, rep.Status)
		}
		return rep
	}
	for name, c := range map[string]struct {
		content string
		status  model.SourceStatus
		detail  string
	}{
		"empty":       {"", model.SourceEmpty, "no SEL records"},
		"blank":       {"\n\n  \n", model.SourceEmpty, "no SEL records"},
		"no-entries":  {"SEL has no entries\n", model.SourceEmpty, "no SEL records"},
		"no-memory":   {"   1 | 11/12/2026 | 03:14:07 | Fan FAN1 | Lower Critical going low | Asserted\n", model.SourceEmpty, "none of the 1 records is from a memory sensor"},
		"sel-save":    {"0x04 0x0c 0x53 0x6f 0xa0 0xff 0x01 # Memory #0x53 Correctable ECC\n", model.SourceError, `"ipmitool sel save" holds the events without their times`},
		"prose":       {"The quick brown fox\njumps over the lazy dog\n", model.SourceError, "none of the forms this reader knows"},
		"dmesg":       {"[    0.000000] Linux version 6.8.0 (builder@host)\n[   12.5] EDAC MC0: 1 CE memory read error\n", model.SourceError, "none of the forms"},
		"binary":      {"\x00\x01\x02\x03\x04", model.SourceError, "binary data that is not a file of 16-byte SEL records"},
		"binary-16":   {strings.Repeat("\x01", 16), model.SourceError, "binary data"},
		"json-number": {"[1, 2, 3]", model.SourceEmpty, "none of the 3 records is from a memory sensor"},
		"json-object": {`{"hello": "world"}`, model.SourceError, "no list of log entries found"},
		"json-broken": {`{"entries": [`, model.SourceError, "not readable as JSON"},
	} {
		rep := read(name, c.content)
		if rep.Status != c.status || !strings.Contains(rep.Detail, c.detail) {
			t.Errorf("%s: status %s, detail %q; want %s, %q", name, rep.Status, rep.Detail, c.status, c.detail)
		}
	}

	var c adapter.Collected
	if rep := NewFile(filepath.Join(dir, "missing"), Options{}).Collect(ctx, &c); rep.Status != model.SourceAbsent {
		t.Errorf("missing file: %+v", rep)
	}
	if rep := NewFile(dir, Options{}).Collect(ctx, &c); rep.Status != model.SourceError || !strings.Contains(rep.Detail, "is a directory") {
		t.Errorf("directory: %+v", rep)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if rep := NewFile(testutil.Testdata(t, "sel", "ipmitool-elist.txt"), Options{}).Collect(cancelled, &c); rep.Status != model.SourceError {
		t.Errorf("cancelled context: %+v", rep)
	}
}

// failing is a sink that refuses actions.
type failing struct {
	adapter.Collected
	err error
}

func (f *failing) Action(*model.Action) error { return f.err }

func TestSinkErrorStopsTheRead(t *testing.T) {
	boom := errors.New("ledger is full")
	f := &failing{err: boom}
	rep := NewFile(testutil.Testdata(t, "sel", "ipmitool-elist.txt"), Options{}).Collect(ctx, f)
	if rep.Status != model.SourceError || !errors.Is(rep.SinkErr, boom) || !strings.Contains(rep.Detail, "ledger rejected a record") {
		t.Errorf("report = %+v", rep)
	}
}

func TestSniff(t *testing.T) {
	for _, name := range []string{"ipmitool-elist.txt", "ipmitool-list.txt", "ipmitool-list-v.txt", "ipmitool-elist.csv", "ipmi-sel.txt", "sel.json",
		"ipmitool-elist-supermicro.txt", "ipmitool-elist-zone.txt"} {
		if !Sniff(testutil.ReadTestdata(t, "sel", name)) {
			t.Errorf("%s is not recognised", name)
		}
	}
	if !Sniff(rawFixture(t)) {
		t.Error("the binary dump is not recognised")
	}
	for name, data := range map[string]string{
		"dmesg":     "[    0.000000] Linux version 6.8.0\n",
		"dmidecode": "# dmidecode 3.5\nHandle 0x1100, DMI type 17, 92 bytes\nMemory Device\n\tSize: 32 GB\n",
		"sel save":  "0x04 0x0c 0x53 0x6f 0xa0 0xff 0x01 # Memory #0x53 Correctable ECC\n",
		"sqlite":    "SQLite format 3\x00" + strings.Repeat("\x00", 84),
		"kmsg":      "6,1234,5678901,-;EDAC MC0: 1 CE memory read error\n",
		"csv":       "1,2,3\na,b,c\n",
	} {
		if Sniff([]byte(data)) {
			t.Errorf("%s is taken for a SEL dump", name)
		}
	}
}

func TestNewReader(t *testing.T) {
	var c adapter.Collected
	a := NewReader(bytes.NewReader(testutil.ReadTestdata(t, "sel", "ipmitool-elist-supermicro.txt")), "ipmitool sel elist", Options{})
	rep := a.Collect(ctx, &c)
	if rep.Status != model.SourceOK || rep.Origin != "ipmitool sel elist" || a.Origin() != "ipmitool sel elist" || a.Kind() != Kind || len(c.Events) != 3 {
		t.Errorf("report = %+v, %d events", rep, len(c.Events))
	}
	if c.Events[0].RawRef != "ipmitool sel elist#L1" {
		t.Errorf("raw reference = %q", c.Events[0].RawRef)
	}
}
