package collect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/redfish/bmctest"
	"github.com/agenticode/my/internal/redfish/client"
	"github.com/agenticode/my/internal/testutil"
)

const (
	testUser     = "reader"
	testPassword = "c0llect-Pa55word!"
)

var fixedNow = time.Date(2026, 11, 16, 3, 0, 0, 0, time.UTC)

func noSleep(context.Context, time.Duration) error { return nil }

// options returns the settings of a run against a stand-in.
func options(srv *bmctest.Server, user, password string) Options {
	return Options{
		Client: client.Config{BaseURL: srv.URL, Insecure: true, User: user, Password: password, Sleep: noSleep, Timeout: 5 * time.Second},
		Tool:   "my test", Now: func() time.Time { return fixedNow },
	}
}

func collect(t *testing.T, opts Options) (*Manifest, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "bmc")
	m, err := Run(context.Background(), dir, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The manifest on disk is the one that was returned.
	onDisk, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	a, _ := json.Marshal(m)
	b, _ := json.Marshal(onDisk)
	if !bytes.Equal(a, b) {
		t.Errorf("the manifest on disk differs from the one returned")
	}
	return m, dir
}

// resource returns the entry of the manifest for a URI.
func resource(t *testing.T, m *Manifest, uri string) Resource {
	t.Helper()
	for _, r := range m.Resources {
		if r.URI == uri {
			return r
		}
	}
	t.Fatalf("the manifest has no entry for %s", uri)
	return Resource{}
}

func state(m *Manifest, class string) string {
	if c := m.Class(class); c != nil {
		return c.State
	}
	return "(no row)"
}

// states returns the state of every class as "class=state" lines.
func states(m *Manifest) string {
	var b strings.Builder
	for _, c := range m.Classes {
		fmt.Fprintf(&b, "%s=%s\n", c.Class, c.State)
	}
	return b.String()
}

// files lists the files of a collection.
func files(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

func empty() fs.FS { return fstest.MapFS{} }

func onlyGETs(t *testing.T, srv *bmctest.Server) {
	t.Helper()
	if w := srv.Writes(); len(w) != 0 {
		t.Errorf("requests that write: %v", w)
	}
	for _, r := range srv.Requests() {
		if !strings.HasPrefix(r, "GET ") && !strings.HasPrefix(r, "POST "+bmctest.Sessions) && !strings.HasPrefix(r, "DELETE "+bmctest.Sessions+"/") {
			t.Errorf("request %q", r)
		}
		for _, private := range []string{"/AccountService", "/Actions/", "/Certificates", "/EthernetInterfaces", "/NetworkProtocol"} {
			if strings.Contains(r, private) {
				t.Errorf("the run asked for %s", r)
			}
		}
	}
}

// The public DMTF mockup, as it is: what comes back, what the manifest
// says about the three things this mockup lacks, and that the tree on disk
// is one the adapter for saved dumps reads.
func TestDMTFMockup(t *testing.T) {
	src := testutil.Testdata(t, "redfish", "dmtf-rackmount1")
	srv := bmctest.New(t, os.DirFS(src), bmctest.Options{})
	m, dir := collect(t, options(srv, "", ""))
	onlyGETs(t, srv)

	if !m.Complete || m.Stopped != "" || m.Auth != "none" || m.TLS != "none" || m.Tool != "my test" {
		t.Errorf("manifest: complete %v, stopped %q, auth %s, tls %s", m.Complete, m.Stopped, m.Auth, m.TLS)
	}
	want := `service-root=ok
system=ok
memory=ok
memory-metrics=not offered
bios=ok
bios-settings=ok
attribute-registry=unsupported
manager=ok
log-service=ok
log-entries=ok
firmware-inventory=ok
chassis=ok
`
	if got := states(m); got != want {
		t.Errorf("classes:\n%s\nwant:\n%s", got, want)
	}
	if m.Service.RedfishVersion != "1.15.0" || len(m.Systems) != 1 || m.Systems[0].ID != "437XR1138R2" ||
		m.Systems[0].AttributeRegistry != "BiosAttributeRegistryP89.v1_0_0" || m.Systems[0].Manufacturer != "Contoso" {
		t.Errorf("service %+v, systems %+v", m.Service, m.Systems)
	}
	if len(m.Managers) != 1 || m.Managers[0].FirmwareVersion == "" || len(m.Firmware) != 3 {
		t.Errorf("managers %+v, firmware %+v", m.Managers, m.Firmware)
	}
	if m.Expand != "$expand=.($levels=1)" {
		t.Errorf("expand = %q", m.Expand)
	}

	// The registry the Bios names is not listed by the service.
	reg := resource(t, m, "/redfish/v1/Registries")
	found := false
	for _, r := range m.Resources {
		if r.Class == ClassRegistry && r.Status == StatusUnsupported && strings.Contains(r.Detail, "BiosAttributeRegistryP89.v1_0_0") {
			found = true
		}
	}
	if !found || reg.Status != StatusOK {
		t.Errorf("registry: %+v", reg)
	}
	// The log ends with a link to a next page. The stand-in, like a
	// server of static files, answers it with the same page again: the
	// two entries are taken once and the manifest says what happened.
	log := resource(t, m, "/redfish/v1/Systems/437XR1138R2/LogServices/Log1/Entries")
	if log.Status != StatusOK || log.Members != 2 || log.Pages != 2 || !strings.Contains(log.Detail, "does not follow its own link to the next page") || log.File == "" {
		t.Errorf("log: %+v", log)
	}
	entries := readJSON(t, filepath.Join(dir, filepath.FromSlash(log.File)))
	if list, _ := entries["Members"].([]any); len(list) != 2 || entries["@odata.nextLink"] != nil {
		t.Errorf("merged log: %d members, nextLink %v", len(list), entries["@odata.nextLink"])
	}

	// Every file is the answer of the service: the same JSON as the fixture.
	for _, f := range files(t, dir) {
		if f == ManifestName {
			continue
		}
		srcFile := filepath.Join(src, filepath.FromSlash(f))
		if _, err := os.Stat(srcFile); err != nil {
			t.Errorf("%s is in the collection and not in the mockup", f)
			continue
		}
		got, wantDoc := readJSON(t, filepath.Join(dir, filepath.FromSlash(f))), readJSON(t, srcFile)
		delete(got, "@odata.nextLink")
		delete(wantDoc, "@odata.nextLink")
		a, _ := json.Marshal(got)
		b, _ := json.Marshal(wantDoc)
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs from the mockup", f)
		}
	}
	for _, f := range files(t, dir) {
		if strings.Contains(f, "AccountService") || strings.Contains(f, "SessionService") {
			t.Errorf("%s was collected", f)
		}
	}

	// The adapter for saved dumps reads the collection.
	var c adapter.Collected
	rep := redfish.New(dir, redfish.Options{Now: fixedNow}).Collect(context.Background(), &c)
	if len(c.Modules) != 4 || rep.HostHint != "web483" {
		t.Errorf("adapter: %d modules, host %q, status %s %s", len(c.Modules), rep.HostHint, rep.Status, rep.Detail)
	}
}

