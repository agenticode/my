package bundle

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// TestFirmwareTablesTravel: firmware records, unread records and the
// registries a BMC published go into a bundle and come out of it; the
// anonymizer treats the vendor's words like any other text of a record.
func TestFirmwareTablesTravel(t *testing.T) {
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	when := time.Date(2026, 11, 3, 2, 14, 7, 0, time.UTC)
	const host = "r12-n034.dc1.example.com"
	const bmcAddr = "10.20.30.40"
	fw := func(common string) *model.FirmwareRecord {
		return &model.FirmwareRecord{Common: common, Vendor: "Example", VendorID: "MEM0804", Registry: "EXAMPLE", RegistryVersion: "2.9",
			Origin: model.OriginBMCLog, Log: "Lclog", Location: "DIMM A7 of " + host, Args: []string{"A7", bmcAddr},
			Text: "repair of DIMM A7 on " + host + " done", Mapping: model.MappingTable, MapRef: "example@1:MEM0804", Basis: "registry"}
	}
	var srcID int64
	err = s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&model.Host{Name: host, FirstSeen: when, LastSeen: when}); err != nil {
			return err
		}
		sc := &model.Scan{Host: host, Kind: model.ScanImport, StartedAt: when.Add(time.Hour)}
		if err := w.InsertScan(sc); err != nil {
			return err
		}
		e := &model.Event{Host: host, ErrorDT: when, ErrorSeverity: model.SevCE, Source: "redfish/log", RawRef: "https://" + bmcAddr + "/redfish/v1/x",
			Label: "DIMM A7", Fingerprint: "e1", ScanID: sc.ID, Firmware: fw("ce-threshold")}
		if _, err := w.InsertEvent(e); err != nil {
			return err
		}
		a := &model.Action{Host: host, At: when.Add(time.Minute), Kind: model.ActionPPR, Result: model.ResultOK, Label: "DIMM A7",
			Source: "redfish/log", RawRef: "entry 2", Fingerprint: "a1", ScanID: sc.ID, Firmware: fw("ppr-done")}
		if _, err := w.InsertAction(a); err != nil {
			return err
		}
		alone := fw("presence")
		alone.Host, alone.At, alone.Fingerprint, alone.Raw, alone.ScanID = host, when.Add(2*time.Minute), "p1", `{"HostName":"`+host+`"}`, sc.ID
		if _, err := w.InsertFirmware(alone); err != nil {
			return err
		}
		src := model.Source{ScanID: sc.ID, Kind: "redfish", Origin: "/home/someone/dump", Status: model.SourcePartial, Records: 4, Skipped: 1, Unread: 1}
		if err := w.InsertSource(&src); err != nil {
			return err
		}
		srcID = src.ID
		if err := w.InsertUnread(src.ID, []model.UnreadRecord{{Ref: "entry 9", Reason: "not JSON", Text: "DIMM A7 on " + host + " at " + bmcAddr}}); err != nil {
			return err
		}
		return w.PutRegistrySeen(&model.RegistrySeen{Host: host, ScanID: sc.ID, SeenAt: when, Prefix: "EXAMPLE", Version: "2.9", Language: "en",
			Messages: 3000, Memory: 122, Mapped: 120, Unmapped: []string{"MEM9999"}, Source: "/home/someone/dump"})
	})
	if err != nil {
		t.Fatal(err)
	}

	plain := write(t, s, Options{})
	holds := map[string]string{}
	for _, item := range plain.Manifest.Collected {
		holds[item.Table] = item.Holds
	}
	for _, tbl := range []string{"fw_record", "unread_record", "registry_seen"} {
		if holds[tbl] == "" || holds[tbl] == tbl {
			t.Errorf("manifest has no description of %s: %q", tbl, holds[tbl])
		}
	}
	c, err := Open(ctx, plain.Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cl := readLedger(t, c.Ledger)
	recs, err := cl.FirmwareRecords(ctx, store.FirmwareFilter{})
	if err != nil || len(recs) != 3 || recs[0].EventID == 0 || recs[1].ActionID == 0 || recs[2].Fingerprint == "" {
		t.Fatalf("firmware records in the bundle: %+v, %v", recs, err)
	}
	if un, _ := cl.UnreadRecords(ctx, srcID); len(un) != 1 || un[0].Reason != "not JSON" {
		t.Errorf("unread records in the bundle: %+v", un)
	}
	if regs, _ := cl.RegistriesSeen(ctx, host); len(regs) != 1 || regs[0].Mapped != 120 {
		t.Errorf("registries in the bundle: %+v", regs)
	}
	c.Close()

	w := write(t, s, Options{Anonymize: true})
	a, err := Open(ctx, w.Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for col, values := range allText(t, a.Ledger) {
		for _, v := range values {
			for _, secret := range []string{"r12-n034", "example.com", bmcAddr, "someone"} {
				if strings.Contains(v, secret) {
					t.Errorf("%s still holds %q: %q", col, secret, v)
				}
			}
		}
	}
	al := readLedger(t, a.Ledger)
	recs, _ = al.FirmwareRecords(ctx, store.FirmwareFilter{})
	if len(recs) != 3 || recs[1].Common != "ppr-done" || recs[1].VendorID != "MEM0804" || recs[1].MapRef != "example@1:MEM0804" ||
		!strings.Contains(recs[1].Location, "DIMM A7") || recs[1].Args[0] != "A7" {
		t.Errorf("anonymized firmware records: %+v", recs)
	}
	if un, _ := al.UnreadRecords(ctx, srcID); len(un) != 1 || !strings.Contains(un[0].Text, "DIMM A7") {
		t.Errorf("anonymized unread records: %+v", un)
	}
}

// TestOlderBundleOpens: a bundle written by a version of schema 7 opens;
// the copy gets the tables of this version, empty, and keeps its records.
func TestOlderBundleOpens(t *testing.T) {
	c, err := Open(ctx, filepath.Join("testdata", "schema7.mycase"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	l := readLedger(t, c.Ledger)
	if v, _ := l.Version(ctx); v != store.SchemaVersion() {
		t.Errorf("schema of the copy: %d", v)
	}
	evs, err := l.EventList(ctx, store.EventFilter{})
	if err != nil || len(evs) != 89 {
		t.Errorf("%d events in the older bundle, want 89 (%v)", len(evs), err)
	}
	if recs, err := l.FirmwareRecords(ctx, store.FirmwareFilter{}); err != nil || len(recs) != 0 {
		t.Errorf("firmware records of an older bundle: %+v, %v", recs, err)
	}
	if hosts, err := l.Hosts(ctx); err != nil || len(hosts) == 0 || hosts[0].CPUSignature != "" || hosts[0].CPUPresent != "" {
		t.Errorf("hosts of an older bundle: %+v, %v", hosts, err)
	} else if spds, err := l.SPDReadings(ctx, hosts[0].Name); err != nil || len(spds) != 0 {
		t.Errorf("SPD readings of an older bundle: %+v, %v", spds, err)
	}
}
