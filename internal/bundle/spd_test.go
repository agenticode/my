package bundle

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// SPD readings travel in a bundle with their bytes and the probe; the
// anonymiser keeps the bytes and the serial numbers in them, replaces the
// host name, and treats the origin as a path whose home directory is
// replaced.
func TestSPDInABundle(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(dir, "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	raw := append([]byte{0x30, 0x10, 0x12, 0x01}, bytes.Repeat([]byte{0x4D, 0x59}, 510)...)
	probe := "spd-probe 1\ni2c present\nclient 0-0050 name=spd5118 driver=spd5118 eeprom=1024 read=1024 state=ok\n"
	err = s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&model.Host{Name: "db-17.example.net"}); err != nil {
			return err
		}
		if err := w.PutReading(&model.HostReading{Host: "db-17.example.net", Kind: model.ReadingSPDProbe, TakenAt: at, Origin: "/sys/bus/i2c/devices", Text: probe}); err != nil {
			return err
		}
		for _, r := range []model.SPDReading{
			{Host: "db-17.example.net", Origin: "/sys/bus/i2c/devices/0-0050/eeprom", Bus: "i2c-0", Address: 0x50, Driver: "spd5118", Form: model.SPDFormEEPROM, State: "ok", Raw: raw, LastSeen: at},
			{Host: "db-17.example.net", Origin: "/home/alice/cases/db-17/dimm-a1.hex", Form: model.SPDFormHex, State: "ok", Raw: raw, LastSeen: at},
			{Host: "db-17.example.net", Origin: "/home/alice/cases/db-17/dimms.txt", Form: model.SPDFormDecodeDimms, State: "ok", Fields: `{"path":"/home/alice/x","serial":"4D590001"}`, LastSeen: at},
		} {
			if _, err := w.PutSPD(&r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	for _, anon := range []bool{false, true} {
		s, err := store.Open(ctx, filepath.Join(dir, "ledger.db"), store.ReadWrite)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, map[bool]string{false: "plain.mycase", true: "anon.mycase"}[anon])
		if _, err := Write(ctx, s, path, Options{Now: at, Anonymize: anon}); err != nil {
			t.Fatal(err)
		}
		s.Close()
		c, err := Open(ctx, path, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		ls, err := store.Open(ctx, c.Ledger, store.ReadWrite)
		if err != nil {
			t.Fatal(err)
		}
		hosts, _ := ls.Hosts(ctx)
		spds, _ := ls.SPDReadings(ctx, hosts[0].Name)
		rs, _ := ls.Readings(ctx, hosts[0].Name)
		ls.Close()
		c.Close()
		if len(spds) != 3 || len(rs) != 1 || rs[0].Kind != model.ReadingSPDProbe {
			t.Fatalf("anonymized %v: %d SPD readings, readings %+v", anon, len(spds), rs)
		}
		for _, r := range spds {
			if r.Form != model.SPDFormDecodeDimms && (!bytes.Equal(r.Raw, raw) || r.SHA256 != store.SPDSum(raw)) {
				t.Errorf("anonymized %v: the bytes of %s changed", anon, r.Origin)
			}
			if r.Origin != "/sys/bus/i2c/devices/0-0050/eeprom" && anon == strings.Contains(r.Origin+r.Fields, "alice") {
				t.Errorf("anonymized %v: origin %q fields %q", anon, r.Origin, r.Fields)
			}
			if anon && r.Form == model.SPDFormDecodeDimms && !strings.Contains(r.Fields, "4D590001") {
				t.Errorf("the serial of the fields: %q", r.Fields)
			}
			if r.Host != hosts[0].Name || anon == (r.Host == "db-17.example.net") {
				t.Errorf("anonymized %v: host %q", anon, r.Host)
			}
		}
	}
}
