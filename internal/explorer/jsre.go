package explorer

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// A regular expression of the reference is written here as JavaScript
// wrote it and turned into a Go expression that matches the same strings:
//
//   - \s and \S use the white space of JavaScript (isJSSpace);
//   - . matches any character but the four line terminators;
//   - without the flag m, ^ and $ are the start and the end of the input;
//     with it, a line starts after LF, CR, U+2028 or U+2029. Go knows LF
//     only, so the input of such an expression is matched in a copy that
//     has LF in place of each terminator (U+2028 and U+2029 become three
//     LF, so that every offset stays the same); groups are cut from the
//     input itself;
//   - the flag i becomes classes of both ASCII cases. JavaScript without
//     the flag u folds no other letter into an ASCII one (U+212A is no K,
//     U+017F no s), and every pattern here is ASCII;
//   - look-aheads are not translated: their places are loops in the code.
//
// Go's regexp returns the match a backtracking engine finds, so groups
// agree as well.

// jsRegexp is one translated expression.
type jsRegexp struct {
	src, flags string
	re         *regexp.Regexp
	multiline  bool
}

// wsClass is the body of a character class of the white space of
// JavaScript.
const wsClass = `\t\n\v\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}`

// dotClass is what . of JavaScript matches.
const dotClass = `[^\n\r\x{2028}\x{2029}]`

// jsre compiles a JavaScript pattern with its flags (any of "gim"; g is
// the business of the caller). It panics on what it cannot translate: the
// patterns are fixed words of this package.
func jsre(src, flags string) *jsRegexp {
	goSrc, err := translate(src, strings.Contains(flags, "i"), strings.Contains(flags, "m"))
	if err != nil {
		panic(fmt.Sprintf("explorer: pattern /%s/%s: %v", src, flags, err))
	}
	return &jsRegexp{src: src, flags: flags, re: regexp.MustCompile(goSrc), multiline: strings.Contains(flags, "m")}
}

// subject is the text the Go expression is matched in.
func (r *jsRegexp) subject(s string) string {
	if !r.multiline || !strings.ContainsAny(s, "\r  ") {
		return s
	}
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, " ", "\n\n\n")
	return strings.ReplaceAll(s, " ", "\n\n\n")
}

// jsMatch is the result of exec(): the groups as offsets into the input.
type jsMatch struct {
	s   string
	idx []int
}

// Group is m[i], "" when the group took no part.
func (m jsMatch) Group(i int) string {
	if 2*i+1 >= len(m.idx) || m.idx[2*i] < 0 {
		return ""
	}
	return m.s[m.idx[2*i]:m.idx[2*i+1]]
}

// Has tells whether group i took part (m[i] !== undefined).
func (m jsMatch) Has(i int) bool { return 2*i+1 < len(m.idx) && m.idx[2*i] >= 0 }

// End is the offset after the match (lastIndex of a global expression).
func (m jsMatch) End() int { return m.idx[1] }

// Start is the offset of the match.
func (m jsMatch) Start() int { return m.idx[0] }

// Match is s.match(re) of an expression without g: the first match.
func (r *jsRegexp) Match(s string) (jsMatch, bool) {
	idx := r.re.FindStringSubmatchIndex(r.subject(s))
	if idx == nil {
		return jsMatch{}, false
	}
	return jsMatch{s: s, idx: runeBounds(s, idx)}, true
}

// runeBounds moves an offset that falls inside a character to its end. In
// the copy with LF for U+2028 and U+2029 a line starts at each of the three
// LF; JavaScript starts it after the character.
func runeBounds(s string, idx []int) []int {
	for i, at := range idx {
		for at > 0 && at < len(s) && !utf8.RuneStart(s[at]) {
			at++
		}
		idx[i] = at
	}
	return idx
}

// Test is re.test(s).
func (r *jsRegexp) Test(s string) bool { return r.re.MatchString(r.subject(s)) }

// All is the loop of exec() over a global expression, or matchAll: every
// match from the end of the one before.
func (r *jsRegexp) All(s string) []jsMatch {
	var out []jsMatch
	for _, idx := range r.re.FindAllStringSubmatchIndex(r.subject(s), -1) {
		out = append(out, jsMatch{s: s, idx: runeBounds(s, idx)})
	}
	return out
}

// Index returns the offset of the first match at or after from, or -1.
func (r *jsRegexp) Index(s string, from int) int {
	loc := r.re.FindStringIndex(r.subject(s)[from:])
	if loc == nil {
		return -1
	}
	return from + loc[0]
}

