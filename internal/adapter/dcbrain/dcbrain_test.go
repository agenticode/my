package dcbrain

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

func TestCalendar(t *testing.T) {
	sum := 0
	for _, d := range monthDays {
		sum += d
	}
	if sum != SpanDays {
		t.Fatalf("month lengths add up to %d days, SpanDays is %d", sum, SpanDays)
	}
	good := []struct {
		text string
		want Stamp
	}{
		{"0001-01-01 00:00:00", 0},
		{"0001-01-31 23:59:59", 31*daySeconds - 1},
		{"0001-02-01 00:00:00", 31 * daySeconds},
		{"0001-02-29 12:00:00", 59*daySeconds + 12*3600},
		{"0001-02-30 00:00:01", 60*daySeconds + 1},
		{"0001-04-31 06:07:08", 122*daySeconds + 6*3600 + 7*60 + 8},
		{"0001-05-29 00:00:00", 151 * daySeconds},
		{"0001-06-31 00:00:00", 182 * daySeconds},
		{"0001-08-31 23:59:59", SpanDays*daySeconds - 1},
	}
	for _, c := range good {
		got, err := ParseStamp(c.text)
		if err != nil || got != c.want {
			t.Errorf("ParseStamp(%q) = %d, %v; want %d", c.text, got, err, c.want)
		}
		if s := c.want.String(); s != c.text {
			t.Errorf("Stamp(%d).String() = %q, want %q", c.want, s, c.text)
		}
	}
	bad := []string{
		"", "0001-01-01", "0001-01-01T00:00:00", "0001-1-01 00:00:00",
		"0002-01-01 00:00:00", "2020-01-01 00:00:00", "0001-00-01 00:00:00", "0001-09-01 00:00:00",
		"0001-01-00 00:00:00", "0001-01-32 00:00:00", "0001-02-31 00:00:00", "0001-05-30 00:00:00",
		"0001-07-31 00:00:00", "0001-01-01 24:00:00", "0001-01-01 00:60:00", "0001-01-01 00:00:60",
		"0001-01-0a 00:00:00", "0001-01-01 00:00:0x", "0001/01/01 00:00:00",
	}
	for _, s := range bad {
		if got, err := ParseStamp(s); err == nil {
			t.Errorf("ParseStamp(%q) = %d, want an error", s, got)
		}
	}
	// Every second of the calendar has one text form and reads back.
	for d := 0; d < SpanDays; d++ {
		at := StampOfDay(d) + 86399
		back, err := ParseStamp(at.String())
		if err != nil || back != at || at.Day() != d {
			t.Fatalf("day %d: %q reads back as %d, %v", d, at.String(), back, err)
		}
	}
	if s := StampOfDay(SpanDays).String(); s != "day +244 00:00:00" {
		t.Errorf("a time after the calendar is written as %q", s)
	}
	if s := Stamp(-1).String(); s != "day -1 23:59:59" {
		t.Errorf("a time before the calendar is written as %q", s)
	}

	base := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	at := StampOfDay(60) + 3661
	real := at.Time(base)
	if want := time.Date(2001, 3, 2, 1, 1, 1, 0, time.UTC); !real.Equal(want) {
		t.Errorf("day 60 placed at 2001-01-01 is %s, want %s", real, want)
	}
	if back := StampOf(real, base); back != at {
		t.Errorf("StampOf = %d, want %d", back, at)
	}
	if back := StampOf(base.Add(-time.Millisecond), base); back != -1 {
		t.Errorf("StampOf just before the base = %d, want -1", back)
	}
	if d := at.Days(); d < 60.04 || d > 60.05 {
		t.Errorf("Days = %v", d)
	}
}

func readErrors(t *testing.T, text string) ([]Row, TableReport, error) {
	t.Helper()
	var rows []Row
	rep, err := ReadErrors(context.Background(), strings.NewReader(text), "mcelog.csv", func(r *Row) error {
		rows = append(rows, *r)
		return nil
	})
	return rows, rep, err
}

