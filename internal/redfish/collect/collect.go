// Package collect asks the Redfish service of a BMC for what MemorY reads
// about a host and writes the answers to a directory: the memory modules
// and their error counts, the BIOS settings with the registry that
// explains them, the event logs, the firmware versions.
//
// It only reads. Every request is a GET (see package client); with session
// authentication the run logs in once and deletes its session at the end.
//
// The directory is laid out the way the DMTF mockup tools lay out a
// service, one index.json per URI, and holds a manifest that lists every
// URI with what became of it. A BMC that is slow, that lacks a resource,
// that denies one or that stops answering in the middle still yields a
// collection: what came back is kept, and the manifest says what did not.
package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/agenticode/my/internal/redfish/client"
)

// Defaults and bounds of a run.
const (
	DefaultDeadline      = 20 * time.Minute
	DefaultMaxLogEntries = 5000
	DefaultMaxTotalBytes = 512 << 20

	// ServiceRoot is where a Redfish service starts.
	ServiceRoot = "/redfish/v1/"

	maxSystems   = 8
	maxManagers  = 8
	maxChassis   = 64
	maxMembers   = 512
	maxLogs      = 64
	maxPages     = 500
	maxResources = 20000
	// maxSingleEntries bounds the entries that are asked for one by one
	// when a log lists its entries as links and cannot expand them.
	maxSingleEntries = 500
	// maxUnreachable is how many resources in a row may go without any
	// answer before the BMC counts as gone.
	maxUnreachable = 3
	// maxUnauthorized is how many 401 answers in a row end a run: the BMC
	// no longer accepts the account, and more requests may lock it.
	maxUnauthorized = 3
)

// skippedLogs are logs that hold no hardware events, by the Id of the log
// service in lower case: the journal and the console of the BMC, POST
// codes, logins, and dumps whose entries are attachments.
var skippedLogs = map[string]string{
	"journal":    "the journal of the BMC itself",
	"hostlogger": "the console output of the host",
	"postcodes":  "POST codes",
	"auditlog":   "logins and configuration changes",
	"audit":      "logins and configuration changes",
	"dump":       "dumps of the BMC",
	"bmcdump":    "dumps of the BMC",
	"systemdump": "dumps of the system",
	"crashdump":  "crash dumps",
	"faultlog":   "dumps of the BMC",
	"telemetry":  "telemetry reports",
}

// Options describe one run against one BMC.
type Options struct {
	// Client says how to reach the BMC: address, account, TLS settings,
	// timeout per request, retries.
	Client client.Config
	// Name is what the collection is called in the manifest and in a
	// support table. Empty means the address of the BMC.
	Name string
	// Tool is the version of the tool, for the manifest.
	Tool string
	// Deadline bounds the whole run. Zero means DefaultDeadline.
	Deadline time.Duration
	// MaxLogEntries bounds the entries read of one log. Zero means
	// DefaultMaxLogEntries; a negative number reads no logs.
	MaxLogEntries int
	// Logs names logs to read besides the ones a run reads anyway, by the
	// Id of their log service: logs that are skipped otherwise.
	Logs []string
	// MaxTotalBytes bounds what one run writes. Zero means
	// DefaultMaxTotalBytes.
	MaxTotalBytes int64
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Progress is called for every resource the run dealt with.
	Progress func(Resource)
}

type object = map[string]any

// run is the state of one collection.
type run struct {
	// ctx ends with the time limit of the run, parent only when the
	// caller ends it.
	ctx    context.Context
	parent context.Context
	opts   Options
	cl     *client.Client
	tree   *tree
	m      *Manifest
	now    func() time.Time

	// stopped is why the run asks for nothing more, or "".
	stopped string
	// authed is set once the BMC answered a request that needs the account.
	authed bool
	// unreachable and unauthorized count failures in a row.
	unreachable, unauthorized int
	written                   int64
	// expand is the $expand query of this service, or "". expandFailed
	// counts collections that refused it.
	expand       string
	expandFailed int
	offered      map[string]bool
	reached      map[string]bool
	// done are the URIs that were dealt with: a service may link one
	// resource from several places.
	done map[string]bool
	logs map[string]bool
}

