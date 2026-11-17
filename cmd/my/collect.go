package main

import (
	"bufio"
	"context"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/redfish/client"
	"github.com/agenticode/my/internal/redfish/collect"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/view"
)

// exitPartial is the exit code of "my collect" when a collection was
// written and lacks something that was denied, timed out or failed.
const exitPartial = 1

// Limits of what "my collect" takes.
const (
	maxTargets  = 10000
	maxParallel = 32
)

// Environment variables of the BMC account.
const (
	envRedfishUser     = "MY_REDFISH_USER"
	envRedfishPassword = "MY_REDFISH_PASSWORD"
)

// bmcTransport replaces the network of every BMC client the commands make.
// It is nil outside tests.
var bmcTransport http.RoundTripper

// list is a flag that may be given several times.
type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

// bmcFlags are the flags that say how a BMC is reached.
type bmcFlags struct {
	bmc          string
	user         string
	passwordFile string
	session      bool
	caFile       string
	fingerprint  string
	insecure     bool
	timeout      string
	retries      int
	// deadline bounds the collection in a command that scans the host;
	// "my collect" has a flag of its own for it.
	deadline string
}

func (b *bmcFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&b.bmc, "bmc", "", "")
	fs.StringVar(&b.user, "user", "", "")
	fs.StringVar(&b.passwordFile, "password-file", "", "")
	fs.BoolVar(&b.session, "session", false, "")
	fs.StringVar(&b.caFile, "ca-file", "", "")
	fs.StringVar(&b.fingerprint, "fingerprint", "", "")
	fs.BoolVar(&b.insecure, "insecure", false, "")
	fs.StringVar(&b.timeout, "timeout", client.DefaultTimeout.String(), "")
	fs.IntVar(&b.retries, "retries", client.DefaultRetries, "")
}

// collectOptions are the flags of "my collect".
type collectOptions struct {
	*options
	bf       bmcFlags
	targets  string
	out      string
	force    bool
	inputs   bool
	deadline string
	maxLog   int
	logs     list
	noLogs   bool
	parallel int
	quiet    bool
	sudo     bool
}

func newCollectOptions() *collectOptions {
	o := &collectOptions{options: &options{name: "my collect", fs: flag.NewFlagSet("my collect", flag.ContinueOnError), color: term.ColorAuto}}
	o.fs.SetOutput(io.Discard)
	o.bf.register(o.fs)
	o.fs.StringVar(&o.targets, "targets", "", "")
	o.fs.StringVar(&o.out, "o", "", "")
	o.fs.BoolVar(&o.force, "force", false, "")
	o.fs.BoolVar(&o.inputs, "with-inputs", false, "")
	o.fs.StringVar(&o.deadline, "deadline", collect.DefaultDeadline.String(), "")
	o.fs.IntVar(&o.maxLog, "max-log-entries", collect.DefaultMaxLogEntries, "")
	o.fs.Var(&o.logs, "log", "")
	o.fs.BoolVar(&o.noLogs, "no-logs", false, "")
	o.fs.IntVar(&o.parallel, "parallel", 4, "")
	o.fs.BoolVar(&o.quiet, "quiet", false, "")
	o.fs.BoolVar(&o.sudo, "sudo", false, "")
	o.fs.Var(&o.maps, "event-map", "")
	o.fs.StringVar(&o.lang, "lang", "", "")
	o.fs.BoolVar(&o.asJSON, "json", false, "")
	o.fs.IntVar(&o.width, "width", 0, "")
	o.fs.BoolVar(&o.help, "help", false, "")
	o.fs.BoolVar(&o.help, "h", false, "")
	return o
}

// target is one BMC to collect from.
type target struct {
	address string
	name    string
	user    string
	// password is set once it is known; passwordFile names where to read
	// it for this BMC alone.
	password     string
	passwordFile string
	dir          string
}

