// Package client is an HTTP client for the Redfish service of a BMC that
// can only read.
//
// Every request is a GET. The transport refuses any other method before it
// reaches the network, with one exception: with session authentication the
// client sends one POST to the session collection to log in and one DELETE
// of the session it was given to log out. Nothing else is written to a BMC:
// no PATCH, no PUT, no action, no change of a setting, no log clear.
//
// A client talks to one BMC and sends one request at a time. It follows a
// redirect only when it stays on the same host, retries timeouts and
// overload answers with a pause in between, and never reads more than a
// fixed number of bytes of an answer.
//
// The password and the session token stay inside the client: they are not
// part of any error, and Scrub removes them from text that came back from
// the BMC before it is written anywhere.
package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Defaults of a Config.
const (
	DefaultTimeout       = 30 * time.Second
	DefaultRetries       = 3
	DefaultBackoff       = time.Second
	DefaultMaxRetryAfter = 60 * time.Second
	DefaultMaxBody       = 32 << 20

	maxBackoff   = 30 * time.Second
	maxRedirects = 5
	maxURI       = 2048
	// maxErrorBody is how much of an error answer is read for its message.
	maxErrorBody = 64 << 10
	// maxRelogins is how often a run logs in again after the BMC dropped
	// its session.
	maxRelogins = 2
)

// DefaultSessions is where the session collection of a service is when the
// service root does not say.
const DefaultSessions = "/redfish/v1/SessionService/Sessions"

// Config describes how to reach one BMC.
type Config struct {
	// BaseURL is the address of the BMC: "https://10.0.0.5",
	// "bmc.example:8443" or "10.0.0.5". Without a scheme it is HTTPS.
	BaseURL string
	// User and Password are the account to read with. Without a user the
	// requests carry no credentials.
	User     string
	Password string
	// Session logs in through the session service and sends the token it
	// returns, instead of sending the account with every request (HTTP
	// Basic). The session is deleted by Logout.
	Session bool

	// CAFile is a PEM file with the certificates that may sign the
	// certificate of the BMC. Without it the system's certificates count.
	CAFile string
	// Fingerprint accepts exactly the certificate with this SHA-256
	// fingerprint (hexadecimal, colons allowed), whatever its name and
	// signer are.
	Fingerprint string
	// Insecure accepts any certificate, and allows plain HTTP.
	Insecure bool

	// Timeout bounds one request, from the connection to the last byte of
	// the answer.
	Timeout time.Duration
	// Retries is how often a request is sent again after a timeout, a
	// broken connection, or an answer 408, 429, 500, 502, 503 or 504.
	// Zero means DefaultRetries; a negative number means never.
	Retries int
	// Backoff is the pause before the first retry. It doubles with every
	// further one.
	Backoff time.Duration
	// MaxRetryAfter bounds the pause a BMC may ask for with Retry-After.
	MaxRetryAfter time.Duration
	// MaxBody bounds the bytes read of one answer, after decompression.
	MaxBody int64
	// UserAgent names the client to the BMC.
	UserAgent string

	// Transport replaces the network. The TLS settings then do not apply.
	// It is for tests.
	Transport http.RoundTripper
	// Sleep replaces the pause between retries. It is for tests.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Kind classifies a failed request.
type Kind int

const (
	// KindError is any failure without a class of its own.
	KindError Kind = iota
	// KindUnsupported: the service does not have the resource (404, 405,
	// 410, 501).
	KindUnsupported
	// KindDenied: the account may not read the resource (401, 403).
	KindDenied
	// KindTimeout: no complete answer within the time of a request.
	KindTimeout
	// KindTooLarge: the answer is larger than the client reads.
	KindTooLarge
	// KindRefused: the client itself refused the request: another method
	// than GET, another host, a redirect away from the BMC.
	KindRefused
	// KindTLS: the certificate of the BMC was not accepted.
	KindTLS
	// KindCanceled: the caller ended the run.
	KindCanceled
)

func (k Kind) String() string {
	switch k {
	case KindUnsupported:
		return "unsupported"
	case KindDenied:
		return "denied"
	case KindTimeout:
		return "timeout"
	case KindTooLarge:
		return "too large"
	case KindRefused:
		return "refused"
	case KindTLS:
		return "certificate"
	case KindCanceled:
		return "canceled"
	}
	return "error"
}

// Error is a request that did not yield a resource.
type Error struct {
	Kind Kind
	// Method and URI name the request: the URI is a path with its query.
	Method string
	URI    string
	// Status is the HTTP status of the last answer, or 0 when there was
	// none.
	Status int
	// Detail says what went wrong: the message of the service, or the
	// error of the connection.
	Detail string
	// Attempts is how many times the request was sent.
	Attempts int
	// RetryAfter is the pause the service asked for, if it did.
	RetryAfter time.Duration
	// Unreachable is set when no connection to the BMC could be made or
	// kept, as opposed to an answer the BMC gave.
	Unreachable bool
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Method)
	b.WriteByte(' ')
	b.WriteString(e.URI)
	b.WriteString(": ")
	if e.Status != 0 {
		fmt.Fprintf(&b, "%d %s", e.Status, http.StatusText(e.Status))
		if e.Detail != "" {
			b.WriteString(": ")
		}
	}
	b.WriteString(e.Detail)
	if e.Attempts > 1 {
		fmt.Fprintf(&b, " (sent %d times)", e.Attempts)
	}
	return b.String()
}

