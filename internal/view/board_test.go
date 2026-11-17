package view

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testutil"
)

// stickNames lists the slots the board draws, in drawing order, and those
// it sets aside by name.
func stickNames(b *Board) (drawn, aside []string) {
	for _, r := range b.Rows {
		for _, p := range r.Parts {
			for _, c := range p.Channels {
				for _, s := range c.Sticks {
					drawn = append(drawn, s.Slot)
				}
			}
		}
	}
	if b.Aside != nil {
		for _, r := range b.Aside.Rows {
			for _, c := range r.Channels {
				for _, s := range c.Slots {
					aside = append(aside, s.Slot)
				}
			}
		}
	}
	return drawn, aside
}

// boardText is a short form of a board for the tests: per row the parts,
// a bank as its channels with their slots.
func boardText(b *Board) string {
	var rows []string
	for _, r := range b.Rows {
		var parts []string
		for _, p := range r.Parts {
			if p.CPU != nil {
				parts = append(parts, fmt.Sprintf("CPU%d", p.CPU.Socket))
				continue
			}
			var chs []string
			for _, c := range p.Channels {
				var s []string
				for _, st := range c.Sticks {
					s = append(s, st.Slot)
				}
				label := "-"
				if c.Channel.Valid {
					label = fmt.Sprint(c.Channel.V)
				}
				chs = append(chs, label+":"+strings.Join(s, ","))
			}
			parts = append(parts, "["+strings.Join(chs, " ")+"]")
		}
		rows = append(rows, strings.Join(parts, " "))
	}
	return strings.Join(rows, " | ")
}

