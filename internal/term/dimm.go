package term

import (
	"io"

	"github.com/agenticode/my/internal/view"
)

// DIMMPlaces is the number of places the boots block of an examination
// lists before the rest is summed in one row.
const DIMMPlaces = 5

// Examination draws everything about one DIMM: the finding, the branch the
// errors sit on, the address map of the bank with the most errors, and the
// timeline.
//
// In short the finding is the decision and what bears on it; with
// st.Detail it is all the finding rests on, as "my why --detail" shows it.
func Examination(w io.Writer, x *view.Examination, st Style) error {
	p := &page{st: st}
	if st.Detail {
		st.dimmHead(p, x.Card)
		p.blank()
		st.card(p, x.Card, true)
	} else {
		st.brief(p, x.Card)
	}
	brief := !st.Detail
	switch {
	case x.Topology == nil || x.Topology.Total == 0:
	case !brief:
		p.blank()
		st.topology(p, x.Topology)
	case drawsTopology(x.Topology):
		p.blank()
		st.topologyBrief(p, x.Topology)
	}
	if x.AddressMap != nil && x.Card.CE+x.Card.UE > 0 {
		p.blank()
		st.addressMap(p, x.AddressMap, brief)
	}
	if x.Boots != nil && len(x.Boots.Rows) > 0 {
		p.blank()
		p.add(" " + st.say("boots.title"))
		st.placesGrid(p, x.Boots, 0, DIMMPlaces)
	}
	if tl := x.Timeline; tl != nil && x.Card.CE+x.Card.UE > 0 {
		// In brief, a timeline of one day is one bar: the boots of that
		// day say more, and only they are drawn.
		oneDay := brief && tl.Days <= 1
		switch {
		case oneDay && x.Boots != nil && len(x.Boots.Rows) > 0:
		case oneDay && tl.Boots != nil && len(tl.Boots.Boots) > 0:
			p.blank()
			p.add(" " + st.say("tl.boots"))
			st.bootsGrid(p, tl.Boots, 0)
		case oneDay && tl.Days == 1:
			// One day: its count and its events, without a chart.
			p.blank()
			st.dayEvents(p, tl)
		default:
			p.blank()
			p.add(" " + st.say("tl.title"))
			st.timeline(p, tl, brief)
		}
	}
	return p.writeTo(w)
}
