package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Input is what the engine reads, held in memory: every error the ledger
// counts, and the records that give the errors their context. The engine
// works on an Input and never touches the ledger while it analyses.
//
// An Input holds all hosts of a ledger. A DIMM that moved from one host to
// another keeps one history that way.
type Input struct {
	Hosts      []model.Host
	DIMMs      []model.DIMM
	Slots      []model.Slot
	Placements []model.Placement
	Boots      []model.Boot
	Actions    []model.Action
	Scans      []model.Scan
	Sources    []model.Source
	GPUs       []model.GPUMem
	// ECS holds the readings of the DDR5 error check and scrub registers.
	// They are not errors: they say that a DIMM is watched from the inside.
	ECS []model.ECSSample
	// Places lists the names error records use for the place of an error,
	// next to the slot the records were tied to.
	Places []store.EventPlace

	events []event

	hostIdx   map[string]int32
	hostNames []string
	slotIdx   map[slotKey]int32
	slots     []slotKey
	dimmIdx   map[string]int32
	dimms     []string
}

type slotKey struct {
	host int32
	name string
}

// Severity classes of the compact event.
const (
	sevCE uint8 = iota + 1
	sevUE
	sevFatal
)

// Scrub types of the compact event.
const (
	scrubUnknown uint8 = iota
	scrubDemand
	scrubPatrol
	scrubOther
)

// Kinds of error vector the compact event can carry.
const (
	vecNone   uint8 = iota
	vecDQ           // mask of failing data lines of one device
	vecBit          // one bit position within the data word
	vecNibble       // mask of failing nibbles of the data word
)

// event is the compact form of a ledger event. One million of them fit in
// about a hundred megabytes, which a model.Event per record would not.
type event struct {
	id   int64
	at   int64 // ErrorDT in microseconds since the Unix epoch
	from int64 // start of the counting window of a counter record, or 0
	addr uint64
	// errors is the number of errors the record adds to totals.
	errors int32
	row    int32
	col    int32
	vec    uint32
	host   int32
	slot   int32 // index into Input.slots, or -1
	dimm   int32 // index into Input.dimms, or -1
	rank   int16
	dev    int16
	bg     int16
	bank   int16

	sev      uint8
	scrub    uint8
	vecKind  uint8
	link     uint8
	counter  bool
	hasAddr  bool
	relative bool // the time counts from boot, not from the epoch
	inexact  bool // the time was estimated
}

// DIMM link values of the compact event.
const (
	linkNone uint8 = iota
	linkObserved
	linkAssumed
	linkSlot
	linkAmbiguous
)

// located reports whether bank, row and column are known: the precondition
// for assigning a fault mode.
func (e *event) located() bool {
	return !e.counter && e.row >= 0 && e.col >= 0 && (e.bank >= 0 || e.bg >= 0)
}

// NewInput returns an empty Input.
func NewInput() *Input {
	return &Input{hostIdx: map[string]int32{}, slotIdx: map[slotKey]int32{}, dimmIdx: map[string]int32{}}
}

func (in *Input) host(name string) int32 {
	if i, ok := in.hostIdx[name]; ok {
		return i
	}
	i := int32(len(in.hostNames))
	in.hostIdx[name] = i
	in.hostNames = append(in.hostNames, name)
	return i
}

func (in *Input) slot(host int32, name string) int32 {
	if name == "" {
		return -1
	}
	k := slotKey{host, name}
	if i, ok := in.slotIdx[k]; ok {
		return i
	}
	i := int32(len(in.slots))
	in.slotIdx[k] = i
	in.slots = append(in.slots, k)
	return i
}

func (in *Input) dimm(uid string) int32 {
	if uid == "" {
		return -1
	}
	if i, ok := in.dimmIdx[uid]; ok {
		return i
	}
	i := int32(len(in.dimms))
	in.dimmIdx[uid] = i
	in.dimms = append(in.dimms, uid)
	return i
}

func optInt16(o model.Opt[int]) int16 {
	if !o.Valid || o.V < 0 || o.V > 1<<15-1 {
		return -1
	}
	return int16(o.V)
}

func optInt32(o model.Opt[int]) int32 {
	if !o.Valid || o.V < 0 || o.V > 1<<31-1 {
		return -1
	}
	return int32(o.V)
}

// parseVector reads the ErrorVector forms the adapters produce:
// "dq:0x<mask>", "bit:<n>" and "nibble:0x<mask>".
func parseVector(s string) (kind uint8, v uint32) {
	name, val, ok := strings.Cut(s, ":")
	if !ok {
		return vecNone, 0
	}
	switch name {
	case "dq", "nibble":
		n, err := strconv.ParseUint(strings.TrimPrefix(val, "0x"), 16, 32)
		if err != nil || n == 0 {
			return vecNone, 0
		}
		if name == "dq" {
			return vecDQ, uint32(n)
		}
		return vecNibble, uint32(n)
	case "bit":
		n, err := strconv.ParseUint(val, 10, 16)
		if err != nil {
			return vecNone, 0
		}
		return vecBit, uint32(n)
	}
	return vecNone, 0
}

