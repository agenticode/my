package klog

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/model"
)

// edacRec is one "EDAC MCn: N CE|UE ..." line taken apart.
type edacRec struct {
	mc       int
	count    int
	kind     string // "CE" or "UE"
	msg      string // the driver's message, may be empty
	label    string
	location string // the layer or firmware location inside the parentheses
	page     uint64
	offset   uint64
	grain    uint64
	syndrome model.Opt[uint64]
	detail   string // the driver detail after " - ", may be empty
}

var (
	edacHeadRE = regexp.MustCompile(`EDAC MC(\d+): (\d+) (CE|UE) (.*)$`)
	edacTailRE = regexp.MustCompile(`^page:0x([0-9a-fA-F]+) offset:0x([0-9a-fA-F]+) grain:(\d+)(?: syndrome:0x([0-9a-fA-F]+))?(?: - (.*))?$`)
)

// isEDACLine is the cheap test that comes before parseEDACLine.
func isEDACLine(msg string) bool {
	i := strings.Index(msg, "EDAC MC")
	return i >= 0 && (strings.Contains(msg[i:], " CE ") || strings.Contains(msg[i:], " UE "))
}

// parseEDACLine takes apart the line edac_mc.c prints for an error:
//
//	EDAC MC0: 1 CE memory read error on LABEL (LOCATION page:0x1f8bd1 offset:0x9c0 grain:32 syndrome:0x0 - DETAIL)
//
// The message before "on" may be empty, the syndrome is missing on UE
// lines, and LOCATION and DETAIL may themselves contain parentheses.
func parseEDACLine(msg string) (edacRec, string) {
	var r edacRec
	m := edacHeadRE.FindStringSubmatch(msg)
	if m == nil {
		return r, "not an EDAC error line"
	}
	r.mc, _ = strconv.Atoi(m[1])
	r.count, _ = strconv.Atoi(m[2])
	r.kind = m[3]
	rest := strings.TrimRight(m[4], " ")
	if !strings.HasSuffix(rest, ")") {
		return r, "EDAC line is cut off before the closing parenthesis"
	}
	rest = rest[:len(rest)-1]

	// The address fields mark the end of the location.
	p := strings.Index(rest, " page:0x")
	if p >= 0 {
		p++
	} else if p = strings.Index(rest, "(page:0x"); p >= 0 {
		p++
	} else {
		return r, "EDAC line has no page field"
	}
	open := strings.LastIndex(rest[:p], " (")
	if open < 0 {
		return r, "EDAC line has no opening parenthesis"
	}
	head := strings.TrimSpace(rest[:open])
	r.location = strings.TrimSpace(rest[open+2 : p])
	switch {
	case strings.HasPrefix(head, "on "):
		r.label = strings.TrimSpace(head[3:])
	case head == "on":
	default:
		i := strings.Index(head, " on ")
		if i < 0 {
			return r, "EDAC line does not name a DIMM"
		}
		r.msg, r.label = strings.TrimSpace(head[:i]), strings.TrimSpace(head[i+4:])
	}

	t := edacTailRE.FindStringSubmatch(rest[p:])
	if t == nil {
		return r, "EDAC line address fields are unreadable"
	}
	var err error
	if r.page, err = strconv.ParseUint(t[1], 16, 64); err != nil {
		return r, "EDAC line page is unreadable"
	}
	if r.offset, err = strconv.ParseUint(t[2], 16, 64); err != nil {
		return r, "EDAC line offset is unreadable"
	}
	r.grain, _ = strconv.ParseUint(t[3], 10, 64)
	if t[4] != "" {
		if s, err := strconv.ParseUint(t[4], 16, 64); err == nil {
			r.syndrome = model.Some(s)
		}
	}
	r.detail = t[5]
	return r, ""
}

// GHES blocks. Every line of one firmware error report carries the same
// sequence tag: "{12}[Hardware Error]: ...".
var ghesTagRE = regexp.MustCompile(`^\{(\d+)\}\[Hardware Error\]: ?(.*)$`)

