package explorer

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A call is one function of the reference with its input, as the parity
// harness writes it. The want files under testdata/explorer/<case>/want and
// the corpus of the harness hold the answers of the reference.
type call struct {
	Fn string          `json:"fn"`
	In json.RawMessage `json:"in"`
}

type callInput struct {
	Text       string            `json:"text"`
	Key        string            `json:"key"`
	CE         string            `json:"ce"`
	UE         string            `json:"ue"`
	HW         string            `json:"hw"`
	Stat       *string           `json:"stat"`
	PID        float64           `json:"pid"`
	Name       string            `json:"name"`
	Cmdline    string            `json:"cmdline"`
	Rss        float64           `json:"rss"`
	Dmidecode  string            `json:"dmidecode"`
	Numactl    string            `json:"numactl"`
	Numastat   string            `json:"numastat"`
	EdacCe     string            `json:"edacCe"`
	EdacUe     string            `json:"edacUe"`
	Lshw       string            `json:"lshw"`
	SysfsCache string            `json:"sysfsCache"`
	User       *string           `json:"user"`
	Profile    string            `json:"profile"`
	Meminfo    string            `json:"meminfo"`
	Vmstat     string            `json:"vmstat"`
	Slabinfo   string            `json:"slabinfo"`
	Pressure   *string           `json:"pressure"`
	SarB       *string           `json:"sarB"`
	Load       string            `json:"load"`
	Status     string            `json:"status"`
	Ps         string            `json:"ps"`
	Pss        string            `json:"pss"`
	Pgrep      string            `json:"pgrep"`
	VmRSS      map[string]string `json:"vmrss"`
	Jstat      map[string]string `json:"jstat"`
	Dangerous  bool              `json:"dangerous"`
	OpenFiles  string            `json:"openFiles"`
	Fincore    string            `json:"fincore"`
	StatSizes  string            `json:"stat_"`
	Find       string            `json:"find"`
	Cgroup     string            `json:"cgroup"`
	NumaStat   *string           `json:"numaStat"`
	CommStatus string            `json:"commStatus"`
	Enabled    *string           `json:"enabled"`
	LruGen     string            `json:"lruGen"`
}

func byPID(m map[string]string) map[Num]string {
	out := map[Num]string{}
	for k, v := range m {
		f, err := strconv.ParseFloat(k, 64)
		if err == nil {
			out[Num(f)] = v
		}
	}
	return out
}

// numArg reads a number argument; the harness writes NaN and the
// infinities as strings.
func numArg(raw json.RawMessage, key string) float64 {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	switch v := m[key].(type) {
	case float64:
		return v
	case string:
		switch v {
		case "NaN":
			return math.NaN()
		case "Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		case "-0":
			return math.Copysign(0, -1)
		}
	}
	return math.NaN()
}

func orNA(p *string, na string) string {
	if p == nil {
		return na
	}
	return *p
}

