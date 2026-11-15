package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// fleetFlags point a command at the store the rasdaemons of a fleet write
// to, a MySQL, MariaDB or PostgreSQL database, in place of this host.
type fleetFlags struct {
	// backend is the value of --fleet: mysql or postgresql.
	backend string
	// envFile is the value of --fleet-env: a file with the settings
	// rasdaemon takes for the store.
	envFile string
	// since is the value of --since.
	since string
}

func (f *fleetFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.backend, "fleet", "", "")
	fs.StringVar(&f.envFile, "fleet-env", "", "")
	fs.StringVar(&f.since, "since", "", "")
}

// set reports whether the command reads a store.
func (f *fleetFlags) set() bool { return f.backend != "" || f.envFile != "" }

// args returns the flags as they were given, for the hints that name the
// next command.
func (f *fleetFlags) args(host string) string {
	var out []string
	if f.backend != "" {
		out = append(out, "--fleet "+f.backend)
	}
	if f.envFile != "" {
		out = append(out, "--fleet-env "+f.envFile)
	}
	if f.since != "" {
		out = append(out, "--since "+f.since)
	}
	if host != "" {
		out = append(out, "--host "+host)
	}
	return strings.Join(out, " ")
}

// usage marks an error as a mistake in the command line.
type usage struct{ msg string }

func (u usage) Error() string { return u.msg }

// parseSince reads the value of --since: a date, a time, or a number of
// days or hours before now ("30d", "72h").
func parseSince(v string, now time.Time) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if n := len(v); n > 1 && (v[n-1] == 'd' || v[n-1] == 'h') {
		if count, err := strconv.Atoi(v[:n-1]); err == nil && count > 0 && count < 100000 {
			unit := time.Hour
			if v[n-1] == 'd' {
				unit = 24 * time.Hour
			}
			return now.Add(-time.Duration(count) * unit), nil
		}
	}
	return parseTime(v)
}

// open connects to the store. The settings are the variables rasdaemon
// takes; what the file of --fleet-env sets comes before the environment.
func (f *fleetFlags) open(ctx context.Context, env *cli.Env, lang i18n.Lang, now time.Time) (*rasdaemon.Store, rasdaemon.ReadOptions, error) {
	var opts rasdaemon.ReadOptions
	if now.IsZero() {
		now = time.Now()
	}
	since, err := parseSince(f.since, now)
	if err != nil {
		return nil, opts, usage{say(lang, "err.since", "value", f.since)}
	}
	opts.Since = since

	getenv := env.Getenv
	if f.envFile != "" {
		file, err := os.Open(f.envFile)
		if err != nil {
			return nil, opts, usage{"--fleet-env " + err.Error()}
		}
		settings, err := rasdaemon.ParseEnvFile(file)
		file.Close()
		if err != nil {
			return nil, opts, usage{fmt.Sprintf("--fleet-env %s: %v", f.envFile, err)}
		}
		getenv = func(name string) string {
			if v, ok := settings[name]; ok {
				return v
			}
			return env.Getenv(name)
		}
	}
	cfg, err := rasdaemon.StoreConfigFromEnv(f.backend, getenv)
	if err != nil {
		flagName := "--fleet"
		if f.backend == "" {
			flagName = "--fleet-env " + f.envFile
		}
		return nil, opts, usage{flagName + ": " + err.Error()}
	}
	st, err := openStore(ctx, cfg)
	return st, opts, err
}

// openStore connects to a store. A test puts a stand-in server here.
var openStore = rasdaemon.OpenStore

// hostList names hosts in a message: the first few and how many there are.
func hostList(names []string) string {
	const show = 8
	if len(names) <= show {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:show], ", ") + ", ..."
}

// storeHosts returns the names of the hosts of a store and the number of
// rows that carry no host name.
func storeHosts(ctx context.Context, st *rasdaemon.Store) (names []string, unnamed int64, err error) {
	hosts, err := st.Hosts(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	for _, h := range hosts {
		if h.Name == "" {
			unnamed = h.Rows
			continue
		}
		names = append(names, h.Name)
	}
	return names, unnamed, nil
}

// readStoreHost reads one host of a store into the ledger. The store is
// read as it is now, so the findings are as of now: a host whose last
// record is a month old has been quiet for a month.
func readStoreHost(ctx context.Context, st *rasdaemon.Store, ledger *store.Store, host string, opts rasdaemon.ReadOptions, now time.Time) error {
	res, err := ingest.Run(ctx, ledger, []adapter.Adapter{st.Adapter(host, opts)},
		ingest.Options{Host: host, Kind: model.ScanLive, Now: now, Privileged: true, Note: "rasdaemon store " + st.Origin()})
	if err != nil {
		return err
	}
	// A store that cannot be read is a failure of the command: there is no
	// other source that could stand in for it.
	for _, src := range res.Sources {
		if src.Kind == rasdaemon.Kind && src.Status == model.SourceError {
			return fmt.Errorf("%s, host %s: %s", st.Origin(), host, src.Detail)
		}
	}
	return nil
}

// scanFleet reads a store into the ledger at path: the host that was
// named, or every host. It returns the ledger and the name of the host
// when there is one to show.
func (o *options) scanFleet(ctx context.Context, env *cli.Env, lang i18n.Lang, path string, now time.Time) (*store.Store, string, error) {
	if o.root != "" {
		return nil, "", usage{say(lang, "err.fleet_root")}
	}
	st, opts, err := o.sf.open(ctx, env, lang, now)
	if err != nil {
		return nil, "", err
	}
	defer st.Close()
	names, unnamed, err := storeHosts(ctx, st)
	if err != nil {
		return nil, "", err
	}
	if unnamed > 0 {
		fmt.Fprintln(env.Stderr, say(lang, "fleet.unnamed", "n", unnamed))
	}
	if len(names) == 0 {
		return nil, "", errors.New(say(lang, "err.fleet_empty", "store", st.Origin()))
	}
	hosts := names
	switch {
	case o.host != "":
		found := ""
		for _, n := range names {
			if strings.EqualFold(n, o.host) {
				found = n
			}
		}
		if found == "" {
			return nil, "", usage{say(lang, "err.fleet_host", "host", o.host, "store", st.Origin(), "n", len(names), "hosts", hostList(names))}
		}
		hosts = []string{found}
	case len(names) > 1 && o.name != cmdName("", nil):
		// Every command but the overview shows one host. Saying so before
		// the store is read saves reading all of it.
		return nil, "", usage{say(lang, "err.fleet_one_host", "n", len(names), "hosts", hostList(names))}
	}

	ledger, err := store.Open(ctx, path, store.Create)
	if err != nil {
		return nil, "", err
	}
	for _, host := range hosts {
		if err := readStoreHost(ctx, st, ledger, host, opts, now); err != nil {
			ledger.Close()
			return nil, "", err
		}
	}
	if len(hosts) == 1 {
		return ledger, hosts[0], nil
	}
	return ledger, "", nil
}
