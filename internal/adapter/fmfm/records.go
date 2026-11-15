package fmfm

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/model"
)

// timeLayouts are the written forms of a time that are read. The framework
// document recommends "Year:Month:Date:Hour:Min:Sec:MSec".
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006/01/02 15:04:05",
}

// colonTime reads "Year:Month:Date:Hour:Min:Sec:MSec", with or without the
// milliseconds.
func colonTime(s string, loc *time.Location) (time.Time, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 6 && len(parts) != 7 {
		return time.Time{}, false
	}
	var n [7]int
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" || len(p) > 4 {
			return time.Time{}, false
		}
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 {
			return time.Time{}, false
		}
		n[i] = v
	}
	if n[1] < 1 || n[1] > 12 || n[2] < 1 || n[2] > 31 || n[3] > 23 || n[4] > 59 || n[5] > 60 || n[6] > 999 {
		return time.Time{}, false
	}
	t := time.Date(n[0], time.Month(n[1]), n[2], n[3], n[4], n[5], n[6]*int(time.Millisecond), loc)
	if t.Day() != n[2] {
		return time.Time{}, false // a day the month does not have
	}
	return t, true
}

// parseTime reads a time: one of the written forms, or a number of
// seconds since 1970 (milliseconds for a number too large to be seconds).
// A time without a zone is read in loc.
func parseTime(v any, loc *time.Location) (time.Time, bool) {
	s := text(v)
	if s == "" {
		return time.Time{}, false
	}
	if loc == nil {
		loc = time.UTC
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		switch {
		case n <= 0:
			return time.Time{}, false
		case n > 1e11:
			return inRange(time.UnixMilli(n).UTC())
		}
		return inRange(time.Unix(n, 0).UTC())
	}
	for _, layout := range timeLayouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return inRange(t.UTC())
		}
	}
	if t, ok := colonTime(s, loc); ok {
		return inRange(t.UTC())
	}
	return time.Time{}, false
}

func inRange(t time.Time) (time.Time, bool) {
	if y := t.Year(); y < 1970 || y > 9999 {
		return time.Time{}, false
	}
	return t, true
}

// number reads a whole number given as a JSON number or as text, decimal
// or with the prefix 0x.
func number(v any) (uint64, bool) {
	s := text(v)
	if s == "" {
		return 0, false
	}
	if _, isBool := v.(bool); isBool {
		return 0, false
	}
	if n, err := strconv.ParseUint(s, 0, 64); err == nil {
		return n, true
	}
	// A number written with a fraction that is zero.
	if f, err := strconv.ParseFloat(s, 64); err == nil && f >= 0 && f < 1<<53 && f == float64(uint64(f)) {
		return uint64(f), true
	}
	return 0, false
}

// small reads a location number: a whole number that fits the ledger.
func small(v any) model.Opt[int] {
	n, ok := number(v)
	if !ok || n > 1<<30 {
		return model.Opt[int]{}
	}
	return model.Some(int(n))
}

// severity reads an ErrorSeverity. The framework's words are
// Informational, CE, Recoverable, UE and Fatal.
func severity(s string) (model.Severity, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "informational", "info":
		return model.SevInformational, true
	case "ce", "corrected", "correctable":
		return model.SevCE, true
	case "recoverable":
		return model.SevRecoverable, true
	case "ue", "uncorrected", "uncorrectable":
		return model.SevUE, true
	case "fatal":
		return model.SevFatal, true
	}
	return "", false
}

// scrubType reads an ErrorScrubType: demand read error, patrol scrub
// error, or other.
func scrubType(s string) model.ScrubType {
	low := strings.ToLower(strings.TrimSpace(s))
	switch {
	case low == "":
		return ""
	case strings.HasPrefix(low, "demand"):
		return model.ScrubDemand
	case strings.HasPrefix(low, "patrol"):
		return model.ScrubPatrol
	}
	return model.ScrubOther
}

// location is the module, and the place in it, a record names.
type location struct {
	socket, channel, slot, sub, rank, device, bankGroup, bank, row, column model.Opt[int]
	label                                                                  string
}