// Response is the answer to a GET.
type Response struct {
	// URI is the path and query that were asked for.
	URI    string
	Status int
	Body   []byte
	// ContentType is the media type of the answer, without parameters.
	ContentType string
	// Attempts is how many times the request was sent, Elapsed the time
	// from the first attempt to the answer.
	Attempts int
	Elapsed  time.Duration
}

// Client reads from one BMC.
type Client struct {
	cfg     Config
	base    *url.URL
	hc      *http.Client
	login   *http.Client // does not follow redirects
	guard   *guard
	tlsMode string

	// mu makes the client send one request at a time.
	mu sync.Mutex
	// The session, when one is open.
	token      string
	session    string // URI of the session resource
	sessions   string // URI of the session collection
	relogins   int
	basic      string // what HTTP Basic puts into the header
	requests   int
	bytesTotal int64
}

// New returns a client for the BMC of cfg. It opens no connection.
func New(cfg Config) (*Client, error) {
	base, err := ParseTarget(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	if base.Scheme == "http" && !cfg.Insecure {
		return nil, errors.New("plain HTTP sends the password unencrypted: use an https address, or say --insecure to allow it")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	switch {
	case cfg.Retries == 0:
		cfg.Retries = DefaultRetries
	case cfg.Retries < 0:
		cfg.Retries = 0
	}
	if cfg.Backoff <= 0 {
		cfg.Backoff = DefaultBackoff
	}
	if cfg.MaxRetryAfter <= 0 {
		cfg.MaxRetryAfter = DefaultMaxRetryAfter
	}
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = DefaultMaxBody
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "my"
	}
	if cfg.Session && cfg.User == "" {
		return nil, errors.New("a session needs an account: name the user")
	}
	c := &Client{cfg: cfg, base: base}

	next := cfg.Transport
	c.tlsMode = "test transport"
	if next == nil {
		tc, mode, err := tlsConfig(cfg, base)
		if err != nil {
			return nil, err
		}
		c.tlsMode = mode
		next = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSClientConfig:       tc,
			TLSHandshakeTimeout:   10 * time.Second,
			MaxConnsPerHost:       1,
			MaxIdleConnsPerHost:   1,
			IdleConnTimeout:       60 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
	}
	c.guard = &guard{next: next, origin: origin(base)}
	c.hc = &http.Client{Transport: c.guard, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		switch {
		case len(via) >= maxRedirects:
			return &refusal{"more than " + strconv.Itoa(maxRedirects) + " redirects"}
		case origin(req.URL) != c.guard.origin:
			return &refusal{"the BMC redirects to another host (" + req.URL.Host + "); a redirect is only followed on the BMC itself"}
		}
		return nil
	}}
	c.login = &http.Client{Transport: c.guard, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	if cfg.User != "" {
		c.basic = base64.StdEncoding.EncodeToString([]byte(cfg.User + ":" + cfg.Password))
	}
	return c, nil
}

// Base returns the address of the BMC: scheme, host and port.
func (c *Client) Base() string { return c.base.String() }

// Host returns the host name or IP address of the BMC, without the port.
func (c *Client) Host() string { return c.base.Hostname() }

// TLSMode says how the certificate of the BMC is checked: "verified"
// (system certificates), "ca-file", "fingerprint", "insecure", or "none"
// for plain HTTP.
func (c *Client) TLSMode() string { return c.tlsMode }

// AuthMode says how the client authenticates: "none", "basic" or
// "session".
func (c *Client) AuthMode() string {
	switch {
	case c.cfg.User == "":
		return "none"
	case c.cfg.Session:
		return "session"
	}
	return "basic"
}

// Requests returns how many requests were sent and how many bytes of
// answers were read.
func (c *Client) Requests() (n int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests, c.bytesTotal
}

// ParseTarget reads the address of a BMC. A bare host is taken as HTTPS. An
// address may not carry credentials, a query or a path other than that of
// the service root.
func ParseTarget(s string) (*url.URL, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("no BMC address")
	}
	if strings.ContainsAny(s, " \t\r\n\\") || len(s) > 512 {
		return nil, fmt.Errorf("%q is not a BMC address", clip(s, 80))
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		// The error of the parser repeats its input, which may hold a
		// password.
		return nil, errors.New("the BMC address is not a valid URL")
	}
	switch {
	case u.User != nil:
		return nil, errors.New("the BMC address carries credentials: name the user with --user and give the password through MY_REDFISH_PASSWORD, --password-file or the prompt")
	case u.Scheme != "https" && u.Scheme != "http":
		return nil, fmt.Errorf("scheme %q: a BMC is reached over https", u.Scheme)
	case u.Hostname() == "":
		return nil, errors.New("the BMC address names no host")
	case u.RawQuery != "" || u.Fragment != "":
		return nil, errors.New("the BMC address carries a query or a fragment: give the address of the BMC only")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("port %q is not a port number", p)
		}
	}
	switch strings.TrimRight(u.Path, "/") {
	case "", "/redfish", "/redfish/v1":
	default:
		return nil, fmt.Errorf("path %q: give the address of the BMC, the service root is found from there", clip(u.Path, 80))
	}
	return &url.URL{Scheme: u.Scheme, Host: strings.ToLower(u.Host)}, nil
}

