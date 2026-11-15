package klog

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// What goes into the queue comes out as it went in and in the same order,
// whether a record waited in memory or in the file, and nothing is left in
// the directory.
func TestWaitingKeepsRecordsAsTheyAre(t *testing.T) {
	dir := t.TempDir()
	q := waiting{dir: dir}
	var in []pending
	for i := range maxWaitingInMemory + 1500 {
		pd := pending{mono: time.Duration(i) * time.Millisecond, hasMono: i%7 != 0}
		switch i % 3 {
		case 0:
			pd.ev = &model.Event{
				Kind: model.KindError, ErrorSeverity: model.SevCE, ErrorScrubType: model.ScrubPatrol, Count: 1 + i%4,
				SocketId: model.Some(i % 2), ChannelId: model.Some(0), Row: model.Some(i), Column: model.Opt[int]{},
				OsPhysicalAddress: model.Some(uint64(i) << 12), Syndrome: model.Some(^uint64(0)),
				Label: fmt.Sprintf("DIMM_%d", i), Source: "klog/edac", RawRef: fmt.Sprintf("dmesg#L%d", i), Raw: "EDAC MC0: 1 CE \xff",
				Fingerprint: fmt.Sprint(i), WindowStart: time.Date(2026, 11, 13, 9, 30, 0, 123456000, time.UTC),
			}
		case 1:
			pd.act = &model.Action{Kind: model.ActionPageOffline, Method: "soft", Result: model.ResultOK,
				OsPhysicalAddress: model.Some(uint64(i)), Source: "klog", RawRef: fmt.Sprintf("dmesg#L%d", i), Count: 1}
		default:
			pd.gpu = &model.GPUMem{Kind: model.GPUXID, XID: model.Some(48), PCIBus: "0000:3b:00.0", Source: "klog/xid", Raw: "NVRM: Xid"}
		}
		in = append(in, pd)
		if err := q.add(pd); err != nil {
			t.Fatal(err)
		}
	}
	if len(q.mem) != maxWaitingInMemory || q.count != 1500 || q.f == nil {
		t.Fatalf("%d records in memory, %d in the file", len(q.mem), q.count)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the file of the queue has a name: %v", entries)
	}
	i := 0
	err := q.each(func(pd pending) error {
		if !reflect.DeepEqual(pd, in[i]) {
			t.Fatalf("record %d came out as\n%+v\nand went in as\n%+v", i, pd, in[i])
		}
		i++
		return nil
	})
	if err != nil || i != len(in) {
		t.Fatalf("%d of %d records came out: %v", i, len(in), err)
	}
	if q.mem != nil || q.f != nil || q.count != 0 {
		t.Errorf("the queue is not empty after it was read: %+v", q)
	}
	// It can be used again, for the next boot.
	if err := q.add(in[0]); err != nil || len(q.mem) != 1 {
		t.Errorf("after reset: %v, %d records", err, len(q.mem))
	}
}

// A log of one boot with more error lines than wait in memory: the lines
// carry seconds since boot only, so every record waits for the end of the
// input. The records are the ones a short log gives, each at its time,
// and no file is left behind.
func TestBootWithMoreRecordsThanWaitInMemory(t *testing.T) {
	const n = maxWaitingInMemory*2 + 37
	var b strings.Builder
	b.WriteString("[    0.000000] Linux version 6.1.0 (builder@example) #1 SMP\n")
	for i := range n {
		fmt.Fprintf(&b, "[%5d.%06d] EDAC MC0: 1 CE memory read error on DIMM_A1 (channel:0 slot:0 page:0x%x offset:0x40 grain:32 syndrome:0x0)\n",
			100+i, i%1000000, 0x1000+i)
	}
	b.WriteString("[99999.000000] end of log\n")
	dir := t.TempDir()
	captured := utc(2026, 11, 13, 0, 0, 0)
	c, rep := collectText(t, b.String(), Options{CapturedAt: captured, TempDir: dir})
	if rep.Status != model.SourceOK || len(c.Events) != n || len(c.Boots) != 1 {
		t.Fatalf("report = %+v, %d events, %d boots", rep, len(c.Events), len(c.Boots))
	}
	start := captured.Add(-99999 * time.Second)
	if !c.Boots[0].StartedAt.Equal(start) {
		t.Fatalf("boot at %v, want %v", c.Boots[0].StartedAt, start)
	}
	seen := map[string]bool{}
	for i, e := range c.Events {
		want := start.Add(time.Duration(100+i)*time.Second + time.Duration(i%1000000)*time.Microsecond)
		if !e.ErrorDT.Equal(want) || e.TimeQuality != model.TimeEstimated || e.OsPhysicalAddress.V != uint64(0x1000+i)<<12|0x40 ||
			e.RawRef != fmt.Sprintf("test#L%d", i+2) || e.Label != "DIMM_A1" || seen[e.Fingerprint] {
			t.Fatalf("event %d: %+v, want time %v", i, e, want)
		}
		seen[e.Fingerprint] = true
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d entries left in the directory for temporary files", len(entries))
	}

	// A directory that cannot be written to: the read fails and says why.
	if os.Getuid() != 0 {
		ro := t.TempDir()
		if err := os.Chmod(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		_, rep = collectText(t, b.String(), Options{CapturedAt: captured, TempDir: ro})
		if rep.Status != model.SourceError || !strings.Contains(rep.Detail, "wait for its time in a temporary file, which failed") {
			t.Errorf("directory without write access: %+v", rep)
		}
	}
}