// translate turns JavaScript pattern syntax into Go syntax.
func translate(src string, fold, multiline bool) (string, error) {
	var b strings.Builder
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch c {
		case '\\':
			if i+1 >= len(src) {
				return "", fmt.Errorf("trailing backslash")
			}
			i++
			e := src[i]
			switch e {
			case 's':
				b.WriteString("[" + wsClass + "]")
			case 'S':
				b.WriteString("[^" + wsClass + "]")
			case 'd', 'D', 'w', 'W', 'b', 'B', 'n', 't', 'r', 'f', 'v':
				b.WriteByte('\\')
				b.WriteByte(e)
			default:
				if isPunct(e) {
					b.WriteByte('\\')
					b.WriteByte(e)
				} else {
					return "", fmt.Errorf("escape \\%c", e)
				}
			}
		case '.':
			b.WriteString(dotClass)
		case '^':
			if multiline {
				b.WriteString("(?m:^)")
			} else {
				b.WriteString(`\A`)
			}
		case '$':
			if multiline {
				b.WriteString("(?m:$)")
			} else {
				b.WriteString(`\z`)
			}
		case '(':
			if strings.HasPrefix(src[i:], "(?=") || strings.HasPrefix(src[i:], "(?!") || strings.HasPrefix(src[i:], "(?<") {
				return "", fmt.Errorf("look-around")
			}
			b.WriteByte('(')
		case '[':
			end, class, err := translateClass(src, i, fold)
			if err != nil {
				return "", err
			}
			b.WriteString(class)
			i = end
		case ')', '|', '*', '+', '?', '{', '}', ':':
			b.WriteByte(c)
		default:
			if c >= 0x80 {
				return "", fmt.Errorf("non-ASCII pattern")
			}
			if fold && isLetter(c) {
				b.WriteString("[" + string(upper(c)) + string(lower(c)) + "]")
			} else {
				b.WriteString(regexp.QuoteMeta(string(c)))
			}
		}
	}
	return b.String(), nil
}

// translateClass translates the class that starts at src[at] and returns
// the offset of its closing bracket.
func translateClass(src string, at int, fold bool) (int, string, error) {
	i := at + 1
	neg := false
	if i < len(src) && src[i] == '^' {
		neg = true
		i++
	}
	if strings.HasPrefix(src[i:], `\s\S]`) && !neg {
		return i + 4, `(?s:.)`, nil
	}
	var items []string
	var letters []byte // single letters and range ends, for folding
	type rng struct{ lo, hi byte }
	var ranges []rng
	for ; i < len(src); i++ {
		c := src[i]
		if c == ']' {
			break
		}
		var lit byte
		if c == '\\' {
			i++
			if i >= len(src) {
				return 0, "", fmt.Errorf("class: trailing backslash")
			}
			switch e := src[i]; e {
			case 's':
				items = append(items, wsClass)
				continue
			case 'd', 'w', 'n', 't', 'r', 'f', 'v':
				items = append(items, `\`+string(e))
				continue
			default:
				if !isPunct(e) {
					return 0, "", fmt.Errorf("class: escape \\%c", e)
				}
				lit = e
			}
		} else {
			if c >= 0x80 {
				return 0, "", fmt.Errorf("class: non-ASCII")
			}
			lit = c
		}
		if i+2 < len(src) && src[i+1] == '-' && src[i+2] != ']' && src[i+2] != '\\' {
			hi := src[i+2]
			ranges = append(ranges, rng{lit, hi})
			items = append(items, quoteClass(lit)+"-"+quoteClass(hi))
			i += 2
			continue
		}
		items = append(items, quoteClass(lit))
		if isLetter(lit) {
			letters = append(letters, lit)
		}
	}
	if i >= len(src) {
		return 0, "", fmt.Errorf("class not closed")
	}
	if fold {
		for _, l := range letters {
			items = append(items, string(upper(l)), string(lower(l)))
		}
		for _, r := range ranges {
			// The part of the range in each case, written in the other.
			if lo, hi := maxb(r.lo, 'A'), minb(r.hi, 'Z'); lo <= hi {
				items = append(items, string(lower(lo))+"-"+string(lower(hi)))
			}
			if lo, hi := maxb(r.lo, 'a'), minb(r.hi, 'z'); lo <= hi {
				items = append(items, string(upper(lo))+"-"+string(upper(hi)))
			}
		}
	}
	prefix := "["
	if neg {
		prefix = "[^"
	}
	return i, prefix + strings.Join(items, "") + "]", nil
}

func quoteClass(c byte) string {
	switch c {
	case '\\', ']', '[', '^', '-':
		return `\` + string(c)
	}
	return string(c)
}

func isPunct(c byte) bool {
	return strings.IndexByte(`.*+?()[]{}|^$\/-:=!,'"_ <>#%&@~`+"`", c) >= 0
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func upper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 32
	}
	return c
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}

func maxb(a, b byte) byte {
	if a > b {
		return a
	}
	return b
}

func minb(a, b byte) byte {
	if a < b {
		return a
	}
	return b
}
