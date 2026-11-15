package report

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The report is built as a tree of elements and written by one serializer.
// The serializer escapes every text and every attribute value, closes every
// element and writes markup that is both HTML and well-formed XML: text
// from a log line, a host name or a slot label cannot become markup, and a
// test can read the whole document with an XML parser.

// node is an element, or a run of text when tag is empty.
type node struct {
	tag   string
	attrs []attr
	kids  []*node
	text  string
	// raw marks the text of a style or script element: it is written as
	// it is.
	raw bool
}

type attr struct{ name, value string }

// A is a list of attribute names and values: A{"class", "note", "id", "x"}.
// An attribute with an empty value is left out.
type A []string

// el builds an element. Arguments are attributes (A), children (*node,
// []*node), text (string), sentences (R) and nil, in any order; text and
// children keep the order they are given in.
func el(tag string, args ...any) *node {
	n := &node{tag: tag}
	n.add(args...)
	return n
}

// add appends attributes, children and text to an element.
func (n *node) add(args ...any) *node {
	for _, a := range args {
		switch v := a.(type) {
		case nil:
		case A:
			for i := 0; i+1 < len(v); i += 2 {
				if v[i+1] != "" {
					n.set(v[i], v[i+1])
				}
			}
		case *node:
			if v != nil {
				n.kids = append(n.kids, v)
			}
		case []*node:
			for _, k := range v {
				if k != nil {
					n.kids = append(n.kids, k)
				}
			}
		case string:
			if v != "" {
				n.kids = append(n.kids, &node{text: v})
			}
		case R:
			n.kids = append(n.kids, rich(string(v))...)
		default:
			panic(fmt.Sprintf("report: el(%s): argument of type %T", n.tag, a))
		}
	}
	return n
}

// set gives an attribute a value. An element has an attribute once: a
// later value replaces an earlier one.
func (n *node) set(name, value string) {
	for i := range n.attrs {
		if n.attrs[i].name == name {
			n.attrs[i].value = value
			return
		}
	}
	n.attrs = append(n.attrs, attr{name, value})
}

// R is a sentence. In the report a date or a time stays on one line
// together with the two words before it ("latest on 2026-11-12 03:14 UTC"
// is not cut after "on" or after a hyphen), and a last word of one or two
// letters is tied to the word before it, so that a line does not end in a
// stub.
type R string

// wholeRE finds a date with or without a time, with up to two words
// before it and what is written onto it: punctuation, or a Korean
// particle.
var wholeRE = regexp.MustCompile(`(?:[^\s()]+ ){0,2}\d{4}-\d{2}-\d{2}(?: \d{2}:\d{2}(?::\d{2})?(?: UTC)?)?\S*`)

// rich turns a sentence into text and spans that do not break.
func rich(text string) []*node {
	lead := text[:len(text)-len(strings.TrimLeft(text, " "))]
	trail := text[len(strings.TrimRight(text, " ")):]
	text = tie(strings.TrimSpace(text))
	var out []*node
	if lead != "" {
		out = append(out, txt(lead))
	}
	last := 0
	for _, m := range wholeRE.FindAllStringIndex(text, -1) {
		if m[0] > last {
			out = append(out, txt(text[last:m[0]]))
		}
		out = append(out, el("span", A{"class", "nw"}, text[m[0]:m[1]]))
		last = m[1]
	}
	if last < len(text) {
		out = append(out, txt(text[last:]))
	}
	if trail != "" && text != "" {
		out = append(out, txt(trail))
	}
	return out
}

// tie joins a last word of one or two letters to the word before it with
// a space that does not break.
func tie(text string) string {
	i := strings.LastIndexByte(text, ' ')
	if i <= 0 || utf8.RuneCountInString(text[i+1:]) > 2 || strings.IndexByte(text[:i], ' ') < 0 {
		return text
	}
	return text[:i] + "\u00a0" + text[i+1:]
}

// txt is a run of text.
func txt(s string) *node { return &node{text: s} }

