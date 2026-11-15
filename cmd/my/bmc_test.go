package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/redfish/bmctest"
)

// overviewDoc is what the tests read of "my --json".
type overviewDoc struct {
	Host string `json:"host"`
	Data struct {
		Ranking []struct {
			Slot string `json:"slot"`
			DIMM struct {
				Serial       string `json:"serial"`
				Manufacturer string `json:"manufacturer"`
			} `json:"dimm"`
			CE int64 `json:"ce"`
		} `json:"ranking"`
		Sources []struct {
			Kind   string   `json:"kind"`
			Origin string   `json:"origin"`
			Status string   `json:"status"`
			Notes  []string `json:"notes"`
		} `json:"sources"`
	} `json:"data"`
}

func overview(t *testing.T, env map[string]string, args ...string) (overviewDoc, string) {
	t.Helper()
	code, out, errOut := runEnv(env, append(args, "--json", "--all")...)
	var doc overviewDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("exit code %d, not JSON: %v\n%s\n%s", code, err, out, errOut)
	}
	return doc, errOut
}

// bmcOf returns a stand-in for the BMC of the demo host: it lists the DIMMs
// of the host by their serial numbers, written in lower case with "0x" in
// front, under slot names of its own, and names another maker. Its event
// log has six corrected errors of "DIMM_A1", which is how it writes the
// service label "DIMM A1" of the first module.
func bmcOf(t *testing.T, host overviewDoc) *bmctest.Server {
	t.Helper()
	n := len(host.Data.Ranking)
	overlay := bmctest.Machine(n, 6, false)
	for i, d := range host.Data.Ranking {
		p := fmt.Sprintf("/redfish/v1/Systems/1/Memory/DIMM%d", i+1)
		overlay[p] = fmt.Sprintf(`{"@odata.id":%q,"@odata.type":"#Memory.v1_17_0.Memory","Id":"DIMM%d","Name":"DIMM %d","DeviceLocator":"BMC.Slot.%d",`+
			`"Location":{"PartLocation":{"ServiceLabel":"DIMM A%d","LocationType":"Slot"}},"CapacityMiB":65536,"Manufacturer":"Another Name Ltd.",`+
			`"SerialNumber":"0x%s","RankCount":2,"Status":{"State":"Enabled","Health":"OK"},"Metrics":{"@odata.id":"%s/MemoryMetrics"}}`,
			p, i+1, i+1, i+1, i+1, strings.ToLower(d.DIMM.Serial), p)
		if i >= 3 {
			// The BMC has counted nothing for most modules.
			overlay[p+"/MemoryMetrics"] = fmt.Sprintf(`{"@odata.id":"%s/MemoryMetrics","@odata.type":"#MemoryMetrics.v1_7_0.MemoryMetrics","Id":"MemoryMetrics",`+
				`"CurrentPeriod":{"CorrectableECCErrorCount":0,"UncorrectableECCErrorCount":0},"LifeTime":{"CorrectableECCErrorCount":0,"UncorrectableECCErrorCount":0}}`, p)
		}
	}
	// The system says when it was last reset, which is when the current
	// counts of the BMC began.
	overlay["/redfish/v1/Systems/1"] = strings.Replace(overlay["/redfish/v1/Systems/1"], `"HostName":"node-a",`, `"HostName":"node-a","LastResetTime":"2026-11-10T00:00:00Z",`, 1)
	return bmctest.New(t, fstest.MapFS{}, bmctest.Options{Overlay: overlay, User: bmcUser, Password: bmcPassword})
}

