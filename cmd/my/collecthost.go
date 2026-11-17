package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/scan"
)

// collectHostRoot and collectHostPrograms let tests collect from a made
// tree with stand-in programs.
var (
	collectHostRoot     = "/"
	collectHostPrograms map[string]string
	collectHostKmsg     func() ([]byte, error)
)

// host runs "my collect host -o DIR": a copy of the files of this host that
// a scan reads, with the output of the read-only programs of the Memory
// Explorer.
func (o *collectOptions) host(ctx context.Context, env *cli.Env, lang i18n.Lang) int {
	if o.out == "" {
		return o.usageError(env, lang, "%s", say(lang, "err.collect_host_out"))
	}
	deadline := 2 * time.Minute
	o.fs.Visit(func(f *flag.Flag) {
		if f.Name == "deadline" {
			deadline = 0
		}
	})
	if deadline == 0 {
		d, err := time.ParseDuration(o.deadline)
		if err != nil || d < time.Second {
			return o.usageError(env, lang, "%s", say(lang, "err.collect_time", "flag", "--deadline", "value", o.deadline))
		}
		deadline = d
	}
	if fi, err := os.Stat(o.out); err == nil {
		if !fi.IsDir() {
			return o.usageError(env, lang, "%s", say(lang, "err.collect_dir", "dir", o.out, "why", say(lang, "err.collect_host_file")))
		}
		if ents, _ := os.ReadDir(o.out); len(ents) > 0 {
			return o.usageError(env, lang, "%s", say(lang, "err.collect_dir", "dir", o.out, "why", say(lang, "err.collect_not_empty")))
		}
	}
	if hostOS != "linux" && collectHostRoot == "/" {
		return o.failure(env, errors.New(say(lang, "err.not_linux", "os", hostOS)))
	}
	now := time.Now()
	if v := os.Getenv("MY_NOW"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			now = t
		}
	}
	res, err := scan.CollectHost(ctx, scan.CollectOptions{Root: collectHostRoot, Out: o.out, Sudo: o.sudo, Deadline: deadline,
		Programs: collectHostPrograms, Kmsg: collectHostKmsg, Now: now, Privileged: os.Geteuid() == 0, NoSPD: o.noSPD})
	if err != nil {
		return o.failure(env, err)
	}
	code := 0
	if res.Partial() {
		code = exitPartial
	}
	if o.asJSON {
		b, _ := json.MarshalIndent(struct {
			Out string `json:"out"`
			*scan.CollectResult
			Partial bool `json:"partial"`
		}{o.out, res, res.Partial()}, "", "  ")
		fmt.Fprintln(env.Stdout, string(b))
		return code
	}
	for _, line := range hostSummary(lang, o.out, res, o.sudo) {
		fmt.Fprintln(env.Stdout, line)
	}
	return code
}

// hostSummary is one line per group of a collection, then where it went.
func hostSummary(lang i18n.Lang, out string, res *scan.CollectResult, sudo bool) []string {
	by := map[string][]scan.CollectEntry{}
	for _, e := range res.Entries {
		by[e.Group] = append(by[e.Group], e)
	}
	names := func(es []scan.CollectEntry, state string) string {
		var n []string
		for _, e := range es {
			if e.State == state {
				n = append(n, e.What)
			}
		}
		return strings.Join(n, ", ")
	}
	var lines []string
	line := func(group, code string, kv ...any) {
		lines = append(lines, say(lang, "collect.host.group."+group)+": "+say(lang, code, kv...))
	}
	ok := func(es []scan.CollectEntry) int {
		n := 0
		for _, e := range es {
			if e.State == scan.StepOK {
				n++
			}
		}
		return n
	}
	line("host", "collect.host.files", "n", ok(by[scan.GroupHost]))
	if s := names(by[scan.GroupErrors], scan.StepOK); s != "" {
		line("errors", "collect.host.written", "what", s)
	} else {
		line("errors", "collect.host.none")
	}
	switch sm := by[scan.GroupSMBIOS]; {
	case ok(sm) > 0 && strings.HasSuffix(sm[0].What, "DMI") && sm[0].State == scan.StepOK:
		line("smbios", "collect.host.tables")
	case ok(sm) > 0:
		line("smbios", "collect.host.dmidecode", "how", sm[len(sm)-1].From)
	case sudo:
		line("smbios", "collect.host.smbios_denied_sudo")
	default:
		line("smbios", "collect.host.smbios_denied")
	}
	rd := by[scan.GroupReadings]
	line("readings", "collect.host.readings", "n", ok(rd), "of", len(rd))
	for _, st := range []string{scan.StepAbsent, scan.StepDenied, scan.StepTimeout, scan.StepFailed} {
		var what []string
		for _, e := range append(append([]scan.CollectEntry{}, by[scan.GroupErrors]...), append(rd, by[scan.GroupMcelog]...)...) {
			if e.State == st {
				what = append(what, e.What)
			}
		}
		if len(what) > 0 {
			lines = append(lines, "  "+say(lang, "collect.host.state."+st, "what", strings.Join(what, ", ")))
		}
	}
	if m := by[scan.GroupMcelog]; len(m) > 0 && m[0].State == scan.StepOK {
		line("mcelog", "collect.host.written", "what", m[0].What)
	}
	lines = append(lines, say(lang, "collect.host.out", "dir", out))
	return lines
}
