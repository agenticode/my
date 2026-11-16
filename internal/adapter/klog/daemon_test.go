package klog

import (
	"strings"
	"testing"

	"github.com/agenticode/my/internal/model"
)

var syslogOpts = Options{CapturedAt: utc(2026, 11, 16, 0, 0, 0)}

const syslogBoot = "Nov 15 10:40:00 lab kernel: [   10.000000] Linux version 7.3.0-ras (b@l) #1 SMP\n"

func lines(l ...string) string { return strings.Join(l, "\n") + "\n" }

// Lines of mcelog (page.c, memdb.c) between the kernel's lines of a
// syslog file.
func TestMcelogPageOfflines(t *testing.T) {
	const thresh = "Nov 15 10:44:00 lab mcelog: Corrected memory errors on page 12345000 exceed threshold 10 in 24h: 10 in 24h"
	const why = "corrected errors exceed the threshold of mcelog, 10 in 24h"
	cases := []struct {
		name    string
		text    string
		source  string
		method  string
		result  model.ActionResult
		detail  string
		actions int
		note    string
	}{
		{"soft offline that worked: the kernel prints nothing", syslogBoot + lines(thresh,
			"Nov 15 10:44:00 lab mcelog: Location SOCKET:0 CHANNEL:1 DIMM:0 [DIMM_A1]",
			"Nov 15 10:44:00 lab mcelog: Offlining page 12345000"),
			"klog/mcelog", "", model.ResultUnknown, "asked for by mcelog: " + why, 1, ""},
		{"soft offline that failed, with the kernel's line of 6.x", syslogBoot + lines(thresh,
			"Nov 15 10:44:00 lab mcelog: Offlining page 12345000",
			"Nov 15 10:44:00 lab kernel: [  250.000000] Memory failure: 0x12345: page migration failed 1, type 0x17ffffc0000000(node=0|zone=2)",
			"Nov 15 10:44:00 lab mcelog: Offlining page 12345000 failed: Device or resource busy"),
			"klog/offline", "soft", model.ResultFailed, "page migration failed 1, type 0x17ffffc0000000(node=0|zone=2); asked for by mcelog: " + why, 1, ""},
		{"failure without the kernel's line", syslogBoot + lines(thresh,
			"Nov 15 10:44:00 lab mcelog: Offlining page 12345000",
			"Nov 15 10:44:00 lab mcelog: Soft offlining of page 12345000 failed, trying hard offlining",
			"Nov 15 10:44:00 lab mcelog: Offlining page 12345000 failed: Device or resource busy"),
			"klog/mcelog", "", model.ResultFailed, "asked for by mcelog: " + why + "; soft offline failed, hard offline tried; failed: Device or resource busy", 1, ""},
		{"hard offline", syslogBoot + lines(thresh,
			"Nov 15 10:44:00 lab mcelog: Offlining page 12345000",
			"Nov 15 10:44:00 lab kernel: [  250.000000] Memory failure: 0x12345: recovery action for free buddy page: Recovered"),
			"klog/offline", "hard", model.ResultOK, "free buddy page: Recovered; asked for by mcelog: " + why, 1, ""},
		{"threshold only: mcelog only counts", syslogBoot + lines(thresh), "", "", "", "", 0,
			"mcelog: the corrected errors of 1 page exceeded its threshold, and no page offline record of it is in this log"},
		{"offline without the threshold line", syslogBoot + lines("Nov 15 10:44:00 lab mcelog: Offlining page 12345000"),
			"klog/mcelog", "", model.ResultUnknown, "asked for by mcelog", 1, ""},
		{"journal export", lines(
			`{"MESSAGE":"Corrected memory errors on page 12345000 exceed threshold 10 in 24h: 10 in 24h","_TRANSPORT":"syslog","SYSLOG_IDENTIFIER":"mcelog","__REALTIME_TIMESTAMP":"1794739440000000","_BOOT_ID":"0123456789abcdef0123456789abcdef"}`,
			`{"MESSAGE":"Offlining page 12345000","_TRANSPORT":"syslog","SYSLOG_IDENTIFIER":"mcelog","__REALTIME_TIMESTAMP":"1794739440000100","_BOOT_ID":"0123456789abcdef0123456789abcdef"}`),
			"klog/mcelog", "", model.ResultUnknown, "asked for by mcelog: " + why, 1, ""},
		{"another program with the words", syslogBoot + lines("Nov 15 10:44:00 lab logger: Offlining page 12345000"), "", "", "", "", 0, ""},
		{"address beyond 52 bits", syslogBoot + lines("Nov 15 10:44:00 lab mcelog: Offlining page ffffffffffffffff"), "", "", "", "", 0,
			"page address of mcelog is out of range"},
		{"address of 17 digits", syslogBoot + lines("Nov 15 10:44:00 lab mcelog: Offlining page 1ffffffffffffffff"), "", "", "", "", 0, ""},
		{"no address", syslogBoot + lines("Nov 15 10:44:00 lab mcelog: Corrected memory errors on page  exceed threshold 10 in 24h"), "", "", "", "", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rep := collectText(t, tc.text, syslogOpts)
			if rep.Status == model.SourceError || rep.Status == model.SourceUnreadable {
				t.Fatalf("status %s: %s", rep.Status, rep.Detail)
			}
			if len(c.Actions) != tc.actions || len(errorsOf(c.Events)) != 0 {
				t.Fatalf("%d actions, %d events: %+v", len(c.Actions), len(c.Events), c.Actions)
			}
			if tc.actions == 1 {
				a := c.Actions[0]
				if a.Source != tc.source || a.Method != tc.method || a.Result != tc.result || a.Detail != tc.detail ||
					a.OsPhysicalAddress != addr(0x12345000) || a.Kind != model.ActionPageOffline || a.TimeQuality != model.TimeExact ||
					a.At.Year() != 2026 {
					t.Errorf("action %+v\nwant source %s, method %q, result %s, detail %q", *a, tc.source, tc.method, tc.result, tc.detail)
				}
				if strings.HasPrefix(tc.text, "{") && a.BootID != "0123456789abcdef0123456789abcdef" {
					t.Errorf("boot %q", a.BootID)
				}
			}
			if tc.note != "" && !hasNote(rep, tc.note) {
				t.Errorf("notes %q lack %q", rep.Notes, tc.note)
			}
		})
	}
}