func TestMachine(t *testing.T) {
	srv := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(4, 6, false), User: testUser, Password: testPassword})
	m, dir := collect(t, options(srv, testUser, testPassword))
	onlyGETs(t, srv)
	if !m.Complete || m.Partial() || m.Failed() != 0 {
		t.Fatalf("complete %v, partial %v, failed %d, stopped %q\n%s", m.Complete, m.Partial(), m.Failed(), m.Stopped, states(m))
	}
	for _, c := range m.Classes {
		if c.State != StateOK {
			t.Errorf("%s = %s", c.Class, c.State)
		}
	}
	// The registry is found through the registry file, in English, and
	// the copy on the vendor's host is not asked for.
	reg := resource(t, m, "/redfish/v1/RegistryStore/en/BiosAttributeRegistryEX.json")
	if reg.Class != ClassRegistry || reg.Status != StatusOK {
		t.Errorf("registry: %+v", reg)
	}
	if srv.Asked("/redfish/v1/RegistryStore/ja/BiosAttributeRegistryEX.json") != 0 || srv.Asked("/redfish/v1/Registries/Base.1.8.0") != 0 {
		t.Errorf("the run asked for registries it has no use for")
	}
	if c := m.Class(ClassRegistry); c.OK != 1 {
		t.Errorf("registry row: %+v (the lookups are not the registry)", c)
	}
	// Logs: the event logs are read, the others are named as skipped.
	for uri, want := range map[string]string{
		"/redfish/v1/Systems/1/LogServices/SEL/Entries":        StatusOK,
		"/redfish/v1/Managers/1/LogServices/EventLog/Entries":  StatusOK,
		"/redfish/v1/Systems/1/LogServices/Journal/Entries":    StatusSkipped,
		"/redfish/v1/Systems/1/LogServices/AuditLog/Entries":   StatusSkipped,
		"/redfish/v1/Systems/1/LogServices/HostLogger/Entries": StatusSkipped,
	} {
		r := resource(t, m, uri)
		if r.Status != want {
			t.Errorf("%s: %s, want %s", uri, r.Status, want)
		}
		if want == StatusSkipped && (srv.Asked(uri) != 0 || !strings.Contains(r.Detail, "--log")) {
			t.Errorf("%s: asked %d times, detail %q", uri, srv.Asked(uri), r.Detail)
		}
	}
	if m.Clock == nil || m.Clock.OffsetSeconds != 120 {
		t.Errorf("clock = %+v, want the BMC 120 s ahead", m.Clock)
	}
	if m.Counts[StatusOK] != len(m.Resources)-3 || m.Counts[StatusSkipped] != 3 {
		t.Errorf("counts = %v of %d", m.Counts, len(m.Resources))
	}
	if n, _ := m.Requests, m.Bytes; n != len(srv.Requests()) {
		t.Errorf("manifest counts %d requests, the service saw %d", n, len(srv.Requests()))
	}
	// Mockup layout.
	for _, f := range []string{"redfish/v1/index.json", "redfish/v1/Systems/1/Memory/DIMM3/MemoryMetrics/index.json",
		"redfish/v1/Systems/1/Bios/Settings/index.json", "redfish/v1/RegistryStore/en/BiosAttributeRegistryEX.json/index.json", ManifestName} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	// The account is in no file.
	leaks(t, dir, testPassword, testUser+":"+testPassword)
}

// leaks fails when a file below dir holds one of the secrets.
func leaks(t *testing.T, dir string, secrets ...string) {
	t.Helper()
	for _, f := range files(t, dir) {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range secrets {
			if s != "" && bytes.Contains(data, []byte(s)) {
				t.Errorf("%s holds %q", f, s)
			}
		}
	}
}

