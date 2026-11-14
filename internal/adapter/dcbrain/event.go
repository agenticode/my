package dcbrain

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/agenticode/my/internal/model"
)

// Source is the source name of the ledger records this package produces.
const Source = "dcbrain/mcelog"

// Decoder names where the location fields come from: the columns of the
// dataset, taken as they are.
const Decoder = "dcbrain"

// DefaultBase is the real time day 0 of the dataset calendar is placed at
// unless a caller chooses another. It is the dataset's year 0001 moved to
// 2001. It says nothing about when the errors happened.
var DefaultBase = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// Topology returns the place of a DIMM on the board for a memoryid: two
// sockets with six channels of two DIMMs each. The dataset description
// does not give this layout; it is the one the authors' own analysis
// scripts use (socket = id / 12, channel = (id mod 12) / 2, position =
// id mod 2).
func Topology(memoryID int) (socket, channel, position int) {
	return memoryID / 12, memoryID % 12 / 2, memoryID % 2
}

// SlotName returns the name the ledger uses for the slot of a memoryid.
func SlotName(memoryID int) string { return "DIMM" + strconv.Itoa(memoryID) }

// Rec is the compact form of one or more rows of one server that share
// place, error type and time. A fleet has tens of millions of rows; a Rec
// takes 24 bytes.
type Rec struct {
	// At is the time on the dataset calendar in seconds, the earliest of
	// the rows the Rec stands for.
	At uint32
	// Row is the row address.
	Row uint32
	// Count is the number of rows the Rec stands for, at least 1.
	Count uint32
	// Line is the file line of the first of those rows.
	Line     uint32
	Col      uint16
	MemoryID uint8
	Rank     uint8
	Bank     uint8
	Type     ErrorType
}

// Rec returns the compact form of the row.
func (r *Row) Rec() Rec {
	return Rec{At: uint32(r.At), Row: uint32(r.Row), Count: 1, Line: uint32(min(r.Line, 1<<32-1)),
		Col: uint16(r.Col), MemoryID: uint8(r.MemoryID), Rank: uint8(r.Rank), Bank: uint8(r.Bank), Type: r.Type}
}

// Stamp returns the time of the record.
func (c *Rec) Stamp() Stamp { return Stamp(c.At) }

func (c *Rec) place() [6]uint32 {
	return [6]uint32{uint32(c.MemoryID), uint32(c.Rank), uint32(c.Bank), c.Row, uint32(c.Col), uint32(c.Type)}
}

// Merge sorts the records of one server by time and merges the records
// that share place and error type and whose times fall into the same
// bucket of the given number of seconds. The merged record keeps the
// earliest time and the first line, and its count is the sum.
//
// With a bucket of one second nothing is lost: the dataset writes times in
// whole seconds, so rows that merge are equal in every column. A wider
// bucket trades the order of errors within a bucket for fewer records.
//
// Merge works in place and returns the shortened slice.
func Merge(recs []Rec, bucket uint32) []Rec {
	if bucket == 0 {
		bucket = 1
	}
	sort.Slice(recs, func(i, j int) bool {
		a, b := &recs[i], &recs[j]
		if x, y := a.At/bucket, b.At/bucket; x != y {
			return x < y
		}
		if x, y := a.place(), b.place(); x != y {
			for k := range x {
				if x[k] != y[k] {
					return x[k] < y[k]
				}
			}
		}
		if a.At != b.At {
			return a.At < b.At
		}
		return a.Line < b.Line
	})
	out := recs[:0]
	for i := range recs {
		c := recs[i]
		if n := len(out); n > 0 {
			last := &out[n-1]
			if last.At/bucket == c.At/bucket && last.place() == c.place() {
				if sum := uint64(last.Count) + uint64(c.Count); sum <= 1<<32-1 {
					last.Count = uint32(sum)
					continue
				}
			}
		}
		out = append(out, c)
	}
	// Within a bucket the records are ordered by place. Readers expect
	// time order.
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// Fill writes the ledger record of rec into e, which is reset first. host
// is the server ID and base the real time of day 0 of the dataset
// calendar. Fill leaves out what only a ledger import needs (the record
// reference, the raw text and the fingerprint); Adapter adds those.
//
// What the record says and does not say:
//
//   - Every row is a corrected error. The dataset logs correctable errors;
//     uncorrectable errors appear only as failure tickets of the server.
//   - Rank, bank, row and column are set, which is observation grade A.
//     The DRAM device and the failing bits are not in the dataset, and
//     neither is a physical address.
//   - The 16 banks of a rank are kept as the dataset numbers them. Bank
//     group and bank address are not told apart.
//   - Socket, channel and position come from Topology.
func Fill(e *model.Event, host string, rec *Rec, base time.Time) {
	*e = model.Event{
		Host:          host,
		Kind:          model.KindError,
		ErrorDT:       rec.Stamp().Time(base),
		ErrorSeverity: model.SevCE,
		Rank:          model.Some(int(rec.Rank)),
		Bank:          model.Some(int(rec.Bank)),
		Row:           model.Some(int(rec.Row)),
		Column:        model.Some(int(rec.Col)),
		Source:        Source,
		Count:         int(rec.Count),
		TimeQuality:   model.TimeExact,
		Label:         SlotName(int(rec.MemoryID)),
		ErrorType:     rec.Type.String(),
		Decoder:       Decoder,
	}
	switch rec.Type {
	case TypeScrub:
		e.ErrorScrubType = model.ScrubPatrol
	case TypeRead, TypeWrite:
		e.ErrorScrubType = model.ScrubDemand
	}
	socket, channel, position := Topology(int(rec.MemoryID))
	e.SocketId, e.ChannelId, e.ChannelSlotId = model.Some(socket), model.Some(channel), model.Some(position)
	e.Grade = e.ComputeGrade()
}

// rawText writes the record the way the error table holds it.
func rawText(host string, rec *Rec) string {
	return fmt.Sprintf("%s,%d,%d,%d,%d,%d,%d,%s", host, rec.MemoryID, rec.Rank, rec.Bank, rec.Row, rec.Col, rec.Type, rec.Stamp())
}
