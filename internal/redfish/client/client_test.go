package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The account of the tests. The password is long enough to be taken out of
// text wherever it occurs.
const (
	testUser     = "reader"
	testPassword = "s3cret-Pa55word!"
)

// noSleep records the pauses a client asked for and takes none.
type noSleep struct {
	mu     sync.Mutex
	pauses []time.Duration
}

func (s *noSleep) sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pauses = append(s.pauses, d)
	return nil
}

func (s *noSleep) all() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration{}, s.pauses...)
}

func newClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// plain returns a client for a test server that speaks HTTP.
func plain(t *testing.T, srv *httptest.Server, cfg Config) *Client {
	t.Helper()
	cfg.BaseURL, cfg.Insecure = srv.URL, true
	return newClient(t, cfg)
}

func kindOf(err error) Kind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return -1
}

func TestParseTarget(t *testing.T) {
	good := map[string]string{
		"10.0.0.5":                       "https://10.0.0.5",
		"10.0.0.5:8443":                  "https://10.0.0.5:8443",
		"https://BMC.example.net":        "https://bmc.example.net",
		"https://bmc.example.net/":       "https://bmc.example.net",
		"https://10.0.0.5/redfish/v1/":   "https://10.0.0.5",
		"http://127.0.0.1:8000":          "http://127.0.0.1:8000",
		"[2001:db8::5]:8443":             "https://[2001:db8::5]:8443",
		"  https://10.0.0.5/redfish  \n": "https://10.0.0.5",
	}
	for in, want := range good {
		u, err := ParseTarget(in)
		if err != nil {
			t.Errorf("ParseTarget(%q): %v", in, err)
			continue
		}
		if u.String() != want {
			t.Errorf("ParseTarget(%q) = %s, want %s", in, u, want)
		}
	}
	bad := []string{
		"", "   ", "https://", "ftp://10.0.0.5", "https://10.0.0.5/redfish/v1/Systems", "https://10.0.0.5?x=1",
		"https://10.0.0.5#frag", "10.0.0.5:99999", "10.0.0.5:0", "two words", "https://10.0.0.5/..",
		"https://reader:" + testPassword + "@10.0.0.5",
	}
	for _, in := range bad {
		u, err := ParseTarget(in)
		if err == nil {
			t.Errorf("ParseTarget(%q) = %s, want an error", in, u)
			continue
		}
		if strings.Contains(err.Error(), testPassword) {
			t.Errorf("ParseTarget(%q): the error repeats the password: %v", in, err)
		}
	}
}

// Whatever is asked of the transport, only a GET reaches the BMC.
func TestOnlyGETReachesTheBMC(t *testing.T) {
	var others atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			others.Add(1)
		}
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()
	c := plain(t, srv, Config{User: testUser, Password: testPassword})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead,
		http.MethodOptions, "TRACE", "CONNECT", "PROPFIND"} {
		for _, path := range []string{"/redfish/v1/Systems/1", "/redfish/v1/Systems/1/Bios/Settings",
			"/redfish/v1/Systems/1/LogServices/SEL/Actions/LogService.ClearLog", DefaultSessions} {
			req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(`{"Attributes":{}}`))
			if err != nil {
				t.Fatal(err)
			}
			res, err := c.hc.Do(req)
			if err == nil {
				res.Body.Close()
				t.Errorf("%s %s went through the transport", method, path)
				continue
			}
			var ref *refusal
			if !errors.As(err, &ref) {
				t.Errorf("%s %s: error %v, want a refusal of the client", method, path, err)
			}
		}
	}
	if n := others.Load(); n != 0 {
		t.Errorf("%d requests other than GET reached the server", n)
	}
	if _, err := c.Get(context.Background(), "/redfish/v1/"); err != nil {
		t.Errorf("GET: %v", err)
	}
}