// readTargets reads a targets file: one BMC per line, its address and
// then name=, user= and password-file= in any order. Empty lines and lines
// that start with # are skipped.
func readTargets(path string) ([]target, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []target
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		t := target{address: fields[0]}
		if _, err := client.ParseTarget(t.address); err != nil {
			return nil, fmt.Errorf("%s line %d: %v", path, n, err)
		}
		for _, kv := range fields[1:] {
			k, v, ok := strings.Cut(kv, "=")
			switch {
			case !ok || v == "":
				return nil, fmt.Errorf("%s line %d: %q: want name=NAME, user=NAME or password-file=FILE after the address", path, n, kv)
			case k == "name":
				t.name = v
			case k == "user":
				t.user = v
			case k == "password-file":
				t.passwordFile = v
			case k == "password":
				return nil, fmt.Errorf("%s line %d: a password does not go into this file; name a file that holds it with password-file=FILE", path, n)
			default:
				return nil, fmt.Errorf("%s line %d: unknown setting %q (name, user, password-file)", path, n, k)
			}
		}
		out = append(out, t)
		if len(out) > maxTargets {
			return nil, fmt.Errorf("%s: more than %d BMCs", path, maxTargets)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s names no BMC", path)
	}
	return out, nil
}

// passwordFromFile reads the first line of a password file.
func passwordFromFile(path string) (pw string, loose bool, err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	if fi.Size() > 4096 {
		return "", false, errors.New("larger than a password")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	line, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimRight(line, "\r"), fi.Mode().Perm()&0o077 != 0, nil
}

// bmcCredentials fills in the account of every target: its own user and
// password file, else the user of the command line or of the environment
// with the password from --password-file, the environment, or the
// terminal. name is the command, for what it says on the standard error.
func bmcCredentials(ctx context.Context, env *cli.Env, lang i18n.Lang, name string, bf *bmcFlags, targets []target) error {
	user := bf.user
	if user == "" {
		user = env.Getenv(envRedfishUser)
	}
	shared, haveShared := "", false
	warned := map[string]bool{}
	fromFile := func(path string) (string, error) {
		pw, loose, err := passwordFromFile(path)
		if err != nil {
			return "", fmt.Errorf("--password-file %w", err)
		}
		if loose && !warned[path] {
			warned[path] = true
			fmt.Fprintln(env.Stderr, name+": "+say(lang, "collect.loose_file", "file", path))
		}
		return pw, nil
	}
	for i := range targets {
		t := &targets[i]
		if t.user == "" {
			t.user = user
		}
		if t.user == "" {
			if t.passwordFile != "" {
				return errors.New(say(lang, "err.collect_no_user"))
			}
			if bf.session {
				return errors.New(say(lang, "err.collect_session_user"))
			}
			continue // no account: the requests carry no credentials
		}
		if t.passwordFile != "" {
			pw, err := fromFile(t.passwordFile)
			if err != nil {
				return err
			}
			t.password = pw
			continue
		}
		if !haveShared {
			switch {
			case bf.passwordFile != "":
				pw, err := fromFile(bf.passwordFile)
				if err != nil {
					return err
				}
				shared = pw
			case env.Getenv(envRedfishPassword) != "":
				shared = env.Getenv(envRedfishPassword)
			default:
				pw, err := promptPassword(ctx, env, say(lang, "collect.prompt", "user", t.user))
				if err != nil {
					return errors.New(say(lang, "err.collect_password", "user", t.user))
				}
				shared = pw
			}
			haveShared = true
		}
		t.password = shared
	}
	if user == "" && (bf.passwordFile != "" || env.Getenv(envRedfishPassword) != "") {
		for _, t := range targets {
			if t.user == "" {
				return errors.New(say(lang, "err.collect_no_user"))
			}
		}
	}
	return nil
}

// runCollect is "my collect redfish" and "my collect support".
func runCollect(ctx context.Context, env *cli.Env, args []string) int {
	o := newCollectOptions()
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "collect", nil)
	}
	switch {
	case len(pos) == 0:
		return o.usageError(env, lang, "%s", say(lang, "err.collect_what"))
	case pos[0] == "support":
		return o.support(env, lang, pos[1:])
	case pos[0] == "host":
		if len(pos) > 1 {
			return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[1]))
		}
		return o.host(ctx, env, lang)
	case o.sudo:
		return o.usageError(env, lang, "%s", say(lang, "err.collect_sudo"))
	case pos[0] != "redfish":
		return o.usageError(env, lang, "%s", say(lang, "err.collect_unknown", "what", pos[0]))
	case len(pos) > 1:
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[1]))
	}
	if err := useEventMaps(env, lang, o.maps); err != nil {
		return o.usageError(env, lang, "%s", errText(lang, err))
	}
	defer useEventMaps(env, lang, nil)
	return o.redfish(ctx, env, lang)
}

