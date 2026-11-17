package view

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// fixtureBoard draws the board of a fixture under testdata/explorer as a
// ledger of its slots would give it: from dmidecode.txt with the socket
// count of its processor records, or from lshw.txt where it has no
// dmidecode text.
func fixtureBoard(t *testing.T, dir string) *Board {
	t.Helper()
	h := &engine.HostResult{Host: "h"}
	if raw, err := os.ReadFile(filepath.Join(dir, "dmidecode.txt")); err == nil {
		text := string(raw)
		reHandle := regexp.MustCompile(`(?m)^Handle (0x[0-9A-Fa-f]+), DMI type 17,`)
		handles := reHandle.FindAllStringSubmatch(text, -1)
		var slots []model.Slot
		for i, d := range explorer.ParseDmidecodeMemory(text, nil, nil) {
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
		return boardOf(i18n.EN, h, slots, fixtureHost(filepath.Join(dir, "dmidecode.txt")))
	}
	raw, err := os.ReadFile(filepath.Join(dir, "lshw.txt"))
	if err != nil {
		t.Errorf("%s: neither dmidecode.txt nor lshw.txt", filepath.Base(dir))
		return &Board{}
	}
	return boardOf(i18n.EN, h, nil, boardHost{lshw: string(raw)})
}

// fixtureHost is what the ledger keeps of the host of a dmidecode text, as
// the SMBIOS reader gives it.
func fixtureHost(path string) boardHost {
	var c adapter.Collected
	smbios.NewDmidecodeFile(path, smbios.Options{}).Collect(context.Background(), &c)
	var x model.Host
	if len(c.Hosts) > 0 {
		x = *c.Hosts[0]
	}
	return boardHost{vendor: x.SystemVendor, product: x.SystemProduct, board: x.Board, processor: x.CPU,
		signature: x.CPUSignature, sockets: x.Sockets, present: x.CPUPresent}
}

// sticksOf are the sticks of a board, on it and set aside.
func sticksOf(b *Board) []BoardStick {
	var out []BoardStick
	for _, r := range b.Rows {
		for _, p := range r.Parts {
			for _, c := range p.Channels {
				out = append(out, c.Sticks...)
			}
		}
	}
	return append(out, b.Unplaced...)
}

// drawn says where the board drew a stick, in the words of the table.
func drawn(st BoardStick) string {
	switch st.Place.State {
	case explorer.Placed:
		return "CPU " + strconv.Itoa(st.Place.Socket) + ", channel " + strconv.Itoa(st.Place.Channel)
	case explorer.NoChannel:
		return "CPU " + strconv.Itoa(st.Place.Socket) + ", no channels"
	}
	return "set aside"
}

// The table of slot names in docs/compatibility.md is what the board draws
// of the first slot of each fixture it names, and every pair of socket and
// channel test that a fixture's slots fire has a row.
func TestSlotNamesOfTheDocs(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(testutil.RepoRoot(t), "docs", "compatibility.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	start := strings.Index(text, "## Slot names of server vendors")
	if start < 0 {
		t.Fatal("docs/compatibility.md has no section on slot names")
	}
	section := text[start:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	root := testutil.Testdata(t, "explorer")
	reRow := regexp.MustCompile("(?m)^\\| ([^|]+) \\| `([^`]*)` \\| (?:`([^`]*)`)? ?\\| ([^|]+) \\| `([^`]+)` \\|$")
	rows := reRow.FindAllStringSubmatch(section, -1)
	if len(rows) < 20 {
		t.Fatalf("%d rows in the table of slot names", len(rows))
	}
	named := map[string]bool{}
	for _, r := range rows {
		locator, bank, want, fixture := r[2], r[3], strings.TrimSpace(r[4]), r[5]
		b := fixtureBoard(t, filepath.Join(root, fixture))
		sticks := sticksOf(b)
		sort.SliceStable(sticks, func(i, j int) bool { return naturalLess(sticks[i].Slot, sticks[j].Slot) })
		var first *BoardStick
		for i := range sticks {
			if sticks[i].Locator == locator && (bank == "" || sticks[i].BankLocator == bank) {
				first = &sticks[i]
				break
			}
		}
		if first == nil {
			t.Errorf("%s: no slot %q | %q", fixture, locator, bank)
			continue
		}
		if got := drawn(*first); got != want {
			t.Errorf("%s: slot %q | %q is drawn %q, the table says %q", fixture, locator, bank, got, want)
		}
		named[first.SocketBy+"|"+first.ChannelBy] = true
	}
	dirs, _ := filepath.Glob(filepath.Join(root, "*"))
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(dir, "dmidecode.txt")); err != nil {
			continue
		}
		for _, st := range sticksOf(fixtureBoard(t, dir)) {
			if k := st.SocketBy + "|" + st.ChannelBy; !named[k] {
				t.Errorf("%s: slot %q | %q is read by the tests %q, which no row of the table shows", filepath.Base(dir), st.Locator, st.BankLocator, k)
				named[k] = true
			}
		}
	}
}
