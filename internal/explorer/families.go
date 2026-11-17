package explorer

// This file is MemorY's own: the board families (the fix board-families).

// phantoms are the names of the records the manual of the board has no
// slot for.
func (p *pass) phantoms() map[string]bool { return nil }

// unnamed gives the empty slots without a name the labels of the manual.
func (p *pass) unnamed() {
	for _, i := range p.live {
		if p.toks[i].unnamed() {
			p.facts.Unnamed++
		}
	}
}

// applyFamily places the slots of a board from the table of its family and
// reports whether it did.
func (p *pass) applyFamily() bool { return false }