// A page that mcelog offlined, and many lines later the kernel's record
// of the same page: they are two offlines.
func TestMcelogOfflineFarFromTheKernelLine(t *testing.T) {
	var b strings.Builder
	b.WriteString(syslogBoot)
	b.WriteString("Nov 15 10:44:00 lab mcelog: Offlining page 12345000\n")
	for range injectionReach + 1 {
		b.WriteString("Nov 15 10:44:01 lab kernel: [  250.000000] EDAC DEBUG: nothing\n")
	}
	b.WriteString("Nov 15 10:45:00 lab kernel: [  300.000000] Memory failure: 0x12345: recovery action for free buddy page: Recovered\n")
	c, _ := collectText(t, b.String(), syslogOpts)
	if len(c.Actions) != 2 || c.Actions[0].Detail != "asked for by mcelog" || c.Actions[1].Detail != "free buddy page: Recovered" {
		t.Errorf("actions %+v", c.Actions)
	}
}

// The kernel's lines about the program an uncorrected error reached
// (arch/x86/kernel/cpu/mce/core.c, mm/memory-failure.c).
func TestProgramThatConsumedAnError(t *testing.T) {
	const boot = "[    0.000000] Linux version 7.3.0-ras (b@l) #1 SMP\n"
	cases := []struct {
		name   string
		text   string
		detail string
		note   string
	}{
		{"a read in user space", boot + lines(
			"[  300.000000] mce: Uncorrected hardware memory error in user-access at 12345040",
			"[  300.000100] Memory failure: 0x12345: Sending SIGBUS to python3:4242 due to hardware memory corruption",
			"[  300.000200] Memory failure: 0x12345: recovery action for dirty LRU page: Recovered"),
			"dirty LRU page: Recovered; a read in user space consumed the error; SIGBUS to python3:4242", ""},
		{"a copy from user memory", boot + lines(
			"[  300.000000] mce: Kernel accessed poison in user space at 12345040",
			"[  300.000200] Memory failure: 0x12345: recovery action for dirty LRU page: Recovered"),
			"dirty LRU page: Recovered; the kernel consumed the error copying from user memory", ""},
		{"a name with a space, killed by force", boot + lines(
			"[  300.000000] mce: Uncorrected hardware memory error in user-access at 12345040",
			"[  300.000100] Memory failure: 0x12345: forcibly killing kworker 1:4242 because of failure to unmap corrupted page",
			"[  300.000200] Memory failure: 0x12345: recovery action for unmapping failed page: Failed"),
			"unmapping failed page: Failed; a read in user space consumed the error; SIGBUS to kworker 1:4242", ""},
		{"no page offline record: the page was poisoned already", boot + lines(
			"[  300.000000] mce: Uncorrected hardware memory error in user-access at 12345040",
			"[  300.000100] Memory failure: 0x12345: already hardware poisoned"),
			"", "1 uncorrected error reached a program or a copy from its memory, and no page offline record of its page is in this log"},
		{"another page", boot + lines(
			"[  300.000000] mce: Uncorrected hardware memory error in user-access at 99999040",
			"[  300.000200] Memory failure: 0x12345: recovery action for dirty LRU page: Recovered"),
			"dirty LRU page: Recovered", "1 uncorrected error reached a program"},
		{"address beyond 52 bits", boot + lines(
			"[  300.000000] mce: Uncorrected hardware memory error in user-access at ffffffffffffffff",
			"[  300.000200] Memory failure: 0x12345: recovery action for dirty LRU page: Recovered"),
			"dirty LRU page: Recovered", "out of range"},
		{"address cut off", boot + lines(
			"[  300.000000] mce: Uncorrected hardware memory error in user-access at ",
			"[  300.000200] Memory failure: 0x12345: recovery action for dirty LRU page: Recovered"),
			"dirty LRU page: Recovered", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rep := collectText(t, tc.text, Options{BootTime: utc(2026, 11, 15, 10, 0, 0)})
			var offlines []*model.Action
			for _, a := range c.Actions {
				offlines = append(offlines, a)
			}
			if tc.detail == "" && len(offlines) != 0 || tc.detail != "" && (len(offlines) != 1 || offlines[0].Detail != tc.detail) {
				t.Errorf("actions %+v, want detail %q", offlines, tc.detail)
			}
			if len(errorsOf(c.Events)) != 0 {
				t.Errorf("events %+v: the lines are no error records", c.Events)
			}
			if tc.note != "" && !hasNote(rep, tc.note) {
				t.Errorf("notes %q lack %q", rep.Notes, tc.note)
			}
		})
	}
}

