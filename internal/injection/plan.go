// Package injection reads injection plans and compares them with a ledger.
//
// An injection plan says which errors a test put into a host: what, where
// and how many times. Compared with what the sources of the host recorded,
// it shows whether the path from the hardware to the ledger loses or
// changes anything: an error that is missing, one that is counted twice, a
// row or a channel that was decoded differently.
//
// A plan is a text file in a small subset of YAML:
//
//	format: 1
//	scenario: mce
//	summary: "machine checks in a nested guest"
//	method: "mce-inject module"
//	kernel: 7.3.0-ras
//	injections:
//	  - id: ce-1
//	    time: 2026-11-15T05:14:11.820Z
//	    inject: error
//	    severity: CE
//	    scrub: Demand
//	    count: 1
//	    address: 0x106b2c040
//	    socket: 0
//	    channel: 0
//	    note: "machine check bank 7"
//	  - id: ce-1-offline
//	    inject: page-offline
//	    how: soft
//	    count: 1
//	    address: 0x106b2c000
//
// The top level holds scalars; "injections" holds a list of flat maps.
// Values are plain or in double quotes; numbers are decimal or hexadecimal
// with 0x. Lines that start with # are comments.
package injection

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/model"
)

// Format is the plan format this package reads.
const Format = 1

// What an injection puts into the host.
const (
	// InjectError is a memory error: the sources should hold Count errors
	// with the severity and the location given.
	InjectError = "error"
	// InjectOffline is a page taken out of use, by the test itself or by
	// the kernel as a consequence of an injected error.
	InjectOffline = "page-offline"
)

// Plan is an injection plan.
type Plan struct {
	Scenario string
	Summary  string
	Method   string
	// Meta holds the other scalars of the top level, such as the kernel
	// and rasdaemon versions of the run.
	Meta       map[string]string
	Injections []Injection
}

// Injection is one entry of a plan.
type Injection struct {
	ID string
	// Time is when the injection started; zero when the plan does not say.
	Time time.Time
	// Inject is InjectError or InjectOffline.
	Inject string
	// How is "hard" or "soft" for a page offline.
	How string
	// Severity and Scrub describe an error in the words of the ledger.
	Severity model.Severity
	Scrub    model.ScrubType
	// Count is how many errors were injected, or how many pages should be
	// offline (1, or 0 for a page that must stay in use).
	Count int
	// Address is the system physical address. DPA is the address within a
	// CXL memory device, named by Device name.
	Address    model.Opt[uint64]
	DPA        model.Opt[uint64]
	DeviceName string

	Socket, Channel, Slot, Rank model.Opt[int]
	Device, BankGroup, Bank     model.Opt[int]
	Row, Column, Bit            model.Opt[int]
	// Label is the DIMM the error belongs to, as the host names it.
	Label string
	Note  string
	// Line is the line of the plan the entry starts on.
	Line int
}

