package bundle

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// A bundle with what a BMC said: the address of the BMC is replaced like
// a host name, wherever it occurs, and the settings and counts stay.
func TestAnonymizeBMCRecords(t *testing.T) {
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	when := time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC)
	const host, bmcName, bmcIP = "r12-n034.dc1.example.com", "bmc-r12-n034.mgmt.example.net", "10.9.8.7"
	err = s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&model.Host{Name: host, FirstSeen: when, LastSeen: when}); err != nil {
			return err
		}
		for i, address := range []string{bmcName, bmcIP} {
			b := &model.BMC{Host: host, ObservedAt: when.Add(time.Duration(i) * time.Hour), Address: address, Firmware: "2.14.0", Live: true,
				Notes:  []string{"the BMC at " + address + " stopped answering after 37 resources"},
				Logs:   []model.BMCLog{{ID: "SEL", Path: "/redfish/v1/Systems/1/LogServices/SEL", Entries: 3}},
				Source: "redfish/manifest", RawRef: "bmc:" + address + "/manifest.json", Fingerprint: model.Fingerprint(host, "bmc", address)}
			if _, err := w.InsertBMC(b); err != nil {
				return err
			}
		}
		for name, value := range map[string]string{"PatrolScrub": "Enabled", "MemoryRemoteLog": "syslog://" + bmcIP + ":514"} {
			if err := w.PutBIOSSetting(&model.BIOSSetting{Host: host, ObservedAt: when, Name: name, Value: value, HasValue: true, Allowed: []string{"Enabled", "Disabled"},
				Source: "redfish/bios", RawRef: "bmc:" + bmcName + "/redfish/v1/Systems/1/Bios/index.json"}); err != nil {
				return err
			}
		}
		_, err := w.InsertBMCMemory(&model.BMCMemory{Host: host, ObservedAt: when, Label: "DIMM_A1", Slot: "DIMM_A1", ServiceLabel: "DIMM 1", Metrics: "ok",
			CELifetime: model.Some(int64(1234)), Source: "redfish/memory", RawRef: "bmc:" + bmcName + "/redfish/v1/Systems/1/Memory/DIMM1/index.json",
			Raw: `{"Id":"DIMM1","Oem":{"Seen":"by ` + bmcIP + `"}}`, Fingerprint: model.Fingerprint(host, "mem", "DIMM_A1")})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// Without anonymizing the bundle says what it holds.
	plain := write(t, s, Options{})
	holds := map[string]string{}
	for _, item := range plain.Manifest.Collected {
		holds[item.Table] = item.Holds
	}
	for table, word := range map[string]string{"bmc": "readings of a BMC", "bios_setting": "settings of the system firmware", "bmc_memory": "memory modules as a BMC describes them"} {
		if !strings.Contains(holds[table], word) {
			t.Errorf("manifest says of %s: %q", table, holds[table])
		}
	}

	w := write(t, s, Options{Anonymize: true})
	if len(w.Names) != 3 || w.Names[bmcName] == "" || w.Names[bmcIP] == "" || w.Names[host] == "" {
		t.Fatalf("names = %v", w.Names)
	}
	c, err := Open(ctx, w.Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for col, values := range allText(t, c.Ledger) {
		for _, v := range values {
			for _, secret := range []string{"r12-n034", "example.com", "example.net", "mgmt", bmcIP} {
				if strings.Contains(v, secret) {
					t.Errorf("%s still holds %q: %q", col, secret, v)
				}
			}
		}
	}
	out := openStore(t, c.Ledger)
	bmcs, err := out.BMCs(ctx, "")
	if err != nil || len(bmcs) != 2 {
		t.Fatalf("BMCs = %+v, %v", bmcs, err)
	}
	if bmcs[0].Address != w.Names[bmcName] || bmcs[1].Address != w.Names[bmcIP] || bmcs[0].Host != w.Names[host] {
		t.Errorf("addresses = %q, %q; host %q", bmcs[0].Address, bmcs[1].Address, bmcs[0].Host)
	}
	// The same name is the same word wherever it stood.
	if !strings.Contains(bmcs[0].Notes[0], w.Names[bmcName]) || !strings.HasPrefix(bmcs[0].RawRef, "bmc:"+w.Names[bmcName]+"/") {
		t.Errorf("note %q, raw_ref %q", bmcs[0].Notes[0], bmcs[0].RawRef)
	}
	if bmcs[0].Firmware != "2.14.0" || len(bmcs[0].Logs) != 1 || bmcs[0].Logs[0].Path != "/redfish/v1/Systems/1/LogServices/SEL" {
		t.Errorf("what is no name was changed: %+v", bmcs[0])
	}
	settings, err := out.BIOSSettings(ctx, "")
	if err != nil || len(settings) != 2 {
		t.Fatalf("settings = %+v, %v", settings, err)
	}
	for _, set := range settings {
		switch set.Name {
		case "PatrolScrub":
			if set.Value != "Enabled" || len(set.Allowed) != 2 {
				t.Errorf("PatrolScrub = %+v", set)
			}
		case "MemoryRemoteLog":
			// The address is known as the address of a BMC: it gets the
			// word of that BMC here too.
			if set.Value != "syslog://"+w.Names[bmcIP]+":514" {
				t.Errorf("MemoryRemoteLog = %q", set.Value)
			}
		}
	}
	mems, err := out.BMCMemory(ctx, "")
	if err != nil || len(mems) != 1 || mems[0].CELifetime.V != 1234 || mems[0].Label != "DIMM_A1" || mems[0].ServiceLabel != "DIMM 1" {
		t.Errorf("memory = %+v, %v", mems, err)
	}
}
