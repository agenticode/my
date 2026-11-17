package view

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/explorer"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/memras"
	"github.com/agenticode/my/internal/population"
)

// Population is the population check of a board (docs/rules.md,
// "Population rules"): the rule set of the board, per processor the slots
// that hold modules and the slots the vendor's table fills for their
// number, the findings, the lines that are no finding and the rules that
// were not checked; or why no rule set was used. A population finding is
// no finding of a DIMM: it has no grade and changes nothing of the
// engine.
type Population struct {
	// Set is the rule set; NoRule why none was used: no-set or not-full.
	Set    string `json:"set,omitzero"`
	NoRule string `json:"no_rule,omitzero"`
	// Ordered is set when the rule set holds a fill order; Strength is the
	// strength of its wording.
	Ordered    bool                   `json:"ordered,omitzero"`
	Strength   string                 `json:"strength,omitzero"`
	Processors []population.Processor `json:"processors,omitzero"`
	Findings   []PopulationFinding    `json:"findings,omitzero"`
	Notes      []PopulationNote       `json:"notes,omitzero"`
	Unchecked  []PopulationUnchecked  `json:"unchecked,omitzero"`
	// Text is the line of no rule.
	Text *Text `json:"text,omitzero"`
}

// PopulationFinding is one population finding with its sentence, the
// strength of the document's wording and the page it stands on.
type PopulationFinding struct {
	Rule         string `json:"rule"`
	Strength     string `json:"strength"`
	StrengthText Text   `json:"strength_text"`
	// Socket is the processor, -1 for the system; Channel the vendor's
	// name of the channel.
	Socket  int    `json:"socket"`
	Channel string `json:"channel,omitzero"`
	// Fill and Leave are the empty slots the table fills and the modules
	// in slots it leaves empty; Slots every slot the finding names; First
	// the slot of the channel the table fills first.
	Fill   []string         `json:"fill,omitzero"`
	Leave  []string         `json:"leave,omitzero"`
	Slots  []string         `json:"slots,omitzero"`
	First  string           `json:"first,omitzero"`
	Counts []int            `json:"counts,omitzero"`
	Types  []string         `json:"types,omitzero"`
	Ranks  []int            `json:"ranks,omitzero"`
	Bytes  []int64          `json:"bytes,omitzero"`
	Speeds []int            `json:"speeds,omitzero"`
	Parts  int              `json:"parts,omitzero"`
	Source PopulationSource `json:"source"`
	Text   Text             `json:"text"`
}

// PopulationNote is a line that is no finding: the modules stand as the
// table has them, as the table of another mode or the second set has
// them, or the table lists no population for their number.
type PopulationNote struct {
	Kind    string            `json:"kind"`
	Socket  int               `json:"socket"`
	Modules int               `json:"modules"`
	Counts  []int             `json:"counts,omitzero"`
	Mode    string            `json:"mode,omitzero"`
	Source  *PopulationSource `json:"source,omitzero"`
	Text    Text              `json:"text"`
}

// PopulationUnchecked is a rule that was not checked, and why.
type PopulationUnchecked struct {
	Rule  string `json:"rule"`
	Field string `json:"field"`
	N     int    `json:"n"`
	Mode  string `json:"mode,omitzero"`
	Text  Text   `json:"text"`
}

// PopulationSource is the page of a public document a line stands on.
type PopulationSource struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Number string `json:"number,omitzero"`
	Date   string `json:"date,omitzero"`
	URL    string `json:"url,omitzero"`
	Pages  string `json:"pages"`
	// Cite is title, number and page in one line, in the language of
	// the document.
	Cite string `json:"cite"`
}

// The check of the population, replaced by tests that make it fail.
var populationCheck = population.Check

// populationOf runs the population check of a board after its placement.
// A fault of the check gives no population and one line of data quality.
func populationOf(lang i18n.Lang, b *Board, bh boardHost, f explorer.Facts, dimms []explorer.Dimm, sticks []BoardStick) {
	in := population.Board{Maker: f.Maker, Product: bh.product, BoardName: bh.board, Family: f.Family,
		Processor: f.Processor.Family, Sockets: bh.sockets, Present: f.Present, Mode: bh.mode, Full: b.Complete.Full}
	live := 0
	for i, st := range sticks {
		if st.Place.State == explorer.NotASlot {
			continue
		}
		live++
		if st.Place.State != explorer.Placed || st.Label != "" {
			continue
		}
		s := population.Slot{Name: st.Slot, Socket: st.Place.Socket, Channel: st.Place.Channel, Position: st.Place.Position,
			Populated: st.State != SlotEmpty && st.State != SlotUnknown}
		if i < len(dimms) {
			s.ChannelName = dimms[i].ChannelName
		}
		if s.Populated {
			s.Module.Bytes = st.Bytes
			if m := st.Module; m != nil {
				s.Module.Type, s.Module.Ranks, s.Module.Speed, s.Module.Part = m.Type, m.Ranks.Or(0), m.SpeedMTs.Or(0), m.PartNumber
			}
		}
		in.Slots = append(in.Slots, s)
	}
	if live == 0 {
		return
	}
	in.NotPlaced = live - len(in.Slots)
	r := adapter.Guard(func() *population.Result { x := populationCheck(in); return &x })
	if r == nil {
		b.Lines = append(b.Lines, boardSay(lang, "pop.fault"))
		return
	}
	b.Population = populationView(lang, *r, bh)
	marks := r.Marks()
	if len(marks) == 0 {
		return
	}
	for ri := range b.Rows {
		for pi := range b.Rows[ri].Parts {
			for ci := range b.Rows[ri].Parts[pi].Channels {
				ss := b.Rows[ri].Parts[pi].Channels[ci].Sticks
				for si := range ss {
					ss[si].Mark = marks[ss[si].Slot]
				}
			}
		}
	}
}