func TestRequestsToOtherHostsAreRefused(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redfish/v1":
			http.Redirect(w, r, "/redfish/v1/", http.StatusMovedPermanently)
		case "/redfish/v1/":
			io.WriteString(w, `{"Id":"RootService"}`)
		case "/away":
			http.Redirect(w, r, other.URL+"/redfish/v1/", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		}
	}))
	defer srv.Close()
	c := plain(t, srv, Config{User: testUser, Password: testPassword})
	ctx := context.Background()

	// A redirect that stays on the BMC is followed.
	res, err := c.Get(ctx, "/redfish/v1")
	if err != nil || !bytes.Contains(res.Body, []byte("RootService")) {
		t.Fatalf("redirect on the same host: %v", err)
	}
	// One that leaves it is not, and the other host sees nothing.
	if _, err := c.Get(ctx, "/away"); kindOf(err) != KindRefused {
		t.Errorf("redirect to another host: %v, want a refusal", err)
	}
	if _, err := c.Get(ctx, "/loop"); kindOf(err) != KindRefused {
		t.Errorf("redirect loop: %v, want a refusal", err)
	}
	// A reference in a resource that names another host is not followed.
	for _, ref := range []string{other.URL + "/redfish/v1/", "//" + strings.TrimPrefix(other.URL, "http://") + "/x",
		"https://reader@evil.example/x", "file:///etc/passwd", "redfish/v1", "/redfish/v1/../../etc/passwd",
		"/redfish/v1/%2e%2e/x", "/redfish/v1/a\x00b", "/redfish\\v1", "", strings.Repeat("/a", 2000)} {
		if _, err := c.Get(ctx, ref); kindOf(err) != KindRefused {
			t.Errorf("Get(%q): %v, want a refusal", clip(ref, 40), err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the other host received %d requests", n)
	}
	// A URL on the BMC itself is fine, with a query and without its fragment.
	uri, err := c.Resolve(srv.URL + "/redfish/v1/Systems/1/LogServices/SEL/Entries?$skip=50#/Members/3")
	if err != nil || uri != "/redfish/v1/Systems/1/LogServices/SEL/Entries?$skip=50" {
		t.Errorf("Resolve = %q, %v", uri, err)
	}
}

func TestBasicAuthAndHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		u, p, ok := r.BasicAuth()
		if !ok || u != testUser || p != testPassword {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		io.WriteString(w, `{"Id":"1"}`)
	}))
	defer srv.Close()

	c := plain(t, srv, Config{User: testUser, Password: testPassword, UserAgent: "my test"})
	res, err := c.Get(context.Background(), "/redfish/v1/Systems/1")
	if err != nil {
		t.Fatal(err)
	}
	if res.ContentType != "application/json" || res.Attempts != 1 || res.URI != "/redfish/v1/Systems/1" {
		t.Errorf("response = %+v", res)
	}
	if got.Get("OData-Version") != "4.0" || got.Get("Accept") != "application/json" || got.Get("User-Agent") != "my test" {
		t.Errorf("headers = %v", got)
	}
	if c.AuthMode() != "basic" || c.TLSMode() != "none" {
		t.Errorf("modes = %s, %s", c.AuthMode(), c.TLSMode())
	}

	// Without a user no credentials are sent.
	anon := plain(t, srv, Config{})
	if _, err := anon.Get(context.Background(), "/redfish/v1/Systems/1"); kindOf(err) != KindDenied {
		t.Errorf("without an account: %v, want denied", err)
	}
	if got.Get("Authorization") != "" || anon.AuthMode() != "none" {
		t.Errorf("a client without a user sent %q", got.Get("Authorization"))
	}
	// A wrong password is not retried: a BMC locks an account after a few
	// failed logins.
	wrong := plain(t, srv, Config{User: testUser, Password: "wrong"})
	_, err = wrong.Get(context.Background(), "/redfish/v1/Systems/1")
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindDenied || e.Attempts != 1 {
		t.Errorf("wrong password: %v", err)
	}
}

// session is a test BMC that only accepts session tokens.
type session struct {
	t        *testing.T
	mu       sync.Mutex
	next     int
	live     map[string]string // token -> session URI
	logins   int
	deletes  []string
	expire   bool // drop every session at the next GET, once
	location func(id string) string
}

