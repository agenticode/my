package redfish

import (
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// A file that is not readable as JSON was counted as skipped and not as
// looked at: "0 records, 1 not usable". Found by FuzzDetect of package
// scan.
func TestUnreadableFileIsPartOfTheCount(t *testing.T) {
	var c adapter.Collected
	rep := NewReader(strings.NewReader(`{"@odata.id"`), "dump.json", Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourceError || rep.Records != 1 || rep.Skipped != 1 || rep.Detail != "no file could be read as JSON" {
		t.Errorf("one unreadable file: %+v", rep)
	}
	// A value that is read and one that is not, in one file.
	doc := `{"@odata.type":"#LogEntry.v1_1_0.LogEntry","Id":"1","EntryType":"Oem","Severity":"Warning","Created":"2026-11-09T05:10:00Z","Message":"DIMM_A1 Correctable ECC"} {"Id":`
	c = adapter.Collected{}
	rep = NewReader(strings.NewReader(doc), "dump.json", Options{Now: now}).Collect(ctx, &c)
	if rep.Status != model.SourcePartial || rep.Records != 2 || rep.Skipped != 1 || len(c.Events) != 1 {
		t.Errorf("a log entry and a broken value: %+v, %d events", rep, len(c.Events))
	}
}
