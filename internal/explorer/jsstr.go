package explorer

import (
	"strings"
	"unicode/utf8"
)

// isJSSpace tells the white space of JavaScript: what \s matches and
// trim() removes (WhiteSpace and LineTerminator of ECMAScript). Go's \s,
// strings.TrimSpace and strings.Fields use other sets.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0x00A0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// Trim is String.prototype.trim.
func Trim(s string) string { return TrimEnd(TrimStart(s)) }

// TrimStart is String.prototype.trimStart.
func TrimStart(s string) string {
	for s != "" {
		r, n := utf8.DecodeRuneInString(s)
		if !isJSSpace(r) {
			break
		}
		s = s[n:]
	}
	return s
}

// TrimEnd is String.prototype.trimEnd.
func TrimEnd(s string) string {
	for s != "" {
		r, n := utf8.DecodeLastRuneInString(s)
		if !isJSSpace(r) {
			break
		}
		s = s[:len(s)-n]
	}
	return s
}

// SplitSpace is s.split(/\s+/): an empty string gives one empty element,
// white space at either end gives an empty first or last element.
func SplitSpace(s string) []string {
	var out []string
	start := 0
	i := 0
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if !isJSSpace(r) {
			i += n
			continue
		}
		out = append(out, s[start:i])
		j := i + n
		for j < len(s) {
			r2, n2 := utf8.DecodeRuneInString(s[j:])
			if !isJSSpace(r2) {
				break
			}
			j += n2
		}
		i = j
		start = j
	}
	return append(out, s[start:])
}

// Lines is s.split('\n').
func Lines(s string) []string { return strings.Split(s, "\n") }

// upperASCII is toUpperCase() for text the patterns made of ASCII letters.
func upperASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

// lowerASCII is toLowerCase() for text the patterns made of ASCII letters.
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// protoKeys are the names an object of JavaScript inherits from
// Object.prototype. A lookup with such a name finds something, and an
// assignment of a number to __proto__ stores nothing.
var protoKeys = map[string]bool{
	"constructor": true, "__proto__": true, "toString": true, "toLocaleString": true, "valueOf": true,
	"hasOwnProperty": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	"__defineGetter__": true, "__defineSetter__": true, "__lookupGetter__": true, "__lookupSetter__": true,
}

// Record is a plain object of JavaScript with number values, in the order
// of its keys as JavaScript keeps them: keys that are array indices first,
// in ascending order, then the others in the order they were set.
type Record struct {
	keys []string
	vals map[string]Num
}

// Set is record[key] = v. A key __proto__ stores nothing.
func (r *Record) Set(key string, v Num) {
	if key == "__proto__" {
		return
	}
	if r.vals == nil {
		r.vals = map[string]Num{}
	}
	if _, ok := r.vals[key]; !ok {
		r.keys = append(r.keys, key)
	}
	r.vals[key] = v
}

// Get is record[key] ?? undefined: ok is false when the record has no own
// key of that name.
func (r *Record) Get(key string) (Num, bool) {
	v, ok := r.vals[key]
	return v, ok
}

// Or0 is record[key] ?? 0.
func (r *Record) Or0(key string) Num {
	v, _ := r.vals[key]
	return v
}

// Len is the number of keys.
func (r *Record) Len() int { return len(r.keys) }

// Keys returns the keys in the order of JavaScript.
func (r *Record) Keys() []string {
	var idx, rest []string
	for _, k := range r.keys {
		if isArrayIndex(k) {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	sortIndexKeys(idx)
	return append(idx, rest...)
}

// MarshalJSON writes the record as JSON.stringify writes the object.
func (r Record) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range r.Keys() {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(jsonString(k))
		b.WriteByte(':')
		v, _ := r.vals[k].MarshalJSON()
		b.Write(v)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

// isArrayIndex tells a canonical numeric string below 2^32-1.
func isArrayIndex(k string) bool {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return false
	}
	var v uint64
	for i := 0; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return false
		}
		v = v*10 + uint64(k[i]-'0')
	}
	return v < 1<<32-1
}

func sortIndexKeys(k []string) {
	for i := 1; i < len(k); i++ {
		for j := i; j > 0 && (len(k[j]) < len(k[j-1]) || len(k[j]) == len(k[j-1]) && k[j] < k[j-1]); j-- {
			k[j], k[j-1] = k[j-1], k[j]
		}
	}
}

// jsonString quotes s as JSON.stringify does.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			const hex = "0123456789abcdef"
			b.WriteString(`\u00`)
			b.WriteByte(hex[r>>4])
			b.WriteByte(hex[r&15])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
