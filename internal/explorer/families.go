package explorer

// This file is MemorY's own: the board families (the fix board-families).
// A family is a row of families.tsv, written from the public documents of
// the board's maker that sources.tsv names.

import (
	_ "embed"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed families.tsv
var familiesTSV string

//go:embed sources.tsv
var sourcesTSV string

// family is one row of families.tsv.
type family struct {
	id, maker string
	re        *regexp.Regexp
	forms     map[string]bool
	when      map[string][]string
	socket    string
	kind      string
	arg       string
	channels  []string
	rawMap    string
	maps      map[int]map[familyKey]familyPlace // by socket, -1 for the table of every socket
	source    []string
	sockmap   map[word]int
}

// familyKey is a slot number or a slot label of a family table.
type familyKey struct {
	n     int
	label string
}

// familyPlace is the place a table gives a slot.
type familyPlace struct {
	socket  int // -1 when the row does not say
	channel string
	pos     int
}

// Source is a document a board family is written from.
type Source struct {
	ID, Maker, Title, Number, Date, URL, Pages string
}

var (
	families    []family
	familyByID  map[string]*family
	sources     map[string]Source
	sourceOrder []string
)

func init() {
	var err error
	if families, err = parseFamilies(familiesTSV); err != nil {
		panic(err)
	}
	familyByID = map[string]*family{}
	for i := range families {
		familyByID[families[i].id] = &families[i]
	}
	sources = map[string]Source{}
	defer func() {
		if errs := checkFamilies(families, sources); len(errs) > 0 {
			panic(fmt.Sprint("families.tsv: ", errs))
		}
	}()
	for _, line := range strings.Split(strings.TrimRight(sourcesTSV, "\n"), "\n") {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "id\t") {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 7 {
			panic("sources.tsv: " + line)
		}
		sources[p[0]] = Source{p[0], p[1], p[2], p[3], p[4], p[5], p[6]}
		sourceOrder = append(sourceOrder, p[0])
	}
}

// asciiLower is s with its ASCII capitals in small letters.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// parseFamilies reads the rows of families.tsv.
func parseFamilies(text string) ([]family, error) {
	var out []family
	for n, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "id\t") || line == "" {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 11 {
			return nil, fmt.Errorf("families.tsv line %d: %d fields", n+1, len(p))
		}
		// The match is over ASCII letters of any case: both sides are
		// written in small letters.
		re, err := regexp.Compile(asciiLower(p[2]))
		if err != nil {
			return nil, fmt.Errorf("families.tsv %s: %v", p[0], err)
		}
		f := family{id: p[0], maker: p[1], re: re, forms: map[string]bool{}, when: map[string][]string{}, socket: p[5], kind: p[6], arg: p[7], rawMap: p[9]}
		for _, x := range strings.Split(p[3], ",") {
			if x != "" {
				f.forms[x] = true
			}
		}
		for _, c := range strings.Split(p[4], ";") {
			if k, v, ok := strings.Cut(c, "="); ok {
				f.when[k] = strings.Split(v, "|")
			}
		}
		for _, c := range strings.Split(p[8], ",") {
			if c != "" {
				f.channels = append(f.channels, c)
			}
		}
		for _, s := range strings.Split(p[10], ";") {
			if s != "" {
				f.source = append(f.source, s)
			}
		}
		switch f.kind {
		case "table", "global", "cards":
			if f.maps, err = parseFamilyMap(p[9], f.kind == "global"); err != nil {
				return nil, fmt.Errorf("families.tsv %s: %v", f.id, err)
			}
		}
		if rest, ok := strings.CutPrefix(f.socket, "map:"); ok {
			f.sockmap = map[word]int{}
			for _, e := range strings.Split(rest, ",") {
				k, v, _ := strings.Cut(e, "=")
				i := strings.IndexAny(k, "0123456789")
				n, err1 := strconv.Atoi(k[i:])
				s, err2 := strconv.Atoi(v)
				if i <= 0 || err1 != nil || err2 != nil {
					return nil, fmt.Errorf("families.tsv %s: socket map %q", f.id, e)
				}
				f.sockmap[word{strings.ToUpper(k[:i]), n, true}] = s
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// parseFamilyMap reads the map of a table: "n=channel.position" per slot,
// "s2:" before the table of a second processor, "n=socket.channel.position"
// for a global table.
func parseFamilyMap(text string, global bool) (map[int]map[familyKey]familyPlace, error) {
	out := map[int]map[familyKey]familyPlace{}
	for _, part := range strings.Split(text, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		sock := -1
		if strings.HasPrefix(part, "s") {
			if h, rest, ok := strings.Cut(part, ":"); ok {
				if n, err := strconv.Atoi(h[1:]); err == nil {
					sock, part = n-1, strings.TrimSpace(rest)
				}
			}
		}
		t := out[sock]
		if t == nil {
			t = map[familyKey]familyPlace{}
			out[sock] = t
		}
		for _, e := range strings.Fields(part) {
			k, v, ok := strings.Cut(e, "=")
			if !ok {
				return nil, fmt.Errorf("entry %q", e)
			}
			key := familyKey{label: strings.ToUpper(k)}
			if n, err := strconv.Atoi(k); err == nil {
				key = familyKey{n: n}
			}
			// The position is the last field; a channel name may hold a dot.
			i := strings.LastIndexByte(v, '.')
			if i < 0 {
				return nil, fmt.Errorf("entry %q", e)
			}
			pos, err := strconv.Atoi(v[i+1:])
			if err != nil {
				return nil, fmt.Errorf("entry %q", e)
			}
			pl := familyPlace{socket: -1, channel: v[:i], pos: pos}
			if global {
				s, ch, ok := strings.Cut(pl.channel, ".")
				n, err := strconv.Atoi(s)
				if !ok || err != nil {
					return nil, fmt.Errorf("entry %q", e)
				}
				pl.socket, pl.channel = n, ch
			}
			t[key] = pl
		}
	}
	return out, nil
}

// familyKinds are the kinds of a row.
var familyKinds = map[string]bool{"table": true, "global": true, "cards": true, "names": true, "hub": true,
	"labels": true, "phantoms": true, "alias": true, "aside": true}

// asideWithoutDocument are the reasons of an aside row that cite no
// document: none was found, or the board is not named.
var asideWithoutDocument = map[string]bool{"no-document": true, "board-unknown": true}

// checkFamilies lists what is wrong with a family table: an unknown kind,
// an alias to no row, a source missing from the documents, a row without a
// source that needs one, an id twice.
func checkFamilies(rows []family, docs map[string]Source) []error {
	var out []error
	ids := map[string]bool{}
	for _, f := range rows {
		if ids[f.id] {
			out = append(out, fmt.Errorf("%s: the id stands twice", f.id))
		}
		ids[f.id] = true
	}
	for _, f := range rows {
		if !familyKinds[f.kind] {
			out = append(out, fmt.Errorf("%s: kind %q", f.id, f.kind))
		}
		if f.kind == "alias" && !ids[f.arg] {
			out = append(out, fmt.Errorf("%s: alias to %q, no row", f.id, f.arg))
		}
		for _, s := range f.source {
			if _, ok := docs[s]; !ok {
				out = append(out, fmt.Errorf("%s: source %q is not in sources.tsv", f.id, s))
			}
		}
		if len(f.source) == 0 && f.kind != "alias" && !(f.kind == "aside" && asideWithoutDocument[f.arg]) {
			out = append(out, fmt.Errorf("%s: no source", f.id))
		}
	}
	return out
}

// Families are the ids of the rows of families.tsv, in table order.
func Families() []string {
	out := make([]string, len(families))
	for i, f := range families {
		out[i] = f.id
	}
	return out
}

// FamilySources are the documents of a board family.
func FamilySources(id string) []Source {
	f := familyByID[id]
	if f == nil {
		return nil
	}
	var out []Source
	for _, s := range f.source {
		out = append(out, sources[s])
	}
	return out
}

// Sources are the documents of sources.tsv, in its order.
func Sources() []Source {
	out := make([]Source, len(sourceOrder))
	for i, id := range sourceOrder {
		out[i] = sources[id]
	}
	return out
}

// key is the string the family rows match: system product and board.
func (p *pass) key() string { return asciiLower(p.hints.product + "|" + p.hints.board) }

// cond tells whether a condition of a row holds.
func (f *family) cond(name string, v int) bool {
	vals, ok := f.when[name]
	if !ok {
		return true
	}
	for _, x := range vals {
		if x == strconv.Itoa(v) {
			return true
		}
	}
	return false
}

// matches tells whether a row speaks of this board.
func (p *pass) matches(f *family) bool {
	if f.maker != "*" && f.maker != p.maker {
		return false
	}
	if !f.re.MatchString(p.key()) {
		return false
	}
	if len(f.forms) > 0 {
		named := 0
		for _, i := range p.live {
			if lf := p.toks[i].lform; lf != "" {
				named++
				if !f.forms[lf] {
					return false
				}
			}
		}
		if named == 0 {
			return false
		}
	}
	if !f.cond("S", p.S) || !f.cond("slots", len(p.live)) {
		return false
	}
	if vals, ok := f.when["cpu"]; ok {
		hit := false
		for _, x := range vals {
			if x == p.proc.Family {
				hit = true
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// phantoms are the names of the records the manual of the board has no
// slot for (rows of kind phantoms).
func (p *pass) phantoms() map[string]bool {
	if !stage(FixBoardFamilies) {
		return nil
	}
	var out map[string]bool
	for i := range families {
		f := &families[i]
		if f.kind != "phantoms" || f.maker != "*" && f.maker != p.maker || !f.re.MatchString(p.key()) {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		p.facts.PhantomRow = f.id
		for _, n := range strings.Split(f.rawMap, ",") {
			if n = strings.TrimSpace(n); n != "" {
				out[squash(n)] = true
			}
		}
	}
	return out
}

// labels are the slot labels of the manual of the board, when a row of
// kind labels lists them.
func (p *pass) labels() []string {
	for i := range families {
		f := &families[i]
		if f.kind != "labels" || f.maker != "*" && f.maker != p.maker || !f.re.MatchString(p.key()) {
			continue
		}
		if !f.cond("S", p.S) || !f.cond("slots", len(p.live)) {
			continue
		}
		p.facts.Labels = f.id
		return strings.Fields(f.rawMap)
	}
	return nil
}

// unnamed counts the empty slots without a name and, when a family row
// lists the labels of the manual, gives each the label no named slot
// carries: they are then read as names, and never count as placed in full.
func (p *pass) unnamed() {
	var blank []int
	for _, i := range p.live {
		if p.toks[i].unnamed() {
			blank = append(blank, i)
		}
	}
	p.facts.Unnamed = len(blank)
	if len(blank) == 0 || !stage(FixBoardFamilies) {
		return
	}
	labels := p.labels()
	if labels == nil {
		return
	}
	for _, i := range blank {
		if p.dimms[i].Populated {
			return
		}
	}
	isBlank := map[int]bool{}
	for _, i := range blank {
		isBlank[i] = true
	}
	have := map[string]bool{}
	for _, i := range p.live {
		if !isBlank[i] {
			have[squash(p.dimms[i].passLocator)] = true
		}
	}
	var missing []string
	for _, l := range labels {
		if !have[squash(l)] {
			missing = append(missing, l)
		}
	}
	if len(missing) != len(blank) || len(have)+len(missing) != len(labels) {
		return
	}
	for j, i := range blank {
		p.toks[i] = readNames(missing[j], "")
		p.slots[i].label = missing[j]
	}
	p.facts.Labelled = len(blank)
}

// applyFamily places the slots of a board from the table of its family and
// reports whether it did; a row of kind names or hub sets what it knows of
// the board and lets the names place the slots.
func (p *pass) applyFamily() bool {
	if !stage(FixBoardFamilies) {
		return false
	}
	for i := range families {
		f := &families[i]
		if f.kind == "labels" || f.kind == "phantoms" || !p.matches(f) {
			continue
		}
		row := f
		if f.kind == "alias" {
			// A board known by its part number takes the rule of the
			// family named dumps of that board carry.
			if row = familyByID[f.arg]; row == nil {
				continue
			}
			p.facts.Alias = f.id
		}
		if done, ok := p.applyRow(row); ok {
			return done
		}
	}
	return false
}

// number is the slot number of a name for a family table.
func number(t tokens) (int, bool) {
	switch {
	case t.num.ok:
		return t.num.v, true
	case t.ln:
		return t.lnN, true
	case t.bnum.ok:
		return t.bnum.v, true
	case t.dl:
		return t.dlN, true
	}
	return 0, false
}

// rowSockets is the socket of every slot under the rule of a row.
func (p *pass) rowSockets(f *family) (map[int]int, bool) {
	out := map[int]int{}
	switch f.socket {
	case "hub":
		for _, i := range p.live {
			out[i] = 0
		}
		return out, true
	case "one":
		for _, i := range p.live {
			out[i] = 0
		}
		return out, true
	case "letter":
		for _, i := range p.live {
			t := p.toks[i]
			L := ""
			switch {
			case t.ln:
				L = t.lnL
			case t.dl:
				L = t.dlL
			}
			if L == "" {
				if p.S == 1 {
					out[i] = 0 // one processor: a name without its letter is on it
					continue
				}
				return nil, false
			}
			out[i] = int(L[0]) - 'A'
		}
		return out, true
	}
	ws := map[int]word{}
	zero := map[string]bool{}
	for _, i := range p.live {
		t := p.toks[i]
		var w word
		if f.socket == "cpu" {
			w = t.cpu
		} else {
			switch {
			case t.bcpu.ok:
				w = t.bcpu
			case t.node.ok:
				w = word{"NODE", t.node.v, true}
			}
		}
		if !w.ok {
			return nil, false
		}
		ws[i] = w
		if w.n == 0 {
			zero[w.w] = true
		}
	}
	for i, w := range ws {
		switch w.w {
		case "BP", "DIGIT":
			out[i] = w.n
		case "PROC":
			out[i] = w.n - 1
		default:
			if zero[w.w] {
				out[i] = w.n
			} else {
				out[i] = w.n - 1
			}
		}
	}
	return out, true
}

// applyRow applies one row. ok is false when the row does not fit the
// board and the next row is to be tried; done says whether the row placed
// the slots (true) or left them to the names (false).
func (p *pass) applyRow(f *family) (done, ok bool) {
	tag := "family:" + f.id
	if f.kind == "names" || f.kind == "hub" {
		if f.sockmap != nil {
			seen := map[word]bool{}
			for _, i := range p.live {
				seen[p.toks[i].cpu] = true
			}
			for w := range f.sockmap {
				if !seen[w] {
					return false, false // the board does not carry the words the row is about
				}
			}
			p.sockmap = f.sockmap
		}
		if c, per, found := strings.Cut(f.arg, ","); found {
			nc, _ := strconv.Atoi(c)
			np, _ := strconv.Atoi(per)
			p.facts.Channels, p.facts.PerChannel, p.facts.ChannelsBy = nc, np, tag
			p.proc.Channels, p.proc.PerChannel = nc, np
		}
		if f.kind == "hub" {
			p.hubFamily()
		}
		p.facts.Family = f.id
		return false, true
	}
	if f.socket == "hub" && !p.hubFamily() {
		return false, false
	}
	if f.kind == "aside" {
		reason := f.arg
		if reason == "" {
			reason = "family"
		}
		for _, i := range p.live {
			p.slots[i].why = reason
		}
		p.facts.Family, p.facts.Aside = f.id, reason
		return true, true
	}
	var named []int
	for _, i := range p.live {
		if p.toks[i].lform != "" {
			named = append(named, i)
		}
	}
	type at struct {
		socket  int
		channel string
		pos     int
		card    int
	}
	place := map[int]at{}
	switch f.kind {
	case "cards":
		k, _ := strconv.Atoi(f.arg)
		tab := f.maps[-1]
		if k <= 0 {
			return false, false
		}
		names := map[string]bool{}
		for _, i := range named {
			t := p.toks[i]
			if !t.card.ok || !t.num.ok {
				return false, false
			}
			e, found := tab[familyKey{n: t.num.v}]
			if !found {
				return false, false
			}
			place[i] = at{(t.card.v - 1) / k, fmt.Sprintf("%d.%s", t.card.v, e.channel), e.pos, t.card.v}
		}
		for _, e := range tab {
			names[e.channel] = true
		}
		for i, a := range place {
			s := &p.slots[i]
			s.placed, s.socket, s.key, s.hasKey, s.chname, s.poskey, s.by, s.chBy = true, some(a.socket), chanKey{mc: a.card, s: a.channel}, true, a.channel, a.pos, tag, tag
		}
		for _, i := range p.live {
			if _, in := place[i]; !in {
				p.slots[i].why = "no-name"
			}
		}
		p.facts.Family = f.id
		p.facts.Channels, p.facts.ChannelsBy = k*len(names), tag
		return true, true
	case "global":
		tab := f.maps[-1]
		for _, i := range named {
			n, found := number(p.toks[i])
			if !found {
				return false, false
			}
			e, in := tab[familyKey{n: n}]
			if !in {
				return false, false
			}
			place[i] = at{e.socket, e.channel, e.pos, 0}
		}
	case "table":
		socks, found := p.rowSockets(f)
		if !found {
			return false, false
		}
		per := map[int][]int{}
		var order []int
		for _, i := range named {
			if _, in := per[socks[i]]; !in {
				order = append(order, socks[i])
			}
			per[socks[i]] = append(per[socks[i]], i)
		}
		for _, s := range order {
			if !f.cond("per", len(per[s])) {
				return false, false
			}
		}
		for _, s := range order {
			idx := per[s]
			nums := map[int]int{}
			seen := map[int]bool{}
			for _, i := range idx {
				n, found := number(p.toks[i])
				if !found || seen[n] {
					return false, false
				}
				seen[n], nums[i] = true, n
			}
			tab := f.maps[s]
			if tab == nil {
				tab = f.maps[-1]
			}
			if tab == nil {
				return false, false
			}
			for _, i := range idx {
				e, in := tab[familyKey{n: nums[i]}]
				if !in {
					return false, false
				}
				place[i] = at{s, e.channel, e.pos, 0}
			}
		}
	default:
		return false, false
	}
	index := map[string]int{}
	for j, c := range f.channels {
		index[c] = j
	}
	for i, a := range place {
		s := &p.slots[i]
		key := chanKey{s: a.channel}
		if j, in := index[a.channel]; in {
			key.s = fmt.Sprintf("%03d", j)
		}
		name := a.channel
		if strings.HasPrefix(name, "?") {
			name = "?" // a group the document shows without a name
		}
		s.placed, s.socket, s.key, s.hasKey, s.chname, s.poskey, s.by, s.chBy = true, some(a.socket), key, true, name, a.pos, tag, tag
		if f.socket == "hub" {
			s.hub = true
		}
	}
	for _, i := range p.live {
		if _, in := place[i]; !in {
			p.slots[i].why = "no-name"
		}
	}
	p.facts.Family = f.id
	if len(f.channels) > 0 {
		p.facts.Channels, p.facts.ChannelsBy = len(f.channels), tag
	}
	if f.socket == "hub" {
		// A platform older than DDR3: the table is the document's count of
		// slots per channel.
		n := map[[2]string]int{}
		most := 0
		for _, a := range place {
			k := [2]string{strconv.Itoa(a.socket), a.channel}
			n[k]++
			most = max(most, n[k])
		}
		p.facts.PerChannel = most
	}
	return true, true
}

// hubFamily makes the board one of memory on the chipset, when the stage
// that reads such boards is in force.
func (p *pass) hubFamily() bool { return false }

// familiesOf lists the ids of the rows whose maker and match fit a board,
// for tests.
func familiesOf(product, board, maker string) []string {
	k := asciiLower(product + "|" + board)
	var out []string
	for _, f := range families {
		if (f.maker == "*" || f.maker == maker) && f.re.MatchString(k) {
			out = append(out, f.id)
		}
	}
	sort.Strings(out)
	return out
}
