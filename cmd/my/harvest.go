package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/outfile"
	"github.com/agenticode/my/internal/preflight"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/term"
)

// The kept ledger of the boot harvest, and the units that run it.
var (
	harvestLedger  = preflight.DefaultLedger
	harvestUnitDir = "/etc/systemd/system"
	harvestRunDir  = "/run/systemd/system"
	// harvestSystemctl is the program install and uninstall run.
	harvestSystemctl = "systemctl"
)

// harvestDeadline bounds the reading of a harvest by default.
const harvestDeadline = 10 * time.Minute

// harvestProbe lets tests point a harvest at a made tree and replace what
// it asks the running system.
var harvestProbe func(*harvestEnv)

// harvestEnv is what a run of the harvest reads from the system.
type harvestEnv struct {
	scan      scan.Options
	freeBytes func(path string) (uint64, error)
	euid      int
	// afterPhaseA is called once phase A is on disk; tests stop there.
	afterPhaseA func()
	// extra are sources read at the end of phase B; tests add one that
	// fails.
	extra []adapter.Adapter
}

// runHarvest is "my harvest": a scan of this host into the kept ledger that
// reads what the boot before left first and makes it durable before it
// reads the rest; and "my harvest install|uninstall|status".
func runHarvest(ctx context.Context, env *cli.Env, args []string) int {
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") && args[0] != "?" {
		sub, args = args[0], args[1:]
	}
	o := &options{name: "my harvest", fs: flag.NewFlagSet("my harvest", flag.ContinueOnError), color: "never"}
	if sub != "" {
		o.name += " " + sub
	}
	o.fs.SetOutput(io.Discard)
	o.fs.StringVar(&o.lang, "lang", "", "")
	o.fs.BoolVar(&o.help, "help", false, "")
	o.fs.BoolVar(&o.help, "h", false, "")
	ledger := o.fs.String("ledger", harvestLedger, "")
	var deadline *time.Duration
	var timer, now, dryRun *bool
	switch sub {
	case "":
		deadline = o.fs.Duration("deadline", harvestDeadline, "")
	case "install":
		timer = o.fs.Bool("timer", false, "")
		now = o.fs.Bool("now", false, "")
		dryRun = o.fs.Bool("dry-run", false, "")
	case "uninstall":
		dryRun = o.fs.Bool("dry-run", false, "")
	case "status":
	default:
		lang := cli.Lang(env)
		return o.usageError(env, lang, "%s", say(lang, "err.harvest_what", "what", sub))
	}
	pos, lang, code, done := o.parse(env, args)
	if done {
		return code
	}
	if o.help {
		return printHelp(env, lang, o.style(env, lang), "harvest", nil)
	}
	if len(pos) > 0 {
		return o.usageError(env, lang, "%s", say(lang, "err.extra", "arg", pos[0]))
	}
	switch sub {
	case "install":
		return harvestInstall(env, lang, o, installOptions{Ledger: *ledger, Timer: *timer, Now: *now, DryRun: *dryRun})
	case "uninstall":
		return harvestUninstall(env, lang, o, *dryRun, *ledger)
	case "status":
		return harvestStatus(ctx, env, lang, o, *ledger)
	}
	if *deadline <= 0 {
		return o.usageError(env, lang, "%s", say(lang, "err.harvest_deadline", "value", deadline.String()))
	}
	return harvestRun(ctx, env, lang, o, *ledger, *deadline)
}

