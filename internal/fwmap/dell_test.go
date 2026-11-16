package fwmap

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/fwevent"
)

// dellGroups are the common events of the Dell messages, typed from the
// registry texts: a range "UEFI0178-0241" stands for each of its IDs that
// the table has.
var dellGroups = []struct {
	event         fwevent.Name
	flags, clears string
	ids           string
}{
	{fwevent.CE, "", "", "MEM0000 MEM9014"},
	{fwevent.UE, "", "", "MEM0001 MEM0002 MEM9015 UEFI0058"},
	{fwevent.UE, "previous-boot", "", "UEFI0079"},
	{fwevent.ScrubCE, "", "", "MEM0003"},
	{fwevent.ScrubUE, "contained", "", "MEM9072"},
	{fwevent.Disabled, "", "", "MEM0004 UEFI0098 UEFI0339 UEFI0430"},
	{fwevent.CELogLimit, "", "", "MEM0005 MEM8000"},
	{fwevent.Presence, "", "", "MEM0006 MEM0022 MEM0600 MEM0601 MEM1017 PR7 PR8"},
	{fwevent.ConfigError, "", "", "MEM0007 MEM1016 MEM1203 MEM7002 PST0196 UEFI0081 UEFI0086-0097 UEFI0099-0102 UEFI0104 UEFI0113-0114 " +
		"UEFI0159-0177 UEFI0242 UEFI0251 UEFI0253-0254 UEFI0288-0294 UEFI0400-0401 UEFI0415"},
	{fwevent.ConfigState, "", "", "MEM0008"},
	{fwevent.Thermal, "", "", "MEM0009 MEM0010 SYS565"},
	{fwevent.Cleared, "", "", "MEM0016 MEM1200 MEM1207 MEM1210 MEM7000"},
	{fwevent.Cleared, "", "disabled", "MEM0020"},
	{fwevent.Cleared, "", "ce-log-limit", "MEM0021 MEM8001"},
	{fwevent.Cleared, "", "config-state", "MEM0024"},
	{fwevent.Cleared, "", "ce-threshold", "MEM0700"},
	{fwevent.Cleared, "", "mirror-lost", "MEM1204"},
	{fwevent.Cleared, "", "thermal", "SYS566"},
	{fwevent.CEThreshold, "", "", "MEM0701 MEM0702 MEM8502 MEM9016"},
	{fwevent.PPRScheduled, "", "", "MEM0802"},
	{fwevent.PPRDone, "", "", "MEM0804 MEM9060 UEFI0279"},
	{fwevent.PPRFailed, "", "", "MEM0805 UEFI0278"},
	{fwevent.MirrorLost, "", "", "MEM1201 MEM1205 MEM1212"},
	{fwevent.MirrorFailover, "", "", "MEM1202 MEM1206 MEM1214"},
	{fwevent.SparingFailed, "", "", "MEM1208"},
	{fwevent.Sparing, "", "", "MEM1209"},
	{fwevent.TrainingFailure, "", "", "UEFI0103 UEFI0106-0112 UEFI0144 UEFI0178-0241 UEFI0243 UEFI0262 UEFI0326-0336 " +
		"PST0091 PST0128-0130 PST0142 PST0193"},
	{fwevent.LogCleared, "", "", "SEL0004 SEL0014"},
	{fwevent.LogFull, "", "", "SEL0002 SEL0006 SEL0008"},
	{fwevent.Unclassified, "", "", "MEM1000-1015 MEM5100-5199 MEM6101-6199 MEM7101-7199 MEM9000-9013 MEM9020-9059 MEM9061 MEM9073"},
}

// dellIDs expands the IDs of a group: "UEFI0178-0241" gives every ID of
// the range that has its prefix and four digits.
func dellIDs(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		first, last, ok := strings.Cut(f, "-")
		if !ok {
			out = append(out, f)
			continue
		}
		i := strings.IndexAny(first, "0123456789")
		var a, b int
		fmt.Sscanf(first[i:], "%d", &a)
		fmt.Sscanf(last, "%d", &b)
		for n := a; n <= b; n++ {
			out = append(out, fmt.Sprintf("%s%0*d", first[:i], len(first)-i, n))
		}
	}
	return out
}

// sample values for the roles of a Dell message, and the place they give.
var dellArg = map[string]string{"location": "DIMM.Socket.A7", "locations": "DIMM_A1, DIMM_B2", "socket": "1", "channel": "2",
	"serial": "0F1E2D3C", "part": "EX-PART-1", "code": "0x1234", "-": "x"}