func (s *session) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const sessions = "/redfish/v1/SessionService/Sessions"
	switch {
	case r.Method == http.MethodPost && r.URL.Path == sessions:
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte(`"UserName":"`+testUser+`"`)) || !bytes.Contains(body, []byte(`"Password":"`+testPassword+`"`)) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "" {
			s.t.Errorf("the login carries an Authorization header")
		}
		s.next++
		s.logins++
		id := fmt.Sprintf("%s/%d", sessions, s.next)
		token := fmt.Sprintf("token-%d-0123456789abcdef", s.next)
		s.live[token] = id
		w.Header().Set("X-Auth-Token", token)
		loc := id
		if s.location != nil {
			loc = s.location(id)
		}
		if loc != "" {
			w.Header().Set("Location", loc)
		}
		w.WriteHeader(http.StatusCreated)
		// What a BMC answers with: the session, and never the password.
		fmt.Fprintf(w, `{"@odata.id":%q,"UserName":%q,"Password":null}`, id, testUser)
	case r.Method == http.MethodDelete:
		token := r.Header.Get("X-Auth-Token")
		if s.live[token] != r.URL.Path {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		delete(s.live, token)
		s.deletes = append(s.deletes, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet:
		if r.Header.Get("Authorization") != "" {
			s.t.Errorf("a request of a session carries the account")
		}
		if s.expire {
			s.expire = false
			s.live = map[string]string{}
		}
		if _, ok := s.live[r.Header.Get("X-Auth-Token")]; !ok {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":{"message":"no valid session"}}`)
			return
		}
		io.WriteString(w, `{"Id":"1"}`)
	default:
		s.t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestSessionLoginAndLogout(t *testing.T) {
	bmc := &session{t: t, live: map[string]string{}}
	srv := httptest.NewServer(bmc)
	defer srv.Close()
	ctx := context.Background()
	c := plain(t, srv, Config{User: testUser, Password: testPassword, Session: true})

	// Before the login the BMC answers 401.
	if _, err := c.Get(ctx, "/redfish/v1/Systems/1"); kindOf(err) != KindDenied {
		t.Fatalf("before login: %v, want denied", err)
	}
	if err := c.Login(ctx, ""); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if !c.LoggedIn() || c.AuthMode() != "session" {
		t.Fatalf("no session after Login")
	}
	if _, err := c.Get(ctx, "/redfish/v1/Systems/1"); err != nil {
		t.Fatalf("after login: %v", err)
	}

	// The transport lets a DELETE through for this session and no other.
	for _, path := range []string{"/redfish/v1/SessionService/Sessions/99", "/redfish/v1/Systems/1", "/redfish/v1/SessionService/Sessions"} {
		req, _ := http.NewRequest(http.MethodDelete, srv.URL+path, nil)
		if res, err := c.hc.Do(req); err == nil {
			res.Body.Close()
			t.Errorf("DELETE %s went through", path)
		}
	}
	// And no POST once the login is over.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+DefaultSessions, strings.NewReader(`{}`))
	if res, err := c.hc.Do(req); err == nil {
		res.Body.Close()
		t.Errorf("a second POST to the session service went through")
	}

	if err := c.Logout(ctx); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if c.LoggedIn() {
		t.Errorf("still logged in after Logout")
	}
	if err := c.Logout(ctx); err != nil {
		t.Errorf("second Logout: %v", err)
	}
	bmc.mu.Lock()
	defer bmc.mu.Unlock()
	if bmc.logins != 1 || len(bmc.deletes) != 1 || bmc.deletes[0] != "/redfish/v1/SessionService/Sessions/1" || len(bmc.live) != 0 {
		t.Errorf("logins %d, deletes %v, live %v", bmc.logins, bmc.deletes, bmc.live)
	}
}

// A BMC that drops the session in the middle of a run: the client logs in
// again and the request succeeds.
func TestSessionIsRenewedAfter401(t *testing.T) {
	bmc := &session{t: t, live: map[string]string{}}
	srv := httptest.NewServer(bmc)
	defer srv.Close()
	ctx := context.Background()
	c := plain(t, srv, Config{User: testUser, Password: testPassword, Session: true})
	if err := c.Login(ctx, DefaultSessions); err != nil {
		t.Fatal(err)
	}
	bmc.mu.Lock()
	bmc.expire = true
	bmc.mu.Unlock()
	res, err := c.Get(ctx, "/redfish/v1/Systems/1")
	if err != nil {
		t.Fatalf("after the session was dropped: %v", err)
	}
	if res.Attempts != 1 {
		t.Errorf("attempts = %d: a new login is not a retry", res.Attempts)
	}
	if err := c.Logout(ctx); err != nil {
		t.Fatal(err)
	}
	bmc.mu.Lock()
	defer bmc.mu.Unlock()
	if bmc.logins != 2 || len(bmc.deletes) != 1 || bmc.deletes[0] != "/redfish/v1/SessionService/Sessions/2" {
		t.Errorf("logins %d, deletes %v", bmc.logins, bmc.deletes)
	}
}

func TestSessionLoginFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("wrong password", func(t *testing.T) {
		srv := httptest.NewServer(&session{t: t, live: map[string]string{}})
		defer srv.Close()
		c := plain(t, srv, Config{User: testUser, Password: "another-wrong-password", Session: true})
		err := c.Login(ctx, "")
		if kindOf(err) != KindDenied || strings.Contains(err.Error(), "another-wrong-password") {
			t.Errorf("Login: %v", err)
		}
	})
	t.Run("no token", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) }))
		defer srv.Close()
		c := plain(t, srv, Config{User: testUser, Password: testPassword, Session: true})
		if err := c.Login(ctx, ""); err == nil || !strings.Contains(err.Error(), "X-Auth-Token") {
			t.Errorf("Login: %v", err)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		var posts atomic.Int32
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1) }))
		defer other.Close()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
		}))
		defer srv.Close()
		c := plain(t, srv, Config{User: testUser, Password: testPassword, Session: true})
		if err := c.Login(ctx, ""); kindOf(err) != KindRefused {
			t.Errorf("Login: %v, want a refusal", err)
		}
		if posts.Load() != 0 {
			t.Errorf("the account was sent to another host")
		}
	})
	t.Run("no session service", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()
		c := plain(t, srv, Config{User: testUser, Password: testPassword, Session: true})
		if err := c.Login(ctx, ""); kindOf(err) != KindUnsupported {
			t.Errorf("Login: %v, want unsupported", err)
		}
	})
	t.Run("session elsewhere", func(t *testing.T) {
		// A session the BMC places on another host cannot be deleted by a
		// client that only talks to the BMC.
		bmc := &session{t: t, live: map[string]string{}, location: func(string) string { return "https://elsewhere.example/s/1" }}
		srv := httptest.NewServer(bmc)
		defer srv.Close()
		c := plain(t, srv, Config{User: testUser, Password: testPassword, Session: true})
		if err := c.Login(ctx, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Get(ctx, "/redfish/v1/Systems/1"); err != nil {
			t.Fatal(err)
		}
		if err := c.Logout(ctx); err == nil || !strings.Contains(err.Error(), "not deleted") {
			t.Errorf("Logout: %v", err)
		}
	})
	t.Run("session in the body", func(t *testing.T) {
		bmc := &session{t: t, live: map[string]string{}, location: func(string) string { return "" }}
		srv := httptest.NewServer(bmc)
		defer srv.Close()
		c := plain(t, srv, Config{User: testUser, Password: testPassword, Session: true})
		if err := c.Login(ctx, ""); err != nil {
			t.Fatal(err)
		}
		if err := c.Logout(ctx); err != nil {
			t.Errorf("Logout: %v", err)
		}
		if len(bmc.deletes) != 1 {
			t.Errorf("deletes = %v", bmc.deletes)
		}
	})
	t.Run("without a user", func(t *testing.T) {
		if _, err := New(Config{BaseURL: "https://10.0.0.5", Session: true}); err == nil {
			t.Errorf("a session without a user was accepted")
		}
	})
}