// A scan with --bmc reads the host and its BMC in one run. The slots and
// the DIMMs are those of the host, the BMC adds its event log and the BIOS
// settings, and nothing is written to the BMC.
func TestScanWithBMC(t *testing.T) {
	root := hostTree(t)
	alone, _ := overview(t, fixed, "--root", root)
	if len(alone.Data.Ranking) != 16 {
		t.Fatalf("%d DIMMs on the host", len(alone.Data.Ranking))
	}
	srv := bmcOf(t, alone)
	bmcHost := strings.TrimPrefix(srv.URL, "http://")

	both, stderr := overview(t, account(), "--root", root, "--bmc", srv.URL, "--insecure")
	// The same slots and the same DIMMs, with the words of the host.
	if len(both.Data.Ranking) != 16 {
		t.Errorf("%d DIMMs with the BMC, want the 16 of the host", len(both.Data.Ranking))
	}
	want := map[string]string{}
	for _, d := range alone.Data.Ranking {
		want[d.Slot] = d.DIMM.Serial + " " + d.DIMM.Manufacturer
	}
	first := alone.Data.Ranking[0]
	for _, d := range both.Data.Ranking {
		if want[d.Slot] != d.DIMM.Serial+" "+d.DIMM.Manufacturer {
			t.Errorf("slot %s holds %q %q, the host alone says %q", d.Slot, d.DIMM.Serial, d.DIMM.Manufacturer, want[d.Slot])
		}
		// Eight entries of two logs of the BMC name the first module. Two
		// of them stand in both logs, which leaves six errors, and one of
		// those is an error rasdaemon recorded in the same minutes.
		if d.Slot == first.Slot && d.CE != first.CE+5 {
			t.Errorf("%s: CE %d, want the %d of the host and 5 more from the logs of the BMC", d.Slot, d.CE, first.CE)
		}
	}
	var redfish, slotMap []string
	for _, src := range both.Data.Sources {
		switch src.Kind {
		case "redfish":
			redfish = append(redfish, src.Origin+" "+src.Status)
		case "slot-map":
			slotMap = src.Notes
		}
	}
	if len(redfish) != 1 || redfish[0] != "bmc:127.0.0.1 ok" {
		t.Errorf("redfish sources = %q", redfish)
	}
	wantLines(t, strings.Join(slotMap, "\n"),
		"16 DIMMs that a second inventory lists are DIMMs SMBIOS describes, by serial number",
		"16 slots have another name in a second inventory")
	// What was read is said on the error stream, with the warning.
	wantLines(t, stderr,
		"my: warning: --insecure",
		bmcHost+": ",
		" resources read in 0s (",
		"  Example EX-2000, BIOS 1.4.2, BMC firmware 2.14.0, Redfish 1.17.0")
	if w := srv.Writes(); len(w) != 0 {
		t.Errorf("requests that write: %v", w)
	}
	noSecret(t, "", stderr)

	// The settings check of the same run has the settings of the BMC.
	// The machine keeps the name the host gives it.
	code, stdout, stderr := runEnv(account(), "bios", "--root", root, "--bmc", srv.URL, "--insecure")
	if code != cli.ExitOK {
		t.Errorf("my bios --bmc: exit code %d\n%s\n%s", code, stdout, stderr)
	}
	wantLines(t, stdout,
		"host r12-n034  Example Systems R760  BIOS 1.4.2  BMC 2.14.0",
		"1 memory setting of the BIOS on record, read 2026-11-16 03:00 UTC from the BMC at 127.0.0.1",
		"PatrolScrub = Enabled")
	noSecret(t, "", stdout, stderr)

	// A bundle of the host carries what the BMC said.
	file := filepath.Join(t.TempDir(), "case.mycase")
	if code, stdout, stderr = runEnv(account(), "bundle", "-o", file, "--root", root, "--bmc", srv.URL, "--insecure"); code != cli.ExitOK {
		t.Fatalf("my bundle --bmc: exit code %d\n%s\n%s", code, stdout, stderr)
	}
	code, stdout, stderr = runEnv(fixed, "open", file, "bios")
	if code != cli.ExitOK || !strings.Contains(stdout, "PatrolScrub = Enabled") {
		t.Errorf("my open case bios: exit code %d\n%s\n%s", code, stdout, stderr)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), bmcPassword) {
		t.Error("the bundle holds the password")
	}
	// The directory the answers passed through is gone.
	if left, _ := filepath.Glob(filepath.Join(os.TempDir(), "my-scan-*", "bmc")); len(left) != 0 {
		t.Errorf("collections left behind: %v", left)
	}
}

