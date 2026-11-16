//go:build unix

// Command perf makes large synthetic inputs and measures what the commands
// of MemorY take on them: wall time and peak resident memory.
//
//	go run ./tools/perf gen -events 1000000 -hosts 200 -ledger fleet.db
//	go run ./tools/perf gen -events 1000000 -hosts 1 -tree host
//	go run ./tools/perf run -my bin/my -dir /tmp/my-perf -events 100000,1000000
//
// "gen" writes a fleet (many hosts, faults of every mode, error counts
// with a heavy tail) into one ledger, or one host as a copy of its files:
// the rasdaemon database, the kernel log, the EDAC counters and the SMBIOS
// tables. "run" makes both for each size, runs the commands on them and
// prints a table. docs/performance.md is made from its output.
//
// Nothing here comes from real hardware.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/synth"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(64)
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = gen(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(64)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "perf:", err)
		os.Exit(70)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage:
  perf gen -events N [-hosts H] [-seed S] [-ledger FILE] [-tree DIR [-flavor intel|amd|ghes]]
  perf run -my PATH -dir DIR [-events N,N...] [-hosts H] [-seed S] [-only NAME,...] [-gomemlimit SIZE] [-format md|json]

gen writes a made-up fleet into a ledger (-ledger) or, with -hosts 1, one
host as a copy of its files (-tree). run makes both for each size below
DIR, runs the commands of the binary at PATH on them and prints wall time
and peak resident memory of each. -gomemlimit sets GOMEMLIMIT for the
commands it runs: 191MiB is what the binary sets by itself on a machine
with 765 MiB of memory.
`)
}

// made says what a generated input holds.
type made struct {
	Hosts   int
	Events  int
	Actions int
	// Largest is the host with the most events, Slot the slot of its
	// largest fault.
	Largest       string
	LargestEvents int
	Slot          string
	Bytes         int64
}

// fleetLedger writes a fleet into a new ledger, one host after the other.
func fleetLedger(ctx context.Context, path string, o synth.FleetOptions) (made, error) {
	var m made
	scenarios, err := synth.Fleet(o)
	if err != nil {
		return m, err
	}
	if _, err := os.Stat(path); err == nil {
		return m, fmt.Errorf("%s exists", path)
	}
	s, err := store.Open(ctx, path, store.Create)
	if err != nil {
		return m, err
	}
	for _, sc := range scenarios {
		ds, err := synth.Generate(sc)
		if err != nil {
			s.Close()
			return m, fmt.Errorf("%s: %w", sc.Name, err)
		}
		res, err := ingest.Run(ctx, s, []adapter.Adapter{ds.Adapter()}, ingest.Options{Kind: model.ScanSynthetic, Now: sc.End,
			Note: fmt.Sprintf("made-up fleet, seed %d", o.Seed)})
		if err != nil {
			s.Close()
			return m, fmt.Errorf("%s: %w", sc.Name, err)
		}
		m.Hosts++
		m.Events += res.Events
		m.Actions += res.Actions
		if res.Events > m.LargestEvents {
			m.Largest, m.LargestEvents, m.Slot = sc.Host.Name, res.Events, largestSlot(ds)
		}
	}
	if err := s.Close(); err != nil {
		return m, err
	}
	if fi, err := os.Stat(path); err == nil {
		m.Bytes = fi.Size()
	}
	return m, nil
}

// largestSlot returns the slot of the fault with the most events.
func largestSlot(ds *synth.Dataset) string {
	slot, most := "", -1
	for _, t := range ds.Truth {
		if t.CE+t.UE > most {
			slot, most = t.Slot, t.CE+t.UE
		}
	}
	return slot
}

// hostTree writes one host as a copy of its files.
func hostTree(dir string, o synth.FleetOptions, flavor synth.Flavor) (made, error) {
	var m made
	o.Hosts = 1
	scenarios, err := synth.Fleet(o)
	if err != nil {
		return m, err
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return m, fmt.Errorf("%s exists and is not empty", dir)
	}
	ds, err := synth.Generate(scenarios[0])
	if err != nil {
		return m, err
	}
	if err := ds.WriteHostTree(dir, flavor); err != nil {
		return m, err
	}
	m = made{Hosts: 1, Events: len(ds.Events), Actions: len(ds.Actions), Largest: scenarios[0].Host.Name, LargestEvents: len(ds.Events), Slot: largestSlot(ds)}
	m.Bytes = treeBytes(dir)
	return m, nil
}

func treeBytes(dir string) int64 {
	var n int64
	filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && fi.Mode().IsRegular() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

func gen(args []string) error {
	fl := flag.NewFlagSet("perf gen", flag.ContinueOnError)
	events := fl.Int("events", 100000, "corrected error events in all")
	hosts := fl.Int("hosts", 200, "hosts of the fleet")
	seed := fl.Uint64("seed", 1, "seed; the same seed gives the same data")
	days := fl.Int("days", 180, "length of the history in days")
	ledger := fl.String("ledger", "", "write the fleet into the new ledger `file`")
	tree := fl.String("tree", "", "write one host as a copy of its files below `dir` (needs -hosts 1)")
	flavor := fl.String("flavor", string(synth.FlavorIntel), "platform the copy imitates: intel, amd or ghes")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *ledger == "" && *tree == "" {
		return errors.New("give -ledger, -tree or both")
	}
	o := synth.FleetOptions{Seed: *seed, Hosts: *hosts, Events: *events, Days: *days}
	if *ledger != "" {
		start := time.Now()
		m, err := fleetLedger(context.Background(), *ledger, o)
		if err != nil {
			return err
		}
		fmt.Printf("ledger %s: %d hosts, %d events, %d actions, %s, made in %s; the largest host is %s with %d events\n",
			*ledger, m.Hosts, m.Events, m.Actions, size(m.Bytes), time.Since(start).Round(time.Millisecond), m.Largest, m.LargestEvents)
	}
	if *tree != "" {
		if *hosts != 1 {
			return errors.New("a copy of a host's files is one host: give -hosts 1")
		}
		start := time.Now()
		m, err := hostTree(*tree, o, synth.Flavor(strings.ToLower(*flavor)))
		if err != nil {
			return err
		}
		fmt.Printf("host copy %s: %d events, %d actions, %s, made in %s\n", *tree, m.Events, m.Actions, size(m.Bytes), time.Since(start).Round(time.Millisecond))
	}
	return nil
}

// size writes a number of bytes the way the tables do: in MB with one
// digit, or in kB below one MB.
func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n > 0:
		return fmt.Sprintf("%.0f kB", float64(n)/(1<<10))
	}
	return "0"
}

// Measurement is one command on one input.
type Measurement struct {
	Name    string   `json:"name"`
	Input   string   `json:"input"`
	Events  int      `json:"events"`
	Command []string `json:"command"`
	// MemoryLimit is the GOMEMLIMIT the command ran under, if any.
	MemoryLimit string `json:"gomemlimit,omitempty"`
	// Seconds is the wall time, PeakRSS the largest resident set of the
	// process in bytes, Exit its exit code.
	Seconds float64 `json:"seconds"`
	PeakRSS int64   `json:"peak_rss_bytes"`
	Exit    int     `json:"exit"`
	// Output is the size of what the command wrote, where it writes a file.
	Output int64 `json:"output_bytes,omitempty"`
	// Ready and Scrape are the two times of the exporter: from its start
	// to its first finished scan, and the first request for /metrics.
	Ready  float64 `json:"ready_seconds,omitempty"`
	Scrape float64 `json:"scrape_seconds,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// peak returns the largest resident set of a finished process in bytes.