func TestReadErrors(t *testing.T) {
	text := "\xef\xbb\xbfsid,memoryid,rankid,bankid,row,col,error_type,error_time\r\n" +
		"srv_a,7,1,15,131071,1016,1,0001-02-30 23:59:59\r\n" +
		"\r\n" +
		"\"srv_a\",\"0\",0,0,0,0,2,\"0001-01-01 00:00:00\"\n" +
		" srv_b , 23 , 0 , 3 , 42 , 8 , 3 , 0001-08-31 00:00:01\n" +
		"srv_b,24,0,3,42,8,1,0001-03-01 00:00:00\n" + // memoryid out of range
		"srv_b,1,0,3,42,8,4,0001-03-01 00:00:00\n" + // unknown error type
		"srv_b,1,0,3,42,8,0,0001-03-01 00:00:00\n" + // unknown error type
		"srv_b,1,0,3,x42,8,1,0001-03-01 00:00:00\n" + // row is not a number
		"srv_b,1,0,3,42,8,1,0001-02-31 00:00:00\n" + // no such day
		"srv_b,1,0,3,42,8,1\n" + // too few fields
		",1,0,3,42,8,1,0001-03-01 00:00:00\n" + // no server
		"srv_b,1,0,3,-1,8,1,0001-03-01 00:00:00\n" + // negative
		"srv_b,1,0,3,42,99999999999,1,0001-03-01 00:00:00\n" + // column too large
		"srv_c,2,0,1,5,16,1,0001-05-29 10:00:00" // last line without a newline
	rows, rep, err := readErrors(t, text)
	if err != nil {
		t.Fatal(err)
	}
	want := []Row{
		{Line: 2, SID: "srv_a", MemoryID: 7, Rank: 1, Bank: 15, Row: 131071, Col: 1016, Type: TypeRead, At: 60*daySeconds + 86399},
		{Line: 4, SID: "srv_a", MemoryID: 0, Type: TypeScrub, At: 0},
		{Line: 5, SID: "srv_b", MemoryID: 23, Bank: 3, Row: 42, Col: 8, Type: TypeWrite, At: 243*daySeconds + 1},
		{Line: 15, SID: "srv_c", MemoryID: 2, Bank: 1, Row: 5, Col: 16, Type: TypeRead, At: 151*daySeconds + 36000},
	}
	if len(rows) != len(want) {
		t.Fatalf("read %d rows, want %d: %+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}
	if rep.Rows != 13 || rep.Skipped != 9 {
		t.Errorf("report: %d rows, %d skipped; want 13 and 9", rep.Rows, rep.Skipped)
	}
	notes := strings.Join(rep.Notes, "\n")
	for _, s := range []string{
		"skipped mcelog.csv:6: memoryid above 23",
		"skipped 2 records: error_type other than 1, 2 or 3 (first: mcelog.csv:7)",
		"skipped 2 records: row is not a whole number (first: mcelog.csv:9)",
		"error_time is not a time of the dataset calendar",
		"7 fields where the header has 8",
		"no server ID",
		"col above 65535",
	} {
		if !strings.Contains(notes, s) {
			t.Errorf("notes lack %q:\n%s", s, notes)
		}
	}
}

func TestReadErrorsHeader(t *testing.T) {
	// Columns are found by name, in any order and any case.
	rows, _, err := readErrors(t, "ERROR_TIME,col,row,bankid,rankid,memoryid,sid,error_type,extra\n0001-01-02 00:00:00,8,9,3,1,5,s1,2,x\n")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v, err %v", rows, err)
	}
	if r := rows[0]; r.SID != "s1" || r.MemoryID != 5 || r.Rank != 1 || r.Bank != 3 || r.Row != 9 || r.Col != 8 || r.Type != TypeScrub || r.At != daySeconds {
		t.Errorf("row = %+v", r)
	}
	for _, c := range []struct{ text, want string }{
		{"", "the file is empty"},
		{"\n\n", "the file is empty"},
		{"sid,memoryid,rankid,bankid,row,col,error_time\n", `no column "error_type"`},
		{"a,b,c\n1,2,3\n", `no column "sid"`},
	} {
		_, _, err := readErrors(t, c.text)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %v, want one that mentions %q", c.text, err, c.want)
		}
	}
}

