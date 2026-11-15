package collect

import (
	"bytes"
	"context"
	"hash/fnv"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/redfish/client"
)

// fuzzToken is the session token the service of the fuzz target hands out.
const fuzzToken = "tok-5d41402abc4b2a76"

// service is a BMC made of three documents. The service root is one of
// them; every other URI answers with one of the other two, chosen by the
// URI, so that a run finds a service in which every link leads somewhere.
type service struct {
	root, a, b []byte
	// status answers the URIs of one in seven, when it is not 200.
	status   int
	requests int
	methods  map[string]int
}

func (s *service) RoundTrip(req *http.Request) (*http.Response, error) {
	s.requests++
	s.methods[req.Method]++
	if req.Body != nil {
		io.Copy(io.Discard, req.Body)
		req.Body.Close()
	}
	answer := func(status int, body []byte, header http.Header) (*http.Response, error) {
		if header == nil {
			header = http.Header{}
		}
		header.Set("Content-Type", "application/json")
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: req}, nil
	}
	switch {
	case req.Method == http.MethodPost:
		return answer(http.StatusCreated, []byte(`{"@odata.id":"/redfish/v1/SessionService/Sessions/1"}`),
			http.Header{"X-Auth-Token": {fuzzToken}, "Location": {"/redfish/v1/SessionService/Sessions/1"}})
	case req.Method == http.MethodDelete:
		return answer(http.StatusNoContent, nil, nil)
	case req.URL.Path == ServiceRoot:
		return answer(http.StatusOK, s.root, nil)
	}
	h := fnv.New32a()
	h.Write([]byte(req.URL.Path))
	sum := h.Sum32()
	if s.status != http.StatusOK && sum%7 == 0 {
		return answer(s.status, s.a, nil)
	}
	if sum%2 == 0 {
		return answer(http.StatusOK, s.a, nil)
	}
	return answer(http.StatusOK, s.b, nil)
}

