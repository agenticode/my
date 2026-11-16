package klog

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/model"
)

// Lines that say why a page went offline, which program consumed an
// uncorrected error, that records are missing, or that poison reached a
// virtual machine. Some are lines of the kernel; the others are lines of
// other programs, which a syslog file or a journal export holds between
// the kernel's lines.

// The mcelog daemon (github.com/andikleen/mcelog) logs to syslog under the
// name "mcelog" (msg.c, opensyslog):
//
//	Corrected memory errors on page 12345000 exceed threshold 10 in 24h: 10 in 24h
//	Offlining page 12345000
//	Offlining page 12345000 failed: Device or resource busy
//	Soft offlining of page 12345000 failed, trying hard offlining
//
// page.c, account_page_error: "Corrected memory errors on page %llx exceed
// threshold %s", to which memdb.c, memdb_trigger, adds ": %s"; the
// threshold is "%u in %u%c" (leaky-bucket.c, bucket_output). The number
// is the physical address of the page. page.c, offline_action: "Offlining
// page %llx" before it writes the address to soft_offline_page or
// hard_offline_page, "Offlining page %llx failed: %s" when that fails;
// memory_offline: "Soft offlining of page %llx failed, trying hard
// offlining" with memory-ce-action = soft-then-hard. The kernel prints
// nothing when a soft offline succeeds, so the line of mcelog is the only
// record of it.
var (
	mcelogThreshRE   = regexp.MustCompile(`^Corrected memory errors on page ([0-9a-fA-F]{1,16}) exceed threshold (\d{1,10} in \d{1,10}[smhd])`)
	mcelogOfflineRE  = regexp.MustCompile(`^Offlining page ([0-9a-fA-F]{1,16})(?: failed: (.*))?$`)
	mcelogSoftHardRE = regexp.MustCompile(`^Soft offlining of page ([0-9a-fA-F]{1,16}) failed, trying hard offlining`)
)

// selLostText: phosphor-sel-logger of OpenBMC, src/sel_logger.cpp,
// selAddOemRecord, when the record could not be handed to the logging
// service (SEL_LOGGER_SEND_TO_LOGGING_SERVICE): "Failed adding this event:
// <error>" to standard error, which the journal of the BMC keeps under the
// name of the program, sel-logger. The record is not in the event log.
const selLostText = "Failed adding this event"

// qemuPoisonText: QEMU, migration/migration.c, migrate_prepare, refuses
// to migrate a guest whose memory holds a page that the host's kernel
// reported as poisoned (kvm_hwpoisoned_mem). libvirt logs it as "unable to
// execute QEMU command 'migrate': <text>" (src/qemu/qemu_monitor_json.c).
const qemuPoisonText = "Can't migrate this vm with hardware poisoned memory"

// Lines of the kernel about the program an uncorrected error reached:
//
//	mce: Uncorrected hardware memory error in user-access at 12345040
//	Memory failure: 0x12345: Sending SIGBUS to python3:4242 due to hardware memory corruption
//	Memory failure: 0x12345: recovery action for dirty LRU page: Recovered
//
// arch/x86/kernel/cpu/mce/core.c, kill_me_maybe: "Uncorrected hardware
// memory error in user-access at %llx" (a program read the poison);
// kill_me_never: "Kernel accessed poison in user space at %llx" (the
// kernel read it while copying from the program's memory). Both name the
// physical address, and memory_failure() for its page follows.
// mm/memory-failure.c, kill_proc: "%#lx: Sending SIGBUS to %s:%d due to
// hardware memory corruption"; kill_procs: "%#lx: forcibly killing %s:%d
// because of failure to unmap corrupted page".
var (
	consumedRE = regexp.MustCompile(`mce: (Uncorrected hardware memory error in user-access|Kernel accessed poison in user space) at ([0-9a-fA-F]{1,16})\s*$`)
	sigbusRE   = regexp.MustCompile(`Memory failure: (0x[0-9a-fA-F]{1,16}): (?:Sending SIGBUS to|forcibly killing) (.{1,16}):(\d{1,10}) (?:due to hardware memory corruption|because of failure to unmap)`)
)

// consumer is what the kernel said about the program an uncorrected error
// of a page reached, before the page offline record of that page.
type consumer struct {
	n     int    // line number of the latest line
	how   string // "user" or "copy", "" when only a signal was seen
	procs []string
}

// maxPending bounds the pages that wait for a line about them.
const maxPending = 512

// detail is the part of a page offline detail a consumer gives.
func (c *consumer) detail() string {
	var parts []string
	switch c.how {
	case "user":
		parts = append(parts, "a read in user space consumed the error")
	case "copy":
		parts = append(parts, "the kernel consumed the error copying from user memory")
	}
	if len(c.procs) > 0 {
		parts = append(parts, "SIGBUS to "+strings.Join(c.procs, ", "))
	}
	return strings.Join(parts, "; ")
}

