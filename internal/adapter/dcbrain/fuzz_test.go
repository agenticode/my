package dcbrain

import (
	"context"
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
