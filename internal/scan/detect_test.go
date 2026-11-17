package scan

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testutil"
)

// kinds returns the adapter names of a detection.
func kinds(as []adapter.Adapter) string {
	var out []string
	for _, a := range as {
		out = append(out, a.Kind())
	}
	return strings.Join(out, ",")
}

func gz(t *testing.T, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestDetectFixtures(t *testing.T) {
	for _, c := range []struct {
		path []string
		kind string
		what string
	}{
		{[]string{"klog", "intel-adxl.dmesg"}, "klog", WhatKernelLog},
		{[]string{"klog", "ghes-6.x.dmesg"}, "klog", WhatKernelLog},
		{[]string{"klog", "ghes-4.18.dmesg-T"}, "klog", WhatKernelLog},
		{[]string{"klog", "intel-skx.messages"}, "klog", WhatKernelLog},
		{[]string{"klog", "kmsg.txt"}, "klog", WhatKernelLog},
		{[]string{"klog", "amd.journal.json"}, "klog", WhatJournal},
		{[]string{"smbios", "dmidecode-t17-ddr4.txt"}, "smbios", WhatDmidecode},
		{[]string{"smbios", "dmidecode-full-vm.txt"}, "smbios", WhatDmidecode},
		{[]string{"sel", "ipmitool-elist.txt"}, "sel", WhatSEL},
		{[]string{"sel", "ipmitool-list.txt"}, "sel", WhatSEL},
		{[]string{"sel", "ipmitool-list-v.txt"}, "sel", WhatSEL},
		{[]string{"sel", "ipmitool-elist.csv"}, "sel", WhatSEL},
		{[]string{"sel", "ipmitool-elist-supermicro.txt"}, "sel", WhatSEL},
		{[]string{"sel", "ipmitool-elist-zone.txt"}, "sel", WhatSEL},
		{[]string{"sel", "ipmi-sel.txt"}, "sel", WhatSEL},
		{[]string{"sel", "sel.json"}, "sel", WhatSEL},
		{[]string{"nvidia", "nvidia-smi-q.txt"}, "nvidia-smi", WhatNvidiaSMI},
		{[]string{"nvidia", "nvidia-smi-q.xml"}, "nvidia-smi", WhatNvidiaSMI},
		{[]string{"nvidia", "nvidia-smi-q-legacy.txt"}, "nvidia-smi", WhatNvidiaSMI},
		{[]string{"nvidia", "query-gpu-h200.csv"}, "nvidia-smi", WhatNvidiaCSV},
		{[]string{"nvidia", "query-gpu-h200-mig.csv"}, "nvidia-smi", WhatNvidiaCSV},
		{[]string{"nvidia", "query-gpu-rtx2080-470.csv"}, "nvidia-smi", WhatNvidiaCSV},
		{[]string{"nvidia", "query-remapped-rows.csv"}, "nvidia-smi", WhatNvidiaCSV},
		{[]string{"nvidia", "query-retired-pages-h200.csv"}, "nvidia-smi", WhatNvidiaCSV},
		{[]string{"klog", "nvidia-xid.dmesg"}, "klog", WhatKernelLog},
		{[]string{"klog", "nvidia-xid.messages"}, "klog", WhatKernelLog},
		{[]string{"mcelog", "client-paper.txt"}, "mcelog", WhatMcelog},
		{[]string{"mcelog", "client-x3850.txt"}, "mcelog", WhatMcelog},
		{[]string{"fmfm", "runtime.json"}, "fmfm", WhatFMFM},
		{[]string{"fmfm", "boot.json"}, "fmfm", WhatFMFM},
		{[]string{"fmfm", "crash.json"}, "fmfm", WhatFMFM},
		{[]string{"fmfm", "combined.jsonl"}, "fmfm", WhatFMFM},
		{[]string{"redfish", "sel-log.json"}, "redfish", WhatRedfish},
		{[]string{"redfish", "registry-log.json"}, "redfish", WhatRedfish},
		{[]string{"redfish", "vendor-text-log.json"}, "redfish", WhatRedfish},
		{[]string{"redfish", "memory-expanded.json"}, "redfish", WhatRedfish},
		{[]string{"redfish", "bmc-tree"}, "redfish", WhatRedfish},
		{[]string{"redfish", "bmc-tree", "redfish", "v1", "Systems", "system", "Memory", "dimm1", "index.json"}, "redfish", WhatRedfish},
	} {
		path := testutil.Testdata(t, c.path...)
		as, what, err := Detect(ctx, path, DetectOptions{})
		if err != nil || kinds(as) != c.kind || what != c.what {
			t.Errorf("%s: %s, %q, %v; want %s, %q", filepath.Join(c.path...), kinds(as), what, err, c.kind, c.what)
			continue
		}
		if as[0].Origin() != path {
			t.Errorf("%s: origin %q", filepath.Join(c.path...), as[0].Origin())
		}
	}
}

// Every fixture of every adapter must be recognised: a new input kind that
// Detect does not know fails here.
func TestDetectKnowsEveryFixture(t *testing.T) {
	root := testutil.Testdata(t)
	// Fixtures that are parts of an input, not an input: files of a
	// sysfs tree, raw tables, scripts that build a database, the
	// hexadecimal form of a binary dump, the notes and licences that lie
	// next to fixtures, and the inputs and answers of the Memory Explorer
	// beside its dmidecode dumps; of the SPD dumps the DDR3 dump, whose
	// memory type is not decoded, and the files of the demo case and of the
	// lab guests, copies of a host's files.
	skip := func(rel string) bool {
		return rel == "spd/rigred/ddr3-sodimm.bin" || strings.HasPrefix(rel, "spd/demo/") || strings.HasPrefix(rel, "spd/lab/") || strings.HasSuffix(rel, "LICENSE") || strings.HasPrefix(rel, "edac-sysfs/") || strings.HasPrefix(rel, "smbios/tables-") || strings.HasSuffix(rel, ".sql") ||
			strings.HasSuffix(rel, ".hex") || strings.HasSuffix(rel, ".cperhex") || strings.HasPrefix(rel, "redfish/bmc-tree/") || strings.HasPrefix(rel, "real/") ||
			strings.HasPrefix(rel, "dcbrain/") || strings.HasSuffix(rel, "/README.md") || strings.HasSuffix(rel, "/LICENSE.md") ||
			strings.HasPrefix(rel, "explorer/") && !strings.HasSuffix(rel, "/dmidecode.txt")
	}
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if skip(rel) {
			return nil
		}
		n++
		if _, _, err := Detect(ctx, p, DetectOptions{}); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
		return nil
	})
	if err != nil || n < 20 {
		t.Fatalf("walked %d fixtures, %v", n, err)
	}
}

