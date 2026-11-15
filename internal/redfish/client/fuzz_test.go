package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// answering is a transport that answers every request with the same
// status, header and body, and keeps what it was asked.
type answering struct {
	status int
	header http.Header
	body   []byte
	// after is what a second kind of answer looks like: requests beyond
	// the first get status 200 when it is set.
	after bool
	seen  []*http.Request
}

func (a *answering) RoundTrip(req *http.Request) (*http.Response, error) {
	a.seen = append(a.seen, req)
	if req.Body != nil {
		io.Copy(io.Discard, req.Body)
		req.Body.Close()
	}
	status := a.status
	if a.after && len(a.seen) > 1 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: a.header.Clone(), Body: io.NopCloser(bytes.NewReader(a.body)), ContentLength: -1, Request: req,
	}, nil
}

// FuzzAnswer lets a BMC answer anything: any status, any body, any value
// in the headers the client reads. Whatever it answers, the client sends
// nothing but its reads, its one login and its one logout, sends them to
// the BMC only, gives up after a bounded number of requests, and puts
// neither the password nor the session token into what it reports.
func FuzzAnswer(f *testing.F) {
	f.Add(uint16(200), []byte(`{"@odata.id":"/redfish/v1/Systems/1","Id":"1"}`), "application/json; charset=utf-8", "", "", "", false, false)
	f.Add(uint16(503), []byte(`{"error":{"message":"busy","@Message.ExtendedInfo":[{"Message":" try\nlater "}]}}`), "application/json", "7", "", "", false, true)
	f.Add(uint16(429), []byte(`<html>`), "text/html", "Wed, 21 Oct 2037 07:28:00 GMT", "", "", false, false)
	f.Add(uint16(401), []byte(`{"error":{"message":"`+testPassword+`"}}`), "", "-1", "", "tok-7f3a9c1d", true, true)
	f.Add(uint16(201), []byte(`{"@odata.id":"/redfish/v1/SessionService/Sessions/9"}`), "application/json", "", "", "tok-7f3a9c1d", true, true)
	f.Add(uint16(201), []byte(`{}`), "", "", "https://evil.example/redfish/v1/SessionService/Sessions/1", "tok\r\nX-Evil: 1", true, false)
	f.Add(uint16(302), []byte{}, "", "", "https://evil.example/", "", false, false)
	f.Add(uint16(307), []byte{}, "", "", "/redfish/v1/Systems/1?again", "", true, true)
	f.Add(uint16(200), bytes.Repeat([]byte("x"), 70000), "APPLICATION/JSON", "99999999999999999999", "/a/../b", strings.Repeat("t", 3000), true, false)
	f.Add(uint16(0), []byte{}, "", "", "", "", false, false)

	const base = "https://10.0.0.5"
	const maxBody = 64 << 10
	f.Fuzz(func(t *testing.T, status uint16, body []byte, contentType, retryAfter, location, token string, session, after bool) {
		tr := &answering{status: int(status), header: http.Header{}, body: body, after: after}
		for k, v := range map[string]string{"Content-Type": contentType, "Retry-After": retryAfter, "Location": location, "X-Auth-Token": token} {
			if v != "" {
				tr.header[k] = []string{v}
			}
		}
		var slept time.Duration
		c, err := New(Config{BaseURL: base, User: testUser, Password: testPassword, Session: session, Transport: tr, Retries: 2, MaxBody: maxBody,
			Timeout: 5 * time.Second,
			Sleep: func(_ context.Context, d time.Duration) error {
				if d < 0 || d > DefaultMaxRetryAfter {
					t.Errorf("a pause of %s", d)
				}
				slept += d
				return nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()

		// What the client reports must not carry its secrets. The token is
		// the BMC's text until the client took it for its session.
		secret := func(what, text string) {
			if strings.Contains(text, testPassword) {
				t.Errorf("%s carries the password: %q", what, text)
			}
			if tok := strings.TrimSpace(token); c.LoggedIn() && len(tok) >= 8 && strings.Contains(text, tok) {
				t.Errorf("%s carries the session token: %q", what, text)
			}
		}
		failed := func(what string, err error) {
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%s failed with %T (%v), not with an *Error", what, err, err)
			}
			if e.Attempts < 0 || e.Attempts > 3 || len(e.Detail) > 600 || strings.ContainsAny(e.Detail, "\r\n") {
				t.Errorf("%s: %d attempts, detail %q", what, e.Attempts, e.Detail)
			}
			secret(what, err.Error())
		}

		if err := c.Login(ctx, ""); err != nil {
			failed("login", err)
		}
		if !session && c.LoggedIn() {
			t.Error("a session without a login")
		}
		res, err := c.Get(ctx, "/redfish/v1/Systems/1")
		switch {
		case err != nil:
			failed("GET", err)
		case res.Status != http.StatusOK || int64(len(res.Body)) > maxBody || res.ContentType != strings.ToLower(res.ContentType) || strings.Contains(res.ContentType, ";"):
			t.Errorf("answer %d with %d bytes of %q was handed on", res.Status, len(res.Body), res.ContentType)
		default:
			out, _ := c.Scrub(res.Body)
			secret("a scrubbed answer", string(out))
		}
		if err := c.Logout(ctx); err != nil {
			secret("logout", err.Error())
		}
		if c.LoggedIn() {
			t.Error("a session after the logout")
		}

		// What went out. A login, the read with its retries and redirects,
		// a second login after a dropped session, the logout.
		if n := len(tr.seen); n > 60 {
			t.Errorf("%d requests", n)
		}
		posts, deletes := 0, 0
		for _, req := range tr.seen {
			if origin(req.URL) != base+":443" {
				t.Fatalf("%s %s went to another host", req.Method, req.URL)
			}
			if req.URL.User != nil || strings.Contains(req.URL.String(), testPassword) {
				t.Errorf("%s carries the account in its address", req.URL)
			}
			switch req.Method {
			case http.MethodGet:
			case http.MethodPost:
				posts++
				if req.URL.Path != DefaultSessions {
					t.Errorf("POST to %s", req.URL)
				}
			case http.MethodDelete:
				deletes++
			default:
				t.Errorf("%s %s was sent", req.Method, req.URL)
			}
			if req.Method != http.MethodPost && req.Header.Get("Authorization") != "" && session {
				t.Errorf("%s %s carries the account although a session was asked for", req.Method, req.URL)
			}
		}
		if (!session && posts+deletes > 0) || posts > 1+maxRelogins || deletes > 1 {
			t.Errorf("%d POST and %d DELETE requests (session %v)", posts, deletes, session)
		}
		if slept > 2*DefaultMaxRetryAfter {
			t.Errorf("the client asked to wait %s in all", slept)
		}
	})
}

// FuzzParseFingerprint reads what a user gives as the fingerprint of a
// certificate: 32 bytes come back, or an error that does not repeat the
// input.
func FuzzParseFingerprint(f *testing.F) {
	sum := strings.Repeat("AB:", 31) + "AB"
	for _, s := range []string{sum, "sha256 Fingerprint=" + sum, strings.ReplaceAll(sum, ":", ""), strings.ToLower(sum), "AB:CD", "", "=", "zz", sum + ":00", " " + sum + "\n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		b, err := ParseFingerprint(s)
		if err != nil {
			if b != nil || (len(s) > 8 && strings.Contains(err.Error(), s)) {
				t.Errorf("ParseFingerprint(%q) = %x, %v", s, b, err)
			}
			return
		}
		if len(b) != 32 {
			t.Fatalf("ParseFingerprint(%q) gave %d bytes", s, len(b))
		}
		if again, err := ParseFingerprint(FormatFingerprint(b)); err != nil || !bytes.Equal(again, b) {
			t.Errorf("%q does not survive its own form: %x, %v", s, again, err)
		}
	})
}