// Run collects from the BMC of opts into dir, which is created when it
// does not exist. The manifest it returns is also written to dir. An error
// means that the collection could not be written, or that the settings are
// wrong; whatever the BMC did or failed to do is in the manifest.
func Run(ctx context.Context, dir string, opts Options) (*Manifest, error) {
	if opts.Deadline <= 0 {
		opts.Deadline = DefaultDeadline
	}
	if opts.MaxLogEntries == 0 {
		opts.MaxLogEntries = DefaultMaxLogEntries
	}
	if opts.MaxTotalBytes <= 0 {
		opts.MaxTotalBytes = DefaultMaxTotalBytes
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	cl, err := client.New(opts.Client)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return nil, err
	}
	t, err := openTree(dir)
	if err != nil {
		return nil, err
	}
	defer t.close()

	name := opts.Name
	if name == "" {
		name = cl.Host()
	}
	timeout := opts.Client.Timeout
	if timeout <= 0 {
		timeout = client.DefaultTimeout
	}
	maxBody := opts.Client.MaxBody
	if maxBody <= 0 {
		maxBody = client.DefaultMaxBody
	}
	r := &run{parent: ctx, opts: opts, cl: cl, tree: t, now: now, offered: map[string]bool{}, reached: map[string]bool{}, done: map[string]bool{}, logs: map[string]bool{}}
	for _, l := range opts.Logs {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			r.logs[l] = true
		}
	}
	r.m = &Manifest{Format: Format, Version: Version, Tool: opts.Tool, StartedAt: now().UTC(),
		BMC: BMC{Address: cl.Host(), URL: cl.Base(), Name: name}, TLS: cl.TLSMode(), Auth: cl.AuthMode(),
		Resources: []Resource{},
		Limits: Limits{TimeoutSeconds: int(timeout / time.Second), DeadlineSeconds: int(opts.Deadline / time.Second),
			MaxLogEntries: opts.MaxLogEntries, MaxBodyBytes: maxBody, MaxTotalBytes: opts.MaxTotalBytes}}

	var cancel context.CancelFunc
	r.ctx, cancel = context.WithTimeout(ctx, opts.Deadline)
	r.crawl()
	if r.stopped == "" && r.ctx.Err() != nil {
		r.stop(r.endReason())
	}
	cancel()

	// The session is ended also when the run was stopped or interrupted:
	// on a context of its own.
	if cl.LoggedIn() {
		lctx, lcancel := context.WithTimeout(context.WithoutCancel(ctx), min(timeout, 15*time.Second))
		if err := cl.Logout(lctx); err != nil {
			r.note("the session on the BMC was not closed: %v", err)
		}
		lcancel()
	}

	r.m.FinishedAt = now().UTC()
	r.m.Complete = r.stopped == ""
	r.m.Stopped = r.stopped
	r.m.Requests, r.m.Bytes = cl.Requests()
	r.m.summarize(r.offered, r.reached)
	data, err := json.MarshalIndent(r.m, "", "  ")
	if err != nil {
		return r.m, err
	}
	if err := t.writeTop(ManifestName, append(data, '\n')); err != nil {
		return r.m, err
	}
	return r.m, nil
}

// endReason says why the context of the run ended.
func (r *run) endReason() string {
	if r.parent.Err() != nil {
		return "the run was interrupted"
	}
	return fmt.Sprintf("the time limit of %s for one BMC was reached", r.opts.Deadline)
}

func (r *run) stop(why string) {
	if r.stopped == "" {
		r.stopped = why
	}
}

func (r *run) note(format string, a ...any) {
	r.m.Notes = append(r.m.Notes, fmt.Sprintf(format, a...))
}

// reach notes that the run got to the point where it asks for the classes.
// A run that was stopped gets nowhere.
func (r *run) reach(classes ...string) {
	if r.stopped != "" {
		return
	}
	for _, c := range classes {
		r.reached[c] = true
	}
}

// record adds a resource to the manifest.
func (r *run) record(res Resource) {
	r.m.Resources = append(r.m.Resources, res)
	if r.opts.Progress != nil {
		r.opts.Progress(res)
	}
	if len(r.m.Resources) >= maxResources {
		r.stop(fmt.Sprintf("the service lists more than %d resources: not the memory, settings and logs of one machine", maxResources))
	}
}

