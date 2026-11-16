package eventstream

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// FuzzLine writes the lines of an event, an action and a firmware record
// made of arbitrary texts and numbers: each is one line of JSON that reads
// back to the texts it was given.
func FuzzLine(f *testing.F) {
	f.Add("DIMM_A1", "MEM0702", "Correctable memory error rate exceeded for DIMM A1.", `{"Id":"1"}`, "ppr", int64(1762916400), 3, true)
	f.Add("", "", "", "", "", int64(0), 0, false)
	f.Add(" \x00\"\\", "<id>", "a\nb", "\xff\xfe", "relative", int64(-1), -5, true)
	f.Fuzz(func(t *testing.T, slot, id, text, raw, kind string, sec int64, n int, relative bool) {
		q := model.TimeExact
		if relative {
			q = model.TimeRelative
		}
		at := time.Unix(sec%(1<<40), 0)
		fw := &model.FirmwareRecord{Common: kind, VendorID: id, Text: text, Location: slot, Args: []string{text, slot}, Mapping: model.MappingTable,
			MapRef: id, Raw: raw, Source: "redfish/log", Fingerprint: id + "f", At: at, Slot: slot}
		e := &model.Event{ErrorDT: at, TimeQuality: q, ErrorSeverity: model.Severity(kind), ErrorType: text, Slot: slot, Count: n, Raw: raw,
			Fingerprint: id, Source: slot, Row: model.Some(n)}
		a := &model.Action{At: at, TimeQuality: q, Kind: model.ActionKind(kind), Detail: text, Slot: slot, Raw: raw, Fingerprint: id, Source: slot}
		for _, l := range []*Line{
			EventLine("h", e, nil, nil, store.RecordLinks{}, true),
			EventLine("h", e, fw, &DIMM{Serial: text}, store.RecordLinks{}, true),
			ActionLine("h", a, fw, nil, store.RecordLinks{}, true),
			FirmwareLine("h", fw, true),
		} {
			var b bytes.Buffer
			enc := json.NewEncoder(&b)
			enc.SetEscapeHTML(false)
			if err := enc.Encode(l); err != nil {
				t.Fatal(err)
			}
			if strings.Count(b.String(), "\n") != 1 {
				t.Fatalf("a line holds a line break: %q", b.String())
			}
			var back Line
			if err := json.Unmarshal(b.Bytes(), &back); err != nil {
				t.Fatalf("%v: %q", err, b.String())
			}
			// JSON carries valid UTF-8 only; each byte that is not becomes
			// U+FFFD, as it does in a conversion to runes.
			valid := func(s string) string { return string([]rune(s)) }
			if valid(l.Slot) != back.Slot || valid(l.Raw) != back.Raw ||
				back.Schema != Schema || (l.Time == "") != (back.Time == "") {
				t.Fatalf("read back %+v from %+v", back, l)
			}
		}
	})
}
