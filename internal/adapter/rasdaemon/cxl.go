package rasdaemon

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/model"
)

// The CXL tables of rasdaemon hold the event records of CXL memory devices
// as the kernel traces them:
//
//	cxl_general_media_event   a media error, by channel, rank and device
//	cxl_dram_event            a media error with the DRAM location
//	cxl_memory_module_event   a change in the health of the device
//	cxl_memory_sparing_event  a spare taking over a cacheline, row, bank or rank
//	cxl_poison_event          an entry of the poison list, or its injection or clearing
//	cxl_overflow_event        records the device lost because its log was full
//
// A device is named by the kernel ("mem0"), which is the label of its
// records, and identified by its serial number, which makes it a DIMM
// identity in the ledger.

// The two media event records number their event types differently.
var (
	cxlDRAMTypes  = []string{"media ECC error", "scrub media ECC error", "invalid address", "data path error", "TE state violation", "corrected error counter expiration", "CKID violation"}
	cxlMediaTypes = []string{"media ECC error", "invalid address", "data path error", "TE state violation", "scrub media ECC error", "corrected error counter expiration", "CKID violation"}
)

// cxlDevice is what the tables say about one device.
type cxlDevice struct {
	name        string
	serial      int64
	first, last time.Time
}

// cxlSeen notes that a table names a device at a time.
func (r *reader) cxlSeen(rw *row, t time.Time) {
	name := rw.str("memdev")
	if name == "" {
		return
	}
	serial, _ := rw.int("serial")
	key := fmt.Sprintf("%s/%x", name, uint64(serial))
	if r.cxlDevs == nil {
		r.cxlDevs = map[string]*cxlDevice{}
	}
	d := r.cxlDevs[key]
	if d == nil {
		d = &cxlDevice{name: name, serial: serial, first: t, last: t}
		r.cxlDevs[key] = d
	}
	if t.Before(d.first) {
		d.first = t
	}
	if t.After(d.last) {
		d.last = t
	}
}

// cxlDevices reports the devices the tables name as modules: the kernel's
// name is the slot, the serial number the identity. A device is reported
// as seen at its first and at its last record, which spans the time it is
// known to have been there.
func (r *reader) cxlDevices() error {
	keys := make([]string, 0, len(r.cxlDevs))
	for k := range r.cxlDevs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d := r.cxlDevs[k]
		if d.serial == 0 {
			continue // the device reported no serial number
		}
		times := []time.Time{d.first}
		if d.last.After(d.first) {
			times = append(times, d.last)
		}
		for _, t := range times {
			m := &model.Module{ObservedAt: t, Locator: "cxl:" + d.name, Populated: true, Source: Kind,
				RawRef: fmt.Sprintf("%s#cxl device %s", r.path, d.name),
				DIMM:   model.DIMM{Serial: fmt.Sprintf("0x%x", uint64(d.serial)), Technology: "CXL"}}
			if err := r.sink.Module(m); err != nil {
				return err
			}
		}
	}
	if r.cxlRepeats > 0 {
		r.cxlNotes = append(r.cxlNotes, fmt.Sprintf("cxl_poison_event: %d rows list poison that an earlier row already listed; each listed range is kept once", r.cxlRepeats))
	}
	if r.cxlQueries > 0 {
		r.cxlNotes = append(r.cxlNotes, fmt.Sprintf("cxl_memory_sparing_event: %d rows only report how many spare resources are left and are not repairs", r.cxlQueries))
	}
	return nil
}

func nameOf(names []string, code int64) string {
	if code >= 0 && int(code) < len(names) {
		return names[code]
	}
	return fmt.Sprintf("event type %d", code)
}

