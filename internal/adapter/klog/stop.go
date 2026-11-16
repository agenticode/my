package klog

import (
	"fmt"
	"strings"

	"github.com/agenticode/my/internal/model"
)

// The lines that say the kernel stops the host. A fatal machine check or a
// fatal firmware error ends in a panic, and the boot ends there:
//
//	mce: [Hardware Error]: CPU 0: Machine Check Exception: 4 Bank 7: be00000000010090
//	mce: [Hardware Error]: TSC 347b319351 ADDR 12345040 MISC 86
//	mce: [Hardware Error]: PROCESSOR 0:50654 TIME 1794739502 SOCKET 0 APIC 0 microcode 1
//	mce: [Hardware Error]: Run the above through 'mcelog --ascii'
//	mce: [Hardware Error]: Machine check: Processor context corrupt
//	Kernel panic - not syncing: Fatal machine check
//
// The panic line is printed by vpanic() in kernel/panic.c as
// "Kernel panic - not syncing: %s", and at its end as
// "---[ end Kernel panic - not syncing: %s ]---". The reason is the text
// the caller gave panic().
const (
	panicMark    = "Kernel panic - not syncing: "
	panicEndMark = "---[ end Kernel panic - not syncing: "
)

// mceStopTexts are the reasons arch/x86/kernel/cpu/mce/core.c (Linux 7.3)
// gives mce_panic(), which prints the pending records, then
// "Machine check: <severity text>", then calls panic() with the reason.
var mceStopTexts = map[string]bool{
	"Fatal machine check":                                             true, // mce_reign
	"Fatal machine check from unknown source":                         true, // mce_reign
	"Fatal local machine check":                                       true, // do_machine_check
	"Fatal machine check on current CPU":                              true, // do_machine_check
	"Local fatal machine check!":                                      true, // do_machine_check
	"Failed kernel mode recovery":                                     true, // do_machine_check
	"Too many consecutive machine checks while accessing user data":   true, // queue_task_work
	"Consecutive machine checks to different user pages":              true, // queue_task_work
	"Timeout: Not all CPUs entered broadcast exception handler":       true, // mce_start, through mce_timed_out
	"Timeout: Subject CPUs unable to finish machine check processing": true, // mce_start, through mce_timed_out
	"Timeout: Monarch CPU unable to finish machine check processing":  true, // mce_end, through mce_timed_out
	"Timeout: Monarch CPU did not finish machine check processing":    true, // mce_end, through mce_timed_out
	"Panicing machine check CPU died":                                 true, // wait_for_panic, calls panic() itself
	"MCA architectural violation!":                                    true, // ex_handler_msr_mce, calls panic() itself
}

// ghesStopTexts are the reasons __ghes_panic() in drivers/acpi/apei/ghes.c
// gives panic() after it printed the fatal firmware error record:
// "Fatal hardware error!" up to Linux 6.x (5.15 has it), GHES_PFX
// "Fatal hardware error" in 7.3. When the kernel does not reboot after a
// panic (panic_timeout 0), 7.3 first prints "GHES: Fatal hardware error but
// panic disabled" and panics all the same.
var ghesStopTexts = map[string]bool{
	"Fatal hardware error!":      true,
	"GHES: Fatal hardware error": true,
}

const ghesNoRebootText = "GHES: Fatal hardware error but panic disabled"

// stopReach is how many lines before the stop the error record that caused
// it may lie. mce_panic() prints the pending records and the final one
// last, then two lines; __ghes_panic() prints the record and panics.
const stopReach = 100

// lastRecord is the latest error record of a boot.
type lastRecord struct {
	fp      string
	n       int // line number when it was sent
	ue      bool
	carried bool
}

// stopRec is the end of a boot the kernel printed: a stop on a hardware
// error, or another panic.
type stopRec struct {
	first    line
	lastN    int
	raw      []string
	hardware bool
	panicked bool   // the panic line itself was seen
	cause    string // fingerprint of the error record before it
}

// stopKind tells what a kernel line says about the end of the boot.
type stopKind int

const (
	notStop stopKind = iota
	stopHardware
	stopOther // a panic for a reason that is not a hardware error
)

// startsWith reports whether msg starts with mark, after the caller ID a
// kernel built with CONFIG_PRINTK_CALLER puts behind the stamp
// ("[    C0] ", "[  T123] "), and returns the rest.
func startsWith(msg, mark string) (string, bool) {
	if strings.HasPrefix(msg, "[") {
		if j := strings.IndexByte(msg, ']'); j > 1 && j <= 12 {
			id := strings.TrimSpace(msg[1:j])
			if len(id) >= 2 && (id[0] == 'C' || id[0] == 'T') && strings.Trim(id[1:], "0123456789") == "" {
				msg = strings.TrimLeft(msg[j+1:], " ")
			}
		}
	}
	if !strings.HasPrefix(msg, mark) {
		return "", false
	}
	return msg[len(mark):], true
}

// panicReason returns the reason of a panic line and whether msg is one.
func panicReason(msg string) (string, bool) {
	if rest, ok := startsWith(msg, panicEndMark); ok {
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "]---")), true
	}
	if rest, ok := startsWith(msg, panicMark); ok {
		return strings.TrimSpace(rest), true
	}
	return "", false
}