// crawl asks for the resources, the ones a finding needs most first, so
// that a run that is cut short keeps them.
func (r *run) crawl() {
	root, ok := r.fetch(ClassRoot, ServiceRoot)
	if !ok {
		if r.stopped == "" {
			last := r.m.Resources[len(r.m.Resources)-1]
			r.stop("the service root did not come back: " + last.Detail)
		}
		return
	}
	r.reach(ClassRoot)
	r.m.Service = Service{RedfishVersion: str(root, "RedfishVersion"), Vendor: str(root, "Vendor"), Product: str(root, "Product")}
	r.expand = expandQuery(child(child(root, "ProtocolFeaturesSupported"), "ExpandQuery"))
	r.m.Expand = r.expand

	if r.cl.AuthMode() == "session" {
		if err := r.cl.Login(r.ctx, link(child(root, "Links"), "Sessions")); err != nil {
			r.stop("the login to the session service failed: " + err.Error())
			return
		}
		r.authed = true
	}

	registries := link(root, "Registries")
	r.reach(ClassSystem)
	if uri := link(root, "Systems"); uri != "" {
		r.offered[ClassSystem] = true
		for _, m := range r.collection(ClassSystem, uri, collOpts{max: maxSystems}) {
			if sys, ok := r.member(ClassSystem, m); ok {
				r.system(sys, registries)
			}
		}
	} else {
		r.note("the service root links no Systems collection")
	}

	r.reach(ClassManager)
	if uri := link(root, "Managers"); uri != "" && r.stopped == "" {
		r.offered[ClassManager] = true
		for _, m := range r.collection(ClassManager, uri, collOpts{max: maxManagers}) {
			mgr, ok := r.member(ClassManager, m)
			if !ok {
				continue
			}
			r.m.Managers = append(r.m.Managers, Manager{ID: idOf(mgr), Model: str(mgr, "Model"), FirmwareVersion: str(mgr, "FirmwareVersion")})
			if t, ok := managerTime(mgr); ok && r.m.Clock == nil {
				here := r.now().UTC()
				r.m.Clock = &Clock{BMC: t, Collector: here, OffsetSeconds: int64(t.Sub(here) / time.Second)}
			}
			r.logServices(link(mgr, "LogServices"))
		}
	}

	r.reach(ClassFirmware)
	if uri := link(root, "UpdateService"); uri != "" && r.stopped == "" {
		r.offered[ClassFirmware] = true
		if us, ok := r.fetch(ClassFirmware, uri); ok {
			if inv := link(us, "FirmwareInventory"); inv != "" {
				for _, m := range r.collection(ClassFirmware, inv, collOpts{max: maxMembers, expand: true}) {
					if fw, ok := r.member(ClassFirmware, m); ok {
						r.m.Firmware = append(r.m.Firmware, Firmware{ID: idOf(fw), Name: str(fw, "Name"), Version: str(fw, "Version")})
					}
				}
			}
		}
	}

	r.reach(ClassChassis)
	if uri := link(root, "Chassis"); uri != "" && r.stopped == "" {
		r.offered[ClassChassis] = true
		for _, m := range r.collection(ClassChassis, uri, collOpts{max: maxChassis}) {
			r.member(ClassChassis, m)
		}
	}
}

// system reads what hangs below one ComputerSystem.
func (r *run) system(sys object, registries string) {
	s := System{ID: idOf(sys), Manufacturer: str(sys, "Manufacturer"), Model: str(sys, "Model"), HostName: str(sys, "HostName"), BiosVersion: str(sys, "BiosVersion")}

	r.reach(ClassMemory, ClassMetrics)
	if uri := link(sys, "Memory"); uri != "" {
		r.offered[ClassMemory] = true
		for _, m := range r.collection(ClassMemory, uri, collOpts{max: maxMembers, expand: true}) {
			mem, ok := r.member(ClassMemory, m)
			if !ok {
				continue
			}
			if metrics := link(mem, "Metrics"); metrics != "" {
				r.offered[ClassMetrics] = true
				r.fetch(ClassMetrics, metrics)
			}
		}
	}

	r.reach(ClassBios, ClassBiosSettings, ClassRegistry)
	if uri := link(sys, "Bios"); uri != "" {
		r.offered[ClassBios] = true
		if bios, ok := r.fetch(ClassBios, uri); ok {
			if pending := link(child(bios, "@Redfish.Settings"), "SettingsObject"); pending != "" {
				r.offered[ClassBiosSettings] = true
				r.fetch(ClassBiosSettings, pending)
			}
			if s.AttributeRegistry = str(bios, "AttributeRegistry"); s.AttributeRegistry != "" {
				r.registry(s.AttributeRegistry, registries)
			}
		}
	}
	r.m.Systems = append(r.m.Systems, s)
	r.logServices(link(sys, "LogServices"))
}

