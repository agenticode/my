package scan

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter/crashlog"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// panicDump returns the last lines of the real fatal machine check capture
// as a pstore record of one part: the header of pstore_dump and the
// priority kmsg_dump_get_buffer writes (fs/pstore/platform.c).
func panicDump(t *testing.T) string {
	t.Helper()
	b := testutil.ReadTestdata(t, "real", "7.3.0-mce-fatal", "console.log")
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	var out strings.Builder
	out.WriteString("Panic#1 Part1\n")
	for _, l := range lines[len(lines)-12:] {
		out.WriteString("<0>" + l + "\n")
	}
	return out.String()
}

// TestCrashLogsOfACopy: a copy of a host's files that holds the pstore file
// system, the archive of systemd-pstore and the dump path of kdump is read
// in all three places, and the same record in two of them is one event.
func TestCrashLogsOfACopy(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "proc/sys/kernel/hostname"), "lab-guest\n", 0o644)
	dump := panicDump(t)
	id := uint64(1794739503)<<32 + 1
	write(t, filepath.Join(root, "sys/fs/pstore", fmt.Sprintf("dmesg-erst-%d", id)), dump, 0o644)
	write(t, filepath.Join(root, "var/lib/systemd/pstore", fmt.Sprint(id), "dmesg.txt"), dump, 0o644)
	write(t, filepath.Join(root, "var/crash/127.0.0.1-2026-11-15-03:25:52/vmcore-dmesg.txt"), strings.ReplaceAll(dump[len("Panic#1 Part1\n"):], "<0>", ""), 0o644)
	opts := Options{Root: root}
	if !HoldsErrorSource(&opts) {
		t.Fatal("a copy with crash logs holds no source of error records")
	}
	s := ledger(t)
	res, err := Run(ctx, s, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, place := range []string{"sys/fs/pstore", "var/lib/systemd/pstore", "var/crash"} {
		found := false
		for _, src := range res.Sources {
			if src.Origin == filepath.Join(root, place) {
				found = true
				// The fatal record, the kernel's lines of the stop, and
				// the record of the panic.
				if src.Status != model.SourceOK || src.Events != 3 {
					t.Errorf("%s: %+v", place, src)
				}
			}
		}
		if !found {
			t.Errorf("%s is not in the inventory: %s", place, inventory(res.Sources))
		}
	}
	// One error, the kernel's lines of the stop, and the panic as pstore
	// and kdump show it: two texts.
	if res.Events != 4 {
		t.Errorf("%d events, want 4", res.Events)
	}
}

// TestCrashLogsOfALiveHost: the pstore file system of a running host is
// read, with the times of its files as the times of the records; an empty
// store is an entry of the inventory that does not count as a source of
// error records.
func TestCrashLogsOfALiveHost(t *testing.T) {
	root, opts := liveCopy(t, "7.3.0-mce-print-all", false)
	write(t, filepath.Join(root, "sys/fs/pstore/dmesg-ramoops-0"), panicDump(t), 0o644)
	s := ledger(t)
	res, err := Run(ctx, s, opts)
	if err != nil {
		t.Fatal(err)
	}
	src := source(t, res.Sources, crashlog.KindPstore)
	if src.Events != 3 || !Reads(src) {
		t.Errorf("pstore: %+v", src)
	}
	empty := model.Source{Kind: crashlog.KindPstore, Status: model.SourceEmpty}
	if Reads(empty) || Reads(model.Source{Kind: crashlog.KindKdump, Status: model.SourceEmpty}) {
		t.Error("an empty crash log place counts as a source of error records")
	}
	if !Blind([]model.Source{empty}) {
		t.Error("a scan that read an empty store alone is not blind")
	}
}

// TestDetectCrashLogs: my import knows a pstore file, a dmesg.txt of
// systemd-pstore, a kdump text log and a directory of them.
func TestDetectCrashLogs(t *testing.T) {
	dir := t.TempDir()
	dump := panicDump(t)
	pst := filepath.Join(dir, "store", "dmesg-efi_pstore-179473950301001")
	write(t, pst, dump, 0o644)
	txt := filepath.Join(dir, "archive", "1794739503", "001", "dmesg.txt")
	write(t, txt, dump, 0o644)
	renamed := filepath.Join(dir, "saved-pstore.txt")
	write(t, renamed, dump, 0o644)
	vm := filepath.Join(dir, "crash", "x", "vmcore-dmesg.txt")
	write(t, vm, strings.ReplaceAll(dump[len("Panic#1 Part1\n"):], "<0>", ""), 0o644)
	for _, tt := range []struct {
		path, what, kinds string
	}{
		{pst, WhatPstore, "pstore"},
		{txt, WhatPstore, "pstore"},
		{renamed, WhatPstore, "pstore"},
		{vm, WhatKdump, "kdump"},
		{filepath.Join(dir, "store"), WhatCrashLogs, "pstore"},
		{filepath.Join(dir, "archive"), WhatCrashLogs, "pstore"},
		{filepath.Join(dir, "crash"), WhatCrashLogs, "kdump"},
		{dir, WhatCrashLogs, "pstore,kdump"},
	} {
		as, what, err := Detect(ctx, tt.path, DetectOptions{})
		if err != nil || what != tt.what || kinds(as) != tt.kinds {
			t.Errorf("%s: %q %q %v, want %q %q", tt.path, kinds(as), what, err, tt.what, tt.kinds)
		}
	}
}