func TestRetries(t *testing.T) {
	type step struct {
		status int
		after  string
	}
	run := func(steps []step, cfg Config) (*Response, error, []time.Duration, int) {
		var n atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			i := int(n.Add(1)) - 1
			if i >= len(steps) {
				io.WriteString(w, `{"Id":"1"}`)
				return
			}
			if steps[i].after != "" {
				w.Header().Set("Retry-After", steps[i].after)
			}
			w.WriteHeader(steps[i].status)
			io.WriteString(w, `{"error":{"code":"Base.1.0.GeneralError","message":"busy","@Message.ExtendedInfo":[{"Message":"The service is busy."}]}}`)
		}))
		defer srv.Close()
		pauses := &noSleep{}
		cfg.Sleep = pauses.sleep
		c := plain(t, srv, cfg)
		res, err := c.Get(context.Background(), "/redfish/v1/Systems")
		return res, err, pauses.all(), int(n.Load())
	}

	// Overload answers are retried, and the answer of the third attempt counts.
	res, err, pauses, sent := run([]step{{503, ""}, {500, ""}}, Config{Backoff: time.Second})
	if err != nil || res.Attempts != 3 || sent != 3 {
		t.Fatalf("503, 500, 200: %v, attempts %d, sent %d", err, res.Attempts, sent)
	}
	if len(pauses) != 2 || pauses[0] < time.Second || pauses[0] > 1250*time.Millisecond || pauses[1] < 2*time.Second || pauses[1] > 2500*time.Millisecond {
		t.Errorf("pauses = %v, want about 1s then 2s", pauses)
	}

	// Retry-After is honoured, and bounded.
	_, err, pauses, _ = run([]step{{429, "7"}, {503, "3600"}}, Config{MaxRetryAfter: 20 * time.Second})
	if err != nil || len(pauses) != 2 || pauses[0] != 7*time.Second || pauses[1] != 20*time.Second {
		t.Errorf("429 with Retry-After: %v, pauses %v", err, pauses)
	}

	// After the retries the last answer is the error.
	_, err, pauses, sent = run([]step{{503, ""}, {503, ""}, {503, ""}, {503, ""}, {503, ""}}, Config{Retries: 2})
	var e *Error
	if !errors.As(err, &e) || e.Status != 503 || e.Attempts != 3 || sent != 3 || len(pauses) != 2 {
		t.Errorf("503 forever: %v, sent %d, pauses %v", err, sent, pauses)
	}
	if e != nil && !strings.Contains(e.Error(), "The service is busy.") {
		t.Errorf("the message of the service is missing: %v", e)
	}

	// Answers that will not change are not asked for again.
	for _, tc := range []struct {
		status int
		kind   Kind
	}{{404, KindUnsupported}, {405, KindUnsupported}, {501, KindUnsupported}, {410, KindUnsupported},
		{401, KindDenied}, {403, KindDenied}, {400, KindError}, {418, KindError}} {
		_, err, pauses, sent = run([]step{{tc.status, ""}}, Config{})
		if kindOf(err) != tc.kind || sent != 1 || len(pauses) != 0 {
			t.Errorf("status %d: %v, sent %d times", tc.status, err, sent)
		}
	}

	// Retries can be turned off.
	_, err, _, sent = run([]step{{503, ""}}, Config{Retries: -1})
	if err == nil || sent != 1 {
		t.Errorf("Retries -1: %v, sent %d", err, sent)
	}
}