// harvestRun is one harvest. Phase A reads what the boot before left, then
// the ledger is checkpointed to disk; phase B reads the rest and the
// ledger passes run.
func harvestRun(ctx context.Context, env *cli.Env, lang i18n.Lang, o *options, path string, deadline time.Duration) int {
	he := &harvestEnv{
		scan:      scan.Options{Root: "/", Live: true, Note: "harvest"},
		freeBytes: freeSpace,
		euid:      os.Geteuid(),
	}
	if harvestProbe != nil {
		harvestProbe(he)
	} else if hostOS != "linux" {
		return o.failure(env, errorText(say(lang, "err.harvest_not_linux", "os", hostOS)))
	}
	he.scan.Privileged = he.euid == 0
	logf := func(code string, kv ...any) { fmt.Fprintf(env.Stderr, "my harvest: %s\n", say(lang, code, kv...)) }

	// The default ledger lies in the state directory of the unit; a run by
	// hand makes it when it is not there.
	if path == harvestLedger {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return o.failure(env, err)
		}
	}
	if err := outfile.CheckLedger(path); err != nil {
		// The ledger cannot be written: the run fails, and the ledger
		// stays as it was.
		return o.failure(env, errorText(say(lang, "harvest.cannot", "ledger", path, "err", targetText(lang, err))))
	}
	unlock, held, err := lockFile(path)
	if err != nil {
		return o.failure(env, err)
	}
	if held {
		logf("harvest.busy", "ledger", path)
		return cli.ExitOK
	}
	defer unlock()

	tmp, err := os.MkdirTemp("", "my-harvest-")
	if err != nil {
		return o.failure(env, err)
	}
	defer os.RemoveAll(tmp)
	he.scan.TempDir = tmp

	st, err := store.Open(ctx, path, store.Create)
	if err != nil {
		return o.failure(env, errorText(say(lang, "harvest.cannot", "ledger", path, "err", err.Error())))
	}
	closed := false
	defer func() {
		if !closed {
			st.Close()
		}
	}()
	he.scan.JournalSince = journalSince(ctx, st)

	a, b := scan.HarvestSources(ctx, he.scan)
	b = append(b, he.extra...)
	if free, err := he.freeBytes(filepath.Dir(path)); err == nil && free < preflight.MinFree {
		logf("harvest.low_space", "mib", int64(free>>20), "dir", filepath.Dir(path))
		b = nil
	}
	before, err := st.RecordCounts(ctx)
	if err != nil {
		return o.failure(env, err)
	}
	start := time.Now()
	phaseA := before
	opts := ingest.Options{Kind: model.ScanLive, Privileged: he.scan.Privileged, Note: "harvest", ReadDeadline: start.Add(deadline),
		AfterSource: func(i int, src model.Source) error {
			if i != len(a)-1 {
				return nil
			}
			// Phase A is in the ledger. Before anything else is read it
			// goes to disk: a reset seconds later must not take it.
			if err := st.Durable(ctx); err != nil {
				return err
			}
			counts, err := st.RecordCounts(ctx)
			if err != nil {
				return err
			}
			logf("harvest.phase_a", "sources", len(a), "records", added(before, counts), "seconds", seconds(time.Since(start)))
			phaseA = counts
			if he.afterPhaseA != nil {
				he.afterPhaseA()
			}
			return nil
		}}
	res, err := ingest.Run(ctx, st, append(a, b...), opts)
	if err != nil {
		return o.failure(env, errorText(say(lang, "harvest.cannot", "ledger", path, "err", err.Error())))
	}
	if he.scan.Live {
		pre := he.scan.Preflight
		if err := st.Update(ctx, func(w *store.Writer) error {
			return w.PutCaptureChecks(res.Scan.ID, res.Host, scan.CaptureChecks(he.scan.Root, pre))
		}); err != nil {
			return o.failure(env, errorText(say(lang, "harvest.cannot", "ledger", path, "err", err.Error())))
		}
	}
	after, err := st.RecordCounts(ctx)
	if err != nil {
		return o.failure(env, err)
	}
	notRead := 0
	for _, s := range res.Sources {
		if s.Detail == ingest.NotReadDetail {
			notRead++
		}
	}
	if b != nil {
		logf("harvest.phase_b", "sources", len(b)-notRead, "records", added(phaseA, after), "seconds", seconds(time.Since(start)))
	}
	if notRead > 0 {
		logf("harvest.deadline", "n", notRead, "deadline", deadline.String())
	}
	if added(before, after) == 0 && res.Events == 0 && res.Actions == 0 && res.Samples == 0 {
		// Nothing new: the run leaves no scan entry, no source inventory
		// and no repeated counter readings behind.
		if err := st.Update(ctx, func(w *store.Writer) error { return w.DropScan(res.Scan.ID, res.Host, res.Scan.StartedAt) }); err != nil {
			return o.failure(env, err)
		}
		logf("harvest.nothing")
	}
	if err := st.SetMeta(ctx, "harvest.last_run", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return o.failure(env, errorText(say(lang, "harvest.cannot", "ledger", path, "err", err.Error())))
	}
	boots, _ := st.Boots(ctx, "")
	closed = true
	if err := st.Close(); err != nil {
		return o.failure(env, err)
	}
	size := int64(0)
	if fi, err := os.Stat(path); err == nil {
		size = fi.Size()
	}
	logf("harvest.done", "boots", len(boots), "ledger", path, "kib", size>>10, "seconds", seconds(time.Since(start)))
	return cli.ExitOK
}

