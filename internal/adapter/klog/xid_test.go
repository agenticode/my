package klog

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

type xidRec struct {
	Line   string
	XID    int
	Text   string
	Bus    string
	UUID   string
	Serial string
}

func xids(c *adapter.Collected) []xidRec {
	var out []xidRec
	for _, g := range c.GPUs {
		out = append(out, xidRec{g.RawRef[strings.LastIndex(g.RawRef, "#")+1:], g.XID.V, g.XIDText, g.PCIBus, g.GPUUUID, g.GPUSerial})
	}
	return out
}

func TestXidLines(t *testing.T) {
	const gpu3, gpu5 = "GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7", "GPU-5a1c2b3d-4e5f-6071-8293-a4b5c6d7e8f9"
	captured := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)
	var c adapter.Collected
	rep := NewFile(testutil.Testdata(t, "klog", "nvidia-xid.dmesg"), Options{CapturedAt: captured}).Collect(ctx, &c)
	want := []xidRec{
		{"L6", 94, "Contained: CE User Channel (0x9). RST: No, D-RST: No", "0000:3b:00.0", gpu3, "1650000000412"},
		{"L7", 48, "An uncorrectable double bit error (DBE) has been detected on GPU in the framebuffer at partition 5, subpartition 0.", "0000:3b:00.0", gpu3, "1650000000412"},
		{"L8", 63, "Row Remapper: New row marked for remapping, reset gpu to activate.", "0000:3b:00.0", gpu3, "1650000000412"},
		{"L12", 64, "", "0000:86:00.0", gpu5, "1650000000977"},
		{"L13", 95, "Uncontained: LTC TAG (0x2,0x0). RST: Yes, D-RST: No", "0000:86:00.0", gpu5, "1650000000977"},
		// The driver did not name the GPUs at these addresses in this log.
		{"L15", 94, "Contained: CE User Channel (0x9). RST: No, D-RST: No", "0000:af:00.0", "", ""},
		{"L18", 92, "High single-bit ECC error rate", "0000:d8:00.0", "", ""},
	}
	if got := xids(&c); !reflect.DeepEqual(got, want) {
		t.Errorf("Xid records:")
		for _, g := range got {
			t.Logf("  have %+v", g)
		}
	}
	if rep.Status != model.SourcePartial || rep.Skipped != 1 {
		t.Errorf("report = %+v", rep)
	}
	notes := strings.Join(rep.Notes, "\n")
	for _, part := range []string{
		"3 Xid records are not about GPU memory and were left out (Xid 31 x1, Xid 45 x1, Xid 79 x1)",
		"#L17: Xid line is unreadable",
	} {
		if !strings.Contains(notes, part) {
			t.Errorf("notes lack %q:\n%s", part, notes)
		}
	}
	// The capture ends 9400 s after boot; the first Xid came 7200 s after it.
	boot := captured.Add(-9400 * time.Second)
	g := c.GPUs[0]
	if !g.ObservedAt.Equal(boot.Add(7200*time.Second+3*time.Microsecond)) || g.TimeQuality != model.TimeEstimated || g.Kind != model.GPUXID ||
		g.Source != "klog/xid" || g.Raw == "" || g.Fingerprint == "" {
		t.Errorf("first record = %+v", g)
	}
	seen := map[string]bool{}
	for _, g := range c.GPUs {
		if seen[g.Fingerprint] {
			t.Errorf("fingerprint of %s is not unique", g.RawRef)
		}
		seen[g.Fingerprint] = true
	}
	if len(c.Boots) != 1 || !c.Boots[0].StartedAt.Equal(boot) {
		t.Errorf("boots = %+v", c.Boots)
	}

	// Without a capture time the records count from boot and stay together
	// under a boot of their own.
	var rel adapter.Collected
	NewFile(testutil.Testdata(t, "klog", "nvidia-xid.dmesg"), Options{}).Collect(ctx, &rel)
	if len(rel.GPUs) != 7 || len(rel.Boots) != 1 {
		t.Fatalf("%d records, %d boots", len(rel.GPUs), len(rel.Boots))
	}
	for _, g := range rel.GPUs {
		if g.TimeQuality != model.TimeRelative || g.BootID != rel.Boots[0].BootID || !strings.HasPrefix(g.BootID, "derived-") {
			t.Errorf("%s: time quality %q, boot %q", g.RawRef, g.TimeQuality, g.BootID)
		}
	}
	if !rel.GPUs[0].ObservedAt.Equal(time.Unix(7200, 3000)) {
		t.Errorf("relative time = %v", rel.GPUs[0].ObservedAt)
	}
	// The same line read in either way is the same record.
	if rel.GPUs[0].Fingerprint != c.GPUs[0].Fingerprint {
		t.Error("the fingerprint depends on the capture time option")
	}
}