func TestTimeoutIsRetriedThenReported(t *testing.T) {
	var n atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) <= 2 {
			// A BMC that accepts the request and then says nothing.
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		io.WriteString(w, `{"Id":"late"}`)
	}))
	defer srv.Close()
	defer close(release)
	pauses := &noSleep{}
	c := plain(t, srv, Config{Timeout: 150 * time.Millisecond, Sleep: pauses.sleep})

	res, err := c.Get(context.Background(), "/redfish/v1/Systems")
	if err != nil || res.Attempts != 3 {
		t.Fatalf("two stalls then an answer: %v", err)
	}

	n.Store(-100) // stall for good
	_, err = c.Get(context.Background(), "/redfish/v1/Systems")
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindTimeout || e.Attempts != DefaultRetries+1 || !e.Unreachable {
		t.Fatalf("stall: %v", err)
	}
	if !strings.Contains(e.Error(), "no answer within 150ms") {
		t.Errorf("error = %v", e)
	}
}

// An answer whose body stops in the middle is a timeout too, not a resource.
func TestStalledBody(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		io.WriteString(w, `{"Id":`)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	c := plain(t, srv, Config{Timeout: 150 * time.Millisecond, Retries: -1})
	if _, err := c.Get(context.Background(), "/redfish/v1/Systems"); kindOf(err) != KindTimeout {
		t.Errorf("stalled body: %v, want a timeout", err)
	}
}