const hwErrMark = "[Hardware Error]: "

// ghesSection is one error section of a GHES block.
type ghesSection struct {
	severity string
	fruText  string
	typ      string // section_type text, for example "memory error"
	lines    []string
}

var (
	ghesSecRE     = regexp.MustCompile(`^\s*(?:Error (\d+), type: (\w+)|section: (\d+), severity: \d+, (\w+))\s*$`)
	ghesEventSev  = regexp.MustCompile(`^\s*(?:event severity: (\w+)|severity: \d+, (\w+))\s*$`)
	ghesFRUText   = regexp.MustCompile(`^\s*fru_text: (.*?)\s*$`)
	ghesSecTypeRE = regexp.MustCompile(`^\s*section_type: (.*?)\s*$`)
)

// splitGHES cuts the body lines of a GHES block into its sections and
// returns the severity of the whole event.
func splitGHES(body []string) (eventSeverity string, sections []ghesSection) {
	var cur *ghesSection
	for _, b := range body {
		if m := ghesSecRE.FindStringSubmatch(b); m != nil {
			sections = append(sections, ghesSection{severity: m[2] + m[4]})
			cur = &sections[len(sections)-1]
			continue
		}
		if cur == nil {
			if m := ghesEventSev.FindStringSubmatch(b); m != nil {
				eventSeverity = m[1] + m[2]
				continue
			}
			// A block whose head was lost: the first field starts a section.
			if strings.Contains(b, "section_type:") || strings.Contains(b, "fru_text:") {
				sections = append(sections, ghesSection{})
				cur = &sections[len(sections)-1]
			} else {
				continue
			}
		}
		if m := ghesFRUText.FindStringSubmatch(b); m != nil {
			cur.fruText = m[1]
			continue
		}
		if m := ghesSecTypeRE.FindStringSubmatch(b); m != nil {
			cur.typ = m[1]
			continue
		}
		cur.lines = append(cur.lines, strings.TrimSpace(b))
	}
	return eventSeverity, sections
}

// memError decodes a memory error section.
func (s *ghesSection) memError() (cper.MemError, bool) {
	m, found := cper.ParseText(strings.Join(s.lines, "\n"))
	return m, found || m.PhysicalAddress.Valid || m.ErrorType.Valid || m.ErrorStatus.Valid
}

// Machine check lines as __print_mce prints them.
var (
	mceHeadRE = regexp.MustCompile(`^CPU (\d+): Machine Check(?: Exception)?: ([0-9a-fA-F]+) Bank (\d+): ([0-9a-fA-F]{1,16})\s*$`)
	mceTSCRE  = regexp.MustCompile(`^TSC ([0-9a-fA-F]+)(.*)$`)
	mceKVRE   = regexp.MustCompile(`\b(ADDR|MISC|PPIN|SYND|IPID) ([0-9a-fA-F]+)`)
	mceProcRE = regexp.MustCompile(`^PROCESSOR (\d+):([0-9a-fA-F]+) TIME (\d+) SOCKET (\d+) APIC ([0-9a-fA-F]+)`)
)

// AMD machine check decode lines as mce_amd.c prints them.
var (
	amdHeadRE = regexp.MustCompile(`^CPU:(\d+) \(([0-9a-fA-F]+):([0-9a-fA-F]+):([0-9a-fA-F]+)\) MC(\d+)_STATUS\[[^\]]*\]: 0x([0-9a-fA-F]{1,16})\s*$`)
	amdAddrRE = regexp.MustCompile(`^Error Addr: 0x([0-9a-fA-F]+)`)
	amdIPIDRE = regexp.MustCompile(`^IPID: 0x([0-9a-fA-F]+)(?:, Syndrome: 0x([0-9a-fA-F]+))?`)
	amdExtRE  = regexp.MustCompile(`^(.+?) Ext\. Error Code: (\d+)(?:, (.*?))?\.?\s*$`)
)