func init() {
	var entries []handEntry
	tb := Shipped().Table("dell")
	if tb == nil {
		return
	}
	want := map[string]int{}
	for i, g := range dellGroups {
		for _, id := range dellIDs(g.ids) {
			want[id] = i
		}
	}
	for _, l := range tb.Lines {
		if l.Text != nil {
			continue
		}
		g, ok := want[l.ID]
		if !ok {
			// No group names the line: the test of the lines fails on it.
			continue
		}
		var args []string
		place := ""
		for _, r := range l.Args {
			args = append(args, dellArg[r])
			switch r {
			case "location":
				place = "DIMM A7"
			case "locations":
				place = "DIMM A1,DIMM B2"
			}
		}
		if place == "" && len(l.Args) > 0 && l.Args[0] == "socket" {
			place = "1/-/-/-"
			if len(l.Args) > 1 && l.Args[1] == "channel" {
				place = "1/2/-/-"
			}
		}
		if l.ID == "PR7" || l.ID == "PR8" {
			args = []string{"DIMM A7"}
		}
		id := "IDRAC.2.15." + l.ID
		if l.NArgs == 2 {
			id = "IDRAC.2.9." + l.ID
		}
		entries = append(entries, handEntry{id: id, args: args, want: dellGroups[g].event, flags: dellGroups[g].flags,
			clears: fwevent.Name(dellGroups[g].clears), place: place})
	}
	handEntries["dell"] = entries
}

// TestDellGroups: every ID a group names is a line of the table, and every
// ID line of the table is in a group.
func TestDellGroups(t *testing.T) {
	tb := Shipped().Table("dell")
	have := map[string]bool{}
	for _, l := range tb.Lines {
		if l.Text == nil {
			have[l.ID] = true
		}
	}
	named := map[string]bool{}
	for _, g := range dellGroups {
		for _, id := range dellIDs(g.ids) {
			named[id] = true
		}
	}
	missing := 0
	for id := range have {
		if !named[id] {
			t.Errorf("the line %s is in no group", id)
		}
	}
	for id := range named {
		if !have[id] {
			missing++
		}
	}
	// The ranges name IDs the registries do not have (MEM5105, MEM9023 and
	// the like); the count of IDs the table holds is fixed.
	counts := map[string]int{}
	for id := range have {
		counts[strings.TrimRight(id, "0123456789")]++
	}
	if counts["MEM"] != 120 || counts["UEFI"] != 146 || counts["PST"] != 7 || counts["SEL"] != 5 || counts["SYS"] != 2 || counts["PR"] != 2 {
		t.Errorf("IDs by prefix %v", counts)
	}
	// MEM8500 and MEM8501, the low memory of the operating system, are
	// skipped: with them every one of the 122 MEM IDs is placed.
	if r := Shipped().Classify(Entry{MessageID: "IDRAC.2.9.MEM8501", Args: []string{"100", "200"}}); !r.Skipped {
		t.Errorf("MEM8501: %+v", r)
	}
	for _, id := range []string{"IDRAC.2.9.PDR111", "IDRAC.2.15.SSD0001", "IDRAC.2.15.BAT0002"} {
		if r := Shipped().Classify(Entry{MessageID: id, Message: "The volatile memory backup device on PCIe SSD is not working."}); !r.Skipped {
			t.Errorf("%s is not skipped: %+v", id, r)
		}
	}
}

// TestDellVariants: MEM6102, MEM6104 and MEM7114 put serial and part
// number before the location from registry 2.14.0 on.
func TestDellVariants(t *testing.T) {
	c := Shipped()
	for _, id := range []string{"MEM6102", "MEM6104", "MEM7114"} {
		old := c.Classify(Entry{MessageID: "IDRAC.2.9." + id, Args: []string{"DIMM.Socket.B2", "0x51"}})
		cur := c.Classify(Entry{MessageID: "IDRAC.2.15." + id, Args: []string{"0F1E2D3C", "EX-PART-1", "DIMM.Socket.B2", "0x51"}})
		po, pc := PlaceOf("dell", old.Roles, []string{"DIMM.Socket.B2", "0x51"}), PlaceOf("dell", cur.Roles, []string{"0F1E2D3C", "EX-PART-1", "DIMM.Socket.B2", "0x51"})
		if strings.Join(po.Labels, ",") != "DIMM B2" || strings.Join(pc.Labels, ",") != "DIMM B2" || pc.Serial != "0F1E2D3C" || pc.Part != "EX-PART-1" {
			t.Errorf("%s: %+v, %+v", id, po, pc)
		}
	}
	// The registry of the BMC gives the number of arguments when the entry
	// lacks some.
	r := c.Classify(Entry{MessageID: "IDRAC.2.15.MEM7114", Registry: &RegistryMessage{NArgs: 4}})
	if r.Line == nil || r.Line.NArgs != 4 {
		t.Errorf("by the registry: %+v", r.Line)
	}
}