func TestPagingAndLimits(t *testing.T) {
	srv := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(7, 23, false), PageSize: 5})
	m, dir := collect(t, options(srv, "", ""))
	sel := resource(t, m, "/redfish/v1/Systems/1/LogServices/SEL/Entries")
	if sel.Status != StatusOK || sel.Pages != 5 || sel.Members != 23 {
		t.Errorf("SEL: %+v", sel)
	}
	doc := readJSON(t, filepath.Join(dir, filepath.FromSlash(sel.File)))
	list, _ := doc["Members"].([]any)
	if len(list) != 23 || doc["Members@odata.nextLink"] != nil {
		t.Fatalf("merged SEL: %d members", len(list))
	}
	ids := map[string]bool{}
	for _, e := range list {
		ids[fmt.Sprint(e.(map[string]any)["Id"])] = true
	}
	if len(ids) != 23 {
		t.Errorf("merged SEL has %d different entries", len(ids))
	}
	mem := resource(t, m, "/redfish/v1/Systems/1/Memory")
	if mem.Pages != 2 || mem.Members != 7 || m.Class(ClassMemory).OK != 8 || m.Class(ClassMetrics).OK != 7 {
		t.Errorf("memory: %+v, classes %s", mem, states(m))
	}

	// A bound on the entries of a log: the first ones are kept and the
	// manifest says how many there are.
	o := options(srv, "", "")
	o.MaxLogEntries = 12
	m, _ = collect(t, o)
	sel = resource(t, m, "/redfish/v1/Systems/1/LogServices/SEL/Entries")
	if sel.Status != StatusOK || sel.Members != 12 || !strings.Contains(sel.Detail, "12 of 23 entries read") {
		t.Errorf("bounded SEL: %+v", sel)
	}
	if !m.Complete || len(m.Notes) == 0 {
		t.Errorf("complete %v, notes %v", m.Complete, m.Notes)
	}

	// No logs at all.
	o.MaxLogEntries = -1
	m, _ = collect(t, o)
	if state(m, ClassLogEntries) != StatusSkipped || srv.Asked("/redfish/v1/Managers/1/LogServices/EventLog/Entries") != 2 {
		t.Errorf("no logs: %s", states(m))
	}

	// A log named for the run is read although it is on the list of
	// skipped logs; the others are treated as before.
	o = options(srv, "", "")
	o.Logs = []string{"Journal"}
	m, _ = collect(t, o)
	if resource(t, m, "/redfish/v1/Systems/1/LogServices/Journal/Entries").Status != StatusOK ||
		resource(t, m, "/redfish/v1/Systems/1/LogServices/SEL/Entries").Status != StatusOK ||
		resource(t, m, "/redfish/v1/Systems/1/LogServices/AuditLog/Entries").Status != StatusSkipped {
		t.Errorf("named logs: %s", states(m))
	}
}

func TestExpand(t *testing.T) {
	srv := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(6, 4, true), Expand: true})
	m, dir := collect(t, options(srv, "", ""))
	if m.Expand != "$expand=.($levels=1)" || !m.Complete || m.Failed() != 0 {
		t.Fatalf("expand %q, %s", m.Expand, states(m))
	}
	for i := 1; i <= 6; i++ {
		uri := fmt.Sprintf("/redfish/v1/Systems/1/Memory/DIMM%d", i)
		r := resource(t, m, uri)
		if srv.Asked(uri) != 0 || r.Via != "expand" || r.Status != StatusOK {
			t.Errorf("%s: asked %d times, %+v", uri, srv.Asked(uri), r)
		}
		doc := readJSON(t, filepath.Join(dir, filepath.FromSlash(r.File)))
		if doc["DeviceLocator"] != fmt.Sprintf("DIMM_A%d", i) {
			t.Errorf("%s on disk: %v", uri, doc["DeviceLocator"])
		}
		// The metrics are not part of the collection and are asked for.
		if srv.Asked(uri+"/MemoryMetrics") != 1 {
			t.Errorf("%s/MemoryMetrics asked %d times", uri, srv.Asked(uri+"/MemoryMetrics"))
		}
	}
	if srv.Asked("/redfish/v1/UpdateService/FirmwareInventory/BIOS") != 0 || len(m.Firmware) != 2 {
		t.Errorf("firmware inventory: %+v", m.Firmware)
	}
	plain := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(6, 4, false)})
	pm, _ := collect(t, options(plain, "", ""))
	if len(srv.Requests()) >= len(plain.Requests()) || pm.Expand != "" {
		t.Errorf("with expand %d requests, without %d", len(srv.Requests()), len(plain.Requests()))
	}

	// A service that says it expands and refuses: the collection is read
	// member by member and the manifest says so.
	refusing := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(3, 2, true), Handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Query().Get("$expand") != "" {
			bmctest.Fail(w, http.StatusBadRequest, "The query parameter $expand is not supported.")
			return true
		}
		return false
	}})
	m, _ = collect(t, options(refusing, "", ""))
	if !m.Complete || m.Failed() != 0 || refusing.Asked("/redfish/v1/Systems/1/Memory/DIMM2") != 1 {
		t.Errorf("refused expand: %s", states(m))
	}
	if len(m.Notes) == 0 || !strings.Contains(strings.Join(m.Notes, "\n"), "refused $expand") {
		t.Errorf("notes = %v", m.Notes)
	}
}

