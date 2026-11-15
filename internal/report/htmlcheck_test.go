package report

import (
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The checks of this file are the validity test of a report. A report is
// written as HTML that is also well-formed XML, so the whole document can
// be read with the XML parser of the standard library; what is read is
// then held against the content model of HTML for the elements a report
// uses, and against the rules of the report itself: nothing is loaded
// from elsewhere, every link leads somewhere, every identifier is unique.

// elem is an element of a parsed report.
type elem struct {
	tag    string
	attrs  map[string]string
	kids   []*elem
	text   string // of a text node (tag is empty)
	parent *elem
	svg    bool // the element lies within an svg element, or is one
}

// parseHTML reads a report with the XML parser.
func parseHTML(doc []byte) (*elem, error) {
	s := string(doc)
	if !strings.HasPrefix(s, "<!DOCTYPE html>\n<html") {
		return nil, fmt.Errorf("the document does not start with the HTML doctype")
	}
	dec := xml.NewDecoder(strings.NewReader(s))
	dec.Strict = true
	root := &elem{tag: "#root", attrs: map[string]string{}}
	cur := root
	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("not well-formed: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			e := &elem{tag: t.Name.Local, attrs: map[string]string{}, parent: cur, svg: cur.svg || t.Name.Local == "svg"}
			if t.Name.Space != "" {
				return nil, fmt.Errorf("<%s> carries a namespace: %s", t.Name.Local, t.Name.Space)
			}
			for _, a := range t.Attr {
				name := a.Name.Local
				if a.Name.Space != "" {
					name = a.Name.Space + ":" + name
				}
				if _, dup := e.attrs[name]; dup {
					return nil, fmt.Errorf("<%s> has the attribute %s twice", e.tag, name)
				}
				e.attrs[name] = a.Value
			}
			cur.kids = append(cur.kids, e)
			cur = e
		case xml.EndElement:
			cur = cur.parent
		case xml.CharData:
			if len(t) > 0 {
				cur.kids = append(cur.kids, &elem{text: string(t), parent: cur, svg: cur.svg})
			}
		case xml.Comment:
			return nil, fmt.Errorf("the document holds a comment")
		case xml.ProcInst:
			return nil, fmt.Errorf("the document holds a processing instruction")
		}
	}
	if len(root.kids) == 0 {
		return nil, fmt.Errorf("empty document")
	}
	for _, k := range root.kids {
		if k.tag == "html" {
			return k, nil
		}
	}
	return nil, fmt.Errorf("no html element")
}

// walk calls fn for every element below e, e included, parents first.
func (e *elem) walk(fn func(*elem)) {
	if e.tag != "" {
		fn(e)
	}
	for _, k := range e.kids {
		k.walk(fn)
	}
}

// find returns the elements below e that match a selector of the form
// "tag", "tag.class" or ".class".
func (e *elem) find(sel string) []*elem {
	tag, class, _ := strings.Cut(sel, ".")
	var out []*elem
	e.walk(func(x *elem) {
		if tag != "" && x.tag != tag {
			return
		}
		if class != "" && !x.hasClass(class) {
			return
		}
		out = append(out, x)
	})
	return out
}

func (e *elem) hasClass(c string) bool {
	for _, x := range strings.Fields(e.attrs["class"]) {
		if x == c {
			return true
		}
	}
	return false
}

// content returns the text below an element with white space folded.
func (e *elem) content() string {
	var b strings.Builder
	var rec func(*elem)
	rec = func(x *elem) {
		if x.tag == "" {
			b.WriteString(x.text)
			return
		}
		if x.tag == "style" || x.tag == "script" || (x.svg && x.tag == "title") {
			return
		}
		for _, k := range x.kids {
			rec(k)
		}
		switch x.tag {
		case "p", "li", "div", "td", "th", "dt", "dd", "br", "tr", "h1", "h2", "h3", "h4", "figcaption", "summary", "text", "pre":
			b.WriteByte(' ')
		}
	}
	rec(e)
	return strings.Join(strings.Fields(b.String()), " ")
}

