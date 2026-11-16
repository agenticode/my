package klog

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// stopsOf returns the records of the ends of boots: stops and panics.
func stopsOf(evs []*model.Event) (stops, panics []*model.Event) {
	for _, e := range evs {
		switch e.ErrorType {
		case model.ErrorTypeStop:
			stops = append(stops, e)
		case model.ErrorTypePanic:
			panics = append(panics, e)
		}
	}
	return stops, panics
}

func errorsOf(evs []*model.Event) []*model.Event {
	var out []*model.Event
	for _, e := range evs {
		if e.ErrorSeverity.Class() != "" {
			out = append(out, e)
		}
	}
	return out
}

// The console of a guest whose kernel panicked on a fatal machine check
// (testdata/real/7.3.0-mce-fatal): the machine check record, then
// "Machine check: Processor context corrupt" and the panic line.
func TestStopOfARealFatalMachineCheck(t *testing.T) {
	path := testutil.Testdata(t, "real", "7.3.0-mce-fatal", "console.log")
	var c adapter.Collected
	rep := NewFile(path, Options{}).Collect(ctx, &c)
	if rep.Status != model.SourceOK {
		t.Fatalf("status %s: %s", rep.Status, rep.Detail)
	}
	errs := errorsOf(c.Events)
	stops, panics := stopsOf(c.Events)
	if len(errs) != 1 || len(stops) != 1 || len(panics) != 0 {
		t.Fatalf("%d errors, %d stops, %d panics", len(errs), len(stops), len(panics))
	}
	mce, st := errs[0], stops[0]
	if mce.ErrorSeverity != model.SevFatal || !strings.HasSuffix(mce.RawRef, "#L528-L530") {
		t.Errorf("machine check: %s %s", mce.ErrorSeverity, mce.RawRef)
	}
	if st.ErrorSeverity != model.SevInformational || st.Source != "klog/stop" || st.CauseFingerprint != mce.Fingerprint ||
		!strings.HasSuffix(st.RawRef, "#L532-L533") || st.Errors() != 0 {
		t.Errorf("stop: %+v", *st)
	}
	if st.Raw != "mce: [Hardware Error]: Machine check: Processor context corrupt\nKernel panic - not syncing: Fatal machine check" {
		t.Errorf("stop lines: %q", st.Raw)
	}
	// The boot starts at the record's own TIME field less its stamp; the
	// panic is 9 ms after the record.
	if d := st.ErrorDT.Sub(mce.ErrorDT); d < 0 || d > 20*1e6 || st.TimeQuality != model.TimeExact {
		t.Errorf("stop at %v (%s), record at %v", st.ErrorDT, st.TimeQuality, mce.ErrorDT)
	}
	if len(c.Boots) != 1 || !hasNote(rep, "1 boot ended in a kernel panic on a hardware error") {
		t.Errorf("%d boots, notes %q", len(c.Boots), rep.Notes)
	}
}

func fatalGHES(seq int, at float64, addr uint64) string {
	var b strings.Builder
	for _, l := range []string{
		fmt.Sprintf("{%d}[Hardware Error]: Hardware error from APEI Generic Hardware Error Source: 1", seq),
		fmt.Sprintf("{%d}[Hardware Error]: event severity: fatal", seq),
		fmt.Sprintf("{%d}[Hardware Error]:  Error 0, type: fatal", seq),
		fmt.Sprintf("{%d}[Hardware Error]:  fru_text: DIMM A1", seq),
		fmt.Sprintf("{%d}[Hardware Error]:   section_type: memory error", seq),
		fmt.Sprintf("{%d}[Hardware Error]:   physical_address: 0x%016x", seq, addr),
		fmt.Sprintf("{%d}[Hardware Error]:   node:0 card:0 module:0 rank:0 bank_group:1 bank_address:2 device:7 row:6703 column:16", seq),
		fmt.Sprintf("{%d}[Hardware Error]:   error_type: 3, multi-bit ECC", seq),
	} {
		fmt.Fprintf(&b, "[%11.6f] %s\n", at, l)
		at += 0.000002
	}
	return b.String()
}