func TestXidLinesWithDates(t *testing.T) {
	var c adapter.Collected
	rep := NewFile(testutil.Testdata(t, "klog", "nvidia-xid.messages"), Options{}).Collect(ctx, &c)
	if rep.Status != model.SourceOK || rep.HostHint != "gpu-01" || len(c.GPUs) != 3 {
		t.Fatalf("report %+v, %d records", rep, len(c.GPUs))
	}
	first, last := c.GPUs[0], c.GPUs[2]
	if !first.ObservedAt.Equal(time.Date(2026, 11, 12, 3, 14, 5, 100200000, time.UTC)) || first.TimeQuality != model.TimeExact ||
		first.XID != model.Some(48) || first.GPUSerial != "1650000000412" {
		t.Errorf("first record = %+v", first)
	}
	// After the reboot the driver has not named the GPU again: the record
	// carries the PCI address only. The ledger completes it on reading.
	if last.XID != model.Some(64) || last.GPUUUID != "" || last.GPUSerial != "" || last.PCIBus != "0000:3b:00.0" {
		t.Errorf("record after the reboot = %+v", last)
	}
}

func TestXidLineForms(t *testing.T) {
	for line, want := range map[string][3]string{
		"NVRM: Xid (PCI:0000:3b:00): 48, pid=1, name=x, text":                {"0000:3b:00", "48", "pid=1, name=x, text"},
		"NVRM: Xid (0000:03:00): 14, Channel 00000001":                       {"0000:03:00", "14", "Channel 00000001"},
		"NVRM: Xid (PCI:0000:01:00 GPU-I:05): 94, pid=7194, Contained: CE":   {"0000:01:00", "94", "pid=7194, Contained: CE"},
		"NVRM: Xid (PCI:0000:3b:00.0): 63, Row Remapper":                     {"0000:3b:00", "63", "Row Remapper"},
		"NVRM: Xid (PCI:0000:3b:00): 64":                                     {"0000:3b:00", "64", ""},
		"kernel: NVRM: Xid (PCI:0000:3B:00): 172, uncorrectable SRAM error.": {"0000:3B:00", "172", "uncorrectable SRAM error."},
	} {
		m := xidRE.FindStringSubmatch(line)
		if m == nil || m[1] != want[0] || m[2] != want[1] || m[3] != want[2] {
			t.Errorf("%q = %q", line, m)
		}
	}
	for _, line := range []string{"NVRM: Xid (broken", "NVRM: Xid (PCI:0000:3b:00): x", "NVRM: Xid (3b:00): 48, text", "NVRM: Xid (PCI:0000:3b:00): 123456, text"} {
		if m := xidRE.FindStringSubmatch(line); m != nil {
			t.Errorf("%q matched as %q", line, m)
		}
	}
	for text, want := range map[string]string{
		"pid=7062, name=python3, Contained: CE":         "Contained: CE",
		"pid='<unknown>', name=<unknown>, Row Remapper": "Row Remapper",
		"pid='<unknown>', name=<unknown>":               "",
		"pid=7194, Contained: CE User Channel":          "Contained: CE User Channel",
		"Channel 00000001":                              "Channel 00000001",
		"name=x":                                        "",
		"":                                              "",
	} {
		if got := xidProcRE.ReplaceAllString(text, ""); got != want {
			t.Errorf("text of %q = %q, want %q", text, got, want)
		}
	}
	if got := otherXIDNote(map[int]int{79: 2, 31: 5, 13: 5}); got != "12 Xid records are not about GPU memory and were left out (Xid 13 x5, Xid 31 x5, Xid 79 x2)" {
		t.Errorf("note = %q", got)
	}
	if otherXIDNote(nil) != "" {
		t.Error("a note without records")
	}
}
