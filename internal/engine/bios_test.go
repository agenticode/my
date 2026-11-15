package engine

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// missingText and checkText join the codes of a finding for a search.
func missingText(v *model.Verdict) string { return strings.Join(missingCodes(v), " ") }
func checkText(v *model.Verdict) string   { return strings.Join(checkCodes(v), " ") }

// bmcTree is a dump of a BMC: two modules, log entries about the first,
// and the BIOS settings given.
func bmcTree(biosAttrs string) fstest.MapFS {
	files := map[string]string{
		"redfish/v1/Systems/1/index.json":          `{"@odata.id":"/redfish/v1/Systems/1","@odata.type":"#ComputerSystem.v1_12_0.ComputerSystem","Id":"1","Manufacturer":"Dell Inc.","Model":"PowerEdge R740","HostName":"node-d"}`,
		"redfish/v1/Systems/1/Memory/1/index.json": `{"@odata.id":"/redfish/v1/Systems/1/Memory/1","@odata.type":"#Memory.v1_10_0.Memory","Id":"1","DeviceLocator":"DIMM_A1","CapacityMiB":16384,"SerialNumber":"T0001","Manufacturer":"Example Memory","PartNumber":"EXM16","Status":{"State":"Enabled","Health":"OK"}}`,
		"redfish/v1/Systems/1/Memory/2/index.json": `{"@odata.id":"/redfish/v1/Systems/1/Memory/2","@odata.type":"#Memory.v1_10_0.Memory","Id":"2","DeviceLocator":"DIMM_A2","CapacityMiB":16384,"SerialNumber":"T0002","Manufacturer":"Example Memory","PartNumber":"EXM16","Status":{"State":"Enabled","Health":"OK"}}`,
		"redfish/v1/Managers/1/LogServices/Sel/Entries/index.json": `{"@odata.id":"/redfish/v1/Managers/1/LogServices/Sel/Entries","Members":[
 {"@odata.id":"/redfish/v1/Managers/1/LogServices/Sel/Entries/1","@odata.type":"#LogEntry.v1_6_0.LogEntry","Id":"1","EntryType":"SEL","Created":"2026-11-01T00:00:00Z","SensorType":"Memory","Message":"Correctable ECC DIMM_A1"},
 {"@odata.id":"/redfish/v1/Managers/1/LogServices/Sel/Entries/2","@odata.type":"#LogEntry.v1_6_0.LogEntry","Id":"2","EntryType":"SEL","Created":"2026-11-03T00:00:00Z","SensorType":"Memory","Message":"Correctable ECC DIMM_A1"}]}`,
	}
	if biosAttrs != "" {
		files["redfish/v1/Systems/1/Bios/index.json"] = `{"@odata.id":"/redfish/v1/Systems/1/Bios","@odata.type":"#Bios.v1_1_0.Bios","Id":"Bios","Attributes":{` + biosAttrs + `}}`
	}
	fsys := fstest.MapFS{}
	for name, text := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(text)}
	}
	return fsys
}