// clientConfig turns the flags of a BMC connection into the settings of a
// client, without address and account.
func (b *bmcFlags) clientConfig(lang i18n.Lang) (client.Config, error) {
	timeout, err := time.ParseDuration(b.timeout)
	if err != nil || timeout < time.Second {
		return client.Config{}, errors.New(say(lang, "err.collect_time", "flag", "--timeout", "value", b.timeout))
	}
	if b.retries < 0 || b.retries > 20 {
		return client.Config{}, errors.New(say(lang, "err.collect_retries", "value", b.retries))
	}
	set := 0
	for _, on := range []bool{b.insecure, b.fingerprint != "", b.caFile != ""} {
		if on {
			set++
		}
	}
	if set > 1 {
		return client.Config{}, errors.New(say(lang, "err.collect_tls"))
	}
	if b.fingerprint != "" {
		if _, err := client.ParseFingerprint(b.fingerprint); err != nil {
			return client.Config{}, errors.New(say(lang, "err.collect_fingerprint"))
		}
	}
	if b.caFile != "" {
		// Read here so that a wrong file is a mistake of the command line,
		// said before any BMC is asked; the client reads it again.
		pem, err := os.ReadFile(b.caFile)
		if err != nil {
			return client.Config{}, fmt.Errorf("--ca-file %w", err)
		}
		if !x509.NewCertPool().AppendCertsFromPEM(pem) {
			return client.Config{}, errors.New(say(lang, "err.collect_ca_pem", "file", b.caFile))
		}
	}
	retries := b.retries
	if retries == 0 {
		retries = -1 // for the client 0 means its default
	}
	return client.Config{Session: b.session, CAFile: b.caFile, Fingerprint: b.fingerprint, Insecure: b.insecure,
		Timeout: timeout, Retries: retries, UserAgent: toolName(), Transport: bmcTransport}, nil
}

// collected is the outcome for one BMC.
type collected struct {
	Name     string            `json:"name"`
	Address  string            `json:"address"`
	Dir      string            `json:"dir,omitzero"`
	Host     string            `json:"host,omitzero"`
	Manifest *collect.Manifest `json:"manifest,omitzero"`
	// Error is set when the collection could not be started or written.
	Error string `json:"error,omitzero"`
}