func locate(flat map[string]any) location {
	l := location{
		socket: small(flat["socketid"]), channel: small(flat["channelid"]), slot: small(flat["channelslotid"]),
		sub: small(flat["subchannel"]), rank: small(flat["rank"]), device: small(flat["deviceid"]),
		bankGroup: small(flat["bankgroup"]), bank: small(flat["bank"]), row: small(flat["row"]), column: small(flat["column"]),
	}
	for _, k := range []string{"devicelocator", "locator", "slotlabel", "dimmlabel", "label"} {
		if s, ok := flat[k].(string); ok && strings.TrimSpace(s) != "" {
			l.label = strings.TrimSpace(s)
			break
		}
	}
	return l
}

func (l location) place() ([3]int, bool) {
	if !l.socket.Valid || !l.channel.Valid || !l.slot.Valid {
		return [3]int{}, false
	}
	return [3]int{l.socket.V, l.channel.V, l.slot.V}, true
}

// emit turns the records into ledger records.
func (r *run) emit() error {
	r.pickHost()
	for i := range r.records {
		if i%1024 == 0 {
			if err := r.ctx.Err(); err != nil {
				return stopped{err}
			}
		}
		rec := &r.records[i]
		if h := text(rec.flat["hostid"]); h != "" && r.host != "" && !strings.EqualFold(h, r.host) {
			r.otherHosts[h]++
			if !r.asked {
				// Nobody asked for these records to be left out.
				r.tally.Skip(rec.ref, "record of another host than "+r.host)
			}
			continue
		}
		used := false
		if rec.obj.has(bootMarks) {
			used = true
			if err := r.boot(rec); err != nil {
				return err
			}
		}
		if rec.obj.has(crashMarks) {
			used = true
			if err := r.crash(rec); err != nil {
				return err
			}
		}
		if rec.obj.has(runtimeMarks) {
			used = true
			if err := r.runtime(rec); err != nil {
				return err
			}
		}
		if !used {
			r.tally.Skip(rec.ref, "record of no known kind")
		}
	}
	return r.modules()
}

// fingerprint identifies a record by its content. The log gives its
// records no number, so two records with the same content are told apart
// by the order in which they stand in the file.
func (r *run) fingerprint(kind string, parts ...string) string {
	key := kind + "\x00" + strings.Join(parts, "\x00")
	n := r.seen[key]
	r.seen[key] = n + 1
	return model.Fingerprint(append([]string{Kind, kind, strconv.Itoa(n)}, parts...)...)
}