// The 6-DIMM capture, from the bundle it was written in before the board
// existed: every slot placed on its CPU, by channel.
func TestBoardOfAnOlderBundle(t *testing.T) {
	path := testutil.Testdata(t, "real", "6.19.10-ghes-multi", "my", "case.mycase")
	bc, err := bundle.Open(ctx, path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Close()
	s, err := store.Open(ctx, bc.Ledger, store.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := Open(ctx, s, i18n.EN, engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Board(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := boardText(b), "[2:CPU0_C1 1:CPU0_B1 0:CPU0_A1] CPU0 [3:CPU0_D1 4:CPU0_E1 5:CPU0_F1]"; got != want {
		t.Errorf("board %s, want %s", got, want)
	}
	if b.Counts != (BoardCounts{Placed: 6}) || b.Populated != 6 || b.CE != 35 || b.UE != 1 {
		t.Errorf("counts %+v, populated %d, CE %d, UE %d", b.Counts, b.Populated, b.CE, b.UE)
	}
	for _, st := range b.Rows[0].Parts[0].Channels[0].Sticks {
		if st.State == "" || st.Size == "" || st.Module == nil {
			t.Errorf("stick %+v lacks what the ledger knows", st)
		}
	}
}

func slotNamed(name, locator, bank string, handle int, populated bool) model.Slot {
	sl := model.Slot{Host: "h", Name: name, Locator: locator, BankLocator: bank, Populated: model.Some(populated)}
	if handle >= 0 {
		sl.SMBIOSHandle = model.Some(handle)
	}
	return sl
}

// Every slot of the ledger is on the board exactly once, drawn or set
// aside, whatever its names (generated ledgers).
func TestBoardHoldsEverySlotOnce(t *testing.T) {
	words := []string{"P0", "P1_Node1_Channel2_Dimm0", "_Node0_Channel1_Dimm1", "DIMM012", "A7", "B12", "PROC 2 DIMM 4", "CPU1", "CPU 2",
		"Node 2", "_C3D1", "_CH2_DIMM0", "Channel 4", "NODE 5", "DIMMC1", "CPU0_DIMM_A1", "CH B", "NO DIMM", "Not Specified", "D0", "-0", "",
		"DIMM_P1_B0", "CHAN A DIMM 0", "\x00\n "}
	r := rand.New(rand.NewPCG(1, 2))
	h := &engine.HostResult{Host: "h"}
	for n := 0; n < 400; n++ {
		var slots []model.Slot
		k := 1 + r.IntN(40)
		for i := 0; i < k; i++ {
			loc := words[r.IntN(len(words))] + words[r.IntN(len(words))]
			bank := words[r.IntN(len(words))]
			handle := -1
			if r.IntN(4) > 0 {
				handle = r.IntN(64)
			}
			name := fmt.Sprintf("s%d-%s", i, loc)
			slots = append(slots, slotNamed(name, loc, bank, handle, r.IntN(2) == 0))
		}
		bh := boardHost{sockets: r.IntN(4)}
		if r.IntN(2) == 0 {
			bh.vendor = "Dell Inc."
		}
		b := boardOf(i18n.EN, h, slots, bh)
		drawn, aside := stickNames(b)
		all := append(drawn, aside...)
		seen := map[string]int{}
		for _, s := range all {
			seen[s]++
		}
		for _, sl := range slots {
			if seen[sl.Name] != 1 {
				t.Fatalf("ledger %d: slot %q is on the board %d times\n%s", n, sl.Name, seen[sl.Name], boardText(b))
			}
		}
		if len(all) != len(slots) {
			t.Fatalf("ledger %d: %d slots on the board, %d in the ledger", n, len(all), len(slots))
		}
		if b.Counts.Placed+b.Counts.NoChannel+b.Counts.Unplaced != countDescribed(slots) {
			t.Fatalf("ledger %d: counts %+v", n, b.Counts)
		}
	}
}

func countDescribed(slots []model.Slot) int {
	n := 0
	for _, sl := range slots {
		if described(sl) {
			n++
		}
	}
	return n
}

// A reader that gives one slot fewer than the text holds, and one that
// panics: every slot is set aside by name, none is lost.
func TestBoardWhenTheReaderFails(t *testing.T) {
	slots := []model.Slot{slotNamed("A1", "A1", "", 1, true), slotNamed("A2", "A2", "", 2, true), slotNamed("B1", "B1", "", 3, false)}
	h := &engine.HostResult{Host: "h"}
	for _, c := range []struct {
		name   string
		parse  func(string, explorer.Counts, explorer.Counts) []explorer.Dimm
		reason string
	}{
		{"count", func(s string, a, b explorer.Counts) []explorer.Dimm {
			return explorer.ParseDmidecodeMemory(s, a, b)[1:]
		}, "count"},
		{"panic", func(string, explorer.Counts, explorer.Counts) []explorer.Dimm { panic("reader") }, "fault"},
	} {
		t.Run(c.name, func(t *testing.T) {
			old := boardParse
			boardParse = c.parse
			defer func() { boardParse = old }()
			b := boardOf(i18n.EN, h, slots, boardHost{})
			drawn, aside := stickNames(b)
			if len(drawn) != 0 || len(aside) != 3 || b.Counts.Unplaced != 3 {
				t.Errorf("drawn %v, aside %v, counts %+v", drawn, aside, b.Counts)
			}
			if len(b.Reasons) != 1 || b.Reasons[0].Reason != c.reason {
				t.Errorf("reasons %+v", b.Reasons)
			}
		})
	}
	if os.Getenv("MY_DEBUG") == "panic" {
		t.Skip("MY_DEBUG=panic lets panics through")
	}
}

// The boards of the public dumps, with the socket count known and not
// known, and with the fixes off (as the first implementation placed them,
// with the checks): testdata/boards-of-dumps.txt.
func TestBoardsOfPublicDumps(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(testutil.RepoRoot(t), "testdata", "explorer", "*", "dmidecode.txt"))
	sort.Strings(files)
	h := &engine.HostResult{Host: "h"}
	reHandle := regexp.MustCompile(`(?m)^Handle (0x[0-9A-Fa-f]+), DMI type 17,`)
	var out strings.Builder
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		ds := explorer.ParseDmidecodeMemory(text, nil, nil)
		handles := reHandle.FindAllStringSubmatch(text, -1)
		var slots []model.Slot
		for i, d := range ds {
			hd := i
			if i < len(handles) {
				fmt.Sscanf(handles[i][1], "0x%x", &hd)
			}
			loc := d.Locator
			if loc == "Not Specified" {
				loc = ""
			}
			slots = append(slots, slotNamed(fmt.Sprintf("%s#%d", d.Locator, i), loc, d.BankLocator, hd, d.Populated))
		}
		// What the ledger keeps of the host, as the SMBIOS reader gives it.
		var c adapter.Collected
		smbios.NewDmidecodeFile(f, smbios.Options{}).Collect(context.Background(), &c)
		var host model.Host
		if len(c.Hosts) > 0 {
			host = *c.Hosts[0]
		}
		fmt.Fprintf(&out, "%s (%d slots, %d processor records)\n", filepath.Base(filepath.Dir(f)), len(slots), host.Sockets)
		for _, c := range []struct {
			label string
			fixes explorer.Fix
			known bool
		}{{"first", 0, true}, {"fixed", explorer.AllFixes, true}, {"fixed, sockets unknown", explorer.AllFixes, false}} {
			old := explorer.SetFixes(c.fixes)
			bh := boardHost{vendor: host.SystemVendor, product: host.SystemProduct, board: host.Board}
			if c.known {
				bh.processor, bh.signature, bh.sockets, bh.present = host.CPU, host.CPUSignature, host.Sockets, host.CPUPresent
			} else {
				bh.processor = host.CPU
			}
			b := boardOf(i18n.EN, h, slots, bh)
			explorer.SetFixes(old)
			var why []string
			for _, r := range b.Reasons {
				why = append(why, fmt.Sprintf("%s %s %d", r.State, r.Reason, r.Count))
			}
			cpus := 0
			for _, r := range b.Rows {
				for _, p := range r.Parts {
					if p.CPU != nil {
						cpus++
					}
				}
			}
			fmt.Fprintf(&out, "  %-22s placed %d, no channel %d, aside %d, CPUs %d %v\n", c.label, b.Counts.Placed, b.Counts.NoChannel, b.Counts.Unplaced, cpus, why)
		}
	}
	testutil.Golden(t, "boards-of-dumps.txt", []byte(out.String()))
}

// The fields of --json of the board are those docs/data-model.md lists.
func TestBoardFieldsAreDocumented(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "data-model.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	start := strings.Index(text, "### The view `board`")
	if start < 0 {
		t.Fatal("docs/data-model.md has no section on the view board")
	}
	section := text[start:]
	if end := strings.Index(section[4:], "\n### "); end >= 0 {
		section = section[:end+4]
	}
	listed := map[string]bool{}
	for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(section, -1) {
		for _, w := range regexp.MustCompile(`[a-z_]+`).FindAllString(m[1], -1) {
			listed[w] = true
		}
	}
	var b Board
	b.Rows = []BoardRow{{Parts: []BoardPart{{Bank: "left", NoChannels: true, Channels: []BoardChannel{{Channel: model.Some(1),
		Sticks: []BoardStick{{Slot: "A1", State: "ok", DIMMUID: "u", CE: 1, UE: 1, Size: "1 GB", Bytes: 1, Locator: "A1", BankLocator: "x",
			SocketBy: "x", ChannelBy: "x", Module: &BoardModule{Maker: "m", PartNumber: "p", Serial: "s", Type: "t", SpeedMTs: model.Some(1), Configured: model.Some(1), Ranks: model.Some(1)},
			Place: explorer.Placement{State: "placed", Reason: "x", Socket: 1, Channel: 1, Assumed: true}}}}}},
		{CPU: &BoardCPU{Socket: 1, DIMMs: 1, Bytes: 1, Size: "x", Caches: []BoardCache{{Level: "L1", Bytes: 1, Size: "x"}}}}}}}
	b.Host, b.Slots, b.Populated, b.Bytes, b.Size, b.CE, b.UE, b.Badge, b.Sockets, b.Caches = "h", 1, 1, 1, "x", 1, 1, "x", 1, "lshw"
	b.Aside = &SlotMap{}
	b.Reasons = []BoardReason{{State: "x", Reason: "x", Count: 1, Locator: "x", BankLocator: "x", Text: Text{Code: "x"}}}
	keys := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				keys[k] = true
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	walk(data)
	for k := range keys {
		if !listed[k] && !strings.Contains("code args text rows stops slots populated label channels imc", k) {
			t.Errorf("docs/data-model.md does not list the field %q of the view board", k)
		}
	}
}
