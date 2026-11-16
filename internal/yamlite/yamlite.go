// Package yamlite reads a small part of YAML: mappings, block lists and
// scalars, indented with spaces. It is what the settings profile of
// "my bios" and the mapping tables of firmware messages are written in,
// and it keeps the reader small enough to check by reading it. What lies
// outside this part (flow style, anchors, tags, multi-line scalars,
// several documents, tabs) is refused with the line it is on.
package yamlite

import (
	"errors"
	"fmt"
	"strings"
)

// Limits of a profile file.
const (
	MaxBytes = 1 << 20
	MaxLines = 20000
	MaxDepth        = 12
)

// kind of a node.
const (
	Scalar = iota
	Map
	List
)

// node is a value of the file.
type Node struct {
	Kind   int
	Line   int
	Scalar string
	// Keys keeps the order of a mapping.
	Keys []string
	Map  map[string]*Node
	List []*Node
}

// line of the file that holds something.
type yline struct {
	n      int // line number, from 1
	indent int
	text   string // without indent and without a trailing comment
}

func ErrAt(line int, format string, a ...any) error {
	return fmt.Errorf("line %d: %s", line, fmt.Sprintf(format, a...))
}

// stripComment cuts a comment off a line: a "#" at the start, or after a
// space, outside quotes.
func stripComment(s string) string {
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '"' && c == '\\':
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			// A quote starts a quoted text only where a value can start.
			if i == 0 || s[i-1] == ' ' {
				quote = c
			}
		case c == '#' && (i == 0 || s[i-1] == ' '):
			return strings.TrimRight(s[:i], " ")
		}
	}
	return strings.TrimRight(s, " ")
}

// lines splits a file into the lines that hold something.
func splitLines(data []byte) ([]yline, error) {
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("larger than %d bytes", MaxBytes)
	}
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	var out []yline
	for i, raw := range strings.Split(text, "\n") {
		n := i + 1
		if n > MaxLines {
			return nil, fmt.Errorf("more than %d lines", MaxLines)
		}
		raw = strings.TrimRight(raw, "\r")
		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		rest := raw[indent:]
		if strings.HasPrefix(rest, "\t") {
			return nil, ErrAt(n, "a tab in the indentation: indent with spaces")
		}
		rest = stripComment(rest)
		if rest == "" {
			continue
		}
		for j := 0; j < len(rest); j++ {
			if rest[j] < 0x20 && rest[j] != '\t' || rest[j] == 0x7f {
				return nil, ErrAt(n, "a control character")
			}
		}
		if rest == "---" || rest == "..." || strings.HasPrefix(rest, "--- ") {
			return nil, ErrAt(n, "document markers are not read: a profile is one document")
		}
		out = append(out, yline{n: n, indent: indent, text: rest})
	}
	return out, nil
}

// scalarText reads a scalar: quoted with " or ', or plain.
func scalarText(s string, line int) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", nil
	case s[0] == '"':
		if len(s) < 2 || s[len(s)-1] != '"' {
			return "", ErrAt(line, "a quoted text without its closing quote")
		}
		var b strings.Builder
		body := s[1 : len(s)-1]
		for i := 0; i < len(body); i++ {
			c := body[i]
			if c == '"' {
				return "", ErrAt(line, "a quote inside a quoted text: write \\\"")
			}
			if c != '\\' {
				b.WriteByte(c)
				continue
			}
			i++
			if i == len(body) {
				return "", ErrAt(line, "a quoted text that ends in a backslash")
			}
			switch body[i] {
			case '"', '\\', '/':
				b.WriteByte(body[i])
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", ErrAt(line, "the escape \\%c is not read", body[i])
			}
		}
		return b.String(), nil
	case s[0] == '\'':
		if len(s) < 2 || s[len(s)-1] != '\'' {
			return "", ErrAt(line, "a quoted text without its closing quote")
		}
		body := s[1 : len(s)-1]
		if strings.Contains(strings.ReplaceAll(body, "''", ""), "'") {
			return "", ErrAt(line, "a quote inside a quoted text: write ''")
		}
		return strings.ReplaceAll(body, "''", "'"), nil
	}
	switch s[0] {
	case '[', '{':
		return "", ErrAt(line, "flow style (%c) is not read: write one item per line", s[0])
	case '&', '*', '!', '|', '>', '%', '@', '`':
		return "", ErrAt(line, "a value that starts with %c is not read: put it in quotes", s[0])
	}
	return s, nil
}

// splitKey splits "key: value" into its parts. ok is false when the text
// is no mapping entry.
func splitKey(s string, line int) (key, rest string, ok bool, err error) {
	end := -1
	switch {
	case s == "":
		return "", "", false, nil
	case s[0] == '"' || s[0] == '\'':
		// A quoted key: find its closing quote.
		q := s[0]
		for i := 1; i < len(s); i++ {
			if q == '"' && s[i] == '\\' {
				i++
				continue
			}
			if s[i] == q {
				if q == '\'' && i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				end = i + 1
				break
			}
		}
		if end < 0 || end >= len(s) || s[end] != ':' {
			return "", "", false, nil
		}
		if key, err = scalarText(s[:end], line); err != nil {
			return "", "", false, err
		}
	default:
		i := strings.Index(s, ": ")
		if i < 0 {
			if !strings.HasSuffix(s, ":") {
				return "", "", false, nil
			}
			i = len(s) - 1
		}
		key, end = strings.TrimSpace(s[:i]), i
		if key == "" || strings.ContainsAny(key[:1], "[{&*!|>%@`-") && key != "-" {
			return "", "", false, nil
		}
	}
	if key == "" {
		return "", "", false, ErrAt(line, "an empty key")
	}
	return key, strings.TrimSpace(s[end+1:]), true, nil
}