// runtime turns a record of the runtime error log into an event, and its
// ECS registers into readings.
func (r *run) runtime(rec *record) error {
	flat := rec.flat
	at, ok := parseTime(flat["errordt"], r.opts.Location)
	if !ok {
		r.tally.Skip(rec.ref, fmt.Sprintf("ErrorDT %q is missing or unreadable", text(flat["errordt"])))
		return nil
	}
	loc := locate(flat)
	for _, k := range []string{"processormetadata", "pmicruntimeregisters"} {
		if rec.obj[k] != nil {
			r.unread[fieldNames[k]]++
		}
	}
	// A temperature is kept with what the record becomes; a record that
	// becomes nothing is reported as skipped.
	stored := false
	defer func() {
		if _, has := flat["dimmtemp"]; has && stored {
			r.temps++
		}
	}()

	_, hasSeverity := flat["errorseverity"]
	ecs := rec.obj["ecsmoderegisters"]
	hasECS := ecs != nil // a field that is null is not there
	if !hasSeverity && !hasECS {
		r.tally.Skip(rec.ref, "record has a time and neither an ErrorSeverity nor ECS registers")
		return nil
	}
	if hasSeverity {
		sev, ok := severity(text(flat["errorseverity"]))
		if !ok {
			r.tally.Skip(rec.ref, fmt.Sprintf("unknown ErrorSeverity %q", text(flat["errorseverity"])))
		} else {
			e := &model.Event{
				Kind: model.KindError, ErrorDT: at, TimeQuality: model.TimeExact, ErrorSeverity: sev, ErrorScrubType: scrubType(text(flat["errorscrubtype"])),
				SocketId: loc.socket, ChannelId: loc.channel, ChannelSlotId: loc.slot, SubChannel: loc.sub, Rank: loc.rank, DeviceId: loc.device,
				BankGroup: loc.bankGroup, Bank: loc.bank, Row: loc.row, Column: loc.column, Label: loc.label,
				Count: 1, Source: Kind + "/runtime", Decoder: "fmfm", RawRef: rec.ref, Raw: rec.raw,
			}
			if v, has := flat["errorvector"]; has {
				e.ErrorVector = text(v)
				if n, isNum := v.(json.Number); isNum {
					if u, ok := number(n); ok {
						e.ErrorVector = fmt.Sprintf("0x%x", u)
					}
				}
			}
			if addr, ok := number(flat["osphysicaladdress"]); ok {
				e.OsPhysicalAddress = model.Some(addr)
			}
			if t := text(flat["errortype"]); t != "" {
				e.ErrorType = t
			}
			e.Grade = e.ComputeGrade()
			e.Fingerprint = r.fingerprint("runtime", text(flat["errordt"]), string(sev), string(e.ErrorScrubType),
				loc.socket.String(), loc.channel.String(), loc.slot.String(), loc.sub.String(), loc.rank.String(), loc.device.String(),
				loc.bankGroup.String(), loc.bank.String(), loc.row.String(), loc.column.String(), e.ErrorVector, e.OsPhysicalAddress.String(), loc.label)
			if err := r.sink.Event(e); err != nil {
				return err
			}
			stored = true
		}
	}
	if hasECS {
		samples, ok := ecsReadings(ecs, loc.device)
		if !ok || len(samples) == 0 {
			r.tally.Skip(rec.ref, "EcsModeRegisters is in a form this reader does not know")
			return nil
		}
		for _, s := range samples {
			s.ObservedAt, s.TimeQuality = at, model.TimeExact
			s.SocketId, s.ChannelId, s.ChannelSlotId, s.SubChannel, s.Rank = loc.socket, loc.channel, loc.slot, loc.sub, loc.rank
			s.Label = loc.label
			s.Source, s.RawRef, s.Raw = Kind+"/runtime", rec.ref, rec.raw
			regs := s.Registers()
			var parts []string
			for _, reg := range regs {
				parts = append(parts, reg.String())
			}
			s.Fingerprint = r.fingerprint("ecs", text(flat["errordt"]), loc.socket.String(), loc.channel.String(), loc.slot.String(),
				loc.sub.String(), loc.rank.String(), s.DeviceId.String(), loc.label, strings.Join(parts, ","))
			if err := r.sink.ECS(s); err != nil {
				return err
			}
			stored = true
		}
		if p, ok := loc.place(); ok {
			r.ecsPlaces[p] = true
		}
	}
	return nil
}

// fieldNames are the names of fields as the framework document writes
// them, for the report.
var fieldNames = map[string]string{
	"processormetadata": "ProcessorMetadata", "pmicruntimeregisters": "PmicRuntimeRegisters", "systemmemoryconfig": "SystemMemoryConfig",
	"primordialbootdt": "PrimordialBootDT", "pmicbooterrorlog": "PmicBootErrorLog", "dramoffsetregisters": "DramOffsetRegisters",
	"rcdcontrolwords": "RcdControlWords", "databuffercontrolwords": "DataBufferControlWords", "memorycontrollermetadata": "MemoryControllerMetadata",
	"pmiccrashregisters": "PmicCrashRegisters", "uptime": "Uptime",
}

// registerValue reads one mode register: a byte as a number or as text,
// decimal or hexadecimal with or without the prefix.
func registerValue(v any) (int, bool) {
	if n, ok := number(v); ok && n <= 0xFF {
		return int(n), true
	}
	if s, isText := v.(string); isText {
		if n, err := strconv.ParseUint(strings.TrimSpace(s), 16, 8); err == nil {
			return int(n), true
		}
	}
	return 0, false
}