// Content categories of the elements a report uses.
var (
	flowElems     = set("div", "p", "section", "article", "header", "footer", "main", "nav", "aside", "h1", "h2", "h3", "h4", "ul", "ol", "dl", "table", "figure", "details", "pre", "span", "a", "strong", "br", "svg", "button")
	phrasingElems = set("span", "a", "strong", "br", "svg", "button")
	svgElems      = set("title", "g", "a", "rect", "circle", "path", "text")
	// allowed says which children an element may have. "#text" stands for
	// text that is not white space.
	allowed = map[string]map[string]bool{
		"html":       set("head", "body"),
		"head":       set("meta", "title", "style"),
		"body":       with(flowElems, "script"),
		"title":      set("#text"),
		"style":      set("#text"),
		"script":     set("#text"),
		"meta":       {},
		"br":         {},
		"div":        with(flowElems, "#text", "dt", "dd"),
		"section":    with(flowElems, "#text"),
		"article":    with(flowElems, "#text"),
		"header":     with(flowElems, "#text"),
		"footer":     with(flowElems, "#text"),
		"main":       with(flowElems, "#text"),
		"nav":        with(flowElems, "#text"),
		"aside":      with(flowElems, "#text"),
		"figure":     with(flowElems, "#text", "figcaption"),
		"details":    with(flowElems, "#text", "summary"),
		"li":         with(flowElems, "#text"),
		"dd":         with(flowElems, "#text"),
		"td":         with(flowElems, "#text"),
		"p":          with(phrasingElems, "#text"),
		"h1":         with(phrasingElems, "#text"),
		"h2":         with(phrasingElems, "#text"),
		"h3":         with(phrasingElems, "#text"),
		"h4":         with(phrasingElems, "#text"),
		"span":       with(phrasingElems, "#text"),
		"strong":     with(phrasingElems, "#text"),
		"summary":    with(phrasingElems, "#text"),
		"figcaption": with(phrasingElems, "#text"),
		"dt":         with(phrasingElems, "#text"),
		"th":         with(phrasingElems, "#text"),
		"pre":        with(phrasingElems, "#text"),
		"button":     with(phrasingElems, "#text"),
		"a":          with(phrasingElems, "#text"),
		"ul":         set("li"),
		"ol":         set("li"),
		"dl":         set("div"),
		"table":      set("thead", "tbody"),
		"thead":      set("tr"),
		"tbody":      set("tr"),
		"tr":         set("th", "td"),
	}
	svgAllowed = map[string]map[string]bool{
		"svg":    svgElems,
		"g":      svgElems,
		"a":      svgElems,
		"title":  set("#text"),
		"text":   set("#text"),
		"rect":   set("title"),
		"circle": set("title"),
		"path":   set("title"),
	}
	// attributes an element may carry beside the global ones.
	attrsOf = map[string]map[string]bool{
		"html": set("lang"), "meta": set("charset", "name", "content"), "a": set("href"), "th": set("scope"), "td": set("colspan"),
		"button": set("type"), "pre": set("tabindex"), "ol": set(), "aside": set(),
	}
	svgAttrsOf = map[string]map[string]bool{
		"svg":    set("viewBox", "width", "height"),
		"g":      set("transform"),
		"a":      set("href"),
		"rect":   set("x", "y", "width", "height", "rx"),
		"circle": set("cx", "cy", "r"),
		"path":   set("d"),
		"text":   set("x", "y", "text-anchor", "transform"),
		"title":  set(),
	}
	globalAttrs = set("id", "class", "title", "hidden", "role", "aria-label", "aria-hidden", "data-rec", "data-label")
)

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func with(base map[string]bool, more ...string) map[string]bool {
	m := map[string]bool{}
	for k := range base {
		m[k] = true
	}
	for _, n := range more {
		m[n] = true
	}
	return m
}

var (
	numberRE = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	idRE     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	pathRE   = regexp.MustCompile(`^[MLHVZAQmlhvzaq0-9 .\-]+$`)
)

