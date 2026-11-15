package bundle

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
)

func TestMain(m *testing.M) { testledger.Main(m) }

var ctx = context.Background()

var now = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)

func write(t *testing.T, s *store.Store, opts Options) *Written {
	t.Helper()
	if opts.Now.IsZero() {
		opts.Now = now
	}
	if opts.Tool == "" {
		opts.Tool = "my test"
	}
	w, err := Write(ctx, s, filepath.Join(t.TempDir(), "case"+Extension), opts)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func openStore(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(ctx, path, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func errors2(t *testing.T, s *store.Store) (ce, ue int64) {
	t.Helper()
	totals, err := s.Totals(ctx, store.EventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range totals {
		ce, ue = ce+x.CE, ue+x.UE
	}
	return
}

func TestWriteAndOpen(t *testing.T) {
	src := testledger.Open(t, "demo", 1, "")
	sub := &Subject{Host: "r12-n034", Slot: "CPU0_C1", Serial: "3B9C56"}
	w := write(t, src, Options{Subject: sub})

	// The archive: four members, in this order, readable by any ZIP tool.
	zr, err := zip.OpenReader(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if !f.Modified.Equal(now) {
			t.Errorf("%s is dated %v, want the creation time", f.Name, f.Modified)
		}
	}
	zr.Close()
	if got := strings.Join(names, " "); got != "manifest.json sources.json README.txt ledger.db" {
		t.Errorf("members = %s", got)
	}

	m := w.Manifest
	if m.Format != Format || m.FormatVersion != Version || !m.CreatedAt.Equal(now) || m.Tool != "my test" || m.Anonymized {
		t.Errorf("manifest = %+v", m)
	}
	if m.Subject == nil || *m.Subject != *sub {
		t.Errorf("subject = %+v", m.Subject)
	}
	if len(m.Hosts) != 1 || m.Hosts[0].Name != "r12-n034" || m.Hosts[0].CE != 1284 || m.Hosts[0].UE != 1 || m.Hosts[0].DIMMs != 16 {
		t.Errorf("hosts = %+v", m.Hosts)
	}
	rows := map[string]int64{}
	for _, it := range m.Collected {
		rows[it.Table] = it.Rows
		if it.Holds == "" || strings.HasPrefix(it.Holds, "table ") {
			t.Errorf("table %s has no description", it.Table)
		}
	}
	if rows["event"] != 1285 || rows["dimm"] != 16 || rows["boot"] != 4 || rows["action"] != 8 {
		t.Errorf("collected = %v", rows)
	}
	if len(m.NotCollected) == 0 || !strings.Contains(m.NotCollected[0], "contents of memory") {
		t.Errorf("not collected = %v", m.NotCollected)
	}
	if v, _ := src.Version(ctx); m.Ledger.SchemaVersion != v || len(m.Ledger.SHA256) != 64 || m.Ledger.Bytes == 0 {
		t.Errorf("ledger info = %+v", m.Ledger)
	}
	if fi, _ := os.Stat(w.Path); fi.Size() != w.Bytes || w.Bytes >= m.Ledger.Bytes {
		t.Errorf("bundle is %d bytes, the ledger in it %d: it should be compressed", w.Bytes, m.Ledger.Bytes)
	}

	// The manifest and the source inventory can be read without the ledger.
	rm, err := ReadManifest(w.Path)
	if err != nil || rm.Ledger.SHA256 != m.Ledger.SHA256 {
		t.Errorf("ReadManifest: %v", err)
	}
	srcs, err := ReadSources(w.Path)
	if err != nil || len(srcs.Scans) != 1 || len(srcs.Scans[0].Sources) != 1 || srcs.Scans[0].Sources[0].Kind != "synth" || srcs.Scans[0].Kind != "synthetic" {
		t.Errorf("ReadSources: %+v, %v", srcs, err)
	}
	if !IsBundle(w.Path) || IsBundle(testledger.Path(t, "demo", 1, "")) || IsBundle(filepath.Join(t.TempDir(), "none")) {
		t.Error("IsBundle")
	}

	// Opening gives the ledger back, record for record.
	c, err := Open(ctx, w.Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := openStore(t, c.Ledger)
	if ce, ue := errors2(t, got); ce != 1284 || ue != 1 {
		t.Errorf("the opened ledger has CE %d, UE %d", ce, ue)
	}
	a, _ := src.EventList(ctx, store.EventFilter{WithRaw: true, Limit: 50})
	b, _ := got.EventList(ctx, store.EventFilter{WithRaw: true, Limit: 50})
	for i := range a {
		if a[i].Fingerprint != b[i].Fingerprint || a[i].Raw != b[i].Raw || a[i].Host != b[i].Host {
			t.Fatalf("event %d differs after the round trip", i)
		}
	}
	got.Close()
	dir := filepath.Dir(c.Ledger)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Close left %s behind", dir)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}

	// A bundle that exists is not replaced unless the caller says so.
	if _, err := Write(ctx, src, w.Path, Options{Now: now}); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("overwrite without Force: %v", err)
	}
	if _, err := Write(ctx, src, w.Path, Options{Now: now, Force: true}); err != nil {
		t.Errorf("overwrite with Force: %v", err)
	}
	// Nothing is left beside the bundle.
	entries, _ := os.ReadDir(filepath.Dir(w.Path))
	if len(entries) != 1 {
		t.Errorf("%d files beside the bundle", len(entries)-1)
	}
}

func TestReadme(t *testing.T) {
	w := write(t, testledger.Open(t, "demo", 1, ""), Options{Subject: &Subject{Host: "r12-n034", Slot: "CPU0_C1", Serial: "3B9C56"}})
	zr, err := zip.OpenReader(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	b, err := readMember(&zr.Reader, readmeName, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{
		"MemorY case bundle",
		"Written by my test on 2026-11-13 09:30:00 UTC.",
		"the memory errors of 1 host",
		"The case is about the DIMM in slot CPU0_C1 of host r12-n034 (serial number 3B9C56).",
		"my open FILE",
		"r12-n034: 16 DIMMs, CE 1,284, UE 1, 2026-08-03 to 2026-11-12",
		"1,285  memory error records",
		"contents of memory, of files or of processes",
		"Host names and network addresses are in this bundle as the sources recorded them.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("README lacks %q:\n%s", want, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "  0  ") {
			t.Errorf("README lists an empty table: %q", line)
		}
	}
}

// rezip copies a bundle, letting change edit or drop members.
func rezip(t *testing.T, from string, change func(name string, content []byte) ([]byte, bool)) string {
	t.Helper()
	zr, err := zip.OpenReader(from)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	to := filepath.Join(t.TempDir(), "changed"+Extension)
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(out)
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		b, keep := change(f.Name, b)
		if !keep {
			continue
		}
		w, _ := zw.Create(f.Name)
		w.Write(b)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	out.Close()
	return to
}

func TestOpenRejects(t *testing.T) {
	good := write(t, testledger.Open(t, "demo", 1, ""), Options{}).Path
	text := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(text, []byte("not an archive"), 0o644)
	manifest := func(edit func(m *Manifest)) func(string, []byte) ([]byte, bool) {
		return func(name string, b []byte) ([]byte, bool) {
			if name != manifestName {
				return b, true
			}
			var m Manifest
			json.Unmarshal(b, &m)
			edit(&m)
			out, _ := json.Marshal(m)
			return out, true
		}
	}
	for _, tt := range []struct {
		name string
		path string
		want error
		text string
	}{
		{"a text file", text, ErrNotBundle, ""},
		{"a ledger", testledger.Path(t, "demo", 1, ""), ErrNotBundle, ""},
		{"an archive of something else", rezip(t, good, func(n string, b []byte) ([]byte, bool) { return b, n == readmeName }), ErrNotBundle, ""},
		{"another format name", rezip(t, good, manifest(func(m *Manifest) { m.Format = "other" })), ErrNotBundle, ""},
		{"a newer format", rezip(t, good, manifest(func(m *Manifest) { m.FormatVersion = Version + 1 })), ErrNewerFormat, "this tool reads up to 1"},
		{"format version zero", rezip(t, good, manifest(func(m *Manifest) { m.FormatVersion = 0 })), ErrDamaged, ""},
		{"a manifest that is not JSON", rezip(t, good, func(n string, b []byte) ([]byte, bool) {
			if n == manifestName {
				return []byte("{"), true
			}
			return b, true
		}), ErrDamaged, ""},
		{"no ledger", rezip(t, good, func(n string, b []byte) ([]byte, bool) { return b, n != ledgerName }), ErrDamaged, "no ledger.db"},
		{"a ledger that was changed", rezip(t, good, func(n string, b []byte) ([]byte, bool) {
			if n == ledgerName {
				b[len(b)-10] ^= 0xff
			}
			return b, true
		}), ErrDamaged, "does not match the checksum"},
		{"a ledger that is no ledger", rezip(t, good, func(n string, b []byte) ([]byte, bool) {
			if n == ledgerName {
				return []byte("garbage"), true
			}
			if n == manifestName {
				var m Manifest
				json.Unmarshal(b, &m)
				m.Ledger.SHA256 = ""
				b, _ = json.Marshal(m)
			}
			return b, true
		}), store.ErrNotLedger, ""},
	} {
		tmp := t.TempDir()
		c, err := Open(ctx, tt.path, tmp)
		if c != nil {
			c.Close()
		}
		if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.text) {
			t.Errorf("%s: %v, want %v (%q)", tt.name, err, tt.want, tt.text)
		}
		// A bundle that is refused leaves nothing behind.
		if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
			t.Errorf("%s: %d entries left in the temporary directory", tt.name, len(entries))
		}
	}
	if _, err := Open(ctx, filepath.Join(t.TempDir(), "none.mycase"), ""); !os.IsNotExist(err) {
		t.Errorf("a missing file: %v", err)
	}
	if _, err := ReadManifest(text); !errors.Is(err, ErrNotBundle) {
		t.Errorf("ReadManifest of a text file: %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Open(cctx, good, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled open: %v", err)
	}
}

// identifying builds the ledger of a host whose files name it and its
// network: a fully qualified host name, IP addresses and a MAC address on
// the kernel command line, and the short host name in every syslog line.
func identifying(t *testing.T) *store.Store {
	t.Helper()
	ds := testledger.Generate(t, "demo", 1, nil)
	// The copy of the host lies below a home directory, as the files of an
	// import often do: its paths carry the name of an account.
	root := filepath.Join(t.TempDir(), "home", "alice", "root")
	if err := ds.WriteHostTree(root, synth.FlavorIntel); err != nil {
		t.Fatal(err)
	}
	put := func(rel, content string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, ds.Scenario.End, ds.Scenario.End)
	}
	put("proc/sys/kernel/hostname", "r12-n034.dc1.example.com\n")
	put("proc/cmdline", "BOOT_IMAGE=/vmlinuz root=UUID=6b1f0c2a-7d3e-4c5b-9a80-1e2f3a4b5c6d rd.luks.uuid=luks-0f1e2d3c-4b5a-4978-8695-a4b3c2d1e0f9 "+
		"ip=10.20.30.40::10.20.30.1:255.255.255.0:r12-n034.dc1.example.com:eth0:none nameserver=2001:db8::53 "+
		"BOOTIF=01-aa-bb-cc-dd-ee-ff nfsroot=192.168.7.9:/export ifname=bootnet:00:1B:44:11:3A:B7 gw6=fe80::1%eth0\n")
	// The kernel log as a syslog file: every line carries the host name.
	dmesg, err := os.ReadFile(filepath.Join(root, "var", "log", "dmesg"))
	if err != nil {
		t.Fatal(err)
	}
	var syslog strings.Builder
	boot := ds.Boots[len(ds.Boots)-1].StartedAt
	for _, line := range strings.Split(strings.TrimSpace(string(dmesg)), "\n") {
		var sec, usec int64
		if _, err := fmt.Sscanf(line, "[%d.%d]", &sec, &usec); err != nil {
			t.Fatalf("kernel log line without a stamp: %q", line)
		}
		at := boot.Add(time.Duration(sec)*time.Second + time.Duration(usec)*time.Microsecond)
		syslog.WriteString(at.Format("Jan _2 15:04:05") + " r12-n034 kernel: " + line + "\n")
	}
	// A line that is not about memory: it must never reach the ledger.
	syslog.WriteString("Nov 12 23:59:01 r12-n034 kernel: [9999999.000001] nfs: server 172.16.99.99 not responding\n")
	put("var/log/messages", syslog.String())
	os.Remove(filepath.Join(root, "var", "log", "dmesg"))

	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := scan.Run(ctx, s, scan.Options{Root: root, TempDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	res, err := engine.Run(ctx, s, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	return s
}

// allText returns every text value of a ledger file, table by table.
func allText(t *testing.T, path string) map[string][]string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	names, err := tableNames(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, table := range names {
		cols, err := textColumns(ctx, db, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cols {
			rows, err := db.Query(`SELECT DISTINCT ` + quote(c) + ` FROM ` + quote(table) + ` WHERE ` + quote(c) + ` IS NOT NULL`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var v string
				rows.Scan(&v)
				out[table+"."+c] = append(out[table+"."+c], v)
			}
			rows.Close()
		}
	}
	return out
}

func TestAnonymize(t *testing.T) {
	src := identifying(t)
	secrets := []string{"r12-n034", "example.com", "dc1.example", "10.20.30.40", "10.20.30.1", "192.168.7.9", "2001:db8::53", "fe80::1", "aa-bb-cc-dd-ee-ff", "00:1B:44:11:3A:B7", "00:1b:44:11:3a:b7",
		"6b1f0c2a-7d3e-4c5b-9a80-1e2f3a4b5c6d", "0f1e2d3c-4b5a-4978-8695-a4b3c2d1e0f9", "/home/alice"}

	// The ledger does hold what is to be removed: the test is not vacuous.
	plain := write(t, src, Options{})
	pc, err := Open(ctx, plain.Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	found := map[string]bool{}
	for _, values := range allText(t, pc.Ledger) {
		for _, v := range values {
			for _, s := range secrets {
				if strings.Contains(v, s) {
					found[s] = true
				}
			}
		}
	}
	for _, s := range secrets {
		if !found[s] && s != "00:1b:44:11:3a:b7" {
			t.Fatalf("the test ledger does not hold %q", s)
		}
	}
	// A kernel log line that is not about memory is not collected at all.
	if raw, _ := os.ReadFile(pc.Ledger); bytes.Contains(raw, []byte("172.16.99.99")) {
		t.Error("a kernel log line that is not about memory errors is in the ledger")
	}

	w := write(t, src, Options{Anonymize: true, Subject: &Subject{Host: "r12-n034.dc1.example.com", Slot: "CPU0_C1"}})
	m := w.Manifest
	word := w.Names["r12-n034.dc1.example.com"]
	if !m.Anonymized || m.Anonymization == nil || m.Anonymization.Hosts != 1 || m.Anonymization.Addresses < 5 || m.Anonymization.Users < 1 || m.Anonymization.Keyed {
		t.Errorf("anonymization = %+v", m.Anonymization)
	}
	if len(w.Names) != 1 || !strings.HasPrefix(word, "host-") || len(word) != 13 {
		t.Fatalf("names = %v", w.Names)
	}
	if m.Hosts[0].Name != word || m.Subject.Host != word {
		t.Errorf("manifest names the host %q and the subject %q, want %q", m.Hosts[0].Name, m.Subject.Host, word)
	}

	c, err := Open(ctx, w.Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Nothing that names the host or its network is left: not in the
	// ledger, not in the manifest, not in the inventory, not in the README.
	for col, values := range allText(t, c.Ledger) {
		for _, v := range values {
			for _, s := range secrets {
				if strings.Contains(strings.ToLower(v), strings.ToLower(s)) {
					t.Errorf("%s still holds %q: %.200s", col, s, v)
				}
			}
		}
	}
	raw, err := os.ReadFile(c.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range secrets {
		if bytes.Contains(raw, []byte(s)) {
			t.Errorf("the ledger file still holds %q somewhere", s)
		}
	}
	zr, err := zip.OpenReader(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{manifestName, sourcesName, readmeName} {
		b, _ := readMember(&zr.Reader, name, 64<<20)
		for _, s := range secrets {
			if bytes.Contains(b, []byte(s)) {
				t.Errorf("%s still holds %q", name, s)
			}
		}
		if name == readmeName && !strings.Contains(string(b), "Host names, IP addresses, MAC addresses, the disk identifiers of the kernel command line\nand the names of home directories in file paths were replaced") {
			t.Errorf("the README does not say that names were replaced")
		}
	}
	zr.Close()

	// The records still belong together under the new name, and what an
	// analysis needs is still there.
	got := openStore(t, c.Ledger)
	hosts, _ := got.Hosts(ctx)
	if len(hosts) != 1 || hosts[0].Name != word {
		t.Fatalf("hosts = %+v", hosts)
	}
	if ce, ue := errors2(t, got); ce != 1284 || ue != 1 {
		wce, wue := errors2(t, src)
		t.Errorf("after anonymizing: CE %d, UE %d; before: CE %d, UE %d", ce, ue, wce, wue)
	}
	dimms, _ := got.DIMMs(ctx)
	serial := false
	for _, d := range dimms {
		serial = serial || d.Serial == "3B9C56"
	}
	if !serial {
		t.Error("the DIMM serial numbers are gone; a return is about them")
	}
	before, err := engine.Run(ctx, src, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	after, err := engine.Run(ctx, got, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Hosts) != 1 || after.Hosts[0].Host != word || after.Grade() != before.Grade() || len(after.Hosts[0].DIMMs) != len(before.Hosts[0].DIMMs) {
		t.Fatalf("findings changed: %s before, %s after", before.Grade(), after.Grade())
	}
	for i, d := range before.Hosts[0].DIMMs {
		x := after.Hosts[0].DIMMs[i]
		if d.Slot != x.Slot || d.Verdict.Grade != x.Verdict.Grade || strings.Join(d.Verdict.Rules, " ") != strings.Join(x.Verdict.Rules, " ") || d.Features.CE != x.Features.CE {
			t.Errorf("finding of %s changed: %s %v -> %s %v", d.Slot, d.Verdict.Grade, d.Verdict.Rules, x.Verdict.Grade, x.Verdict.Rules)
		}
	}
	// Identifiers derived from the host name were hashed again.
	a, _ := src.EventList(ctx, store.EventFilter{Limit: 5})
	b, _ := got.EventList(ctx, store.EventFilter{Limit: 5})
	for i := range a {
		if a[i].Fingerprint == b[i].Fingerprint || len(b[i].Fingerprint) != 32 {
			t.Errorf("fingerprint of event %d was kept: %s", i, b[i].Fingerprint)
		}
	}

	// The source of the bundle was not touched.
	if hosts, _ := src.Hosts(ctx); hosts[0].Name != "r12-n034.dc1.example.com" {
		t.Errorf("anonymizing changed the ledger it read: %+v", hosts)
	}

	// A key makes the words repeatable; without one every bundle differs.
	k1 := write(t, src, Options{Anonymize: true, Key: []byte("site key")})
	k2 := write(t, src, Options{Anonymize: true, Key: []byte("site key")})
	k3 := write(t, src, Options{Anonymize: true, Key: []byte("another key")})
	r1 := write(t, src, Options{Anonymize: true})
	n := func(w *Written) string { return w.Names["r12-n034.dc1.example.com"] }
	if n(k1) != n(k2) || n(k1) == n(k3) || n(k1) == n(r1) || n(r1) == word || !k1.Manifest.Anonymization.Keyed {
		t.Errorf("words: %s %s %s %s %s", n(k1), n(k2), n(k3), n(r1), word)
	}
}

func TestTextAnonymizing(t *testing.T) {
	a, err := newAnonymizer([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	h := a.host("web-7.prod.example.org")
	a.host("db")
	a.host("localhost")
	a.learn()
	ip := func(s string) string { return a.word("ip-", s, 8) }
	for _, tt := range []struct{ in, want string }{
		{"", ""},
		{"nothing to see", "nothing to see"},
		// Host names: whole words only, with their domain, in any case.
		{"Nov 12 03:14:07 web-7 kernel: EDAC MC0", "Nov 12 03:14:07 " + h + " kernel: EDAC MC0"},
		{"from WEB-7.prod.example.org:22", "from " + h + ":22"},
		{"web-7.prod.example.org", h},
		{"web-71 and xweb-7 are other hosts", "web-71 and xweb-7 are other hosts"},
		{"/cases/web-7.dmesg.txt and web-7.log", "/cases/" + h + ".dmesg.txt and " + h + ".log"},
		{"web-7.other.net is the same host elsewhere", h + " is the same host elsewhere"},
		{"ras-mc_event.db", "ras-mc_event." + a.names["db"]},
		{"localhost kernel: x", "localhost kernel: x"},
		// IPv4: addresses, not version numbers.
		{"ip=10.1.2.3::10.1.2.1:255.255.255.0", "ip=" + ip("10.1.2.3") + "::" + ip("10.1.2.1") + ":" + ip("255.255.255.0")},
		{"server 192.168.7.9 not responding", "server " + ip("192.168.7.9") + " not responding"},
		{"(10.0.0.1)", "(" + ip("10.0.0.1") + ")"},
		{"at 10.0.0.1.", "at " + ip("10.0.0.1") + "."},
		{"kernel 5.14.0-570.12.1.el9_6.x86_64", "kernel 5.14.0-570.12.1.el9_6.x86_64"},
		{"BIOS 2.4.4 03/11/2026", "BIOS 2.4.4 03/11/2026"},
		{"version 300.1.2.3", "version 300.1.2.3"},
		{"firmware 1.2.3.4.5", "firmware 1.2.3.4.5"},
		// IPv6: addresses, not times or PCI addresses or symbols.
		{"nameserver=2001:db8::53 ", "nameserver=" + a.word("ip6-", "2001:db8::53", 8) + " "},
		{"fe80::1%eth0", a.word("ip6-", "fe80::1", 8) + "%eth0"},
		{"at 03:14:07 on 0000:3b:00.0", "at 03:14:07 on 0000:3b:00.0"},
		{"std::string err_code:0x0101:0x0090", "std::string err_code:0x0101:0x0090"},
		// MAC addresses, also as a network boot passes them.
		{"BOOTIF=01-aa-bb-cc-dd-ee-ff", "BOOTIF=" + a.word("mac-", "01-aa-bb-cc-dd-ee-ff", 8)},
		{"ether 00:1B:44:11:3A:B7 up", "ether " + a.word("mac-", "00:1b:44:11:3a:b7", 8) + " up"},
		{"ifname=bootnet:00:1b:44:11:3a:b7", "ifname=bootnet:" + a.word("mac-", "00:1b:44:11:3a:b7", 8)},
		{"GUID 00:11:22:33:44:55:66:77", "GUID " + a.word("mac-", "00:11:22:33:44:55:66:77", 8)},
		{"uuid 6f1c0a8e-2b7d-4f0e-9a3b-5c7d9e1f2a3b and 2026-11-12 03:14:07", "uuid 6f1c0a8e-2b7d-4f0e-9a3b-5c7d9e1f2a3b and 2026-11-12 03:14:07"},
		// The name of a home directory in a path, and what is no such name.
		{"/home/alice/cases/dmesg.txt#L533", "/home/" + a.word("user-", "alice", 8) + "/cases/dmesg.txt#L533"},
		{`{"origin":"/Users/Bob Lee/x","root":"/export/home/alice"}`, `{"origin":"/Users/` + a.word("user-", "Bob", 8) + ` Lee/x","root":"/export/home/` + a.word("user-", "alice", 8) + `"}`},
		{"/Users/Shared/cases and /home/lost+found", "/Users/Shared/cases and /home/lost+found"},
		{"/home/" + a.word("user-", "alice", 8) + "/x is anonymized already", "/home/" + a.word("user-", "alice", 8) + "/x is anonymized already"},
		{"/var/lib/rasdaemon/ras-mc_event.db#mc_event/7 and /homework/a", "/var/lib/rasdaemon/ras-mc_event." + a.names["db"] + "#mc_event/7 and /homework/a"},
		{"/home/ and /home", "/home/ and /home"},
		// Identifiers derived from the host name.
		{`{"old":"s0123456789abcdef012"}`, `{"old":"` + a.word("s", "s0123456789abcdef012", 19) + `"}`},
		{"boot derived-0123456789abcdef.", "boot " + a.word("derived-", "0123456789abcdef", 16) + "."},
		{"d0123456789abcdef0123 stays", "d0123456789abcdef0123 stays"},
	} {
		if got := a.text(tt.in); got != tt.want {
			t.Errorf("text(%q)\n got  %q\n want %q", tt.in, got, tt.want)
		}
	}
	// The same input gives the same word; another key another word.
	b, _ := newAnonymizer([]byte("other"))
	if a.word("ip-", "10.0.0.1", 8) != ip("10.0.0.1") || b.word("ip-", "10.0.0.1", 8) == ip("10.0.0.1") {
		t.Error("words do not depend on the key alone")
	}
	if a.apply(derived, "") != "" || a.apply(bootID, "6f1c0a8e2b7d4f0e9a3b5c7d9e1f2a3b") != "6f1c0a8e2b7d4f0e9a3b5c7d9e1f2a3b" || a.apply(keep, "x") != "x" {
		t.Error("apply")
	}
}

// A kernel command line names the disks of the machine. Seen on the lab
// host: the anonymized bundle still held root=UUID=... of its root file
// system.
func TestCommandLineAnonymizing(t *testing.T) {
	a, err := newAnonymizer([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	a.host("r12-n034")
	a.learn()
	id := func(s string) string { return a.word("uuid-", s, 8) }
	for _, tt := range []struct{ in, want string }{
		{"", ""},
		{"ro quiet mce=print_all ras=cec_disable", "ro quiet mce=print_all ras=cec_disable"},
		{"BOOT_IMAGE=(hd0,gpt3)/boot/vmlinuz-7.3.0-ras root=UUID=6b1f0c2a-7d3e-4c5b-9a80-1e2f3a4b5c6d ro rootflags=subvol=root console=ttyS0,115200n8",
			"BOOT_IMAGE=(hd0,gpt3)/boot/vmlinuz-7.3.0-ras root=UUID=" + id("6b1f0c2a-7d3e-4c5b-9a80-1e2f3a4b5c6d") + " ro rootflags=subvol=root console=ttyS0,115200n8"},
		// Upper case names the same disk.
		{"resume=PARTUUID=6B1F0C2A-7D3E-4C5B-9A80-1E2F3A4B5C6D", "resume=PARTUUID=" + id("6b1f0c2a-7d3e-4c5b-9a80-1e2f3a4b5c6d")},
		{"rd.luks.uuid=luks-0f1e2d3c-4b5a-4978-8695-a4b3c2d1e0f9 rd.md.uuid=0a1b2c3d:4e5f6a7b:8c9d0e1f:2a3b4c5d",
			"rd.luks.uuid=luks-" + id("0f1e2d3c-4b5a-4978-8695-a4b3c2d1e0f9") + " rd.md.uuid=" + id("0a1b2c3d:4e5f6a7b:8c9d0e1f:2a3b4c5d")},
		{"systemd.machine_id=0a1b2c3d4e5f60718293a4b5c6d7e8f9 quiet", "systemd.machine_id=" + a.word("id-", "0a1b2c3d4e5f60718293a4b5c6d7e8f9", 8) + " quiet"},
		// What free text gets still applies.
		{"ip=10.1.2.3 hostname=r12-n034", "ip=" + a.word("ip-", "10.1.2.3", 8) + " hostname=" + a.names["r12-n034"]},
	} {
		if got := a.apply(commandLine, tt.in); got != tt.want {
			t.Errorf("command line %q\n got  %q\n want %q", tt.in, got, tt.want)
		}
	}
}

// TestEveryTextColumnIsClassified fails when the ledger gains a text
// column that the anonymizer does not know. Whoever adds the column says
// in anonymize.go what it holds: a host name, text that may name a host,
// or nothing of the kind.
func TestEveryTextColumnIsClassified(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	s, err := store.Open(ctx, path, store.Create)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	names, err := tableNames(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	inSchema := map[string]bool{}
	for _, table := range names {
		if _, ok := tables[table]; !ok {
			t.Errorf("table %s has no description for the manifest (bundle.go, tables)", table)
		}
		cols, err := textColumns(ctx, db, table)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cols {
			inSchema[table+"."+c] = true
			if _, ok := columns[table+"."+c]; !ok {
				t.Errorf("text column %s.%s is not classified for anonymizing (anonymize.go, columns)", table, c)
			}
		}
	}
	for col := range columns {
		if !inSchema[col] {
			t.Errorf("%s is classified and is no text column of the ledger", col)
		}
	}
	// A column nobody classified is treated as text that may name a host.
	if treatmentOf("event", "brand_new") != freeText {
		t.Error("an unknown column is not scrubbed")
	}
}

func FuzzText(f *testing.F) {
	for _, s := range []string{"", "web-7 kernel: 10.1.2.3 fe80::1 00:1B:44:11:3A:B7", "::", "a::b", "1.2.3.4.", "web-7.", "s0123456789abcdef012", strings.Repeat(":", 40)} {
		f.Add(s)
	}
	a, _ := newAnonymizer([]byte("k"))
	a.host("web-7.prod.example.org")
	a.learn()
	f.Fuzz(func(t *testing.T, s string) {
		out := a.text(s)
		if strings.Contains(strings.ToLower(out), "web-7.prod.example.org") {
			t.Errorf("the host name survived: %q -> %q", s, out)
		}
	})
}

func FuzzReadManifest(f *testing.F) {
	good, err := os.ReadFile(func() string {
		dir, _ := os.MkdirTemp("", "my-fuzz-")
		path := filepath.Join(dir, "seed.mycase")
		s, err := store.Open(ctx, filepath.Join(dir, "ledger.db"), store.Create)
		if err != nil {
			f.Fatal(err)
		}
		defer s.Close()
		if _, err := Write(ctx, s, path, Options{Now: now, Tool: "my fuzz"}); err != nil {
			f.Fatal(err)
		}
		f.Cleanup(func() { os.RemoveAll(dir) })
		return path
	}())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte{})
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(dir, "fuzz.mycase")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		// Whatever the file holds, reading it ends with a manifest or an
		// error, and opening it leaves nothing behind when it fails.
		if m, err := ReadManifest(path); err == nil && m.Format != Format {
			t.Errorf("a manifest of format %q was accepted", m.Format)
		}
		tmp := t.TempDir()
		c, err := Open(ctx, path, tmp)
		if err != nil {
			if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
				t.Errorf("a refused bundle left %d entries behind", len(entries))
			}
			return
		}
		c.Close()
	})
}