// ecsReadings reads the EcsModeRegisters of a record: mode registers MR14
// to MR20 of one device, or of several. The forms that are read:
//
//	{"MR14": 32, "MR15": "0x03", ...}       the registers by name
//	[32, 3, 47, 26, 12, 0, 2]               MR14 to MR20 in order
//	"20032f1a0c0002"                        the seven bytes as hexadecimal
//	[{"DeviceId": 3, "MR14": 32, ...}, ...] one of the above per device
//	{"3": {...}, "7": [...]}                the same, by device number
func ecsReadings(v any, device model.Opt[int]) ([]*model.ECSSample, bool) {
	if one, ok := ecsOne(v); ok {
		one.DeviceId = device
		return []*model.ECSSample{one}, true
	}
	var out []*model.ECSSample
	switch x := v.(type) {
	case []any:
		for i, item := range x {
			m, isObj := item.(map[string]any)
			if !isObj {
				return nil, false
			}
			o := folded(m)
			var s *model.ECSSample
			var ok bool
			for _, k := range []string{"registers", "ecsmoderegisters", "moderegisters", "values"} {
				if inner, has := o[k]; has {
					s, ok = ecsOne(inner)
					break
				}
			}
			if !ok {
				s, ok = ecsOne(item)
			}
			if !ok {
				return nil, false
			}
			s.DeviceId = small(o["deviceid"])
			if !s.DeviceId.Valid {
				s.DeviceId = small(o["device"])
			}
			if !s.DeviceId.Valid {
				s.DeviceId = model.Some(i)
			}
			out = append(out, s)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s, ok := ecsOne(x[k])
			if !ok {
				return nil, false
			}
			digits := strings.TrimLeft(strings.ToLower(k), "abcdefghijklmnopqrstuvwxyz_ -")
			n, err := strconv.Atoi(digits)
			if err != nil || n < 0 || n > 1<<16 {
				return nil, false
			}
			s.DeviceId = model.Some(n)
			out = append(out, s)
		}
	default:
		return nil, false
	}
	return out, len(out) > 0
}

// ecsOne reads the registers of one device.
func ecsOne(v any) (*model.ECSSample, bool) {
	s := &model.ECSSample{}
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			name := fold(k)
			digits := strings.TrimPrefix(strings.TrimPrefix(name, "mr"), "moderegister")
			mr, err := strconv.Atoi(digits)
			if err != nil || mr < 14 || mr > 20 {
				continue
			}
			b, ok := registerValue(val)
			if !ok {
				return nil, false
			}
			s.SetRegister(mr, b)
		}
	case []any:
		if len(x) != 7 {
			return nil, false
		}
		for i, val := range x {
			if val == nil {
				continue // a register that was not read
			}
			b, ok := registerValue(val)
			if !ok {
				return nil, false
			}
			s.SetRegister(14+i, b)
		}
	case string:
		clean := strings.NewReplacer(" ", "", ",", "", ":", "", "0x", "", "0X", "").Replace(strings.TrimSpace(x))
		b, err := hex.DecodeString(clean)
		if err != nil || len(b) != 7 {
			return nil, false
		}
		for i, val := range b {
			s.SetRegister(14+i, int(val))
		}
	default:
		return nil, false
	}
	return s, !s.Empty()
}