// clip cuts a text to at most n bytes, at the start of a character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}

// origin is scheme, host and port of a URL in one form, so that two URLs of
// the same service compare equal.
func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

// refusal is a request the client did not send.
type refusal struct{ why string }

func (r *refusal) Error() string { return r.why }

// guard is the transport of a client. It lets through what a read-only
// client may send and nothing else, whatever the code above it asks for.
type guard struct {
	next   http.RoundTripper
	origin string

	mu sync.Mutex
	// post and del are the one URL a POST and the one URL a DELETE may go
	// to: the session collection while a login is under way, and the
	// session that login returned.
	post, del string
}

func (g *guard) allow(method, target string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch method {
	case http.MethodPost:
		g.post = target
	case http.MethodDelete:
		g.del = target
	}
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if origin(req.URL) != g.origin {
		return nil, &refusal{"a request to another host than the BMC (" + req.URL.Host + ")"}
	}
	g.mu.Lock()
	post, del := g.post, g.del
	g.mu.Unlock()
	switch req.Method {
	case http.MethodGet:
	case http.MethodPost:
		if post == "" || req.URL.String() != post {
			return nil, &refusal{"POST: this client only reads; the one POST it sends is the login to the session service"}
		}
	case http.MethodDelete:
		if del == "" || req.URL.String() != del {
			return nil, &refusal{"DELETE: this client only reads; the one DELETE it sends ends its own session"}
		}
	default:
		return nil, &refusal{req.Method + ": this client only reads"}
	}
	return g.next.RoundTrip(req)
}

