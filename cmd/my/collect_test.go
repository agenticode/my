package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/redfish/bmctest"
	"github.com/agenticode/my/internal/redfish/collect"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/testutil"
)

const (
	bmcUser     = "reader"
	bmcPassword = "bmc-Pa55word-for-tests"
)

// account is the environment a collection reads its account from.
func account() map[string]string {
	return map[string]string{"MY_NOW": "2026-11-16T03:00:00Z", envRedfishUser: bmcUser, envRedfishPassword: bmcPassword}
}

// treeFiles lists the files below dir.
func treeFiles(t *testing.T, dir string) []string {
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
	return out
}

// noSecret fails when the password is in what a command printed or wrote.
func noSecret(t *testing.T, dir string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if strings.Contains(text, bmcPassword) {
			t.Errorf("the password was printed:\n%s", text)
		}
	}
	if dir == "" {
		return
	}
	for _, f := range treeFiles(t, dir) {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(bmcPassword)) {
			t.Errorf("%s holds the password", f)
		}
	}
}

func TestCollectRedfishToDirectory(t *testing.T) {
	srv := bmctest.New(t, fstest.MapFS{}, bmctest.Options{Overlay: bmctest.Machine(4, 6, false), User: bmcUser, Password: bmcPassword})
	out := filepath.Join(t.TempDir(), "bmc")
	code, stdout, stderr := runEnv(account(), "collect", "redfish", "--bmc", srv.URL, "--insecure", "-o", out)
	if code != cli.ExitOK {
		t.Fatalf("exit code %d\n%s\n%s", code, stdout, stderr)
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	wantLines(t, stdout,
		host+": 34 resources read in 0s (34 requests, ",
		"  Example EX-2000, BIOS 1.4.2, BMC firmware 2.14.0, Redfish 1.17.0",
		"  logs left out: Journal, AuditLog, HostLogger. They hold no hardware events; --log NAME reads one",
		"collection written: "+out,
		"read it as a case: my import "+out,
		"which resources each BMC gave: my collect support "+out)
	// --insecure is said once, and nothing else goes to the error stream.
	if strings.Count(stderr, "warning: --insecure") != 1 || strings.Count(stderr, "\n") != 2 {
		t.Errorf("stderr:\n%s", stderr)
	}
	wantLines(t, stderr, "25 resources read")
	if w := srv.Writes(); len(w) != 0 {
		t.Errorf("requests that write: %v", w)
	}
	m, err := collect.ReadManifest(out)
	if err != nil || !m.Complete || m.Auth != "basic" || m.TLS != "none" || m.BMC.Name != host || !strings.HasPrefix(m.Tool, "my ") {
		t.Fatalf("manifest: %v, %+v", err, m)
	}
	if m.StartedAt.Format("2006-01-02T15:04:05Z") != "2026-11-16T03:00:00Z" {
		t.Errorf("started at %s", m.StartedAt)
	}
	noSecret(t, out, stdout, stderr)

	// The collection is an input of "my import": the six entries of the
	// event log are corrected errors of DIMM_A1, and the exit code is the
	// finding for the host.
	code, stdout, stderr = runEnv(fixed, "import", out)
	if code != 1 || !strings.Contains(stdout, ": Redfish dump") || !strings.Contains(stdout, "host node-a") || !strings.Contains(stdout, "DIMM_A1") {
		t.Errorf("import of the collection: exit code %d\n%s\n%s", code, stdout, stderr)
	}

	// A directory that holds something is not written into.
	code, _, stderr = runEnv(account(), "collect", "redfish", "--bmc", srv.URL, "--insecure", "-o", out)
	if code != cli.ExitUsage || !strings.Contains(stderr, "it is not empty; a collection goes into a new directory") {
		t.Errorf("existing collection: exit code %d, %s", code, stderr)
	}
}

func TestCollectRedfishAsJSONAndQuiet(t *testing.T) {
	srv := bmctest.New(t, fstest.MapFS{}, bmctest.Options{Overlay: bmctest.Machine(2, 2, false)})
	out := filepath.Join(t.TempDir(), "bmc")
	code, stdout, stderr := runEnv(fixed, "collect", "redfish", "--bmc", srv.URL, "--insecure", "-o", out, "--json", "--quiet", "--no-logs")
	if code != cli.ExitOK || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("exit code %d, stderr %q", code, stderr)
	}
	var doc struct {
		Out, Kind   string
		ExitCode    int `json:"exit_code"`
		Collections []struct {
			Name, Address, Dir string
			Manifest           collect.Manifest
		}
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, stdout)
	}
	if doc.Kind != "directory" || doc.Out != out || doc.ExitCode != 0 || len(doc.Collections) != 1 || doc.Collections[0].Dir != out ||
		!doc.Collections[0].Manifest.Complete || doc.Collections[0].Manifest.Class(collect.ClassLogEntries).State != collect.StatusSkipped {
		t.Errorf("document: %+v", doc)
	}
}