// run calls the Go function that carries fn.
func run(c call) (any, error) {
	var in callInput
	if err := json.Unmarshal(c.In, &in); err != nil {
		return nil, err
	}
	// The input of stat lines is "stat" in the harness; it collides with
	// the numastat argument of parseNumaInfo.
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(c.In, &raw)
	if s, ok := raw["stat"]; ok && c.Fn == "collectFileCacheEstimate" {
		_ = json.Unmarshal(s, &in.StatSizes)
	}
	user := "root"
	if in.User != nil {
		user = *in.User
	}
	switch c.Fn {
	case "parseDmidecodeMemory":
		return orEmpty(ParseDmidecodeMemory(in.Text, ParseEdacCounts(in.CE), ParseEdacCounts(in.UE))), nil
	case "parseLshwDimms":
		return orEmpty(ParseLshwDimms(in.Text, ParseEdacCounts(in.CE), ParseEdacCounts(in.UE))), nil
	case "parseNumaInfo":
		return orEmpty(ParseNumaInfo(in.HW, orNA(in.Stat, ""))), nil
	case "parseLshwCacheHierarchy":
		return orEmpty(ParseLshwCacheHierarchy(in.Text)), nil
	case "parseSysfsCacheInfo":
		return orEmpty(ParseSysfsCacheInfo(in.Text)), nil
	case "parseMeminfo":
		return ParseMeminfo(in.Text), nil
	case "parseVmstat":
		return ParseVmstat(in.Text), nil
	case "parsePsi":
		return ParsePsi(in.Text), nil
	case "parseSlabinfo":
		return orEmpty(ParseSlabinfo(in.Text)), nil
	case "parsePsOutput":
		return orEmpty(ParsePsOutput(in.Text)), nil
	case "parseStatusEntries":
		return orEmpty(ParseStatusEntries(in.Text)), nil
	case "parseStatusKb":
		return parseStatusKb(in.Text, in.Key), nil
	case "parseStatusInt":
		return parseStatusInt(in.Text, in.Key), nil
	case "parsePgrepJava":
		return orEmpty(ParsePgrepJava(in.Text)), nil
	case "parseJstatGc":
		return ParseJstatGc(Num(in.PID), in.Name, in.Cmdline, Num(in.Rss), in.Text, nil), nil
	case "parseJvmProcFallback":
		return ParseJvmProcFallback(Num(in.PID), in.Name, in.Cmdline, Num(in.Rss), nil), nil
	case "inferFileType":
		return InferFileType(in.Text), nil
	case "collectMemoryMap":
		return MemoryMap(MapInput{Dmidecode: in.Dmidecode, Numactl: in.Numactl, Numastat: in.Numastat, EdacCE: in.EdacCe,
			EdacUE: in.EdacUe, Lshw: in.Lshw, SysfsCache: in.SysfsCache, User: user, Profile: in.Profile}), nil
	case "mapCommands":
		return MapCommands(user, in.Profile), nil
	case "collectSystemMemory":
		return SystemMemory(SystemInput{Meminfo: in.Meminfo, Vmstat: in.Vmstat, Slabinfo: in.Slabinfo, Pressure: in.Pressure}), nil
	case "collectPageStats":
		return PageStatsOf(in.Vmstat, orNA(in.SarB, "NOT_AVAILABLE\n")), nil
	case "collectProcessMemory":
		return ProcessMemory(ProcessInput{Load: in.Load, Status: in.Status, Ps: in.Ps, Pss: in.Pss}), nil
	case "collectJvmMemory":
		return JvmMemory(JVMInput{Load: in.Load, Pgrep: in.Pgrep, VmRSS: byPID(in.VmRSS), Jstat: byPID(in.Jstat)}), nil
	case "collectFileCacheEstimate":
		return FileCacheEstimate(FileCacheInput{Load: in.Load, FdWalkDangerous: in.Dangerous, Meminfo: in.Meminfo,
			OpenFiles: in.OpenFiles, Fincore: in.Fincore, Stat: in.StatSizes, Find: in.Find}), nil
	case "getProcessNuma":
		return ProcessNuma(ProcessNumaInput{PID: Num(in.PID), Cgroup: in.Cgroup, NumaStat: orNA(in.NumaStat, "NOT_AVAILABLE\n"),
			CommStatus: in.CommStatus}), nil
	case "getMglru":
		return Mglru(orNA(in.Enabled, "NOT_AVAILABLE\n"), headLines(in.LruGen, MaxLruLines)), nil
	case "detectServerLoad":
		r := ServerLoad(in.Text)
		kinds := []string{}
		for _, x := range r.Reasons {
			kinds = append(kinds, x.Kind)
		}
		return map[string]any{"profile": r.Profile, "estabCount": r.EstabCount, "cpuLoadRatio": r.CPULoadRatio,
			"memUsedPct": r.MemUsedPct, "reasons": kinds}, nil
	case "formatBytes":
		return FormatBytes(numArg(c.In, "n")), nil
	case "formatNumber":
		return FormatNumber(numArg(c.In, "n")), nil
	case "pct":
		return Num(Pct(numArg(c.In, "a"), numArg(c.In, "b"))), nil
	case "clamp":
		return Num(Clamp(numArg(c.In, "v"), numArg(c.In, "lo"), numArg(c.In, "hi"))), nil
	}
	return nil, fmt.Errorf("no Go function for %s", c.Fn)
}

// asData turns a value into plain JSON data, without the fields the
// reference does not have.
func asData(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return "marshal: " + err.Error()
	}
	var d any
	_ = json.Unmarshal(b, &d)
	return strip(d)
}

func strip(d any) any {
	switch x := d.(type) {
	case map[string]any:
		delete(x, "socketBy")
		delete(x, "channelBy")
		for k, v := range x {
			x[k] = strip(v)
		}
	case []any:
		for i, v := range x {
			x[i] = strip(v)
		}
	}
	return d
}