// Lines that say that records are missing or that poison reached a
// guest: notes, never errors.
func TestLinesAboutMissingRecordsAndPoison(t *testing.T) {
	text := syslogBoot + lines(
		"Nov 15 10:44:00 bmc sel-logger[300]: Failed adding this event: sd_bus_call: org.freedesktop.DBus.Error.ServiceUnknown",
		"Nov 15 10:45:00 bmc sel-logger[300]: Failed adding this event: sd_bus_call: org.freedesktop.DBus.Error.Timeout",
		"Nov 15 10:46:00 hv virtqemud[1234]: internal error: unable to execute QEMU command 'migrate': Can't migrate this vm with hardware poisoned memory, please reboot the vm and try again",
		// The words in a line of another program, and in the kernel's.
		"Nov 15 10:47:00 hv bash[1]: Failed adding this event: not a BMC",
	)
	c, rep := collectText(t, text, syslogOpts)
	if len(c.Actions) != 0 || len(errorsOf(c.Events)) != 0 {
		t.Fatalf("records %+v %+v", c.Actions, c.Events)
	}
	for _, want := range []string{
		`sel-logger of the BMC could not add 2 records to its event log ("Failed adding this event"): the event log lacks them`,
		`QEMU refused 1 migration of a virtual machine because its memory holds poisoned pages ("Can't migrate this vm with hardware poisoned memory"), the latest on 2026-11-15 10:46 UTC`,
	} {
		if !hasNote(rep, want) {
			t.Errorf("notes %q lack %q", rep.Notes, want)
		}
	}
	// The journal of a BMC: standard error of sel-logger.
	c, rep = collectText(t, `{"MESSAGE":"Failed adding this event: x","_TRANSPORT":"stdout","SYSLOG_IDENTIFIER":"sel-logger","__REALTIME_TIMESTAMP":"1794739440000000"}`+"\n", Options{})
	if !hasNote(rep, "could not add 1 record") || rep.Status != model.SourceOK {
		t.Errorf("journal: %+v", rep)
	}
}

// The kernel log of a lab guest after three uncorrected machine checks
// (testdata/real/7.3.0-mce): the third reached a program, and the kernel
// said so before it took the page offline.
func TestUserAccessOfARealCapture(t *testing.T) {
	c, rep, _ := collectFile(t, "../real/7.3.0-mce/dmesg", Options{})
	if rep.Status != model.SourceOK {
		t.Fatalf("status %s: %s", rep.Status, rep.Detail)
	}
	var got []string
	for _, a := range c.Actions {
		if a.Method == "hard" {
			got = append(got, a.Detail)
		}
	}
	want := []string{"dirty LRU page: Recovered", "dirty LRU page: Recovered", "dirty LRU page: Recovered; a read in user space consumed the error"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("details %q\nwant    %q", got, want)
	}
	if hasNote(rep, "reached a program") {
		t.Errorf("notes %q", rep.Notes)
	}
}
