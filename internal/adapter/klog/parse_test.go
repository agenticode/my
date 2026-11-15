package klog

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

func TestFrame(t *testing.T) {
	ref := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	seoul := time.FixedZone("KST", 9*3600)
	mono := 1234*time.Second + 567890*time.Microsecond
	tests := []struct {
		name     string
		in       string
		loc      *time.Location
		wantMsg  string
		wantWall time.Time
		wantMono time.Duration
		hasMono  bool
		host     string
		kernel   bool
	}{
		{"dmesg", "[ 1234.567890] EDAC MC0: x", time.UTC, "EDAC MC0: x", time.Time{}, mono, true, "", true},
		{"dmesg, large uptime", "[1234567.000001] x", time.UTC, "x", time.Time{}, 1234567*time.Second + time.Microsecond, true, "", true},
		{"dmesg -r", "<4>[ 1234.567890] EDAC MC0: x", time.UTC, "EDAC MC0: x", time.Time{}, mono, true, "", true},
		{"dmesg -T", "[Thu Nov 12 03:14:07 2026] EDAC MC0: x", time.UTC, "EDAC MC0: x", time.Date(2026, 11, 12, 3, 14, 7, 0, time.UTC), 0, false, "", true},
		{"dmesg -T, single-digit day, local zone", "[Sun Nov  1 03:14:07 2026] x", seoul, "x", time.Date(2026, 10, 31, 18, 14, 7, 0, time.UTC), 0, false, "", true},
		{"dmesg --time-format=iso", "2026-11-12T03:14:07,123456+09:00 Memory failure: 0x1234: recovery action for dirty LRU page: Recovered", time.UTC,
			"Memory failure: 0x1234: recovery action for dirty LRU page: Recovered", time.Date(2026, 11, 11, 18, 14, 7, 123456000, time.UTC), 0, false, "", true},
		{"syslog", "Nov 12 03:14:07 r12-n034 kernel: [ 1234.567890] EDAC MC0: x", time.UTC, "EDAC MC0: x", time.Date(2026, 11, 12, 3, 14, 7, 0, time.UTC), mono, true, "r12-n034", true},
		{"syslog without stamp", "Nov 12 03:14:07 r12-n034 kernel: EDAC MC0: x", time.UTC, "EDAC MC0: x", time.Date(2026, 11, 12, 3, 14, 7, 0, time.UTC), 0, false, "r12-n034", true},
		{"syslog, another program", "Nov 12 03:14:07 r12-n034 sshd[4410]: Accepted publickey", time.UTC, "Accepted publickey", time.Date(2026, 11, 12, 3, 14, 7, 0, time.UTC), 0, false, "r12-n034", false},
		{"syslog, date after the reference is last year", "Dec 31 23:59:59 h kernel: x", time.UTC, "x", time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC), 0, false, "h", true},
		{"syslog with RFC 3339 time", "2026-11-12T03:14:07.123456+09:00 r12-n034 kernel: [ 1234.567890] EDAC MC0: x", time.UTC, "EDAC MC0: x",
			time.Date(2026, 11, 11, 18, 14, 7, 123456000, time.UTC), mono, true, "r12-n034", true},
		{"journalctl -o short-iso", "2026-11-12T03:14:07+0900 r12-n034 kernel: EDAC MC0: x", time.UTC, "EDAC MC0: x", time.Date(2026, 11, 11, 18, 14, 7, 0, time.UTC), 0, false, "r12-n034", true},
		{"/dev/kmsg", "4,1532,1234567890,-;EDAC MC0: x", time.UTC, "EDAC MC0: x", time.Time{}, mono, true, "", true},
		{"journalctl -o short-monotonic", "[ 1234.567890] r12-n034 kernel: EDAC MC0: x", time.UTC, "EDAC MC0: x", time.Time{}, mono, true, "r12-n034", true},
		{"dmesg, message with a colon in its first word", "[ 1234.567890] mce: [Hardware Error]: Machine check events logged", time.UTC,
			"mce: [Hardware Error]: Machine check events logged", time.Time{}, mono, true, "", true},
		{"bare message", "EDAC MC0: 1 CE x", time.UTC, "EDAC MC0: 1 CE x", time.Time{}, 0, false, "", true},
		{"firmware block line, bare", "{1}[Hardware Error]: event severity: corrected", time.UTC, "{1}[Hardware Error]: event severity: corrected", time.Time{}, 0, false, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := framer{loc: tt.loc, ref: ref}
			l, ok := f.frame(1, tt.in)
			if !ok {
				t.Fatal("not framed")
			}
			if l.msg != tt.wantMsg || !l.wall.Equal(tt.wantWall) || l.hasMono != tt.hasMono || l.mono != tt.wantMono || l.host != tt.host || l.kernel != tt.kernel {
				t.Errorf("got msg=%q wall=%v mono=%v/%v host=%q kernel=%v", l.msg, l.wall, l.mono, l.hasMono, l.host, l.kernel)
			}
		})
	}
	f := framer{loc: time.UTC}
	for _, blank := range []string{"", "   ", "\r\n"} {
		if _, ok := f.frame(1, blank); ok {
			t.Errorf("blank line %q was framed", blank)
		}
	}
	// In a syslog file with RFC 3339 times, lines of other programs are
	// recognised once a kernel line showed that it is a syslog file.
	f = framer{loc: time.UTC}
	if l, _ := f.frame(1, "2026-11-12T03:14:07+09:00 host kernel: x"); !l.kernel || l.host != "host" {
		t.Errorf("kernel line: %+v", l)
	}
	if l, _ := f.frame(2, "2026-11-12T03:14:08+09:00 host chronyd[812]: Selected source"); l.kernel {
		t.Errorf("line of another program was taken for a kernel message: %+v", l)
	}
}

