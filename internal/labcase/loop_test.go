package labcase

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/injection"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/term"
	"github.com/agenticode/my/internal/testutil"
	"github.com/agenticode/my/internal/view"
)

// A loop capture: a guest that was made to stop with a fatal error boot
// after boot. boots.jsonl names the boots of the plan, and the kept ledger
// of my harvest is part of the copy.
type loop struct {
	dir  string
	host string
	// window is the number of boots of the window, stops those that end
	// in a stop: the boots of the plan. copyWindow is the window of the
	// copy of the host where it differs: a copy without the journal knows
	// the boots its other files name.
	window, copyWindow, stops int
	// copyStops and copyWhere, when set, are what the copy of the host
	// gives instead of the plan: a copy without the journal holds the
	// last boot only, and the plan is checked on the kept ledger.
	copyStops int
	copyWhere string
	// where says where the uncorrected errors of the window lie
	// (engine.Recurrence.Where).
	where string
	// rule is the statement that says it boot over boot: the code of an
	// evidence of the host finding, or "" for none.
	rule string
	// dimmRule is the rule every DIMM of the plan fires, when the records
	// name a DIMM.
	dimmRule string
}

var loops = []loop{
	{dir: "6.19.10-mce-loop", host: "lab-x86", window: 4, stops: 3, where: "none", rule: "host.loop_address"},
	{dir: "6.19.10-mce-loop-scatter", host: "lab-x86", window: 4, stops: 3, where: "none"},
	{dir: "6.19.10-mce-loop-kdump", host: "lab-x86", window: 3, copyWindow: 2, stops: 1, where: "none"},
	{dir: "6.19.10-ghes-loop-spread", host: "lab-arm-loop", window: 5, copyWindow: 4, stops: 3, where: engine.RuleLoopSpread, rule: "loop.spread_host", dimmRule: engine.RuleLoopSpread},
	{dir: "6.19.10-bert-loop", host: "lab-x86", window: 4, copyWindow: 2, stops: 3, where: engine.RuleLoopPlace, dimmRule: engine.RuleLoopPlace,
		copyStops: 1, copyWhere: "dimm"},
}

// loopBoot is one line of boots.jsonl: what was injected in a boot.
type loopBoot struct {
	Boot     int       `json:"boot"`
	BootID   string    `json:"boot_id"`
	Injected time.Time `json:"injected"`
	// Slot names the DIMM of the record, when the plan puts it on one.
	Slot   string `json:"slot"`
	Record struct {
		Time    time.Time `json:"time"`
		Address string    `json:"address"`
	} `json:"record"`
	Address string `json:"address"`
	Bank    int    `json:"bank"`
	Status  string `json:"status"`
}