// registry finds the attribute registry a Bios resource names: through the
// Registries collection to the registry file resource and from there to
// the JSON of the registry itself, when the BMC holds a copy.
func (r *run) registry(name, registries string) {
	if r.stopped != "" {
		return
	}
	r.offered[ClassRegistry] = true
	missing := func(detail string) {
		r.record(Resource{URI: registries, Class: ClassRegistry, Status: StatusUnsupported, Detail: detail})
	}
	if registries == "" {
		r.record(Resource{URI: ServiceRoot, Class: ClassRegistry, Status: StatusUnsupported,
			Detail: "the Bios resource names the registry " + name + ", and the service root links no Registries collection"})
		return
	}
	members := r.collectionAs(ClassRegistry, registries, collOpts{max: maxMembers}, true)
	if r.stopped != "" || members == nil {
		return
	}
	// The registry file resource is named after the registry, with or
	// without its version.
	want := strings.ToLower(name)
	base, _, _ := strings.Cut(want, ".")
	best, rank := "", 0
	for _, m := range members {
		id := str(m, "@odata.id")
		seg := strings.ToLower(lastSegment(id))
		score := 0
		switch {
		case seg == want:
			score = 3
		case seg == base || strings.HasPrefix(seg, base+".") || strings.HasPrefix(want, seg+"."):
			score = 2
		case strings.Contains(seg, "biosattributeregistry") && base != "" && strings.Contains(seg, base):
			score = 1
		}
		if score > rank {
			best, rank = id, score
		}
	}
	if best == "" {
		missing("the Bios resource names the registry " + name + ", and the Registries collection has no entry for it")
		return
	}
	file, ok := r.fetchAs(ClassRegistry, best, true)
	if !ok {
		return
	}
	// The copy on the BMC, in English when there is a choice.
	local, published := "", ""
	if locs, ok := file["Location"].([]any); ok {
		for _, l := range locs {
			lo, ok := l.(object)
			if !ok {
				continue
			}
			if p := str(lo, "PublicationUri"); p != "" && published == "" {
				published = p
			}
			uri := str(lo, "Uri")
			if uri == "" {
				continue
			}
			if local == "" || strings.HasPrefix(strings.ToLower(str(lo, "Language")), "en") {
				local = uri
			}
		}
	}
	if local == "" {
		detail := "the registry file " + best + " names no copy on the BMC"
		if published != "" {
			detail += "; it is published at " + clipText(published, 200) + ", which is another host and was not asked"
		}
		missing(detail)
		return
	}
	r.fetch(ClassRegistry, local)
}

// logServices reads the logs below a LogServices collection.
func (r *run) logServices(uri string) {
	r.reach(ClassLogService, ClassLogEntries)
	if uri == "" || r.stopped != "" {
		return
	}
	r.offered[ClassLogService], r.offered[ClassLogEntries] = true, true
	for _, m := range r.collection(ClassLogService, uri, collOpts{max: maxLogs}) {
		svc, ok := r.member(ClassLogService, m)
		if !ok {
			continue
		}
		entries := link(svc, "Entries")
		if entries == "" {
			continue
		}
		if why := r.skipLog(svc); why != "" {
			r.record(Resource{URI: entries, Class: ClassLogEntries, Status: StatusSkipped, Detail: why})
			continue
		}
		r.entries(entries)
	}
}

