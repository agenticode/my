package bundle

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Readings travel in a bundle; the anonymiser replaces the host name in
// their text, where sar prints it in its first line.
func TestReadingsInABundle(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(dir, "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	sar := "Linux 6.12.0-55.el10.x86_64 (db-17.example.net) \t11/17/2026 \t_x86_64_\t(8 CPU)\n\n00:00:01 pgpgin/s\n00:10:01 1.00\n"
	err = s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&model.Host{Name: "db-17.example.net", Sockets: 2, CPU: "Example CPU", CPUSignature: "6/85/4", CPUPresent: "10"}); err != nil {
			return err
		}
		return w.PutReading(&model.HostReading{Host: "db-17.example.net", Kind: model.ReadingSarB, TakenAt: at, Origin: "sar -B", Text: sar})
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
		rs, _ := ls.Readings(ctx, hosts[0].Name)
		ls.Close()
		c.Close()
		if len(rs) != 1 || hosts[0].Sockets != 2 || hosts[0].CPU != "Example CPU" || hosts[0].CPUSignature != "6/85/4" || hosts[0].CPUPresent != "10" {
			t.Fatalf("anonymized %v: hosts %+v, readings %+v", anon, hosts, rs)
		}
		named := strings.Contains(rs[0].Text, "db-17.example.net")
		if anon == named {
			t.Errorf("anonymized %v: the host name in the text: %q", anon, rs[0].Text)
		}
		if anon && !strings.Contains(rs[0].Text, "(host-") {
			t.Errorf("anonymized text %q", rs[0].Text)
		}
	}
}