// Resolve turns a reference found in a resource into the path and query to
// ask the BMC for. A reference that leads to another host, or that is no
// plain path, is refused: the text of a resource comes from the BMC and is
// not trusted to say where requests go.
func (c *Client) Resolve(ref string) (string, error) {
	u, err := c.resolve(ref)
	if err != nil {
		return "", err
	}
	return u.RequestURI(), nil
}

func (c *Client) resolve(ref string) (*url.URL, error) {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "":
		return nil, errors.New("empty reference")
	case len(ref) > maxURI:
		return nil, errors.New("reference longer than " + strconv.Itoa(maxURI) + " bytes")
	}
	for i := 0; i < len(ref); i++ {
		if ref[i] < 0x20 || ref[i] == 0x7f || ref[i] == '\\' {
			return nil, errors.New("reference with a control character or a backslash")
		}
	}
	u, err := url.Parse(ref)
	if err != nil {
		return nil, errors.New("reference is not a URI")
	}
	if u.IsAbs() || u.Host != "" {
		if u.User != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
			return nil, errors.New("reference to another kind of address")
		}
		if u.Scheme == "" {
			u.Scheme = c.base.Scheme
		}
		if origin(u) != c.guard.origin {
			return nil, fmt.Errorf("reference to another host (%s)", clip(u.Host, 80))
		}
	}
	if !strings.HasPrefix(u.Path, "/") {
		return nil, errors.New("reference is not an absolute path")
	}
	for _, seg := range strings.Split(u.EscapedPath(), "/") {
		if un, err := url.PathUnescape(seg); err != nil || un == ".." || un == "." || strings.ContainsAny(un, "/\\\x00") {
			return nil, errors.New("reference with a path segment that is no name")
		}
	}
	out := *c.base
	out.Path, out.RawPath, out.RawQuery = u.Path, u.RawPath, u.RawQuery
	return &out, nil
}