// The kernel reports it in bytes on macOS and in kilobytes on Linux.
func peak(st *os.ProcessState) int64 {
	ru, ok := st.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return int64(ru.Maxrss)
	}
	return int64(ru.Maxrss) << 10
}

// childEnv is the environment of a measured command: that of perf, plain
// output, and the memory limit asked for.
func childEnv(limit string) []string {
	env := []string{"NO_COLOR=1", "LC_ALL=C.UTF-8"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOMEMLIMIT=") {
			env = append(env, kv)
		}
	}
	if limit != "" {
		env = append(env, "GOMEMLIMIT="+limit)
	}
	return env
}

// measure runs one command. Its standard output goes nowhere: what is
// measured is the work, not a terminal. An exit code below 64 is the grade
// of a finding; from 64 on the command failed.
func measure(m *Measurement, out string) {
	cmd := exec.Command(m.Command[0], m.Command[1:]...)
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	cmd.Env = childEnv(m.MemoryLimit)
	start := time.Now()
	err := cmd.Run()
	m.Seconds = time.Since(start).Seconds()
	if cmd.ProcessState != nil {
		m.PeakRSS, m.Exit = peak(cmd.ProcessState), cmd.ProcessState.ExitCode()
	}
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || m.Exit >= 64 || m.Exit < 0) {
		m.Error = strings.TrimSpace(firstLines(stderr.String(), 3) + " (" + err.Error() + ")")
	}
	if out != "" {
		if fi, err := os.Stat(out); err == nil {
			if fi.IsDir() {
				m.Output = treeBytes(out)
			} else {
				m.Output = fi.Size()
			}
		}
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}