func loopBoots(t *testing.T, l loop) []loopBoot {
	t.Helper()
	f, err := os.Open(testutil.Testdata(t, "real", l.dir, "boots.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []loopBoot
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var b loopBoot
		if err := json.Unmarshal(sc.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

// TestLoops reads each loop capture twice, through its kept ledger and as
// a copy of the host, and compares the boots with the plan: per boot of
// the plan the place, the outcome, and the rule that speaks about it.
func TestLoops(t *testing.T) {
	for _, l := range loops {
		t.Run(l.dir, func(t *testing.T) {
			kept := keptLedger(t, l)
			copied := ledger(t)
			res, err := scan.Run(ctx, copied, scan.Options{Root: testutil.Testdata(t, "real", l.dir), TempDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if res.Host != l.host {
				t.Errorf("copy: host %q", res.Host)
			}
			p, err := injection.ReadFile(testutil.Testdata(t, "real", l.dir, "plan.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			at := capturedAt(t, capture{dir: l.dir})
			for name, s := range map[string]*store.Store{"kept ledger": kept, "copy": copied} {
				if name == "copy" && l.copyStops > 0 {
					out, err := engine.Run(ctx, s, engine.Options{Now: at})
					if err != nil {
						t.Fatal(err)
					}
					h := out.Host(l.host)
					if h == nil || h.Recurrence == nil {
						t.Fatalf("copy: no boots of %s", l.host)
					}
					if r := h.Recurrence; r.Window() != l.copyWindow || r.Stops != l.copyStops || r.Where != l.copyWhere {
						t.Errorf("copy: window %d, stops %d, where %q; want %d, %d, %q", r.Window(), r.Stops, r.Where, l.copyWindow, l.copyStops, l.copyWhere)
					}
					continue
				}
				cmp, err := injection.Compare(ctx, s, l.host, p)
				if err != nil {
					t.Fatal(err)
				}
				for _, ch := range cmp.Failed() {
					t.Errorf("%s: injection %s: found %d of %d, differences %v", name, ch.Injection.ID, ch.Found, ch.Injection.Count, ch.Differences)
				}
				if cmp.UnplannedErrors != 0 {
					t.Errorf("%s: beyond the plan: %v", name, cmp.Unplanned)
				}
				out, err := engine.Run(ctx, s, engine.Options{Now: at})
				if err != nil {
					t.Fatal(err)
				}
				h := out.Host(l.host)
				if h == nil || h.Recurrence == nil {
					t.Fatalf("%s: no boots of %s", name, l.host)
				}
				checkLoop(t, name, l, loopBoots(t, l), h)
			}
			// my boots of the kept ledger, as the screen shows it.
			c, err := view.Open(ctx, kept, i18n.EN, engine.Options{Now: at})
			if err != nil {
				t.Fatal(err)
			}
			h, err := c.Host(l.host)
			if err != nil {
				t.Fatal(err)
			}
			b, err := c.BootsView(ctx, h, nil)
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := term.Boots(&buf, b, term.Style{Lang: i18n.EN}, 0, nil); err != nil {
				t.Fatal(err)
			}
			testutil.Golden(t, l.dir+".boots.txt", buf.Bytes())
		})
	}
}

func checkLoop(t *testing.T, name string, l loop, plan []loopBoot, h *engine.HostResult) {
	t.Helper()
	r := h.Recurrence
	window := l.window
	if name == "copy" && l.copyWindow > 0 {
		window = l.copyWindow
	}
	if r.Window() != window || r.Stops != l.stops || r.Where != l.where {
		t.Errorf("%s: window %d, stops %d, where %q; want %d, %d, %q", name, r.Window(), r.Stops, r.Where, window, l.stops, l.where)
	}
	// The boot of the window an injection fell into: by its boot ID, or
	// by time where a source does not record the ID (a pstore dump).
	bootOf := func(pb loopBoot) int {
		for _, b := range r.Boots {
			if b.BootID == pb.BootID {
				return b.Index
			}
		}
		for k, b := range r.Boots {
			if !b.StartedAt.After(pb.Injected) && (k+1 == len(r.Boots) || r.Boots[k+1].StartedAt.After(pb.Injected)) {
				return b.Index
			}
		}
		return 0
	}
	for _, pb := range plan {
		if pb.Injected.IsZero() {
			pb.Injected = pb.Record.Time
		}
		if pb.Address == "" {
			pb.Address = pb.Record.Address
		}
		i := bootOf(pb)
		if i == 0 {
			t.Errorf("%s: boot %d of the plan (%s) is not in the window", name, pb.Boot, pb.BootID)
			continue
		}
		if end := r.Boots[i-1].End; end != model.BootStop {
			t.Errorf("%s: boot %d ended %q, want a stop", name, pb.Boot, end)
		}
		// The place of the boot: on the DIMM of the plan, or the cache line
		// of the injected address among the errors that name no DIMM.
		found := false
		if pb.Slot != "" {
			for _, db := range r.DIMMs {
				if db.Slot != pb.Slot {
					continue
				}
				for _, c := range db.Cells {
					found = found || (c.Boot == i && c.UE > 0 && c.Stopped)
				}
				if l.dimmRule != "" {
					switch d := h.DIMM(db.UID); {
					case d == nil:
						t.Errorf("%s: %s: no finding", name, pb.Slot)
					case !hasRule(d.Verdict.Rules, l.dimmRule):
						t.Errorf("%s: %s: rules %v, want %s", name, pb.Slot, d.Verdict.Rules, l.dimmRule)
					}
				}
			}
		}
		for _, place := range r.Loose {
			if place.Level != engine.PlaceAddress || !sameLine(place.Place, pb.Address) {
				continue
			}
			for _, c := range place.Cells {
				found = found || (c.Boot == i && c.UE > 0 && c.Stopped)
			}
		}
		if !found {
			t.Errorf("%s: boot %d: no uncorrected error at %s in it: %+v", name, pb.Boot, pb.Address, r.Loose)
		}
	}
	var codes []string
	if h.Loose != nil {
		for _, e := range h.Loose.Evidence {
			codes = append(codes, e.Code)
		}
	}
	has := false
	for _, c := range codes {
		has = has || c == l.rule
	}
	if l.rule != "" && !has || l.rule == "" && strings.Contains(strings.Join(codes, " "), "loop") {
		t.Errorf("%s: the host finding says %v, want %q", name, codes, l.rule)
	}
}

func hasRule(rules []string, id string) bool {
	for _, r := range rules {
		if r == id {
			return true
		}
	}
	return false
}

// sameLine says whether two addresses lie in one cache line.
func sameLine(a, b string) bool {
	x, y := parseHex(a), parseHex(b)
	return x >= 0 && x>>6 == y>>6
}

func parseHex(s string) int64 {
	var v int64
	s = strings.TrimPrefix(strings.ToLower(s), "0x")
	if s == "" {
		return -1
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			v = v<<4 | int64(r-'0')
		case r >= 'a' && r <= 'f':
			v = v<<4 | int64(r-'a'+10)
		default:
			return -1
		}
	}
	return v
}

// keptLedger opens a copy of the kept ledger of a capture: a test must not
// change the file of the checkout. The copy is opened for writing, so that
// a ledger of an older schema is migrated, as my open does.
func keptLedger(t *testing.T, l loop) *store.Store {
	t.Helper()
	src := testutil.Testdata(t, "real", l.dir, "var", "lib", "my", "ledger.db")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ledger.db")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, path, store.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