// suffixField returns the first text field of o whose folded name ends in
// one of the given endings. The framework names what a boot record holds
// (supplier, part number, serial number) and not the field names.
func suffixField(o object, endings ...string) string {
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, end := range endings {
		for _, k := range keys {
			if strings.HasSuffix(k, end) {
				if s := text(o[k]); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// boot turns a record of the boot log into a boot, the installed memory
// into modules, and training failures into informational events.
func (r *run) boot(rec *record) error {
	flat := rec.flat
	at, hasTime := parseTime(flat["currentbootdt"], r.opts.Location)
	if _, given := flat["currentbootdt"]; given && !hasTime {
		r.tally.Skip(rec.ref, fmt.Sprintf("CurrentBootDT %q is unreadable", text(flat["currentbootdt"])))
	}
	for _, k := range []string{"systemmemoryconfig", "primordialbootdt", "pmicbooterrorlog", "dramoffsetregisters", "rcdcontrolwords", "databuffercontrolwords", "memorycontrollermetadata"} {
		if rec.obj[k] != nil {
			r.unread[fieldNames[k]]++
		}
	}
	if hasTime {
		b := &model.Boot{StartedAt: at, StartQuality: model.TimeExact, Derived: true, Source: Kind + "/boot", RawRef: rec.ref}
		switch fw := rec.obj["installedfirmware"].(type) {
		case string:
			b.BIOS = strings.TrimSpace(fw)
		case map[string]any:
			o := folded(fw)
			b.BIOS = suffixField(o, "version", "firmware", "bios")
		}
		if err := r.sink.Boot(b); err != nil {
			return err
		}
	}
	seenAt := at
	if !hasTime {
		seenAt = r.now
	}

	if list, ok := rec.obj["installedmemory"].([]any); ok {
		// The entries of this record by place, as indexes into r.placed.
		here := map[[3]int]int{}
		for i, item := range list {
			m, isObj := item.(map[string]any)
			if !isObj {
				r.tally.Skip(fmt.Sprintf("%s/InstalledMemory[%d]", rec.ref, i), "entry of the installed memory is not an object")
				continue
			}
			o := folded(m)
			sub := map[string]any{}
			flatten(o, sub, 0)
			loc := locate(sub)
			mod := model.Module{ObservedAt: seenAt, Locator: loc.label, Populated: true, Source: Kind + "/boot",
				RawRef: fmt.Sprintf("%s/InstalledMemory[%d]", rec.ref, i)}
			mod.DIMM.Manufacturer = suffixField(object(sub), "supplier", "manufacturer", "vendor")
			mod.DIMM.PartNumber = suffixField(object(sub), "partnumber")
			mod.DIMM.Serial = suffixField(object(sub), "serialnumber", "serial")
			p, placed := loc.place()
			named := mod.Locator != ""
			if !named && !placed {
				r.tally.Skip(mod.RawRef, "entry of the installed memory names no slot: no locator and no socket, channel and slot numbers")
				continue
			}
			if !placed {
				if err := r.sink.Module(&mod); err != nil {
					return err
				}
				continue
			}
			mod.Socket, mod.Channel, mod.Position = loc.socket, loc.channel, loc.slot
			if !named {
				// The name the ledger gives a place that error records
				// name by numbers.
				mod.Locator = model.PlaceName(p[0], p[1], p[2])
			}
			lm := listedModule{place: p, module: mod, named: named}
			if j, taken := here[p]; taken {
				first := &r.placed[j]
				if first.module.Locator == mod.Locator && first.module.DIMM == mod.DIMM {
					continue // the same module listed twice
				}
				first.clash, lm.clash = true, true
			} else {
				here[p] = len(r.placed)
			}
			r.placed = append(r.placed, lm)
		}
	} else if rec.obj["installedmemory"] != nil {
		r.tally.Skip(rec.ref, "InstalledMemory is not a list")
	}

	// A training failure names the step that failed and the module. It
	// happened at the boot the record is about.
	list, _ := rec.obj["trainingeventlogs"].([]any)
	if rec.obj["trainingeventlogs"] != nil && list == nil {
		r.tally.Skip(rec.ref, "TrainingEventLogs is not a list")
	}
	for i, item := range list {
		ref := fmt.Sprintf("%s/TrainingEventLogs[%d]", rec.ref, i)
		if !hasTime {
			r.tally.Skip(ref, "training event of a boot record without a readable CurrentBootDT")
			continue
		}
		m, isObj := item.(map[string]any)
		if !isObj {
			r.tally.Skip(ref, "entry of the training events is not an object")
			continue
		}
		sub := map[string]any{}
		flatten(folded(m), sub, 0)
		loc := locate(sub)
		step := suffixField(object(sub), "step", "failure", "event", "description", "message")
		e := &model.Event{
			Kind: model.KindError, ErrorDT: at, TimeQuality: model.TimeExact, ErrorSeverity: model.SevInformational,
			ErrorType: strings.TrimSpace("memory training failure at boot: " + step),
			SocketId:  loc.socket, ChannelId: loc.channel, ChannelSlotId: loc.slot, SubChannel: loc.sub, DeviceId: loc.device, Label: loc.label,
			Count: 1, Source: Kind + "/boot", Decoder: "fmfm", RawRef: ref, Raw: rec.raw,
		}
		e.Grade = e.ComputeGrade()
		e.Fingerprint = r.fingerprint("training", text(flat["currentbootdt"]), step, loc.socket.String(), loc.channel.String(), loc.slot.String(), loc.label)
		if err := r.sink.Event(e); err != nil {
			return err
		}
	}
	return nil
}

// truthy reads the FatalError flag of a crash record.
func truthy(v any) (set bool, what string) {
	switch x := v.(type) {
	case nil:
		return false, ""
	case bool:
		return x, ""
	case json.Number:
		n, ok := number(x)
		return ok && n != 0, ""
	case string:
		switch low := strings.ToLower(strings.TrimSpace(x)); low {
		case "", "0", "no", "false", "none", "n/a":
			return false, ""
		case "1", "yes", "true":
			return true, ""
		}
		return true, strings.TrimSpace(x)
	}
	return true, ""
}

// crash turns a record of the crash log into an event at the time of the
// crash: a fatal error when the record flags one, a mark otherwise.
func (r *run) crash(rec *record) error {
	flat := rec.flat
	// The time of the operating system is preferred, as the framework
	// recommends: the clock of the firmware may be set elsewhere.
	field := "oscrashdt"
	at, ok := parseTime(flat[field], r.opts.Location)
	if !ok {
		field = "systemcrashdt"
		at, ok = parseTime(flat[field], r.opts.Location)
	}
	if !ok {
		r.tally.Skip(rec.ref, "crash record has no readable time")
		return nil
	}
	for _, k := range []string{"pmiccrashregisters", "uptime"} {
		if rec.obj[k] != nil {
			r.unread[fieldNames[k]]++
		}
	}
	loc := locate(flat)
	fatal, what := truthy(rec.obj["fatalerror"])
	e := &model.Event{
		Kind: model.KindError, ErrorDT: at, TimeQuality: model.TimeExact, ErrorSeverity: model.SevInformational, ErrorType: "system crash",
		SocketId: loc.socket, ChannelId: loc.channel, ChannelSlotId: loc.slot, SubChannel: loc.sub, Rank: loc.rank, DeviceId: loc.device, Label: loc.label,
		Count: 1, Source: Kind + "/crash", Decoder: "fmfm", RawRef: rec.ref, Raw: rec.raw,
	}
	if fatal {
		e.ErrorSeverity = model.SevFatal
		e.ErrorType = "fatal memory error at system crash"
		if what != "" {
			e.ErrorType += ": " + what
		}
	}
	e.Grade = e.ComputeGrade()
	e.Fingerprint = r.fingerprint("crash", text(flat[field]), string(e.ErrorSeverity), what, loc.socket.String(), loc.channel.String(), loc.slot.String())
	return r.sink.Event(e)
}

// modules reports the modules the boot records of the log list by socket,
// channel and slot number. A module in a place ECS registers were read from
// is a DDR5 module: no other kind has them.
func (r *run) modules() error {
	for i := range r.placed {
		lm := &r.placed[i]
		m := lm.module
		if lm.clash {
			r.clashes++
			if !lm.named {
				r.tally.Skip(m.RawRef, "entry of the installed memory has the socket, channel and slot numbers of another entry and no slot name")
				continue
			}
			m.Socket, m.Channel, m.Position = model.Opt[int]{}, model.Opt[int]{}, model.Opt[int]{}
		}
		if r.ecsPlaces[lm.place] {
			m.DIMM.MemoryType = "DDR5"
		}
		if err := r.sink.Module(&m); err != nil {
			return err
		}
	}
	return nil
}