// exporter starts "my serve --metrics" on a copy of a host, waits until
// the first scan has finished, asks for /metrics once and stops the
// server.
func exporter(m *Measurement, limit time.Duration) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		m.Error = err.Error()
		return
	}
	addr := l.Addr().String()
	l.Close()
	m.Command = append(m.Command, "--listen", addr)
	cmd := exec.Command(m.Command[0], m.Command[1:]...)
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	cmd.Env = childEnv(m.MemoryLimit)
	start := time.Now()
	if err := cmd.Start(); err != nil {
		m.Error = err.Error()
		return
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stop := func() {
		cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(30 * time.Second):
			cmd.Process.Kill()
			<-exited
		}
		m.Seconds = time.Since(start).Seconds()
		m.PeakRSS, m.Exit = peak(cmd.ProcessState), cmd.ProcessState.ExitCode()
	}
	client := &http.Client{Timeout: limit}
	for {
		select {
		case err := <-exited:
			m.Error = strings.TrimSpace(firstLines(stderr.String(), 3) + fmt.Sprintf(" (the server ended: %v)", err))
			if cmd.ProcessState != nil {
				m.PeakRSS, m.Exit = peak(cmd.ProcessState), cmd.ProcessState.ExitCode()
			}
			return
		default:
		}
		if res, err := client.Get("http://" + addr + "/-/ready"); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Since(start) > limit {
			m.Error = "the first scan did not finish within " + limit.String()
			stop()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.Ready = time.Since(start).Seconds()
	t := time.Now()
	res, err := client.Get("http://" + addr + "/metrics")
	if err != nil {
		m.Error = err.Error()
		stop()
		return
	}
	n, _ := io.Copy(io.Discard, res.Body)
	res.Body.Close()
	m.Scrape, m.Output = time.Since(t).Seconds(), n
	if res.StatusCode != http.StatusOK {
		m.Error = "/metrics answered " + res.Status
	}
	stop()
}

func run(args []string) error {
	fl := flag.NewFlagSet("perf run", flag.ContinueOnError)
	my := fl.String("my", "", "the binary to measure")
	dir := fl.String("dir", "", "working directory for the inputs and outputs; inputs that are there are used again")
	sizes := fl.String("events", "100000,1000000", "sizes to measure, in events, separated by commas")
	hosts := fl.Int("hosts", 200, "hosts of the fleet")
	seed := fl.Uint64("seed", 1, "seed of the data")
	only := fl.String("only", "", "measure only the commands with these names, separated by commas")
	format := fl.String("format", "md", "md for a table, json for one line per measurement")
	limit := fl.Duration("limit", 30*time.Minute, "how long the first scan of the exporter may take")
	memLimit := fl.String("gomemlimit", "", "run the commands with GOMEMLIMIT set to `size` (191MiB, 1GiB)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *my == "" || *dir == "" {
		return errors.New("give -my and -dir")
	}
	bin, err := filepath.Abs(*my)
	if err != nil {
		return err
	}
	if _, err := os.Stat(bin); err != nil {
		return err
	}
	want := map[string]bool{}
	for _, n := range strings.Split(*only, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	ctx := context.Background()
	var all []Measurement
	for _, field := range strings.Split(*sizes, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || n < 1 {
			return fmt.Errorf("-events %q: not a number of events", field)
		}
		work := filepath.Join(*dir, strconv.Itoa(n))
		if err := os.MkdirAll(work, 0o755); err != nil {
			return err
		}
		o := synth.FleetOptions{Seed: *seed, Hosts: *hosts, Events: n}

		// The inputs. What a run before left is used again: making a
		// large input takes longer than measuring on it.
		var host, fleet made
		tree, ledger := filepath.Join(work, "host"), filepath.Join(work, "fleet.db")
		if err := remember(filepath.Join(work, "host.json"), &host, func() (made, error) { return hostTree(tree, o, synth.FlavorIntel) }); err != nil {
			return err
		}
		if err := remember(filepath.Join(work, "fleet.json"), &fleet, func() (made, error) { return fleetLedger(ctx, ledger, o) }); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%d events: host copy %s (%d events), fleet ledger %s (%d hosts, %d events, largest %s with %d)\n",
			n, size(host.Bytes), host.Events, size(fleet.Bytes), fleet.Hosts, fleet.Events, fleet.Largest, fleet.LargestEvents)

		hostCase, fleetCase := filepath.Join(work, "host.mycase"), filepath.Join(work, "fleet.mycase")
		type step struct {
			name, input string
			events      int
			args        []string
			out         string
			serve       bool
		}
		steps := []step{
			{"scan", "host copy", host.Events, []string{"scan", "--root", tree}, "", false},
			{"bundle", "host copy", host.Events, []string{"bundle", "--root", tree, "-o", hostCase, "--force"}, hostCase, false},
			{"open", "host case", host.Events, []string{"open", hostCase}, "", false},
			{"dimm", "host case", host.Events, []string{"open", hostCase, "dimm", host.Slot}, "", false},
			{"report", "host case", host.Events, []string{"report", hostCase, "-o", filepath.Join(work, "host.html"), "--force"}, filepath.Join(work, "host.html"), false},
			{"export", "host case", host.Events, []string{"open", hostCase, "export", "fmfm", "-o", filepath.Join(work, "host.fmfm.json"), "--force"}, filepath.Join(work, "host.fmfm.json"), false},
			{"export --records", "host case", host.Events, []string{"open", hostCase, "export", "fmfm", "--records", "-o", filepath.Join(work, "host.records.json"), "--force"}, filepath.Join(work, "host.records.json"), false},
			{"serve", "host copy", host.Events, []string{"serve", "--metrics", "--root", tree}, "", true},
			{"open", "fleet ledger", fleet.Events, []string{"open", ledger}, "", false},
			{"open --host", "fleet ledger", fleet.Events, []string{"open", ledger, "--host", fleet.Largest}, "", false},
			{"report --host", "fleet ledger", fleet.Events, []string{"report", ledger, "--host", fleet.Largest, "-o", filepath.Join(work, "fleet.html"), "--force"}, filepath.Join(work, "fleet.html"), false},
			{"bundle", "fleet ledger", fleet.Events, []string{"open", ledger, "bundle", "-o", fleetCase, "--force"}, fleetCase, false},
			{"open", "fleet case", fleet.Events, []string{"open", fleetCase}, "", false},
			{"export --host", "fleet ledger", fleet.Events, []string{"open", ledger, "export", "fmfm", "--host", fleet.Largest, "-o", filepath.Join(work, "fleet.fmfm.json"), "--force"}, filepath.Join(work, "fleet.fmfm.json"), false},
		}
		for _, st := range steps {
			if len(want) > 0 && !want[st.name] {
				continue
			}
			m := Measurement{Name: st.name, Input: st.input, Events: st.events, Command: append([]string{bin}, st.args...), MemoryLimit: *memLimit}
			if st.serve {
				exporter(&m, *limit)
			} else {
				measure(&m, st.out)
			}
			fmt.Fprintf(os.Stderr, "  %-18s %-13s %8.2f s %9s  exit %d %s\n", m.Name, m.Input, m.Seconds, size(m.PeakRSS), m.Exit, m.Error)
			all = append(all, m)
		}
	}

	switch *format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		for i := range all {
			// The table and the page name the commands, not the place of
			// the working directory.
			for j, a := range all[i].Command {
				all[i].Command[j] = strings.ReplaceAll(a, *dir+string(filepath.Separator), "")
			}
			all[i].Command[0] = "my"
			if err := enc.Encode(&all[i]); err != nil {
				return err
			}
		}
	default:
		table(os.Stdout, all)
	}
	for _, m := range all {
		if m.Error != "" {
			return fmt.Errorf("%s on the %s with %d events failed: %s", m.Name, m.Input, m.Events, m.Error)
		}
	}
	return nil
}

