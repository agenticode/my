package mcelog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

var ctx = context.Background()

var now = time.Date(2026, 11, 14, 9, 30, 0, 0, time.UTC)

const day = 24 * time.Hour

func read(t *testing.T, text string) (*adapter.Collected, adapter.Report) {
	t.Helper()
	var c adapter.Collected
	rep := NewReader(strings.NewReader(text), "client.txt", Options{Now: now}).Collect(ctx, &c)
	return &c, rep
}

func some(n int) model.Opt[int]      { return model.Some(n) }
func num(n uint64) model.Opt[uint64] { return model.Some(n) }

// sample is a reading as the tests write them down: the fields that are
// the same for every reading of a run are filled in by want.
func want(samples ...model.CounterSample) []model.CounterSample {
	for i := range samples {
		s := &samples[i]
		s.ObservedAt, s.Source = now, Kind
		s.MCName = "mcelog socket " + string(rune('0'+s.MC))
	}
	return samples
}

func got(c *adapter.Collected) []model.CounterSample {
	out := make([]model.CounterSample, len(c.Counters))
	for i, s := range c.Counters {
		out[i] = *s
	}
	return out
}

func compare(t *testing.T, got, want []model.CounterSample) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%d readings, want %d\n got: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("reading %d\n got: %+v\nwant: %+v", i, got[i], want[i])
		}
	}
}