// Get asks the BMC for a resource. ref is a path, with a query if needed,
// or a URL on the BMC. On failure the error is an *Error.
func (c *Client) Get(ctx context.Context, ref string) (*Response, error) {
	u, err := c.resolve(ref)
	if err != nil {
		return nil, &Error{Kind: KindRefused, Method: http.MethodGet, URI: clip(ref, 200), Detail: err.Error(), Attempts: 0}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	start := time.Now()
	for attempt := 1; ; attempt++ {
		resp, e := c.get(ctx, u)
		if e == nil {
			resp.Attempts, resp.Elapsed = attempt, time.Since(start)
			return resp, nil
		}
		e.Attempts = attempt
		wait, again := c.retry(e, attempt)
		if !again {
			return nil, e
		}
		if err := c.sleep(ctx, wait); err != nil {
			e.Kind, e.Detail = KindCanceled, "stopped while waiting to retry: "+e.Detail
			return nil, e
		}
	}
}

// retry says whether a failed request is sent again, and after what pause.
func (c *Client) retry(e *Error, attempt int) (time.Duration, bool) {
	if attempt > c.cfg.Retries {
		return 0, false
	}
	switch {
	case e.Kind == KindTimeout, e.Unreachable && e.Kind == KindError:
	case e.Status == http.StatusRequestTimeout, e.Status == http.StatusTooManyRequests,
		e.Status == http.StatusInternalServerError, e.Status == http.StatusBadGateway,
		e.Status == http.StatusServiceUnavailable, e.Status == http.StatusGatewayTimeout:
	default:
		return 0, false
	}
	if e.RetryAfter > 0 {
		// The BMC says when to come back. A pause longer than the bound is
		// cut: the run has a deadline of its own.
		return min(e.RetryAfter, c.cfg.MaxRetryAfter), true
	}
	wait := c.cfg.Backoff << (attempt - 1)
	if wait > maxBackoff || wait <= 0 {
		wait = maxBackoff
	}
	// Several clients that were turned away together do not come back
	// together.
	wait += time.Duration(rand.Int64N(int64(wait)/4 + 1))
	return wait, true
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.cfg.Sleep != nil {
		return c.cfg.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// get sends one GET. An answer 401 to a session token is taken as a session
// the BMC dropped: the client logs in again, a bounded number of times.
func (c *Client) get(ctx context.Context, u *url.URL) (*Response, *Error) {
	resp, e := c.exchange(ctx, u)
	if e != nil && e.Status == http.StatusUnauthorized && c.token != "" && c.relogins < maxRelogins {
		c.relogins++
		c.token, c.session = "", ""
		c.guard.allow(http.MethodDelete, "")
		if lerr := c.loginLocked(ctx); lerr != nil {
			e.Detail = "the BMC dropped the session and the new login failed: " + lerr.Error()
			return nil, e
		}
		return c.exchange(ctx, u)
	}
	return resp, e
}

func (c *Client) exchange(ctx context.Context, u *url.URL) (*Response, *Error) {
	fail := &Error{Method: http.MethodGet, URI: u.RequestURI()}
	rctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, u.String(), nil)
	if err != nil {
		fail.Detail = "the request could not be built"
		return nil, fail
	}
	c.headers(req)
	c.requests++
	res, err := c.hc.Do(req)
	if err != nil {
		c.classify(ctx, rctx, err, fail)
		return nil, fail
	}
	defer res.Body.Close()
	fail.Status = res.StatusCode
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
		c.bytesTotal += int64(len(body))
		fail.Detail = c.scrubText(serviceMessage(body))
		fail.RetryAfter = retryAfter(res.Header.Get("Retry-After"), time.Now())
		switch res.StatusCode {
		case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusGone, http.StatusNotImplemented:
			fail.Kind = KindUnsupported
		case http.StatusUnauthorized, http.StatusForbidden:
			fail.Kind = KindDenied
		}
		return nil, fail
	}
	if res.ContentLength > c.cfg.MaxBody {
		fail.Kind, fail.Detail = KindTooLarge, fmt.Sprintf("the answer is %d bytes, more than the %d this client reads", res.ContentLength, c.cfg.MaxBody)
		return nil, fail
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, c.cfg.MaxBody+1))
	c.bytesTotal += int64(len(body))
	if err != nil {
		c.classify(ctx, rctx, err, fail)
		if fail.Kind == KindError {
			fail.Detail = "the answer broke off after " + strconv.Itoa(len(body)) + " bytes: " + fail.Detail
		}
		return nil, fail
	}
	if int64(len(body)) > c.cfg.MaxBody {
		fail.Kind, fail.Detail = KindTooLarge, fmt.Sprintf("the answer is larger than the %d bytes this client reads", c.cfg.MaxBody)
		return nil, fail
	}
	ct := res.Header.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return &Response{URI: u.RequestURI(), Status: res.StatusCode, Body: body, ContentType: strings.ToLower(strings.TrimSpace(ct))}, nil
}

// headers sets what every request carries.
func (c *Client) headers(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OData-Version", "4.0")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	switch {
	case c.token != "":
		req.Header.Set("X-Auth-Token", c.token)
	case c.basic != "" && !c.cfg.Session:
		req.Header.Set("Authorization", "Basic "+c.basic)
	}
}

