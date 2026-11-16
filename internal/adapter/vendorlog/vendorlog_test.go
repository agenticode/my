package vendorlog

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/fwevent"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

var ctx = context.Background()

func collect(t *testing.T, name string, opts Options) (*adapter.Collected, adapter.Report) {
	t.Helper()
	var c adapter.Collected
	rep := NewFile(testutil.Testdata(t, "vendorlog", name), opts).Collect(ctx, &c)
	return &c, rep
}

// what lists what the reader made of the records, by firmware record.
func what(c *adapter.Collected) []string {
	var out []string
	for _, e := range c.Events {
		f := e.Firmware
		out = append(out, strings.Join([]string{f.VendorID, f.Common, e.Label, f.Origin, f.Basis, e.ErrorDT.Format("2006-01-02 15:04:05")}, " "))
	}
	for _, a := range c.Actions {
		f := a.Firmware
		out = append(out, strings.Join([]string{f.VendorID, f.Common, a.Label, f.Origin, f.Basis, a.At.Format("2006-01-02 15:04:05")}, " "))
	}
	for _, f := range c.Firmwares {
		out = append(out, strings.Join([]string{f.VendorID, f.Common, "-", f.Origin, f.Basis, f.At.Format("2006-01-02 15:04:05")}, " "))
	}
	return out
}

func TestGetSEL(t *testing.T) {
	c, rep := collect(t, "racadm-getsel.txt", Options{})
	if rep.Status != model.SourceOK || rep.Records != 8 || rep.Kind != Kind {
		t.Fatalf("report %+v", rep)
	}
	got := strings.Join(what(c), "\n")
	want := strings.Join([]string{
		"MEM0701 ce-threshold DIMM B2 tool-export registry 2026-11-03 21:04:55",
		"MEM9072 scrub-ue DIMM A10 tool-export sample 2026-11-09 15:44:41",
		"MEM0001 ue DIMM A10 tool-export sample 2026-11-09 15:44:42",
		"MEM9072 scrub-ue DIMM A10 tool-export sample 2026-11-09 15:44:44",
		"MEM0804 ppr-done DIMM B2 tool-export registry 2026-11-10 06:20:03",
		"SEL0004 log-cleared - tool-export sample 2026-10-28 08:02:11",
	}, "\n")
	if got != want {
		t.Errorf("records:\n%s\nwant\n%s", got, want)
	}
	notes := strings.Join(rep.Notes, "\n")
	for _, w := range []string{"racadm getsel output", "6 records about memory", "2 records not about memory left out", "read as UTC"} {
		if !strings.Contains(notes, w) {
			t.Errorf("notes lack %q:\n%s", w, notes)
		}
	}
	for _, e := range c.Events {
		if e.Source != "vendorlog/racadm-getsel" || !strings.HasPrefix(e.RawRef, testutil.Testdata(t, "vendorlog", "racadm-getsel.txt")+"#record/") ||
			!strings.Contains(e.Raw, "Description:") || e.Firmware.Log != "SEL" || e.Firmware.Mapping != model.MappingTable {
			t.Errorf("event %+v", e)
		}
	}
}

func TestLCLog(t *testing.T) {
	c, rep := collect(t, "racadm-lclog.txt", Options{})
	if rep.Status != model.SourceOK || rep.Records != 6 {
		t.Fatalf("report %+v", rep)
	}
	got := strings.Join(what(c), "\n")
	want := strings.Join([]string{
		"MEM7114 unclassified DIMM A3 tool-export registry 2026-11-05 10:00:00",
		"MEM0001 ue DIMM A3 sel registry 2026-11-06 12:00:00",
		"MEM0802 ppr-scheduled DIMM B1 sel registry 2026-11-03 04:11:27",
		"UEFI0279 ppr-done DIMM B1 tool-export registry 2026-11-03 04:19:40",
		"MEM0804 ppr-done DIMM B1 sel registry 2026-11-03 04:20:02",
	}, "\n")
	if got != want {
		t.Errorf("records:\n%s\nwant\n%s", got, want)
	}
	// The message of 9302 runs over two lines; the arguments of 9305 are
	// on one line.
	for _, a := range c.Actions {
		if a.Firmware.VendorID == "MEM0802" && !strings.HasSuffix(a.Firmware.Text, "Reboot system to initiate self-heal process.") {
			t.Errorf("message %q", a.Firmware.Text)
		}
	}
	for _, e := range c.Events {
		if e.Firmware.VendorID == "MEM7114" && (strings.Join(e.Firmware.Args, "|") != "0F1E2D3C|EX-PART-1|DIMM.Socket.A3|0x1234") {
			t.Errorf("arguments %q", e.Firmware.Args)
		}
	}
	// The login record is no memory record and leaves nothing behind.
	for _, e := range c.Events {
		if strings.Contains(e.Raw, "192.0.2.10") {
			t.Errorf("a record not about memory was kept: %s", e.Raw)
		}
	}
}

// TestTimeZone: --tz names the zone of the times.
func TestTimeZone(t *testing.T) {
	seoul, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		t.Skip(err)
	}
	c, rep := collect(t, "racadm-getsel.txt", Options{Location: seoul})
	if c.Events[0].ErrorDT.Format(time.RFC3339) != "2026-11-03T12:04:55Z" {
		t.Errorf("time %s", c.Events[0].ErrorDT)
	}
	if strings.Contains(strings.Join(rep.Notes, "\n"), "read as UTC") {
		t.Errorf("notes %v", rep.Notes)
	}
}

