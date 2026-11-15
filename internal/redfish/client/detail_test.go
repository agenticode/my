package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// The cause of a failed request can quote what the BMC sent. A redirect
// with a Location header that is no URL put the whole header, twice, into
// the detail of the error, which goes into the manifest and onto the
// screen. The detail is one line of bounded length. Found by FuzzAnswer.
func TestDetailOfAFailedRequestIsBounded(t *testing.T) {
	location := "\x7f" + strings.Repeat("é", 4000)
	tr := &answering{status: http.StatusFound, header: http.Header{"Location": {location}}}
	c := newClient(t, Config{BaseURL: "https://10.0.0.5", Transport: tr, Retries: -1})
	_, err := c.Get(context.Background(), "/redfish/v1/")
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %T: %v", err, err)
	}
	if !strings.HasPrefix(e.Detail, "failed to parse Location header") || !strings.HasSuffix(e.Detail, "...") {
		t.Errorf("detail %q", e.Detail)
	}
	if len(e.Detail) > 303 || strings.ContainsAny(e.Detail, "\r\n") || !utf8.ValidString(e.Detail) {
		t.Errorf("detail of %d bytes: %q", len(e.Detail), e.Detail)
	}

	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly ten", 11, "exactly ten"},
		{"0123456789", 4, "0123..."},
		{"abécd", 3, "ab..."}, // the cut would fall inside the character
		{"abécd", 4, "abé..."},
		{"é", 1, "..."},
	} {
		if got := clip(tc.in, tc.n); got != tc.want {
			t.Errorf("clip(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

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