// added counts the rows that were added between two counts.
func added(before, after map[string]int64) int64 {
	var n int64
	for k, v := range after {
		n += v - before[k]
	}
	return n
}

func seconds(d time.Duration) string { return fmt.Sprintf("%.1f", d.Seconds()) }

// journalSince returns where the journal is read from: the start of the
// boot that ran during the last harvest that finished, whose lines after
// that harvest are not in the ledger yet. Without a finished harvest the
// journal is read in full.
func journalSince(ctx context.Context, st *store.Store) time.Time {
	last, err := st.Meta(ctx, "harvest.last_run")
	if err != nil || last == "" {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339, last)
	if err != nil {
		return time.Time{}
	}
	boots, err := st.Boots(ctx, "")
	if err != nil {
		return time.Time{}
	}
	var since time.Time
	for _, b := range boots {
		if b.StartQuality == model.TimeRelative || b.StartQuality == store.QualityUnknown || b.StartedAt.After(at) {
			continue
		}
		if b.StartedAt.After(since) {
			since = b.StartedAt
		}
	}
	if since.IsZero() {
		return time.Time{}
	}
	// A second before: the start of a boot is worked out from its lines,
	// and the journal compares to the second.
	return since.Add(-time.Second)
}

// installOptions are the flags of "my harvest install".
type installOptions struct {
	Ledger string
	Timer  bool
	Now    bool
	DryRun bool
}

// unitFiles returns the unit files install writes for a binary.
func unitFiles(binary, ledger string, timer bool) map[string]string {
	exec := binary + " harvest"
	if ledger != "" && ledger != preflight.DefaultLedger {
		exec += " --ledger " + ledger
	}
	out := map[string]string{"my-harvest.service": strings.ReplaceAll(harvestService, "@EXEC@", exec)}
	if timer {
		out["my-harvest.timer"] = harvestTimer
	}
	return out
}

// binaryCheck is installCheck; tests, which run as a user, replace it.
var binaryCheck = installCheck

// installCheck says why the binary cannot be the one a unit runs, as the
// code of a message and its arguments, or "". The unit protects /home and
// runs as root: a binary another user can replace would run as root at the
// next boot.
func installCheck(binary string) (string, []any) {
	for _, dir := range []string{"/home/", "/root/", "/tmp/", "/run/", "/var/tmp/"} {
		if strings.HasPrefix(binary, dir) {
			return "harvest.why_below", []any{"dir", strings.TrimSuffix(dir, "/")}
		}
	}
	if !unitWord(binary) {
		return "harvest.why_word", nil
	}
	fi, err := os.Stat(binary)
	if err != nil {
		return "harvest.why_other", []any{"why", cli.ErrorText(i18n.EN, err)}
	}
	if owner, ok := fileOwner(fi); ok && owner != 0 {
		return "harvest.why_owner", nil
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return "harvest.why_writable", nil
	}
	return "", nil
}