// diff describes the first difference of two JSON values, "" when equal.
func diff(path string, got, want any) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Sprintf("%s: got %s, want an object", path, short(got))
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		var ks []string
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			gv, gok := g[k]
			wv, wok := w[k]
			if gok != wok {
				return fmt.Sprintf("%s.%s: got %s, want %s", path, k, present(gok, gv), present(wok, wv))
			}
			if d := diff(path+"."+k, gv, wv); d != "" {
				return d
			}
		}
		return ""
	case []any:
		g, ok := got.([]any)
		if !ok {
			return fmt.Sprintf("%s: got %s, want an array", path, short(got))
		}
		if len(g) != len(w) {
			return fmt.Sprintf("%s: got %d elements, want %d", path, len(g), len(w))
		}
		for i := range w {
			if d := diff(fmt.Sprintf("%s[%d]", path, i), g[i], w[i]); d != "" {
				return d
			}
		}
		return ""
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Sprintf("%s: got %s, want %s", path, short(got), short(want))
	}
	return ""
}

func present(ok bool, v any) string {
	if !ok {
		return "nothing"
	}
	return short(v)
}

func short(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 120 {
		return string(b[:120]) + "..."
	}
	return string(b)
}

func compare(t *testing.T, name string, c call, want any) bool {
	t.Helper()
	got, err := run(c)
	if err != nil {
		t.Errorf("%s: %v", name, err)
		return false
	}
	if d := diff(c.Fn, asData(got), want); d != "" {
		t.Errorf("%s: %s", name, d)
		return false
	}
	return true
}

// resolveFiles replaces every string "@file:REL" of a call input with the
// text of the file REL of the case directory.
func resolveFiles(in json.RawMessage, dir string) (json.RawMessage, error) {
	var v any
	if err := json.Unmarshal(in, &v); err != nil {
		return nil, err
	}
	var walk func(any) (any, error)
	walk = func(x any) (any, error) {
		switch y := x.(type) {
		case string:
			if rel, ok := strings.CutPrefix(y, "@file:"); ok {
				b, err := os.ReadFile(filepath.Join(dir, rel))
				return string(b), err
			}
		case map[string]any:
			for k, e := range y {
				r, err := walk(e)
				if err != nil {
					return nil, err
				}
				y[k] = r
			}
		}
		return x, nil
	}
	v, err := walk(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// fixtureRoot is the directory of the fixtures of the explorer.
const fixtureRoot = "../../testdata/explorer"

// TestReference compares every carried function with the answers of the
// reference in testdata/explorer/<case>/want/<function>.json. Each want
// file holds the call ("fn", "in") and the answer ("out").
func TestReference(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(fixtureRoot, "*", "want", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.Glob(filepath.Join("../../testdata/real", "*", "want", "*.json"))
	files = append(files, real...)
	if len(files) == 0 {
		t.Fatal("no want files")
	}
	fns := map[string]int{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var w struct {
			call
			Out any `json:"out"`
		}
		if err := json.Unmarshal(b, &w); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		rel, _ := filepath.Rel("../..", f)
		w.In, err = resolveFiles(w.In, filepath.Dir(filepath.Dir(f)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		compare(t, rel, w.call, w.Out)
		fns[w.Fn]++
	}
	t.Logf("%d want files over %d functions", len(files), len(fns))
}

// TestCorpus runs the corpus of the parity harness: the directory in
// ME_PARITY_DIR holds corpus/*.jsonl.gz, one call and answer per line.
func TestCorpus(t *testing.T) {
	dir := os.Getenv("ME_PARITY_DIR")
	if dir == "" {
		t.Skip("ME_PARITY_DIR is not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "corpus", "*.jsonl.gz"))
	if len(files) == 0 {
		t.Fatalf("no corpus in %s", dir)
	}
	var report strings.Builder
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		zr, err := gzip.NewReader(fh)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(zr)
		sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
		n, bad := 0, 0
		for sc.Scan() {
			var w struct {
				call
				Out any `json:"out"`
			}
			if err := json.Unmarshal(sc.Bytes(), &w); err != nil {
				t.Fatalf("%s line %d: %v", f, n+1, err)
			}
			n++
			if !compare(t, fmt.Sprintf("%s:%d", filepath.Base(f), n), w.call, w.Out) {
				bad++
				if bad >= 5 {
					t.Errorf("%s: 5 differences, the rest of the file is not compared", f)
					break
				}
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatal(err)
		}
		fh.Close()
		fmt.Fprintf(&report, "%s\t%d\t%d\n", strings.TrimSuffix(filepath.Base(f), ".jsonl.gz"), n, bad)
	}
	if out := os.Getenv("ME_PARITY_REPORT"); out != "" {
		_ = os.WriteFile(out, []byte(report.String()), 0o644)
	}
	t.Logf("corpus:\n%s", report.String())
}