// remember runs make unless its result is on file from a run before.
func remember(file string, m *made, make func() (made, error)) error {
	if b, err := os.ReadFile(file); err == nil && json.Unmarshal(b, m) == nil && m.Events > 0 {
		return nil
	}
	got, err := make()
	if err != nil {
		return err
	}
	*m = got
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o644)
}

// table prints the measurements as a Markdown table: one row per command
// and input, one pair of columns per size.
func table(w io.Writer, all []Measurement) {
	var sizes []int
	seen := map[int]bool{}
	type key struct{ name, input string }
	var rows []key
	have := map[key]bool{}
	cell := map[key]map[int]Measurement{}
	// The sizes a row is filed under are those asked for: the inputs hold
	// about as many events, and the row says how many.
	nominal := func(m Measurement) int {
		best := 1
		for n := 10; n <= 1_000_000_000; n *= 10 {
			if abs(m.Events-n) < abs(m.Events-best) {
				best = n
			}
		}
		return best
	}
	for _, m := range all {
		n := nominal(m)
		if !seen[n] {
			seen[n] = true
			sizes = append(sizes, n)
		}
		k := key{m.Name, m.Input}
		if !have[k] {
			have[k] = true
			rows = append(rows, k)
			cell[k] = map[int]Measurement{}
		}
		cell[k][n] = m
	}
	sort.Ints(sizes)
	fmt.Fprint(w, "| Command | Input |")
	for _, n := range sizes {
		fmt.Fprintf(w, " %s events: time | peak RSS |", thousands(n))
	}
	fmt.Fprint(w, "\n| --- | --- |")
	for range sizes {
		fmt.Fprint(w, " ---: | ---: |")
	}
	fmt.Fprintln(w)
	for _, k := range rows {
		fmt.Fprintf(w, "| `%s` | %s |", k.name, k.input)
		for _, n := range sizes {
			m, ok := cell[k][n]
			switch {
			case !ok:
				fmt.Fprint(w, " | |")
			case m.Error != "":
				fmt.Fprint(w, " failed | |")
			case m.Ready > 0:
				fmt.Fprintf(w, " %s to ready, %s scrape | %s |", seconds(m.Ready), seconds(m.Scrape), size(m.PeakRSS))
			default:
				fmt.Fprintf(w, " %s | %s |", seconds(m.Seconds), size(m.PeakRSS))
			}
		}
		fmt.Fprintln(w)
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func seconds(s float64) string {
	switch {
	case s < 0.1:
		return fmt.Sprintf("%.0f ms", s*1000)
	case s >= 100:
		return fmt.Sprintf("%.0f s", s)
	case s >= 10:
		return fmt.Sprintf("%.1f s", s)
	}
	return fmt.Sprintf("%.2f s", s)
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
