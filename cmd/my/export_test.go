package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/fmfmlog"
	"github.com/agenticode/my/internal/testutil"
)

var fixedClock = map[string]string{"MY_NOW": "2026-11-13T09:30:00Z"}

func TestExportFMFM(t *testing.T) {
	ledger := demo(t)

	// The log goes to standard output, as JSON.
	code, out, errOut := runEnv(fixedClock, "open", ledger, "export", "fmfm")
	if code != cli.ExitOK || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	var doc fmfmlog.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if doc.Format != fmfmlog.Format || doc.HostID != "r12-n034" || doc.OutputDT != "2026-11-13T09:30:00.000Z" || doc.Analyzer.Name != "MemorY" ||
		len(doc.AnalysisLog) != 2 || len(doc.RuntimeLog) != 0 || doc.ActionDescriptors != "none" {
		t.Errorf("document: %+v", doc)
	}
	if a := doc.AnalysisLog[0]; a.DimmSerialNumber != "3B9C56" || a.FaultMode != "Row fault" || a.SeverityClassifier != "high" || a.Finding.Grade != "replace-now" {
		t.Errorf("first record: %+v", a)
	}
	testutil.Golden(t, "json/export-fmfm.json", []byte(out))

	// --json changes nothing: the log is JSON. "-o -" is standard output.
	for _, extra := range [][]string{{"--json"}, {"-o", "-"}} {
		_, again, _ := runEnv(fixedClock, append([]string{"open", ledger, "export", "fmfm"}, extra...)...)
		if again != out {
			t.Errorf("%v changed the log", extra)
		}
	}

	// With the runtime log: every error of the host once; with --all the
	// DIMMs without errors too.
	_, out, _ = runEnv(fixedClock, "open", ledger, "export", "fmfm", "--records", "--all")
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.RuntimeLog) != 1285 || len(doc.AnalysisLog) != 16 || doc.RuntimeLog[0].ErrorDT == "" || doc.RuntimeLog[0].Grade != "A" {
		t.Errorf("%d runtime records, %d analysis records", len(doc.RuntimeLog), len(doc.AnalysisLog))
	}

	// The sentences follow --lang.
	_, out, _ = runEnv(fixedClock, "open", ledger, "export", "fmfm", "--lang", "ko")
	if !strings.Contains(out, `"next_action": "DIMM 교체. 이 DIMM의 케이스 번들을 반품에 첨부."`) || !strings.Contains(out, `"FaultMode": "Row fault"`) {
		t.Errorf("--lang ko:\n%s", out)
	}
}

func TestExportFMFMToAFile(t *testing.T) {
	ledger := demo(t)
	file := filepath.Join(t.TempDir(), "analysis.json")

	code, out, errOut := runEnv(fixedClock, "open", ledger, "export", "fmfm", "-o", file, "--records")
	if code != cli.ExitOK || errOut != "" {
		t.Fatalf("exit code %d, stderr %q", code, errOut)
	}
	wantLines(t, out,
		"FMFM analysis log written: "+file+" (",
		"  host r12-n034, finding replace now: 2 DIMMs analysed, 1,285 error records in the runtime log",
		"  the log holds findings and suggestions for a person, and no action descriptor (CPAD)",
	)
	b, err := os.ReadFile(file)
	var doc fmfmlog.Document
	if err != nil || json.Unmarshal(b, &doc) != nil || len(doc.RuntimeLog) != 1285 {
		t.Fatalf("the file: %v, %d runtime records", err, len(doc.RuntimeLog))
	}

	// The file is there: it is not replaced unless asked.
	code, out, errOut = runEnv(fixedClock, "open", ledger, "export", "fmfm", "-o", file)
	if code != cli.ExitUsage || out != "" || !strings.Contains(errOut, "--force") {
		t.Errorf("an existing file: exit code %d, stdout %q, stderr %q", code, out, errOut)
	}
	code, out, _ = runEnv(fixedClock, "open", ledger, "export", "fmfm", "-o", file, "--force", "--json")
	var res struct {
		Path, Format, Host string
		Bytes, DIMMs       int
		Records            int
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || code != cli.ExitOK {
		t.Fatalf("--json: exit code %d, %v\n%s", code, err, out)
	}
	if res.Path != file || res.Format != fmfmlog.Format || res.Host != "r12-n034" || res.DIMMs != 2 || res.Records != 0 || res.Bytes < 1000 {
		t.Errorf("--json: %+v", res)
	}
	if fi, _ := os.Stat(file); fi == nil || int(fi.Size()) != res.Bytes {
		t.Errorf("the file has %v bytes, the result says %d", fi, res.Bytes)
	}

	// Korean.
	_, out, _ = runEnv(fixedClock, "open", ledger, "export", "fmfm", "-o", file, "--force", "--lang", "ko")
	wantLines(t, out, "FMFM 분석 로그 저장: ", "호스트 r12-n034, 소견 즉시 교체: 분석한 DIMM 2개, runtime 로그의 오류 기록 0건", "조치 기술자(CPAD)는 없음")
}

func TestExportCommandLine(t *testing.T) {
	ledger := demo(t)
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"export"}, "name what to write: my export fmfm, my export events, or my export labels"},
		{[]string{"export", "cper"}, `unknown format "cper"; this version writes fmfm (an FMFM analysis log), events (every record as a JSON line) and labels`},
		{[]string{"export", "fmfm", "--raw"}, "--raw goes with my export events"},
		{[]string{"export", "labels", "--since", "30d"}, "--since goes with my export events"},
		{[]string{"export", "events", "--since", "yesterday"}, "--since yesterday: want a date"},
		{[]string{"export", "fmfm", "more"}, `unexpected argument "more"`},
		{[]string{"export", "fmfm", "--audience", "fa"}, "unknown flag"},
	} {
		code, out, errOut := run(append([]string{"open", ledger}, tt.args...)...)
		if code != cli.ExitUsage || out != "" || !strings.Contains(errOut, tt.want) || !strings.Contains(errOut, `run "my help export" for usage`) {
			t.Errorf("%v: exit code %d, stdout %q, stderr %q", tt.args, code, out, errOut)
		}
	}
}

