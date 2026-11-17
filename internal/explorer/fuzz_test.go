package explorer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// The readers see valid UTF-8: the reader of a reading replaces invalid
// bytes before it stores the text. Each target does the same, then checks
// that nothing panics and every bound holds.

func seedFiles(f *testing.F, names ...string) {
	for _, n := range names {
		files, _ := filepath.Glob(filepath.Join(fixtureRoot, "*", n))
		for i, file := range files {
			if i >= 12 {
				break
			}
			b, err := os.ReadFile(file)
			if err == nil {
				f.Add(string(b))
			}
		}
	}
}

func valid(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}

func checkDimms(t *testing.T, ds []Dimm) {
	if len(ds) > MaxSlots+1 {
		t.Fatalf("%d slots", len(ds))
	}
	for _, d := range ds {
		if d.SocketBy == "" && d.Socket != 0 || d.ChannelBy == "" && d.Channel != 0 {
			t.Fatalf("%q|%q: socket %v by %q, channel %v by %q", d.Locator, d.BankLocator, d.Socket, d.SocketBy, d.Channel, d.ChannelBy)
		}
	}
}

func FuzzDimms(f *testing.F) {
	seedFiles(f, "dmidecode.txt", "lshw.txt")
	f.Add(block("P1-DIMMA1", "P0_Node0_Channel0_Dimm0", "16 GB") + block("A1", "x", "16 GiB"))
	f.Fuzz(func(t *testing.T, s string) {
		s = valid(s)
		ce := ParseEdacCounts(s)
		checkDimms(t, ParseDmidecodeMemory(s, ce, ce))
		checkDimms(t, ParseLshwDimms(s, ce, ce))
		checkDimms(t, ParseDmidecodeMemory(WriteSlots([]SlotText{{Locator: s, BankLocator: s, Populated: true}}), nil, nil))
	})
}

// FuzzNames writes made-up hosts and slot names as the board writes the
// slots of a ledger: the host as "vendor|product|board|processor|signature|
// present" and one slot per line as "locator|bank locator".
func FuzzNames(f *testing.F) {
	f.Add("Dell Inc.|PowerEdge R640|Dell Inc. 0W23H8|Intel(R) Xeon(R) Gold 6130 CPU @ 2.10GHz|6/85/4|11", "A1|\nB1|\nA2|")
	f.Add("Supermicro|X10DRi|Supermicro X10DRi|Intel(R) Xeon(R) CPU E5-2630 v4 @ 2.20GHz||10", "P1-DIMMA1|P0_Node0_Channel0_Dimm0\nNO DIMM|NO DIMM\nP2-DIMME1|P1_Node1_Channel0_Dimm0")
	f.Add("HP|ProLiant DL380 Gen9||Intel(R) Xeon(R) CPU E5-2680 v3||11", "PROC 1 DIMM 1|\nPROC 2 DIMM 12|\nBIOS|ROM0")
	f.Add("|||AMD|25/17/1|1", "DIMM_A1|NODE 0 CHANNEL 0 DIMM 0\nDIMM_A2|NO DIMM\nController0-ChannelA-DIMM0|BANK 0")
	f.Fuzz(func(t *testing.T, host, names string) {
		host, names = valid(host), valid(names)
		h := strings.SplitN(host+"|||||", "|", 7)
		present := h[5]
		if len(present) > 16 {
			present = present[:16]
		}
		sh := SlotHost{Vendor: h[0], Product: h[1], Board: h[2], Processor: h[3], Signature: h[4], Present: present, Sockets: len(present)}
		var slots []SlotText
		for i, line := range strings.Split(names, "\n") {
			if i >= 64 {
				break
			}
			l, b, _ := strings.Cut(line, "|")
			slots = append(slots, SlotText{Locator: l, BankLocator: b, Populated: i%2 == 0, Type: map[bool]string{true: "Flash"}[strings.HasPrefix(l, "BIOS")]})
		}
		d, facts := ReadSlots(WriteSlotsOf(sh, slots), nil, nil)
		if len(d) != len(slots) {
			t.Fatalf("%d slots read, %d written", len(d), len(slots))
		}
		checkDimms(t, d)
		for _, x := range d {
			if x.PlaceBy != "" && (x.Socket < 0 || x.Channel < 0 || x.Position < 0 || x.NotSlot != "") {
				t.Fatalf("%q|%q placed at %v/%v/%v (%s)", x.Locator, x.BankLocator, x.Socket, x.Channel, x.Position, x.NotSlot)
			}
		}
		if facts.Sockets != len(present) && !(len(present) == 0 && h[3] != "" && facts.Sockets == 1) {
			t.Fatalf("%d processor records, %d written", facts.Sockets, len(present))
		}
	})
}

func FuzzCaches(f *testing.F) {
	seedFiles(f, "lshw.txt", "cpu-cache.txt")
	f.Fuzz(func(t *testing.T, s string) {
		s = valid(s)
		if n := len(ParseLshwCacheHierarchy(s)); n > MaxBlocks+1 {
			t.Fatalf("%d packages", n)
		}
		if n := len(ParseSysfsCacheInfo(s)); n > MaxBlocks {
			t.Fatalf("%d packages", n)
		}
	})
}