// journalctl -o short-monotonic prints every program in the shape dmesg
// prints kernel messages. Lines of other programs are told apart once a
// "host kernel:" line showed what the input is.
func TestFrameShortMonotonic(t *testing.T) {
	f := framer{loc: time.UTC}
	if l, _ := f.frame(1, "[   32.185375] lab-vm systemd[1]: Hostname set to <lab-vm>."); !l.kernel {
		t.Errorf("before any kernel line a tagged line is a message: %+v", l)
	}
	if l, _ := f.frame(2, "[   16.055546] localhost kernel: Linux version 7.3.0-ras (root@lab-vm) #2 SMP"); !l.kernel || l.host != "localhost" ||
		l.msg != "Linux version 7.3.0-ras (root@lab-vm) #2 SMP" || l.mono != 16055546*time.Microsecond {
		t.Errorf("kernel line: %+v", l)
	}
	if l, _ := f.frame(3, "[   32.185375] lab-vm systemd[1]: Hostname set to <lab-vm>."); l.kernel || l.host != "lab-vm" {
		t.Errorf("line of another program was taken for a kernel message: %+v", l)
	}
	if l, _ := f.frame(4, "[   90.703791] lab-vm kernel: Memory failure: 0x64faa1: recovery action for dirty LRU page: Recovered"); !l.kernel ||
		l.msg != "Memory failure: 0x64faa1: recovery action for dirty LRU page: Recovered" {
		t.Errorf("kernel line with colons in the message: %+v", l)
	}
}