func TestReadErrorsStops(t *testing.T) {
	text := "sid,memoryid,rankid,bankid,row,col,error_type,error_time\n" + strings.Repeat("s,1,0,0,1,8,1,0001-01-01 00:00:00\n", 10)
	stop := errors.New("stop")
	n := 0
	_, err := ReadErrors(context.Background(), strings.NewReader(text), "m", func(*Row) error {
		n++
		if n == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || n != 3 {
		t.Errorf("err %v after %d rows", err, n)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadErrors(ctx, strings.NewReader(text), "m", func(*Row) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled read: %v", err)
	}
	// A stream that fails in the middle is an error, not a short table.
	broken := io.MultiReader(strings.NewReader(text), errReader{})
	if _, err := ReadErrors(context.Background(), broken, "m", func(*Row) error { return nil }); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("broken stream: %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestLongLine(t *testing.T) {
	text := "sid,memoryid,rankid,bankid,row,col,error_type,error_time\n" +
		"s,1,0,0,1,8,1,0001-01-01 00:00:00\n" +
		strings.Repeat("x", maxLine+10) + "\n" +
		"s,1,0,0,2,8,1,0001-01-01 00:00:00\n"
	rows, rep, err := readErrors(t, text)
	if err != nil || len(rows) != 2 || rep.Skipped != 1 || rep.Rows != 3 {
		t.Fatalf("rows %d, report %+v, err %v", len(rows), rep, err)
	}
	if rows[1].Line != 4 {
		t.Errorf("line after the long line = %d, want 4", rows[1].Line)
	}
}

func TestInventoryAndTickets(t *testing.T) {
	inv := "sid,DRAM_model,DIMM_num,server_manufacturter\ns1,A1,12,M2\ns2,C2,many,M1\ns3,B3,24,M4\n"
	servers, rep, err := ReadInventory(strings.NewReader(inv), "inventory.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 || servers[0] != (Server{Line: 2, SID: "s1", Manufacturer: "M2", DRAMModel: "A1", DIMMs: 12}) || servers[1].SID != "s3" {
		t.Errorf("servers = %+v", servers)
	}
	if rep.Rows != 3 || rep.Skipped != 1 || !strings.Contains(strings.Join(rep.Notes, " "), "DIMM_number is not a whole number") {
		t.Errorf("report = %+v", rep)
	}

	tk := "sid,failure_type,failure_time\ns1,1,0001-03-04 05:06:07\ns2,0,0001-01-01 00:00:00\ns3,4,0001-01-01 00:00:00\ns4,2,yesterday\n"
	tickets, rep, err := ReadTickets(strings.NewReader(tk), "trouble_tickets.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 2 || tickets[0].Type != FailureUE || tickets[0].At.String() != "0001-03-04 05:06:07" || tickets[1].Type != FailureUnspecified {
		t.Errorf("tickets = %+v", tickets)
	}
	if rep.Skipped != 2 {
		t.Errorf("report = %+v", rep)
	}
	if _, _, err := ReadTickets(strings.NewReader("sid,failure_type\n"), "t"); err == nil {
		t.Error("a ticket table without a time column was accepted")
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	rows, servers, tickets := fixture()
	files := fixtureFiles(t)
	got, rep, err := readErrors(t, string(files["mcelog.csv"]))
	if err != nil || rep.Skipped != 0 {
		t.Fatalf("report %+v, err %v", rep, err)
	}
	if len(got) != len(rows) {
		t.Fatalf("%d rows read, %d written", len(got), len(rows))
	}
	for i := range rows {
		if got[i] != rows[i] {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], rows[i])
		}
	}
	s, _, err := ReadInventory(bytes.NewReader(files["inventory.csv"]), "inventory.csv")
	if err != nil || len(s) != len(servers) {
		t.Fatalf("inventory: %d servers, err %v", len(s), err)
	}
	k, _, err := ReadTickets(bytes.NewReader(files["trouble_tickets.csv"]), "trouble_tickets.csv")
	if err != nil || len(k) != len(tickets) {
		t.Fatalf("tickets: %d, err %v", len(k), err)
	}
	for i := range tickets {
		if k[i].SID != tickets[i].SID || k[i].Type != tickets[i].Type || k[i].At != tickets[i].At {
			t.Errorf("ticket %d = %+v, want %+v", i, k[i], tickets[i])
		}
	}
}

// tarGz packs one file into a gzip-compressed tar archive, preceded by the
// kind of extra member macOS adds.
func tarGz(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		data []byte
	}{{"data/._" + name, []byte("resource fork")}, {"data/" + name, data}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readAll(t *testing.T, d *Dataset, tb Table) []byte {
	t.Helper()
	rc, err := d.OpenTable(tb)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOpen(t *testing.T) {
	files := fixtureFiles(t)

	// A directory of plain files: the committed fixture.
	d, err := Open(testutil.Testdata(t, "dcbrain"))
	if err != nil {
		t.Fatal(err)
	}
	if !d.Has(TableErrors) || !d.Has(TableInventory) || !d.Has(TableTickets) || d.Name(TableErrors) != "mcelog.csv" {
		t.Errorf("fixture directory: %+v", d)
	}
	if got := readAll(t, d, TableErrors); !bytes.Equal(got, files["mcelog.csv"]) {
		t.Error("the error table of the fixture reads differently")
	}

	// A directory of archives, one level down, with the ticket table under
	// the name the dataset description uses.
	dir := t.TempDir()
	sub := filepath.Join(dir, "dramdata")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p string, b []byte) {
		t.Helper()
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(sub, "mcelog.tar.gz"), tarGz(t, "mcelog.csv", files["mcelog.csv"]))
	write(filepath.Join(sub, "trouble_ticket.tar.gz"), tarGz(t, "trouble_tickets.csv", files["trouble_tickets.csv"]))
	d, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d.Has(TableInventory) || d.Name(TableInventory) != "" {
		t.Error("an inventory table was found where there is none")
	}
	if _, err := d.OpenTable(TableInventory); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("opening a missing table: %v", err)
	}
	if got := readAll(t, d, TableErrors); !bytes.Equal(got, files["mcelog.csv"]) {
		t.Error("the error table reads differently through tar.gz")
	}
	if got := readAll(t, d, TableTickets); !bytes.Equal(got, files["trouble_tickets.csv"]) {
		t.Error("the ticket table reads differently through tar.gz")
	}
	// Plain text next to the archive is preferred.
	write(filepath.Join(sub, "mcelog.csv"), []byte("sid,memoryid,rankid,bankid,row,col,error_type,error_time\n"))
	if d, err = Open(dir); err != nil || d.Name(TableErrors) != "dramdata/mcelog.csv" {
		t.Errorf("plain text next to an archive: %v, %v", d, err)
	}

	// The zip archive the dataset is distributed as: archives in an archive.
	zipPath := filepath.Join(t.TempDir(), "dramdata.zip")
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for name, data := range map[string][]byte{
		"dramdata/mcelog.tar.gz":          tarGz(t, "mcelog.csv", files["mcelog.csv"]),
		"dramdata/inventory.tar.gz":       tarGz(t, "inventory.csv", files["inventory.csv"]),
		"dramdata/trouble_tickets.tar.gz": tarGz(t, "trouble_tickets.csv", files["trouble_tickets.csv"]),
		"dramdata/readme.md":              []byte("not a table"),
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	write(zipPath, zbuf.Bytes())
	d, err = Open(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if d.Name(TableErrors) != "dramdata.zip/dramdata/mcelog.tar.gz" {
		t.Errorf("name in a zip archive: %q", d.Name(TableErrors))
	}
	for tb, name := range map[Table]string{TableErrors: "mcelog.csv", TableInventory: "inventory.csv", TableTickets: "trouble_tickets.csv"} {
		if got := readAll(t, d, tb); !bytes.Equal(got, files[name]) {
			t.Errorf("%s reads differently through the zip archive", name)
		}
	}

	// What is not a dataset.
	if _, err := Open(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing path: %v", err)
	}
	if _, err := Open(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no error table") {
		t.Errorf("empty directory: %v", err)
	}
	notZip := filepath.Join(t.TempDir(), "x.zip")
	write(notZip, []byte("plain text"))
	if _, err := Open(notZip); err == nil || !strings.Contains(err.Error(), "neither a directory nor a zip archive") {
		t.Errorf("a file that is no archive: %v", err)
	}
	// An archive without a table in it, and one that is cut off.
	bad := t.TempDir()
	write(filepath.Join(bad, "mcelog.tar.gz"), tarGz(t, "notes.txt", []byte("x")))
	d, err = Open(bad)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.OpenTable(TableErrors); err == nil || !strings.Contains(err.Error(), "holds no .csv file") {
		t.Errorf("archive without a table: %v", err)
	}
	whole := tarGz(t, "mcelog.csv", bytes.Repeat(files["mcelog.csv"], 20))
	write(filepath.Join(bad, "mcelog.tar.gz"), whole[:len(whole)/2])
	d, _ = Open(bad)
	rc, err := d.OpenTable(TableErrors)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ReadErrors(context.Background(), rc, d.Name(TableErrors), func(*Row) error { return nil })
	rc.Close()
	if err == nil {
		t.Error("a cut-off archive was read without an error")
	}
}

func TestMerge(t *testing.T) {
	rec := func(at uint32, mem, bank uint8, row uint32, col uint16, typ ErrorType, line uint32) Rec {
		return Rec{At: at, Row: row, Count: 1, Line: line, Col: col, MemoryID: mem, Bank: bank, Type: typ}
	}
	in := []Rec{
		rec(100, 1, 2, 7, 8, TypeRead, 5),
		rec(100, 1, 2, 7, 8, TypeRead, 3),
		rec(100, 1, 2, 7, 8, TypeScrub, 4), // other error type
		rec(100, 1, 2, 7, 16, TypeRead, 6), // other column
		rec(101, 1, 2, 7, 8, TypeRead, 7),  // one second later
		rec(40, 2, 2, 7, 8, TypeRead, 9),   // other DIMM, earlier
		rec(100, 1, 2, 7, 8, TypeRead, 8),
	}
	out := Merge(append([]Rec(nil), in...), 1)
	if len(out) != 5 {
		t.Fatalf("%d records after merging by second, want 5: %+v", len(out), out)
	}
	if out[0].At != 40 || out[len(out)-1].At != 101 {
		t.Errorf("not in time order: %+v", out)
	}
	total := uint32(0)
	for _, c := range out {
		total += c.Count
		if c.At == 100 && c.Col == 8 && c.Type == TypeRead {
			if c.Count != 3 || c.Line != 3 {
				t.Errorf("merged record = %+v, want count 3 and line 3", c)
			}
		}
	}
	if total != uint32(len(in)) {
		t.Errorf("counts add up to %d, want %d", total, len(in))
	}

	// A bucket of a minute also merges the error one second later, and
	// keeps the earliest time.
	out = Merge(append([]Rec(nil), in...), 60)
	if len(out) != 4 {
		t.Fatalf("%d records after merging by minute, want 4: %+v", len(out), out)
	}
	for _, c := range out {
		if c.MemoryID == 1 && c.Col == 8 && c.Type == TypeRead && (c.Count != 4 || c.At != 100) {
			t.Errorf("merged record = %+v, want count 4 at 100", c)
		}
	}
	// Times on both sides of a bucket boundary stay apart.
	out = Merge([]Rec{rec(59, 1, 0, 1, 8, TypeRead, 1), rec(60, 1, 0, 1, 8, TypeRead, 2)}, 60)
	if len(out) != 2 {
		t.Errorf("records across a bucket boundary were merged: %+v", out)
	}
	// Counts that would overflow are not merged.
	big := rec(5, 1, 0, 1, 8, TypeRead, 1)
	big.Count = 1<<32 - 1
	out = Merge([]Rec{big, rec(5, 1, 0, 1, 8, TypeRead, 2)}, 1)
	if len(out) != 2 {
		t.Errorf("an overflowing count was merged: %+v", out)
	}
	if out := Merge(nil, 0); len(out) != 0 {
		t.Errorf("Merge(nil) = %+v", out)
	}
}

func TestFill(t *testing.T) {
	base := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	row := Row{Line: 77, SID: "s9", MemoryID: 17, Rank: 1, Bank: 15, Row: 0x1a2f, Col: 0x238, Type: TypeScrub, At: StampOfDay(60) + 5}
	rec := row.Rec()
	rec.Count = 4
	var e model.Event
	e.DeviceId = model.Some(3) // Fill must reset what was there
	Fill(&e, "s9", &rec, base)
	if e.Host != "s9" || e.Kind != model.KindError || e.ErrorSeverity != model.SevCE || e.ErrorScrubType != model.ScrubPatrol {
		t.Errorf("event = %+v", e)
	}
	if !e.ErrorDT.Equal(time.Date(2001, 3, 2, 0, 0, 5, 0, time.UTC)) || e.TimeQuality != model.TimeExact {
		t.Errorf("time = %s (%s)", e.ErrorDT, e.TimeQuality)
	}
	if e.Rank != model.Some(1) || e.Bank != model.Some(15) || e.Row != model.Some(0x1a2f) || e.Column != model.Some(0x238) {
		t.Errorf("location = rank %v bank %v row %v col %v", e.Rank, e.Bank, e.Row, e.Column)
	}
	if e.DeviceId.Valid || e.BankGroup.Valid || e.OsPhysicalAddress.Valid || e.ErrorVector != "" {
		t.Errorf("fields the dataset does not have are set: %+v", e)
	}
	if e.SocketId != model.Some(1) || e.ChannelId != model.Some(2) || e.ChannelSlotId != model.Some(1) || e.Label != "DIMM17" {
		t.Errorf("place = socket %v channel %v position %v label %q", e.SocketId, e.ChannelId, e.ChannelSlotId, e.Label)
	}
	if e.Grade != model.GradeA || e.Count != 4 || e.Errors() != 4 || e.Source != Source || e.Decoder != Decoder || e.ErrorType != "memory scrubbing error" {
		t.Errorf("event = %+v", e)
	}
	for typ, want := range map[ErrorType]model.ScrubType{TypeRead: model.ScrubDemand, TypeWrite: model.ScrubDemand, TypeScrub: model.ScrubPatrol} {
		rec.Type = typ
		Fill(&e, "s9", &rec, base)
		if e.ErrorScrubType != want {
			t.Errorf("error type %d: scrub type %q, want %q", typ, e.ErrorScrubType, want)
		}
	}
	for id, want := range map[int][3]int{0: {0, 0, 0}, 1: {0, 0, 1}, 11: {0, 5, 1}, 12: {1, 0, 0}, 23: {1, 5, 1}} {
		s, c, p := Topology(id)
		if [3]int{s, c, p} != want {
			t.Errorf("Topology(%d) = %d %d %d, want %v", id, s, c, p, want)
		}
	}
	rec.Type = TypeScrub
	if got := rawText("s9", &rec); got != "s9,17,1,15,6703,568,2,0001-02-30 00:00:05" {
		t.Errorf("raw text = %q", got)
	}
}

func collect(t *testing.T, sid string, opts Options) (*adapter.Collected, adapter.Report) {
	t.Helper()
	d, err := Open(testutil.Testdata(t, "dcbrain"))
	if err != nil {
		t.Fatal(err)
	}
	var sink adapter.Collected
	rep := NewServer(d, sid, opts).Collect(context.Background(), &sink)
	return &sink, rep
}

func TestAdapter(t *testing.T) {
	sink, rep := collect(t, "s0002", Options{})
	if rep.Status != model.SourceOK || rep.Kind != Kind || rep.HostHint != "s0002" || !strings.HasSuffix(rep.Origin, "#s0002") {
		t.Fatalf("report = %+v", rep)
	}
	rows, _, _ := fixture()
	want := 0
	for _, r := range rows {
		if r.SID == "s0002" {
			want++
		}
	}
	errs := 0
	seen := map[string]bool{}
	for _, e := range sink.Events {
		errs += e.Errors()
		if seen[e.Fingerprint] {
			t.Errorf("fingerprint %s twice", e.Fingerprint)
		}
		seen[e.Fingerprint] = true
		if e.Host != "" || e.Grade != model.GradeA || !strings.HasPrefix(e.RawRef, "mcelog.csv:") || !strings.HasPrefix(e.Raw, "s0002,0,1,9,") {
			t.Errorf("event = %+v", e)
		}
	}
	if rep.Records != want || errs != want {
		t.Errorf("%d records, %d errors in events; the fixture has %d rows of the server", rep.Records, errs, want)
	}
	for i := 1; i < len(sink.Events); i++ {
		if sink.Events[i].ErrorDT.Before(sink.Events[i-1].ErrorDT) {
			t.Fatal("events are not in time order")
		}
	}
	if len(sink.Hosts) != 1 || sink.Hosts[0].SystemVendor != "M2" {
		t.Errorf("hosts = %+v", sink.Hosts)
	}
	notes := strings.Join(rep.Notes, "\n")
	for _, s := range []string{
		"inventory: 24 DIMMs of DRAM model B1, server manufacturer M2",
		"failure ticket at 0001-03-11 03:14:07 on the dataset calendar (failure_type 1, uncorrectable error)",
		"first day placed at 2001-01-01",
	} {
		if !strings.Contains(notes, s) {
			t.Errorf("notes lack %q:\n%s", s, notes)
		}
	}

	// Rows that are equal in every column become one record with a count.
	sink, rep = collect(t, "s0008", Options{Base: time.Date(2010, 6, 1, 0, 0, 0, 0, time.FixedZone("x", 3600))})
	if rep.Records != 9 || len(sink.Events) != 5 {
		t.Fatalf("s0008: %d records, %d events; want 9 and 5", rep.Records, len(sink.Events))
	}
	found := false
	for _, e := range sink.Events {
		if e.Count == 5 {
			found = true
			// Day 122 of a time line that starts on 2010-05-31 23:00 UTC.
			if want := time.Date(2010, 9, 30, 23, 0, 0, 0, time.UTC).Add(12 * time.Hour); !e.ErrorDT.Equal(want) {
				t.Errorf("merged record at %s, want %s", e.ErrorDT, want)
			}
			if e.Raw != "s0008,10,0,0,66,16,1,0001-04-31 12:00:00" {
				t.Errorf("raw = %q", e.Raw)
			}
		}
	}
	if !found {
		t.Error("no record with count 5")
	}

	// A server the dataset does not have.
	sink, rep = collect(t, "nobody", Options{})
	if rep.Status != model.SourceEmpty || len(sink.Events) != 0 || !strings.Contains(strings.Join(rep.Notes, "\n"), "does not list the server") {
		t.Errorf("unknown server: %+v", rep)
	}

	// More rows than the limit: the report says what was left out.
	sink, rep = collect(t, "s0005", Options{MaxRows: 50})
	if rep.Status != model.SourcePartial || rep.Records != 210 || rep.Skipped != 160 || !strings.Contains(rep.Detail, "210 rows; the first 50") {
		t.Errorf("limited read: %+v", rep)
	}
	n := 0
	for _, e := range sink.Events {
		n += e.Errors()
	}
	if n != 50 {
		t.Errorf("%d errors read with a limit of 50", n)
	}
}

func TestAdapterFailures(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mcelog.csv"), []byte("sid,memoryid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sink adapter.Collected
	rep := NewServer(d, "s1", Options{}).Collect(context.Background(), &sink)
	if rep.Status != model.SourceError || !strings.Contains(rep.Detail, `no column "rankid"`) {
		t.Errorf("table without columns: %+v", rep)
	}
	// The table disappears between Open and Collect.
	os.Remove(filepath.Join(dir, "mcelog.csv"))
	rep = NewServer(d, "s1", Options{}).Collect(context.Background(), &sink)
	if rep.Status != model.SourceAbsent {
		t.Errorf("missing table: %+v", rep)
	}
	// A sink that refuses a record stops the read.
	d, _ = Open(testutil.Testdata(t, "dcbrain"))
	rep = NewServer(d, "s0001", Options{}).Collect(context.Background(), &refusing{})
	if rep.SinkErr == nil || rep.Status != model.SourceError {
		t.Errorf("refusing sink: %+v", rep)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep = NewServer(d, "s0001", Options{}).Collect(ctx, &sink)
	if rep.Status != model.SourceError || rep.Detail != "reading was interrupted" {
		t.Errorf("cancelled read: %+v", rep)
	}
}

type refusing struct{ adapter.Collected }

func (*refusing) Event(*model.Event) error { return errors.New("ledger full") }

func TestRecSize(t *testing.T) {
	// The evaluation holds one Rec per row of a server; the size matters.
	if n := unsafe.Sizeof(Rec{}); n != 24 {
		t.Errorf("a Rec takes %d bytes, want 24", n)
	}
}

func BenchmarkReadErrors(b *testing.B) {
	var buf bytes.Buffer
	buf.WriteString("sid,memoryid,rankid,bankid,row,col,error_type,error_time\n")
	const rows = 200_000
	for i := 0; i < rows; i++ {
		fmt.Fprintf(&buf, "Server_%d,%d,%d,%d,%d,%d,%d,%s\n", i%3000, i%24, i%2, i%16, (i*7919)%(1<<17), (i%128)*8, 1+i%2, Stamp(i*97%(SpanDays*daySeconds)))
	}
	data := buf.Bytes()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n := 0
		_, err := ReadErrors(context.Background(), bytes.NewReader(data), "bench", func(*Row) error { n++; return nil })
		if err != nil || n != rows {
			b.Fatalf("%d rows, %v", n, err)
		}
	}
}