func TestAccount(t *testing.T) {
	tree := bmctest.Machine(2, 2, false)
	t.Run("wrong password", func(t *testing.T) {
		srv := bmctest.New(t, empty(), bmctest.Options{Overlay: tree, User: testUser, Password: testPassword})
		m, _ := collect(t, options(srv, testUser, "not-the-password"))
		if m.Complete || !strings.Contains(m.Stopped, "refused the account") || len(srv.Requests()) != 2 {
			t.Errorf("stopped %q after %v", m.Stopped, srv.Requests())
		}
		if state(m, ClassRoot) != StateOK || state(m, ClassSystem) != StatusDenied || state(m, ClassMemory) != StateNotReached {
			t.Errorf("classes:\n%s", states(m))
		}
	})
	t.Run("no account", func(t *testing.T) {
		srv := bmctest.New(t, empty(), bmctest.Options{Overlay: tree, User: testUser, Password: testPassword})
		m, _ := collect(t, options(srv, "", ""))
		if !strings.Contains(m.Stopped, "asks for an account") || len(srv.Requests()) != 2 {
			t.Errorf("stopped %q after %v", m.Stopped, srv.Requests())
		}
	})
	t.Run("session", func(t *testing.T) {
		srv := bmctest.New(t, empty(), bmctest.Options{Overlay: tree, User: testUser, Password: testPassword, SessionOnly: true})
		o := options(srv, testUser, testPassword)
		o.Client.Session = true
		m, dir := collect(t, o)
		onlyGETs(t, srv)
		if !m.Complete || m.Auth != "session" || m.Failed() != 0 {
			t.Fatalf("complete %v, auth %s, stopped %q", m.Complete, m.Auth, m.Stopped)
		}
		if opened, closed, live := srv.SessionCounts(); opened != 1 || closed != 1 || live != 0 {
			t.Errorf("sessions: opened %d, closed %d, live %d", opened, closed, live)
		}
		reqs := srv.Requests()
		if reqs[0] != "GET /redfish/v1/" || reqs[1] != "POST "+bmctest.Sessions || reqs[len(reqs)-1] != "DELETE "+bmctest.Sessions+"/1" {
			t.Errorf("order: %v ... %v", reqs[:2], reqs[len(reqs)-1])
		}
		leaks(t, dir, testPassword, "session-token-")
	})
	t.Run("session without the right password", func(t *testing.T) {
		srv := bmctest.New(t, empty(), bmctest.Options{Overlay: tree, User: testUser, Password: testPassword, SessionOnly: true})
		o := options(srv, testUser, "not-the-password")
		o.Client.Session = true
		m, _ := collect(t, o)
		if !strings.Contains(m.Stopped, "login to the session service failed") || len(srv.Requests()) != 2 || strings.Contains(m.Stopped, "not-the-password") {
			t.Errorf("stopped %q after %v", m.Stopped, srv.Requests())
		}
	})
	t.Run("basic against a service that wants a session", func(t *testing.T) {
		srv := bmctest.New(t, empty(), bmctest.Options{Overlay: tree, User: testUser, Password: testPassword, SessionOnly: true})
		m, _ := collect(t, options(srv, testUser, testPassword))
		if !strings.Contains(m.Stopped, "refused the account") {
			t.Errorf("stopped %q", m.Stopped)
		}
	})
	t.Run("session dropped in the middle", func(t *testing.T) {
		var srv *bmctest.Server
		var n atomic.Int32
		srv = bmctest.New(t, empty(), bmctest.Options{Overlay: tree, User: testUser, Password: testPassword, SessionOnly: true,
			Handler: func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == http.MethodGet && n.Add(1) == 6 {
					srv.DropSessions()
				}
				return false
			}})
		o := options(srv, testUser, testPassword)
		o.Client.Session = true
		m, _ := collect(t, o)
		opened, closed, live := srv.SessionCounts()
		if !m.Complete || m.Failed() != 0 || opened != 2 || closed != 1 || live != 0 {
			t.Errorf("complete %v, failed %d, sessions %d/%d/%d", m.Complete, m.Failed(), opened, closed, live)
		}
	})
}

func TestResourcesThatDoNotComeBack(t *testing.T) {
	var chassis atomic.Int32
	srv := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(3, 2, false), Handler: func(w http.ResponseWriter, r *http.Request) bool {
		switch p := r.URL.Path; {
		case strings.HasSuffix(p, "/MemoryMetrics"):
			bmctest.Fail(w, http.StatusNotFound, "not here")
		case p == "/redfish/v1/Systems/1/Bios/Settings":
			bmctest.Fail(w, http.StatusMethodNotAllowed, "no")
		case p == "/redfish/v1/Systems/1/LogServices/SEL/Entries":
			bmctest.Fail(w, http.StatusNotImplemented, "no")
		case p == "/redfish/v1/Managers/1/LogServices":
			bmctest.Fail(w, http.StatusForbidden, "The account may not read the logs of the manager.")
		case p == "/redfish/v1/UpdateService/FirmwareInventory/BMC":
			bmctest.Fail(w, http.StatusInternalServerError, "internal error")
		case p == "/redfish/v1/Chassis" && chassis.Add(1) == 1:
			w.Header().Set("Retry-After", "2")
			bmctest.Fail(w, http.StatusTooManyRequests, "slow down")
		case p == "/redfish/v1/Systems/1/Memory/DIMM2":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<html><body>Please log in</body></html>")
		case p == "/redfish/v1/Systems/1/Memory/DIMM3":
			io.WriteString(w, `[1,2,3]`)
		default:
			return false
		}
		return true
	}})
	var pauses []time.Duration
	o := options(srv, "", "")
	o.Client.Sleep = func(_ context.Context, d time.Duration) error { pauses = append(pauses, d); return nil }
	m, dir := collect(t, o)
	if !m.Complete || !m.Partial() {
		t.Fatalf("complete %v, partial %v", m.Complete, m.Partial())
	}
	want := `service-root=ok
system=ok
memory=partial
memory-metrics=unsupported
bios=ok
bios-settings=unsupported
attribute-registry=ok
manager=ok
log-service=partial
log-entries=unsupported
firmware-inventory=partial
chassis=ok
`
	if got := states(m); got != want {
		t.Errorf("classes:\n%s\nwant:\n%s", got, want)
	}
	for uri, w := range map[string]struct {
		status string
		http   int
		detail string
	}{
		"/redfish/v1/Systems/1/Memory/DIMM1/MemoryMetrics": {StatusUnsupported, 404, "404 Not Found: not here"},
		"/redfish/v1/Systems/1/Bios/Settings":              {StatusUnsupported, 405, "405"},
		"/redfish/v1/Systems/1/LogServices/SEL/Entries":    {StatusUnsupported, 501, "501"},
		"/redfish/v1/Managers/1/LogServices":               {StatusDenied, 403, "may not read the logs"},
		"/redfish/v1/UpdateService/FirmwareInventory/BMC":  {StatusError, 500, "sent 4 times"},
		"/redfish/v1/Systems/1/Memory/DIMM2":               {StatusError, 200, "not a JSON object (text/html"},
		"/redfish/v1/Systems/1/Memory/DIMM3":               {StatusError, 200, "another kind of JSON value"},
		"/redfish/v1/UpdateService/FirmwareInventory/BIOS": {StatusOK, 200, ""},
		"/redfish/v1/Chassis":                              {StatusOK, 200, ""},
	} {
		r := resource(t, m, uri)
		if r.Status != w.status || r.HTTP != w.http || !strings.Contains(r.Detail, w.detail) {
			t.Errorf("%s: %s %d %q, want %s %d %q", uri, r.Status, r.HTTP, r.Detail, w.status, w.http, w.detail)
		}
		_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(uri, "/")), "index.json"))
		if (err == nil) != (w.status == StatusOK) {
			t.Errorf("%s: file on disk: %v", uri, err)
		}
	}
	if r := resource(t, m, "/redfish/v1/Chassis"); r.Attempts != 2 {
		t.Errorf("chassis after 429: %+v", r)
	}
	found := false
	for _, p := range pauses {
		if p == 2*time.Second {
			found = true
		}
	}
	if !found {
		t.Errorf("Retry-After 2 was not honoured: pauses %v", pauses)
	}
}

