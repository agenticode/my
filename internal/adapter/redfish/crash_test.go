package redfish

import (
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// A message with a letter whose lower case is longer than the letter
// itself (the Turkish capital I with a dot, for one) and the name of a
// memory event at its end made the reader panic: the event was looked for
// in a lower-case copy and cut out of the original at the position found
// there. Found by FuzzLabels of package ipmi.
func TestMessageWithLettersOutsideASCII(t *testing.T) {
	for _, msg := range []string{
		"DİMM_A1: Correctable ECC",
		"ȺȺȺȺ correctable ecc",
		"İİİİİİİİ DIMM_A1 Uncorrectable ECC",
	} {
		doc := `{"Members":[{"@odata.type":"#LogEntry.v1_1_0.LogEntry","Id":"1","EntryType":"Oem","Severity":"Warning","Created":"2026-11-09T05:10:00Z","Message":"` + msg + `"}]}`
		var c adapter.Collected
		rep := NewReader(strings.NewReader(doc), "log.json", Options{Now: now}).Collect(ctx, &c)
		if rep.Status != model.SourceOK || len(c.Events) != 1 {
			t.Errorf("%q: report %+v, %d events", msg, rep, len(c.Events))
			continue
		}
		want := model.SevCE
		if strings.Contains(msg, "Uncorrectable") {
			want = model.SevUE
		}
		if e := c.Events[0]; e.ErrorSeverity != want {
			t.Errorf("%q: severity %s, want %s", msg, e.ErrorSeverity, want)
		}
	}
}