func TestBrokenConnectionIsRetried(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		io.WriteString(w, `{"Id":"1"}`)
	}))
	defer srv.Close()
	pauses := &noSleep{}
	c := plain(t, srv, Config{Sleep: pauses.sleep})
	res, err := c.Get(context.Background(), "/redfish/v1/Systems")
	if err != nil || res.Attempts != 2 {
		t.Fatalf("after a broken connection: %v", err)
	}

	// A BMC that is gone: every attempt fails, and the error says so.
	srv.Close()
	_, err = c.Get(context.Background(), "/redfish/v1/Systems")
	var e *Error
	if !errors.As(err, &e) || !e.Unreachable || e.Attempts != DefaultRetries+1 || e.Kind != KindError {
		t.Errorf("BMC gone: %v", err)
	}
}

func TestCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()
	c := plain(t, srv, Config{Timeout: 5 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := c.Get(ctx, "/redfish/v1/Systems")
	if kindOf(err) != KindCanceled {
		t.Errorf("canceled run: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("the cancel took %v", time.Since(start))
	}
	// A pause before a retry ends with the run as well.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer down.Close()
	c = plain(t, down, Config{Backoff: time.Hour})
	ctx, cancel = context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := c.Get(ctx, "/redfish/v1/Systems"); kindOf(err) != KindCanceled {
		t.Errorf("canceled pause: %v", err)
	}
}

func TestLargeAnswersAreRefused(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/length": // says how large it is
			w.Header().Set("Content-Length", "5000")
			w.Write(big)
		case "/chunked": // does not
			w.(http.Flusher).Flush()
			w.Write(big)
		case "/gzip": // small on the wire, large when unpacked
			w.Header().Set("Content-Encoding", "gzip")
			zw := gzip.NewWriter(w)
			zw.Write(bytes.Repeat([]byte("y"), 1<<20))
			zw.Close()
		case "/fits":
			w.Write(big[:1000])
		}
	}))
	defer srv.Close()
	c := plain(t, srv, Config{MaxBody: 1000})
	for _, path := range []string{"/length", "/chunked", "/gzip"} {
		_, err := c.Get(context.Background(), path)
		var e *Error
		if !errors.As(err, &e) || e.Kind != KindTooLarge || e.Attempts != 1 {
			t.Errorf("%s: %v, want too large without a retry", path, err)
		}
	}
	if res, err := c.Get(context.Background(), "/fits"); err != nil || len(res.Body) != 1000 {
		t.Errorf("an answer of exactly the limit: %v", err)
	}
	if _, total := c.Requests(); total > 8000 {
		t.Errorf("%d bytes were read of answers that were refused for their size", total)
	}
}

func TestTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"Id":"RootService"}`)
	}))
	defer srv.Close()
	ctx := context.Background()
	cert := srv.Certificate()
	sum := sha256.Sum256(cert.Raw)
	fingerprint := FormatFingerprint(sum[:])

	// Without any setting a certificate nobody vouches for is refused, and
	// the error names its fingerprint.
	c := newClient(t, Config{BaseURL: srv.URL, Retries: -1})
	_, err := c.Get(ctx, "/redfish/v1/")
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindTLS || e.Attempts != 1 {
		t.Fatalf("self-signed certificate: %v", err)
	}
	if !strings.Contains(e.Error(), fingerprint) || !strings.Contains(e.Error(), "not trusted") {
		t.Errorf("error = %v, want the fingerprint %s", e, fingerprint)
	}
	if c.TLSMode() != "verified" {
		t.Errorf("mode = %s", c.TLSMode())
	}

	// With the certificate as the authority it is accepted.
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	c = newClient(t, Config{BaseURL: srv.URL, CAFile: ca})
	if _, err := c.Get(ctx, "/redfish/v1/"); err != nil || c.TLSMode() != "ca-file" {
		t.Errorf("--ca-file: %v (mode %s)", err, c.TLSMode())
	}
	// But not for a name it was not issued for.
	other := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	c = newClient(t, Config{BaseURL: other, CAFile: ca, Retries: -1})
	if _, err := c.Get(ctx, "/redfish/v1/"); kindOf(err) != KindTLS || !strings.Contains(err.Error(), "not issued for the address") {
		t.Errorf("wrong name: %v", err)
	}

	// Named by its fingerprint it is accepted under any name.
	for _, form := range []string{fingerprint, strings.ToLower(strings.ReplaceAll(fingerprint, ":", "")), "sha256 Fingerprint=" + fingerprint} {
		c = newClient(t, Config{BaseURL: other, Fingerprint: form})
		if _, err := c.Get(ctx, "/redfish/v1/"); err != nil || c.TLSMode() != "fingerprint" {
			t.Errorf("--fingerprint %s: %v", form, err)
		}
	}
	wrong := strings.Repeat("00:", 31) + "00"
	c = newClient(t, Config{BaseURL: srv.URL, Fingerprint: wrong, Retries: -1})
	if _, err := c.Get(ctx, "/redfish/v1/"); kindOf(err) != KindTLS || !strings.Contains(err.Error(), "another certificate") {
		t.Errorf("wrong fingerprint: %v", err)
	}

	c = newClient(t, Config{BaseURL: srv.URL, Insecure: true})
	if _, err := c.Get(ctx, "/redfish/v1/"); err != nil || c.TLSMode() != "insecure" {
		t.Errorf("--insecure: %v", err)
	}

	for _, cfg := range []Config{
		{BaseURL: srv.URL, Insecure: true, CAFile: ca},
		{BaseURL: srv.URL, Fingerprint: fingerprint, CAFile: ca},
		{BaseURL: srv.URL, Fingerprint: "abcd"},
		{BaseURL: srv.URL, CAFile: filepath.Join(t.TempDir(), "missing.pem")},
		{BaseURL: srv.URL, CAFile: func() string {
			p := filepath.Join(t.TempDir(), "empty.pem")
			os.WriteFile(p, []byte("not a certificate"), 0o600)
			return p
		}()},
		{BaseURL: "http://10.0.0.5"},
	} {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v) succeeded", cfg)
		}
	}
}

// The account never shows in an error, whatever the BMC sends back.
func TestSecretsStayInside(t *testing.T) {
	var token string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			token = "tok-9f8e7d6c5b4a39281706"
			w.Header().Set("X-Auth-Token", token)
			w.Header().Set("Location", "/redfish/v1/SessionService/Sessions/1")
			w.WriteHeader(http.StatusCreated)
			return
		}
		// A BMC that repeats what it was sent.
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"message":"bad request with Authorization %s token %s password %s"}}`,
			r.Header.Get("Authorization"), r.Header.Get("X-Auth-Token"), testPassword)
	}))
	defer srv.Close()
	ctx := context.Background()

	basic := plain(t, srv, Config{User: testUser, Password: testPassword})
	_, err := basic.Get(ctx, "/redfish/v1/Systems")
	if err == nil {
		t.Fatal("no error")
	}
	for _, secret := range []string{testPassword, basic.basic} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error holds %q: %v", secret, err)
		}
	}
	if !strings.Contains(err.Error(), removed) {
		t.Errorf("error = %v", err)
	}

	sess := plain(t, srv, Config{User: testUser, Password: testPassword, Session: true})
	if err := sess.Login(ctx, ""); err != nil {
		t.Fatal(err)
	}
	_, err = sess.Get(ctx, "/redfish/v1/Systems")
	if err == nil || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), testPassword) {
		t.Errorf("the error holds a secret: %v", err)
	}

	// Scrub does the same for a body that is about to be written.
	body := []byte(fmt.Sprintf(`{"Password":%q,"Note":"token %s, basic %s, password %s"}`, testPassword, token, sess.basic, testPassword))
	clean, n := sess.Scrub(body)
	for _, secret := range []string{testPassword, token, sess.basic} {
		if bytes.Contains(clean, []byte(secret)) {
			t.Errorf("Scrub left %q in %s", secret, clean)
		}
	}
	if n < 4 {
		t.Errorf("Scrub counted %d replacements in %s", n, clean)
	}
	if out, n := sess.Scrub([]byte(`{"Id":"DIMM1"}`)); n != 0 || string(out) != `{"Id":"DIMM1"}` {
		t.Errorf("Scrub changed a body without secrets: %s", out)
	}

	// A short password is replaced where it is a whole string, and not
	// inside other words.
	short := plain(t, srv, Config{User: "admin", Password: "admin"})
	clean, n = short.Scrub([]byte(`{"UserName":"admin","Role":"administrator","Password":"admin"}`))
	if string(clean) != `{"UserName":"[removed]","Role":"administrator","Password":"[removed]"}` || n != 2 {
		t.Errorf("Scrub with a short password = %s (%d)", clean, n)
	}
	// A password with characters JSON escapes is found in both forms.
	quoted := plain(t, srv, Config{User: testUser, Password: `pa"ss\word-1234`})
	clean, _ = quoted.Scrub([]byte(`{"a":"pa\"ss\\word-1234","b":"x pa\"ss\\word-1234 y"}`))
	if bytes.Contains(clean, []byte(`ss\\word`)) {
		t.Errorf("Scrub left an escaped password: %s", clean)
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Duration{
		"":                              0,
		"5":                             5 * time.Second,
		" 120 ":                         2 * time.Minute,
		"0":                             0,
		"-3":                            0,
		"soon":                          0,
		"99999999999999999999":          0,
		"Mon, 16 Nov 2026 12:00:30 GMT": 30 * time.Second,
		"Mon, 16 Nov 2026 11:00:00 GMT": 0,
	} {
		if got := retryAfter(in, now); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestErrorText(t *testing.T) {
	e := &Error{Kind: KindUnsupported, Method: "GET", URI: "/redfish/v1/Systems/1/Memory/1/MemoryMetrics", Status: 404}
	if got := e.Error(); got != "GET /redfish/v1/Systems/1/Memory/1/MemoryMetrics: 404 Not Found" {
		t.Errorf("Error() = %q", got)
	}
	e = &Error{Kind: KindTimeout, Method: "GET", URI: "/redfish/v1/", Detail: "no answer within 30s", Attempts: 4}
	if got := e.Error(); got != "GET /redfish/v1/: no answer within 30s (sent 4 times)" {
		t.Errorf("Error() = %q", got)
	}
	for k := KindError; k <= KindCanceled; k++ {
		if k.String() == "" {
			t.Errorf("kind %d has no name", k)
		}
	}
}

func FuzzParseTarget(f *testing.F) {
	for _, s := range []string{"10.0.0.5", "https://bmc.example:8443/redfish/v1/", "http://[::1]:8000", "https://u:p@h", "a b", "://", "https://h/%zz"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		u, err := ParseTarget(s)
		if err != nil {
			return
		}
		if u.User != nil || u.Path != "" || u.RawQuery != "" || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
			t.Errorf("ParseTarget(%q) = %#v", s, u)
		}
	})
}