// cxlMedia fills what the two media event tables share. types are the
// names of the event types of the table. It reports false, after noting
// the reason, for a row without a usable time.
func (r *reader) cxlMedia(rw *row, e *model.Event, types []string) bool {
	if !r.base(rw, e) {
		return false
	}
	r.cxlSeen(rw, e.ErrorDT)
	e.Decoder = "cxl"
	desc, _ := rw.int("descriptor")
	typ, _ := rw.int("type")
	trans, _ := rw.int("transaction_type")
	e.ErrorType = nameOf(types, typ)
	switch e.ErrorType {
	case "invalid address", "TE state violation", "CKID violation":
		// The host used the device wrongly; no memory cell failed.
		e.ErrorSeverity = model.SevInformational
	default:
		e.ErrorSeverity = model.SevCE
		if desc&1 != 0 {
			e.ErrorSeverity = model.SevUE
		}
	}
	switch {
	case e.ErrorType == "scrub media ECC error", trans == 3, trans == 5, trans == 7:
		// Host scan media, internal media scrub, internal error check scrub.
		e.ErrorScrubType = model.ScrubPatrol
	case trans == 1 || trans == 2: // host read, host write
		e.ErrorScrubType = model.ScrubDemand
	default:
		e.ErrorScrubType = model.ScrubOther
	}
	// The host physical address is all ones when the device address is not
	// mapped into a region.
	if hpa, ok := rw.int("hpa"); ok && hpa > 0 {
		e.OsPhysicalAddress = model.Some(uint64(hpa))
	}
	if dev := rw.str("memdev"); dev != "" {
		e.Label = "cxl:" + dev
	}
	return true
}

// cxlDRAM turns a DRAM event record into an event. Location columns are
// only meaningful when the device marked them valid, and rasdaemon stores
// zero otherwise without keeping the validity flags.
func (r *reader) cxlDRAM(rw *row) error {
	var e model.Event
	if !r.cxlMedia(rw, &e, cxlDRAMTypes) {
		return nil
	}
	bg, _ := rw.int("bank_group")
	bank, _ := rw.int("bank")
	rowN, _ := rw.int("row")
	col, _ := rw.int("column")
	nibble, _ := rw.int("nibble_mask")
	// A DRAM location of all zeros with no correction mask is taken as
	// "not reported": the validity flags are not in the database.
	if bg != 0 || bank != 0 || rowN != 0 || col != 0 || len(rw.blob("cor_mask")) > 0 {
		e.BankGroup, e.Bank = optFromInt(bg, true), optFromInt(bank, true)
		e.Row, e.Column = optFromInt(rowN, true), optFromInt(col, true)
		if ch, ok := rw.int("channel"); ok {
			e.ChannelId = optFromInt(ch, true)
		}
		if rk, ok := rw.int("rank"); ok {
			e.Rank = optFromInt(rk, true)
		}
		if sc, ok := rw.int("sub_channel"); ok {
			e.SubChannel = optFromInt(sc, true)
		}
		if nibble != 0 {
			e.ErrorVector = fmt.Sprintf("nibble:0x%x", nibble)
		}
	}
	return r.emit(&e)
}

func (r *reader) cxlGeneralMedia(rw *row) error {
	var e model.Event
	if !r.cxlMedia(rw, &e, cxlMediaTypes) {
		return nil
	}
	ch, _ := rw.int("channel")
	rk, _ := rw.int("rank")
	dev, _ := rw.int("device")
	if ch != 0 || rk != 0 || dev != 0 {
		e.ChannelId, e.Rank, e.DeviceId = optFromInt(ch, true), optFromInt(rk, true), optFromInt(dev, true)
	}
	return r.emit(&e)
}

// Names of the memory module event record.
var (
	cxlModuleEvents = []string{"health status change", "media status change", "life used change", "temperature change", "data path error",
		"LSA error", "unrecoverable sideband bus error", "memory media FRU error", "power management fault"}
	cxlMediaStatus = []string{"normal", "not ready", "write persistency lost", "all data lost", "write persistency loss in the event of power loss",
		"write persistency loss in the event of shutdown", "write persistency loss imminent", "all data loss in the event of power loss",
		"all data loss in the event of shutdown", "all data loss imminent"}
	cxlHealthFlags = []string{"maintenance needed", "performance degraded", "hardware replacement needed", "memory capacity degraded"}
)

// Health status bit: the device asks to be replaced.
const cxlReplacementNeeded = 1 << 2