func TestSlowAndDyingBMC(t *testing.T) {
	t.Run("one resource stalls", func(t *testing.T) {
		srv := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(3, 2, false), Handler: func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/redfish/v1/Systems/1/Memory/DIMM2/MemoryMetrics" {
				bmctest.Stall(r)
				return true
			}
			return false
		}})
		o := options(srv, "", "")
		o.Client.Timeout, o.Client.Retries = 200*time.Millisecond, 1
		m, _ := collect(t, o)
		r := resource(t, m, "/redfish/v1/Systems/1/Memory/DIMM2/MemoryMetrics")
		if r.Status != StatusTimeout || r.Attempts != 2 || !m.Complete || !m.Partial() || state(m, ClassMetrics) != StatePartial {
			t.Errorf("stalled resource: %+v, complete %v\n%s", r, m.Complete, states(m))
		}
		if resource(t, m, "/redfish/v1/Chassis/1").Status != StatusOK {
			t.Errorf("the run did not go on after the stall")
		}
	})
	t.Run("the BMC dies in the middle", func(t *testing.T) {
		var n atomic.Int32
		srv := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(8, 2, false), Handler: func(w http.ResponseWriter, r *http.Request) bool {
			if n.Add(1) > 9 {
				bmctest.Drop(w)
				return true
			}
			return false
		}})
		o := options(srv, "", "")
		o.Client.Retries = 1
		m, dir := collect(t, o)
		if m.Complete || !strings.Contains(m.Stopped, "stopped answering after 9 resources") {
			t.Fatalf("complete %v, stopped %q", m.Complete, m.Stopped)
		}
		// What came back before is on disk, and the manifest lists the
		// three resources that got no answer.
		if m.Counts[StatusOK] != 9 || m.Counts[StatusError] != 3 {
			t.Errorf("counts = %v", m.Counts)
		}
		saved := 0
		for _, f := range files(t, dir) {
			if strings.HasSuffix(f, "/index.json") {
				saved++
			}
		}
		if saved != 9 {
			t.Errorf("%d files on disk, want 9", saved)
		}
		if state(m, ClassMemory) != StatePartial || state(m, ClassBios) != StateNotReached || state(m, ClassChassis) != StateNotReached {
			t.Errorf("classes:\n%s", states(m))
		}
		// 3 resources, each sent twice, and no request after that. The
		// HTTP library itself repeats a request once when a connection it
		// kept open turns out to be closed.
		if got := int(n.Load()); got < 9+6 || got > 9+6+3 {
			t.Errorf("the service saw %d requests, want 15 to 18", got)
		}
	})
	t.Run("the BMC is not there", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		dir := filepath.Join(t.TempDir(), "gone")
		m, err := Run(context.Background(), dir, Options{Client: client.Config{BaseURL: url, Insecure: true, Sleep: noSleep, Retries: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if m.Complete || !strings.Contains(m.Stopped, "the service root did not come back") || state(m, ClassRoot) != StatusError {
			t.Errorf("stopped %q\n%s", m.Stopped, states(m))
		}
		if got := files(t, dir); len(got) != 1 || got[0] != ManifestName {
			t.Errorf("files = %v", got)
		}
	})
	t.Run("time limit", func(t *testing.T) {
		srv := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(8, 2, false), Handler: func(w http.ResponseWriter, r *http.Request) bool {
			time.Sleep(60 * time.Millisecond)
			return false
		}})
		o := options(srv, "", "")
		o.Deadline = 400 * time.Millisecond
		m, _ := collect(t, o)
		if m.Complete || !strings.Contains(m.Stopped, "time limit of 400ms") || m.Counts[StatusOK] == 0 {
			t.Errorf("stopped %q, counts %v", m.Stopped, m.Counts)
		}
	})
	t.Run("interrupted", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		var n atomic.Int32
		srv := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(8, 2, false), User: testUser, Password: testPassword,
			Handler: func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method == http.MethodGet && n.Add(1) == 7 {
					cancel()
				}
				return false
			}})
		o := options(srv, testUser, testPassword)
		o.Client.Session = true
		dir := filepath.Join(t.TempDir(), "bmc")
		m, err := Run(ctx, dir, o)
		if err != nil {
			t.Fatal(err)
		}
		if m.Complete || m.Stopped != "the run was interrupted" {
			t.Errorf("stopped %q", m.Stopped)
		}
		// The session is closed although the run was stopped.
		if opened, closed, live := srv.SessionCounts(); opened != 1 || closed != 1 || live != 0 {
			t.Errorf("sessions: opened %d, closed %d, live %d", opened, closed, live)
		}
		if _, err := ReadManifest(dir); err != nil {
			t.Errorf("no manifest after an interrupt: %v", err)
		}
	})
}