func mceBlockText(at float64, status uint64, addr uint64, unix int64) string {
	return fmt.Sprintf(`[%11.6f] mce: [Hardware Error]: CPU 0: Machine Check Exception: 4 Bank 7: %016x
[%11.6f] mce: [Hardware Error]: TSC 347b319351 ADDR %x MISC 86
[%11.6f] mce: [Hardware Error]: PROCESSOR 0:50654 TIME %d SOCKET 0 APIC 0 microcode 1
[%11.6f] mce: [Hardware Error]: Run the above through 'mcelog --ascii'
`, at, status, at+0.001, addr, at+0.002, unix, at+0.003)
}

const bootHead = "[    0.000000] Linux version 7.3.0-ras (builder@lab) #1 SMP\n"

func TestStopLinesWrittenFromTheSource(t *testing.T) {
	const memUE = 0xbe00000000010090 // bank 7, memory read error, uncorrected
	const busUE = 0xbe00000000000e0b // a bus error: no memory error
	cases := []struct {
		name  string
		text  string
		stops int
		cause bool // the stop is tied to the error record before it
		other int  // panics for another reason
		note  string
	}{
		{"fatal machine check", bootHead + mceBlockText(58.25, memUE, 0x12345040, 1794739502) +
			"[   58.260571] mce: [Hardware Error]: Machine check: Processor context corrupt\n" +
			"[   58.261381] Kernel panic - not syncing: Fatal machine check\n" +
			"[   58.275447] ---[ end Kernel panic - not syncing: Fatal machine check ]---\n", 1, true, 0, ""},
		{"firmware error of Linux 7.3", bootHead + fatalGHES(1, 100.5, 0x100010140) +
			"[  100.510000] GHES: Fatal hardware error but panic disabled\n" +
			"[  100.510010] Kernel panic - not syncing: GHES: Fatal hardware error\n", 1, true, 0, ""},
		{"firmware error of Linux 5.15", bootHead + fatalGHES(3, 100.5, 0x100010140) +
			"[  100.510010] Kernel panic - not syncing: Fatal hardware error!\n", 1, true, 0, ""},
		{"machine check that is no memory error", bootHead + mceBlockText(58.25, busUE, 0, 1794739502) +
			"[   58.261381] Kernel panic - not syncing: Fatal machine check\n", 1, false, 0, ""},
		{"no record before the panic", bootHead +
			"[   58.261381] Kernel panic - not syncing: Fatal machine check from unknown source\n", 1, false, 0, ""},
		{"timeout while handling a machine check", bootHead +
			"[   58.261381] Kernel panic - not syncing: Timeout: Not all CPUs entered broadcast exception handler\n", 1, false, 0, ""},
		{"text of Linux 3.10", bootHead + mceBlockText(58.25, memUE, 0x12345040, 1794739502) +
			"[   58.261381] Kernel panic - not syncing: Fatal Machine check\n", 1, true, 0, ""},
		{"the end line only", bootHead + mceBlockText(58.25, memUE, 0x12345040, 1794739502) +
			"[   58.275447] ---[ end Kernel panic - not syncing: Fatal machine check ]---\n", 1, true, 0, ""},
		{"the severity line only: the console was cut", bootHead + mceBlockText(58.25, memUE, 0x12345040, 1794739502) +
			"[   58.260571] mce: [Hardware Error]: Machine check: Processor context corrupt\n", 1, true, 0, ""},
		{"fake panic", bootHead + mceBlockText(58.25, memUE, 0x12345040, 1794739502) +
			"[   58.260571] mce: [Hardware Error]: Machine check: Processor context corrupt\n" +
			"[   58.260600] mce: [Hardware Error]: Fake kernel panic: Fatal machine check\n", 0, false, 0, "fake panic mode"},
		{"panic for another reason", bootHead +
			"[    2.500000] Kernel panic - not syncing: VFS: Unable to mount root fs on unknown-block(0,0)\n" +
			"[    2.500100] ---[ end Kernel panic - not syncing: VFS: Unable to mount root fs on unknown-block(0,0) ]---\n", 0, false, 1,
			"1 boot ended in a kernel panic for another reason"},
		{"empty reason", bootHead + "[    2.500000] Kernel panic - not syncing: \n", 0, false, 1, ""},
		{"record far before the panic", bootHead + mceBlockText(58.25, memUE, 0x12345040, 1794739502) +
			strings.Repeat("[   59.000000] EDAC MC0: Giving out device to module ghes_edac\n", stopReach+5) +
			"[   60.000000] Kernel panic - not syncing: Fatal machine check\n", 1, false, 0, ""},
		{"corrected error before the panic", bootHead + mceBlockText(58.25, 0x9c00000000010090, 0x12345040, 1794739502) +
			"[   60.000000] Kernel panic - not syncing: Fatal machine check\n", 1, false, 0, ""},
		{"the severity line many times", bootHead +
			strings.Repeat("[   58.260571] mce: [Hardware Error]: Machine check: Processor context corrupt\n", 5000), 1, false, 0, ""},
		{"record held until the end of its boot (TSC 0, carried.go rule 2)", bootHead +
			strings.Replace(mceBlockText(58.25, memUE, 0x12345040, 1794739502), "TSC 347b319351", "TSC 0", 1) +
			"[   58.261381] Kernel panic - not syncing: Fatal machine check\n", 1, true, 0, ""},
		{"panic text in the middle of a line", bootHead +
			"[    5.000000] audit: comm=\"x\" Kernel panic - not syncing: Fatal machine check\n", 0, false, 0, ""},
		{"caller ID before the panic line", bootHead +
			"[   58.261381][    C0] Kernel panic - not syncing: Fatal machine check\n", 1, false, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rep := collectText(t, tc.text, Options{})
			if rep.Status == model.SourceError || rep.Status == model.SourceUnreadable {
				t.Fatalf("status %s: %s", rep.Status, rep.Detail)
			}
			stops, panics := stopsOf(c.Events)
			if len(stops) != tc.stops || len(panics) != tc.other {
				t.Fatalf("%d stops, %d other panics; events %d", len(stops), len(panics), len(c.Events))
			}
			for _, st := range stops {
				var cause *model.Event
				for _, e := range c.Events {
					if e.Fingerprint == st.CauseFingerprint {
						cause = e
					}
				}
				if (st.CauseFingerprint != "") != tc.cause || (tc.cause && (cause == nil || !cause.ErrorSeverity.Uncorrected())) {
					t.Errorf("cause %q (%v), want one: %v", st.CauseFingerprint, cause, tc.cause)
				}
				if st.Raw == "" || st.Errors() != 0 || strings.Count(st.Raw, "\n") >= maxStopLines {
					t.Errorf("stop: %+v", *st)
				}
			}
			for _, p := range panics {
				// The text of another panic is not kept.
				if p.Raw != "" || p.Source != "klog/panic" || p.ErrorSeverity != model.SevInformational {
					t.Errorf("panic: %+v", *p)
				}
			}
			if tc.note != "" && !hasNote(rep, tc.note) {
				t.Errorf("notes %q lack %q", rep.Notes, tc.note)
			}
		})
	}
}