// consumerLine handles the kernel's lines about the program an error
// reached. done is false for any other line.
func (p *parser) consumerLine(l *line) bool {
	seg := p.seg
	if m := consumedRE.FindStringSubmatch(l.msg); m != nil {
		p.records++
		addr, err := strconv.ParseUint(m[2], 16, 64)
		if err != nil || addr>>52 != 0 {
			p.tally.Skip(p.ref(l), "address of an uncorrected error in user space is out of range")
			return true
		}
		if c := p.consumerOf(addr>>seg.pageShift, l.n); c != nil {
			c.how = "user"
			if strings.HasPrefix(m[1], "Kernel") {
				c.how = "copy"
			}
		}
		return true
	}
	if m := sigbusRE.FindStringSubmatch(l.msg); m != nil {
		p.records++
		pfn, err := strconv.ParseUint(strings.TrimPrefix(m[1], "0x"), 16, 44)
		if err != nil {
			p.tally.Skip(p.ref(l), "page of a signal line is out of range")
			return true
		}
		if c := p.consumerOf(pfn, l.n); c != nil {
			proc := strings.TrimSpace(m[2]) + ":" + m[3]
			if len(c.procs) < 4 && !contains(c.procs, proc) {
				c.procs = append(c.procs, proc)
			}
		}
		return true
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// consumerOf returns the consumer of a page in the current boot, or nil
// when too many pages wait already.
func (p *parser) consumerOf(pfn uint64, n int) *consumer {
	seg := p.seg
	if seg.consumed == nil {
		seg.consumed = map[uint64]*consumer{}
	}
	c := seg.consumed[pfn]
	if c == nil {
		if len(seg.consumed) >= maxPending {
			p.expireConsumers(n)
		}
		if len(seg.consumed) >= maxPending {
			p.tally.Skip(fmt.Sprintf("%s#L%d", p.origin, n), "too many pages wait for their page offline record")
			return nil
		}
		c = &consumer{}
		seg.consumed[pfn] = c
	}
	c.n = n
	return c
}

// expireConsumers drops the consumers whose page offline record did not
// follow; those of an uncorrected error are counted for a note.
func (p *parser) expireConsumers(n int) {
	for pfn, c := range p.seg.consumed {
		if n < 0 || n-c.n > injectionReach {
			if c.how != "" {
				p.unoffered++
			}
			delete(p.seg.consumed, pfn)
		}
	}
}

// tieConsumer adds to the detail of a page offline what the kernel said
// about the program the error of the page reached.
func (p *parser) tieConsumer(o *offline, n int) {
	c := p.seg.consumed[o.pfn]
	if c == nil {
		return
	}
	delete(p.seg.consumed, o.pfn)
	if n-c.n > injectionReach {
		if c.how != "" {
			p.unoffered++
		}
		return
	}
	if d := c.detail(); d != "" {
		o.detail += "; " + d
	}
}

// mcelogPage is what the lines of mcelog said about one page.
type mcelogPage struct {
	why   string // the threshold that was crossed
	act   *model.Action
	n     int  // line number of the latest line
	taken bool // a record of the kernel took the reason
}

// daemonLine handles a line of a program other than the kernel.
func (p *parser) daemonLine(l *line) error {
	switch {
	case l.prog == "mcelog":
		return p.mcelogLine(l)
	case strings.Contains(l.prog, "sel-logger") && strings.HasPrefix(l.msg, selLostText):
		p.records++
		p.selLost++
	case strings.Contains(l.msg, qemuPoisonText):
		p.records++
		p.poisonVM++
		if l.wall.After(p.poisonLast) {
			p.poisonLast = l.wall
		}
	}
	return nil
}

// mcelogLine handles a line of the mcelog daemon.
func (p *parser) mcelogLine(l *line) error {
	var m []string
	kind := ""
	for _, try := range []struct {
		re   *regexp.Regexp
		kind string
	}{{mcelogThreshRE, "threshold"}, {mcelogOfflineRE, "offline"}, {mcelogSoftHardRE, "soft-then-hard"}} {
		if m = try.re.FindStringSubmatch(l.msg); m != nil {
			kind = try.kind
			break
		}
	}
	if kind == "" {
		return nil
	}
	p.records++
	addr, err := strconv.ParseUint(m[1], 16, 64)
	if err != nil || addr>>52 != 0 {
		p.tally.Skip(p.ref(l), "page address of mcelog is out of range")
		return nil
	}
	if err := p.expireMcelog(l.n, false); err != nil {
		return err
	}
	pg := p.mcelog[addr]
	if pg == nil {
		if len(p.mcelog) >= maxPending {
			if err := p.expireMcelog(-1, true); err != nil {
				return err
			}
		}
		if p.mcelog == nil {
			p.mcelog = map[uint64]*mcelogPage{}
		}
		pg = &mcelogPage{}
		p.mcelog[addr] = pg
	}
	pg.n = l.n
	switch kind {
	case "threshold":
		pg.why = "corrected errors exceed the threshold of mcelog, " + m[2]
	case "soft-then-hard":
		if pg.act != nil {
			pg.act.Detail += "; soft offline failed, hard offline tried"
			pg.act.Raw += "\n" + l.msg
		}
	case "offline":
		if pg.taken {
			// The kernel's record of this page has the reason already.
			return nil
		}
		if pg.act == nil {
			at, q, ok := p.daemonTime(l)
			if !ok {
				p.tally.Skip(p.ref(l), "line of mcelog has no date")
				return nil
			}
			pg.act = &model.Action{
				Kind: model.ActionPageOffline, Result: model.ResultUnknown, OsPhysicalAddress: model.Some(addr),
				Detail: "asked for by mcelog", Source: Kind + "/mcelog", RawRef: p.ref(l), Raw: l.msg,
				Fingerprint: p.fingerprint("mcelog", l, ""), At: at, TimeQuality: q, BootID: l.bootID,
			}
			if pg.why != "" {
				pg.act.Detail += ": " + pg.why
			}
		} else if m[2] != "" {
			pg.act.Raw += "\n" + l.msg
		}
		if m[2] != "" {
			pg.act.Result = model.ResultFailed
			pg.act.Detail += "; failed: " + strings.TrimSpace(m[2])
		}
	}
	return nil
}

// daemonTime is the time of a line of another program: its date, else its
// seconds since boot in a boot whose start is known.
func (p *parser) daemonTime(l *line) (time.Time, model.TimeQuality, bool) {
	switch {
	case !l.wall.IsZero():
		return l.wall, model.TimeExact, true
	case l.hasMono && p.seg != nil && p.seg.startQuality != "":
		return p.seg.start.Add(l.mono), p.seg.startQuality, true
	}
	return time.Time{}, "", false
}

// tieMcelog adds the reason mcelog gave to the kernel's record of a page
// offline of the same page: the kernel's record is the one kept.
func (p *parser) tieMcelog(o *offline, n int) {
	shift := p.seg.pageShift
	for addr, pg := range p.mcelog {
		if addr>>shift != o.pfn || n-pg.n > injectionReach || pg.taken {
			continue
		}
		o.detail += "; asked for by mcelog"
		if pg.why != "" {
			o.detail += ": " + pg.why
		}
		pg.act, pg.taken, pg.n = nil, true, n
		return
	}
}

// expireMcelog sends the page offlines of mcelog that no line continues
// any more; all of them when all is set.
func (p *parser) expireMcelog(n int, all bool) error {
	var due []uint64
	for addr, pg := range p.mcelog {
		if all || n-pg.n > injectionReach {
			due = append(due, addr)
		}
	}
	// In the order of the addresses, so that a read gives the same ledger
	// every time.
	slices.Sort(due)
	for _, addr := range due {
		pg := p.mcelog[addr]
		delete(p.mcelog, addr)
		switch {
		case pg.act != nil:
			if err := p.send(nil, pg.act, pg.act.At, pg.act.TimeQuality, pg.act.BootID); err != nil {
				return err
			}
		case pg.why != "" && !pg.taken:
			p.untiedThresholds++
		}
	}
	return nil
}

// daemonNotes says what the lines of this file say beyond the records.
func (p *parser) daemonNotes() []string {
	var out []string
	if p.untiedThresholds > 0 {
		out = append(out, fmt.Sprintf("mcelog: the corrected errors of %d %s exceeded its threshold, and no page offline record of %s is in this log",
			p.untiedThresholds, plural(p.untiedThresholds, "page", "pages"), plural(p.untiedThresholds, "it", "them")))
	}
	if p.unoffered > 0 {
		out = append(out, fmt.Sprintf("%d uncorrected %s reached a program or a copy from its memory, and no page offline record of %s is in this log",
			p.unoffered, plural(p.unoffered, "error", "errors"), plural(p.unoffered, "its page", "their pages")))
	}
	if p.selLost > 0 {
		out = append(out, fmt.Sprintf("sel-logger of the BMC could not add %d %s to its event log (%q): the event log lacks %s",
			p.selLost, plural(p.selLost, "record", "records"), selLostText, plural(p.selLost, "it", "them")))
	}
	if p.poisonVM > 0 {
		latest := ""
		if !p.poisonLast.IsZero() {
			latest = ", the latest on " + p.poisonLast.UTC().Format("2006-01-02 15:04 UTC")
		}
		out = append(out, fmt.Sprintf("QEMU refused %d %s of a virtual machine because its memory holds poisoned pages (%q)%s: an uncorrected error reached the memory of a guest",
			p.poisonVM, plural(p.poisonVM, "migration", "migrations"), qemuPoisonText, latest))
	}
	return out
}
