package view

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// placeOf writes where the pass put a slot and why.
func placeOf(d explorer.Dimm) string {
	return fmt.Sprintf("%v/%v/%s/%v %s %s %s %s %s", d.Socket, d.Channel, d.ChannelName, d.Position, d.PlaceBy, d.Aside, d.Label, d.Family, d.Idx)
}

// TestDumpTextAndLedgerText is a step of the parity harness: the directory
// in ME_PARITY_DUMPS holds the dmidecode texts of the public collection.
// For every dump the readers give the same slots from the dump text and
// from the text WriteSlotsOf writes out of what the ledger keeps of it (the
// SMBIOS reader's host and modules), the board the same verdict, and the
// slot list of testdata/explorer/population the same host.
func TestDumpTextAndLedgerText(t *testing.T) {
	dir := os.Getenv("ME_PARITY_DUMPS")
	if dir == "" {
		t.Skip("ME_PARITY_DUMPS is not set")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	if len(files) == 0 {
		t.Fatalf("no dumps in %s", dir)
	}
	sort.Strings(files)
	// The host of each list of the population file.
	root := testutil.Testdata(t, "explorer", "population")
	listOf := map[string]string{}
	b, err := os.ReadFile(filepath.Join(root, "dumps.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if p := strings.Split(line, "\t"); len(p) == 3 && p[0] != "dump" {
			listOf[p[0]] = p[1]
		}
	}
	hostOf := map[string]string{}
	b, err = os.ReadFile(filepath.Join(root, "slots.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if p := strings.Split(line, "\t"); len(p) == 23 && p[0] != "list" {
			hostOf[p[0]] = strings.Join(p[2:9], "|")
		}
	}
	dumps, slots, differ := 0, 0, 0
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".txt")
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		dumps++
		fromDump, fd := explorer.ReadSlots(string(raw), nil, nil)
		var c adapter.Collected
		smbios.NewDmidecodeFile(f, smbios.Options{}).Collect(context.Background(), &c)
		var h model.Host
		if len(c.Hosts) > 0 {
			h = *c.Hosts[0]
		}
		var in []explorer.SlotText
		for _, m := range c.Modules {
			st := explorer.SlotText{Locator: m.Locator, BankLocator: m.BankLocator, Populated: m.Populated, SizeMB: m.DIMM.SizeBytes >> 20}
			if m.Populated {
				st.Type = m.DIMM.MemoryType
			}
			in = append(in, st)
		}
		sh := explorer.SlotHost{Vendor: h.SystemVendor, Product: h.SystemProduct, Board: h.Board, Processor: h.CPU,
			Signature: h.CPUSignature, Sockets: h.Sockets, Present: h.CPUPresent}
		fromLedger, fl := explorer.ReadSlots(explorer.WriteSlotsOf(sh, in), nil, nil)
		// The ledger keeps no flash device: the dump's are left out.
		var kept []explorer.Dimm
		for _, d := range fromDump {
			if d.NotSlot != explorer.NotSlotFlash {
				kept = append(kept, d)
			}
		}
		if len(kept) != len(fromLedger) {
			t.Errorf("%s: %d slots from the dump, %d from the ledger", id, len(kept), len(fromLedger))
			differ++
			continue
		}
		bad := false
		for i := range kept {
			slots++
			if a, b := placeOf(kept[i]), placeOf(fromLedger[i]); a != b {
				if !bad {
					t.Errorf("%s %q|%q: from the dump %s, from the ledger %s", id, kept[i].Locator, kept[i].BankLocator, a, b)
				}
				bad = true
			}
		}
		cd := explorer.Complete(fromDump, fd, explorer.PlaceBoard(fromDump, fd))
		cl := explorer.Complete(fromLedger, fl, explorer.PlaceBoard(fromLedger, fl))
		if cd != cl || fd.Hub != fl.Hub || fd.Family != fl.Family || fd.Channels != fl.Channels {
			t.Errorf("%s: the board from the dump %+v %s, from the ledger %+v %s", id, cd, fd.Family, cl, fl.Family)
			bad = true
		}
		if l, ok := listOf[id]; ok {
			want := fmt.Sprintf("%d|%s|%s|%s|%s|%s|%s", h.Sockets, h.SystemVendor, h.SystemProduct, h.Board, h.CPU, h.CPUSignature, h.CPUPresent)
			if got := hostOf[l]; got != want {
				t.Errorf("%s: the population file holds the host %q, the SMBIOS reader %q", id, got, want)
				bad = true
			}
		}
		if bad {
			differ++
		}
	}
	t.Logf("%d dumps, %d slots, %d dumps differ", dumps, slots, differ)
	if out := os.Getenv("ME_PARITY_REPORT"); out != "" {
		_ = os.WriteFile(out, []byte(fmt.Sprintf("ledger\t%d\t%d\t%d\n", dumps, slots, differ)), 0o644)
	}
}
