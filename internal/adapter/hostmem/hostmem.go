// Package hostmem reads what the Memory Explorer shows of a host: the
// counters of the kernel about memory (meminfo, vmstat, pressure, slab
// caches), the cache sizes of the CPUs, the NUMA nodes (numactl and
// numastat), the paging table of sar -B, the state of MGLRU, and lshw text
// a collection holds. Each is kept as a reading: text, the time it was
// taken and where it came from. A reading holds no error record.
//
// On a live host the files are read under / and numactl, numastat and sar
// are run when they are on PATH, each with a time limit; lshw is never run
// here (my collect host runs it). In a copy of a host's files the plain
// names of a collection and the names an sos report writes are read.
package hostmem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/model"
)

// Kind is the source kind of the readings.
const Kind = "host-memory"

// Time limits of the programs (the limits of the explorer); tests make
// them shorter.
var (
	NumactlTimeout  = 5 * time.Second
	NumastatTimeout = 5 * time.Second
	SarTimeout      = 15 * time.Second
)

// Options tell the reader where the host is and what it may run.
type Options struct {
	// Live is set for the running host: the programs are run.
	Live bool
	// Now is the time of the readings.
	Now time.Time
	// Numactl, Numastat and Sar are the programs. Empty looks each up in
	// PATH; "-" does not run it.
	Numactl, Numastat, Sar string
}

// A source of one reading: files, first found, or a program.
type source struct {
	kind  string
	files []string
	max   int
	// prog is the program and its arguments, on a live host.
	prog    func(o Options) (string, []string, []string, time.Duration)
	prepare func(string) string
}

// Readings are the readings, in the order they are made.
var readings = []source{
	{kind: model.ReadingMeminfo, files: []string{"proc/meminfo"}, max: 256 << 10},
	{kind: model.ReadingVmstat, files: []string{"proc/vmstat"}, max: 256 << 10},
	{kind: model.ReadingPressure, files: []string{"proc/pressure/memory"}, max: 4 << 10},
	{kind: model.ReadingSlabinfo, files: []string{"proc/slabinfo"}, max: 4 << 20, prepare: explorer.SlabTop},
	{kind: model.ReadingNumactl, files: []string{"numactl-hardware.txt", "sos_commands/numa/numactl_--hardware"}, max: 256 << 10,
		prog: func(o Options) (string, []string, []string, time.Duration) {
			return o.Numactl, []string{"--hardware"}, nil, NumactlTimeout
		}},
	{kind: model.ReadingNumastat, files: []string{"numastat.txt", "sos_commands/numa/numastat"}, max: 256 << 10,
		prog: func(o Options) (string, []string, []string, time.Duration) {
			// One block of nodes as wide as they need: numastat fits its
			// output to 80 columns when it writes to no terminal.
			return o.Numastat, nil, []string{"NUMASTAT_WIDTH=4096"}, NumastatTimeout
		}},
	{kind: model.ReadingSarB, files: []string{"sar-B.txt"}, max: 1 << 20,
		prog: func(o Options) (string, []string, []string, time.Duration) {
			return o.Sar, []string{"-B"}, nil, SarTimeout
		}},
	{kind: model.ReadingLruGenEnabled, files: []string{"sys/kernel/mm/lru_gen/enabled"}, max: 64},
	{kind: model.ReadingLshw, files: []string{"lshw.txt", "sos_commands/hardware/lshw"}, max: 4 << 20},
}

// Prog names the program of a kind, for the lists of what a scan runs.
var progName = map[string]string{model.ReadingNumactl: "numactl", model.ReadingNumastat: "numastat", model.ReadingSarB: "sar"}

// Adapter reads the readings of one host.
type Adapter struct {
	root string
	opts Options
}

// New returns the reader of the host whose files are under root ("/" for
// the running host).
func New(root string, opts Options) *Adapter { return &Adapter{root: root, opts: opts} }

// Kind is the source kind.
func (a *Adapter) Kind() string { return Kind }

// Origin is the root read.
func (a *Adapter) Origin() string { return a.root }

// Collect makes the readings. A reading that is missing, denied, timed out
// or failed is left out with a note; the others are made.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.root}
	now := a.opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	var tally adapter.Tally
	made := 0
	for _, src := range readings {
		if ctx.Err() != nil {
			break
		}
		err := adapter.Each(&tally, src.kind, "", func() error {
			text, origin, cut, note := a.read(ctx, src)
			if note != "" {
				rep.Notes = append(rep.Notes, src.kind+": "+note)
				return nil
			}
			rep.Records++
			r := &model.HostReading{Kind: src.kind, TakenAt: now, Origin: origin, Text: text, Cut: cut}
			if err := sink.Reading(r); err != nil {
				return err
			}
			made++
			return nil
		})
		if err != nil {
			rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
			return rep
		}
	}
	if err := adapter.Each(&tally, model.ReadingCPUCache, "", func() error {
		text, note := a.cpuCache()
		if note != "" {
			rep.Notes = append(rep.Notes, model.ReadingCPUCache+": "+note)
			return nil
		}
		rep.Records++
		made++
		return sink.Reading(&model.HostReading{Kind: model.ReadingCPUCache, TakenAt: now, Origin: filepath.Join(a.root, "sys/devices/system/cpu"), Text: text})
	}); err != nil {
		rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
		return rep
	}
	rep.AddTally(&tally)
	if made > 0 {
		rep.Status = model.SourceOK
	} else {
		rep.Status, rep.Detail = model.SourceAbsent, "no memory counters were found"
	}
	sort.Strings(rep.Notes)
	return rep
}