func TestDetectDatabases(t *testing.T) {
	db := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", "intel-skx-0.6.sql"), "ras-mc_event.db")
	as, what, err := Detect(ctx, db, DetectOptions{})
	if err != nil || kinds(as) != "rasdaemon" || what != WhatRasdaemon {
		t.Fatalf("rasdaemon database: %s, %q, %v", kinds(as), what, err)
	}
	var c adapter.Collected
	// The fixture holds rows that cannot be used, on purpose.
	if rep := as[0].Collect(ctx, &c); rep.Status != model.SourcePartial || len(c.Events) == 0 {
		t.Errorf("reading the detected database: %+v, %d events", rep, len(c.Events))
	}

	// A ledger is the result of an import, not an input.
	ledgerPath := filepath.Join(t.TempDir(), "ledger.db")
	s, err := store.Open(ctx, ledgerPath, store.Create)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, _, err := Detect(ctx, ledgerPath, DetectOptions{}); !errors.Is(err, ErrUnknownInput) || !strings.Contains(err.Error(), "MemorY ledger") {
		t.Errorf("ledger: %v", err)
	}

	// A compressed database is not read through.
	raw, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	zipped := filepath.Join(t.TempDir(), "ras-mc_event.db.gz")
	if err := os.WriteFile(zipped, gz(t, raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Detect(ctx, zipped, DetectOptions{}); !errors.Is(err, ErrUnknownInput) || !strings.Contains(err.Error(), "unpack it first") {
		t.Errorf("compressed database: %v", err)
	}
}

func TestDetectHostTree(t *testing.T) {
	root, _ := tree(t, synth.FlavorIntel)
	as, what, err := Detect(ctx, root, DetectOptions{Now: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)})
	if err != nil || what != WhatHostTree || kinds(as) != "hostinfo,rasdaemon,klog,edac-sysfs,smbios" {
		t.Fatalf("host tree: %s, %q, %v", kinds(as), what, err)
	}
	// A directory that is neither.
	empty := t.TempDir()
	if _, _, err := Detect(ctx, empty, DetectOptions{}); !errors.Is(err, ErrUnknownInput) || !strings.Contains(err.Error(), "neither a copy of a host's files") {
		t.Errorf("empty directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(empty, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Detect(ctx, empty, DetectOptions{}); !errors.Is(err, ErrUnknownInput) {
		t.Errorf("directory with a text file: %v", err)
	}
}

// yield collects what an adapter yields, without the parts that name the
// file it was read from.
func yield(t *testing.T, a adapter.Adapter) (events, actions, modules int, first string, rep adapter.Report) {
	t.Helper()
	var c adapter.Collected
	rep = a.Collect(ctx, &c)
	if len(c.Events) > 0 {
		e := c.Events[0]
		first = e.ErrorDT.UTC().Format(time.RFC3339) + " " + string(e.ErrorSeverity) + " " + e.Label + " " + string(e.TimeQuality)
	}
	if len(c.GPUs) > 0 {
		g := c.GPUs[0]
		first += " " + g.ObservedAt.UTC().Format(time.RFC3339) + " " + g.GPUSerial + " " + g.Fingerprint
	}
	// GPU snapshots are counted with the modules: both are inventory.
	return len(c.Events), len(c.Actions), len(c.Modules) + len(c.GPUs), first, rep
}

// A file compressed with gzip gives the records of the file itself.
func TestDetectReadsThroughGzip(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		path []string
		what string
	}{
		{[]string{"klog", "intel-skx.messages"}, WhatKernelLog},
		{[]string{"klog", "amd.journal.json"}, WhatJournal},
		{[]string{"smbios", "dmidecode-t17-ddr4.txt"}, WhatDmidecode},
		{[]string{"sel", "ipmitool-elist.txt"}, WhatSEL},
		{[]string{"sel", "sel.json"}, WhatSEL},
		{[]string{"redfish", "registry-log.json"}, WhatRedfish},
		{[]string{"nvidia", "nvidia-smi-q.xml"}, WhatNvidiaSMI},
		{[]string{"nvidia", "nvidia-smi-q.txt"}, WhatNvidiaSMI},
		{[]string{"fmfm", "combined.jsonl"}, WhatFMFM},
	} {
		plain := testutil.Testdata(t, c.path...)
		zipped := filepath.Join(dir, c.path[len(c.path)-1]+".gz")
		if err := os.WriteFile(zipped, gz(t, testutil.ReadTestdata(t, c.path...)), 0o600); err != nil {
			t.Fatal(err)
		}
		opts := DetectOptions{Now: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)}
		pa, pw, err1 := Detect(ctx, plain, opts)
		za, zw, err2 := Detect(ctx, zipped, opts)
		if err1 != nil || err2 != nil || pw != c.what || zw != c.what {
			t.Errorf("%s: %q %v, compressed %q %v", c.path[1], pw, err1, zw, err2)
			continue
		}
		pe, pact, pm, pf, prep := yield(t, pa[0])
		ze, zact, zm, zf, zrep := yield(t, za[0])
		if pe != ze || pact != zact || pm != zm || pf != zf || prep.Status != zrep.Status || prep.Records != zrep.Records {
			t.Errorf("%s: plain %d/%d/%d %q %s, compressed %d/%d/%d %q %s", c.path[1], pe, pact, pm, pf, prep.Status, ze, zact, zm, zf, zrep.Status)
		}
		if pe+pact+pm == 0 {
			t.Errorf("%s: nothing was read", c.path[1])
		}
		if za[0].Origin() != zipped {
			t.Errorf("origin of the compressed file = %q", za[0].Origin())
		}
	}

	// A compressed file that is cut off is reported by the adapter.
	data := gz(t, testutil.ReadTestdata(t, "klog", "intel-skx.messages"))
	cut := filepath.Join(dir, "cut.gz")
	if err := os.WriteFile(cut, data[:len(data)-12], 0o600); err != nil {
		t.Fatal(err)
	}
	as, _, err := Detect(ctx, cut, DetectOptions{})
	if err != nil {
		t.Fatalf("cut file: %v", err)
	}
	if _, _, _, _, rep := yield(t, as[0]); rep.Status != model.SourceError {
		t.Errorf("cut file: status %s, %q", rep.Status, rep.Detail)
	}

	// A compressed file that ends right behind its header holds nothing to
	// recognise. The reason names the compression: before, it was the
	// bare "unexpected EOF" of the decompressor. Found by FuzzDetect.
	header := filepath.Join(dir, "header.gz")
	if err := os.WriteFile(header, data[:10], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Detect(ctx, header, DetectOptions{}); err == nil ||
		err.Error() != header+": gzip: the compressed data is damaged or cut off: unexpected EOF" {
		t.Errorf("file that ends behind the gzip header: %v", err)
	}
}

func TestDetectBinaryEventLog(t *testing.T) {
	var raw []byte
	for _, line := range strings.Split(string(testutil.ReadTestdata(t, "sel", "sel-writeraw.hex")), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		b, err := hex.DecodeString(strings.ReplaceAll(line, " ", ""))
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, b...)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{"sel.bin": raw, "sel.bin.gz": gz(t, raw)} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		as, what, err := Detect(ctx, p, DetectOptions{})
		if err != nil || what != WhatSEL || kinds(as) != "sel" {
			t.Errorf("%s: %s, %q, %v", name, kinds(as), what, err)
			continue
		}
		// Record 3, a disable that record 9 takes back, is an action.
		if e, _, _, _, rep := yield(t, as[0]); e != 4 || rep.Records != 9 {
			t.Errorf("%s: %d events of %d records", name, e, rep.Records)
		}
	}
}