// redfish is "my collect redfish".
func (o *collectOptions) redfish(ctx context.Context, env *cli.Env, lang i18n.Lang) int {
	switch {
	case o.bf.bmc == "" && o.targets == "":
		return o.usageError(env, lang, "%s", say(lang, "err.collect_bmc"))
	case o.bf.bmc != "" && o.targets != "":
		return o.usageError(env, lang, "%s", say(lang, "err.collect_both"))
	case o.out == "":
		return o.usageError(env, lang, "%s", say(lang, "err.collect_out"))
	case o.parallel < 1 || o.parallel > maxParallel:
		return o.usageError(env, lang, "%s", say(lang, "err.collect_parallel", "value", o.parallel, "max", maxParallel))
	case o.maxLog < 1:
		return o.usageError(env, lang, "%s", say(lang, "err.collect_entries", "value", o.maxLog))
	}
	deadline, err := time.ParseDuration(o.deadline)
	if err != nil || deadline < time.Second {
		return o.usageError(env, lang, "%s", say(lang, "err.collect_time", "flag", "--deadline", "value", o.deadline))
	}
	base, err := o.bf.clientConfig(lang)
	if err != nil {
		return o.usageError(env, lang, "%s", errText(lang, err))
	}

	var targets []target
	if o.targets != "" {
		if targets, err = readTargets(o.targets); err != nil {
			return o.usageError(env, lang, "--targets %s", errText(lang, err))
		}
	} else {
		if _, err := client.ParseTarget(o.bf.bmc); err != nil {
			return o.usageError(env, lang, "--bmc: %v", err)
		}
		targets = []target{{address: o.bf.bmc}}
	}
	// Names: what a BMC is called in the output, and its directory.
	used := map[string]string{}
	for i := range targets {
		t := &targets[i]
		u, _ := client.ParseTarget(t.address)
		if t.name == "" {
			t.name = u.Host
		}
		dir := collect.SafeName(t.name)
		if other, taken := used[strings.ToLower(dir)]; taken {
			return o.usageError(env, lang, "%s", say(lang, "err.collect_same_name", "a", other, "b", t.address, "dir", dir))
		}
		used[strings.ToLower(dir)] = t.address
		t.dir = dir
		if u.Scheme == "http" && !o.bf.insecure {
			return o.usageError(env, lang, "%s", say(lang, "err.collect_http", "bmc", t.address))
		}
	}

	// Where the answers go: a directory, or a case bundle.
	asCase := strings.EqualFold(filepath.Ext(o.out), bundle.Extension)
	if o.inputs && !asCase {
		return o.usageError(env, lang, "%s", say(lang, "err.collect_inputs"))
	}
	work := o.out
	if asCase {
		if code, ok := o.checkTarget(env, lang, o.out, o.force); !ok {
			return code
		}
		tmp, err := os.MkdirTemp("", "my-collect-")
		if err != nil {
			return o.failure(env, err)
		}
		defer os.RemoveAll(tmp)
		work = tmp
	} else if why, args := newDirectory(o.out); why != "" {
		return o.usageError(env, lang, "%s", say(lang, "err.collect_dir", "dir", o.out, "why", say(lang, why, args...)))
	}
	if err := bmcCredentials(ctx, env, lang, o.name, &o.bf, targets); err != nil {
		return o.usageError(env, lang, "%s", errText(lang, err))
	}
	if o.bf.insecure {
		fmt.Fprintln(env.Stderr, "my collect: "+say(lang, "collect.insecure"))
	}

	// One BMC is asked one request at a time; several BMCs are asked side
	// by side, a few at once.
	now := clock(env)
	results := make([]collected, len(targets))
	var say1 sync.Mutex
	progress := func(format string, a ...any) {
		if o.quiet {
			return
		}
		say1.Lock()
		defer say1.Unlock()
		fmt.Fprintf(env.Stderr, format+"\n", a...)
	}
	sem := make(chan struct{}, o.parallel)
	var wg sync.WaitGroup
	for i := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			t := targets[i]
			res := &results[i]
			res.Name, res.Address = t.name, t.address
			if ctx.Err() != nil {
				res.Error = say(lang, "collect.not_started")
				return
			}
			cfg := base
			cfg.BaseURL, cfg.User, cfg.Password = t.address, t.user, t.password
			dir := work
			if len(targets) > 1 || o.targets != "" || asCase {
				dir = filepath.Join(work, t.dir)
			}
			read := 0
			opts := collect.Options{Client: cfg, Name: t.name, Tool: toolName(), Deadline: deadline, MaxLogEntries: o.maxLog, Logs: o.logs,
				Progress: func(r collect.Resource) {
					switch r.Status {
					case collect.StatusOK:
						if read++; read%25 == 0 {
							progress("%s: %s", t.name, say(lang, "collect.progress", "n", read))
						}
					case collect.StatusSkipped:
					default:
						progress("%s: %s: %s: %s", t.name, r.URI, say(lang, "collect.state."+r.Status), r.Detail)
					}
				}}
			if o.noLogs {
				opts.MaxLogEntries = -1
			}
			if !now.IsZero() {
				opts.Now = func() time.Time { return now }
			}
			m, err := collect.Run(ctx, dir, opts)
			res.Manifest = m
			if err != nil {
				res.Error = err.Error()
				return
			}
			res.Dir = dir
		}(i)
	}
	wg.Wait()

	// The outcome: nothing came back from any BMC, something is missing,
	// or everything is there.
	exit, any := cli.ExitOK, false
	for _, r := range results {
		switch {
		case r.Manifest == nil || r.Manifest.Counts[collect.StatusOK] == 0:
			exit = exitPartial
		case r.Manifest.Partial() || r.Error != "":
			exit, any = exitPartial, true
		default:
			any = true
		}
	}
	if !any {
		exit = cli.ExitFailure
	}

	var written *bundle.Written
	var hosts []string
	if asCase && any {
		if written, hosts, err = o.writeCase(ctx, env, lang, results); err != nil {
			return o.failure(env, errors.New(targetText(lang, err)))
		}
	}
	if o.asJSON {
		doc := struct {
			Out         string           `json:"out"`
			Kind        string           `json:"kind"`
			ExitCode    int              `json:"exit_code"`
			Collections []collected      `json:"collections"`
			Bundle      *bundle.Manifest `json:"bundle,omitzero"`
		}{Out: o.out, Kind: "directory", ExitCode: exit, Collections: results}
		if asCase {
			doc.Kind = "bundle"
			for i := range doc.Collections {
				doc.Collections[i].Dir = "" // a directory that is gone
			}
			if written != nil {
				doc.Bundle = &written.Manifest
			}
		}
		if err := writeJSON(env.Stdout, doc); err != nil {
			return o.failure(env, err)
		}
		return exit
	}

	st := o.style(env, lang)
	w := env.Stdout
	for _, r := range results {
		collectSummary(w, lang, st, r)
	}
	switch {
	case !any:
		fmt.Fprintln(env.Stderr, "my collect: "+say(lang, "collect.nothing"))
	case asCase:
		fmt.Fprintln(w, say(lang, "bundle.written", "file", written.Path, "size", sizeText(written.Bytes)))
		fmt.Fprintf(w, "  %s\n", say(lang, "collect.case_hosts", "n", len(hosts), "hosts", strings.Join(hosts, ", ")))
		fmt.Fprintln(w, say(lang, "bundle.open", "file", written.Path))
	default:
		fmt.Fprintln(w, say(lang, "collect.written", "dir", o.out))
		fmt.Fprintln(w, say(lang, "collect.next_import", "dir", importHint(o.out, results, o.targets != "")))
		fmt.Fprintln(w, say(lang, "collect.next_support", "dir", o.out))
	}
	return exit
}