// ReadFile reads the plan at path.
func ReadFile(path string) (*Plan, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, err := Read(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

// maxLine bounds one line of a plan.
const maxLine = 64 << 10

// Read parses a plan. An error names the line it is about.
func Read(r io.Reader) (*Plan, error) {
	p := &Plan{Meta: map[string]string{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxLine)
	var (
		n          int
		format     = -1
		inList     bool
		sawList    bool
		cur        *Injection
		itemIndent int
		seen       map[string]bool
		ids        = map[string]int{}
	)
	finish := func() error {
		if cur == nil {
			return nil
		}
		if err := cur.validate(); err != nil {
			return fmt.Errorf("line %d: injection %q: %w", cur.Line, cur.ID, err)
		}
		if first, dup := ids[cur.ID]; dup {
			return fmt.Errorf("line %d: injection %q is already defined on line %d", cur.Line, cur.ID, first)
		}
		ids[cur.ID] = cur.Line
		p.Injections = append(p.Injections, *cur)
		cur = nil
		return nil
	}
	for sc.Scan() {
		n++
		raw := strings.TrimRight(sc.Text(), " \r")
		body := strings.TrimLeft(raw, " ")
		if body == "" || strings.HasPrefix(body, "#") {
			continue
		}
		indent := len(raw) - len(body)
		if strings.HasPrefix(body, "\t") || strings.Contains(raw[:indent], "\t") {
			return nil, fmt.Errorf("line %d: a tab in the indentation", n)
		}
		item := false
		if strings.HasPrefix(body, "- ") {
			item = true
			body = strings.TrimLeft(body[2:], " ")
		} else if body == "-" {
			return nil, fmt.Errorf("line %d: an empty list entry", n)
		}
		key, value, err := keyValue(body)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}

		switch {
		case indent == 0 && !item:
			if err := finish(); err != nil {
				return nil, err
			}
			inList = false
			switch key {
			case "injections":
				if sawList {
					return nil, fmt.Errorf("line %d: \"injections\" is given twice", n)
				}
				sawList = true
				switch value {
				case "":
					inList = true
				case "[]":
				default:
					return nil, fmt.Errorf("line %d: \"injections\" takes a list of entries, each starting with \"- \"", n)
				}
			case "format":
				v, err := strconv.Atoi(value)
				if err != nil {
					return nil, fmt.Errorf("line %d: format %q is not a number", n, value)
				}
				format = v
			case "scenario":
				p.Scenario = value
			case "summary":
				p.Summary = value
			case "method":
				p.Method = value
			default:
				if _, dup := p.Meta[key]; dup {
					return nil, fmt.Errorf("line %d: %q is given twice", n, key)
				}
				p.Meta[key] = value
			}
		case !inList:
			return nil, fmt.Errorf("line %d: an indented line outside the list of injections", n)
		case item:
			if err := finish(); err != nil {
				return nil, err
			}
			cur = &Injection{Count: 1, Line: n}
			itemIndent = indent + 2
			seen = map[string]bool{}
			fallthrough
		default:
			if cur == nil {
				return nil, fmt.Errorf("line %d: a field before the first \"- \" of the list", n)
			}
			if !item && indent != itemIndent {
				return nil, fmt.Errorf("line %d: the fields of an injection must line up (indent %d, expected %d)", n, indent, itemIndent)
			}
			if seen[key] {
				return nil, fmt.Errorf("line %d: %q is given twice", n, key)
			}
			seen[key] = true
			if err := cur.set(key, value); err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, fmt.Errorf("line %d: longer than %d bytes", n+1, maxLine)
		}
		return nil, err
	}
	if err := finish(); err != nil {
		return nil, err
	}
	switch {
	case format == -1:
		return nil, errors.New("not an injection plan: no \"format\" line")
	case format != Format:
		return nil, fmt.Errorf("plan format %d is not known to this version (it reads format %d)", format, Format)
	case p.Scenario == "":
		return nil, errors.New("the plan names no scenario")
	case !sawList:
		return nil, errors.New("the plan has no \"injections\" list")
	}
	return p, nil
}

// keyValue splits "key: value" and unquotes the value.
func keyValue(s string) (key, value string, err error) {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return "", "", fmt.Errorf("expected \"key: value\", got %q", clip(s))
	}
	key = s[:i]
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return "", "", fmt.Errorf("%q is not a field name", clip(key))
		}
	}
	rest := s[i+1:]
	if rest != "" && rest[0] != ' ' {
		return "", "", fmt.Errorf("a space must follow the colon after %q", key)
	}
	rest = strings.TrimLeft(rest, " ")
	if strings.HasPrefix(rest, `"`) {
		value, tail, err := unquote(rest)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", key, err)
		}
		if t := strings.TrimLeft(tail, " "); t != "" && !strings.HasPrefix(t, "#") {
			return "", "", fmt.Errorf("%s: text after the closing quote", key)
		}
		return key, value, nil
	}
	// A plain value ends where a comment starts.
	if j := strings.Index(rest, " #"); j >= 0 {
		rest = strings.TrimRight(rest[:j], " ")
	}
	if strings.ContainsAny(rest, "{}[]&*!|>'%@`") && rest != "[]" {
		return "", "", fmt.Errorf("%s: a value with special characters must be in double quotes", key)
	}
	return key, rest, nil
}

