package term

import (
	"io"

	"github.com/agenticode/my/internal/view"
)

// Examination draws everything about one DIMM: the finding, the branch the
// errors sit on, the address map of the bank with the most errors, and the
// timeline.
func Examination(w io.Writer, x *view.Examination, st Style) error {
	p := &page{st: st}
	st.dimmHead(p, x.Card)
	p.blank()
	st.card(p, x.Card, false)
	if x.Topology != nil && x.Topology.Total > 0 {
		p.blank()
		st.topology(p, x.Topology)
	}
	if x.AddressMap != nil && x.Card.CE+x.Card.UE > 0 {
		p.blank()
		st.addressMap(p, x.AddressMap)
	}
	if x.Timeline != nil && x.Card.CE+x.Card.UE > 0 {
		p.blank()
		p.add(" " + st.say("tl.title"))
		st.timeline(p, x.Timeline)
	}
	return p.writeTo(w)
}
