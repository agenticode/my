package klog

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/model"
)

// Records the next boot prints of what the boot before left: the contents
// of a machine check bank that survived the reset, and the boot error
// region of the firmware. Both are "carried": the error happened in the
// boot before the one that printed it (store.LinkCarried).

// MCi_STATUS bits (arch/x86/include/asm/mce.h).
const (
	statusUC  = 1 << 61 // MCI_STATUS_UC: uncorrected
	statusPCC = 1 << 57 // MCI_STATUS_PCC: processor context corrupt
	statusS   = 1 << 56 // MCI_STATUS_S: signaled as a machine check exception
)

// carry says whether a machine check record was in a bank before the boot
// that printed it.
type carry int

const (
	carryNo carry = iota
	carryYes
	// carryIfGoesOn: rule 2 below; known at the end of the boot.
	carryIfGoesOn
)

// carriedMCE applies the two rules to a machine check record as
// __print_mce printed it (arch/x86/kernel/cpu/mce/core.c).
//
// Rule 1. The head line has no " Exception": the record was polled
// (MCG_STATUS.MCIP is clear), and its status is uncorrected with the
// processor context corrupt, or uncorrected and signaled. A poll of the
// running boot does not log such a record: should_log_poll_error logs an
// uncorrected record only when the poll has MCP_UC, which is the poll of
// every bank at CPU bring-up (__mcheck_cpu_init_prepare_banks, "Log the
// machine checks left over from the previous reset"), or when it is
// UCNA (uncorrected, no PCC, no S) or not enabled
// (ser_should_log_poll_error). So such a record was in the bank before
// this boot. On AMD processors with SMCA the poll logs every valid record
// (smca_should_log_poll_error); a bank that holds an uncorrected record
// with PCC raises a machine check before a poll sees it, so the rule holds
// there as far as the source tells.
//
// Rule 2, for platforms whose MCG_STATUS keeps MCIP over the reset (not
// shown by the lab, where QEMU clears MCG_STATUS at reset): the head has
// " Exception", the status has PCC and the TSC is 0, and the boot goes on
// after the record. A record with PCC raised in this boot ends it: the
// kernel panics within the second (mce_panic). The decision waits for the
// end of the boot (closeCarried).
func carriedMCE(exception bool, status, tsc uint64) carry {
	uc := status&statusUC != 0
	switch {
	case !exception && uc && (status&statusPCC != 0 || status&statusS != 0):
		return carryYes
	case exception && status&statusPCC != 0 && tsc == 0:
		return carryIfGoesOn
	}
	return carryNo
}

// goesOnAfter is how long a boot must go on after a record of rule 2 for
// the record to count as carried. A panic follows the record of a fatal
// machine check within the same second.
const goesOnAfter = 10 * time.Second

// heldMCE is a record of rule 2 that waits for the end of its boot.
type heldMCE struct {
	ev   *model.Event
	line line
	unix int64
}

// carryFingerprint is the extra of the fingerprint of a carried machine
// check record. The kernel's stamp and text identify a line; two boots
// that print the same leftover record at the same microsecond after boot
// would be one record. The TIME field, which is the time the boot read
// the bank, tells them apart. Records that are not carried keep the
// fingerprint they always had.
func carryFingerprint(unix int64) string { return "carried " + strconv.FormatInt(unix, 10) }

// holdRule2 keeps a record of rule 2 until the end of its boot.
func (p *parser) holdRule2(e *model.Event, l *line, unix int64) error {
	p.seg.held = append(p.seg.held, heldMCE{ev: e, line: *l, unix: unix})
	return nil
}

// closeCarried decides the records of rule 2 at the end of a boot and
// sends them on.
func (p *parser) closeCarried(seg *segment) error {
	held := seg.held
	seg.held = nil
	for _, h := range held {
		l := h.line
		if l.hasMono && seg.hasMono && seg.lastMono-l.mono >= goesOnAfter {
			h.ev.Carried = true
			h.ev.Fingerprint = p.fingerprint("mce", &l, carryFingerprint(h.unix))
		}
		if err := p.emit(h.ev, nil, &l); err != nil {
			return err
		}
	}
	return nil
}

