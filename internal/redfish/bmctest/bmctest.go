// Package bmctest is a stand-in for the Redfish service of a BMC, for
// tests. It serves a tree of resources (redfish/v1/<path>/index.json, as
// the fixtures under testdata/redfish are laid out) over HTTP or HTTPS,
// asks for an account when it is given one, hands out sessions, pages and
// expands collections, and keeps a list of every request, so that a test
// can say what a client asked for and that it wrote nothing.
package bmctest

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Sessions is where the session collection of the stand-in is.
const Sessions = "/redfish/v1/SessionService/Sessions"

// Options shape a Server.
type Options struct {
	// User and Password are the account the service asks for. Without a
	// user it asks for none.
	User, Password string
	// SessionOnly refuses HTTP Basic: a client has to log in.
	SessionOnly bool
	// TLS serves HTTPS with the self-signed certificate of httptest.
	TLS bool
	// Expand makes the service honour $expand on collections. The service
	// root of the tree says whether a client may ask for it.
	Expand bool
	// PageSize, when positive, serves the members of a collection in
	// pages of that size, linked by Members@odata.nextLink.
	PageSize int
	// Overlay adds resources to the tree, or replaces them: the path of a
	// resource ("/redfish/v1/Systems/1") and its JSON.
	Overlay map[string]string
	// Handler sees every request first. It reports whether it answered.
	Handler func(w http.ResponseWriter, r *http.Request) bool
}

// Server is a running stand-in.
type Server struct {
	*httptest.Server
	opts Options
	fsys fs.FS

	mu       sync.Mutex
	requests []string
	writes   []string
	sessions map[string]string // token -> URI of the session
	opened   int
	closed   int
}

// New starts a stand-in that serves the tree in fsys. It is closed when
// the test ends.
func New(tb testing.TB, fsys fs.FS, opts Options) *Server {
	tb.Helper()
	s := &Server{opts: opts, fsys: fsys, sessions: map[string]string{}}
	s.Server = httptest.NewUnstartedServer(s)
	// A client that refuses the certificate is part of the tests, not
	// something to print.
	s.Server.Config.ErrorLog = log.New(io.Discard, "", 0)
	if opts.TLS {
		s.Server.StartTLS()
	} else {
		s.Server.Start()
	}
	tb.Cleanup(s.Close)
	return s
}

// Requests returns every request so far as "METHOD path?query".
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.requests...)
}

// Asked returns how many requests named the path, with any query.
func (s *Server) Asked(path string) int {
	n := 0
	for _, r := range s.Requests() {
		_, uri, _ := strings.Cut(r, " ")
		if p, _, _ := strings.Cut(uri, "?"); strings.TrimRight(p, "/") == strings.TrimRight(path, "/") {
			n++
		}
	}
	return n
}

// Writes returns the requests that were no GET, apart from the login to
// the session collection and the deletion of a session that login opened.
func (s *Server) Writes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.writes...)
}

// SessionCounts returns how many sessions were opened, how many of them
// were deleted, and how many are open.
func (s *Server) SessionCounts() (opened, closed, live int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened, s.closed, len(s.sessions)
}

// DropSessions forgets every session, as a BMC does that restarts its web
// server or times a session out.
func (s *Server) DropSessions() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = map[string]string{}
}

// Drop closes the connection of a request without an answer.
func Drop(w http.ResponseWriter) {
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			conn.Close()
		}
	}
}

// Stall holds a request until the client gives up.
func Stall(r *http.Request) { <-r.Context().Done() }