// classify turns an error of the connection into the fields of an Error.
func (c *Client) classify(ctx, rctx context.Context, err error, e *Error) {
	var ref *refusal
	var cert *CertError
	var ne net.Error
	switch {
	case errors.As(err, &ref):
		e.Kind, e.Detail = KindRefused, ref.why
	case ctx.Err() != nil:
		e.Kind, e.Detail = KindCanceled, "the run was stopped"
	case errors.As(err, &cert):
		e.Kind, e.Detail = KindTLS, cert.Error()
	case rctx.Err() != nil, errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		e.Kind, e.Unreachable = KindTimeout, true
		e.Detail = "no answer within " + c.cfg.Timeout.String()
	default:
		e.Unreachable = true
		// The cause can quote what the BMC sent, a Location header for
		// one: it is kept to one line of bounded length.
		e.Detail = clip(oneLine(c.scrubText(plainError(err))), 300)
	}
}

// plainError returns the cause of a failed request without the URL the
// standard library puts in front of it.
func plainError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

// serviceMessage reads the message of a Redfish error answer, or returns a
// short form of a body that is none.
func serviceMessage(body []byte) string {
	var doc struct {
		Error struct {
			Message string `json:"message"`
			Info    []struct {
				Message string `json:"Message"`
			} `json:"@Message.ExtendedInfo"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &doc) == nil {
		for _, i := range doc.Error.Info {
			if m := strings.TrimSpace(i.Message); m != "" {
				return clip(oneLine(m), 300)
			}
		}
		if m := strings.TrimSpace(doc.Error.Message); m != "" {
			return clip(oneLine(m), 300)
		}
	}
	return ""
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// retryAfter reads a Retry-After header: seconds, or a date.
func retryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.ParseInt(v, 10, 32); err == nil {
		if n <= 0 {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// Login opens a session: one POST of the account to the session collection.
// sessions is the URI of that collection as the service root gives it; ""
// means the usual place. Without Config.Session, Login does nothing.
func (c *Client) Login(ctx context.Context, sessions string) error {
	if !c.cfg.Session {
		return nil
	}
	if sessions == "" {
		sessions = DefaultSessions
	}
	u, err := c.resolve(sessions)
	if err != nil {
		return fmt.Errorf("session service: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions = u.RequestURI()
	return c.loginLocked(ctx)
}

func (c *Client) loginLocked(ctx context.Context) error {
	u, err := c.resolve(c.sessions)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"UserName": c.cfg.User, "Password": c.cfg.Password})
	if err != nil {
		return errors.New("the login could not be built")
	}
	rctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return errors.New("the login could not be built")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OData-Version", "4.0")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	c.guard.allow(http.MethodPost, u.String())
	c.requests++
	res, err := c.login.Do(req)
	c.guard.allow(http.MethodPost, "")
	e := &Error{Method: http.MethodPost, URI: u.RequestURI(), Attempts: 1}
	if err != nil {
		c.classify(ctx, rctx, err, e)
		return e
	}
	defer res.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	c.bytesTotal += int64(len(answer))
	e.Status = res.StatusCode
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		e.Kind, e.Detail = KindDenied, "the BMC refused the account"
		return e
	case res.StatusCode >= 300 && res.StatusCode < 400:
		e.Kind, e.Detail = KindRefused, "the session service answers with a redirect; the login is not sent anywhere else"
		return e
	case res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK:
		if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusMethodNotAllowed || res.StatusCode == http.StatusNotImplemented {
			e.Kind = KindUnsupported
		}
		e.Detail = c.scrubText(serviceMessage(answer))
		return e
	}
	token := strings.TrimSpace(res.Header.Get("X-Auth-Token"))
	if token == "" {
		e.Status, e.Detail = 0, "the session service returned no X-Auth-Token"
		return e
	}
	// From here on the token is a secret of this client.
	c.token = token
	loc := strings.TrimSpace(res.Header.Get("Location"))
	if loc == "" {
		var doc struct {
			ID string `json:"@odata.id"`
		}
		if json.Unmarshal(answer, &doc) == nil {
			loc = doc.ID
		}
	}
	if su, err := c.resolve(loc); err == nil {
		c.session = su.RequestURI()
		c.guard.allow(http.MethodDelete, su.String())
	} else {
		// A session that cannot be named cannot be deleted. It ends when
		// the BMC times it out.
		c.session = ""
	}
	return nil
}

// LoggedIn reports whether a session is open.
func (c *Client) LoggedIn() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.token != ""
}

// Logout ends the session of this client: one DELETE of the session the
// login returned. It does nothing when no session is open. ctx should not
// be the context of the run: a run that was stopped still has to log out.
func (c *Client) Logout(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == "" {
		return nil
	}
	token, session := c.token, c.session
	c.token, c.session = "", ""
	if session == "" {
		return errors.New("the BMC did not say where the session is: it was not deleted and ends when the BMC times it out")
	}
	u, err := c.resolve(session)
	if err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return errors.New("the logout could not be built")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OData-Version", "4.0")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("X-Auth-Token", token)
	c.requests++
	res, err := c.login.Do(req)
	c.guard.allow(http.MethodDelete, "")
	e := &Error{Method: http.MethodDelete, URI: u.RequestURI(), Attempts: 1}
	if err != nil {
		c.classify(ctx, rctx, err, e)
		return e
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, maxErrorBody))
	switch res.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent,
		// A session that is already gone is what a logout wants.
		http.StatusNotFound, http.StatusUnauthorized:
		return nil
	}
	e.Status, e.Detail = res.StatusCode, "the session was not deleted"
	return e
}

// removed stands in for a secret that was taken out of a text.
const removed = "[removed]"

// markerFor returns what replaces a secret. It is "[removed]" unless the
// secret holds a bracket: then the end of the marker and the text after it
// could spell the secret again, so the marker is made of a character the
// secret does not have.
func markerFor(secret string) string {
	if !strings.ContainsAny(secret, "[]") {
		return removed
	}
	for _, ch := range "*#x=~+-_" + "abcdefghijklmnopqrstuvwyz0123456789" {
		if !strings.ContainsRune(secret, ch) {
			return strings.Repeat(string(ch), 8)
		}
	}
	return strings.Repeat("\uFFFD", 8)
}

// Scrub takes the secrets of this client out of data that came from the
// BMC: the session token and the Basic authorization wherever they occur,
// a JSON string that is exactly the password, and a password of eight
// characters or more wherever it occurs. A shorter password is only
// replaced as a whole JSON string: as a part of other text it would cut
// words that happen to contain it. n is the number of replacements.
func (c *Client) Scrub(data []byte) (out []byte, n int) {
	out = data
	replace := func(secret, with string) {
		if secret == "" || !bytes.Contains(out, []byte(secret)) {
			return
		}
		n += bytes.Count(out, []byte(secret))
		out = bytes.ReplaceAll(out, []byte(secret), []byte(with))
	}
	c.mu.Lock()
	token := c.token
	c.mu.Unlock()
	if len(token) >= 8 {
		replace(token, markerFor(token))
	}
	if c.basic != "" {
		replace(c.basic, removed)
	}
	if pw := c.cfg.Password; pw != "" {
		mark := markerFor(pw)
		quoted, err := json.Marshal(pw)
		if err == nil {
			replace(string(quoted), `"`+mark+`"`)
		}
		if len(pw) >= 8 {
			replace(pw, mark)
			// The same password as JSON writes it inside a longer string.
			if err == nil && len(quoted) > 2 {
				if inner := string(quoted[1 : len(quoted)-1]); inner != pw {
					replace(inner, mark)
				}
			}
		}
	}
	return out, n
}

// scrubText is Scrub for a message. Here the password is taken out
// wherever it occurs, whatever its length: a message is short and is
// written to the terminal. The caller holds the lock of the client.
func (c *Client) scrubText(s string) string {
	if s == "" {
		return s
	}
	for _, secret := range []string{c.token, c.basic} {
		if len(secret) >= 8 {
			s = strings.ReplaceAll(s, secret, markerFor(secret))
		}
	}
	if pw := c.cfg.Password; pw != "" {
		s = strings.ReplaceAll(s, pw, markerFor(pw))
	}
	return s
}