func FuzzNuma(f *testing.F) {
	seedFiles(f, "numactl-hardware.txt", "numastat.txt")
	f.Fuzz(func(t *testing.T, s string) {
		s = valid(s)
		hw, stat, _ := strings.Cut(s, "\x00")
		if n := len(ParseNumaInfo(hw, stat)); n > MaxNodes+1 {
			t.Fatalf("%d nodes", n)
		}
		r := MemoryMap(MapInput{Dmidecode: s, Numactl: hw, Numastat: stat, Lshw: s, SysfsCache: s, EdacCE: s})
		checkDimms(t, r.Dimms)
	})
}

func FuzzSystem(f *testing.F) {
	seedFiles(f, "meminfo", "vmstat", "slabinfo", "memory")
	f.Fuzz(func(t *testing.T, s string) {
		s = valid(s)
		ParseMeminfo(s)
		ParseVmstat(s)
		ParsePsi(s)
		if n := len(ParseSlabinfo(s)); n > MaxSlabs+1 {
			t.Fatalf("%d slabs", n)
		}
		if n := len(SystemMemory(SystemInput{Meminfo: s, Vmstat: s, Slabinfo: s, Pressure: &s}).TopSlabs); n > 21 {
			t.Fatalf("%d top slabs", n)
		}
	})
}

func FuzzPage(f *testing.F) {
	seedFiles(f, "sar-B.txt", "vmstat")
	f.Fuzz(func(t *testing.T, s string) {
		s = valid(s)
		if n := len(PageStatsOf(s, s).SarB); n > MaxSarRows {
			t.Fatalf("%d rows", n)
		}
	})
}

func FuzzProcesses(f *testing.F) {
	f.Add("PID:1 NAME:init CMD:/sbin/init\nVmRSS:\t 1234 kB\nThreads:\t1\nCGROUP:0::/\n---\n")
	f.Add("  1 1234 5678 init\n 22 99 100 bash\n")
	f.Fuzz(func(t *testing.T, s string) {
		s = valid(s)
		if n := len(ParseStatusEntries(s)); n > MaxProcesses {
			t.Fatalf("%d processes", n)
		}
		if n := len(ParsePsOutput(s)); n > MaxLines+1 {
			t.Fatalf("%d processes", n)
		}
		ParsePssLines(s)
		ProcessMemory(ProcessInput{Load: LoadNormal, Status: s, Ps: s, Pss: s})
	})
}

func FuzzJVM(f *testing.F) {
	f.Add("4242 java -Xmx4g -jar app.jar\n", " S0C S1C S0U S1U EC EU OC OU MC MU CCSC CCSU YGC YGCT FGC FGCT GCT\n1 2 3 4 5 6 7 8 9 10 11 12 13 0.5 1 0.25 0.75\n")
	f.Fuzz(func(t *testing.T, pgrep, jstat string) {
		pgrep, jstat = valid(pgrep), valid(jstat)
		ParseJstatGc(1, "java", pgrep, 1<<30, jstat, nil)
		ParseJvmProcFallback(1, "java", pgrep, 1<<30, nil)
		js := map[Num]string{}
		for _, p := range ParsePgrepJava(pgrep) {
			js[p.PID] = jstat
		}
		if n := len(JvmMemory(JVMInput{Load: LoadNormal, Pgrep: pgrep, Jstat: js, VmRSS: js})); n > 20 {
			t.Fatalf("%d JVMs", n)
		}
	})
}

func FuzzFileCache(f *testing.F) {
	f.Add("/var/log/a.log\n", "1 2097152 2097152 /var/log/a.log\n")
	f.Fuzz(func(t *testing.T, open, lines string) {
		open, lines = valid(open), valid(lines)
		for _, load := range []string{LoadNormal, LoadHigh} {
			if n := len(FileCacheEstimate(FileCacheInput{Load: load, Meminfo: lines, OpenFiles: open, Fincore: lines, Stat: lines, Find: lines})); n > MaxLines+1 {
				t.Fatalf("%d files", n)
			}
		}
		InferFileType(open)
	})
}

func FuzzProcessNuma(f *testing.F) {
	f.Add("0::/system.slice/a.service\n", "anon N0=1 N1=2\nfile N0=3\n")
	f.Fuzz(func(t *testing.T, cgroup, stat string) {
		cgroup, stat = valid(cgroup), valid(stat)
		r := ProcessNuma(ProcessNumaInput{PID: 1, Cgroup: cgroup, NumaStat: stat, CommStatus: cgroup + "---" + stat})
		if len(r.Entries) > MaxLines+1 {
			t.Fatalf("%d entries", len(r.Entries))
		}
	})
}

func FuzzMglru(f *testing.F) {
	f.Add("0x0007", "memcg 1 /\n node 0\n   0 1 2 3\n")
	f.Fuzz(func(t *testing.T, enabled, lru string) {
		Mglru(valid(enabled), valid(lru))
	})
}

func FuzzLoad(f *testing.F) {
	f.Add(LoadText("TCP: 10 (estab 3, closed 0)\n", "0.50 0.40 0.30 1/100 5\n", "8\n", "MemTotal: 100 kB\nMemAvailable: 50 kB\n"))
	f.Fuzz(func(t *testing.T, s string) {
		r := ServerLoad(valid(s))
		if r.Profile != LoadNormal && len(r.Reasons) == 0 {
			t.Fatalf("profile %s without a reason", r.Profile)
		}
	})
}

func FuzzFormat(f *testing.F) {
	f.Add(1024.0, 0.5)
	f.Fuzz(func(t *testing.T, a, b float64) {
		FormatBytes(a)
		FormatNumber(a)
		Pct(a, b)
		Clamp(a, 0, b)
		ToFixed(a, 2)
		NumString(b)
	})
}