func TestCollectRedfishToCase(t *testing.T) {
	srv := bmctest.New(t, os.DirFS(testutil.Testdata(t, "redfish", "dmtf-rackmount1")), bmctest.Options{User: bmcUser, Password: bmcPassword, SessionOnly: true})
	file := filepath.Join(t.TempDir(), "bmc.mycase")
	pw := filepath.Join(t.TempDir(), "pw")
	if err := os.WriteFile(pw, []byte(bmcPassword+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runEnv(fixed, "collect", "redfish", "--bmc", srv.URL, "--insecure", "--session", "--user", bmcUser, "--password-file", pw, "-o", file, "--quiet")
	if code != cli.ExitOK {
		t.Fatalf("exit code %d\n%s\n%s", code, stdout, stderr)
	}
	wantLines(t, stdout, "case bundle written: "+file+" (", "  1 host: web483", "look at it: my open "+file)
	if opened, closed, live := srv.SessionCounts(); opened != 1 || closed != 1 || live != 0 {
		t.Errorf("sessions: opened %d, closed %d, live %d", opened, closed, live)
	}
	noSecret(t, "", stdout, stderr)
	data, err := os.ReadFile(file)
	if err != nil || bytes.Contains(data, []byte(bmcPassword)) {
		t.Fatalf("bundle: %v", err)
	}
	// The case opens, and its records name the BMC and no directory of
	// the machine that collected.
	code, stdout, stderr = runEnv(fixed, "open", file, "--json")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("open: exit code %d, %s", code, stderr)
	}
	if !strings.Contains(stdout, `"host": "web483"`) || strings.Contains(stdout, "my-collect-") || strings.Contains(stdout, os.TempDir()) {
		t.Errorf("the case as JSON:\n%s", stdout)
	}

	// An existing bundle is replaced only when asked.
	code, _, stderr = runEnv(fixed, "collect", "redfish", "--bmc", srv.URL, "--insecure", "--session", "--user", bmcUser, "--password-file", pw, "-o", file)
	if code != cli.ExitUsage || !strings.Contains(stderr, "exists; --force replaces it") {
		t.Errorf("existing bundle: exit code %d, %s", code, stderr)
	}
	if n := len(srv.Requests()); n == 0 {
		t.Fatal("no requests")
	}
	before := len(srv.Requests())
	code, _, _ = runEnv(fixed, "collect", "redfish", "--bmc", srv.URL, "--insecure", "--session", "--user", bmcUser, "--password-file", pw, "-o", file, "--force", "--quiet")
	if code != cli.ExitOK || len(srv.Requests()) == before {
		t.Errorf("--force: exit code %d", code)
	}
}

func TestCollectSeveralBMCsAndSupport(t *testing.T) {
	a := bmctest.New(t, fstest.MapFS{}, bmctest.Options{Overlay: bmctest.Machine(4, 3, false), User: bmcUser, Password: bmcPassword})
	b := bmctest.New(t, os.DirFS(testutil.Testdata(t, "redfish", "dmtf-rackmount1")), bmctest.Options{User: "other", Password: "another-Pa55word"})
	c := bmctest.New(t, fstest.MapFS{}, bmctest.Options{Overlay: bmctest.Machine(8, 3, false), User: bmcUser, Password: bmcPassword})
	dir := t.TempDir()
	otherPW := filepath.Join(dir, "other.pw")
	if err := os.WriteFile(otherPW, []byte("another-Pa55word"), 0o600); err != nil {
		t.Fatal(err)
	}
	targets := filepath.Join(dir, "targets")
	text := fmt.Sprintf("# rack 12\n%s name=node-a\n\n%s name=node-b user=other password-file=%s\n%s name=node-c\n", a.URL, b.URL, otherPW, c.URL)
	if err := os.WriteFile(targets, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "fleet")
	code, stdout, stderr := runEnv(account(), "collect", "redfish", "--targets", targets, "--insecure", "-o", out, "--parallel", "2", "--quiet")
	if code != cli.ExitOK {
		t.Fatalf("exit code %d\n%s\n%s", code, stdout, stderr)
	}
	wantLines(t, stdout, "node-a (127.0.0.1): ", "node-b (127.0.0.1): ", "node-c (127.0.0.1): ",
		"  unsupported: attribute registry", "  not offered: memory metrics",
		"read it as a case: my import "+filepath.Join(out, "node-a"))
	for _, name := range []string{"node-a", "node-b", "node-c"} {
		if _, err := collect.ReadManifest(filepath.Join(out, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	noSecret(t, out, stdout, stderr)
	for _, f := range treeFiles(t, out) {
		data, _ := os.ReadFile(filepath.Join(out, filepath.FromSlash(f)))
		if bytes.Contains(data, []byte("another-Pa55word")) {
			t.Errorf("%s holds the password of node-b", f)
		}
	}

	// The support table: node-a and node-c are one machine and firmware.
	code, stdout, stderr = runEnv(fixed, "collect", "support", out)
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("support: exit code %d, %s", code, stderr)
	}
	testutil.Golden(t, "collect-support.txt", []byte(stdout))
	code, stdout, _ = runEnv(fixed, "collect", "support", out, "--lang", "ko", "--width", "70")
	if code != cli.ExitOK {
		t.Fatalf("support --lang ko: exit code %d", code)
	}
	testutil.Golden(t, "collect-support.ko.txt", []byte(stdout))
	code, stdout, _ = runEnv(fixed, "collect", "support", filepath.Join(out, "node-b"), "--json")
	var table collect.Support
	if err := json.Unmarshal([]byte(stdout), &table); err != nil || code != cli.ExitOK {
		t.Fatalf("support --json: %v\n%s", err, stdout)
	}
	if len(table.Groups) != 1 || table.Groups[0].State(collect.ClassRegistry) != collect.StatusUnsupported || table.Groups[0].BMCs[0].Name != "node-b" {
		t.Errorf("table: %+v", table)
	}
	if code, _, stderr := runEnv(fixed, "collect", "support", dir); code != cli.ExitFailure || !strings.Contains(stderr, "no manifest.json here or one directory below") {
		t.Errorf("support of a directory without collections: exit code %d, %s", code, stderr)
	}
}

func TestCollectExitCodes(t *testing.T) {
	// Something was denied: the collection is written, and the exit code
	// says that it lacks something.
	denied := bmctest.New(t, fstest.MapFS{}, bmctest.Options{Overlay: bmctest.Machine(2, 2, false), Handler: func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasPrefix(r.URL.Path, "/redfish/v1/Systems/1/Bios") {
			bmctest.Fail(w, http.StatusForbidden, "The account may not read the BIOS settings.")
			return true
		}
		return false
	}})
	out := filepath.Join(t.TempDir(), "bmc")
	code, stdout, stderr := runEnv(fixed, "collect", "redfish", "--bmc", denied.URL, "--insecure", "-o", out)
	if code != exitPartial {
		t.Fatalf("exit code %d\n%s\n%s", code, stdout, stderr)
	}
	wantLines(t, stdout, "  denied: BIOS settings", "collection written: "+out)
	wantLines(t, stderr, "/redfish/v1/Systems/1/Bios: denied: 403 Forbidden: The account may not read the BIOS settings.")

	// A wrong password: one request with the account, then nothing.
	guarded := bmctest.New(t, fstest.MapFS{}, bmctest.Options{Overlay: bmctest.Machine(2, 2, false), User: bmcUser, Password: bmcPassword})
	out = filepath.Join(t.TempDir(), "bmc")
	env := account()
	env[envRedfishPassword] = "not-the-password"
	code, stdout, stderr = runEnv(env, "collect", "redfish", "--bmc", guarded.URL, "--insecure", "-o", out)
	if code != exitPartial || len(guarded.Requests()) != 2 {
		t.Errorf("wrong password: exit code %d, requests %v", code, guarded.Requests())
	}
	wantLines(t, stdout, "stopped early: the BMC refused the account (401 for /redfish/v1/Systems)")
	if strings.Contains(stdout+stderr, "not-the-password") {
		t.Errorf("the wrong password was printed")
	}

	// No BMC at the address.
	gone := bmctest.New(t, fstest.MapFS{}, bmctest.Options{})
	url := gone.URL
	gone.Close()
	out = filepath.Join(t.TempDir(), "bmc")
	code, stdout, stderr = runEnv(fixed, "collect", "redfish", "--bmc", url, "--insecure", "-o", out, "--retries", "0", "--quiet")
	if code != cli.ExitFailure || !strings.Contains(stderr, "no BMC answered: nothing was collected") {
		t.Errorf("no BMC: exit code %d\n%s\n%s", code, stdout, stderr)
	}
	wantLines(t, stdout, "stopped early: the service root did not come back")

	// A certificate nobody vouches for: refused, with its fingerprint.
	tls := bmctest.New(t, fstest.MapFS{}, bmctest.Options{Overlay: bmctest.Machine(2, 2, false), TLS: true})
	out = filepath.Join(t.TempDir(), "bmc")
	code, stdout, _ = runEnv(fixed, "collect", "redfish", "--bmc", tls.URL, "-o", out, "--quiet")
	if code != cli.ExitFailure || len(tls.Requests()) != 0 {
		t.Errorf("untrusted certificate: exit code %d, requests %v", code, tls.Requests())
	}
	m := regexp.MustCompile(`Its SHA-256 fingerprint is ([0-9A-F:]{95})`).FindStringSubmatch(strings.Join(strings.Fields(stdout), " "))
	if m == nil {
		t.Fatalf("no fingerprint in:\n%s", stdout)
	}
	// Named by that fingerprint it is accepted.
	out = filepath.Join(t.TempDir(), "bmc")
	if code, stdout, stderr = runEnv(fixed, "collect", "redfish", "--bmc", tls.URL, "--fingerprint", m[1], "-o", out, "--quiet"); code != cli.ExitOK || stderr != "" {
		t.Errorf("with --fingerprint: exit code %d\n%s\n%s", code, stdout, stderr)
	}
	if mf, err := collect.ReadManifest(out); err != nil || mf.TLS != "fingerprint" {
		t.Errorf("manifest: %v", err)
	}
}

func TestCollectUsage(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	file := filepath.Join(dir, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	targets := func(text string) string {
		p := filepath.Join(t.TempDir(), "targets")
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tt := range []struct {
		env  map[string]string
		args []string
		want string
	}{
		{nil, []string{"collect"}, "name what to collect"},
		{nil, []string{"collect", "ipmi"}, `unknown source "ipmi"`},
		{nil, []string{"collect", "redfish", "extra", "--bmc", "10.0.0.5", "-o", out}, `unexpected argument "extra"`},
		{nil, []string{"collect", "redfish", "-o", out}, "name the BMC with --bmc ADDRESS"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "--targets", "x", "-o", out}, "give one of the two"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5"}, "name where the answers go with -o"},
		{nil, []string{"collect", "redfish", "--bmc", "ftp://10.0.0.5", "-o", out}, `scheme "ftp"`},
		{nil, []string{"collect", "redfish", "--bmc", "https://reader:" + bmcPassword + "@10.0.0.5", "-o", out}, "carries credentials"},
		{nil, []string{"collect", "redfish", "--bmc", "http://10.0.0.5", "-o", out}, "plain http sends the password unencrypted"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", file}, "it is a file"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", filepath.Join(dir, "no", "such", "dir")}, "does not exist"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", out, "--parallel", "0"}, "--parallel 0"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", out, "--timeout", "soon"}, "--timeout soon"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", out, "--deadline", "1ms"}, "--deadline 1ms"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", out, "--retries", "99"}, "--retries 99"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", out, "--max-log-entries", "0"}, "--no-logs reads no logs"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", out, "--insecure", "--fingerprint", "ab"}, "three ways to treat the certificate"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", out, "--fingerprint", "ab"}, "64 hexadecimal digits"},
		// The account: a user without a password and no terminal to ask on,
		// and a password without a user.
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", out, "--user", "reader"}, "no password for user reader"},
		{map[string]string{envRedfishPassword: bmcPassword}, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", out}, "a password is given and no user"},
		{nil, []string{"collect", "redfish", "--bmc", "10.0.0.5", "-o", out, "--user", "reader", "--password-file", filepath.Join(dir, "missing")}, "--password-file"},
		{nil, []string{"collect", "redfish", "--targets", filepath.Join(dir, "missing"), "-o", out}, "--targets"},
		{nil, []string{"collect", "redfish", "--targets", targets("# nothing\n"), "-o", out}, "names no BMC"},
		{nil, []string{"collect", "redfish", "--targets", targets("10.0.0.5 password=" + bmcPassword + "\n"), "-o", out}, "a password does not go into this file"},
		{nil, []string{"collect", "redfish", "--targets", targets("10.0.0.5 colour=red\n"), "-o", out}, `unknown setting "colour"`},
		{nil, []string{"collect", "redfish", "--targets", targets("10.0.0.5\nhttps://10.0.0.5/\n"), "-o", out}, "would both be written to the directory"},
		{nil, []string{"collect", "redfish", "--targets", targets("10.0.0.5\nnot a url\n"), "-o", out}, "line 2"},
		{nil, []string{"collect", "support"}, "name the collections to compare"},
	} {
		code, stdout, stderr := runEnv(tt.env, tt.args...)
		if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: exit code %d, stdout %q, stderr %q, want %q", tt.args, code, stdout, stderr, tt.want)
		}
		if strings.Contains(stderr, bmcPassword) {
			t.Errorf("%v: the error repeats the password: %s", tt.args, stderr)
		}
	}
	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a wrong command line left %s behind", out)
	}
}