func TestLargeAnswers(t *testing.T) {
	tree := bmctest.Machine(2, 2, false)
	tree["/redfish/v1/RegistryStore/en/BiosAttributeRegistryEX.json"] = `{"Id":"BiosAttributeRegistryEX.v1_0_0","Filler":"` + strings.Repeat("x", 6000) + `"}`
	srv := bmctest.New(t, empty(), bmctest.Options{Overlay: tree})
	o := options(srv, "", "")
	o.Client.MaxBody = 4000
	m, _ := collect(t, o)
	r := resource(t, m, "/redfish/v1/RegistryStore/en/BiosAttributeRegistryEX.json")
	if r.Status != StatusError || !strings.Contains(r.Detail, "larger than the 4000 bytes") || r.Attempts != 1 || !m.Complete {
		t.Errorf("large registry: %+v", r)
	}
	if state(m, ClassRegistry) != StatusError {
		t.Errorf("registry row = %s", state(m, ClassRegistry))
	}

	// A bound on the whole collection ends the run.
	o = options(srv, "", "")
	o.MaxTotalBytes = 3000
	m, dir := collect(t, o)
	if m.Complete || !strings.Contains(m.Stopped, "size limit of 3000 bytes") {
		t.Errorf("stopped %q", m.Stopped)
	}
	var size int64
	for _, f := range files(t, dir) {
		if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err == nil && f != ManifestName {
			size += fi.Size()
		}
	}
	if size > 3000 {
		t.Errorf("%d bytes written with a limit of 3000", size)
	}
}

func TestRedirectAndLinksToOtherHosts(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, `{"Id":"stolen"}`)
	}))
	defer other.Close()
	tree := bmctest.Machine(2, 2, false)
	// Links a service should not hand out: to another host, upwards, and
	// with names that are no file names.
	tree["/redfish/v1/Systems/1/Memory"] = fmt.Sprintf(`{"@odata.id":"/redfish/v1/Systems/1/Memory","Members":[
		{"@odata.id":"/redfish/v1/Systems/1/Memory/DIMM1"},
		{"@odata.id":%q},
		{"@odata.id":"/redfish/v1/Systems/1/Memory/../../../../../../etc/passwd"},
		{"@odata.id":"/redfish/v1/Systems/1/Memory/%%2e%%2e/%%2e%%2e/x"},
		{"@odata.id":"//%s/redfish/v1/x"},
		{"@odata.id":"file:///etc/passwd"},
		{"@odata.id":"/redfish/v1/Systems/1/Memory/a:b"},
		{"@odata.id":"/redfish/v1/Systems/1/Memory/%s"},
		{"@odata.id":"/redfish/v1/Systems/1/Memory/dimm1"},
		{"@odata.id":"/redfish/v1/Systems/1/Memory/Away"}]}`, other.URL+"/redfish/v1/Systems/1/Memory/DIMM9", strings.TrimPrefix(other.URL, "http://"), strings.Repeat("n", 300))
	tree["/redfish/v1/Systems/1/Memory/a:b"] = `{"Id":"a:b"}`
	tree["/redfish/v1/Systems/1/Memory/"+strings.Repeat("n", 300)] = `{"Id":"long"}`
	tree["/redfish/v1/Systems/1/Memory/dimm1"] = `{"Id":"dimm1"}`
	srv := bmctest.New(t, empty(), bmctest.Options{Overlay: tree, Handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/redfish/v1/Systems/1/Memory/Away" {
			http.Redirect(w, r, other.URL+"/redfish/v1/Systems/1/Memory/Away", http.StatusFound)
			return true
		}
		return false
	}})
	parent := t.TempDir()
	dir := filepath.Join(parent, "bmc")
	m, err := Run(context.Background(), dir, options(srv, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Errorf("the other host received %d requests", hits.Load())
	}
	if !m.Complete {
		t.Errorf("stopped %q", m.Stopped)
	}
	bad := 0
	for _, r := range m.Resources {
		if r.Class == ClassMemory && r.Status == StatusError {
			bad++
			if r.File != "" {
				t.Errorf("%s was written to %s", r.URI, r.File)
			}
		}
	}
	// On a file system that keeps case apart "dimm1" is a file of its own.
	if bad != 8 && bad != 9 {
		t.Errorf("%d members were refused, want 8 or 9\n%+v", bad, m.Resources)
	}
	if r := resource(t, m, "/redfish/v1/Systems/1/Memory/Away"); !strings.Contains(r.Detail, "redirects to another host") {
		t.Errorf("redirect: %+v", r)
	}
	// Nothing was written next to the collection.
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 {
		t.Errorf("the directory above the collection holds %d entries", len(entries))
	}
	for _, f := range files(t, dir) {
		if strings.Contains(f, "..") || strings.Contains(f, "passwd") {
			t.Errorf("file %s", f)
		}
	}
}

