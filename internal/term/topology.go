package term

import (
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/agenticode/my/internal/view"
)

// percent writes a share as a whole percentage.
func percent(share float64) string {
	switch {
	case share <= 0:
		return "0%"
	case share < 0.005:
		return "<1%"
	}
	return fmt.Sprintf("%.0f%%", math.Min(share*100, 100))
}

// topoSeg is one drawn segment: a branch, or several small ones folded.
type topoSeg struct {
	start, end int // columns, the border included
	label      string
	share      float64
	dim        bool
	parent     int
}

// topoLayout places the segments of a level on a bar of the given width.
// A segment starts where the shares before it end, so a branch always lies
// below the branch it belongs to. Segments too narrow to draw are folded
// with their neighbours.
func (st Style) topoLayout(lv *view.Level, width int) []topoSeg {
	var out []topoSeg
	var cum float64
	var pending *topoSeg
	folded := 0
	flush := func() {
		if pending == nil {
			return
		}
		switch w := pending.end - pending.start; {
		case w >= 2:
			if folded > 1 {
				pending.label, pending.dim = fmt.Sprintf("+%d", folded), true
			}
			out = append(out, *pending)
		case w == 1 && len(out) > 0:
			// One column cannot hold a border and a mark: the neighbour
			// takes it.
			out[len(out)-1].end = pending.end
		case w == 1:
			out = append(out, *pending) // widened by the next segment
		}
		pending, folded = nil, 0
	}
	for i, s := range lv.Segments {
		a := int(math.Round(cum * float64(width)))
		cum += s.Share
		b := int(math.Round(cum * float64(width)))
		if i == len(lv.Segments)-1 {
			b = width
		}
		label := s.Label
		if s.Others > 0 {
			label = fmt.Sprintf("+%d", s.Others)
		}
		seg := topoSeg{start: a, end: b, label: label, share: s.Share, dim: s.Unknown || s.Others > 0, parent: s.Parent}
		if s.Others > 0 && pending != nil && pending.parent == s.Parent {
			// Branches too narrow to draw join the fold of their siblings.
			seg.start, seg.share = pending.start, seg.share+pending.share
			seg.label = fmt.Sprintf("+%d", s.Others+folded)
			if n := len(out); n == 0 || out[n-1].parent != s.Parent {
				// Nothing of this parent is drawn by itself: say how many
				// branches the segment stands for.
				seg.label = st.say("topo.fold", "n", s.Others+folded, "level", lv.Name)
			}
			pending, folded = nil, 0
		}
		if seg.end-seg.start >= 2 {
			flush()
			if n := len(out); n > 0 && out[n-1].end-out[n-1].start == 1 {
				// A lone column before this one could not be drawn.
				seg.start = out[n-1].start
				out = out[:n-1]
			}
			out = append(out, seg)
			continue
		}
		if pending == nil {
			pending = &seg
			folded = 1
			continue
		}
		pending.end = b
		pending.share += s.Share
		folded++
	}
	flush()
	if n := len(out); n > 0 {
		out[n-1].end = width
	}
	return out
}

// topoLine draws the segments of one level.
func (st Style) topoLine(segs []topoSeg, pct bool) string {
	g := st.g()
	var b strings.Builder
	for _, s := range segs {
		inner := s.end - s.start - 1
		fill := g.bar
		if s.dim {
			fill = g.barDim
		}
		label, share := s.label, ""
		if pct {
			share = percent(s.share)
		}
		lw, pw := Width(label), Width(share)
		var body string
		switch {
		case share != "" && lw+pw+3 <= inner:
			body = label + " " + st.paint(dimIf(s.dim), strings.Repeat(fill, inner-lw-pw-2)) + " " + share
		case lw+2 <= inner:
			body = label + " " + st.paint(dimIf(s.dim), strings.Repeat(fill, inner-lw-1))
		case lw <= inner:
			body = label + strings.Repeat(" ", inner-lw)
		default:
			body = st.paint(dimIf(s.dim), strings.Repeat(fill, inner))
		}
		b.WriteString(g.sep + body)
	}
	b.WriteString(g.sep)
	return b.String()
}

func dimIf(dim bool) string {
	if dim {
		return cDim
	}
	return ""
}

// topology draws the topology bars of a DIMM.
func (st Style) topology(p *page, t *view.Topology) {
	if t.Total == 0 {
		return
	}
	p.add(" " + st.say("topo.title"))
	lw := 0
	for _, lv := range t.Levels {
		lw = max(lw, Width(lv.Name))
	}
	width := st.width() - 1 - lw - 2 - 1
	for i := range t.Levels {
		lv := &t.Levels[i]
		var line string
		if !lv.Known {
			// Nothing in the records says which branch: one segment says so.
			line = st.topoLine([]topoSeg{{start: 0, end: width, label: st.say("topo.unreported"), dim: true}}, false)
		} else {
			line = st.topoLine(st.topoLayout(lv, width), true)
		}
		p.add(" " + st.dim(padRight(lv.Name, lw)) + "  " + line)
	}
}

// Topology draws the topology bars of a DIMM on their own.
func Topology(w io.Writer, t *view.Topology, st Style) error {
	p := &page{st: st}
	st.topology(p, t)
	return p.writeTo(w)
}