func TestFrameJSON(t *testing.T) {
	l, err := frameJSON(7, `{"MESSAGE":"EDAC MC0: x","_TRANSPORT":"kernel","_BOOT_ID":"abc","_HOSTNAME":"h","__REALTIME_TIMESTAMP":"1794420847000000","__MONOTONIC_TIMESTAMP":"1234568000","_SOURCE_MONOTONIC_TIMESTAMP":"1234567890"}`)
	if err != nil {
		t.Fatal(err)
	}
	if l.msg != "EDAC MC0: x" || !l.kernel || l.bootID != "abc" || l.host != "h" || !l.wall.Equal(time.UnixMicro(1794420847000000)) ||
		l.mono != 1234567890*time.Microsecond || !l.bootStart.Equal(time.UnixMicro(1794420847000000-1234568000)) {
		t.Errorf("entry: %+v", l)
	}
	// Without the kernel's own stamp the journal's receive time stands in.
	l, _ = frameJSON(1, `{"MESSAGE":"x","_TRANSPORT":"kernel","__REALTIME_TIMESTAMP":"1794420847000000","__MONOTONIC_TIMESTAMP":"5000000"}`)
	if !l.hasMono || l.mono != 5*time.Second {
		t.Errorf("fallback stamp: %+v", l)
	}
	for name, in := range map[string]string{
		"null message":       `{"MESSAGE":null,"_TRANSPORT":"kernel"}`,
		"no message":         `{"_TRANSPORT":"kernel"}`,
		"byte array message": `{"MESSAGE":[69,68,65,67,255],"_TRANSPORT":"kernel"}`,
	} {
		l, err := frameJSON(1, in)
		if err != nil || !l.kernel {
			t.Errorf("%s: %+v, %v", name, l, err)
		}
		if name == "byte array message" && l.msg != "EDAC�" {
			t.Errorf("byte array decoded to %q", l.msg)
		}
	}
	if l, _ := frameJSON(1, `{"MESSAGE":"x","_TRANSPORT":"syslog","SYSLOG_IDENTIFIER":"rasdaemon"}`); l.kernel {
		t.Error("an entry of another program was taken for a kernel message")
	}
	for _, bad := range []string{`{"MESSAGE":"cut off`, `{"MESSAGE":{"a":1}}`, `{"MESSAGE":["x"]}`} {
		if _, err := frameJSON(1, bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestParseEDACLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want edacRec
		why  string
	}{
		{
			name: "Intel, corrected, with detail",
			in:   "EDAC MC1: 1 CE memory read error on CPU_SrcID#0_MC#1_Chan#0_DIMM#0 (channel:0 slot:0 page:0x1f8bd1 offset:0x9c0 grain:32 syndrome:0x0 -  err_code:0x0101:0x0090 socket:0 imc:1 rank:0 bg:0 ba:3 row:0x1a2f col:0x3f8)",
			want: edacRec{mc: 1, count: 1, kind: "CE", msg: "memory read error", label: "CPU_SrcID#0_MC#1_Chan#0_DIMM#0", location: "channel:0 slot:0",
				page: 0x1f8bd1, offset: 0x9c0, grain: 32, syndrome: model.Some(uint64(0)),
				detail: " err_code:0x0101:0x0090 socket:0 imc:1 rank:0 bg:0 ba:3 row:0x1a2f col:0x3f8"},
		},
		{
			name: "AMD, empty message, no detail",
			in:   "EDAC MC0: 1 CE on mc#0csrow#2channel#1 (csrow:2 channel:1 page:0x7f5d2c offset:0x9c0 grain:64 syndrome:0x4)",
			want: edacRec{mc: 0, count: 1, kind: "CE", label: "mc#0csrow#2channel#1", location: "csrow:2 channel:1", page: 0x7f5d2c, offset: 0x9c0, grain: 64, syndrome: model.Some(uint64(4))},
		},
		{
			name: "uncorrected: no syndrome",
			in:   "EDAC MC0: 1 UE on mc#0csrow#2channel#1 (csrow:2 channel:1 page:0x7f5d2c offset:0x9c0 grain:64)",
			want: edacRec{mc: 0, count: 1, kind: "UE", label: "mc#0csrow#2channel#1", location: "csrow:2 channel:1", page: 0x7f5d2c, offset: 0x9c0, grain: 64},
		},
		{
			name: "label with spaces, batched count, prefix before EDAC",
			in:   "kernel: EDAC MC2: 12 CE memory read error on CPU1 DIMM A1 (channel:2 slot:1 page:0x10 offset:0x0 grain:8 syndrome:0xdeadbeef)",
			want: edacRec{mc: 2, count: 12, kind: "CE", msg: "memory read error", label: "CPU1 DIMM A1", location: "channel:2 slot:1", page: 0x10, grain: 8, syndrome: model.Some(uint64(0xdeadbeef))},
		},
		{
			name: "firmware location with parentheses in the detail",
			in:   "EDAC MC0: 1 CE single-bit ECC on unknown memory (node:0 card:1 module:0 DIMM location: not present. DMI handle: 0x0012 page:0x12f4a6 offset:0x0 grain:4096 syndrome:0x0 - APEI location: node:0 card:1 module:0 status(0x0000000000000400): Storage error in DRAM memory)",
			want: edacRec{mc: 0, count: 1, kind: "CE", msg: "single-bit ECC", label: "unknown memory",
				location: "node:0 card:1 module:0 DIMM location: not present. DMI handle: 0x0012", page: 0x12f4a6, grain: 4096, syndrome: model.Some(uint64(0)),
				detail: "APEI location: node:0 card:1 module:0 status(0x0000000000000400): Storage error in DRAM memory"},
		},
		{
			name: "no layer position",
			in:   "EDAC MC0: 1 CE Failed to map error addr to a csrow on any memory (page:0x0 offset:0x0 grain:1 syndrome:0x0)",
			want: edacRec{mc: 0, count: 1, kind: "CE", msg: "Failed to map error addr to a csrow", label: "any memory", grain: 1, syndrome: model.Some(uint64(0))},
		},
		{name: "cut off", in: "EDAC MC1: 1 CE memory read error on CPU_SrcID#0_MC#1_Chan#0_DIMM#0 (channel:0 slot:0 page:0x1f8bd1 offset:0x9c0 grain:32 synd", why: "EDAC line is cut off before the closing parenthesis"},
		{name: "no address fields", in: "EDAC MC1: 1 CE memory read error on DIMM (channel:0 slot:0)", why: "EDAC line has no page field"},
		{name: "bad address fields", in: "EDAC MC1: 1 CE memory read error on DIMM (channel:0 slot:0 page:0xZZ offset:0x0 grain:1)", why: "EDAC line address fields are unreadable"},
		{name: "no DIMM", in: "EDAC MC1: 1 CE memory read error (channel:0 slot:0 page:0x1 offset:0x0 grain:1 syndrome:0x0)", why: "EDAC line does not name a DIMM"},
		{name: "another EDAC message", in: "EDAC MC0: Giving out device to module skx_edac controller Skylake Socket#0 IMC#0: DEV 0000:64:0a.0 (INTERRUPT)", why: "not an EDAC error line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, why := parseEDACLine(tt.in)
			if why != tt.why {
				t.Fatalf("reason = %q, want %q", why, tt.why)
			}
			if why == "" && got != tt.want {
				t.Errorf("\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

func TestParseOffline(t *testing.T) {
	tests := []struct {
		in   string
		want offline
		ok   bool
	}{
		{"Memory failure: 0x1f8bd1: recovery action for dirty LRU page: Recovered", offline{0x1f8bd1, "hard", model.ResultOK, "dirty LRU page: Recovered"}, true},
		{"Memory failure: 0x1f8bd1: recovery action for free buddy page: Delayed", offline{0x1f8bd1, "hard", model.ResultPending, "free buddy page: Delayed"}, true},
		{"Memory failure: 0x9e0040: recovery action for kernel slab page: Failed", offline{0x9e0040, "hard", model.ResultFailed, "kernel slab page: Failed"}, true},
		{"Memory failure: 0x9e0040: recovery action for reserved kernel page: Ignored", offline{0x9e0040, "hard", model.ResultFailed, "reserved kernel page: Ignored"}, true},
		{"RAS: Soft-offlining pfn: 0x1f8bd1", offline{0x1f8bd1, "soft", model.ResultUnknown, "corrected error collector"}, true},
		{"Soft offlining pfn 0x1f8bd1 at process virtual address 0x7f2c00000000", offline{0x1f8bd1, "soft", model.ResultUnknown, "requested through madvise"}, true},
		{"soft offline: 0x1f8bd1: migration failed 1, type 17ffffc0020014", offline{0x1f8bd1, "soft", model.ResultFailed, "migration failed 1, type 17ffffc0020014"}, true},
		{"Soft offline: 0x1f8bd1: page migration failed 1, type 0x17ffffc0020014", offline{0x1f8bd1, "soft", model.ResultFailed, "page migration failed 1, type 0x17ffffc0020014"}, true},
		{"soft_offline: 0x1f8bd1: invalidated", offline{0x1f8bd1, "soft", model.ResultOK, "invalidated"}, true},
		{"soft offline: 0x1f8bd1 page already poisoned", offline{}, false},
		{"Soft offline: 0x1f8bd1: page already poisoned", offline{}, false},
		{"Memory failure: 0x1f8bd1: already hardware poisoned", offline{}, false},
		{"Memory failure: 0x1f8bd1: Sending SIGBUS to postgres:4242 due to hardware memory corruption", offline{}, false},
		{"memory offlining [mem 0x100000000-0x107ffffff] failed due to memory holes", offline{}, false},
	}
	for _, tt := range tests {
		got, ok := parseOffline(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("parseOffline(%q) = %+v, %v; want %+v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestQuickMono(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"[    0.000000] Linux version", 0, true},
		{"[ 1234.567890] x", 1234*time.Second + 567890*time.Microsecond, true},
		{"<6>[ 1234.567890] x", 1234*time.Second + 567890*time.Microsecond, true},
		{"Nov 12 03:14:07 host kernel: [ 1234.567890] x", 1234*time.Second + 567890*time.Microsecond, true},
		{"2026-11-12T03:14:07.123456+09:00 host kernel: [ 1234.567890] x", 1234*time.Second + 567890*time.Microsecond, true},
		{"4,1532,1234567890,-;x", 1234*time.Second + 567890*time.Microsecond, true},
		{"[Thu Nov 12 03:14:07 2026] x", 0, false},
		{"Nov 12 03:14:07 host systemd[1]: Started", 0, false},
		{"Nov 12 03:14:07 host app: took [ 12.345678] seconds", 0, false},
		{"[ 12.34] x", 0, false},
		{"[9999999999.000000] x", 0, false},
		{"4,1,9999999999999999999,-;x", 0, false},
		{"", 0, false},
		{" SUBSYSTEM=edac", 0, false},
		{"1,2;x", 0, false},
	}
	for _, tt := range tests {
		got, ok := quickMono(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("quickMono(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestHas64KPages(t *testing.T) {
	for rel, want := range map[string]bool{
		"4.18.0-553.el8_10.aarch64":         true,
		"3.10.0-1160.el7.aarch64":           true,
		"5.14.0-570.12.1.el9_6.aarch64":     false,
		"5.14.0-570.12.1.el9_6.aarch64+64k": true,
		"6.8.0-45-generic-64k":              true,
		"4.18.0-553.el8_10.x86_64":          false,
		"6.12.0-55.9.1.el10_0.aarch64":      false,
	} {
		if got := has64KPages(rel); got != want {
			t.Errorf("has64KPages(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestMachineName(t *testing.T) {
	for in, want := range map[string]string{
		"Dell Inc. PowerEdge R740/0923K0":                                       "Dell Inc. PowerEdge R740/0923K0",
		"QEMU Standard PC (Q35 + ICH9, 2009)":                                   "QEMU Standard PC (Q35 + ICH9, 2009)",
		"VMware, Inc. VMware Virtual Platform/440BX Desktop Reference Platform": "VMware, Inc. VMware Virtual Platform/440BX Desktop Reference Platform",
		// A board that repeats the product, as one cloud reports it.
		"ExampleCloud mi1-g3;MICRO/mi1-g3;MICRO": "ExampleCloud mi1-g3;MICRO",
		"KVM/KVM":                                "KVM",
		"Example Systems X1/":                    "Example Systems X1",
		"":                                       "",
	} {
		if got := machineName(in); got != want {
			t.Errorf("machineName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCoverageNote(t *testing.T) {
	note := func(text string, opts Options) string {
		var c adapter.Collected
		rep := NewReader(strings.NewReader(text), "log", opts).Collect(context.Background(), &c)
		for _, n := range rep.Notes {
			if strings.HasPrefix(n, "covers ") {
				return n
			}
		}
		return ""
	}
	boot := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, text string
		opts       Options
		want       string
	}{
		{"dmesg of one boot, boot time unknown",
			"[    0.000000] Linux version 6.8.0 (x)\n[  100.000000] usb 1-1: new device\n", Options{},
			"covers 0 s to 100 s after boot"},
		{"the ring buffer of a live host that reaches back to the boot",
			"6,0,0,-;Linux version 6.8.0 (x)\n6,9,86400000000,-;something\n", Options{BootTime: boot},
			"covers 2026-07-11 08:00 to 2026-07-12 08:00 UTC"},
		{"a ring buffer that has wrapped",
			"6,900,864000000000,-;something\n6,901,950400000000,-;more\n", Options{BootTime: boot},
			"covers 2026-07-21 08:00 to 2026-07-22 08:00 UTC; it starts 10.0 days after boot, and what the kernel said before that is not in it"},
		{"a journal export",
			`{"__REALTIME_TIMESTAMP":"1794700800000000","_TRANSPORT":"kernel","MESSAGE":"a","_SOURCE_MONOTONIC_TIMESTAMP":"5000000"}` + "\n" +
				`{"__REALTIME_TIMESTAMP":"1794787200000000","_TRANSPORT":"kernel","MESSAGE":"b","_SOURCE_MONOTONIC_TIMESTAMP":"86405000000"}` + "\n", Options{},
			"covers 2026-11-15 00:00 to 2026-11-16 00:00 UTC"},
		{"a syslog file with RFC 3339 times",
			"2026-11-12T03:14:07.123456+09:00 host kernel: a\n2026-11-13T03:14:07.123456+09:00 host kernel: b\n", Options{},
			"covers 2026-11-11 18:14 to 2026-11-12 18:14 UTC"},
		{"nothing that carries a time", "hello\nworld\n", Options{}, ""},
		{"an empty input", "", Options{}, ""},
	} {
		if got := note(tc.text, tc.opts); got != tc.want {
			t.Errorf("%s:\n got  %q\n want %q", tc.name, got, tc.want)
		}
	}
}
