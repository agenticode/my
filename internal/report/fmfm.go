package report

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/agenticode/my/internal/model"
)

// fmfmRecord is an error record with the field names of the FMFM runtime
// error log, in the order the framework document lists them. The fields
// in lower case are additions of MemorY. A field a source did not report
// is left out: nothing is filled in.
type fmfmRecord struct {
	ErrorDT           string `json:"ErrorDT,omitempty"`
	ErrorScrubType    string `json:"ErrorScrubType,omitempty"`
	ErrorSeverity     string `json:"ErrorSeverity"`
	HostID            string `json:"HostID"`
	SocketId          *int   `json:"SocketId,omitempty"`
	ChannelId         *int   `json:"ChannelId,omitempty"`
	ChannelSlotId     *int   `json:"ChannelSlotId,omitempty"`
	SubChannel        *int   `json:"SubChannel,omitempty"`
	Rank              *int   `json:"Rank,omitempty"`
	DeviceId          *int   `json:"DeviceId,omitempty"`
	BankGroup         *int   `json:"BankGroup,omitempty"`
	Bank              *int   `json:"Bank,omitempty"`
	Row               *int   `json:"Row,omitempty"`
	Column            *int   `json:"Column,omitempty"`
	ErrorVector       string `json:"ErrorVector,omitempty"`
	OsPhysicalAddress string `json:"OsPhysicalAddress,omitempty"`

	// SecondsSinceBoot stands in for ErrorDT on a record that has no date.
	SecondsSinceBoot *int64 `json:"seconds_since_boot,omitempty"`
	Count            int    `json:"count,omitempty"`
	Slot             string `json:"slot,omitempty"`
	Grade            string `json:"grade"`
	Source           string `json:"source"`
	RawRef           string `json:"raw_ref"`
}

func optPtr(o model.Opt[int]) *int {
	if !o.Valid {
		return nil
	}
	v := o.V
	return &v
}

// fmfmOf writes an error record with FMFM field names.
func fmfmOf(e *model.Event) fmfmRecord {
	r := fmfmRecord{
		ErrorScrubType: string(e.ErrorScrubType), ErrorSeverity: string(e.ErrorSeverity), HostID: e.Host,
		SocketId: optPtr(e.SocketId), ChannelId: optPtr(e.ChannelId), ChannelSlotId: optPtr(e.ChannelSlotId), SubChannel: optPtr(e.SubChannel),
		Rank: optPtr(e.Rank), DeviceId: optPtr(e.DeviceId), BankGroup: optPtr(e.BankGroup), Bank: optPtr(e.Bank),
		Row: optPtr(e.Row), Column: optPtr(e.Column), ErrorVector: e.ErrorVector,
		Slot: e.Slot, Grade: string(e.Grade), Source: e.Source, RawRef: e.RawRef,
	}
	if e.TimeQuality == model.TimeRelative {
		s := e.ErrorDT.Unix()
		r.SecondsSinceBoot = &s
	} else if !e.ErrorDT.IsZero() {
		r.ErrorDT = e.ErrorDT.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	}
	if e.OsPhysicalAddress.Valid {
		// As text: an address can be larger than a JSON reader keeps exact.
		r.OsPhysicalAddress = fmt.Sprintf("%#x", e.OsPhysicalAddress.V)
	}
	if e.Count != 1 {
		r.Count = e.Count
	}
	return r
}

// fmfmLines writes the error records of the report with FMFM field names,
// one JSON object per line.
func (doc *Document) fmfmLines() (string, int, error) {
	var b strings.Builder
	n := 0
	for _, r := range doc.Records {
		if r.Event == nil {
			continue
		}
		line, err := json.Marshal(fmfmOf(r.Event))
		if err != nil {
			return "", 0, err
		}
		b.Write(line)
		b.WriteByte('\n')
		n++
	}
	return b.String(), n, nil
}