// cxlModule turns a memory module event record, a change in the health of
// the device, into an informational event. A device that says it needs to
// be replaced is, in addition, marked as failed: once per device, at the
// first record that says so.
func (r *reader) cxlModule(rw *row) error {
	var e model.Event
	if !r.base(rw, &e) {
		return nil
	}
	r.cxlSeen(rw, e.ErrorDT)
	e.Decoder, e.ErrorSeverity = "cxl", model.SevInformational
	if dev := rw.str("memdev"); dev != "" {
		e.Label = "cxl:" + dev
	}
	typ, _ := rw.int("event_type")
	health, _ := rw.int("health_status")
	media, _ := rw.int("media_status")
	parts := []string{"CXL device " + nameOf(cxlModuleEvents, typ)}
	var flags []string
	for i, name := range cxlHealthFlags {
		if health&(1<<i) != 0 {
			flags = append(flags, name)
		}
	}
	if len(flags) > 0 {
		parts = append(parts, "health: "+strings.Join(flags, ", "))
	}
	if media != 0 {
		parts = append(parts, "media: "+nameOf(cxlMediaStatus, media))
	}
	if v, ok := rw.int("life_used"); ok && v > 0 && v <= 255 {
		parts = append(parts, fmt.Sprintf("life used %d%%", v))
	}
	if v, ok := rw.int("device_temp"); ok && v != 0 {
		parts = append(parts, fmt.Sprintf("temperature %d C", int16(v)))
	}
	if v, ok := rw.int("cor_vol_err_cnt"); ok && v > 0 {
		parts = append(parts, fmt.Sprintf("corrected volatile errors %d", v))
	}
	if v, ok := rw.int("dirty_shutdown_cnt"); ok && v > 0 {
		parts = append(parts, fmt.Sprintf("dirty shutdowns %d", v))
	}
	e.ErrorType = strings.Join(parts, "; ")
	if err := r.emit(&e); err != nil {
		return err
	}

	if health&cxlReplacementNeeded == 0 || e.Label == "" {
		return nil
	}
	serial, _ := rw.int("serial")
	key := fmt.Sprintf("%s/%x", e.Label, uint64(serial))
	if r.cxlMarked[key] {
		return nil
	}
	if r.cxlMarked == nil {
		r.cxlMarked = map[string]bool{}
	}
	r.cxlMarked[key] = true
	a := &model.Action{At: e.ErrorDT, TimeQuality: model.TimeExact, Kind: model.ActionMarkFailed, Method: "device", Result: model.ResultOK,
		Label: e.Label, Detail: "the CXL device reports that it needs to be replaced", Source: e.Source, RawRef: e.RawRef, Raw: e.Raw,
		Fingerprint: model.Fingerprint(Kind, "cxl-replace", key)}
	if err := r.sink.Action(a); err != nil {
		return sinkError{err}
	}
	return nil
}

// cxlPoison turns a row of the poison list into an informational event.
// The list is read out on request, so the same poisoned range comes back
// with every reading; each range is kept once, at its first listing.
// Poison is the trace of an uncorrected error, and the error itself is in
// the media event tables: an entry of the list is counted in no total.
func (r *reader) cxlPoison(rw *row) error {
	var e model.Event
	if !r.base(rw, &e) {
		return nil
	}
	r.cxlSeen(rw, e.ErrorDT)
	e.Decoder, e.ErrorSeverity = "cxl", model.SevInformational
	dev := rw.str("memdev")
	if dev != "" {
		e.Label = "cxl:" + dev
	}
	trace, source := rw.str("trace_type"), rw.str("source")
	dpa, _ := rw.int("dpa")
	length, _ := rw.int("dpa_length")
	if hpa, ok := rw.int("hpa"); ok && hpa > 0 {
		e.OsPhysicalAddress = model.Some(uint64(hpa))
	}
	where := fmt.Sprintf("device address 0x%x, %d bytes", uint64(dpa), length)
	switch strings.ToLower(trace) {
	case "inject":
		e.ErrorType = "CXL poison injected at " + where
	case "clear":
		e.ErrorType = "CXL poison cleared at " + where
	default:
		serial, _ := rw.int("serial")
		key := fmt.Sprintf("%s/%x/%x/%d/%s", dev, uint64(serial), uint64(dpa), length, source)
		if r.cxlPoisons[key] {
			r.cxlRepeats++
			return nil
		}
		if r.cxlPoisons == nil {
			r.cxlPoisons = map[string]bool{}
		}
		r.cxlPoisons[key] = true
		// The listing is the record, whichever row of the table shows it first.
		e.Fingerprint = model.Fingerprint(Kind, "cxl-poison", key)
		e.ErrorType = "CXL poison list entry at " + where
		if source != "" {
			e.ErrorType += ", source " + strings.ToLower(source)
		}
	}
	return r.emit(&e)
}