// populationView turns the result of the check into the view.
func populationView(lang i18n.Lang, r population.Result, bh boardHost) *Population {
	p := &Population{Set: r.Set, NoRule: r.NoRule, Ordered: r.Ordered, Strength: r.Strength, Processors: r.Processors}
	switch r.NoRule {
	case population.NoSet:
		system := strings.TrimSpace(bh.vendor + " " + bh.product)
		if b := strings.TrimSpace(bh.board); b != "" && !strings.Contains(system, b) {
			system = strings.TrimSpace(system + " " + b)
		}
		t := boardSay(lang, "pop.no_set", "system", system)
		p.Text = &t
		return p
	case population.NotFull:
		code := "pop.not_full"
		if r.NotPlaced == 0 {
			code = "pop.not_full_checks"
		}
		t := boardSay(lang, code, "n", r.NotPlaced)
		p.Text = &t
		return p
	}
	cite := func(refs []population.Ref) PopulationSource { return citeOf(refs, bh.product) }
	for _, f := range r.Findings {
		pf := PopulationFinding{Rule: f.Rule, Strength: f.Strength, StrengthText: boardSay(lang, "pop."+f.Strength), Socket: f.Socket,
			Channel: f.Channel, Fill: f.Fill, Leave: f.Leave, Slots: f.Slots, First: f.First, Counts: f.Counts, Types: f.Types,
			Ranks: f.Ranks, Bytes: f.Bytes, Speeds: f.Speeds, Parts: f.Parts, Source: cite(f.Source)}
		pf.Text = findingText(lang, f, r)
		p.Findings = append(p.Findings, pf)
	}
	for _, n := range r.Notes {
		pn := PopulationNote{Kind: n.Kind, Socket: n.Socket, Modules: n.Modules, Counts: n.Counts, Mode: n.Mode}
		var src string
		if len(n.Source) > 0 {
			s := cite(n.Source)
			pn.Source, src = &s, s.Cite
		}
		switch n.Kind {
		case population.Agrees:
			pn.Text = boardSay(lang, "pop.agrees", "n", n.Modules, "source", src)
		case population.Holds:
			pn.Text = boardSay(lang, "pop.holds", "n", n.Modules, "source", src)
		case population.NotCompared:
			code := "pop.not_compared"
			if len(n.Counts) == 1 && n.Counts[0] == 1 {
				code = "pop.not_compared_one"
			}
			pn.Text = boardSay(lang, code, "cpu", n.Socket, "n", n.Modules, "counts", countsText(lang, n.Counts))
		case population.OtherMode:
			pn.Text = boardSay(lang, "pop.other_mode", "cpu", n.Socket, "n", n.Modules, "mode", memras.StateText(lang, n.Mode), "source", src)
		case population.SecondSet:
			pn.Text = boardSay(lang, "pop.second_set", "cpu", n.Socket, "n", n.Modules, "source", src)
		}
		p.Notes = append(p.Notes, pn)
	}
	for _, u := range r.Unchecked {
		pu := PopulationUnchecked{Rule: u.Rule, Field: u.Field, N: u.N, Mode: u.Mode}
		switch u.Field {
		case population.FieldStatus:
			pu.Text = boardSay(lang, "pop.unchecked.status", "rule", u.Rule, "n", u.N)
		case population.FieldPlaces:
			pu.Text = boardSay(lang, "pop.unchecked.places", "rule", u.Rule, "n", u.N)
		case population.FieldMode:
			pu.Text = boardSay(lang, "pop.unchecked.mode", "rule", u.Rule, "mode", memras.StateText(lang, u.Mode))
		default:
			pu.Text = boardSay(lang, "pop.unchecked", "rule", u.Rule, "n", u.N, "field", "@pop.field."+u.Field)
		}
		p.Unchecked = append(p.Unchecked, pu)
	}
	return p
}