// A stop belongs to its boot; the next boot of the same log goes on.
func TestStopEndsItsBoot(t *testing.T) {
	text := bootHead + mceBlockText(58.25, 0xbe00000000010090, 0x12345040, 1794739502) +
		"[   58.261381] Kernel panic - not syncing: Fatal machine check\n" +
		bootHead + "[   30.000000] mce: [Hardware Error]: Machine check events logged\n" +
		"[   40.000000] Kernel panic - not syncing: sysrq triggered crash\n"
	c, _ := collectText(t, text, Options{})
	stops, panics := stopsOf(c.Events)
	if len(stops) != 1 || len(panics) != 1 || len(c.Boots) != 2 {
		t.Fatalf("%d stops, %d panics, %d boots", len(stops), len(panics), len(c.Boots))
	}
	// The first boot is placed by the record's own time, the second not at
	// all: its records stay under a boot of their own.
	if stops[0].BootID != "" || panics[0].BootID == "" || panics[0].TimeQuality != model.TimeRelative {
		t.Errorf("stop boot %q, panic boot %q (%s)", stops[0].BootID, panics[0].BootID, panics[0].TimeQuality)
	}
}

// A line of another program that quotes the text is no stop.
func TestStopTextOfAnotherProgram(t *testing.T) {
	text := "Nov 15 10:44:00 lab kernel: [   10.000000] Linux version 7.3.0-ras (b@l) #1 SMP\n" +
		"Nov 15 10:45:00 lab logger: Kernel panic - not syncing: Fatal machine check\n"
	c, _ := collectText(t, text, Options{})
	if stops, panics := stopsOf(c.Events); len(stops)+len(panics) != 0 {
		t.Errorf("%d stops, %d panics", len(stops), len(panics))
	}
}
