package bundle

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// A bundle of a ledger with a carried record, the end of a boot and the
// checks of preflight: the columns travel, and anonymizing treats what
// preflight read like a kernel command line.
func TestCarriedRecordsAndChecks(t *testing.T) {
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	when := time.Date(2026, 11, 12, 3, 0, 0, 0, time.UTC)
	const host = "r12-n034.dc1.example.com"
	const uuid = "4f3c2b1a-9e8d-4c7b-a6f5-0e1d2c3b4a59"
	err = s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&model.Host{Name: host, FirstSeen: when, LastSeen: when}); err != nil {
			return err
		}
		for i, id := range []string{"0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210"} {
			if _, err := w.UpsertBoot(&model.Boot{Host: host, BootID: id, StartedAt: when.Add(time.Duration(i) * time.Hour), LastSeen: when.Add(time.Duration(i)*time.Hour + 30*time.Minute)}); err != nil {
				return err
			}
		}
		e := &model.Event{Host: host, ErrorDT: when.Add(time.Hour + time.Minute), ErrorSeverity: model.SevFatal, Source: "klog/mce", RawRef: "/sys/fs/pstore/dmesg-erst-1#L3",
			OsPhysicalAddress: model.Some(uint64(0x12345040)), Carried: true, Fingerprint: "carried-1"}
		if _, err := w.InsertEvent(e); err != nil {
			return err
		}
		sc := &model.Scan{Host: host, Kind: model.ScanLive, StartedAt: when.Add(2 * time.Hour)}
		if err := w.InsertScan(sc); err != nil {
			return err
		}
		return w.PutCaptureChecks(sc.ID, host, []model.CaptureCheck{{Name: "console", State: "kept",
			Detail:   model.Statement{Code: "pf.console.serial", Args: map[string]any{"console": "ttyS0"}, Text: "a serial console (ttyS0) of " + host},
			Evidence: []string{"/proc/cmdline: root=UUID=" + uuid + " console=ttyS0,115200", "/proc/sys/kernel/hostname: " + host}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, pass := range []func() error{
		func() error { return s.LinkBoots(ctx, host) },
		func() error { return s.LinkCarried(ctx, host) },
		func() error { return s.BootOutcomes(ctx, host) },
	} {
		if err := pass(); err != nil {
			t.Fatal(err)
		}
	}

	plain := write(t, s, Options{})
	holds := map[string]string{}
	for _, item := range plain.Manifest.Collected {
		holds[item.Table] = item.Holds
	}
	if !strings.Contains(holds["capture_check"], "what a reset") || !strings.Contains(holds["boot"], "how a boot ended") {
		t.Errorf("manifest: %q, %q", holds["capture_check"], holds["boot"])
	}
	c, err := Open(ctx, plain.Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cl := readLedger(t, c.Ledger)
	evs, err := cl.EventList(ctx, store.EventFilter{})
	if err != nil || len(evs) != 1 || !evs[0].Carried || evs[0].ReadBootID != "fedcba9876543210fedcba9876543210" || evs[0].BootID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("events in the bundle: %+v, %v", evs, err)
	}
	boots, _ := cl.Boots(ctx, host)
	if len(boots) != 2 || boots[0].End != model.BootStop {
		t.Errorf("boots in the bundle: %+v", boots)
	}
	if checks, _ := cl.CaptureChecks(ctx, host); len(checks) != 1 || checks[0].Detail.Code != "pf.console.serial" {
		t.Errorf("checks in the bundle: %+v", checks)
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
			for _, secret := range []string{"r12-n034", "example.com", uuid} {
				if strings.Contains(v, secret) {
					t.Errorf("%s still holds %q: %q", col, secret, v)
				}
			}
		}
	}
	checks, _ := readLedger(t, a.Ledger).CaptureChecks(ctx, w.Names[host])
	if len(checks) != 1 || checks[0].State != "kept" || !strings.Contains(strings.Join(checks[0].Evidence, " "), "console=ttyS0") {
		t.Errorf("anonymized checks: %+v", checks)
	}
}

func readLedger(t *testing.T, path string) *store.Store {
	t.Helper()
	l, err := store.Open(ctx, path, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l
}
