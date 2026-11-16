package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/testutil"
	"github.com/agenticode/my/internal/view"
)

// wide fails for a line that does not fit the default width.
func wide(t *testing.T, name, body string) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if term.Width(line) > term.DefaultWidth {
			t.Errorf("%s: line wider than %d columns: %q", name, term.DefaultWidth, line)
		}
	}
}

// The inventory of two fixture trees, where a BMC is all that was read,
// as text in both languages. The golden files are what a reader sees.
func TestInventoryScreens(t *testing.T) {
	for _, tree := range []string{"dell-shaped", "hpe-shaped"} {
		file := bmcCase(t, tree)
		for _, lang := range []string{"en", "ko"} {
			code, stdout, stderr := runEnv(fixed, "open", file, "inventory", "--all", "--lang", lang)
			if code != cli.ExitOK || stderr != "" {
				t.Fatalf("%s (%s): exit code %d, stderr %q", tree, lang, code, stderr)
			}
			// The first line names the case file, which lies in a
			// directory of this run.
			_, body, _ := strings.Cut(stdout, "\n")
			testutil.Golden(t, "inventory-"+tree+"."+lang+".txt", []byte(body))
			wide(t, tree+" ("+lang+")", body)
		}
	}
}

// The host and its BMC in one run: the table has the names of both, the
// corrected errors of both over the period both cover, and below it what
// can explain where they differ.
func TestInventoryOfHostAndBMC(t *testing.T) {
	root := hostTree(t)
	alone, _ := overview(t, fixed, "--root", root)
	srv := bmcOf(t, alone)
	for _, lang := range []string{"en", "ko"} {
		code, stdout, stderr := runEnv(account(), "inventory", "--root", root, "--bmc", srv.URL, "--insecure", "--lang", lang)
		if code != cli.ExitOK {
			t.Fatalf("%s: exit code %d\n%s\n%s", lang, code, stdout, stderr)
		}
		testutil.Golden(t, "inventory-host-bmc."+lang+".txt", []byte(stdout))
		wide(t, lang, stdout)
		noSecret(t, "", stdout, stderr)
	}

	code, stdout, stderr := runEnv(account(), "inventory", "--root", root, "--bmc", srv.URL, "--insecure", "--json")
	if code != cli.ExitOK {
		t.Fatalf("--json: exit code %d\n%s", code, stderr)
	}
	var e struct {
		View     string
		Host     string
		Grade    *string
		ExitCode int `json:"exit_code"`
		Data     view.Inventory
	}
	if err := json.Unmarshal([]byte(stdout), &e); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	d := e.Data
	if e.View != "inventory" || e.Host != "r12-n034" || e.Grade != nil || e.ExitCode != 0 || d.DIMMs != 16 || d.Empty != 16 || len(d.Rows) != 16 {
		t.Fatalf("envelope %+v, %d DIMMs, %d empty, %d rows", e, d.DIMMs, d.Empty, len(d.Rows))
	}
	s := d.Sides
	if s == nil || !s.Common || !s.OS.Read || !s.BMC.Read || s.From.Format("2006-01-02") != "2026-11-02" || s.To.Format("2006-01-02") != "2026-11-13" {
		t.Fatalf("sides = %+v", s)
	}
	rows := map[string]view.InventoryRow{}
	for _, r := range d.Rows {
		rows[r.Slot] = r
	}
	// The first DIMM of the ranking: errors on both sides, one entry of
	// the BMC at the time of an OS record, and names of three kinds.
	c1 := rows["CPU0_C1"]
	if !c1.CEOS.Valid || c1.CEOS.V == 0 || c1.CEBMC.V != 6 || len(c1.BMCNames) != 3 || !strings.HasPrefix(c1.EDACName, "CPU_SrcID#0_") ||
		c1.Org != "2Rx4" || c1.Serial != "3B9C56" || c1.BMCCount == nil || c1.BMCCount.CELifetime.V != 10 || len(c1.Events) != 2 {
		t.Errorf("CPU0_C1 = %+v", c1)
	}
	outcomes := map[string]view.SidesRow{}
	for _, r := range s.Rows {
		outcomes[r.Slot] = r
	}
	if r := outcomes["CPU0_C1"]; r.Outcome != view.SidesBoth || r.BMC != 6 || r.Matched != 1 || r.OS != c1.CEOS.V || r.Count == nil || r.Count.BMC != 1 || r.Count.OS == 1 {
		t.Errorf("sides of CPU0_C1 = %+v, count %+v", r, r.Count)
	}
	if r := outcomes["CPU1_D1"]; r.Outcome != view.SidesOSOnly || r.BMC != 0 || r.OS == 0 {
		t.Errorf("sides of CPU1_D1 = %+v", r)
	}
	// A DIMM without errors in the logs that the BMC counted since the
	// reset: the third module of the BMC, which counts 3.
	third := d.Rows[0]
	for _, r := range d.Rows {
		if r.BMCCount != nil && r.BMCCount.CECurrent.V == 3 {
			third = r
		}
	}
	if r := outcomes[third.Slot]; r.Outcome != view.SidesCounted || r.Count == nil || r.Count.BMC != 3 || r.Count.OS != 0 || r.Text.Code != "sides.count.differ" {
		t.Errorf("sides of %s = %+v", third.Slot, r)
	}
	if len(s.Rows) != 3 {
		t.Errorf("%d modules in the comparison, want 3", len(s.Rows))
	}
	kinds := map[string]int{}
	for _, g := range s.Candidates {
		kinds[g.Kind] = len(g.Items)
		if !strings.Contains(g.Head.Text, "Candidates for the reason, none established") {
			t.Errorf("candidates of %s are not called candidates: %q", g.Kind, g.Head.Text)
		}
	}
	if kinds[view.SidesOSOnly] == 0 || kinds["count"] == 0 || kinds[view.SidesBMCOnly] != 0 {
		t.Errorf("candidate groups = %v", kinds)
	}
}