// A reference from a resource never leads off the BMC and never holds a
// path that climbs.
func FuzzResolve(f *testing.F) {
	for _, s := range []string{"/redfish/v1/Systems/1", "https://10.0.0.5/redfish/v1", "//evil/x", "/a/../b", "/a/%2e%2e/b",
		"/redfish/v1/Systems/1/LogServices/SEL/Entries?$skip=50", "/a#/Members/0", "http://10.0.0.5:443/x", "/a/%2Fb", "/\x00"} {
		f.Add(s)
	}
	c, err := New(Config{BaseURL: "https://10.0.0.5"})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, ref string) {
		u, err := c.resolve(ref)
		if err != nil {
			return
		}
		if origin(u) != "https://10.0.0.5:443" || u.User != nil || !strings.HasPrefix(u.Path, "/") {
			t.Fatalf("resolve(%q) = %s", ref, u)
		}
		for _, seg := range strings.Split(u.Path, "/") {
			if seg == ".." || seg == "." {
				t.Fatalf("resolve(%q) = %s: a path that climbs", ref, u)
			}
		}
	})
}

func FuzzScrub(f *testing.F) {
	f.Add([]byte(`{"Password":"`+testPassword+`"}`), testPassword)
	f.Add([]byte(`plain text`), "admin")
	f.Add([]byte{0xff, 0x00, '"'}, "\"\\")
	f.Fuzz(func(t *testing.T, data []byte, password string) {
		c, err := New(Config{BaseURL: "https://10.0.0.5", User: "u", Password: password})
		if err != nil {
			t.Fatal(err)
		}
		out, n := c.Scrub(data)
		if n == 0 && !bytes.Equal(out, data) {
			t.Fatalf("Scrub changed the data and counted nothing")
		}
		if len(password) >= 8 && bytes.Contains(out, []byte(password)) {
			t.Fatalf("Scrub left the password %q in %q", password, out)
		}
	})
}