// The lines the kernel prints of the boot error region at boot
// (drivers/acpi/apei/bert.c, bert_print_all): "BERT: Error records from
// previous boot:", then each record as cper_estatus_print prints it with
// the prefix "[Hardware Error]: " and no "{N}" tag of a GHES source, then
// "BERT: Total records found: N", or before it "[Hardware Error]: Skipped
// N error records".
const (
	bertHead    = "BERT: Error records from previous boot:"
	bertTotal   = "BERT: Total records found:"
	bertSkipped = "Skipped"
)

// bertBlock is what the kernel printed of the boot error region.
type bertBlock struct {
	first line
	lastN int
	body  []string
	raw   []string
}

// bertLine takes a line of the boot error region. It reports whether the
// line was one.
func (p *parser) bertLine(l *line) (bool, error) {
	msg := l.msg
	switch {
	case strings.Contains(msg, bertHead):
		if err := p.flushBERT(); err != nil {
			return true, err
		}
		p.bert = &bertBlock{first: *l, lastN: l.n, raw: []string{msg}}
		return true, nil
	case p.bert == nil:
		return false, nil
	case strings.Contains(msg, bertTotal):
		p.bert.raw = append(p.bert.raw, msg)
		return true, p.flushBERT()
	case strings.HasPrefix(msg, hwErrMark):
		body := strings.TrimPrefix(msg, hwErrMark)
		p.bert.raw = append(p.bert.raw, msg)
		p.bert.lastN = l.n
		if !strings.HasPrefix(strings.TrimSpace(body), bertSkipped) {
			p.bert.body = append(p.bert.body, body)
		}
		return true, nil
	}
	// Any other line ends the block.
	return false, p.flushBERT()
}

// flushBERT turns the memory error sections of the boot error region the
// kernel printed into carried events. The region holds one Generic Error
// Status Block after another; each starts with its event severity.
func (p *parser) flushBERT() error {
	b := p.bert
	if b == nil {
		return nil
	}
	p.bert = nil
	var blocks [][]string
	prevCorrected := false
	for _, line := range b.body {
		// cper_estatus_print starts a block with its severity, and a
		// corrected block with a sentence before it.
		corrected := strings.Contains(line, "It has been corrected by h/w")
		if len(blocks) == 0 || corrected || (ghesEventSev.MatchString(line) && !prevCorrected) {
			blocks = append(blocks, nil)
		}
		blocks[len(blocks)-1] = append(blocks[len(blocks)-1], line)
		prevCorrected = corrected
	}
	ref := p.refRange(&b.first, b.lastN)
	for bi, body := range blocks {
		eventSev, sections := splitGHES(body)
		for si := range sections {
			s := &sections[si]
			m, found := s.memError()
			if s.typ != "memory error" && !(s.typ == "" && found) {
				p.otherSections++
				continue
			}
			p.records++
			p.seg.records++
			sevText := s.severity
			if sevText == "" {
				sevText = eventSev
			}
			sev, ok := cper.SeverityFromText(sevText)
			if !ok {
				p.tally.Skip(ref, fmt.Sprintf("boot error record has no readable severity (%q)", sevText))
				continue
			}
			e := &model.Event{
				Kind: model.KindError, ErrorSeverity: sev, Source: Kind + "/bert", Decoder: "cper", Count: 1, Carried: true,
				RawRef: ref, Raw: strings.Join(b.raw, "\n"),
				Fingerprint: p.fingerprint("bert", &b.first, strconv.Itoa(bi)+"/"+strconv.Itoa(si)),
			}
			m.Apply(e)
			if e.Label == "" {
				e.Label = s.fruText
			}
			if err := p.emit(e, nil, &b.first); err != nil {
				return err
			}
		}
	}
	return nil
}