// parser walks the lines of a file.
type parser struct {
	lines []yline
	at    int
}

// parseYAML reads a file into its tree.
func Parse(data []byte) (*Node, error) {
	lines, err := splitLines(data)
	if err != nil {
		return nil, err
	}
	if len(lines) == 0 {
		return nil, errors.New("the file holds nothing")
	}
	p := &parser{lines: lines}
	root, err := p.block(lines[0].indent, 0)
	if err != nil {
		return nil, err
	}
	if p.at < len(p.lines) {
		return nil, ErrAt(p.lines[p.at].n, "the indentation does not fit the lines above")
	}
	return root, nil
}

// block reads the lines at one indentation: a mapping or a list.
func (p *parser) block(indent, depth int) (*Node, error) {
	if depth > MaxDepth {
		return nil, ErrAt(p.lines[p.at].n, "nested deeper than %d levels", MaxDepth)
	}
	first := p.lines[p.at]
	if strings.HasPrefix(first.text, "- ") || first.text == "-" {
		return p.sequence(indent, depth)
	}
	return p.mapping(indent, depth, nil)
}

// value reads what follows a key or a list dash: a scalar on the same
// line, or a block on the lines below that are indented deeper than
// parent.
func (p *parser) value(rest string, line, parent, depth int) (*Node, error) {
	if rest != "" {
		text, err := scalarText(rest, line)
		if err != nil {
			return nil, err
		}
		return &Node{Kind: Scalar, Line: line, Scalar: text}, nil
	}
	if p.at < len(p.lines) && p.lines[p.at].indent > parent {
		return p.block(p.lines[p.at].indent, depth+1)
	}
	return &Node{Kind: Scalar, Line: line}, nil
}

// mapping reads the entries of a mapping at an indentation. into is a
// mapping that was begun on the line of a list dash.
func (p *parser) mapping(indent, depth int, into *Node) (*Node, error) {
	n := into
	if n == nil {
		n = &Node{Kind: Map, Line: p.lines[p.at].n, Map: map[string]*Node{}}
	}
	for p.at < len(p.lines) && p.lines[p.at].indent == indent {
		l := p.lines[p.at]
		if strings.HasPrefix(l.text, "- ") || l.text == "-" {
			return nil, ErrAt(l.n, "a list item where a key is expected")
		}
		key, rest, ok, err := splitKey(l.text, l.n)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrAt(l.n, "expected \"key: value\"")
		}
		if _, dup := n.Map[key]; dup {
			return nil, ErrAt(l.n, "the key %q is given twice", key)
		}
		p.at++
		v, err := p.value(rest, l.n, indent, depth)
		if err != nil {
			return nil, err
		}
		n.Keys, n.Map[key] = append(n.Keys, key), v
	}
	return n, nil
}

// sequence reads the items of a list at an indentation.
func (p *parser) sequence(indent, depth int) (*Node, error) {
	n := &Node{Kind: List, Line: p.lines[p.at].n}
	for p.at < len(p.lines) && p.lines[p.at].indent == indent {
		l := p.lines[p.at]
		if !strings.HasPrefix(l.text, "- ") && l.text != "-" {
			return nil, ErrAt(l.n, "a key where a list item is expected")
		}
		rest := strings.TrimSpace(strings.TrimPrefix(l.text, "-"))
		// "- key: value" begins a mapping whose other keys stand below the
		// first one.
		if key, tail, ok, err := splitKey(rest, l.n); err != nil {
			return nil, err
		} else if ok {
			inner := indent + (len(l.text) - len(strings.TrimLeft(l.text[1:], " ")))
			item := &Node{Kind: Map, Line: l.n, Map: map[string]*Node{}}
			p.at++
			v, err := p.value(tail, l.n, inner, depth+1)
			if err != nil {
				return nil, err
			}
			item.Keys, item.Map[key] = append(item.Keys, key), v
			if p.at < len(p.lines) && p.lines[p.at].indent == inner {
				if _, err := p.mapping(inner, depth+1, item); err != nil {
					return nil, err
				}
			}
			n.List = append(n.List, item)
			continue
		}
		p.at++
		v, err := p.value(rest, l.n, indent, depth)
		if err != nil {
			return nil, err
		}
		n.List = append(n.List, v)
	}
	return n, nil
}

// Text returns the value of a scalar node; what names the node in the
// error.
func (n *Node) Text(what string) (string, error) {
	if n.Kind != Scalar {
		return "", ErrAt(n.Line, "%s: expected a value", what)
	}
	return n.Scalar, nil
}

// Mapping checks that n is a mapping whose keys are among known; with no
// known keys any key is taken.
func (n *Node) Mapping(what string, known ...string) error {
	if n.Kind != Map {
		return ErrAt(n.Line, "%s: expected keys with values", what)
	}
	for _, k := range n.Keys {
		ok := len(known) == 0
		for _, w := range known {
			ok = ok || k == w
		}
		if !ok {
			return ErrAt(n.Map[k].Line, "%s: unknown key %q (known: %s)", what, k, strings.Join(known, ", "))
		}
	}
	return nil
}

// Items returns the items of a list node; an empty value is an empty list.
func (n *Node) Items(what string) ([]*Node, error) {
	if n.Kind == Scalar && n.Scalar == "" {
		return nil, nil
	}
	if n.Kind != List {
		return nil, ErrAt(n.Line, "%s: expected a list", what)
	}
	return n.List, nil
}