// amdStatusTexts are the one-line summaries that open an AMD decode block.
var amdStatusTexts = []string{
	"System Fatal error.",
	"Uncorrected, software restartable error.",
	"Uncorrected, software containable error.",
	"Deferred error, no action required.",
	"Corrected error, no action required.",
}

func isAMDStatusText(body string) bool {
	for _, s := range amdStatusTexts {
		if strings.HasPrefix(body, s) {
			return true
		}
	}
	return false
}

// Page offlining messages of the kernel.
var (
	// Memory failure: 0x1f8bd1: recovery action for dirty LRU page: Recovered
	mfActionRE = regexp.MustCompile(`Memory failure: (0x[0-9a-fA-F]+): recovery action for (.+): (\w+)\s*$`)
	// RAS: Soft-offlining pfn: 0x1f8bd1 (the corrected error collector)
	cecRE = regexp.MustCompile(`Soft-offlining pfn: (0x[0-9a-fA-F]+)`)
	// Soft offlining pfn 0x1f8bd1 at process virtual address 0x7f... (madvise)
	madviseRE = regexp.MustCompile(`Soft offlining pfn (0x[0-9a-fA-F]+) at process virtual address`)
	// soft offline: 0x1f8bd1: migration failed 1, type ...   (up to 5.x)
	// Soft offline: 0x1f8bd1: page migration failed ...       (6.x)
	// soft_offline: 0x1f8bd1: invalidated                      (clean page cache dropped)
	softRE = regexp.MustCompile(`[Ss]oft[ _]offline: (0x[0-9a-fA-F]+):? ?(.*)$`)
)

// offline is a page offline message taken apart.
type offline struct {
	pfn    uint64
	method string // "soft" or "hard"
	result model.ActionResult
	detail string
}

// parseOffline reads a page offlining message. ok is false for messages
// that do not report an offline attempt, such as "already poisoned".
func parseOffline(msg string) (o offline, ok bool) {
	pfn := func(s string) bool {
		// 44 bits of page number cover any physical address, with 4 KiB
		// and with 64 KiB pages.
		v, err := strconv.ParseUint(strings.TrimPrefix(s, "0x"), 16, 44)
		o.pfn = v
		return err == nil
	}
	if m := mfActionRE.FindStringSubmatch(msg); m != nil && pfn(m[1]) {
		o.method, o.detail = "hard", m[2]+": "+m[3]
		switch strings.ToLower(m[3]) {
		case "recovered":
			o.result = model.ResultOK
		case "delayed":
			o.result = model.ResultPending
		case "failed", "ignored":
			o.result = model.ResultFailed
		default:
			o.result = model.ResultUnknown
		}
		return o, true
	}
	if m := cecRE.FindStringSubmatch(msg); m != nil && pfn(m[1]) {
		// The collector decided to offline the page; the kernel reports
		// the outcome only when it fails.
		o.method, o.result, o.detail = "soft", model.ResultUnknown, "corrected error collector"
		return o, true
	}
	if m := madviseRE.FindStringSubmatch(msg); m != nil && pfn(m[1]) {
		o.method, o.result, o.detail = "soft", model.ResultUnknown, "requested through madvise"
		return o, true
	}
	if m := softRE.FindStringSubmatch(msg); m != nil && pfn(m[1]) {
		text := strings.TrimSpace(m[2])
		switch {
		case strings.Contains(text, "already poisoned"):
			return o, false
		case strings.Contains(text, "invalidated"):
			o.result = model.ResultOK
		case strings.Contains(text, "failed") || strings.Contains(text, "unknown non LRU") || strings.Contains(text, "non anonymous thp"):
			o.result = model.ResultFailed
		default:
			o.result = model.ResultUnknown
		}
		o.method, o.detail = "soft", text
		return o, true
	}
	return o, false
}

