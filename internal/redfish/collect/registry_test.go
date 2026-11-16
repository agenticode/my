package collect

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/redfish/bmctest"
	"github.com/agenticode/my/internal/testutil"
)

// oneHost is a transport that fails the test when a request goes to
// another host than the stand-in BMC.
type oneHost struct {
	t    *testing.T
	host string
}

func (o oneHost) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != o.host {
		o.t.Errorf("a request went to another host: %s", r.URL)
		return nil, fmt.Errorf("not the BMC")
	}
	return http.DefaultTransport.RoundTrip(r)
}

// registryMachine is the stand-in machine with a log whose entries use the
// registries of the OpenBMC project and of a vendor, and a Registries
// collection with the standard Base registry, those two, one that is only
// published elsewhere, one whose copy never answers, and 17 more of the
// vendor.
func registryMachine(t *testing.T, extra int) map[string]string {
	tree := bmctest.Machine(2, 2, false)
	openbmc, err := os.ReadFile(testutil.Testdata(t, "redfish", "registries", "openbmc", "OpenBMC.0.5.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	tree["/redfish/v1/Managers/1/LogServices/EventLog/Entries"] = `{"@odata.id":"/redfish/v1/Managers/1/LogServices/EventLog/Entries","Members@odata.count":2,"Members":[` +
		`{"@odata.id":"/redfish/v1/Managers/1/LogServices/EventLog/Entries/1","@odata.type":"#LogEntry.v1_15_0.LogEntry","Id":"1","EntryType":"Event","Created":"2026-11-10T01:00:00Z","MessageId":"OpenBMC.0.1.MemoryECCCorrectable","MessageArgs":["1","A","0","0"],"Message":"x"},` +
		`{"@odata.id":"/redfish/v1/Managers/1/LogServices/EventLog/Entries/2","@odata.type":"#LogEntry.v1_15_0.LogEntry","Id":"2","EntryType":"Event","Created":"2026-11-10T02:00:00Z","MessageId":"ExampleCo.1.0.MemFault7","MessageArgs":["DIMM_A1"],"Message":"Event 7"}]}`
	members := []string{"Base.1.8.0", "OpenBMC", "ExampleCo.1.0.0", "Elsewhere.1.0.0", "Silent.1.0.0", "BiosAttributeRegistryEX.v1_0_0"}
	file := func(id, loc string) {
		tree["/redfish/v1/Registries/"+id] = fmt.Sprintf(`{"@odata.id":"/redfish/v1/Registries/%s","@odata.type":"#MessageRegistryFile.v1_1_0.MessageRegistryFile","Id":%q,"Registry":%q,"Location":[%s]}`, id, id, id, loc)
	}
	file("OpenBMC", `{"Language":"en","Uri":"/redfish/v1/Registries/OpenBMC/OpenBMC"}`)
	tree["/redfish/v1/Registries/OpenBMC/OpenBMC"] = string(openbmc)
	file("ExampleCo.1.0.0", `{"Language":"ja","Uri":"/redfish/v1/RegistryStore/ja/ExampleCo.json"},{"Language":"en","Uri":"/redfish/v1/RegistryStore/en/ExampleCo.json","PublicationUri":"https://vendor.example/ExampleCo.json"}`)
	tree["/redfish/v1/RegistryStore/en/ExampleCo.json"] = `{"@odata.type":"#MessageRegistry.v1_6_0.MessageRegistry","Id":"ExampleCo.1.0.0","RegistryPrefix":"ExampleCo","RegistryVersion":"1.0.0","OwningEntity":"Example Co","Language":"en",` +
		`"Messages":{"MemFault7":{"Message":"Post package repair of DIMM %1 failed.","MessageSeverity":"OK","NumberOfArgs":1},"MemFault9":{"Message":"Memory module %1 overheated.","NumberOfArgs":1}}}`
	file("Elsewhere.1.0.0", `{"Language":"en","PublicationUri":"https://vendor.example/Elsewhere.json"}`)
	file("Silent.1.0.0", `{"Language":"en","Uri":"/redfish/v1/RegistryStore/en/Silent.json"}`)
	for i := range extra {
		id := fmt.Sprintf("Extra%02d.1.0.0", i)
		members = append(members, id)
		file(id, fmt.Sprintf(`{"Language":"en","Uri":"/redfish/v1/RegistryStore/en/Extra%02d.json"}`, i))
		tree[fmt.Sprintf("/redfish/v1/RegistryStore/en/Extra%02d.json", i)] = fmt.Sprintf(`{"RegistryPrefix":"Extra%02d","RegistryVersion":"1.0.0","Messages":{}}`, i)
	}
	var refs []string
	for _, m := range members {
		refs = append(refs, fmt.Sprintf(`{"@odata.id":"/redfish/v1/Registries/%s"}`, m))
	}
	tree["/redfish/v1/Registries"] = fmt.Sprintf(`{"@odata.id":"/redfish/v1/Registries","Members@odata.count":%d,"Members":[%s]}`, len(refs), strings.Join(refs, ","))
	return tree
}

// TestMessageRegistries: the collector fetches with GET the registries the
// log entries use and those of the vendor, from the BMC itself, at most 16;
// a registry published only on another host is named and not asked; one
// that does not answer leaves the others alone. The adapter then classes
// the entries by them and records what they cover.
func TestMessageRegistries(t *testing.T) {
	srv := bmctest.New(t, empty(), bmctest.Options{Overlay: registryMachine(t, 17), Handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/redfish/v1/RegistryStore/en/Silent.json" {
			<-r.Context().Done()
			return true
		}
		return false
	}})
	opts := options(srv, "", "")
	u, _ := url.Parse(srv.URL)
	opts.Client.Transport = oneHost{t: t, host: u.Host}
	opts.Client.Timeout = 300 * 1e6 // 300 ms: the silent registry ends there
	m, dir := collect(t, opts)
	onlyGETs(t, srv)
	if srv.Asked("/redfish/v1/Registries/Base.1.8.0") != 0 || srv.Asked("/redfish/v1/RegistryStore/ja/ExampleCo.json") != 0 {
		t.Errorf("asked for the standard Base registry or the Japanese copy")
	}
	for _, uri := range []string{"/redfish/v1/Registries/OpenBMC/OpenBMC", "/redfish/v1/RegistryStore/en/ExampleCo.json"} {
		if r := resource(t, m, uri); r.Class != ClassMessageRegistry || r.Status != StatusOK {
			t.Errorf("%s: %+v", uri, r)
		}
	}
	elsewhere := false
	for _, r := range m.Resources {
		if r.URI == "/redfish/v1/Registries/Elsewhere.1.0.0" && r.Class == ClassMessageRegistry {
			elsewhere = r.Status == StatusUnsupported && strings.Contains(r.Detail, "another host")
		}
	}
	if !elsewhere {
		t.Errorf("the registry published elsewhere is not named as such")
	}
	if r := resource(t, m, "/redfish/v1/RegistryStore/en/Silent.json"); r.Status == StatusOK {
		t.Errorf("the silent registry: %+v", r)
	}
	fetched := 0
	for _, r := range m.Resources {
		if r.Class == ClassMessageRegistry && strings.HasPrefix(r.URI, "/redfish/v1/RegistryStore/") || strings.HasSuffix(r.URI, "/OpenBMC/OpenBMC") {
			fetched++
		}
	}
	if fetched != maxMessageRegistries || !strings.Contains(strings.Join(m.Notes, "\n"), "more message registries than the 16") {
		t.Errorf("%d registries asked, notes %v", fetched, m.Notes)
	}
	if c := m.Class(ClassMessageRegistry); c.State != StatePartial && c.State != StateOK {
		t.Errorf("class: %+v", c)
	}

	// The case made of the collection.
	var c adapter.Collected
	rep := redfish.New(filepath.Join(dir), redfish.Options{}).Collect(context.Background(), &c)
	seen := map[string]int{}
	for _, r := range c.Registries {
		seen[r.Prefix] = r.Mapped*1000 + r.Wording
	}
	if seen["OpenBMC"] != 15000 || seen["ExampleCo"] != 1 {
		t.Errorf("registries seen: %+v\n%v", c.Registries, rep.Notes)
	}
	acts := 0
	for _, a := range c.Actions {
		if a.Firmware != nil && a.Firmware.VendorID == "MemFault7" && a.Firmware.Mapping == "bmc-registry" {
			acts++
		}
	}
	if acts != 1 {
		t.Errorf("the vendor message was not read by its registry: %+v", c.Actions)
	}
}