func TestDetectOptionsReachTheAdapters(t *testing.T) {
	dir := t.TempDir()
	saved := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	put := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, saved, saved); err != nil {
			t.Fatal(err)
		}
		return p
	}
	collect := func(p string, opts DetectOptions) *adapter.Collected {
		as, _, err := Detect(ctx, p, opts)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		var c adapter.Collected
		as[0].Collect(ctx, &c)
		return &c
	}

	// dmidecode output has no time: the modules are seen when the file
	// was saved, or when the caller says.
	dmi := put("dmidecode.txt", testutil.ReadTestdata(t, "smbios", "dmidecode-t17-ddr4.txt"))
	if c := collect(dmi, DetectOptions{ModTime: true}); len(c.Modules) == 0 || !c.Modules[0].ObservedAt.Equal(saved) {
		t.Errorf("dmidecode with ModTime: %d modules, seen %v", len(c.Modules), c.Modules[0].ObservedAt)
	}
	told := saved.Add(48 * time.Hour)
	if c := collect(dmi, DetectOptions{ModTime: true, Now: told}); !c.Modules[0].ObservedAt.Equal(told) {
		t.Errorf("dmidecode with Now: seen %v", c.Modules[0].ObservedAt)
	}

	// A dmesg capture has seconds since boot only.
	dmesg := put("dmesg.txt", testutil.ReadTestdata(t, "klog", "ghes-6.x.dmesg"))
	rel := collect(dmesg, DetectOptions{})
	est := collect(dmesg, DetectOptions{ModTime: true})
	if len(rel.Events) == 0 || rel.Events[0].TimeQuality != model.TimeRelative {
		t.Errorf("dmesg without a capture time: %d events, %q", len(rel.Events), rel.Events[0].TimeQuality)
	}
	if len(est.Events) != len(rel.Events) || est.Events[0].TimeQuality != model.TimeEstimated || est.Events[0].ErrorDT.After(saved) {
		t.Errorf("dmesg with ModTime: %q at %v", est.Events[0].TimeQuality, est.Events[0].ErrorDT)
	}
	capt := collect(dmesg, DetectOptions{CapturedAt: told})
	if !capt.Events[0].ErrorDT.Equal(est.Events[0].ErrorDT.Add(48 * time.Hour)) {
		t.Errorf("dmesg with CapturedAt: %v, with ModTime %v", capt.Events[0].ErrorDT, est.Events[0].ErrorDT)
	}

	// A zone for times that carry none.
	kst := time.FixedZone("KST", 9*3600)
	log := put("sel.txt", testutil.ReadTestdata(t, "sel", "ipmitool-elist-supermicro.txt"))
	utc, local := collect(log, DetectOptions{}), collect(log, DetectOptions{Location: kst})
	if d := utc.Events[0].ErrorDT.Sub(local.Events[0].ErrorDT); d != 9*time.Hour {
		t.Errorf("SEL times differ by %v with the zone option, want 9h", d)
	}
	msgs := put("messages", testutil.ReadTestdata(t, "klog", "intel-skx.messages"))
	utc, local = collect(msgs, DetectOptions{CapturedAt: told}), collect(msgs, DetectOptions{CapturedAt: told, Location: kst})
	if len(utc.Events) == 0 || utc.Events[0].ErrorDT.Sub(local.Events[0].ErrorDT) != 9*time.Hour {
		t.Errorf("syslog times with the zone option: %v and %v", utc.Events[0].ErrorDT, local.Events[0].ErrorDT)
	}

	// The memory resources of a Redfish dump are seen when it was saved.
	rf := put("memory.json", testutil.ReadTestdata(t, "redfish", "memory-expanded.json"))
	if c := collect(rf, DetectOptions{ModTime: true}); len(c.Modules) != 3 || !c.Modules[0].ObservedAt.Equal(saved) {
		t.Errorf("Redfish with ModTime: seen %v", c.Modules[0].ObservedAt)
	}

	// An FMFM log with the records of two hosts: the host that is asked
	// for is read, and the first one the log names otherwise. Times
	// written without a zone follow the zone option.
	fm := put("runtime.json", testutil.ReadTestdata(t, "fmfm", "runtime.json"))
	first, other := collect(fm, DetectOptions{}), collect(fm, DetectOptions{Host: "r12-n035"})
	if len(first.Events) != 6 || len(other.Events) != 1 || other.Events[0].ErrorDT.Equal(first.Events[0].ErrorDT) {
		t.Errorf("FMFM log: %d events without a host, %d for the other host", len(first.Events), len(other.Events))
	}
	if d := first.Events[0].ErrorDT.Sub(collect(fm, DetectOptions{Location: kst}).Events[0].ErrorDT); d != 9*time.Hour {
		t.Errorf("FMFM times differ by %v with the zone option, want 9h", d)
	}
}