// skipLog says why a log is not read, or "".
func (r *run) skipLog(svc object) string {
	id := strings.ToLower(idOf(svc))
	switch {
	case r.opts.MaxLogEntries < 0:
		return "the run was asked to read no logs"
	case r.logs[id]:
		return "" // named for this run
	}
	if what, ok := skippedLogs[id]; ok {
		return "holds " + what + ", no hardware events; name it with --log to read it"
	}
	// A log that says what it is for, and is for nothing but logins or
	// telemetry.
	if purposes, ok := svc["LogPurposes"].([]any); ok && len(purposes) > 0 {
		for _, p := range purposes {
			if s, _ := p.(string); s != "Security" && s != "Telemetry" {
				return ""
			}
		}
		return "its purpose is security or telemetry, no hardware events; name it with --log to read it"
	}
	return ""
}

// entries reads the entries of one log.
func (r *run) entries(uri string) {
	members := r.collection(ClassLogEntries, uri, collOpts{max: r.opts.MaxLogEntries, entries: true})
	// A log that lists its entries as links: one request each, up to a
	// bound.
	single := 0
	for _, m := range members {
		if inline(m) || r.stopped != "" {
			continue
		}
		if single == maxSingleEntries {
			r.note("%s lists its entries as links and does not expand them: the first %d were asked for one by one, the others were not read", uri, maxSingleEntries)
			break
		}
		single++
		r.fetch(ClassLogEntries, str(m, "@odata.id"))
	}
}

