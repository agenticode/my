package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/redfish/client"
	"github.com/agenticode/my/internal/redfish/collect"
)

// A command that scans the host can ask the BMC of that host in the same
// run: "my --bmc ADDRESS", "my bundle --bmc ADDRESS -o FILE". The answers
// are collected the way "my collect redfish" collects them, into a
// directory that is removed when the command ends, and read as one more
// source of the scan. Without --bmc no command of this kind opens a
// connection.

// scanBMCDeadline bounds the time a scan waits for the BMC. A collection
// that is cut off by it is read as far as it got.
const scanBMCDeadline = 5 * time.Minute

// registerScan registers the flags by which a command that scans the host
// is told to ask its BMC as well.
func (b *bmcFlags) registerScan(fs *flag.FlagSet) {
	fs.StringVar(&b.bmc, "bmc", "", "")
	fs.StringVar(&b.user, "user", "", "")
	fs.StringVar(&b.passwordFile, "password-file", "", "")
	fs.BoolVar(&b.session, "session", false, "")
	fs.StringVar(&b.caFile, "ca-file", "", "")
	fs.StringVar(&b.fingerprint, "fingerprint", "", "")
	fs.BoolVar(&b.insecure, "insecure", false, "")
	fs.StringVar(&b.deadline, "bmc-deadline", scanBMCDeadline.String(), "")
	b.timeout, b.retries = client.DefaultTimeout.String(), client.DefaultRetries
}

// alone names a flag that says how a BMC is reached and was given without
// the BMC, or "".
func (b *bmcFlags) alone() string {
	switch {
	case b.bmc != "":
	case b.user != "":
		return "--user"
	case b.passwordFile != "":
		return "--password-file"
	case b.session:
		return "--session"
	case b.caFile != "":
		return "--ca-file"
	case b.fingerprint != "":
		return "--fingerprint"
	case b.insecure:
		return "--insecure"
	}
	return ""
}

// readBMC asks the BMC the command line names and returns its answers as a
// source of the scan. What came back and what did not is said on the
// standard error. A BMC that gave nothing ends the command: the reader
// asked for it, and an overview without it would not say so.
func (o *options) readBMC(ctx context.Context, env *cli.Env, lang i18n.Lang, tmp string, now time.Time) (adapter.Adapter, error) {
	b := &o.bmc
	u, err := client.ParseTarget(b.bmc)
	if err != nil {
		return nil, usage{"--bmc: " + err.Error()}
	}
	if u.Scheme == "http" && !b.insecure {
		return nil, usage{say(lang, "err.collect_http", "bmc", b.bmc)}
	}
	deadline, err := time.ParseDuration(b.deadline)
	if err != nil || deadline < time.Second {
		return nil, usage{say(lang, "err.collect_time", "flag", "--bmc-deadline", "value", b.deadline)}
	}
	cfg, err := b.clientConfig(lang)
	if err != nil {
		return nil, usage{errText(lang, err)}
	}
	targets := []target{{address: b.bmc, name: u.Host}}
	if err := bmcCredentials(ctx, env, lang, o.name, b, targets); err != nil {
		return nil, usage{errText(lang, err)}
	}
	if b.insecure {
		fmt.Fprintln(env.Stderr, o.name+": "+say(lang, "collect.insecure"))
	}
	cfg.BaseURL, cfg.User, cfg.Password = b.bmc, targets[0].user, targets[0].password

	dir := filepath.Join(tmp, "bmc")
	opts := collect.Options{Client: cfg, Name: u.Host, Tool: toolName(), Deadline: deadline}
	if !now.IsZero() {
		opts.Now = func() time.Time { return now }
	}
	m, err := collect.Run(ctx, dir, opts)
	r := collected{Name: u.Host, Address: b.bmc, Manifest: m}
	if err != nil {
		r.Error = err.Error()
	}
	collectSummary(env.Stderr, lang, o.style(env, lang), r)
	// Without the system resource there is no way to the memory modules,
	// the BIOS settings and the logs: a service root alone, which many
	// BMCs give to anyone, is nothing.
	if m == nil || m.Counts[collect.StatusOK] == 0 || !gave(m, collect.ClassSystem) {
		return nil, errors.New(say(lang, "err.bmc_nothing", "bmc", b.bmc))
	}
	// The records name the BMC they came from, and not the directory they
	// passed through on this machine.
	return redfish.NewFS(os.DirFS(dir), bmcOrigin+m.BMC.Address, redfish.Options{Now: m.StartedAt}), nil
}

// bmcOrigin starts the origin of a source that was read from a BMC by this
// run.
const bmcOrigin = "bmc:"

// gave reports whether a collection holds a resource of a class.
func gave(m *collect.Manifest, class string) bool {
	c := m.Class(class)
	return c != nil && (c.State == collect.StateOK || c.State == collect.StatePartial)
}