// Without a BMC the table has the OS side alone and says how to read the
// other; the command opens no connection for it.
func TestInventoryOfHostAlone(t *testing.T) {
	root := hostTree(t)
	code, stdout, stderr := runEnv(fixed, "inventory", "--root", root, "--all")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	wantLines(t, stdout,
		"16 DIMMs in 32 slots",
		"slot names and module data from SMBIOS, EDAC",
		"no BIOS settings on record, so the RAS column is empty; my bios --bmc ADDRESS reads them",
		" CPU0_C1  3B9C56  Example Memory  EXM64G48R2X4-A  64 GB  2Rx4  4800/4800  1,231       -",
		"CE OS counts the corrected errors in the records of the operating system.\n",
		"No BMC was read: my inventory --bmc ADDRESS reads it in the same run.",
		"  empty")
	wide(t, "host alone", stdout)

	// A narrow terminal: the table gives up width of the maker and stays
	// inside it; ASCII only when asked.
	code, stdout, stderr = runEnv(fixed, "inventory", "--root", root, "--width", "76", "--ascii")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("--width 76: exit code %d, stderr %q", code, stderr)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if term.Width(line) > 76 {
			t.Errorf("line wider than 76 columns: %q", line)
		}
		for _, r := range line {
			if r > 127 {
				t.Errorf("--ascii: %q in %q", r, line)
			}
		}
	}
	wantLines(t, stdout,
		" SLOT     SERIAL  MAKER     PART NU..   SIZE  ORG   SPEED      CE OS  CE BMC",
		" CPU0_C1  3B9C56  Exampl..  EXM64G4..  64 GB  2Rx4  4800/4800  1,231       -")

	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"inventory", "--root", root, "extra"}, `unexpected argument "extra"`},
		{[]string{"inventory", "--root", root, "--user", "reader"}, "--user says how a BMC is reached"},
	} {
		if code, _, stderr := runEnv(fixed, tt.args...); code != cli.ExitUsage || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: exit code %d, stderr %q", tt.args, code, stderr)
		}
	}
	code, stdout, _ = runEnv(fixed, "inventory", "--help", "--lang", "ko")
	if code != cli.ExitOK || !strings.Contains(stdout, "my inventory [옵션]") || !strings.Contains(stdout, "--bmc ADDRESS") {
		t.Errorf("help: exit code %d\n%s", code, stdout)
	}
}