// findingText is the sentence of a finding: what stands where and what
// the table or the document has.
func findingText(lang i18n.Lang, f population.Finding, r population.Result) Text {
	slot := func(i int) string {
		if i < len(f.Slots) {
			return f.Slots[i]
		}
		return "?"
	}
	switch f.Rule {
	case population.Order:
		for _, p := range r.Processors {
			if p.Socket == f.Socket {
				return boardSay(lang, "pop.order", "cpu", f.Socket, "n", p.Modules, "installed", slotList(p.Installed),
					"recommended", slotList(p.Recommended))
			}
		}
	case population.First:
		return boardSay(lang, "pop.first", "cpu", f.Socket, "channel", f.Channel, "slot", slot(0), "first", f.First)
	case population.CPU:
		return boardSay(lang, "pop.cpu", "cpu", f.Socket)
	case population.Balance:
		parts := make([]string, len(f.Counts))
		for i, n := range f.Counts {
			parts[i] = strconv.Itoa(n)
		}
		return boardSay(lang, "pop.balance", "counts", listText(lang, parts))
	case population.MixType:
		return boardSay(lang, "pop.mix_type", "types", listText(lang, f.Types))
	case population.MixRank:
		ra, rb := 0, 0
		if len(f.Ranks) == 2 {
			ra, rb = f.Ranks[0], f.Ranks[1]
		}
		return boardSay(lang, "pop.mix_rank", "cpu", f.Socket, "channel", f.Channel, "a", slot(0), "ra", ra, "b", slot(1), "rb", rb, "first", f.First)
	case population.MixSize:
		sa, sb := "?", "?"
		if len(f.Bytes) == 2 {
			sa, sb = sizeText(f.Bytes[0]), sizeText(f.Bytes[1])
		}
		return boardSay(lang, "pop.mix_size", "cpu", f.Socket, "channel", f.Channel, "a", slot(0), "sa", sa, "b", slot(1), "sb", sb, "first", f.First)
	case population.MixSpeed:
		parts := make([]string, len(f.Speeds))
		for i, v := range f.Speeds {
			parts[i] = fmt.Sprintf("%d MT/s", v)
		}
		return boardSay(lang, "pop.mix_speed", "speeds", listText(lang, parts))
	case population.MixPart:
		return boardSay(lang, "pop.mix_part", "cpu", f.Socket, "channel", f.Channel, "k", f.Parts)
	}
	return boardSay(lang, "pop.rule", "rule", f.Rule)
}

// citeOf names the page a line stands on: of the documents given, the one
// whose title names the product, else the first.
func citeOf(refs []population.Ref, product string) PopulationSource {
	if len(refs) == 0 {
		return PopulationSource{}
	}
	pick := refs[0]
	if p := strings.ToLower(strings.TrimSpace(product)); p != "" {
		for _, r := range refs {
			if src, ok := explorer.SourceByID(r.ID); ok && strings.Contains(strings.ToLower(src.Title), p) {
				pick = r
				break
			}
		}
	}
	out := PopulationSource{ID: pick.ID, Pages: pick.Pages}
	if src, ok := explorer.SourceByID(pick.ID); ok {
		out.Title, out.Number, out.Date, out.URL = src.Title, src.Number, src.Date, src.URL
	}
	parts := []string{}
	if out.Title != "" {
		parts = append(parts, out.Title)
	} else {
		parts = append(parts, out.ID)
	}
	if out.Number != "" {
		parts = append(parts, out.Number)
	}
	if pg := pagesText(out.Pages); pg != "" {
		parts = append(parts, pg)
	}
	out.Cite = strings.Join(parts, ", ")
	return out
}

// pagesText writes pages as the documents' language does: "p. 48",
// "pp. 41, 47", "pp. 41-48".
func pagesText(pages string) string {
	switch {
	case pages == "" || pages == "topic":
		return ""
	case strings.ContainsAny(pages, ",-"):
		return "pp. " + strings.ReplaceAll(pages, ",", ", ")
	}
	return "p. " + pages
}

// slotList joins slot names.
func slotList(names []string) string { return strings.Join(names, ", ") }

// listText joins words: "a, b and c" in English, "a, b, c" in Korean.
func listText(lang i18n.Lang, words []string) string {
	if len(words) < 2 || lang == i18n.KO {
		return strings.Join(words, ", ")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// countsText writes the numbers of modules a table lists: a run from 1 as
// a range, others as a list.
func countsText(lang i18n.Lang, counts []int) string {
	run := len(counts) > 2
	for i, n := range counts {
		if n != i+1 {
			run = false
		}
	}
	if run {
		if lang == i18n.KO {
			return fmt.Sprintf("1~%d", counts[len(counts)-1])
		}
		return fmt.Sprintf("1 to %d", counts[len(counts)-1])
	}
	parts := make([]string, len(counts))
	for i, n := range counts {
		parts[i] = strconv.Itoa(n)
	}
	return listText(lang, parts)
}

// WhyPopulation gives each card of a DIMM the line of the first population
// finding of the board that names its slot, for my why. The line changes
// no check, no confidence and no grade.
func (c *Case) WhyPopulation(ctx context.Context, h *engine.HostResult, cards []*Card) error {
	b, err := c.Board(ctx, h)
	if err != nil {
		return err
	}
	p := b.Population
	if p == nil {
		return nil
	}
	for _, cd := range cards {
		if cd.Scope == ScopeHost {
			continue
		}
		for _, f := range p.Findings {
			if slices.Contains(f.Slots, cd.Slot) {
				t := boardSay(c.Lang, "pop.why", "rule", f.Rule, "text", f.Text.Text)
				cd.Population = &t
				break
			}
		}
	}
	return nil
}