// The BMC of another machine is not added to the host: the command ends
// and says why, and a ledger that is kept holds the host alone.
func TestScanWithTheBMCOfAnotherMachine(t *testing.T) {
	root := hostTree(t)
	// The DIMMs of this stand-in have serial numbers the host does not have.
	srv := bmctest.New(t, fstest.MapFS{}, bmctest.Options{Overlay: bmctest.Machine(4, 6, false), User: bmcUser, Password: bmcPassword})
	ledger := filepath.Join(t.TempDir(), "ledger.db")
	code, stdout, stderr := runEnv(account(), "--root", root, "--ledger", ledger, "--bmc", srv.URL, "--insecure")
	if code != cli.ExitFailure || stdout != "" {
		t.Fatalf("exit code %d\n%s\n%s", code, stdout, stderr)
	}
	wantLines(t, stderr,
		"my: the answers of the BMC at 127.0.0.1 were not added: describes another machine: "+
			"none of the 4 DIMM serial numbers it lists is among the 16 SMBIOS lists for this host. Nothing of it was added")
	doc, _ := overview(t, fixed, "--ledger", ledger, "--root", root)
	if len(doc.Data.Ranking) != 16 {
		t.Errorf("%d DIMMs in the ledger that was kept, want the 16 of the host", len(doc.Data.Ranking))
	}
	for _, d := range doc.Data.Ranking {
		if strings.HasPrefix(d.DIMM.Serial, "TEST") || strings.HasPrefix(d.Slot, "DIMM_A") {
			t.Errorf("the ledger holds %s %s of the other machine", d.Slot, d.DIMM.Serial)
		}
	}
}

// A BMC that gives nothing ends the command, and the flags of a BMC are
// checked before anything is read.
func TestScanWithBMCErrors(t *testing.T) {
	root := hostTree(t)
	srv := bmctest.New(t, fstest.MapFS{}, bmctest.Options{Overlay: bmctest.Machine(2, 2, false), User: bmcUser, Password: bmcPassword})
	wrong := map[string]string{"MY_NOW": fixed["MY_NOW"], envRedfishUser: bmcUser, envRedfishPassword: "not-the-password"}
	code, stdout, stderr := runEnv(wrong, "--root", root, "--bmc", srv.URL, "--insecure")
	if code != cli.ExitFailure || stdout != "" {
		t.Errorf("wrong password: exit code %d\n%s\n%s", code, stdout, stderr)
	}
	wantLines(t, stderr, "denied: system", "stopped early: the BMC refused the account",
		"my: the BMC at "+srv.URL+" gave nothing about the system: see the lines above. The host was not scanned; without --bmc it is scanned alone")
	if strings.Contains(stderr, "not-the-password") {
		t.Errorf("the password was printed:\n%s", stderr)
	}

	before := len(srv.Requests())
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"a flag of the BMC without the BMC", []string{"--root", root, "--user", "reader"}, "--user says how a BMC is reached: name the BMC with --bmc ADDRESS"},
		{"insecure alone", []string{"bundle", "-o", filepath.Join(t.TempDir(), "x.mycase"), "--root", root, "--insecure"}, "--insecure says how a BMC is reached"},
		{"with a fleet store", []string{"--fleet", "mysql", "--bmc", srv.URL}, "--bmc asks the BMC of the one host that is scanned, and --fleet reads the store of many hosts"},
		{"plain http", []string{"--root", root, "--bmc", srv.URL}, "plain http sends the password unencrypted"},
		{"no address", []string{"--root", root, "--bmc", "https://"}, "--bmc: "},
		{"a deadline that is none", []string{"--root", root, "--bmc", srv.URL, "--insecure", "--bmc-deadline", "soon"}, "--bmc-deadline soon: want a time of at least 1s"},
		{"two ways to treat the certificate", []string{"--root", root, "--bmc", srv.URL, "--insecure", "--ca-file", "ca.pem"}, "give one"},
	} {
		code, stdout, stderr := runEnv(account(), tt.args...)
		if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, tt.want) {
			t.Errorf("%s: exit code %d, stderr %q", tt.name, code, stderr)
		}
	}
	if after := len(srv.Requests()); after != before {
		t.Errorf("%d requests were sent by commands with a wrong command line", after-before)
	}
}