// Boot banner lines.
var (
	linuxVersionRE = regexp.MustCompile(`^Linux version (\S+)`)
	// The line that identifies the machine always has the BIOS part. Other
	// lines of the DMI code ("DMI: Memory slots populated: 2/2", printed
	// since Linux 6.8) do not, and are not about the machine's identity.
	dmiRE     = regexp.MustCompile(`^DMI: (.*?), BIOS (.*)$`)
	cmdlineRE = regexp.MustCompile(`^(?:Kernel command line|Command line): (.*)$`)
	// rtc_cmos 00:02: setting system clock to 2026-11-12T03:14:07 UTC (1794539647)
	rtcRE = regexp.MustCompile(`setting system clock to \S+(?: \S+)? UTC \((\d{9,11})\)`)
)

// candidate reports whether a raw input line can hold a record this
// adapter reads. It is a cheap test that lets most lines of a large log
// skip the regular expressions.
func candidate(raw string) bool {
	return strings.Contains(raw, "EDAC MC") ||
		strings.Contains(raw, "Hardware Error]") ||
		strings.Contains(raw, "emory failure: ") ||
		strings.Contains(raw, "offlin") ||
		strings.Contains(raw, "Linux version ") ||
		strings.Contains(raw, "DMI: ") ||
		strings.Contains(raw, "ommand line: ") ||
		strings.Contains(raw, "setting system clock to ") ||
		strings.Contains(raw, "NVRM: ") ||
		// A journal message that is not valid UTF-8 is exported as an
		// array of byte values and has to be decoded to be looked at.
		strings.Contains(raw, `"MESSAGE":[`) || strings.Contains(raw, `"MESSAGE" : [`)
}

// Lines of the NVIDIA GPU driver. An Xid line reports an error of a GPU by
// number; before the first one of a GPU in a boot the driver prints which
// GPU sits at the PCI address and its board serial number:
//
//	NVRM: GPU at PCI:0000:3b:00: GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7
//	NVRM: GPU Board Serial Number: 1562720005599
//	NVRM: Xid (PCI:0000:3b:00): 48, pid=1234, name=python, An uncorrectable double bit error (DBE) has been detected ...
//	NVRM: Xid (PCI:0000:01:00 GPU-I:05): 94, pid=7194, Contained: CE User Channel (0x9). RST: No, D-RST: No
//	NVRM: Xid (0000:03:00): 14, Channel 00000001
var (
	xidRE       = regexp.MustCompile(`NVRM: Xid \((?:PCI:)?([0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2})(?:\.[0-7])?(?: [^)]*)?\): (\d{1,4})(?:, ?(.*))?$`)
	gpuAtRE     = regexp.MustCompile(`NVRM: GPU at (?:PCI:)?([0-9a-fA-F]{4}:[0-9a-fA-F]{2}:[0-9a-fA-F]{2})(?:\.[0-7])?: (GPU-[0-9a-fA-F-]{8,48})`)
	gpuSerialRE = regexp.MustCompile(`NVRM: GPU Board Serial Number: ?(\S*)`)
	// The process the driver blames, which is not part of the error text.
	xidProcRE = regexp.MustCompile(`^(?:pid=[^,]*(?:, ?|$))?(?:name=[^,]*(?:, ?|$))?`)
)

// MemoryXIDs are the Xid numbers that report on the memory of a GPU, with
// the names of the vendor's catalog. Other Xids (application errors, bus
// and link errors) are counted and left out.
var MemoryXIDs = map[int]string{
	48:  "double bit ECC error",
	63:  "GPU memory remapping event",
	64:  "GPU memory remapping failure",
	92:  "high single-bit ECC error rate",
	94:  "contained memory error",
	95:  "uncontained memory error",
	140: "ECC unrecovered error",
	156: "resource retirement event",
	157: "resource retirement failure",
	160: "channel retirement event",
	161: "channel retirement failure",
	168: "reduced GPU memory capacity",
	171: "uncorrectable DRAM error",
	172: "uncorrectable SRAM error",
}

// pciAddress writes a PCI address the way the ledger keeps it: domain,
// bus, device and function in lower case, as nvidia-smi prints it without
// the upper half of the domain.
func pciAddress(domainBusDev string) string {
	return strings.ToLower(domainBusDev) + ".0"
}
