package labcase

import (
	"testing"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/view"
)

// boardGuest is a capture of tools/lab/boards.sh and what the board and
// the tabs of the explorer make of it.
type boardGuest struct {
	dir string
	// sockets is the number of processor records; slots and populated
	// count the slots of SMBIOS.
	sockets, slots, populated int
	// places are the CPU and channel the board draws each slot at, by
	// slot name.
	places map[string][2]int
	// socketBy and channelBy name the tests that read the place of every
	// slot.
	socketBy, channelBy string
	// nodes is the number of NUMA nodes of numactl --hardware.
	nodes int
}

var boardGuests = []boardGuest{
	{
		dir: "6.19.10-board-two-sockets", sockets: 2, slots: 8, populated: 4, nodes: 2,
		socketBy: explorer.ByCPU, channelBy: explorer.ByCHDimm,
		places: map[string][2]int{
			"CPU0_CH0_DIMM0": {0, 0}, "CPU0_CH0_DIMM1": {0, 0}, "CPU0_CH1_DIMM0": {0, 1}, "CPU0_CH1_DIMM1": {0, 1},
			"CPU1_CH0_DIMM0": {1, 0}, "CPU1_CH0_DIMM1": {1, 0}, "CPU1_CH1_DIMM0": {1, 1}, "CPU1_CH1_DIMM1": {1, 1},
		},
	},
	{
		dir: "6.19.10-board-digits", sockets: 1, slots: 4, populated: 2, nodes: 1,
		socketBy: explorer.ByDimmDigits, channelBy: explorer.ByDimmDigits,
		places: map[string][2]int{"DIMM000": {0, 0}, "DIMM001": {0, 0}, "DIMM010": {0, 1}, "DIMM011": {0, 1}},
	},
	{
		dir: "6.19.10-board-letters", sockets: 1, slots: 4, populated: 2, nodes: 1,
		socketBy: "", channelBy: explorer.ByDimmLetter,
		places: map[string][2]int{"DIMMA1": {0, 0}, "DIMMA2": {0, 0}, "DIMMB1": {0, 1}, "DIMMB2": {0, 1}},
	},
}

// The board of each guest places every slot where its name says, and the
// copy of the guest holds every reading of the explorer.
func TestBoardsOfTheLabGuests(t *testing.T) {
	for _, g := range boardGuests {
		t.Run(g.dir, func(t *testing.T) {
			c := capture{dir: g.dir}
			s := ledger(t)
			if _, err := scan.Run(ctx, s, scan.Options{Root: dir(t, c), TempDir: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			cs, err := view.Open(ctx, s, i18n.EN, engine.Options{Now: capturedAt(t, c)})
			if err != nil {
				t.Fatal(err)
			}
			h, err := cs.Host("")
			if err != nil {
				t.Fatal(err)
			}
			b, err := cs.Board(ctx, h)
			if err != nil {
				t.Fatal(err)
			}
			if b.Sockets != g.sockets || b.Slots != g.slots || b.Populated != g.populated {
				t.Errorf("board of %d sockets, %d slots, %d populated; want %d, %d, %d", b.Sockets, b.Slots, b.Populated, g.sockets, g.slots, g.populated)
			}
			if b.Counts.Placed != g.slots || b.Counts.NoChannel != 0 || b.Counts.Unplaced != 0 || b.Aside != nil {
				t.Errorf("counts %+v, aside %v; want every slot placed", b.Counts, b.Aside != nil)
			}
			// lshw did not answer within the 15 seconds my collect host
			// gives it in the emulated guests: the caches come from sysfs.
			if b.Caches != "sysfs" {
				t.Errorf("caches from %q, want sysfs", b.Caches)
			}
			cpus, seen := 0, map[string]bool{}
			for _, r := range b.Rows {
				for _, p := range r.Parts {
					if p.CPU != nil {
						cpus++
						if len(p.CPU.Caches) == 0 {
							t.Errorf("CPU %d has no caches", p.CPU.Socket)
						}
					}
					for _, ch := range p.Channels {
						for _, st := range ch.Sticks {
							seen[st.Slot] = true
							want, ok := g.places[st.Slot]
							if !ok {
								t.Errorf("slot %s is not one of the guest", st.Slot)
								continue
							}
							if st.Place.State != explorer.Placed || st.Place.Socket != want[0] || st.Place.Channel != want[1] {
								t.Errorf("%s: %+v, want CPU %d channel %d", st.Slot, st.Place, want[0], want[1])
							}
							if st.SocketBy != g.socketBy || st.ChannelBy != g.channelBy {
								t.Errorf("%s: read by %q and %q, want %q and %q", st.Slot, st.SocketBy, st.ChannelBy, g.socketBy, g.channelBy)
							}
						}
					}
				}
			}
			if cpus != g.sockets || len(seen) != g.slots {
				t.Errorf("%d CPUs and %d slots drawn, want %d and %d", cpus, len(seen), g.sockets, g.slots)
			}
			e, err := cs.Explorer(ctx, h)
			if err != nil {
				t.Fatal(err)
			}
			if len(e.Missing) != 0 || len(e.Faults) != 0 {
				t.Errorf("readings missing %v, failed %v", e.Missing, e.Faults)
			}
			if e.System == nil || len(e.Slabs) == 0 || e.Page == nil || len(e.Page.SarB) == 0 || e.Mglru == nil {
				t.Errorf("a tab is empty: system %v, %d slabs, page %v, MGLRU %v", e.System != nil, len(e.Slabs), e.Page != nil, e.Mglru != nil)
			}
			if len(e.Numa) != g.nodes || e.NumaAll == nil || !e.NumaAll.HasCounts {
				t.Errorf("%d NUMA nodes, want %d, with counts", len(e.Numa), g.nodes)
			}
		})
	}
}