// Fail answers with a status and a Redfish error body.
func Fail(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"code":"Base.1.0.GeneralError","message":%q}}`, message)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+r.URL.RequestURI())
	s.mu.Unlock()
	if s.opts.Handler != nil && s.opts.Handler(w, r) {
		return
	}
	path := strings.TrimRight(r.URL.Path, "/")
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		if path == Sessions {
			s.login(w, r)
			return
		}
		s.wrote(w, r)
		return
	case http.MethodDelete:
		if s.logout(w, r, path) {
			return
		}
		s.wrote(w, r)
		return
	default:
		s.wrote(w, r)
		return
	}

	if path != "/redfish/v1" && path != "/redfish" && !s.allowed(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="bmc"`)
		Fail(w, http.StatusUnauthorized, "The request requires authentication.")
		return
	}
	doc, ok := s.resource(path)
	if !ok {
		Fail(w, http.StatusNotFound, "The resource at "+path+" was not found.")
		return
	}
	if members, isColl := doc["Members"].([]any); isColl {
		q := r.URL.Query()
		if s.opts.Expand && q.Get("$expand") != "" {
			for i, m := range members {
				if ref, ok := m.(map[string]any); ok && len(ref) == 1 {
					if id, _ := ref["@odata.id"].(string); id != "" {
						if full, ok := s.resource(strings.TrimRight(id, "/")); ok {
							members[i] = full
						}
					}
				}
			}
		}
		if size := s.opts.PageSize; size > 0 && len(members) > size {
			skip, _ := strconv.Atoi(q.Get("$skip"))
			skip = max(0, min(skip, len(members)))
			end := min(skip+size, len(members))
			doc["Members@odata.count"] = len(members)
			if end < len(members) {
				next := path + "?$skip=" + strconv.Itoa(end)
				if e := q.Get("$expand"); e != "" {
					next += "&$expand=" + e
				}
				doc["Members@odata.nextLink"] = next
			}
			members = members[skip:end]
		}
		doc["Members"] = members
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("OData-Version", "4.0")
	json.NewEncoder(w).Encode(doc)
}

// wrote notes a request that would change the service, and refuses it.
func (s *Server) wrote(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.writes = append(s.writes, r.Method+" "+r.URL.RequestURI())
	s.mu.Unlock()
	Fail(w, http.StatusMethodNotAllowed, "The stand-in changes nothing.")
}

// allowed reports whether a request carries the account or a session.
func (s *Server) allowed(r *http.Request) bool {
	if s.opts.User == "" {
		return true
	}
	if token := r.Header.Get("X-Auth-Token"); token != "" {
		s.mu.Lock()
		_, ok := s.sessions[token]
		s.mu.Unlock()
		return ok
	}
	if s.opts.SessionOnly {
		return false
	}
	u, p, ok := r.BasicAuth()
	return ok && u == s.opts.User && p == s.opts.Password
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var account struct {
		UserName string
		Password string
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if json.Unmarshal(body, &account) != nil || account.UserName != s.opts.User || account.Password != s.opts.Password || s.opts.User == "" {
		Fail(w, http.StatusUnauthorized, "The account was not accepted.")
		return
	}
	s.mu.Lock()
	s.opened++
	id := Sessions + "/" + strconv.Itoa(s.opened)
	token := fmt.Sprintf("session-token-%04d-7c1f0e5a9b3d", s.opened)
	s.sessions[token] = id
	s.mu.Unlock()
	w.Header().Set("X-Auth-Token", token)
	w.Header().Set("Location", id)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	fmt.Fprintf(w, `{"@odata.id":%q,"Id":%q,"UserName":%q,"Password":null}`, id, strconv.Itoa(s.opened), account.UserName)
}

// logout deletes the session a DELETE names, when the token of the
// request is the token of that session.
func (s *Server) logout(w http.ResponseWriter, r *http.Request, path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := r.Header.Get("X-Auth-Token")
	if id, ok := s.sessions[token]; !ok || id != path {
		return false
	}
	delete(s.sessions, token)
	s.closed++
	w.WriteHeader(http.StatusNoContent)
	return true
}

// resource loads the JSON of a resource: from the overlay, or from the
// tree.
func (s *Server) resource(path string) (map[string]any, bool) {
	var data []byte
	if text, ok := s.opts.Overlay[path]; ok {
		data = []byte(text)
	} else {
		var err error
		data, err = fs.ReadFile(s.fsys, strings.TrimPrefix(path, "/")+"/index.json")
		if err != nil {
			return nil, false
		}
	}
	var doc map[string]any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if dec.Decode(&doc) != nil {
		return nil, false
	}
	return doc, true
}

// Paths lists the resources of the tree and the overlay, sorted.
func (s *Server) Paths() []string {
	seen := map[string]bool{}
	for p := range s.opts.Overlay {
		seen[p] = true
	}
	fs.WalkDir(s.fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "index.json" {
			seen["/"+strings.TrimSuffix(p, "/index.json")] = true
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