// unitWord reports whether a path can stand in ExecStart= as it is: the
// line is split at white space and quotes, and % and $ start specifiers
// and variables (systemd.service(5), "Command lines").
func unitWord(p string) bool {
	return p != "" && !strings.ContainsAny(p, " \t\n\"'\\%$;")
}

// installLedger returns the ledger a unit may keep, as an absolute path.
// The unit runs with ProtectSystem=strict and may write below its state
// directory, /var/lib/my, only.
func installLedger(ledger string) (string, bool) {
	abs, err := filepath.Abs(ledger)
	if err != nil || !unitWord(abs) || !strings.HasPrefix(abs, filepath.Dir(preflight.DefaultLedger)+"/") {
		return abs, false
	}
	return abs, true
}

// harvestInstall writes the unit files, reloads systemd and enables them.
func harvestInstall(env *cli.Env, lang i18n.Lang, o *options, in installOptions) int {
	euid := os.Geteuid()
	binary, err := os.Executable()
	if err == nil {
		binary, err = filepath.EvalSymlinks(binary)
	}
	if installProbe != nil {
		installProbe(&euid, &binary)
	}
	if err != nil {
		return o.failure(env, err)
	}
	if euid != 0 && !in.DryRun {
		return o.failure(env, errorText(say(lang, "harvest.not_root")))
	}
	if _, err := os.Stat(harvestRunDir); err != nil {
		return o.failure(env, errorText(say(lang, "harvest.no_systemd")))
	}
	if why, args := binaryCheck(binary); why != "" {
		return o.failure(env, errorText(say(lang, "harvest.bad_binary", "binary", binary, "why", say(lang, why, args...))))
	}
	ledger, ok := installLedger(in.Ledger)
	if !ok {
		return o.usageError(env, lang, "%s", say(lang, "harvest.bad_ledger", "ledger", ledger, "dir", filepath.Dir(preflight.DefaultLedger)))
	}
	in.Ledger = ledger
	files := unitFiles(binary, in.Ledger, in.Timer)
	names := []string{"my-harvest.service"}
	if in.Timer {
		names = append(names, "my-harvest.timer")
	}
	for _, n := range names {
		if foreign(filepath.Join(harvestUnitDir, n)) {
			return o.failure(env, errorText(say(lang, "harvest.foreign", "file", filepath.Join(harvestUnitDir, n))))
		}
	}
	cmds := [][]string{{"daemon-reload"}, append([]string{"enable"}, names...)}
	if in.Now {
		cmds = append(cmds, []string{"start", "my-harvest.service"})
	}
	w := env.Stdout
	for _, n := range names {
		fmt.Fprintf(w, "%s %s\n", say(lang, "harvest.writes"), filepath.Join(harvestUnitDir, n))
	}
	for _, c := range cmds {
		fmt.Fprintf(w, "%s %s %s\n", say(lang, "harvest.runs"), harvestSystemctl, strings.Join(c, " "))
	}
	if in.DryRun {
		fmt.Fprintln(w, say(lang, "harvest.dry_run"))
		return cli.ExitOK
	}
	for _, n := range names {
		if _, err := outfile.Write(filepath.Join(harvestUnitDir, n), true, func(f io.Writer) error {
			_, err := f.Write([]byte(files[n]))
			return err
		}); err != nil {
			return o.failure(env, err)
		}
		_ = os.Chmod(filepath.Join(harvestUnitDir, n), 0o644)
	}
	for _, c := range cmds {
		if err := systemctl(c...); err != nil {
			return o.failure(env, err)
		}
	}
	fmt.Fprintln(w, say(lang, "harvest.installed", "ledger", in.Ledger))
	return cli.ExitOK
}

// installProbe lets tests set who runs install and which binary it names.
var installProbe func(euid *int, binary *string)

// foreign reports whether a unit file of that name is there and was not
// written by install: it is left alone.
func foreign(path string) bool {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	return err != nil || !strings.HasPrefix(string(b), preflight.UnitMarker)
}