func hasNote(rep adapter.Report, part string) bool {
	for _, n := range rep.Notes {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}

// The listing of the mcelog paper: two DIMMs, three corrected errors each,
// all of them within the last 24 hours.
func TestPaperSample(t *testing.T) {
	var c adapter.Collected
	path := testutil.Testdata(t, "mcelog", "client-paper.txt")
	rep := NewFile(path, Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourceOK || rep.Records != 2 || rep.Skipped != 0 || rep.Kind != Kind || rep.Origin != path {
		t.Fatalf("report = %+v", rep)
	}
	compare(t, got(&c), want(
		model.CounterSample{MC: 0, DIMMIndex: index(2, 1), SocketId: some(0), ChannelId: some(2), ChannelSlotId: some(1),
			CE: 3, UE: 0, RecentCE: num(3), RecentCEWindow: day, RecentUE: num(0), RecentUEWindow: day, RawRef: path + "#L2"},
		model.CounterSample{MC: 0, DIMMIndex: index(0, 0), SocketId: some(0), ChannelId: some(0), ChannelSlotId: some(0),
			CE: 3, UE: 0, RecentCE: num(3), RecentCEWindow: day, RecentUE: num(0), RecentUEWindow: day, RawRef: path + "#L10"},
	))
	for _, part := range []string{
		"2 DIMMs listed, 2 with errors: 6 corrected and 0 uncorrected errors since the daemon started",
		"2 of the DIMMs have no DMI name",
		"rate window of the daemon: 24h",
	} {
		if !hasNote(rep, part) {
			t.Errorf("no note with %q in %q", part, rep.Notes)
		}
	}
	if len(c.Events)+len(c.Actions)+len(c.Modules)+len(c.Hosts) != 0 {
		t.Errorf("records other than counter readings: %+v", c)
	}
}

// The sample of a host whose daemon could not tell the DIMM: a block for
// the socket, which holds every error of the socket, and one for a channel.
func TestSocketAndChannelSample(t *testing.T) {
	var c adapter.Collected
	path := testutil.Testdata(t, "mcelog", "client-x3850.txt")
	rep := NewFile(path, Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourceOK || rep.Records != 2 || rep.Skipped != 0 {
		t.Fatalf("report = %+v", rep)
	}
	compare(t, got(&c), want(
		// Eight errors on the socket, one of them on channel 0: seven that
		// belong to no block of their own. No recent count: the window of
		// the socket ages apart from those of its DIMMs.
		model.CounterSample{MC: 3, DIMMIndex: -1, SocketId: some(3), CE: 7, UE: 0, RawRef: path + "#L2"},
		model.CounterSample{MC: 3, DIMMIndex: index(0, -1), SocketId: some(3), ChannelId: some(0),
			CE: 1, UE: 0, RecentCE: num(1), RecentCEWindow: day, RecentUE: num(0), RecentUEWindow: day, RawRef: path + "#L10"},
	))
	for _, part := range []string{
		"0 DIMMs listed, 0 with errors",
		"8 errors are counted for a socket or a channel and for no DIMM",
	} {
		if !hasNote(rep, part) {
			t.Errorf("no note with %q in %q", part, rep.Notes)
		}
	}
}

// The lines the samples do not have, in the form the source of the daemon
// prints them: the DMI line (memdb.c, dump_bios) and the page table
// (page.c, dump_page_errors).
func TestDMIAndPages(t *testing.T) {
	text := "Memory errors\n" +
		"SOCKET 0 CHANNEL any DIMM any\n" +
		"corrected memory errors:\n\t40 total\n\t12 in 24h\n" +
		"uncorrected memory errors:\n\t1 total\n\t1 in 24h\n" +
		"\n" +
		"SOCKET 0 CHANNEL 0 DIMM 0\n" +
		"DMI_NAME \"CPU1_DIMM_A1\" DMI_LOCATION \"NODE 0 CHANNEL 0 DIMM 0\"\n" +
		"corrected memory errors:\n\t0 total\n\t0 in 24h\n" +
		"uncorrected memory errors:\n\t0 total\n\t0 in 24h\n" +
		"\n" +
		"SOCKET 0 CHANNEL 1 DIMM 0\n" +
		"DMI_NAME \"CPU1_DIMM_B1\" DMI_LOCATION \"NODE 0 CHANNEL 1 DIMM 0\"\n" +
		"corrected memory errors:\n\t37 total\n\t9 in 24h\n" +
		"uncorrected memory errors:\n\t1 total\n\t1 in 24h\n" +
		"\n" +
		"SOCKET 1 CHANNEL 2 DIMM 1\n" +
		"DMI_NAME \"CPU2_DIMM_G2\"\n" +
		"corrected memory errors:\n\t2 total\n\t0 in 24h\n" +
		"uncorrected memory errors:\n\t0 total\n\t0 in 24h\n" +
		"\n" +
		"SOCKET 1 CHANNEL 3 DIMM 0\n" +
		"DMI_LOCATION \"_Node1_Channel3_Dimm0\"\n" +
		"corrected memory errors:\n\t1 total\n\t1 in 24h\n" +
		"uncorrected memory errors:\n\t0 total\n\t0 in 24h\n" +
		"Per page corrected memory statistics:\n" +
		"1060bc000: total 3 seen \"1 in 24h\" offline triggered\n\n" +
		"1060c1000: total 1 seen \"1 in 24h\" online\n\n" +
		"7f3a82000: total 9 seen \"0 in 24h\" offline-failed triggered\n\n"
	c, rep := read(t, text)
	if rep.Status != model.SourceOK || rep.Records != 5 || rep.Skipped != 0 {
		t.Fatalf("report = %+v", rep)
	}
	compare(t, got(c), want(
		// 40 on the socket, 37 of them on a DIMM; 1 uncorrected, on a DIMM.
		model.CounterSample{MC: 0, DIMMIndex: -1, SocketId: some(0), CE: 3, UE: 0, RawRef: "client.txt#L2"},
		model.CounterSample{MC: 0, DIMMIndex: index(0, 0), Label: "CPU1_DIMM_A1", BankLocator: "NODE 0 CHANNEL 0 DIMM 0",
			SocketId: some(0), ChannelId: some(0), ChannelSlotId: some(0),
			RecentCE: num(0), RecentCEWindow: day, RecentUE: num(0), RecentUEWindow: day, RawRef: "client.txt#L10"},
		model.CounterSample{MC: 0, DIMMIndex: index(1, 0), Label: "CPU1_DIMM_B1", BankLocator: "NODE 0 CHANNEL 1 DIMM 0",
			SocketId: some(0), ChannelId: some(1), ChannelSlotId: some(0),
			CE: 37, UE: 1, RecentCE: num(9), RecentCEWindow: day, RecentUE: num(1), RecentUEWindow: day, RawRef: "client.txt#L19"},
		model.CounterSample{MC: 1, DIMMIndex: index(2, 1), Label: "CPU2_DIMM_G2",
			SocketId: some(1), ChannelId: some(2), ChannelSlotId: some(1),
			CE: 2, RecentCE: num(0), RecentCEWindow: day, RecentUE: num(0), RecentUEWindow: day, RawRef: "client.txt#L28"},
		model.CounterSample{MC: 1, DIMMIndex: index(3, 0), BankLocator: "_Node1_Channel3_Dimm0",
			SocketId: some(1), ChannelId: some(3), ChannelSlotId: some(0),
			CE: 1, RecentCE: num(1), RecentCEWindow: day, RecentUE: num(0), RecentUEWindow: day, RawRef: "client.txt#L37"},
	))
	for _, part := range []string{
		"4 DIMMs listed, 3 with errors: 40 corrected and 1 uncorrected errors",
		"1 of the DIMMs have no DMI name",
		"3 errors are counted for a socket or a channel and for no DIMM",
		"the page table of the daemon lists 3 pages with corrected errors, 1 offline and 1 where offlining failed; it is not read into the ledger",
	} {
		if !hasNote(rep, part) {
			t.Errorf("no note with %q in %q", part, rep.Notes)
		}
	}
}

func TestForms(t *testing.T) {
	const block = "SOCKET 0 CHANNEL 1 DIMM 0\n"
	for _, tc := range []struct {
		name, text string
		status     model.SourceStatus
		detail     string // part of the detail
		skipped    int
		note       string // part of a note
		check      func(t *testing.T, s []model.CounterSample)
	}{
		{
			name:   "without the heading and without windows",
			text:   block + "corrected memory errors:\n\t5 total\n",
			status: model.SourceOK,
			check: func(t *testing.T, s []model.CounterSample) {
				if len(s) != 1 || s[0].CE != 5 || s[0].UE != 0 || s[0].RecentCE.Valid || s[0].RecentUE.Valid {
					t.Errorf("readings = %+v", s)
				}
			},
		},
		{
			name:   "windows in other units",
			text:   block + "corrected memory errors:\n\t9 total\n\t4 in 90m\nuncorrected memory errors:\n\t2 total\n\t1 in 2d\n",
			status: model.SourceOK, note: "rate window of the daemon: 90m, 2d",
			check: func(t *testing.T, s []model.CounterSample) {
				if len(s) != 1 || s[0].RecentCEWindow != 90*time.Minute || s[0].RecentUEWindow != 48*time.Hour || s[0].RecentCE.V != 4 || s[0].RecentUE.V != 1 {
					t.Errorf("readings = %+v", s)
				}
			},
		},
		{
			// A threshold written without a unit: the daemon prints the
			// seconds followed by a NUL byte.
			name:   "window without a unit",
			text:   block + "corrected memory errors:\n\t9 total\n\t4 in 3600\x00\n",
			status: model.SourceOK, note: "rate window of the daemon: 1h",
			check: func(t *testing.T, s []model.CounterSample) {
				if len(s) != 1 || s[0].RecentCEWindow != time.Hour || s[0].RecentCE.V != 4 {
					t.Errorf("readings = %+v", s)
				}
			},
		},
		{
			name:   "carriage returns and a byte order mark",
			text:   "\xef\xbb\xbfMemory errors\r\n" + strings.ReplaceAll(block+"corrected memory errors:\n\t5 total\n\t1 in 24h\n", "\n", "\r\n"),
			status: model.SourceOK,
			check: func(t *testing.T, s []model.CounterSample) {
				if len(s) != 1 || s[0].CE != 5 || s[0].RecentCE.V != 1 {
					t.Errorf("readings = %+v", s)
				}
			},
		},
		{
			name:   "the answer as it comes off the socket",
			text:   "Memory errors\n" + block + "corrected memory errors:\n\t5 total\n\t1 in 24h\ndone\nPer page corrected memory statistics:\n1000: total 5 seen \"1 in 24h\" online\n\ndone\n",
			status: model.SourceOK, note: "lists 1 pages with corrected errors, 0 offline",
		},
		{
			name:   "a name with a quote in it",
			text:   block + "DMI_NAME \"DIMM \"A1\"\" DMI_LOCATION \"BANK 0\"\ncorrected memory errors:\n\t1 total\n",
			status: model.SourceOK,
			check: func(t *testing.T, s []model.CounterSample) {
				if len(s) != 1 || s[0].Label != `DIMM "A1"` || s[0].BankLocator != "BANK 0" {
					t.Errorf("readings = %+v", s)
				}
			},
		},
		{
			name:   "unknown lines are counted",
			text:   block + "corrected memory errors:\n\t5 total\n\tabout 3 more\nsomething else:\n\t1 total\n",
			status: model.SourcePartial, skipped: 3, note: "a line of a DIMM block that is not known",
			check: func(t *testing.T, s []model.CounterSample) {
				// "something else:" is not a count the reader knows; the
				// total below it must not be taken for corrected errors.
				if len(s) != 1 || s[0].CE != 5 {
					t.Errorf("readings = %+v", s)
				}
			},
		},
		{
			name:   "a block twice",
			text:   block + "corrected memory errors:\n\t5 total\n\n" + block + "corrected memory errors:\n\t7 total\n",
			status: model.SourcePartial, skipped: 1, note: "a second block for the DIMM of line 1",
			check: func(t *testing.T, s []model.CounterSample) {
				if len(s) != 1 || s[0].CE != 5 {
					t.Errorf("readings = %+v", s)
				}
			},
		},
		{
			name:   "numbers out of range",
			text:   "SOCKET 0 CHANNEL 70000 DIMM 0\ncorrected memory errors:\n\t5 total\n\n" + block + "corrected memory errors:\n\t99999999999 total\n\t1 in 0h\n",
			status: model.SourcePartial, skipped: 5,
			check: func(t *testing.T, s []model.CounterSample) {
				if len(s) != 1 || s[0].CE != 0 || s[0].RecentCE.Valid {
					t.Errorf("readings = %+v", s)
				}
			},
		},
		{
			name:   "a socket total smaller than its DIMMs",
			text:   "SOCKET 0 CHANNEL any DIMM any\ncorrected memory errors:\n\t2 total\n\n" + block + "corrected memory errors:\n\t5 total\n",
			status: model.SourceOK,
			check: func(t *testing.T, s []model.CounterSample) {
				// Socket tracking was switched on later than DIMM tracking.
				if len(s) != 2 || s[0].DIMMIndex != -1 || s[0].CE != 0 || s[1].CE != 5 {
					t.Errorf("readings = %+v", s)
				}
			},
		},
		{name: "no output", text: "", status: model.SourceEmpty, detail: "no output"},
		{name: "blank lines only", text: "\n\n \n", status: model.SourceEmpty, detail: "no output"},
		{name: "the heading alone", text: "Memory errors\n", status: model.SourceEmpty, detail: "the daemon lists no DIMM"},
		{
			name:   "pages without DIMMs",
			text:   "Per page corrected memory statistics:\n1000: total 5 seen \"1 in 24h\" offline\n\n",
			status: model.SourceEmpty, detail: "the daemon lists no DIMM", note: "lists 1 pages with corrected errors, 1 offline",
		},
		{name: "no daemon", text: "mcelog: client connect: No such file or directory\nmcelog: client connect: No such file or directory\n", status: model.SourceAbsent, detail: "no mcelog daemon is running"},
		{name: "daemon gone", text: "mcelog: client connect: Connection refused\n", status: model.SourceAbsent, detail: "no mcelog daemon is running"},
		{name: "not root", text: "permission denied\n", status: model.SourceUnreadable, detail: "answers root only"},
		{name: "socket closed to this user", text: "mcelog: client connect: Permission denied\n", status: model.SourceUnreadable, detail: "readable as root"},
		{name: "another failure", text: "mcelog: client read: Connection reset by peer\n", status: model.SourceError, detail: "mcelog --client failed: mcelog: client read: Connection reset by peer"},
		{name: "something else", text: "Hardware event. This is not a software error.\nMCE 0\nCPU 24 BANK 9\n", status: model.SourceError, detail: "not the output of mcelog --client: Hardware event"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, rep := read(t, tc.text)
			if rep.Status != tc.status || !strings.Contains(rep.Detail, tc.detail) || rep.Skipped != tc.skipped {
				t.Errorf("status %s, detail %q, skipped %d; want %s, %q, %d\nnotes: %q", rep.Status, rep.Detail, rep.Skipped, tc.status, tc.detail, tc.skipped, rep.Notes)
			}
			if tc.note != "" && !hasNote(rep, tc.note) {
				t.Errorf("no note with %q in %q", tc.note, rep.Notes)
			}
			if tc.check != nil {
				tc.check(t, got(c))
			}
			if rep.Status != model.SourceOK && rep.Status != model.SourcePartial && len(c.Counters) != 0 {
				t.Errorf("%d readings from a source with status %s", len(c.Counters), rep.Status)
			}
		})
	}
}

func TestSniff(t *testing.T) {
	for _, name := range []string{"client-paper.txt", "client-x3850.txt"} {
		if !Sniff(testutil.ReadTestdata(t, "mcelog", name)) {
			t.Errorf("%s is not recognised", name)
		}
	}
	for text, want := range map[string]bool{
		"1060bc000: total 3 seen \"1 in 24h\" offline triggered\n":        true,
		"  SOCKET 1 CHANNEL any DIMM any  \r\n":                           true,
		"Memory errors\n":                                                 false, // says nothing by itself
		"SOCKET 0 CHANNEL 0 DIMM\n":                                       false,
		"[  12.5] EDAC MC0: 1 CE memory read error on CPU_SrcID#0_Ha#0\n": false,
		"Hardware event. This is not a software error.\nSOCKETID 3\n":     false,
		"": false,
	} {
		if got := Sniff([]byte(text)); got != want {
			t.Errorf("Sniff(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestFileErrors(t *testing.T) {
	dir := t.TempDir()
	var c adapter.Collected
	if rep := NewFile(filepath.Join(dir, "none"), Options{}).Collect(ctx, &c); rep.Status != model.SourceAbsent {
		t.Errorf("a missing file: %+v", rep)
	}
	if rep := NewFile(dir, Options{}).Collect(ctx, &c); rep.Status != model.SourceError || rep.Detail != "is a directory" {
		t.Errorf("a directory: %+v", rep)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if rep := NewFile(testutil.Testdata(t, "mcelog", "client-paper.txt"), Options{}).Collect(cancelled, &c); rep.Status != model.SourceError {
		t.Errorf("a cancelled read: %+v", rep)
	}
	if len(c.Counters) != 0 {
		t.Errorf("readings from sources that could not be read: %+v", c.Counters)
	}
}

// refuse is a sink that takes no counter reading.
type refuse struct{ adapter.Collected }

var errFull = errors.New("ledger is full")

func (*refuse) Counter(*model.CounterSample) error { return errFull }

func TestSinkError(t *testing.T) {
	rep := NewFile(testutil.Testdata(t, "mcelog", "client-paper.txt"), Options{Now: now}).Collect(ctx, &refuse{})
	if rep.Status != model.SourceError || !errors.Is(rep.SinkErr, errFull) {
		t.Errorf("report = %+v", rep)
	}
}

// fakeProgram writes a shell script that plays mcelog.
func fakeProgram(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell")
	}
	path := filepath.Join(t.TempDir(), "mcelog")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCommand(t *testing.T) {
	sample := testutil.Testdata(t, "mcelog", "client-paper.txt")
	for _, tc := range []struct {
		name, body string
		timeout    time.Duration
		status     model.SourceStatus
		detail     string
		readings   int
	}{
		{name: "answers", body: `[ "$1" = "--client" ] || exit 9` + "\ncat '" + sample + "'\n", status: model.SourceOK, readings: 2},
		// The client reports a missing daemon on its error output and
		// still exits with success.
		{name: "no daemon", body: "echo 'mcelog: client connect: No such file or directory' >&2\nexit 0\n", status: model.SourceAbsent, detail: "no mcelog daemon is running"},
		{name: "not root", body: "echo 'permission denied'\nexit 0\n", status: model.SourceUnreadable, detail: "answers root only"},
		{name: "fails", body: "echo 'mcelog: unknown option' >&2\nexit 1\n", status: model.SourceError, detail: "mcelog --client failed: mcelog: unknown option"},
		{name: "prints nothing", body: "exit 0\n", status: model.SourceEmpty, detail: "the daemon answered and lists no DIMM"},
		{name: "hangs", body: "sleep 30\n", timeout: 300 * time.Millisecond, status: model.SourceError, detail: "did not answer within"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog := fakeProgram(t, tc.body)
			timeout := tc.timeout
			if timeout == 0 {
				timeout = 10 * time.Second // the first run of a new script is slow
			}
			var c adapter.Collected
			a := NewCommand(prog, timeout, Options{Now: now})
			if a.Origin() != prog+" --client" {
				t.Errorf("origin = %q", a.Origin())
			}
			rep := a.Collect(ctx, &c)
			if rep.Status != tc.status || !strings.Contains(rep.Detail, tc.detail) || len(c.Counters) != tc.readings {
				t.Errorf("status %s, detail %q, %d readings; want %s, %q, %d", rep.Status, rep.Detail, len(c.Counters), tc.status, tc.detail, tc.readings)
			}
		})
	}

	t.Run("not installed", func(t *testing.T) {
		var c adapter.Collected
		rep := NewCommand(filepath.Join(t.TempDir(), "mcelog"), 0, Options{}).Collect(ctx, &c)
		if rep.Status != model.SourceAbsent || rep.Detail != "mcelog is not installed" {
			t.Errorf("report = %+v", rep)
		}
		t.Setenv("PATH", t.TempDir())
		a := NewCommand("", 0, Options{})
		if rep := a.Collect(ctx, &c); rep.Status != model.SourceAbsent || a.Origin() != "mcelog --client" {
			t.Errorf("report = %+v, origin %q", rep, a.Origin())
		}
	})
}

func TestTooLarge(t *testing.T) {
	big := bytes.Repeat([]byte("SOCKET 0 CHANNEL 0 DIMM 0\n"), maxInput/26+2)
	var c adapter.Collected
	rep := NewReader(bytes.NewReader(big), "big", Options{}).Collect(ctx, &c)
	if rep.Status != model.SourceError || !strings.Contains(rep.Detail, "larger than 64 MiB") || len(c.Counters) != 0 {
		t.Errorf("status %s, detail %q, %d readings", rep.Status, rep.Detail, len(c.Counters))
	}
}