func TestCollectHelp(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
		code, out, errOut := run("help", "collect", "--lang", string(lang))
		if code != cli.ExitOK || errOut != "" {
			t.Fatalf("exit code %d, stderr %q", code, errOut)
		}
		flat := strings.Join(strings.Fields(out), " ")
		if lang == i18n.EN {
			wantLines(t, flat, "Usage: my collect redfish --bmc ADDRESS -o OUT [flags]", "It only reads. Every request is a GET",
				"these are the only two requests that are no GET", "It is never given on the command line",
				"The exit code is 0 when everything that was asked for came back, 1 when")
		} else if !strings.Contains(out, "사용법:") || !strings.Contains(out, "옵션:") || !strings.Contains(out, "읽기만 합니다") {
			t.Errorf("not Korean:\n%s", out)
		}
		// Every flag of the command is in its help, and the help names no
		// flag the command does not take.
		known := map[string]bool{"--help": true}
		newCollectOptions().fs.VisitAll(func(f *flag.Flag) {
			if f.Name == "h" || f.Name == "help" {
				return
			}
			name := "--" + f.Name
			if len(f.Name) == 1 {
				name = "-" + f.Name
			}
			known[name] = true
			if !strings.Contains(out, name+" ") {
				t.Errorf("help of collect (%s) lacks %s", lang, name)
			}
		})
		for _, f := range flagRE.FindAllString(out, -1) {
			if !known[f] {
				t.Errorf("help of collect (%s) names %s, which the command does not take", lang, f)
			}
		}
		for _, line := range strings.Split(out, "\n") {
			if term.Width(line) > term.DefaultWidth {
				t.Errorf("help of collect (%s): line wider than %d columns: %q", lang, term.DefaultWidth, line)
			}
		}
	}
	if _, out, _ := run("help"); !strings.Contains(out, "collect ") {
		t.Errorf("the command list lacks collect:\n%s", out)
	}
	// Every text of the command exists in both languages.
	for code, m := range collectWords {
		if m.EN == "" || m.KO == "" {
			t.Errorf("%s lacks a language", code)
		}
	}
}