// FuzzCrawl runs a collection against a BMC whose resources are arbitrary
// JSON. Whatever the service says, the run ends, asks a bounded number of
// times, only reads, writes nothing outside its directory, keeps the
// account out of the files, and leaves a collection the Redfish reader
// gets through.
func FuzzCrawl(f *testing.F) {
	root := `{"@odata.id":"/redfish/v1/","RedfishVersion":"1.17.0","Vendor":"Example","Product":"X",
"ProtocolFeaturesSupported":{"ExpandQuery":{"ExpandAll":true,"Levels":true,"NoLinks":true}},
"Systems":{"@odata.id":"/redfish/v1/Systems"},"Managers":{"@odata.id":"/redfish/v1/Managers"},
"Chassis":{"@odata.id":"/redfish/v1/Chassis"},"UpdateService":{"@odata.id":"/redfish/v1/UpdateService"},
"Registries":{"@odata.id":"/redfish/v1/Registries"},"Links":{"Sessions":{"@odata.id":"/redfish/v1/SessionService/Sessions"}}}`
	// A collection that is a system and a log service as well.
	a := `{"@odata.id":"/redfish/v1/Systems","Id":"1","@odata.type":"#ComputerSystem.v1_20_0.ComputerSystem","Members@odata.count":3,
"Members":[{"@odata.id":"/redfish/v1/Systems/1"},
{"@odata.id":"/redfish/v1/Systems/1/Memory/2","Id":"2","@odata.type":"#Memory.v1_17_0.Memory","Name":"DIMM 2","DeviceLocator":"CPU0_C1","CapacityMiB":32768,
 "SerialNumber":"80A1C3","Status":{"State":"Enabled","Health":"Critical"},"Metrics":{"@odata.id":"/redfish/v1/Systems/1/Memory/2/MemoryMetrics"}},
{"@odata.id":"/redfish/v1/Systems/1/LogServices/SEL/Entries/7","Id":"7","@odata.type":"#LogEntry.v1_15_0.LogEntry","EntryType":"SEL","SensorType":"Memory",
 "Created":"2026-11-09T04:55:37Z","Message":"Correctable ECC","MessageId":"0x6fa0ff01","Severity":"Warning"}],
"Members@odata.nextLink":"/redfish/v1/Systems?$skip=3",
"Memory":{"@odata.id":"/redfish/v1/Systems/1/Memory"},"Bios":{"@odata.id":"/redfish/v1/Systems/1/Bios"},
"LogServices":{"@odata.id":"/redfish/v1/Systems/1/LogServices"},"Entries":{"@odata.id":"/redfish/v1/Systems/1/LogServices/SEL/Entries"},
"Manufacturer":"Example","Model":"R760","HostName":"node-a","BiosVersion":"1.2.3"}`
	// A Bios resource that is a registry file and a manager as well, and
	// repeats the password and the token of the run.
	b := `{"@odata.id":"/redfish/v1/Systems/1/Bios","Id":"Bios","AttributeRegistry":"BiosAttributeRegistry.1.0.0",
"@Redfish.Settings":{"SettingsObject":{"@odata.id":"/redfish/v1/Systems/1/Bios/Settings"}},"Attributes":{"MemPatrolScrub":"Standard","Password":"` + testPassword + `"},
"Location":[{"Language":"en","Uri":"/redfish/v1/Registries/BiosAttributeRegistry.1.0.0.json","PublicationUri":"https://example.org/r.json"}],
"Members":[{"@odata.id":"/redfish/v1/Registries/BiosAttributeRegistry.1.0.0"},{"@odata.id":"/redfish/v1/Managers/1"}],
"DateTime":"2026-11-16T03:00:00","DateTimeLocalOffset":"+09:00","Model":"BMC","FirmwareVersion":"2.1","Oem":{"Token":"` + fuzzToken + `"},
"FirmwareInventory":{"@odata.id":"/redfish/v1/UpdateService/FirmwareInventory"},"LogPurposes":["Security"],
"LogServices":{"@odata.id":"/redfish/v1/Managers/1/LogServices"},"Entries":{"@odata.id":"/redfish/v1/Managers/1/LogServices/Log/Entries"}}`
	f.Add([]byte(root), []byte(a), []byte(b), uint16(200), false)
	f.Add([]byte(root), []byte(a), []byte(b), uint16(404), true)
	f.Add([]byte(root), []byte(b), []byte(a), uint16(503), true)
	f.Add([]byte(root), []byte(a), []byte(b), uint16(401), false)
	f.Add([]byte(root), []byte(`{"Members":[{"@odata.id":"/a/../b"},{"@odata.id":"https://evil.example/x"},{"@odata.id":"/a/b:c"},{"@odata.id":"//x//"},{"@odata.id":"/A"},{"@odata.id":"/a"}],
"Members@odata.nextLink":"/redfish/v1/Systems","Memory":{"@odata.id":"/COM1"},"Bios":{"@odata.id":"/redfish/v1/manifest.json"}}`), []byte(`[]`), uint16(200), false)
	f.Add([]byte(`{"Systems":{"@odata.id":"/redfish/v1/"},"Managers":{"@odata.id":"/redfish/v1"}}`), []byte(`{}`), []byte(`null`), uint16(200), false)
	f.Add([]byte(`{`), []byte(``), []byte(``), uint16(200), false)

	ctx := context.Background()
	f.Fuzz(func(t *testing.T, root, a, b []byte, status uint16, session bool) {
		svc := &service{root: root, a: a, b: b, status: int(status), methods: map[string]int{}}
		parent := t.TempDir()
		dir := filepath.Join(parent, "bmc")
		const maxTotal = 4 << 20
		m, err := Run(ctx, dir, Options{
			Client: client.Config{BaseURL: "https://10.0.0.5", User: testUser, Password: testPassword, Session: session, Transport: svc,
				Sleep: noSleep, Timeout: 5 * time.Second, Retries: 1, MaxBody: 256 << 10},
			Tool: "my fuzz", Now: func() time.Time { return fixedNow }, Deadline: 20 * time.Second, MaxLogEntries: 20, MaxTotalBytes: maxTotal,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if svc.requests > 4000 || len(m.Resources) > maxResources {
			t.Fatalf("%d requests for %d resources", svc.requests, len(m.Resources))
		}
		for method, n := range svc.methods {
			switch {
			case method == http.MethodGet:
			case method == http.MethodPost && session && n <= 3, method == http.MethodDelete && session && n == 1:
			default:
				t.Errorf("%d %s requests (session %v)", n, method, session)
			}
		}
		if got, err := ReadManifest(dir); err != nil || got.Requests != m.Requests || len(got.Resources) != len(m.Resources) {
			t.Fatalf("the manifest on disk: %v", err)
		}

		// What is on disk: the files the manifest lists, below the
		// directory, and nothing that holds a secret of the run.
		listed := map[string]bool{ManifestName: true}
		for _, r := range m.Resources {
			if len(r.URI) > 2300 || len(r.Detail) > 2000 || strings.ContainsAny(r.Detail, "\r\n") {
				t.Errorf("resource %q with a detail of %d bytes", r.URI, len(r.Detail))
			}
			if r.File == "" {
				continue
			}
			if !filepath.IsLocal(filepath.FromSlash(r.File)) || !strings.HasSuffix(r.File, "/"+indexName) {
				t.Fatalf("resource %s was written to %q", r.URI, r.File)
			}
			listed[r.File] = true
		}
		entries, err := os.ReadDir(parent)
		if err != nil || len(entries) != 1 {
			t.Fatalf("%d entries beside the collection (%v)", len(entries), err)
		}
		var written int64
		err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(dir, p)
			rel = filepath.ToSlash(rel)
			if !d.Type().IsRegular() || !listed[rel] {
				t.Errorf("%s is in the collection and not in the manifest", rel)
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if rel != ManifestName {
				written += int64(len(data))
				if _, err := parseObject(data); err != nil {
					t.Errorf("%s is no JSON object: %v", rel, err)
				}
			}
			if bytes.Contains(data, []byte(testPassword)) {
				t.Errorf("%s holds the password", rel)
			}
			if session && m.Auth == "session" && bytes.Contains(data, []byte(fuzzToken)) && svc.methods[http.MethodPost] > 0 {
				t.Errorf("%s holds the session token", rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if written > maxTotal {
			t.Errorf("%d bytes written, the limit is %d", written, maxTotal)
		}

		// The collection is an input: the reader of Redfish dumps gets
		// through it, and what it hands on are records the ledger takes.
		var c adapter.Collected
		rep := redfish.New(dir, redfish.Options{Now: fixedNow}).Collect(ctx, &c)
		if rep.Status == "" || rep.SinkErr != nil {
			t.Fatalf("the reader of the collection: %+v", rep)
		}
		okTime := func(at time.Time) bool { y := at.UTC().Year(); return !at.IsZero() && y >= 1970 && y <= 9999 }
		for _, e := range c.Events {
			if !e.ErrorSeverity.Valid() || !e.Grade.Valid() || e.Fingerprint == "" || e.Source == "" || e.Count != 1 || !okTime(e.ErrorDT) {
				t.Errorf("event the ledger would refuse: %+v", e)
			}
		}
		for _, a := range c.Actions {
			if !a.Kind.Valid() || a.Fingerprint == "" || a.Source == "" || !okTime(a.At) {
				t.Errorf("action the ledger would refuse: %+v", a)
			}
		}
		for _, mod := range c.Modules {
			if mod.Locator == "" || !okTime(mod.ObservedAt) || mod.DIMM.SizeBytes < 0 {
				t.Errorf("module the ledger would refuse: %+v", mod)
			}
		}
	})
}