// importHint names a collection to hand to "my import": the directory
// itself, or the directory of one BMC when there are several.
func importHint(out string, results []collected, perBMC bool) string {
	if !perBMC {
		return out
	}
	for _, r := range results {
		if r.Dir != "" {
			return r.Dir
		}
	}
	return filepath.Join(out, "NAME")
}

// newDirectory checks that a collection can be written to dir: it does
// not exist yet, or it is an empty directory. What stands against it is
// returned as the code of a message and its arguments.
func newDirectory(dir string) (code string, args []any) {
	fi, err := os.Stat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		parent := filepath.Dir(dir)
		if pfi, perr := os.Stat(parent); perr != nil || !pfi.IsDir() {
			return "err.no_dir", []any{"file", parent}
		}
		return "", nil
	case err != nil:
		return "err.collect_unreadable", []any{"why", cli.ErrorText(i18n.EN, err)}
	case !fi.IsDir():
		return "err.collect_file", []any{"ext", bundle.Extension}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "err.collect_unreadable", []any{"why", cli.ErrorText(i18n.EN, err)}
	}
	if len(entries) > 0 {
		return "err.collect_not_empty", nil
	}
	return "", nil
}

// collectSummary writes what came back from one BMC.
func collectSummary(w io.Writer, lang i18n.Lang, st term.Style, r collected) {
	m := r.Manifest
	head := r.Name
	if m != nil && m.BMC.Address != "" && m.BMC.Address != r.Name && !strings.HasPrefix(r.Name, m.BMC.Address) {
		head += " (" + m.BMC.Address + ")"
	}
	if m == nil {
		fmt.Fprintf(w, "%s: %s\n", head, say(lang, "collect.failed", "why", r.Error))
		return
	}
	took := m.FinishedAt.Sub(m.StartedAt).Round(time.Second)
	fmt.Fprintf(w, "%s: %s\n", head, say(lang, "collect.read", "n", m.Counts[collect.StatusOK], "size", sizeText(m.Bytes), "took", took.String(), "requests", m.Requests))
	if len(m.Systems) > 0 {
		s := m.Systems[0]
		parts := []string{strings.TrimSpace(s.Manufacturer + " " + s.Model)}
		if s.BiosVersion != "" {
			parts = append(parts, "BIOS "+s.BiosVersion)
		}
		if len(m.Managers) > 0 && m.Managers[0].FirmwareVersion != "" {
			parts = append(parts, say(lang, "collect.bmc_firmware", "version", m.Managers[0].FirmwareVersion))
		}
		if m.Service.RedfishVersion != "" {
			parts = append(parts, "Redfish "+m.Service.RedfishVersion)
		}
		fmt.Fprintf(w, "  %s\n", strings.Join(parts, ", "))
	}
	// The classes that are not whole, by what became of them.
	by := map[string][]string{}
	for _, c := range m.Classes {
		if c.State != collect.StateOK {
			by[c.State] = append(by[c.State], say(lang, "collect.class."+c.Class))
		}
	}
	for _, state := range []string{collect.StatePartial, collect.StatusDenied, collect.StatusTimeout, collect.StatusError,
		collect.StatusUnsupported, collect.StateNotOffered, collect.StatusSkipped, collect.StateNotReached} {
		if len(by[state]) == 0 {
			continue
		}
		for i, line := range st.Wrap(say(lang, "collect.state."+state)+": "+strings.Join(by[state], ", "), 4) {
			if i == 0 {
				line = "  " + strings.TrimLeft(line, " ")
			}
			fmt.Fprintln(w, line)
		}
	}
	// Logs that were left out on purpose, by name.
	var left []string
	for _, res := range m.Resources {
		if res.Class == collect.ClassLogEntries && res.Status == collect.StatusSkipped {
			left = append(left, filepath.Base(strings.TrimSuffix(res.URI, "/Entries")))
		}
	}
	if len(left) > 0 {
		for i, line := range st.Wrap(say(lang, "collect.logs_left", "logs", strings.Join(left, ", ")), 4) {
			if i == 0 {
				line = "  " + strings.TrimLeft(line, " ")
			}
			fmt.Fprintln(w, line)
		}
	}
	if m.Stopped != "" {
		for i, line := range st.Wrap(say(lang, "collect.stopped", "why", m.Stopped), 4) {
			if i == 0 {
				line = "  " + strings.TrimLeft(line, " ")
			}
			fmt.Fprintln(w, line)
		}
	}
	if m.SecretsRemoved > 0 {
		fmt.Fprintf(w, "  %s\n", say(lang, "collect.secrets", "n", m.SecretsRemoved))
	}
	if r.Error != "" {
		fmt.Fprintf(w, "  %s\n", say(lang, "collect.failed", "why", r.Error))
	}
}