// TestPoisonedRecord: one record that cannot be read between good ones is
// counted as not understood; the records around it are read.
func TestPoisonedRecord(t *testing.T) {
	for _, tt := range []struct {
		file, bad string
	}{
		{"racadm-getsel.txt", "Record:      99\nDate/Time:   13/45/2026 99:99:99\nSource:      system\nSeverity:    Critical\n" +
			"Description: Multi-bit memory errors are detected on the memory device at location(s) DIMM_A1.\n" + strings.Repeat("-", 79) + "\n"},
		{"racadm-lclog.txt", "SeqNumber = 9399\nMessage ID = MEM0001\nSeverity = Critical\nMessage = Multi-bit memory errors\n" + strings.Repeat("-", 79) + "\n"},
	} {
		data, err := os.ReadFile(testutil.Testdata(t, "vendorlog", tt.file))
		if err != nil {
			t.Fatal(err)
		}
		good, _ := collect(t, tt.file, Options{})
		// The bad record goes after the third record.
		sep := strings.Repeat("-", 79) + "\n"
		parts := strings.SplitAfter(string(data), sep)
		poisoned := strings.Join(parts[:3], "") + tt.bad + strings.Join(parts[3:], "")
		var c adapter.Collected
		rep := NewReader(strings.NewReader(poisoned), tt.file, Options{}).Collect(ctx, &c)
		if rep.Status != model.SourceOK || rep.Unread != 1 || len(rep.UnreadRecords) != 1 {
			t.Errorf("%s: report %+v", tt.file, rep)
		}
		if strings.Join(what(&c), "\n") != strings.Join(what(good), "\n") {
			t.Errorf("%s: the records around the bad one changed:\n%s", tt.file, strings.Join(what(&c), "\n"))
		}
	}
}

func TestFormatOf(t *testing.T) {
	for _, tt := range []struct {
		text, want string
	}{
		{"Record:      1\nDate/Time:   06/26/2022 19:11:19\nSource:      system\nSeverity:    Ok\nDescription: Log cleared.\n", FormatGetSEL},
		{"SeqNumber = 286\nMessage ID = USR0005\nCategory = Audit\nTimestamp = 2017-05-15 06:25:27\n", FormatLCLog},
		{"   1 | 11/12/2026 | 03:14:07 | Memory #0x02 | Correctable ECC | Asserted\n", ""},
		{"Record: 1\nnothing else\n", ""},
		{"", ""},
	} {
		if got := FormatOf([]byte(tt.text)); got != tt.want {
			t.Errorf("%q: %q, want %q", tt.text, got, tt.want)
		}
	}
}

// TestCancel: a read stops when its context ends.
func TestCancel(t *testing.T) {
	c, cancel := context.WithCancel(ctx)
	cancel()
	var got adapter.Collected
	if rep := NewFile(testutil.Testdata(t, "vendorlog", "racadm-getsel.txt"), Options{}).Collect(c, &got); rep.Status != model.SourceError {
		t.Errorf("report %+v", rep)
	}
}

// TestSinkError: a sink that fails stops the read and is reported.
func TestSinkError(t *testing.T) {
	rep := NewFile(testutil.Testdata(t, "vendorlog", "racadm-lclog.txt"), Options{}).Collect(ctx, &failing{})
	if rep.SinkErr == nil || rep.Status != model.SourceError {
		t.Errorf("report %+v", rep)
	}
}

type failing struct{ adapter.Collected }

func (*failing) Event(*model.Event) error   { return errFull }
func (*failing) Action(*model.Action) error { return errFull }

var errFull = bytes.ErrTooLarge

func FuzzCollect(f *testing.F) {
	for _, name := range []string{"racadm-getsel.txt", "racadm-lclog.txt"} {
		data, err := os.ReadFile(testutil.Testdata(f, "vendorlog", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte("SeqNumber = 1\nMessage ID = MEM0001\nTimestamp = 2026-11-01 00:00:00\nMessage Arg 1 = Message Arg 2 = x\n"))
	f.Add([]byte("Record: 1\nDate/Time: 01/01/2026 00:00:00\nDescription: memory device at location DIMM_A1 is disabled\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		var c adapter.Collected
		rep := NewReader(bytes.NewReader(data), "fuzz", Options{}).Collect(ctx, &c)
		n := len(c.Events) + len(c.Actions) + len(c.Firmwares)
		if n > rep.Records*8+8 {
			t.Fatalf("%d records from %d", n, rep.Records)
		}
		for _, e := range c.Events {
			if e.Firmware == nil || !fwevent.Name(e.Firmware.Common).Valid() || e.Fingerprint == "" {
				t.Fatalf("event %+v", e)
			}
		}
	})
}

// TestSlotsOfSMBIOS: the iDRAC calls a module "DIMM B1" where the SMBIOS
// table of the same host calls the slot "B1"; the records land in that
// slot.
func TestSlotsOfSMBIOS(t *testing.T) {
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	as := []adapter.Adapter{
		smbios.NewDmidecodeFile(testutil.Testdata(t, "smbios", "dmidecode-t17-ddr4.txt"), smbios.Options{Now: now}),
		NewFile(testutil.Testdata(t, "vendorlog", "racadm-lclog.txt"), Options{}),
	}
	if _, err := ingest.Run(ctx, s, as, ingest.Options{Host: "h1", Now: now}); err != nil {
		t.Fatal(err)
	}
	acts, err := s.Actions(ctx, store.ActionFilter{Host: "h1", Duplicates: true})
	if err != nil {
		t.Fatal(err)
	}
	slots := map[string]bool{}
	for _, a := range acts {
		slots[a.Slot] = true
	}
	evs, err := s.EventList(ctx, store.EventFilter{Host: "h1", WithDuplicates: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		slots[e.Slot] = true
	}
	if !slots["B1"] || !slots["A3"] || slots["DIMM B1"] || slots["DIMM A3"] {
		t.Errorf("slots %v", slots)
	}
}
