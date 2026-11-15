package term

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/synth"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/view"
)

func TestMain(m *testing.M) { testledger.Main(m) }

func TestDump(t *testing.T) {
	what := os.Getenv("MY_DUMP")
	if what == "" {
		t.Skip()
	}
	ctx := context.Background()
	scen := os.Getenv("MY_SCEN")
	if scen == "" {
		scen = "demo"
	}
	lang := i18n.Lang(os.Getenv("MY_LANG"))
	if lang == "" {
		lang = i18n.EN
	}
	s := testledger.Open(t, scen, 1, synth.Flavor(os.Getenv("MY_FLAVOR")))
	c, err := view.Open(ctx, s, lang, engine.Options{Now: time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	w, _ := strconv.Atoi(os.Getenv("MY_WIDTH"))
	st := Style{Lang: lang, Width: w, ASCII: os.Getenv("MY_ASCII") != "", Color: os.Getenv("MY_COLOR") != ""}
	slot := os.Getenv("MY_SLOT")
	if slot == "" {
		slot = h.DIMMs[0].Slot
	}
	d, err := c.Find(ctx, h, slot)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("\n")
	switch what {
	case "overview":
		ov, err := c.Overview(ctx, h, false)
		if err != nil {
			t.Fatal(err)
		}
		Overview(os.Stdout, ov, st, []Hint{{"my dimm " + slot, "examination"}, {"my why " + slot, "reasons"}, {"my bundle -o case.mycase", "case bundle"}, {"my help", "how to read the figures"}})
	case "dimm":
		x, err := c.Examine(ctx, h, d, st.MapCells())
		if err != nil {
			t.Fatal(err)
		}
		Examination(os.Stdout, x, st)
	case "why":
		card, err := c.Card(ctx, h, d)
		if err != nil {
			t.Fatal(err)
		}
		Why(os.Stdout, []*view.Card{card}, st)
		os.Stdout.WriteString(Oneline(card, st) + "\n")
	case "map":
		m, err := c.AddressMap(ctx, h, d, view.MapOptions{Cells: st.MapCells()})
		if err != nil {
			t.Fatal(err)
		}
		AddressMap(os.Stdout, m, st)
	case "tl":
		tl, err := c.Timeline(ctx, h, d, view.TimelineOptions{})
		if err != nil {
			t.Fatal(err)
		}
		Timeline(os.Stdout, tl, st)
	case "tlh":
		tl, err := c.Timeline(ctx, h, nil, view.TimelineOptions{})
		if err != nil {
			t.Fatal(err)
		}
		Timeline(os.Stdout, tl, st)
	}
}