// The JSON of the inventory is an interface: its field names are pinned.
func TestInventoryAsJSON(t *testing.T) {
	file := bmcCase(t, "dell-shaped")
	code, stdout, stderr := runEnv(fixed, "open", file, "inventory", "--json")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	var e struct {
		View string
		Host string
		Data view.Inventory
	}
	if err := json.Unmarshal([]byte(stdout), &e); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	d := e.Data
	if e.View != "inventory" || e.Host != "node-d" || d.DIMMs != 4 || d.Slots != 4 || d.Machine != "Dell Inc. PowerEdge R740" || d.BMC == nil || d.BMC.Live ||
		d.RASCommon != "patrol scrub, ADDDC (width not known)" || d.Sides == nil || d.Sides.OS.Read || !d.Sides.BMC.Read || d.Sides.Common {
		t.Errorf("data: %+v", d)
	}
	// The service label of the BMC is a name of the slot, and the event
	// log, which writes it with an underscore, is tied to it.
	a1 := d.Rows[0]
	if a1.Slot != "A1" || strings.Join(a1.BMCNames, "|") != "DIMM A1|DIMM.Socket.A1" || a1.CEOS.Valid || a1.CEBMC.V != 2 || len(a1.Events) != 2 ||
		a1.BMCCount == nil || a1.BMCCount.CELifetime.V != 1250 || a1.BMCCount.CECurrent.V != 3 || a1.Settings[1].Effect != "unknown" {
		t.Errorf("A1: %+v", a1)
	}
	testutil.Golden(t, "json/inventory.json", []byte(stdout))

	// Every field of a firmware record is named in docs/data-model.md.
	page, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "data-model.md"))
	if err != nil {
		t.Fatal(err)
	}
	at := strings.Index(string(page), "### Firmware records in the views")
	if at < 0 {
		t.Fatal("docs/data-model.md has no section on firmware records in the views")
	}
	section := string(page)[at:]
	if end := strings.Index(section[4:], "\n#"); end > 0 {
		section = section[:end+4]
	}
	var raw struct {
		Data struct {
			Rows []map[string]any `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, row := range raw.Data.Rows {
		if _, ok := row["firmware_marks"]; ok && !strings.Contains(section, "`firmware_marks`") {
			t.Error("docs/data-model.md does not name the field firmware_marks")
		}
		list, _ := row["firmware"].([]any)
		for _, f := range list {
			for k := range f.(map[string]any) {
				seen++
				if !strings.Contains(section, "`"+k+"`") {
					t.Errorf("docs/data-model.md does not name the field %q of a firmware record", k)
				}
			}
		}
	}
	if seen == 0 {
		t.Error("no row of the inventory holds firmware records")
	}
}

// The report has the table of "my inventory" in its section on data
// quality: one line per DIMM with what the BMC says of it, and the
// comparison of the corrected errors of the two sides.
func TestReportHasTheInventory(t *testing.T) {
	root := hostTree(t)
	alone, _ := overview(t, fixed, "--root", root)
	srv := bmcOf(t, alone)
	dir := t.TempDir()
	for _, lang := range []string{"en", "ko"} {
		file := filepath.Join(dir, "report."+lang+".html")
		code, stdout, stderr := runEnv(account(), "report", "--root", root, "--bmc", srv.URL, "--insecure", "-o", file, "--lang", lang)
		if code != cli.ExitOK {
			t.Fatalf("%s: exit code %d\n%s\n%s", lang, code, stdout, stderr)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		html := string(raw)
		if dump := os.Getenv("MY_DUMP_REPORT"); dump != "" {
			if err := os.WriteFile(filepath.Join(dump, "inventory-report."+lang+".html"), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		noSecret(t, "", html, stdout, stderr)
		want := []string{`<table class="modules">`, "BMC: BMC.Slot.1, DIMM A1, DIMM1", `<a href="#dimm-CPU0_C1">CPU0_C1</a>`}
		if lang == "en" {
			want = append(want, "<h3>Modules</h3>", "<h4>Operating system and BMC</h4>",
				"CPU0_C1: the OS has 604 corrected errors, the logs of the BMC 6 entries", "Candidates for the reason, none established")
		} else {
			want = append(want, "<h3>모듈</h3>", "<h4>운영체제와 BMC</h4>")
		}
		for _, w := range want {
			if !strings.Contains(html, w) {
				t.Errorf("%s: the report lacks %q", lang, w)
			}
		}
		// A report has no command line to run: the texts of the table that
		// name a flag are replaced.
		for _, w := range []string{"--all lists them", "my bios --bmc", "my inventory --bmc", "--all로 표시"} {
			if strings.Contains(html, w) {
				t.Errorf("%s: the report says %q", lang, w)
			}
		}
	}
}