func TestSecretsAreNotWritten(t *testing.T) {
	tree := bmctest.Machine(2, 2, false)
	// A BMC that hands the account back: as an attribute, inside a
	// message, and in an error.
	tree["/redfish/v1/Systems/1/Bios"] = strings.Replace(tree["/redfish/v1/Systems/1/Bios"], `"AdminPassword":null`,
		`"AdminPassword":"`+testPassword+`","Hint":"the password is `+testPassword+`"`, 1)
	var token atomic.Value
	token.Store("")
	srv := bmctest.New(t, empty(), bmctest.Options{Overlay: tree, User: testUser, Password: testPassword, Handler: func(w http.ResponseWriter, r *http.Request) bool {
		if tok := r.Header.Get("X-Auth-Token"); tok != "" {
			token.Store(tok)
		}
		if r.URL.Path == "/redfish/v1/Chassis/1" {
			bmctest.Fail(w, http.StatusBadRequest, "request of "+r.Header.Get("X-Auth-Token")+" with "+testPassword)
			return true
		}
		if r.URL.Path == "/redfish/v1/Managers/1" {
			fmt.Fprintf(w, `{"@odata.id":"/redfish/v1/Managers/1","Id":"1","Echo":%q}`, "token "+r.Header.Get("X-Auth-Token"))
			return true
		}
		return false
	}})
	o := options(srv, testUser, testPassword)
	o.Client.Session = true
	m, dir := collect(t, o)
	tok := token.Load().(string)
	if tok == "" {
		t.Fatal("no session token seen")
	}
	leaks(t, dir, testPassword, tok)
	if m.SecretsRemoved < 3 {
		t.Errorf("secrets removed = %d", m.SecretsRemoved)
	}
	bios := readJSON(t, filepath.Join(dir, "redfish/v1/Systems/1/Bios/index.json"))
	if attrs := bios["Attributes"].(map[string]any); attrs["AdminPassword"] != "[removed]" || attrs["PatrolScrub"] != "Enabled" {
		t.Errorf("attributes on disk: %v", attrs)
	}
	if r := resource(t, m, "/redfish/v1/Chassis/1"); strings.Contains(r.Detail, tok) || strings.Contains(r.Detail, testPassword) || r.Status != StatusError {
		t.Errorf("error detail: %+v", r)
	}
}

func TestTLS(t *testing.T) {
	srv := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(2, 2, false), TLS: true})
	sum := sha256.Sum256(srv.Certificate().Raw)
	fingerprint := client.FormatFingerprint(sum[:])

	dir := filepath.Join(t.TempDir(), "bmc")
	m, err := Run(context.Background(), dir, Options{Client: client.Config{BaseURL: srv.URL, Sleep: noSleep}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Complete || !strings.Contains(m.Stopped, "not trusted") || !strings.Contains(m.Stopped, fingerprint) || m.TLS != "verified" {
		t.Errorf("stopped %q, tls %s", m.Stopped, m.TLS)
	}
	if len(srv.Requests()) != 0 {
		t.Errorf("requests reached a service whose certificate was refused: %v", srv.Requests())
	}

	dir = filepath.Join(t.TempDir(), "bmc")
	m, err = Run(context.Background(), dir, Options{Client: client.Config{BaseURL: srv.URL, Fingerprint: fingerprint, Sleep: noSleep}})
	if err != nil || !m.Complete || m.TLS != "fingerprint" || m.Failed() != 0 {
		t.Errorf("with the fingerprint: %v, stopped %q, tls %s", err, m.Stopped, m.TLS)
	}
}

func TestSettingsThatAreWrong(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bmc")
	for _, cfg := range []client.Config{
		{BaseURL: ""},
		{BaseURL: "http://10.0.0.5"},
		{BaseURL: "https://user:pw@10.0.0.5"},
		{BaseURL: "https://10.0.0.5", Session: true},
	} {
		if _, err := Run(context.Background(), dir, Options{Client: cfg}); err == nil {
			t.Errorf("Run(%+v) succeeded", cfg)
		}
	}
	if _, err := os.Stat(dir); err == nil {
		t.Errorf("a directory was made for a run that could not start")
	}
}

func TestFilePath(t *testing.T) {
	for uri, want := range map[string]string{
		"/redfish/v1/":                              "redfish/v1/index.json",
		"/redfish/v1":                               "redfish/v1/index.json",
		"/redfish/v1/Systems/1/Memory/DIMM%201":     "redfish/v1/Systems/1/Memory/DIMM%201/index.json",
		"/redfish/v1/Registries/Bios.v1_0_0.json":   "redfish/v1/Registries/Bios.v1_0_0.json/index.json",
		"/redfish/v1/Systems/1/Entries?$skip=5#x":   "redfish/v1/Systems/1/Entries/index.json",
		"/registries/BiosAttributeRegistry.v1.json": "registries/BiosAttributeRegistry.v1.json/index.json",
		// What a file name cannot hold is kept in its escaped form.
		"/a/b|c":   "a/b%7Cc/index.json",
		`/a/"b"`:   "a/%22b%22/index.json",
		"/a/b c/d": "a/b%20c/d/index.json",
	} {
		got, err := filePath(uri)
		if err != nil || got != want {
			t.Errorf("filePath(%q) = %q, %v, want %q", uri, got, err, want)
		}
	}
	for _, uri := range []string{"", "/", "/a/../b", "/a/./b", "/a//b", "/a/%2e%2e/b", "/a/b%2fc", "/a/b%5cc", "/a/b:c", "/a/b*c",
		"/a/" + strings.Repeat("x", 300), strings.Repeat("/a", 40), "/a/b\x01c", "/a/%00"} {
		if got, err := filePath(uri); err == nil {
			t.Errorf("filePath(%q) = %q, want an error", uri, got)
		}
	}
}

func FuzzFilePath(f *testing.F) {
	for _, s := range []string{"/redfish/v1/Systems/1", "/a/../b", "/a/%2e%2e/b", "/a/b%2fc", "//x", "/a\\b", "/a/b?c#d", "/%"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, uri string) {
		name, err := filePath(uri)
		if err != nil {
			return
		}
		if !strings.HasSuffix(name, "/index.json") || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
			t.Fatalf("filePath(%q) = %q", uri, name)
		}
		for _, seg := range strings.Split(name, "/") {
			if seg == "" || seg == "." || seg == ".." {
				t.Fatalf("filePath(%q) = %q", uri, name)
			}
		}
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			t.Fatalf("filePath(%q) = %q leaves the collection", uri, name)
		}
	})
}

