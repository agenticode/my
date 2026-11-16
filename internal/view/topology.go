package view

import (
	"context"
	"fmt"
	"sort"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// Topology shows which branch of a DIMM its errors sit on. The memory of a
// DIMM is a tree: ranks hold devices, devices hold banks, banks hold rows.
// Every level splits all errors of the DIMM, and the segments of a level
// lie below the segment they belong to in the level above, so the widest
// path from top to bottom is where the errors are.
type Topology struct {
	// Total is the number of errors of the DIMM.
	Total int64 `json:"total"`
	// Levels are rank, device, bank and row, in that order.
	Levels []Level `json:"levels"`
	// Window is the number of boots of the window of the host (engine
	// recurrence), and Boots those of them the DIMM had errors in.
	Window int `json:"window,omitzero"`
	Boots  int `json:"boots,omitzero"`
}

// Level names of a Topology.
const (
	LevelRank   = "rank"
	LevelDevice = "device"
	LevelBank   = "bank"
	LevelRow    = "row"
)

// Level is one level of the tree.
type Level struct {
	Name string `json:"name"`
	// Known is false when no record of the DIMM reports this level.
	Known bool `json:"known"`
	// Segments are in drawing order, left to right.
	Segments []Segment `json:"segments"`
	// Boots is the number of boots of the window the largest segment had
	// errors in.
	Boots int `json:"boots,omitzero"`
}

// Segment is one branch at one level.
type Segment struct {
	// Label names the branch: "rank0", "dev7", "bg0 bank3", "0x1a2f". It is
	// "?" where the records do not report the level, and empty for a
	// segment that stands for several small branches.
	Label string `json:"label"`
	// Parent is the index of the segment this one belongs to in the level
	// above, or -1 at the top level.
	Parent int   `json:"parent"`
	Errors int64 `json:"errors"`
	// Share is Errors divided by the errors of the DIMM.
	Share float64 `json:"share"`
	// Unknown is set where the records do not report the level.
	Unknown bool `json:"unknown,omitzero"`
	// Others is set on a segment that stands for that many branches too
	// small to list.
	Others int `json:"others,omitzero"`
	// Boots is the number of boots of the window the branch had errors in.
	Boots int `json:"boots,omitzero"`
}

// maxBranches is how many branches of one parent a level lists before the
// rest is folded into one segment.
const maxBranches = 6

type topoNode struct {
	label    string
	unknown  bool
	errors   int64
	order    int64 // sort key among equal counts: the number of the branch
	children map[int64]*topoNode
	boots    map[int]bool // boots of the window with errors here
}

func (n *topoNode) boot(b int) {
	if b < 0 {
		return
	}
	if n.boots == nil {
		n.boots = map[int]bool{}
	}
	n.boots[b] = true
}

func (n *topoNode) child(key int64, label string) *topoNode {
	if n.children == nil {
		n.children = map[int64]*topoNode{}
	}
	c := n.children[key]
	if c == nil {
		c = &topoNode{label: label, unknown: key < 0, order: key}
		n.children[key] = c
	}
	return c
}

func optKey(o model.Opt[int]) int64 {
	if !o.Valid {
		return -1
	}
	return int64(o.V)
}

// bankLabel names a bank the way the findings do.
func bankLabel(bg, bank model.Opt[int]) string {
	switch {
	case bg.Valid && bank.Valid:
		return fmt.Sprintf("bg%d bank%d", bg.V, bank.V)
	case bank.Valid:
		return fmt.Sprintf("bank%d", bank.V)
	case bg.Valid:
		return fmt.Sprintf("bg%d", bg.V)
	}
	return "?"
}

// Topology builds the topology bars of a DIMM from its records.
func (c *Case) Topology(ctx context.Context, d *engine.DIMMResult) (*Topology, error) {
	root := &topoNode{}
	// The boots of the window, by boot ID.
	window := map[string]int{}
	if h := c.Result.Host(d.Host); h != nil && h.Recurrence != nil {
		for _, b := range h.Recurrence.Boots {
			window[b.BootID] = b.Index
		}
	}
	for e, err := range c.store.Events(ctx, store.EventFilter{DIMMUID: d.UID}) {
		if err != nil {
			return nil, err
		}
		n := int64(e.Errors())
		if n == 0 {
			continue
		}
		bi := -1
		if i, ok := window[e.BootID]; ok && e.Kind != model.KindCounter {
			bi = i
		}
		root.errors += n
		root.boot(bi)
		label := "?"
		if e.Rank.Valid {
			label = fmt.Sprintf("rank%d", e.Rank.V)
		}
		node := root.child(optKey(e.Rank), label)
		node.errors += n
		node.boot(bi)

		label = "?"
		if e.DeviceId.Valid {
			label = fmt.Sprintf("dev%d", e.DeviceId.V)
		}
		node = node.child(optKey(e.DeviceId), label)
		node.errors += n
		node.boot(bi)

		// A bank is known when the bank group or the bank address is.
		key := int64(-1)
		if e.BankGroup.Valid || e.Bank.Valid {
			key = (optKey(e.BankGroup)+1)<<16 | (optKey(e.Bank) + 1)
		}
		node = node.child(key, bankLabel(e.BankGroup, e.Bank))
		node.errors += n
		node.boot(bi)

		label = "?"
		if e.Row.Valid {
			label = fmt.Sprintf("%#x", e.Row.V)
		}
		node = node.child(optKey(e.Row), label)
		node.errors += n
		node.boot(bi)
	}

	t := &Topology{Total: root.errors, Levels: []Level{{Name: LevelRank}, {Name: LevelDevice}, {Name: LevelBank}, {Name: LevelRow}},
		Window: len(window), Boots: len(root.boots)}
	for i := range t.Levels {
		t.Levels[i].Segments = []Segment{}
	}
	if root.errors == 0 {
		return t, nil
	}
	// Walk the tree level by level. A parent that is itself a fold of
	// small branches has nothing to split further: it passes down as one
	// segment.
	type open struct {
		node   *topoNode // nil for a fold
		index  int       // index of the segment in the level above
		errs   int64
		others int // number of branches a fold stands for
		boots  int
	}
	parents := []open{{node: root, index: -1, errs: root.errors}}
	for li := range t.Levels {
		lv := &t.Levels[li]
		var next []open
		for _, p := range parents {
			if p.node == nil {
				lv.Segments = append(lv.Segments, Segment{Parent: p.index, Errors: p.errs, Share: share(p.errs, root.errors), Others: p.others, Boots: p.boots})
				next = append(next, open{index: len(lv.Segments) - 1, errs: p.errs, others: p.others, boots: p.boots})
				continue
			}
			kids := make([]*topoNode, 0, len(p.node.children))
			for _, k := range p.node.children {
				kids = append(kids, k)
			}
			sort.Slice(kids, func(i, j int) bool {
				a, b := kids[i], kids[j]
				if a.errors != b.errors {
					return a.errors > b.errors
				}
				if a.unknown != b.unknown {
					return !a.unknown
				}
				return a.order < b.order
			})
			var rest int64
			folded := 0
			restBoots := map[int]bool{}
			for i, k := range kids {
				if i >= maxBranches && len(kids) > maxBranches+1 {
					rest += k.errors
					folded++
					for b := range k.boots {
						restBoots[b] = true
					}
					continue
				}
				lv.Segments = append(lv.Segments, Segment{Label: k.label, Parent: p.index, Errors: k.errors, Share: share(k.errors, root.errors), Unknown: k.unknown,
					Boots: len(k.boots)})
				lv.Known = lv.Known || !k.unknown
				next = append(next, open{node: k, index: len(lv.Segments) - 1, errs: k.errors})
			}
			if folded > 0 {
				lv.Segments = append(lv.Segments, Segment{Parent: p.index, Errors: rest, Share: share(rest, root.errors), Others: folded, Boots: len(restBoots)})
				lv.Known = true
				next = append(next, open{index: len(lv.Segments) - 1, errs: rest, others: folded, boots: len(restBoots)})
			}
		}
		parents = next
		// The boots of the largest branch of the level.
		var top int64 = -1
		for _, s := range lv.Segments {
			if s.Errors > top {
				top, lv.Boots = s.Errors, s.Boots
			}
		}
	}
	return t, nil
}

func share(n, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total)
}