// writeCase turns the collections into one case bundle: every BMC is read
// as the host it manages.
func (o *collectOptions) writeCase(ctx context.Context, env *cli.Env, lang i18n.Lang, results []collected) (*bundle.Written, []string, error) {
	tmp, err := os.MkdirTemp("", "my-collect-ledger-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	ledger, err := store.Open(ctx, filepath.Join(tmp, "ledger.db"), store.Create)
	if err != nil {
		return nil, nil, err
	}
	defer ledger.Close()
	now := clock(env)
	var hosts []string
	taken := map[string]bool{}
	for _, r := range results {
		if r.Manifest == nil || r.Dir == "" || r.Manifest.Counts[collect.StatusOK] == 0 {
			continue
		}
		// The records name the BMC they came from, and not the directory
		// they passed through on this machine.
		a := redfish.NewFS(os.DirFS(r.Dir), bmcOrigin+r.Manifest.BMC.Address, redfish.Options{Now: r.Manifest.StartedAt})
		host := strings.TrimSpace(a.HostHint())
		if host == "" || taken[strings.ToLower(host)] {
			host = r.Name
		}
		taken[strings.ToLower(host)] = true
		if _, err := ingest.Run(ctx, ledger, []adapter.Adapter{a}, ingest.Options{Host: host, Kind: model.ScanImport, Now: now}); err != nil {
			return nil, nil, err
		}
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	files, err := o.collectionFiles(ctx, results)
	if err != nil {
		return nil, nil, err
	}
	c, err := view.Open(ctx, ledger, lang, engine.Options{Now: now})
	if err != nil {
		return nil, nil, err
	}
	if err := c.Result.Save(ctx, ledger); err != nil {
		return nil, nil, err
	}
	w, err := bundle.Write(ctx, ledger, o.out, bundle.Options{Now: now, Tool: toolName(), Force: o.force, Inputs: files, WithInputs: o.inputs})
	return w, hosts, err
}

// collectionFiles are the files of the collections a bundle is made from:
// the manifest of each collection, and with --with-inputs every answer as
// well. A name starts with the name of the collection.
func (o *collectOptions) collectionFiles(ctx context.Context, results []collected) ([]bundle.InputFile, error) {
	var out []bundle.InputFile
	for _, r := range results {
		if r.Manifest == nil || r.Dir == "" {
			continue
		}
		if !o.inputs {
			out = append(out, bundle.InputFile{Name: r.Name + "/" + collect.ManifestName, Path: filepath.Join(r.Dir, collect.ManifestName)})
			continue
		}
		// The collection is this run's own directory, bounded by the
		// limits of the collector: every file of it is taken.
		err := filepath.WalkDir(r.Dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			if !d.Type().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(r.Dir, p)
			if err != nil {
				return err
			}
			out = append(out, bundle.InputFile{Name: r.Name + "/" + filepath.ToSlash(rel), Path: p})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// support is "my collect support DIR...": which BMC gave which class of
// resources, from the manifests of collections.
func (o *collectOptions) support(env *cli.Env, lang i18n.Lang, dirs []string) int {
	if len(dirs) == 0 {
		return o.usageError(env, lang, "%s", say(lang, "err.collect_support"))
	}
	manifests, err := collect.FindManifests(dirs...)
	if err != nil {
		return o.failure(env, err)
	}
	table := collect.NewSupport(manifests)
	if o.asJSON {
		if err := writeJSON(env.Stdout, table); err != nil {
			return o.failure(env, err)
		}
		return cli.ExitOK
	}
	writeSupport(env.Stdout, lang, o.style(env, lang), table)
	return cli.ExitOK
}

// writeSupport draws a support table: one column per group of BMCs, one
// row per class of resources.
func writeSupport(w io.Writer, lang i18n.Lang, st term.Style, s *collect.Support) {
	letter := func(i int) string {
		if i < 26 {
			return string(rune('A' + i))
		}
		return string(rune('A'+i/26-1)) + string(rune('A'+i%26))
	}
	for i, g := range s.Groups {
		var what []string
		if v := strings.TrimSpace(g.Vendor + " " + g.Model); v != "" {
			what = append(what, v)
		}
		if g.Firmware != "" {
			what = append(what, say(lang, "collect.bmc_firmware", "version", g.Firmware))
		}
		if g.BiosVersion != "" {
			what = append(what, "BIOS "+g.BiosVersion)
		}
		if g.RedfishVersion != "" {
			what = append(what, "Redfish "+g.RedfishVersion)
		}
		if len(what) == 0 {
			what = append(what, say(lang, "collect.unknown_machine"))
		}
		names := make([]string, 0, len(g.BMCs))
		for j, b := range g.BMCs {
			if j == 6 && len(g.BMCs) > 8 {
				names = append(names, say(lang, "collect.more", "n", len(g.BMCs)-6))
				break
			}
			names = append(names, b.Name)
		}
		head := fmt.Sprintf("%s %s: %s", letter(i), strings.Join(what, ", "), say(lang, "collect.bmcs", "n", len(g.BMCs), "names", strings.Join(names, ", ")))
		for j, line := range st.Wrap(head, 2) {
			if j == 0 {
				line = strings.TrimLeft(line, " ")
			}
			fmt.Fprintln(w, line)
		}
		for _, b := range g.BMCs {
			if b.Stopped != "" {
				for _, line := range st.Wrap(b.Name+": "+say(lang, "collect.stopped", "why", b.Stopped), 5) {
					fmt.Fprintln(w, line)
				}
			}
		}
	}
	if len(s.Groups) == 0 {
		return
	}
	fmt.Fprintln(w)
	labelW := 0
	for _, class := range s.Classes {
		labelW = max(labelW, term.Width(say(lang, "collect.class."+class)))
	}
	cellW := 0
	for _, g := range s.Groups {
		for _, state := range g.States {
			cellW = max(cellW, term.Width(say(lang, "collect.state."+state)))
		}
	}
	cellW += 2
	width := st.Width
	if width <= 0 {
		width = term.DefaultWidth
	}
	perBlock := max(1, (width-labelW-2)/cellW)
	pad := func(s string, n int) string { return s + strings.Repeat(" ", max(0, n-term.Width(s))) }
	for from := 0; from < len(s.Groups); from += perBlock {
		to := min(from+perBlock, len(s.Groups))
		if from > 0 {
			fmt.Fprintln(w)
		}
		line := pad("", labelW+2)
		for i := from; i < to; i++ {
			line += pad(letter(i), cellW)
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
		for r, class := range s.Classes {
			line := pad(say(lang, "collect.class."+class), labelW+2)
			for i := from; i < to; i++ {
				line += pad(say(lang, "collect.state."+s.Groups[i].States[r]), cellW)
			}
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	}
	fmt.Fprintln(w)
	for _, line := range st.Wrap(say(lang, "collect.legend"), 0) {
		fmt.Fprintln(w, line)
	}
}