// TestDellText: the sentences racadm getsel prints, read by the text lines
// for a log of a Dell system. The locations are made up.
func TestDellText(t *testing.T) {
	c := Shipped()
	met := map[*Line]bool{}
	for _, tt := range []struct {
		msg      string
		id       string
		want     fwevent.Name
		location string
	}{
		{"The system memory has uncorrectable multi-bit memory errors in the non-execution path of a memory device at the location DIMM_B7. Immediately replace the DIMM.", "MEM9072", fwevent.ScrubUE, "DIMM_B7"},
		{"Multi-bit memory errors are detected on the memory device at location(s) DIMM_B7. Immediately replace the DIMM.", "MEM0001", fwevent.UE, "DIMM_B7"},
		{"Log cleared.", "SEL0004", fwevent.LogCleared, ""},
		{"The system memory has detected an error on a memory transaction at the location DIMM_C3. Reboot the system for auto-healing.", "MEM9072", fwevent.ScrubUE, "DIMM_C3"},
		{"Persistent correctable memory errors detected on a memory device at location(s) DIMM_A3, DIMM_A4.", "MEM0000", fwevent.CE, "DIMM_A3, DIMM_A4"},
		{"Parity memory errors detected on a memory device at location DIMM_A3.", "MEM0002", fwevent.UE, "DIMM_A3"},
		{"Stuck bit memory error detected on a memory device at location DIMM_A3.", "MEM0003", fwevent.ScrubCE, "DIMM_A3"},
		{"Memory device at location DIMM_A3 is disabled.", "MEM0004", fwevent.Disabled, "DIMM_A3"},
		{"Memory device at location DIMM_A3 is enabled.", "MEM0020", fwevent.Cleared, "DIMM_A3"},
		{"Persistent correctable memory error limit reached for a memory device at location(s) DIMM_A3.", "MEM0005", fwevent.CELogLimit, "DIMM_A3"},
		{"Correctable memory error logging disabled for a memory device at location DIMM_A3.", "MEM8000", fwevent.CELogLimit, "DIMM_A3"},
		{"Persistent correctable memory error logging enabled for a memory device at location DIMM_A3.", "MEM8001", fwevent.Cleared, "DIMM_A3"},
		{"Correctable memory errors detected for DIMM_A3.", "MEM0701", fwevent.CEThreshold, "DIMM_A3"},
		{"The persistent correctable memory error rate is at normal levels for a memory device at location DIMM_A3.", "MEM0700", fwevent.Cleared, "DIMM_A3"},
		{"The memory health monitor feature has detected a degradation in the DIMM installed in DIMM_A3. Reboot system to initiate self-heal process.", "MEM0802", fwevent.PPRScheduled, "DIMM_A3"},
		{"The self-heal operation successfully completed at DIMM DIMM_A3.", "MEM0804", fwevent.PPRDone, "DIMM_A3"},
		{"Unable to complete the self-heal operation of the memory in the DIMM installed in DIMM_A3 because of internal issues.", "MEM0805", fwevent.PPRFailed, "DIMM_A3"},
		{"The self-heal operation successfully completed on the memory module.", "MEM9060", fwevent.PPRDone, ""},
		{"A correctable memory error has occurred in Dual In-line Memory Module (DIMM) device DIMM_A3.", "MEM9014", fwevent.CE, "DIMM_A3"},
		{"Multi-bit memory errors detected on a Dual In-line Memory Module (DIMM) device DIMM_A3.", "MEM9015", fwevent.UE, "DIMM_A3"},
		{"Correctable memory error rate exceeded for Dual In-line Memory Module (DIMM) DIMM_A3.", "MEM9016", fwevent.CEThreshold, "DIMM_A3"},
		{"Memory mirror redundancy is lost. Check memory device at location(s) DIMM_A3, DIMM_B3.", "MEM1205", fwevent.MirrorLost, "DIMM_A3, DIMM_B3"},
		{"Memory mirror redundancy is degraded. Check memory device at location DIMM_A3.", "MEM1206", fwevent.MirrorFailover, "DIMM_A3"},
		{"Memory spare redundancy is lost. Check memory device at location DIMM_A3.", "MEM1208", fwevent.SparingFailed, "DIMM_A3"},
		{"Memory spare redundancy is degraded. Check memory device at location DIMM_A3.", "MEM1209", fwevent.Sparing, "DIMM_A3"},
		{"System event log (SEL) is full.", "SEL0008", fwevent.LogFull, ""},
	} {
		r := c.Classify(Entry{Message: tt.msg, Manufacturer: "Dell Inc."})
		met[r.Line] = true
		if r.Event != tt.want || r.Key != tt.id || r.Line == nil || r.Line.Text == nil || strings.TrimSpace(r.Text["location"]) != tt.location {
			t.Errorf("%q: %s %s %q, want %s %s %q", tt.msg, r.Event, r.Key, r.Text["location"], tt.want, tt.id, tt.location)
		}
	}
	// Every text line is met above.
	for _, l := range c.Table("dell").texts {
		if !met[l] {
			t.Errorf("the text line %s (line %d of the file) is met by no sentence", l.ID, l.Line)
		}
	}
	// Without the maker, or for another maker, no pattern is asked.
	if r := c.Classify(Entry{Message: "Memory device at location DIMM_A3 is disabled.", Manufacturer: "Example Systems"}); r.Line != nil {
		t.Errorf("another maker: %+v", r.Line)
	}
	if r := c.Classify(Entry{Message: "An OEM diagnostic event occurred.", Manufacturer: "Dell Inc."}); r.Memory {
		t.Errorf("a sentence about nothing of memory: %+v", r)
	}
}