// What the BIOS settings say about a BMC's log is on the findings of a
// host the BMC is the only witness of, and changes no grade.
func TestBIOSSettingsSayWhatABMCLogShows(t *testing.T) {
	when := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	run := func(t *testing.T, attrs string) (a1, a2 *model.Verdict) {
		t.Helper()
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		dump := redfish.NewFS(bmcTree(attrs), "dump", redfish.Options{Now: when})
		if _, err := ingest.Run(ctx, s, []adapter.Adapter{dump}, ingest.Options{Now: when}); err != nil {
			t.Fatal(err)
		}
		res, err := Run(ctx, s, Options{Now: when})
		if err != nil {
			t.Fatal(err)
		}
		h := res.Hosts[0]
		if d := h.InSlot("DIMM_A1"); d != nil {
			a1 = d.Verdict
		}
		if d := h.InSlot("DIMM_A2"); d != nil {
			a2 = d.Verdict
		}
		if a1 == nil || a2 == nil {
			t.Fatalf("no finding for a DIMM: %v, %v", a1, a2)
		}
		return a1, a2
	}

	// Without BIOS settings nothing is said about them.
	plain1, plain2 := run(t, "")
	for _, v := range []*model.Verdict{plain1, plain2} {
		if strings.Contains(missingText(v), "bmc_") || strings.Contains(missingText(v), "patrol_off") {
			t.Errorf("without settings: %s", missingText(v))
		}
	}

	// Corrected errors are not logged to the BMC, and patrol scrub is off.
	a1, a2 := run(t, `"MemPatrolScrub":"Disabled","CorrEccSmi":"Disabled","CECriticalSEL":"Enabled"`)
	if got := missingText(a1); !strings.Contains(got, "missing.bmc_ce_off") || !strings.Contains(got, "missing.patrol_off") || strings.Contains(got, "missing.bmc_threshold") {
		t.Errorf("DIMM with errors: %s", got)
	}
	// The DIMM without records rests on the silence of the BMC: it carries
	// the line about the log, and none about scrub finds it does not have.
	if got := missingText(a2); !strings.Contains(got, "missing.bmc_ce_off") || strings.Contains(got, "missing.patrol_off") {
		t.Errorf("DIMM without errors: %s", got)
	}
	if a1.Grade != plain1.Grade || a2.Grade != plain2.Grade || fmt.Sprint(a1.Rules) != fmt.Sprint(plain1.Rules) {
		t.Errorf("the settings changed a finding: %s %v, was %s %v", a1.Grade, a1.Rules, plain1.Grade, plain1.Rules)
	}
	for _, m := range a1.Missing {
		if m.Code == "missing.bmc_ce_off" {
			if got := Text(i18n.EN, m); got != "the firmware logs no corrected error to the BMC (CorrEccSmi = Disabled): the BMC log holds none, and its silence says nothing about corrected errors" {
				t.Errorf("text: %s", got)
			}
			if ko := Text(i18n.KO, m); !strings.Contains(ko, "CorrEccSmi = Disabled") {
				t.Errorf("text in Korean: %s", ko)
			}
		}
	}

	// Corrected errors are logged, and the threshold event too: the BMC
	// counts threshold events.
	a1, a2 = run(t, `"MemPatrolScrub":"Standard","CorrEccSmi":"Enabled","CECriticalSEL":"Enabled"`)
	if got := missingText(a1); !strings.Contains(got, "missing.bmc_threshold") || strings.Contains(got, "missing.bmc_ce_off") || strings.Contains(got, "missing.patrol_off") {
		t.Errorf("threshold event on: %s", got)
	}
	if got := missingText(a2); !strings.Contains(got, "missing.bmc_threshold") {
		t.Errorf("threshold event on, DIMM without errors: %s", got)
	}

	// Settings that say neither: no line.
	a1, _ = run(t, `"MemPatrolScrub":"Standard","CorrEccSmi":"Enabled","CECriticalSEL":"Disabled"`)
	if got := missingText(a1); strings.Contains(got, "bmc_") || strings.Contains(got, "patrol_off") {
		t.Errorf("neither: %s", got)
	}
}

// Silence after a repair says less when patrol scrub is off, and the
// finding says which BIOS attribute tells it.
func TestPatrolScrubOffAndSilence(t *testing.T) {
	ds := generate(t, "repaired", 1, nil)
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	res0, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: ds.Scenario.End})
	if err != nil {
		t.Fatal(err)
	}
	quiet := func() *model.Verdict {
		res, err := Run(ctx, s, Options{Now: ds.Scenario.End})
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range res.Hosts[0].DIMMs {
			if d.Verdict != nil && has(d.Verdict.Rules, RuleQuiet) {
				return d.Verdict
			}
		}
		t.Fatal("no DIMM with the rule for silence after a repair")
		return nil
	}
	before := quiet()
	if got := checkText(before); strings.Contains(got, "check.patrol_off") {
		t.Fatalf("checks without settings: %s", got)
	}
	// The same host, with the BIOS settings of a BMC: patrol scrub off.
	err = s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&model.Host{Name: res0.Host, SystemVendor: "HPE", FirstSeen: ds.Scenario.End, LastSeen: ds.Scenario.End}); err != nil {
			return err
		}
		return w.PutBIOSSetting(&model.BIOSSetting{Host: res0.Host, ObservedAt: ds.Scenario.End, Name: "MemPatrolScrubbing", Value: "Disabled", HasValue: true, Source: "redfish/bios"})
	})
	if err != nil {
		t.Fatal(err)
	}
	after := quiet()
	if after.Grade != before.Grade {
		t.Errorf("grade %s, was %s", after.Grade, before.Grade)
	}
	if got := missingText(after); !strings.Contains(got, "missing.patrol_off") {
		t.Errorf("missing: %s", got)
	}
	// Where the finding said that no scrub find is on record, it now says
	// why.
	if strings.Contains(checkText(before), "check.no_patrol") {
		if got := checkText(after); !strings.Contains(got, "check.patrol_off") || strings.Contains(got, "check.no_patrol") {
			t.Errorf("checks: %s", got)
		}
	}
	// A reading from before the time of the analysis counts; one from
	// after it does not.
	res, err := Run(ctx, s, Options{Now: ds.Scenario.End, AsOf: ds.Scenario.End.Add(-24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Hosts[0].DIMMs {
		if d.Verdict != nil && strings.Contains(missingText(d.Verdict), "missing.patrol_off") {
			t.Errorf("a setting read after the time of the analysis was used")
		}
	}
}