func systemctl(args ...string) error {
	cmd := exec.Command(harvestSystemctl, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", harvestSystemctl, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// harvestUninstall takes back what install did: it disables the units,
// removes the files it wrote and reloads systemd. The ledger stays.
func harvestUninstall(env *cli.Env, lang i18n.Lang, o *options, dryRun bool, ledger string) int {
	euid := os.Geteuid()
	binary := ""
	if installProbe != nil {
		installProbe(&euid, &binary)
	}
	if euid != 0 && !dryRun {
		return o.failure(env, errorText(say(lang, "harvest.not_root")))
	}
	var names []string
	for _, n := range []string{"my-harvest.timer", "my-harvest.service"} {
		p := filepath.Join(harvestUnitDir, n)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if foreign(p) {
			fmt.Fprintln(env.Stderr, say(lang, "harvest.left_alone", "file", p))
			continue
		}
		names = append(names, n)
	}
	w := env.Stdout
	if len(names) == 0 {
		fmt.Fprintln(w, say(lang, "harvest.none_installed", "ledger", ledger))
		return cli.ExitOK
	}
	cmds := [][]string{append([]string{"disable", "--now"}, names...)}
	for _, c := range cmds {
		fmt.Fprintf(w, "%s %s %s\n", say(lang, "harvest.runs"), harvestSystemctl, strings.Join(c, " "))
	}
	for _, n := range names {
		fmt.Fprintf(w, "%s %s\n", say(lang, "harvest.removes"), filepath.Join(harvestUnitDir, n))
	}
	fmt.Fprintf(w, "%s %s daemon-reload\n", say(lang, "harvest.runs"), harvestSystemctl)
	if dryRun {
		fmt.Fprintln(w, say(lang, "harvest.dry_run"))
		return cli.ExitOK
	}
	for _, c := range cmds {
		if err := systemctl(c...); err != nil {
			return o.failure(env, err)
		}
	}
	for _, n := range names {
		if err := os.Remove(filepath.Join(harvestUnitDir, n)); err != nil {
			return o.failure(env, err)
		}
	}
	if err := systemctl("daemon-reload"); err != nil {
		return o.failure(env, err)
	}
	fmt.Fprintln(w, say(lang, "harvest.uninstalled", "ledger", ledger))
	return cli.ExitOK
}

// harvestStatus says whether the units are installed and enabled, and
// what the ledger holds.
func harvestStatus(ctx context.Context, env *cli.Env, lang i18n.Lang, o *options, ledger string) int {
	w := env.Stdout
	for _, n := range []string{"my-harvest.service", "my-harvest.timer"} {
		p := filepath.Join(harvestUnitDir, n)
		state := "harvest.state.absent"
		switch _, err := os.Stat(p); {
		case err != nil:
		case foreign(p):
			state = "harvest.state.foreign"
		default:
			state = "harvest.state.disabled"
			target := "multi-user.target"
			if strings.HasSuffix(n, ".timer") {
				target = "timers.target"
			}
			if _, err := os.Lstat(filepath.Join(harvestUnitDir, target+".wants", n)); err == nil {
				state = "harvest.state.enabled"
			}
		}
		fmt.Fprintf(w, "%s %s\n", statusLabel(n), say(lang, state))
	}
	fi, err := os.Stat(ledger)
	if err != nil {
		fmt.Fprintf(w, "%s %s\n", statusLabel(say(lang, "harvest.ledger")), say(lang, "harvest.no_ledger", "ledger", ledger))
		return cli.ExitOK
	}
	st, err := store.Open(ctx, ledger, store.ReadOnly)
	if err != nil {
		return o.failure(env, err)
	}
	defer st.Close()
	all, _ := st.Boots(ctx, "")
	// A boot whose start is known only relative to itself is no boot of
	// the window my boots shows (engine.Recurrence): count as it counts.
	var boots []model.Boot
	for _, b := range all {
		if b.StartQuality != model.TimeRelative {
			boots = append(boots, b)
		}
	}
	hosts, _ := st.Hosts(ctx)
	fmt.Fprintf(w, "%s %s\n", statusLabel(say(lang, "harvest.ledger")), say(lang, "harvest.ledger_is", "ledger", ledger, "kib", fi.Size()>>10, "boots", len(boots), "hosts", len(hosts)))
	last, _ := st.Meta(ctx, "harvest.last_run")
	if last == "" {
		last = say(lang, "harvest.never")
	}
	fmt.Fprintf(w, "%s %s\n", statusLabel(say(lang, "harvest.last")), last)
	if cut := cutScans(ctx, st); cut != "" {
		fmt.Fprintf(w, "%s %s\n", statusLabel(""), say(lang, "case.cut", "time", cut))
	}
	return cli.ExitOK
}

// statusLabel pads a label of "my harvest status" to its column by the
// width on a terminal, which a Korean label has twice its letters of.
func statusLabel(s string) string {
	if w := term.Width(s); w < 20 {
		s += strings.Repeat(" ", 20-w)
	}
	return s
}

// cutScans returns the start of the latest scan of a ledger that did not
// finish, or "": a run that a reset cut has no finish time, and the passes
// did not run after it.
func cutScans(ctx context.Context, st *store.Store) string {
	scans, err := st.Scans(ctx)
	if err != nil {
		return ""
	}
	cut := ""
	for _, s := range scans {
		if s.FinishedAt.IsZero() {
			cut = s.StartedAt.UTC().Format("2006-01-02 15:04 UTC")
		}
	}
	return cut
}

// The unit files. "@EXEC@" is the command line the service runs. The
// copies in contrib/systemd are these with /usr/local/bin/my.
const harvestService = preflight.UnitMarker + `
#
# MemorY boot harvest: at every boot, read what the boot before left into
# the kept ledger /var/lib/my/ledger.db and make it durable, then read the
# rest of the host. A fatal memory error resets the host before the
# journal or rasdaemon can store anything; the panic text in pstore, the
# boot error region of the firmware, the text log of kdump and the records
# the next boot finds in the machine check banks are what is left of it.
#
#   my harvest install         writes this file and enables it
#   my harvest uninstall       disables it and removes it; the ledger stays
#
# or by hand:
#
#   install -m 0755 my /usr/local/bin/my
#   install -m 0644 my-harvest.service /etc/systemd/system/
#   systemctl daemon-reload && systemctl enable my-harvest.service
#
# The service reads, and writes its ledger and nothing else. It runs as
# root, because the boot error region and the pstore file system are
# readable by root only, with two capabilities: reading files whatever
# their mode, and reading the kernel log. It has no network. Nothing is
# ordered after it, so it holds up no boot.

[Unit]
Description=MemorY boot harvest: crash records of the boot before into a kept ledger
Documentation=https://github.com/agenticode/my/blob/main/docs/crash-loop.md
After=local-fs.target systemd-journal-flush.service systemd-pstore.service rasdaemon.service

[Service]
Type=oneshot
ExecStart=@EXEC@
TimeoutStartSec=15min
Nice=10
IOSchedulingClass=idle

StateDirectory=my
StateDirectoryMode=0700
UMask=0077

CapabilityBoundingSet=CAP_DAC_READ_SEARCH CAP_SYSLOG
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateNetwork=yes
RestrictAddressFamilies=AF_UNIX

DevicePolicy=closed
DeviceAllow=/dev/kmsg r

ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
ProtectClock=yes
ProtectHostname=yes
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
SystemCallArchitectures=native
SystemCallFilter=@system-service

[Install]
WantedBy=multi-user.target
`

const harvestTimer = preflight.UnitMarker + `
#
# Runs the boot harvest again during a long boot, so that counter readings
# and corrected errors are in the kept ledger before a reset. In a loop of
# short boots the run at boot is the one that matters.

[Unit]
Description=MemorY boot harvest, again during a long boot
Documentation=https://github.com/agenticode/my/blob/main/docs/crash-loop.md

[Timer]
OnBootSec=15min
OnUnitActiveSec=1h

[Install]
WantedBy=timers.target
`