func TestDetectUnknownInputs(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct {
		content string
		want    string
	}{
		"empty":      {"", "the file is empty"},
		"blank":      {"\n \n", "the file is empty"},
		"prose":      {"Dear support,\nour server shows memory errors.\n", `text of no known kind (it starts "Dear support,")`},
		"json":       {`{"hello": "world"}`, "JSON that is neither a journal export, a Redfish resource, an FMFM log nor a list of event log entries"},
		"zip":        {"PK\x03\x04\x14\x00\x00\x00", "a zip archive"},
		"binary":     {"\x00\x01\x02\x03\x04\x05", "binary data of no known kind"},
		"sel-save":   {"0x04 0x0c 0x53 0x6f 0xa0 0xff 0x01 # Memory #0x53 Correctable ECC\n", "text of no known kind"},
		"csv":        {"host,slot,count\nr12-n034,DIMM_A1,3\n", "text of no known kind"},
		"html":       {"<html><body>System Event Log</body></html>", "text of no known kind"},
		"lspci":      {"00:00.0 Host bridge: Example Corp Device 0998\n", "text of no known kind"},
		"edac-count": {"17\n", "text of no known kind"},
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(c.content), 0o600); err != nil {
			t.Fatal(err)
		}
		as, what, err := Detect(ctx, p, DetectOptions{})
		if !errors.Is(err, ErrUnknownInput) || !strings.Contains(err.Error(), c.want) || as != nil || what != "" {
			t.Errorf("%s: %s, %q, %v; want an error with %q", name, kinds(as), what, err, c.want)
		}
		if err != nil && !strings.HasPrefix(err.Error(), p+": ") {
			t.Errorf("%s: the error does not start with the path: %v", name, err)
		}
	}
	if _, _, err := Detect(ctx, filepath.Join(dir, "missing"), DetectOptions{}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing path: %v", err)
	}
}