// classifyPanic says whether the reason of a panic is a hardware error.
// Besides the texts of Linux 7.3, a reason that names a machine check is
// one: older kernels wrote "Fatal Machine check" and "Machine check from
// unknown source".
func classifyPanic(reason string) stopKind {
	if mceStopTexts[reason] || ghesStopTexts[reason] || strings.Contains(strings.ToLower(reason), "machine check") {
		return stopHardware
	}
	return stopOther
}

// noteLast remembers the latest error record of the boot, which a stop
// that follows it is tied to.
func (p *parser) noteLast(e *model.Event) {
	if e == nil || e.ErrorSeverity.Class() == "" || p.seg == nil {
		return
	}
	p.seg.last = &lastRecord{fp: e.Fingerprint, n: p.lineNo, ue: e.ErrorSeverity.Uncorrected(), carried: e.Carried}
}

// forgetLast is called for an error record that is not kept, such as a
// machine check that is not a memory error: a stop after it is not tied to
// an earlier record.
func (p *parser) forgetLast() {
	if p.seg != nil {
		p.seg.last = nil
	}
}

// stopLine handles a kernel line that is not a "[Hardware Error]" line and
// may say that the boot ends. done is false for any other line.
func (p *parser) stopLine(l *line) (done bool, err error) {
	msg := l.msg
	kind := notStop
	if reason, ok := panicReason(msg); ok {
		kind = classifyPanic(reason)
	} else if _, ok := startsWith(msg, ghesNoRebootText); ok {
		kind = stopHardware
	}
	if kind == notStop {
		return false, nil
	}
	// The firmware error record printed before the panic is complete now.
	if err := p.flushGHES(); err != nil {
		return true, err
	}
	_, noReboot := startsWith(msg, ghesNoRebootText)
	p.stopAt(l, kind == stopHardware, !noReboot)
	return true, nil
}

// isMCEStopBody reports whether the body of a "[Hardware Error]" line is
// one that mce_panic() prints after the records: "Machine check: <severity
// text>" before the panic, or "Fake kernel panic: <reason>" in place of it
// when the machine check code runs with fake_panic set (a debugfs setting
// for tests), where the host goes on.
func isMCEStopBody(body string) bool {
	return strings.HasPrefix(body, "Machine check: ") || strings.HasPrefix(body, "Fake kernel panic: ")
}

// mceStopLine handles a line isMCEStopBody accepts.
func (p *parser) mceStopLine(l *line, body string) error {
	if err := p.flushMCE(); err != nil {
		return err
	}
	if !strings.HasPrefix(body, "Fake kernel panic: ") {
		p.stopAt(l, true, false)
		return nil
	}
	if st := p.seg.stop; st != nil && st.hardware && !st.panicked {
		p.seg.stop = nil
	}
	p.fakePanics++
	return nil
}

// stopAt adds a line to the end of the current boot.
func (p *parser) stopAt(l *line, hardware, panicLine bool) {
	seg := p.seg
	st := seg.stop
	if st == nil {
		st = &stopRec{first: *l}
		seg.stop = st
	}
	st.lastN = l.n
	st.panicked = st.panicked || panicLine
	if hardware && !st.hardware {
		st.hardware = true
		if c := seg.last; c != nil && c.ue && !c.carried && l.n-c.n <= stopReach {
			st.cause = c.fp
		}
	}
	if hardware && len(st.raw) < maxStopLines {
		st.raw = append(st.raw, l.msg)
	}
}

// maxStopLines bounds the lines a stop record keeps. The kernel prints two
// or three; a log that repeats them is not kept in full.
const maxStopLines = 8

// closeStop sends the end of the boot as a record. A stop on a hardware
// error keeps the kernel's lines and names the error record before it;
// another panic keeps no text, whose reason can be about anything on the
// host (the ledger keeps lines about memory errors and machine checks
// only). Neither counts in any total.
func (p *parser) closeStop() error {
	seg := p.seg
	st := seg.stop
	if st == nil {
		return nil
	}
	seg.stop = nil
	p.records++
	seg.records++
	e := &model.Event{
		Kind: model.KindError, ErrorSeverity: model.SevInformational, Count: 1,
		RawRef: p.refRange(&st.first, st.lastN),
	}
	if st.hardware {
		e.ErrorType, e.Source, e.Raw, e.CauseFingerprint = model.ErrorTypeStop, Kind+"/stop", strings.Join(st.raw, "\n"), st.cause
		e.Fingerprint = p.fingerprint("stop", &st.first, "")
		p.stops++
	} else {
		e.ErrorType, e.Source = model.ErrorTypePanic, Kind+"/panic"
		e.Fingerprint = p.fingerprint("panic", &st.first, "")
		p.panics++
	}
	return p.emit(e, nil, &st.first)
}

// stopNotes says how many boots of the log ended in a panic.
func (p *parser) stopNotes() []string {
	var out []string
	if p.stops > 0 {
		out = append(out, fmt.Sprintf("%d %s in a kernel panic on a hardware error", p.stops, plural(p.stops, "boot ended", "boots ended")))
	}
	if p.panics > 0 {
		out = append(out, fmt.Sprintf("%d %s in a kernel panic for another reason", p.panics, plural(p.panics, "boot ended", "boots ended")))
	}
	if p.fakePanics > 0 {
		out = append(out, fmt.Sprintf("%d machine check %s printed in fake panic mode, a test setting of the kernel: the host went on running", p.fakePanics, plural(p.fakePanics, "panic was", "panics were")))
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
