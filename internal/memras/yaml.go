package memras

import (
	"errors"
	"fmt"
	"strings"
)

// A profile is written in a small part of YAML: mappings, block lists and
// scalars, indented with spaces. That is all a profile needs, and it keeps
// the reader small enough to check by reading it. What lies outside this
// part (flow style, anchors, tags, multi-line scalars, several documents,
// tabs) is refused with the line it is on.

// Limits of a profile file.
const (
	maxProfileBytes = 1 << 20
	maxProfileLines = 20000
	maxDepth        = 12
)

// kind of a node.
const (
	kindScalar = iota
	kindMap
	kindList
)

// node is a value of the file.
type node struct {
	kind   int
	line   int
	scalar string
	// keys keeps the order of a mapping.
	keys []string
	m    map[string]*node
	list []*node
}

// line of the file that holds something.
type yline struct {
	n      int // line number, from 1
	indent int
	text   string // without indent and without a trailing comment
}

func errAt(line int, format string, a ...any) error {
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
	if len(data) > maxProfileBytes {
		return nil, fmt.Errorf("larger than %d bytes", maxProfileBytes)
	}
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	var out []yline
	for i, raw := range strings.Split(text, "\n") {
		n := i + 1
		if n > maxProfileLines {
			return nil, fmt.Errorf("more than %d lines", maxProfileLines)
		}
		raw = strings.TrimRight(raw, "\r")
		indent := 0
		for indent < len(raw) && raw[indent] == ' ' {
			indent++
		}
		rest := raw[indent:]
		if strings.HasPrefix(rest, "\t") {
			return nil, errAt(n, "a tab in the indentation: indent with spaces")
		}
		rest = stripComment(rest)
		if rest == "" {
			continue
		}
		for j := 0; j < len(rest); j++ {
			if rest[j] < 0x20 && rest[j] != '\t' || rest[j] == 0x7f {
				return nil, errAt(n, "a control character")
			}
		}
		if rest == "---" || rest == "..." || strings.HasPrefix(rest, "--- ") {
			return nil, errAt(n, "document markers are not read: a profile is one document")
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
			return "", errAt(line, "a quoted text without its closing quote")
		}
		var b strings.Builder
		body := s[1 : len(s)-1]
		for i := 0; i < len(body); i++ {
			c := body[i]
			if c == '"' {
				return "", errAt(line, "a quote inside a quoted text: write \\\"")
			}
			if c != '\\' {
				b.WriteByte(c)
				continue
			}
			i++
			if i == len(body) {
				return "", errAt(line, "a quoted text that ends in a backslash")
			}
			switch body[i] {
			case '"', '\\', '/':
				b.WriteByte(body[i])
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", errAt(line, "the escape \\%c is not read", body[i])
			}
		}
		return b.String(), nil
	case s[0] == '\'':
		if len(s) < 2 || s[len(s)-1] != '\'' {
			return "", errAt(line, "a quoted text without its closing quote")
		}
		body := s[1 : len(s)-1]
		if strings.Contains(strings.ReplaceAll(body, "''", ""), "'") {
			return "", errAt(line, "a quote inside a quoted text: write ''")
		}
		return strings.ReplaceAll(body, "''", "'"), nil
	}
	switch s[0] {
	case '[', '{':
		return "", errAt(line, "flow style (%c) is not read: write one item per line", s[0])
	case '&', '*', '!', '|', '>', '%', '@', '`':
		return "", errAt(line, "a value that starts with %c is not read: put it in quotes", s[0])
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
		return "", "", false, errAt(line, "an empty key")
	}
	return key, strings.TrimSpace(s[end+1:]), true, nil
}

// parser walks the lines of a file.
type parser struct {
	lines []yline
	at    int
}

// parseYAML reads a file into its tree.
func parseYAML(data []byte) (*node, error) {
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
		return nil, errAt(p.lines[p.at].n, "the indentation does not fit the lines above")
	}
	return root, nil
}

// block reads the lines at one indentation: a mapping or a list.
func (p *parser) block(indent, depth int) (*node, error) {
	if depth > maxDepth {
		return nil, errAt(p.lines[p.at].n, "nested deeper than %d levels", maxDepth)
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
func (p *parser) value(rest string, line, parent, depth int) (*node, error) {
	if rest != "" {
		text, err := scalarText(rest, line)
		if err != nil {
			return nil, err
		}
		return &node{kind: kindScalar, line: line, scalar: text}, nil
	}
	if p.at < len(p.lines) && p.lines[p.at].indent > parent {
		return p.block(p.lines[p.at].indent, depth+1)
	}
	return &node{kind: kindScalar, line: line}, nil
}

// mapping reads the entries of a mapping at an indentation. into is a
// mapping that was begun on the line of a list dash.
func (p *parser) mapping(indent, depth int, into *node) (*node, error) {
	n := into
	if n == nil {
		n = &node{kind: kindMap, line: p.lines[p.at].n, m: map[string]*node{}}
	}
	for p.at < len(p.lines) && p.lines[p.at].indent == indent {
		l := p.lines[p.at]
		if strings.HasPrefix(l.text, "- ") || l.text == "-" {
			return nil, errAt(l.n, "a list item where a key is expected")
		}
		key, rest, ok, err := splitKey(l.text, l.n)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errAt(l.n, "expected \"key: value\"")
		}
		if _, dup := n.m[key]; dup {
			return nil, errAt(l.n, "the key %q is given twice", key)
		}
		p.at++
		v, err := p.value(rest, l.n, indent, depth)
		if err != nil {
			return nil, err
		}
		n.keys, n.m[key] = append(n.keys, key), v
	}
	return n, nil
}

// sequence reads the items of a list at an indentation.
func (p *parser) sequence(indent, depth int) (*node, error) {
	n := &node{kind: kindList, line: p.lines[p.at].n}
	for p.at < len(p.lines) && p.lines[p.at].indent == indent {
		l := p.lines[p.at]
		if !strings.HasPrefix(l.text, "- ") && l.text != "-" {
			return nil, errAt(l.n, "a key where a list item is expected")
		}
		rest := strings.TrimSpace(strings.TrimPrefix(l.text, "-"))
		// "- key: value" begins a mapping whose other keys stand below the
		// first one.
		if key, tail, ok, err := splitKey(rest, l.n); err != nil {
			return nil, err
		} else if ok {
			inner := indent + (len(l.text) - len(strings.TrimLeft(l.text[1:], " ")))
			item := &node{kind: kindMap, line: l.n, m: map[string]*node{}}
			p.at++
			v, err := p.value(tail, l.n, inner, depth+1)
			if err != nil {
				return nil, err
			}
			item.keys, item.m[key] = append(item.keys, key), v
			if p.at < len(p.lines) && p.lines[p.at].indent == inner {
				if _, err := p.mapping(inner, depth+1, item); err != nil {
					return nil, err
				}
			}
			n.list = append(n.list, item)
			continue
		}
		p.at++
		v, err := p.value(rest, l.n, indent, depth)
		if err != nil {
			return nil, err
		}
		n.list = append(n.list, v)
	}
	return n, nil
}