func TestLooksLikeKernelLog(t *testing.T) {
	for line, want := range map[string]bool{
		"[    0.000000] Linux version 6.8.0":                                                 true,
		"<6>[   12.345678] EDAC MC0: 1 CE memory read error":                                 true,
		"6,1234,5678901,-;EDAC MC0: 1 CE":                                                    true,
		"[Thu Nov 12 03:14:07 2026] EDAC MC0: 1 CE":                                          true,
		"Nov 12 03:14:07 r12-n034 kernel: [ 1234.567890] EDAC MC0: 1 CE":                     true,
		"Nov  2 03:14:07 r12-n034 sshd[4211]: Accepted publickey":                            true,
		"2026-11-12T03:14:07.123456+09:00 r12-n034 kernel: EDAC MC0: 1 CE":                   true,
		"2026-11-12T03:14:07,123456+09:00 EDAC MC0: 1 CE memory read error":                  true,
		"   1 | 11/12/2026 | 03:14:07 | Memory DIMM_A1 | Correctable ECC | Asserted":         false,
		"Handle 0x1100, DMI type 17, 92 bytes":                                               false,
		"GPU 00000000:07:00.0":                                                               false,
		"[1, 2, 3]":                                                                          false,
		"Timestamp                                 : Thu Nov 12 03:14:07 2026":               false,
		"The log says [   12.345678] somewhere in the middle":                                false,
		"1,11/12/2026,03:14:07,Memory DIMM_C1,Correctable ECC,Asserted":                      false,
		"SEL Record ID          : 0001":                                                      false,
		"ID  | Date        | Time     | Name             | Type                     | Event": false,
	} {
		if got := looksLikeKernelLog([]byte(line + "\n")); got != want {
			t.Errorf("%q: %v, want %v", line, got, want)
		}
	}
}

// The adapter Detect returns gives the same records as the adapter built
// by hand.
func TestDetectedAdapterEqualsDirectOne(t *testing.T) {
	path := testutil.Testdata(t, "redfish", "bmc-tree")
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	as, _, err := Detect(ctx, path, DetectOptions{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	var a, b adapter.Collected
	as[0].Collect(ctx, &a)
	redfish.New(path, redfish.Options{Now: now}).Collect(ctx, &b)
	if !reflect.DeepEqual(a, b) || len(a.Modules) != 4 {
		t.Errorf("detected and direct adapter differ: %d and %d modules", len(a.Modules), len(b.Modules))
	}
}