// Flags of the memory sparing event record.
const (
	cxlSparingQuery  = 1 << 0 // the record answers a query for spare resources
	cxlSparingHard   = 1 << 1
	cxlSparingDevice = 1 << 2 // the device started the operation itself
)

// Validity flags of the memory sparing event record.
const (
	cxlSparingValidChannel = 1 << iota
	cxlSparingValidRank
	cxlSparingValidNibble
	cxlSparingValidBankGroup
	cxlSparingValidBank
	cxlSparingValidRow
	cxlSparingValidColumn
	_
	_
	cxlSparingValidSubChannel
)

var cxlSparingKinds = []string{"cacheline", "row", "bank", "rank"}

// cxlSparing turns a memory sparing event record into a sparing action: a
// spare took over a cacheline, a row, a bank or a rank of the device.
func (r *reader) cxlSparing(rw *row) error {
	ref, id := r.ref(rw)
	t, _, ok := parseTimestamp(rw.str("timestamp"))
	if !ok {
		r.tally.Skip(ref, "timestamp is empty or unreadable")
		return nil
	}
	r.cxlSeen(rw, t)
	flags, _ := rw.int("flags")
	if flags&cxlSparingQuery != 0 {
		r.cxlQueries++
		return nil
	}
	result, _ := rw.int("result")
	valid, _ := rw.int("validity_flags")
	sub, hasSub := rw.int("hdr_maint_op_sub_class")
	a := &model.Action{At: r.kernelTime(t), TimeQuality: model.TimeExact, Kind: model.ActionSparing, Method: "soft", Result: model.ResultOK,
		Source: Kind + "/" + rw.table, RawRef: ref, Raw: rw.raw(), Fingerprint: fingerprint(rw.table, id, t)}
	if flags&cxlSparingHard != 0 {
		a.Method = "hard"
	}
	if dev := rw.str("memdev"); dev != "" {
		a.Label = "cxl:" + dev
	}
	kind := "memory"
	if hasSub {
		kind = nameOf(cxlSparingKinds, sub)
	}
	detail := []string{kind + " sparing"}
	if flags&cxlSparingDevice != 0 {
		detail = append(detail, "started by the device")
	}
	if result != 0 {
		// Anything but zero is not a completed operation.
		a.Result = model.ResultFailed
		detail = append(detail, fmt.Sprintf("result code 0x%x", result))
	}
	if left, ok := rw.int("res_avail"); ok {
		detail = append(detail, fmt.Sprintf("%d spare resources left", left))
	}
	a.Detail = strings.Join(detail, ", ")
	get := func(flag int64, col string) model.Opt[int] {
		if valid&flag == 0 {
			return model.Opt[int]{}
		}
		v, ok := rw.int(col)
		return optFromInt(v, ok)
	}
	a.Rank = get(cxlSparingValidRank, "rank")
	a.BankGroup, a.Bank = get(cxlSparingValidBankGroup, "bank_group"), get(cxlSparingValidBank, "bank")
	a.Row = get(cxlSparingValidRow, "row")
	if err := r.sink.Action(a); err != nil {
		return sinkError{err}
	}
	return nil
}

// cxlOverflow notes that a device lost records because its event log was
// full. Nothing enters the ledger: what was lost is not known.
func (r *reader) cxlOverflow(rw *row) error {
	t, _, ok := parseTimestamp(rw.str("timestamp"))
	if ok {
		r.cxlSeen(rw, t)
	}
	n, _ := rw.int("count")
	if len(r.cxlNotes) < 8 {
		r.cxlNotes = append(r.cxlNotes, fmt.Sprintf("cxl_overflow_event: device %s lost %d records of its %s log between %s and %s",
			rw.str("memdev"), n, strings.ToLower(rw.str("log_type")), rw.str("first_ts"), rw.str("last_ts")))
	}
	return nil
}