// checkHTML holds a parsed report against the rules. It returns every
// violation it finds.
func checkHTML(root *elem, raw []byte) []string {
	var bad []string
	fail := func(format string, a ...any) {
		if len(bad) < 60 {
			bad = append(bad, fmt.Sprintf(format, a...))
		}
	}
	where := func(e *elem) string {
		var parts []string
		for x := e; x != nil && x.tag != "#root"; x = x.parent {
			name := x.tag
			if id := x.attrs["id"]; id != "" {
				name += "#" + id
			} else if c := x.attrs["class"]; c != "" {
				name += "." + strings.Fields(c)[0]
			}
			parts = append([]string{name}, parts...)
		}
		return strings.Join(parts, " > ")
	}

	// The frame of the document.
	if root.attrs["lang"] == "" {
		fail("html has no lang attribute")
	}
	if len(root.kids) != 2 || root.kids[0].tag != "head" || root.kids[1].tag != "body" {
		fail("html must hold head and body, nothing else")
		return bad
	}
	head, body := root.kids[0], root.kids[1]
	if len(head.kids) == 0 || head.kids[0].tag != "meta" || head.kids[0].attrs["charset"] != "utf-8" {
		fail("the first element of head must be <meta charset=\"utf-8\">")
	}
	if t := head.find("title"); len(t) != 1 || t[0].content() == "" && len(t[0].kids) == 0 {
		fail("head needs one title with text")
	}
	if n := len(root.find("style")); n != 1 {
		fail("%d style elements, want 1", n)
	}
	scripts := root.find("script")
	if len(scripts) != 1 || scripts[0].parent != body || body.kids[len(body.kids)-1] != scripts[0] {
		fail("the report has one script, the last element of body")
	}
	if n := len(root.find("main")); n != 1 {
		fail("%d main elements, want 1", n)
	}
	if n := len(root.find("h1")); n != 1 {
		fail("%d h1 elements, want 1", n)
	}

	ids := map[string]*elem{}
	level := 0
	root.walk(func(e *elem) {
		// Content model.
		model, ok := allowed[e.tag]
		attrs := attrsOf[e.tag]
		if e.svg {
			model, ok = svgAllowed[e.tag]
			attrs = svgAttrsOf[e.tag]
		}
		if !ok {
			fail("%s: element <%s> is not one a report uses", where(e), e.tag)
			return
		}
		for i, k := range e.kids {
			name := k.tag
			if name == "" {
				if strings.TrimSpace(k.text) == "" {
					continue
				}
				name = "#text"
			}
			if !model[name] {
				fail("%s: <%s> may not hold %s", where(e), e.tag, name)
			}
			switch {
			case e.tag == "details" && name == "summary" && i != 0:
				fail("%s: summary must come first", where(e))
			case e.tag == "figure" && name == "figcaption" && i != 0 && i != len(e.kids)-1:
				fail("%s: figcaption must come first or last", where(e))
			case e.svg && name == "title" && i != 0:
				fail("%s: the title of a figure must come first", where(e))
			}
		}
		// Elements that must hold something.
		switch e.tag {
		case "ul", "ol", "dl", "thead", "tbody", "tr", "table", "p", "li", "h1", "h2", "h3", "h4", "figure", "section", "article", "a", "button", "summary", "figcaption", "dt", "th":
			if len(e.kids) == 0 && !(e.tag == "p" && e.attrs["id"] == "panel-label") {
				fail("%s: empty <%s>", where(e), e.tag)
			}
		}
		if e.tag == "details" && (len(e.kids) == 0 || e.kids[0].tag != "summary") {
			fail("%s: details without a summary", where(e))
		}
		if e.tag == "dl" {
			for _, d := range e.kids {
				if d.tag == "div" && (len(d.kids) < 2 || d.kids[0].tag != "dt" || d.kids[1].tag != "dd") {
					fail("%s: a group of a description list holds a dt, then a dd", where(d))
				}
			}
		}
		if (e.tag == "dt" || e.tag == "dd") && (e.parent.tag != "div" || e.parent.parent.tag != "dl") {
			fail("%s: <%s> outside a description list", where(e), e.tag)
		}
		if !e.svg && e.tag == "a" {
			for x := e.parent; x != nil; x = x.parent {
				if x.tag == "a" {
					fail("%s: a link inside a link", where(e))
				}
			}
		}

		// Attributes.
		for name, v := range e.attrs {
			if !globalAttrs[name] && !attrs[name] {
				fail("%s: <%s> has the attribute %s", where(e), e.tag, name)
			}
			if v == "" {
				fail("%s: empty attribute %s", where(e), name)
			}
			if strings.ContainsAny(v, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f") {
				fail("%s: control character in %s", where(e), name)
			}
			switch name {
			case "id":
				if !idRE.MatchString(v) {
					fail("%s: identifier %q", where(e), v)
				}
				if ids[v] != nil {
					fail("%s: identifier %q is used twice", where(e), v)
				}
				ids[v] = e
			case "href":
				if !strings.HasPrefix(v, "#") || len(v) < 2 {
					fail("%s: href %q leaves the report", where(e), v)
				}
			case "x", "y", "width", "height", "rx", "cx", "cy", "r":
				if !numberRE.MatchString(v) {
					fail("%s: %s=%q is not a number", where(e), name, v)
				}
				if f, _ := strconv.ParseFloat(v, 64); (name == "width" || name == "height" || name == "r" || name == "rx") && f < 0 {
					fail("%s: negative %s", where(e), name)
				}
			case "d":
				if !pathRE.MatchString(v) {
					fail("%s: path data %q", where(e), v)
				}
			case "viewBox":
				f := strings.Fields(v)
				if len(f) != 4 || f[0] != "0" || f[1] != "0" || !numberRE.MatchString(f[2]) || !numberRE.MatchString(f[3]) {
					fail("%s: viewBox %q", where(e), v)
				}
			case "colspan":
				if n, err := strconv.Atoi(v); err != nil || n < 2 {
					fail("%s: colspan %q", where(e), v)
				}
			}
		}
		if e.tag == "a" && e.attrs["href"] == "" {
			fail("%s: a link without href", where(e))
		}
		if e.tag == "svg" {
			if e.attrs["viewBox"] == "" {
				fail("%s: svg without viewBox", where(e))
			}
			// A figure names itself; a swatch beside its label is hidden
			// from readers who do not see it.
			named := e.attrs["role"] == "img" && e.attrs["aria-label"] != "" && len(e.kids) > 0 && e.kids[0].tag == "title"
			if !named && e.attrs["aria-hidden"] != "true" {
				fail("%s: an svg is a named image or hidden", where(e))
			}
		}
		if e.tag == "th" && e.attrs["scope"] == "" {
			fail("%s: th without scope", where(e))
		}

		// Headings do not skip a level.
		if len(e.tag) == 2 && e.tag[0] == 'h' && e.tag[1] >= '1' && e.tag[1] <= '4' {
			n := int(e.tag[1] - '0')
			if n > level+1 {
				fail("%s: h%d follows h%d", where(e), n, level)
			}
			level = n
		}

		// Tables: every row has as many cells as the head.
		if e.tag == "table" {
			want := -1
			for _, sec := range e.kids {
				for _, tr := range sec.kids {
					n := 0
					for _, c := range tr.kids {
						span := 1
						if v := c.attrs["colspan"]; v != "" {
							span, _ = strconv.Atoi(v)
						}
						n += span
					}
					if want < 0 {
						want = n
					}
					if n != want {
						fail("%s: a row has %d cells, the head has %d", where(e), n, want)
					}
				}
			}
			if len(e.kids) == 0 || e.kids[0].tag != "thead" {
				fail("%s: table without a head", where(e))
			}
		}
	})

	// Links lead somewhere, and a mark that names records names records
	// the report holds.
	root.walk(func(e *elem) {
		if h := e.attrs["href"]; strings.HasPrefix(h, "#") && ids[h[1:]] == nil {
			fail("%s: link to %s, which is not in the report", where(e), h)
		}
		if keys, ok := e.attrs["data-rec"]; ok {
			if e.attrs["href"] == "" || e.attrs["data-label"] == "" {
				fail("%s: a mark with records needs a link and a label", where(e))
			}
			for _, k := range strings.Fields(keys) {
				if t := ids["rec-"+k]; t == nil || t.tag != "tbody" {
					fail("%s: record %s is not in the appendix", where(e), k)
				}
			}
		}
	})

	// Nothing is loaded from elsewhere. The content model above admits no
	// element that loads something and no attribute that names a resource;
	// what is left to check is the style sheet and the script.
	for _, e := range append(root.find("style"), scripts...) {
		text := e.content0()
		for _, banned := range []string{"http:", "https:", "//", "@import", "url(", "fetch", "XMLHttpRequest", "import(", "eval(", "document.write", "innerHTML", "src"} {
			if i := strings.Index(text, banned); i >= 0 {
				fail("<%s> holds %q: %s", e.tag, banned, strings.TrimSpace(text[max(i-40, 0):min(i+60, len(text))]))
			}
		}
	}
	_ = raw
	return bad
}

// content0 returns the text directly below an element, as it is.
func (e *elem) content0() string {
	var b strings.Builder
	for _, k := range e.kids {
		if k.tag == "" {
			b.WriteString(k.text)
		}
	}
	return b.String()
}