// unquote reads a value in double quotes and returns what follows it.
func unquote(s string) (value, tail string, err error) {
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			return b.String(), s[i+1:], nil
		case '\\':
			i++
			if i == len(s) {
				return "", "", errors.New("a backslash at the end of the value")
			}
			switch s[i] {
			case '"', '\\':
				b.WriteByte(s[i])
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", "", fmt.Errorf("unknown escape \\%c", s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", "", errors.New("the closing quote is missing")
}

func clip(s string) string {
	if len(s) > 40 {
		return s[:40] + "..."
	}
	return s
}

// number reads a decimal or hexadecimal number.
func number(s string, bits int) (uint64, error) {
	if s == "" {
		return 0, errors.New("is empty")
	}
	v, err := strconv.ParseUint(s, 0, bits)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", clip(s))
	}
	return v, nil
}

// set stores one field of an injection.
func (in *Injection) set(key, value string) error {
	place := func(dst *model.Opt[int]) error {
		v, err := number(value, 31)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dst = model.Some(int(v))
		return nil
	}
	address := func(dst *model.Opt[uint64]) error {
		v, err := number(value, 64)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dst = model.Some(v)
		return nil
	}
	switch key {
	case "id":
		in.ID = value
	case "time":
		t, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return fmt.Errorf("time %q is not in the form 2026-11-15T05:14:11Z", clip(value))
		}
		in.Time = t.UTC()
	case "inject":
		in.Inject = value
	case "how":
		in.How = value
	case "severity":
		in.Severity = model.Severity(value)
	case "scrub":
		in.Scrub = model.ScrubType(value)
	case "count":
		v, err := number(value, 31)
		if err != nil {
			return fmt.Errorf("count: %w", err)
		}
		in.Count = int(v)
	case "address":
		return address(&in.Address)
	case "dpa":
		return address(&in.DPA)
	case "device_name":
		in.DeviceName = value
	case "socket":
		return place(&in.Socket)
	case "channel":
		return place(&in.Channel)
	case "slot":
		return place(&in.Slot)
	case "rank":
		return place(&in.Rank)
	case "device":
		return place(&in.Device)
	case "bank_group":
		return place(&in.BankGroup)
	case "bank":
		return place(&in.Bank)
	case "row":
		return place(&in.Row)
	case "column":
		return place(&in.Column)
	case "bit":
		return place(&in.Bit)
	case "label":
		in.Label = value
	case "note":
		in.Note = value
	default:
		return fmt.Errorf("%q is not a field of an injection", key)
	}
	return nil
}

func (in *Injection) validate() error {
	if in.ID == "" {
		return errors.New("it has no id")
	}
	if in.DPA.Valid && in.DeviceName == "" {
		return errors.New("an address within a device (dpa) needs the device (device_name)")
	}
	switch in.Inject {
	case InjectError:
		if !in.Severity.Valid() {
			return fmt.Errorf("severity %q is none of Informational, CE, Recoverable, UE, Fatal", in.Severity)
		}
		if !in.Scrub.Valid() {
			return fmt.Errorf("scrub %q is none of Demand, Patrol, Other", in.Scrub)
		}
		if in.How != "" {
			return errors.New("\"how\" belongs to a page offline")
		}
		if !in.Address.Valid && !in.Row.Valid && in.Label == "" && in.DeviceName == "" {
			return errors.New("an error needs an address, a row, a label or a device to be found by")
		}
	case InjectOffline:
		if in.How != "hard" && in.How != "soft" {
			return fmt.Errorf("how %q is neither hard nor soft", in.How)
		}
		if !in.Address.Valid {
			return errors.New("a page offline needs the address of the page")
		}
		if in.Count > 1 {
			return errors.New("a page is offline or not: count is 0 or 1")
		}
		if in.Severity != "" || in.Scrub != "" {
			return errors.New("severity and scrub belong to an error")
		}
	case "":
		return errors.New("it does not say what was injected (inject: error or page-offline)")
	default:
		return fmt.Errorf("inject %q is neither error nor page-offline", in.Inject)
	}
	return nil
}