// TestExportEvents: the event stream of a case with records of the host
// and of its BMC, to standard output and to a file, and every field it
// writes is named in docs/data-model.md.
func TestExportEvents(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "fw.mycase")
	if code, _, errOut := runEnv(fixedClock, "import", filepath.Join(testutil.Testdata(t, "redfish"), "standard-log.json"), "-o", bundle); code != cli.ExitOK {
		t.Fatalf("import: exit code %d, %s", code, errOut)
	}
	page, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "data-model.md"))
	if err != nil {
		t.Fatal(err)
	}
	at := strings.Index(string(page), "## The event stream")
	if at < 0 {
		t.Fatal("docs/data-model.md has no section on the event stream")
	}
	section := string(page)[at:]

	keys := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, sub := range x {
				keys[k] = true
				walk(sub)
			}
		case []any:
			for _, sub := range x {
				walk(sub)
			}
		}
	}
	for _, ledger := range []string{bundle, demo(t)} {
		code, out, errOut := runEnv(fixedClock, "open", ledger, "export", "events", "--raw")
		if code != cli.ExitOK || errOut != "" {
			t.Fatalf("%s: exit code %d, stderr %q", ledger, code, errOut)
		}
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(lines) < 10 {
			t.Fatalf("%s: %d lines", ledger, len(lines))
		}
		for i, line := range lines {
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("%s line %d: %v", ledger, i+1, err)
			}
			if m["schema"] != "my-events/1" {
				t.Fatalf("%s line %d: schema %v", ledger, i+1, m["schema"])
			}
			walk(m)
		}
	}
	for k := range keys {
		if !strings.Contains(section, "`"+k+"`") {
			t.Errorf("docs/data-model.md does not name the field %q of the event stream", k)
		}
	}
	for _, k := range []string{"vendor", "mapping", "place", "dimm", "raw", "registry_version", "row"} {
		if !keys[k] {
			t.Errorf("no line has the field %q", k)
		}
	}

	// To a file: the lines, and what was written.
	file := filepath.Join(dir, "events.jsonl")
	code, out, errOut := runEnv(fixedClock, "open", bundle, "export", "events", "-o", file, "--since", "2026-11-10T12:00:00Z")
	if code != cli.ExitOK || errOut != "" {
		t.Fatalf("-o: exit code %d, stderr %q", code, errOut)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	n := strings.Count(string(b), "\n")
	wantLines(t, out, "event stream written: "+file+" (", fmt.Sprintf("  host localhost: %d records in the schema my-events/1", n))
	_, all, _ := runEnv(fixedClock, "open", bundle, "export", "events")
	if n == 0 || n >= strings.Count(all, "\n") {
		t.Errorf("--since kept %d of %d lines", n, strings.Count(all, "\n"))
	}
	if strings.Contains(string(b), `"raw":`) {
		t.Error("the raw text is in the stream without --raw")
	}

	// A scan of a host: --since cuts the stream and needs no fleet.
	code, out, errOut = runEnv(fixedClock, "export", "events", "--root", hostTree(t), "--since", "3650d")
	if code != cli.ExitOK || errOut != "" || !strings.HasPrefix(out, `{"schema":"my-events/1"`) {
		t.Errorf("a scan: exit code %d, stderr %q\n%.300s", code, errOut, out)
	}
}