// read makes one reading: from a program on a live host, else from the
// first of its files that exists.
func (a *Adapter) read(ctx context.Context, src source) (text, origin string, cut bool, note string) {
	if a.opts.Live && src.prog != nil {
		prog, args, env, timeout := src.prog(a.opts)
		return a.run(ctx, progName[src.kind], prog, args, env, timeout, src.max)
	}
	if a.opts.Live && src.kind == model.ReadingLshw {
		return "", "", false, "lshw runs only in my collect host"
	}
	for _, rel := range src.files {
		path := filepath.Join(a.root, filepath.FromSlash(rel))
		b, c, err := readBounded(path, src.max)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			st, detail := adapter.StatusForError(err)
			if st == model.SourceUnreadable {
				return "", "", false, "not readable: " + detail
			}
			return "", "", false, err.Error()
		}
		t := valid(string(b))
		if src.prepare != nil {
			t = src.prepare(t)
		}
		return t, path, c, ""
	}
	return "", "", false, "not there"
}

// run runs a program with a time limit and keeps at most max bytes of what
// it prints.
func (a *Adapter) run(ctx context.Context, name, prog string, args, env []string, timeout time.Duration, max int) (string, string, bool, string) {
	switch prog {
	case "-":
		return "", "", false, "not run"
	case "":
		p, err := exec.LookPath(name)
		if err != nil {
			return "", "", false, name + " is not installed"
		}
		prog = p
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, prog, args...)
	cmd.Env = append(os.Environ(), env...)
	var out limited
	out.max = max
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	origin := strings.TrimSpace(name + " " + strings.Join(args, " "))
	switch {
	case ctx.Err() != nil:
		return "", "", false, fmt.Sprintf("%s did not answer within %s", origin, timeout)
	case err != nil && out.buf.Len() == 0 && errors.Is(err, fs.ErrNotExist):
		return "", "", false, name + " is not installed"
	case err != nil && out.buf.Len() == 0:
		return "", "", false, origin + " failed: " + err.Error()
	case out.buf.Len() == 0:
		return "", "", false, origin + " printed nothing"
	}
	return valid(out.buf.String()), origin, out.cut, ""
}

// limited is a writer that keeps the first max bytes.
type limited struct {
	buf bytes.Buffer
	max int
	cut bool
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
			l.cut = true
		} else {
			l.buf.Write(p)
		}
	} else if len(p) > 0 {
		l.cut = true
	}
	return len(p), nil
}

// readBounded reads at most max bytes of a file, cut at the end of a line.
func readBounded(path string, max int) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, false, err
	}
	if len(b) <= max {
		return b, false, nil
	}
	b = b[:max]
	if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
		b = b[:i+1]
	}
	return b, true, nil
}

// valid replaces bytes that are not UTF-8.
func valid(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

// cpuCache writes the cache files of sysfs of the first CPU of each
// package as the lines "SOCKET:n LEVEL:n TYPE:t SIZE:s".
func (a *Adapter) cpuCache() (string, string) {
	dir := filepath.Join(a.root, "sys/devices/system/cpu")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "not there"
		}
		return "", err.Error()
	}
	type cpu struct{ n, pkg int }
	var cpus []cpu
	for _, e := range entries {
		n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "cpu"))
		if !strings.HasPrefix(e.Name(), "cpu") || err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name(), "topology", "physical_package_id"))
		if err != nil {
			continue
		}
		pkg, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			continue
		}
		cpus = append(cpus, cpu{n, pkg})
	}
	sort.Slice(cpus, func(i, j int) bool { return cpus[i].n < cpus[j].n })
	first := map[int]int{}
	var pkgs []int
	for _, c := range cpus {
		if _, ok := first[c.pkg]; !ok {
			first[c.pkg] = c.n
			pkgs = append(pkgs, c.pkg)
		}
	}
	sort.Ints(pkgs)
	var b strings.Builder
	for _, p := range pkgs {
		cdir := filepath.Join(dir, fmt.Sprintf("cpu%d", first[p]), "cache")
		idx, _ := filepath.Glob(filepath.Join(cdir, "index*"))
		sort.Strings(idx)
		for _, ix := range idx {
			field := func(name string) string {
				v, _ := os.ReadFile(filepath.Join(ix, name))
				return strings.TrimRight(valid(string(v)), "\n")
			}
			fmt.Fprintf(&b, "SOCKET:%d LEVEL:%s TYPE:%s SIZE:%s\n", p, field("level"), field("type"), field("size"))
			if b.Len() > 64<<10 {
				return b.String(), ""
			}
		}
	}
	if b.Len() == 0 {
		return "", "no cache files"
	}
	return b.String(), ""
}

// Holds tells whether the files of a host below root hold a reading. A
// copy of a host's files without one gets no source.
func Holds(root string) bool {
	for _, src := range readings {
		for _, rel := range src.files {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				return true
			}
		}
	}
	m, _ := filepath.Glob(filepath.Join(root, "sys/devices/system/cpu/cpu*/cache"))
	return len(m) > 0
}
