package dcbrain

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzTables feeds arbitrary text to the three table readers. They must
// not panic, must account for every data line as used or skipped, and
// every row they return must lie within the limits of its columns.
func FuzzTables(f *testing.F) {
	files := fixtureFiles(f)
	for _, b := range files {
		f.Add(string(b[:min(len(b), 600)]))
	}
	f.Add("sid,memoryid,rankid,bankid,row,col,error_type,error_time\ns,1,0,0,1,8,1,0001-02-30 00:00:00\n")
	f.Add("\xef\xbb\xbfsid,failure_type,failed_time\r\n\"a,b\",1,0001-01-01 00:00:00\r\n")
	f.Add("sid,server_manufacturer,DRAM_model,DIMM_number\ns,M1,A1,24")
	f.Add("sid,memoryid,rankid,bankid,row,col,error_type,error_time\n\"unterminated,1,0,0,1,8,1,0001-01-01 00:00:00\n")
	f.Fuzz(func(t *testing.T, text string) {
		var used int64
		rep, err := ReadErrors(context.Background(), strings.NewReader(text), "f", func(r *Row) error {
			used++
			if r.SID == "" || r.MemoryID < 0 || r.MemoryID > MaxMemoryID || r.Rank < 0 || r.Rank > MaxRank || r.Bank < 0 || r.Bank > MaxBank ||
				r.Row < 0 || r.Row > MaxRow || r.Col < 0 || r.Col > MaxCol || r.Type < TypeRead || r.Type > TypeWrite ||
				r.At < 0 || r.At.Day() >= SpanDays {
				t.Fatalf("row outside the limits: %+v", *r)
			}
			rec := r.Rec()
			if int(rec.Row) != r.Row || int(rec.Col) != r.Col || int(rec.MemoryID) != r.MemoryID || Stamp(rec.At) != r.At {
				t.Fatalf("compact form %+v differs from the row %+v", rec, *r)
			}
			return nil
		})
		if err == nil && rep.Rows != used+rep.Skipped {
			t.Fatalf("%d data lines, %d used and %d skipped", rep.Rows, used, rep.Skipped)
		}
		servers, rep, err := ReadInventory(strings.NewReader(text), "f")
		if err == nil && rep.Rows != int64(len(servers))+rep.Skipped {
			t.Fatalf("inventory: %d data lines, %d used and %d skipped", rep.Rows, len(servers), rep.Skipped)
		}
		tickets, rep, err := ReadTickets(strings.NewReader(text), "f")
		if err == nil && rep.Rows != int64(len(tickets))+rep.Skipped {
			t.Fatalf("tickets: %d data lines, %d used and %d skipped", rep.Rows, len(tickets), rep.Skipped)
		}
		for _, k := range tickets {
			if k.Type < FailureUnspecified || k.Type > FailureMisc || k.At < 0 {
				t.Fatalf("ticket outside the limits: %+v", k)
			}
		}
	})
}

// packOne packs one file into a gzip-compressed tar archive, the form the
// tables of the dataset are distributed in.
func packOne(tb testing.TB, name string, data []byte) []byte {
	tb.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		tb.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		tb.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		tb.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		tb.Fatal(err)
	}
	return b.Bytes()
}

// FuzzArchive opens a dataset that is a zip archive with one member of
// arbitrary name and content, the way the dataset is downloaded. The
// member is read as a stream: whatever it holds, opening and reading it
// ends with rows or an error, and nothing is written to disk.
func FuzzArchive(f *testing.F) {
	csv := fixtureFiles(f)["mcelog.csv"]
	csv = csv[:min(len(csv), 2000)]
	packed := packOne(f, "mcelog.csv", csv)
	f.Add("data/mcelog.tar.gz", packed)
	f.Add("mcelog.csv", csv)
	f.Add("MCELOG.TGZ", packed[:len(packed)/2])
	f.Add("data/meclog.tar.gz", packOne(f, "._mcelog.csv", csv))
	f.Add("a/b/c/mcelog.tar.gz", packOne(f, "notes.txt", csv))
	f.Add("../mcelog.csv", csv)
	f.Add("mcelog.tar.gz", []byte("\x1f\x8b\x08\x00"))
	f.Add("inventory.csv", csv)

	dir := f.TempDir()
	path := filepath.Join(dir, "dramdata.zip")
	f.Fuzz(func(t *testing.T, name string, member []byte) {
		out, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(out)
		if w, err := zw.Create(name); err == nil {
			w.Write(member)
		}
		w, _ := zw.Create("readme.md")
		w.Write([]byte("x"))
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		out.Close()

		ds, err := Open(path)
		if err != nil {
			return
		}
		if !ds.Has(TableErrors) || ds.Name(TableErrors) == "" {
			t.Fatalf("a dataset without an error table was opened")
		}
		for _, table := range []Table{TableErrors, TableInventory, TableTickets} {
			rc, err := ds.OpenTable(table)
			if err != nil {
				continue
			}
			var rows int64
			rep, err := ReadErrors(context.Background(), rc, ds.Name(table), func(*Row) error { rows++; return nil })
			if err == nil && rep.Rows != rows+rep.Skipped {
				t.Errorf("%d data lines, %d used and %d skipped", rep.Rows, rows, rep.Skipped)
			}
			if cerr := rc.Close(); cerr != nil && err == nil {
				// A stream that was read to its end closes without a word
				// or says what is wrong with the archive.
				_ = cerr
			}
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("%d entries beside the archive", len(entries)-1)
		}
	})
}