// rawText is the content of a style or script element.
func rawText(s string) *node { return &node{text: s, raw: true} }

// attr returns the value of an attribute, or "".
func (n *node) attr(name string) string {
	for _, a := range n.attrs {
		if a.name == name {
			return a.value
		}
	}
	return ""
}

// voidElements are the HTML elements that have no end tag.
var voidElements = map[string]bool{"meta": true, "br": true, "hr": true, "col": true, "wbr": true, "img": true, "input": true, "link": true}

// rawElements hold text that HTML does not parse: it is written as it is.
var rawElements = map[string]bool{"style": true, "script": true}

var textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
var attrEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "\n", "&#10;", "\t", "&#9;", "\r", "&#13;")

// clean drops the characters XML does not allow and HTML does not show:
// control characters other than tab and line feed. A log line can hold
// any byte.
func clean(s string) string {
	ok := utf8.ValidString(s)
	for _, r := range s {
		if !ok {
			break
		}
		ok = !bad(r) && r != '\r'
	}
	if ok {
		return s
	}
	// Line ends of any kind become line feeds; bytes that are not UTF-8
	// and characters that cannot be written become the replacement
	// character, so that the reader sees that something was there.
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	var b strings.Builder
	for _, r := range s {
		if bad(r) {
			b.WriteRune(utf8.RuneError)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func bad(r rune) bool {
	switch {
	case r == '\t' || r == '\n':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r < 0xa0:
		return true
	case r == 0xFFFE || r == 0xFFFF, r >= 0xD800 && r <= 0xDFFF:
		return true
	}
	return false
}

// write serializes the tree. inSVG says that the element lies inside an
// svg element, where an element without children is written self-closed.
func (n *node) write(w *strings.Builder, inSVG bool) error {
	if n.tag == "" {
		if n.raw {
			// The text of a style or script element is not parsed by HTML;
			// to stay well-formed XML too it must not hold these.
			if strings.ContainsAny(n.text, "<&") {
				return fmt.Errorf("report: style or script text holds '<' or '&'")
			}
			w.WriteString(n.text)
			return nil
		}
		w.WriteString(textEscaper.Replace(clean(n.text)))
		return nil
	}
	svg := inSVG || n.tag == "svg"
	w.WriteByte('<')
	w.WriteString(n.tag)
	for _, a := range n.attrs {
		w.WriteByte(' ')
		w.WriteString(a.name)
		w.WriteString(`="`)
		w.WriteString(attrEscaper.Replace(clean(a.value)))
		w.WriteByte('"')
	}
	switch {
	case voidElements[n.tag] && !svg:
		if len(n.kids) > 0 {
			return fmt.Errorf("report: <%s> cannot have content", n.tag)
		}
		w.WriteString(" />")
		return nil
	case svg && len(n.kids) == 0:
		w.WriteString("/>")
		return nil
	}
	w.WriteByte('>')
	for _, k := range n.kids {
		if rawElements[n.tag] && !svg && (k.tag != "" || !k.raw) {
			return fmt.Errorf("report: <%s> takes raw text only", n.tag)
		}
		if err := k.write(w, svg); err != nil {
			return err
		}
	}
	w.WriteString("</")
	w.WriteString(n.tag)
	w.WriteByte('>')
	return nil
}

// writeDocument writes the tree below root as an HTML document.
func writeDocument(out io.Writer, root *node) error {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n")
	if err := root.write(&b, false); err != nil {
		return err
	}
	b.WriteByte('\n')
	_, err := io.WriteString(out, b.String())
	return err
}

// num writes a coordinate with at most two decimals and without trailing
// zeros, so that the markup stays short and stable.
func num(v float64) string {
	s := fmt.Sprintf("%.2f", v)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}

// classes joins class names, leaving out empty ones.
func classes(names ...string) string {
	var out []string
	for _, n := range names {
		if n != "" {
			out = append(out, n)
		}
	}
	return strings.Join(out, " ")
}