// Add puts one ledger event into the Input. Records that add nothing to the
// totals are left out: informational records, duplicates, and counter
// records whose errors located records already account for.
func (in *Input) Add(e *model.Event) {
	n := e.Errors()
	if n <= 0 {
		return
	}
	if n > 1<<31-1 {
		n = 1<<31 - 1
	}
	h := in.host(e.Host)
	c := event{
		id: e.ID, at: e.ErrorDT.UnixMicro(), errors: int32(n),
		host: h, slot: in.slot(h, e.Slot), dimm: in.dimm(e.DIMMUID),
		rank: optInt16(e.Rank), dev: optInt16(e.DeviceId), bg: optInt16(e.BankGroup), bank: optInt16(e.Bank),
		row: optInt32(e.Row), col: optInt32(e.Column),
		counter:  e.Kind == model.KindCounter,
		relative: e.TimeQuality == model.TimeRelative,
		inexact:  e.TimeQuality == model.TimeEstimated,
	}
	if !e.WindowStart.IsZero() {
		c.from = e.WindowStart.UnixMicro()
	}
	if e.OsPhysicalAddress.Valid {
		c.addr, c.hasAddr = e.OsPhysicalAddress.V, true
	}
	switch e.ErrorSeverity {
	case model.SevCE:
		c.sev = sevCE
	case model.SevFatal:
		c.sev = sevFatal
	default:
		c.sev = sevUE
	}
	switch e.ErrorScrubType {
	case model.ScrubDemand:
		c.scrub = scrubDemand
	case model.ScrubPatrol:
		c.scrub = scrubPatrol
	case model.ScrubOther:
		c.scrub = scrubOther
	}
	switch e.DIMMLink {
	case model.LinkObserved:
		c.link = linkObserved
	case model.LinkAssumed:
		c.link = linkAssumed
	case model.LinkSlot:
		c.link = linkSlot
	case model.LinkAmbiguous:
		c.link = linkAmbiguous
	}
	c.vecKind, c.vec = parseVector(e.ErrorVector)
	in.events = append(in.events, c)
}

// Events returns the number of records the Input holds.
func (in *Input) Events() int { return len(in.events) }

// Load reads a ledger into an Input.
func Load(ctx context.Context, s *store.Store) (*Input, error) {
	in := NewInput()
	var err error
	fail := func(what string, err error) (*Input, error) {
		return nil, fmt.Errorf("engine: read %s: %w", what, err)
	}
	if in.Hosts, err = s.Hosts(ctx); err != nil {
		return fail("hosts", err)
	}
	if in.DIMMs, err = s.DIMMs(ctx); err != nil {
		return fail("DIMMs", err)
	}
	for _, h := range in.Hosts {
		in.host(h.Name)
	}
	if in.Placements, err = s.Placements(ctx, "", ""); err != nil {
		return fail("placements", err)
	}
	if in.Boots, err = s.Boots(ctx, ""); err != nil {
		return fail("boots", err)
	}
	if in.Actions, err = s.Actions(ctx, store.ActionFilter{}); err != nil {
		return fail("actions", err)
	}
	if in.Scans, err = s.Scans(ctx); err != nil {
		return fail("scans", err)
	}
	if in.Sources, err = s.Sources(ctx, 0); err != nil {
		return fail("source inventory", err)
	}
	if in.Places, err = s.EventPlaces(ctx, ""); err != nil {
		return fail("event places", err)
	}
	for e, err := range s.Events(ctx, store.EventFilter{}) {
		if err != nil {
			return fail("events", err)
		}
		in.Add(e)
	}
	// Slots and GPU records are kept per host. A host is known through its
	// own record, or through the records that name it.
	for _, p := range in.Placements {
		in.host(p.Host)
	}
	for _, a := range in.Actions {
		in.host(a.Host)
	}
	for _, name := range in.hostNames {
		slots, err := s.Slots(ctx, name)
		if err != nil {
			return fail("slots", err)
		}
		in.Slots = append(in.Slots, slots...)
		gpus, err := s.GPUMem(ctx, name)
		if err != nil {
			return fail("GPU memory records", err)
		}
		in.GPUs = append(in.GPUs, gpus...)
	}
	if in.ECS, err = s.ECS(ctx, store.ECSFilter{}); err != nil {
		return fail("ECS readings", err)
	}
	return in, nil
}