func FuzzParseManifest(f *testing.F) {
	f.Add([]byte(`{"format":"my-redfish-collection","version":1,"bmc":{"address":"10.0.0.5"},"resources":[{"uri":"/redfish/v1/","class":"service-root","status":"ok"}]}`))
	f.Add([]byte(`{"format":"other"}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := ParseManifest(data)
		if err != nil {
			return
		}
		m.summarize(map[string]bool{}, map[string]bool{})
		NewSupport([]*Manifest{m})
		m.Partial()
	})
}

func TestSupportTable(t *testing.T) {
	root := t.TempDir()
	full := bmctest.New(t, empty(), bmctest.Options{Overlay: bmctest.Machine(4, 2, false)})
	o := options(full, "", "")
	o.Name = "node-a"
	if _, err := Run(context.Background(), filepath.Join(root, "node-a"), o); err != nil {
		t.Fatal(err)
	}
	dmtf := bmctest.New(t, os.DirFS(testutil.Testdata(t, "redfish", "dmtf-rackmount1")), bmctest.Options{})
	o = options(dmtf, "", "")
	o.Name = "node-b"
	if _, err := Run(context.Background(), filepath.Join(root, "node-b"), o); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("not a collection"), 0o600); err != nil {
		t.Fatal(err)
	}

	ms, err := FindManifests(root)
	if err != nil || len(ms) != 2 {
		t.Fatalf("FindManifests: %d, %v", len(ms), err)
	}
	// The same collections named one by one, and twice.
	again, err := FindManifests(filepath.Join(root, "node-b"), filepath.Join(root, "node-a", ManifestName), filepath.Join(root, "node-a"))
	if err != nil || len(again) != 2 {
		t.Fatalf("FindManifests by name: %d, %v", len(again), err)
	}
	// A third BMC of the same machine and firmware as node-a.
	o = options(full, "", "")
	o.Name = "node-c"
	twin, err := Run(context.Background(), filepath.Join(root, "node-c"), o)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSupport(append(again, twin))
	if len(s.Groups) != 2 || len(s.Classes) != len(Classes) {
		t.Fatalf("support: %+v", s.Groups)
	}
	a, b := s.Groups[0], s.Groups[1]
	if len(a.BMCs) != 2 || a.BMCs[0].Name != "node-a" || a.BMCs[1].Name != "node-c" || len(b.BMCs) != 1 || b.BMCs[0].Name != "node-b" {
		t.Errorf("groups: %+v | %+v", a.BMCs, b.BMCs)
	}
	if a.Vendor != "Example" || a.Firmware != "2.14.0" || a.BiosVersion != "1.4.2" || b.Model != "3500" || b.Vendor != "Contoso" {
		t.Errorf("groups: %+v | %+v", a, b)
	}
	for class, want := range map[string][2]string{
		ClassRoot:       {StateOK, StateOK},
		ClassMemory:     {StateOK, StateOK},
		ClassMetrics:    {StateOK, StateNotOffered},
		ClassRegistry:   {StateOK, StatusUnsupported},
		ClassLogEntries: {StateOK, StateOK},
	} {
		if got := [2]string{a.State(class), b.State(class)}; got != want {
			t.Errorf("%s: %v, want %v", class, got, want)
		}
	}

	if _, err := FindManifests(t.TempDir()); err == nil || !strings.Contains(err.Error(), "my collect redfish") {
		t.Errorf("empty directory: %v", err)
	}
	if _, err := FindManifests(filepath.Join(root, "missing")); err == nil {
		t.Errorf("missing directory: no error")
	}
	if _, err := ParseManifest([]byte(`{"format":"my-redfish-collection","version":99}`)); err == nil {
		t.Errorf("a manifest of a later version was read")
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.5":               "10.0.0.5",
		"bmc.example.net":        "bmc.example.net",
		"[2001:db8::5]:8443":     "2001_db8__5__8443",
		"../../etc":              "etc",
		"":                       "bmc",
		"...":                    "bmc",
		"rack 12 / node 3":       "rack_12___node_3",
		strings.Repeat("a", 300): strings.Repeat("a", 100),
	} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// Two URIs whose paths differ only in the case of a directory: on a file
// system that ignores case the second would land in the directory of the
// first, and the manifest would name a file a copy elsewhere does not
// have. The second is refused, on every file system alike, and every file
// on disk is the one its name says.
func TestPathsThatDifferOnlyInCase(t *testing.T) {
	dir := t.TempDir()
	tr, err := openTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.close()
	if _, err := tr.write("/redfish/v1", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if name, err := tr.write("/redfisH/v1/Managers", []byte(`{"a":1}`)); err == nil || !strings.Contains(err.Error(), "differs from redfish only in case") {
		t.Errorf("a directory in another case: %q, %v", name, err)
	}
	if name, err := tr.write("/redfish/V1/Systems", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "redfish/V1 differs from redfish/v1") {
		t.Errorf("a deeper directory in another case: %q, %v", name, err)
	}
	if name, err := tr.write("/redfish/v1/Managers", []byte(`{"b":2}`)); err != nil || name != "redfish/v1/Managers/index.json" {
		t.Fatalf("the same directory in the same case: %q, %v", name, err)
	}
	var files []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)
	if want := []string{"redfish/v1/Managers/index.json", "redfish/v1/index.json"}; !reflect.DeepEqual(files, want) {
		t.Errorf("files %v, want %v", files, want)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "redfish", "v1", "Managers", "index.json")); string(b) != `{"b":2}` {
		t.Errorf("the file holds %s", b)
	}
}