// managerTime reads the clock of a Manager: DateTime, which carries its
// offset or is completed by DateTimeLocalOffset.
func managerTime(mgr object) (time.Time, bool) {
	dt := str(mgr, "DateTime")
	if dt == "" {
		return time.Time{}, false
	}
	for _, v := range []string{dt, dt + str(mgr, "DateTimeLocalOffset"), dt + "Z"} {
		if t, err := time.Parse(time.RFC3339, v); err == nil && t.Year() >= 2000 {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// expandQuery picks the $expand query a service supports for the members
// of a collection.
func expandQuery(q object) string {
	if q == nil {
		return ""
	}
	form := ""
	switch {
	case boolean(q, "NoLinks"):
		form = "."
	case boolean(q, "ExpandAll"):
		form = "*"
	default:
		return ""
	}
	if boolean(q, "Levels") {
		form += "($levels=1)"
	}
	return "$expand=" + form
}

// status turns a failed request into the status of the manifest.
func status(e *client.Error) string {
	switch e.Kind {
	case client.KindUnsupported:
		return StatusUnsupported
	case client.KindDenied:
		return StatusDenied
	case client.KindTimeout:
		return StatusTimeout
	}
	return StatusError
}

// get sends one request and keeps the books of the run: the failures in a
// row that end it, and whether the account was accepted.
func (r *run) get(uri string) (*client.Response, *client.Error) {
	if r.stopped != "" {
		return nil, &client.Error{Kind: client.KindCanceled, Method: http.MethodGet, URI: uri, Detail: r.stopped}
	}
	res, err := r.cl.Get(r.ctx, uri)
	if err == nil {
		r.unreachable, r.unauthorized = 0, 0
		if uri != ServiceRoot {
			r.authed = true
		}
		return res, nil
	}
	var e *client.Error
	if !errors.As(err, &e) {
		e = &client.Error{Method: http.MethodGet, URI: uri, Detail: err.Error()}
	}
	switch {
	case e.Kind == client.KindCanceled:
		r.stop(r.endReason())
	case e.Kind == client.KindTLS:
		r.stop(e.Detail)
	case e.Status == http.StatusUnauthorized:
		r.unauthorized++
		switch {
		case !r.authed && r.cl.AuthMode() == "none":
			r.stop("the BMC asks for an account (401 for " + e.URI + "): name one with --user")
		case !r.authed:
			r.stop("the BMC refused the account (401 for " + e.URI + "); nothing more was asked, so that a wrong password does not lock the account")
		case r.unauthorized >= maxUnauthorized:
			r.stop(fmt.Sprintf("the BMC answered 401 to %d requests in a row: it no longer accepts the account", r.unauthorized))
		}
	case e.Unreachable:
		r.unreachable++
		if r.unreachable >= maxUnreachable {
			n := 0
			for _, res := range r.m.Resources {
				if res.Status == StatusOK {
					n++
				}
			}
			r.stop(fmt.Sprintf("the BMC stopped answering after %d resources (%s)", n, e.Detail))
		}
	default:
		r.unreachable, r.unauthorized = 0, 0
	}
	return nil, e
}

// parse reads the body of an answer as a JSON object, with the secrets of
// the run taken out first.
func (r *run) parse(res *client.Response) (object, []byte, error) {
	data, n := r.cl.Scrub(res.Body)
	r.m.SecretsRemoved += n
	obj, err := parseObject(data)
	if err != nil {
		what := res.ContentType
		if what == "" {
			what = "no content type"
		}
		return nil, nil, fmt.Errorf("the answer is not a JSON object (%s, %d bytes): %v", what, len(res.Body), err)
	}
	return obj, data, nil
}

func parseObject(data []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, errors.New(clipText(err.Error(), 120))
	}
	obj, ok := v.(object)
	if !ok {
		return nil, errors.New("it is another kind of JSON value")
	}
	if dec.More() {
		return nil, errors.New("text follows the object")
	}
	return obj, nil
}

// save writes the answer for a URI and accounts for its size.
func (r *run) save(uri string, data []byte) (string, error) {
	if r.written+int64(len(data)) > r.opts.MaxTotalBytes {
		r.stop(fmt.Sprintf("the collection reached its size limit of %d bytes", r.opts.MaxTotalBytes))
		return "", errors.New("the size limit of the collection is reached")
	}
	file, err := r.tree.write(uri, data)
	if err != nil {
		return "", err
	}
	r.written += int64(len(data))
	return file, nil
}

// pathOf returns a URI without its query and fragment.
func pathOf(uri string) string {
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		return uri[:i]
	}
	return uri
}

// fetch asks for one resource, writes it and lists it in the manifest.
func (r *run) fetch(class, ref string) (object, bool) { return r.fetchAs(class, ref, false) }

// fetchAs is fetch for a resource that only leads to the one the class is
// about (aux): it is listed, and counts for the class only when it fails.
func (r *run) fetchAs(class, ref string, aux bool) (object, bool) {
	if r.stopped != "" || ref == "" {
		return nil, false
	}
	uri, err := r.cl.Resolve(ref)
	if err != nil {
		r.record(Resource{URI: clipText(ref, 200), Class: class, Status: StatusError, Detail: "the link is not followed: " + err.Error()})
		return nil, false
	}
	uri = pathOf(uri)
	if r.done[uri] {
		return nil, false
	}
	r.done[uri] = true
	res, e := r.get(uri)
	if e != nil {
		r.record(Resource{URI: uri, Class: class, Status: status(e), HTTP: e.Status, Attempts: e.Attempts, Detail: failureText(e)})
		return nil, false
	}
	out := Resource{URI: uri, Class: class, Status: StatusOK, HTTP: res.Status, Attempts: res.Attempts, Millis: res.Elapsed.Milliseconds()}
	if aux {
		out.Class = auxClass(class)
	}
	obj, data, err := r.parse(res)
	if err == nil {
		out.File, err = r.save(uri, data)
		out.Bytes = len(data)
	}
	if err != nil {
		out.Class, out.Status, out.Detail, out.Bytes = class, StatusError, err.Error(), 0
		r.record(out)
		return nil, false
	}
	r.record(out)
	return obj, true
}

// auxClass names the class of a resource that only leads to the resources
// of class. It is listed in the manifest and left out of the summary of
// the class, which is about the resource a reader wants.
func auxClass(class string) string { return class + "-lookup" }

// failureText says why a request failed, without the method and the URI
// the manifest lists next to it.
func failureText(e *client.Error) string {
	text := e.Detail
	if e.Status != 0 {
		text = strings.TrimSuffix(fmt.Sprintf("%d %s: %s", e.Status, http.StatusText(e.Status), e.Detail), ": ")
	}
	if e.Attempts > 1 {
		text += fmt.Sprintf(" (sent %d times)", e.Attempts)
	}
	return text
}

// collOpts say how a collection is read.
type collOpts struct {
	// max bounds the members read.
	max int
	// expand asks the service to put the members into the answer, when it
	// says it can.
	expand bool
	// entries marks the entries of a log, whose members are not written
	// to files of their own.
	entries bool
}

// collection reads a collection, page by page, writes it as one document
// and returns its members: each is the member itself when the service put
// it into the answer, and a link otherwise.
func (r *run) collection(class, ref string, o collOpts) []object {
	return r.collectionAs(class, ref, o, false)
}

func (r *run) collectionAs(class, ref string, o collOpts, aux bool) []object {
	if r.stopped != "" || ref == "" {
		return nil
	}
	uri, err := r.cl.Resolve(ref)
	if err != nil {
		r.record(Resource{URI: clipText(ref, 200), Class: class, Status: StatusError, Detail: "the link is not followed: " + err.Error()})
		return nil
	}
	uri = pathOf(uri)
	if r.done[uri] {
		return nil
	}
	r.done[uri] = true
	out := Resource{URI: uri, Class: class, Status: StatusOK}
	if aux {
		out.Class = auxClass(class)
	}
	fail := func(e *client.Error) {
		out.Class, out.Status, out.HTTP, out.Attempts, out.Detail = class, status(e), e.Status, e.Attempts, failureText(e)
	}

	var first object
	var res *client.Response
	var e *client.Error
	if o.expand && r.expand != "" && r.expandFailed < 2 {
		if res, e = r.get(uri + "?" + r.expand); e != nil && r.stopped == "" && e.Status != 0 &&
			e.Kind != client.KindDenied && e.Status != http.StatusNotFound {
			// The service says it expands and refuses here: ask plainly.
			r.expandFailed++
			r.note("%s refused %s (%s); the collection was read without it", uri, r.expand, failureText(e))
			res, e = r.get(uri)
		}
	} else {
		res, e = r.get(uri)
	}
	if e != nil {
		fail(e)
		r.record(out)
		return nil
	}
	out.HTTP, out.Attempts, out.Pages = res.Status, res.Attempts, 1
	out.Millis = res.Elapsed.Milliseconds()
	first, _, err = r.parse(res)
	if err != nil {
		out.Class, out.Status, out.Detail = class, StatusError, err.Error()
		r.record(out)
		return nil
	}

	members := []object{}
	total := 0
	// A member is taken once. A service that ignores the query of a link
	// to the next page answers with the first page again.
	known := map[string]bool{}
	take := func(page object) (fresh int, more bool) {
		list, _ := page["Members"].([]any)
		for _, item := range list {
			m, ok := item.(object)
			if !ok {
				continue
			}
			if id := str(m, "@odata.id"); id != "" {
				if known[id] {
					continue
				}
				known[id] = true
			}
			total++
			fresh++
			if len(members) < o.max {
				members = append(members, m)
			}
		}
		return fresh, len(members) < o.max
	}
	next := func(page object) string {
		if n := str(page, "Members@odata.nextLink"); n != "" {
			return n
		}
		return str(page, "@odata.nextLink")
	}
	_, more := take(first)
	seen := map[string]bool{uri: true, uri + "?" + r.expand: true}
	for link := next(first); link != "" && more && r.stopped == ""; {
		if out.Pages >= maxPages {
			out.Detail = fmt.Sprintf("the collection has more than %d pages; the rest was not read", maxPages)
			break
		}
		puri, err := r.cl.Resolve(link)
		if err != nil || seen[puri] {
			out.Class, out.Status, out.Detail = class, StatusError, fmt.Sprintf("read %d members; the link to the next page is not followed", len(members))
			break
		}
		seen[puri] = true
		pres, pe := r.get(puri)
		if pe != nil {
			out.Class, out.Status, out.HTTP = class, status(pe), pe.Status
			out.Detail = fmt.Sprintf("read %d members; page %d failed: %s", len(members), out.Pages+1, failureText(pe))
			break
		}
		out.Pages++
		out.Millis += pres.Elapsed.Milliseconds()
		page, _, err := r.parse(pres)
		if err != nil {
			out.Class, out.Status = class, StatusError
			out.Detail = fmt.Sprintf("read %d members; page %d: %v", len(members), out.Pages, err)
			break
		}
		var fresh int
		if fresh, more = take(page); fresh == 0 {
			out.Detail = fmt.Sprintf("page %d repeats members of the pages before it: the service does not follow its own link to the next page; %d members read", out.Pages, len(members))
			r.note("%s: %s", uri, out.Detail)
			break
		}
		link = next(page)
	}
	if _, has := first["Members"]; !has {
		r.note("%s has no Members: it was read as a collection without any", uri)
	}
	if count, ok := integer(first, "Members@odata.count"); ok && count > int64(total) {
		total = int(count)
	}
	if total > len(members) && out.Status == StatusOK {
		what := "members"
		if o.entries {
			what = "entries"
		}
		out.Detail = fmt.Sprintf("%d of %d %s read: the limit of this run is %d", len(members), total, what, o.max)
		r.note("%s: %s", uri, out.Detail)
	}
	out.Members = len(members)

	// One document for the collection: the first page with the members of
	// every page.
	list := make([]any, len(members))
	for i, m := range members {
		list[i] = m
	}
	first["Members"] = list
	delete(first, "Members@odata.nextLink")
	delete(first, "@odata.nextLink")
	data, err := json.MarshalIndent(first, "", "    ")
	if err == nil {
		out.File, err = r.save(uri, append(data, '\n'))
		out.Bytes = len(data) + 1
	}
	if err != nil {
		out.Class, out.Status, out.Detail, out.Bytes = class, StatusError, err.Error(), 0
		r.record(out)
		return nil
	}
	r.record(out)
	return members
}

// member returns a member of a collection as a resource: the member
// itself when the service put it into the collection, which is then
// written to its own file as well, and the answer to a request otherwise.
func (r *run) member(class string, m object) (object, bool) {
	id := str(m, "@odata.id")
	if !inline(m) {
		return r.fetch(class, id)
	}
	if r.stopped != "" {
		return nil, false
	}
	uri, err := r.cl.Resolve(id)
	if err != nil {
		// A member without a place of its own is used as it is.
		return m, true
	}
	uri = pathOf(uri)
	if r.done[uri] {
		return m, true
	}
	r.done[uri] = true
	out := Resource{URI: uri, Class: class, Status: StatusOK, Via: "expand"}
	data, err := json.MarshalIndent(m, "", "    ")
	if err == nil {
		out.File, err = r.save(uri, append(data, '\n'))
		out.Bytes = len(data) + 1
	}
	if err != nil {
		out.Status, out.Detail, out.Bytes = StatusError, err.Error(), 0
		r.record(out)
		return m, r.stopped == ""
	}
	r.record(out)
	return m, true
}

// inline reports whether a member of a collection is the resource itself
// and not a link to it. A link is an object with "@odata.id" and at most a
// name or a type beside it.
func inline(m object) bool {
	if len(m) <= 3 {
		return false
	}
	_, hasID := m["Id"]
	_, hasType := m["@odata.type"]
	return hasID || hasType
}

// Helpers to read a resource.

func str(o object, key string) string {
	s, _ := o[key].(string)
	return strings.TrimSpace(s)
}

func child(o object, key string) object {
	c, _ := o[key].(object)
	return c
}

func boolean(o object, key string) bool {
	b, _ := o[key].(bool)
	return b
}

func integer(o object, key string) (int64, bool) {
	n, ok := o[key].(json.Number)
	if !ok {
		return 0, false
	}
	i, err := n.Int64()
	return i, err == nil
}

// link returns the URI a property of a resource points to.
func link(o object, key string) string { return str(child(o, key), "@odata.id") }

// idOf returns the Id of a resource, or the last part of its URI.
func idOf(o object) string {
	if id := str(o, "Id"); id != "" {
		return id
	}
	return lastSegment(str(o, "@odata.id"))
}

func lastSegment(uri string) string {
	seg := path.Base(strings.TrimRight(pathOf(uri), "/"))
	if un, err := url.PathUnescape(seg); err == nil {
		seg = un
	}
	if seg == "." || seg == "/" {
		return ""
	}
	return seg
}

func clipText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// SkippedLogs lists the logs a run does not read unless they are named,
// by the Id of their log service, with what they hold.
func SkippedLogs() map[string]string {
	out := make(map[string]string, len(skippedLogs))
	for k, v := range skippedLogs {
		out[k] = v
	}
	return out
}

// SkippedLogIDs returns the Ids of SkippedLogs in order.
func SkippedLogIDs() []string {
	ids := make([]string, 0, len(skippedLogs))
	for id := range skippedLogs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
