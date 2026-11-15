package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Options say how an analyzer is run.
type Options struct {
	// Path is the program. It is run as it is, without a shell and without
	// arguments.
	Path string
	// Timeout bounds one run; zero means DefaultTimeout.
	Timeout time.Duration
}

// DefaultTimeout bounds a run of an analyzer.
const DefaultTimeout = 10 * time.Second

// DefaultPreviousLogs is how many records before the newest one a request
// carries per DIMM unless the caller says otherwise.
const DefaultPreviousLogs = 1000

// Error says why an analyzer could not be used. The run that asked for
// the analyzer stops on it: a finding that silently lacks what the caller
// asked for would be worse than no finding.
type Error struct {
	// Path is the program.
	Path string
	// Reason says what went wrong.
	Reason string
}

func (e *Error) Error() string { return "vendor analyzer " + e.Path + ": " + e.Reason }

// Result is a response that passed every check.
type Result struct {
	// Path is the program that answered.
	Path     string
	Analyzer Analyzer
	// DIMMs holds the analyses by the DimmId of the request.
	DIMMs map[string]*ResponseDIMM
	// Notes are remarks about the response that are no errors: fields this
	// version does not know, a wish for more previous records.
	Notes []string
	// Took is how long the analyzer ran.
	Took time.Duration

	adjacent *adjacency
}

// limited is a writer that keeps the first max bytes and counts the rest.
type limited struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room >= len(p) {
		l.buf.Write(p)
	} else {
		if room > 0 {
			l.buf.Write(p[:room])
		}
		l.over = true
	}
	return len(p), nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 300 {
				l = l[:300] + "..."
			}
			return strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return ' '
				}
				return r
			}, l)
		}
	}
	return ""
}

// Run sends a request to an analyzer and returns its checked response.
func Run(ctx context.Context, opts Options, req *Request) (*Result, error) {
	fail := func(format string, a ...any) (*Result, error) {
		return nil, &Error{Path: opts.Path, Reason: fmt.Sprintf(format, a...)}
	}
	if opts.Path == "" {
		return fail("no program named")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	in, err := json.Marshal(req)
	if err != nil {
		return fail("the request could not be written: %v", err)
	}

	run, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(run, opts.Path)
	cmd.Stdin = bytes.NewReader(in)
	var out, errOut limited
	out.max, errOut.max = MaxResponseBytes, 4<<10
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.Env = append(os.Environ(), "MY_ANALYZER_CONTRACT="+Contract)
	// A child of the analyzer that keeps the output open must not hold up
	// the run once the analyzer was stopped.
	cmd.WaitDelay = time.Second
	start := time.Now()
	err = cmd.Run()
	took := time.Since(start)
	switch {
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case run.Err() != nil:
		return fail("no answer within %s; --plugin-timeout sets the limit", timeout)
	case err != nil:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			reason := fmt.Sprintf("failed (%s)", exit.ProcessState.String())
			if line := firstLine(errOut.buf.String()); line != "" {
				reason += ": " + line
			}
			return fail("%s", reason)
		}
		return fail("cannot be run: %v", err)
	case out.over:
		return fail("the response is larger than %d MiB", MaxResponseBytes>>20)
	}
	resp, notes, err := parse(out.buf.Bytes())
	if err != nil {
		return fail("the response is not readable: %v", err)
	}
	res, err := check(req, resp)
	if err != nil {
		return fail("the response is not valid: %v", err)
	}
	res.Path, res.Took = opts.Path, took
	res.Notes = append(notes, res.Notes...)
	return res, nil
}

// parse reads a response: one JSON document and nothing after it. It
// returns, as notes, the fields this version does not know.
func parse(data []byte) (*Response, []string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil, errors.New("the analyzer wrote nothing to its standard output")
	}
	if !utf8.Valid(data) {
		return nil, nil, errors.New("it is not UTF-8")
	}
	var resp Response
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&resp); err != nil {
		return nil, nil, fmt.Errorf("not JSON in the form of the contract: %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, nil, errors.New("text after the end of the JSON document")
	}

	// Fields beyond the contract are allowed, and named: a field that was
	// misspelt would otherwise be dropped without a word.
	var raw struct {
		Dimms []map[string]json.RawMessage `json:"Dimms"`
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(data, &top)
	_ = json.Unmarshal(data, &raw)
	unknown := map[string]bool{}
	for k := range top {
		switch k {
		case "Contract", "Analyzer", "Dimms":
		default:
			unknown[k] = true
		}
	}
	for _, d := range raw.Dimms {
		for k := range d {
			switch k {
			case "DimmId", "Level2", "AdjacentRows", "Baseline":
			default:
				unknown["Dimms."+k] = true
			}
		}
	}
	var notes []string
	if len(unknown) > 0 {
		// The names are the analyzer's own text: they are shown clipped
		// and without anything a terminal would act on.
		names := make([]string, 0, len(unknown))
		for k := range unknown {
			names = append(names, clip(k))
		}
		sort.Strings(names)
		names = slices.Compact(names)
		if len(names) > 8 {
			names = append(names[:8], "...")
		}
		notes = append(notes, "the response has fields this version does not read: "+strings.Join(names, ", "))
	}
	return &resp, notes, nil
}

// unsafe reports whether a character changes what a terminal or a report
// shows beyond its own place: a control character, a line or paragraph
// separator, an override of the writing direction, or the mark of a broken
// encoding.
func unsafe(r rune) bool {
	switch {
	case unicode.IsControl(r), r == utf8.RuneError:
		return true
	case r == 0x2028, r == 0x2029:
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// text checks a text of a response: its length, and that it holds nothing
// a terminal or a report would act on.
func text(what, s string, limit int, required bool) error {
	switch {
	case required && strings.TrimSpace(s) == "":
		return fmt.Errorf("%s is empty", what)
	case len(s) > limit:
		return fmt.Errorf("%s is longer than %d bytes", what, limit)
	}
	for _, r := range s {
		switch {
		case unicode.IsControl(r) || r == utf8.RuneError:
			return fmt.Errorf("%s holds a control character", what)
		case unsafe(r):
			return fmt.Errorf("%s holds a character that breaks the line or turns the writing direction (%U)", what, r)
		}
	}
	return nil
}

// check holds a response against its request and builds the result.
func check(req *Request, resp *Response) (*Result, error) {
	if resp.Contract != Contract {
		return nil, fmt.Errorf("it names the contract %q; this version speaks %q", clip(resp.Contract), Contract)
	}
	for _, f := range []struct{ what, s string }{{"the name of the analyzer", resp.Analyzer.Name}, {"the version of the analyzer", resp.Analyzer.Version}} {
		if err := text(f.what, f.s, MaxName, true); err != nil {
			return nil, err
		}
	}
	if err := text("the vendor of the analyzer", resp.Analyzer.Vendor, MaxName, false); err != nil {
		return nil, err
	}
	if resp.Analyzer.PreviousLogs < 0 {
		return nil, errors.New("Analyzer.PreviousLogs is negative")
	}

	asked := map[string]*RequestDIMM{}
	for i := range req.Dimms {
		asked[req.Dimms[i].DimmId] = &req.Dimms[i]
	}
	res := &Result{Analyzer: resp.Analyzer, DIMMs: map[string]*ResponseDIMM{}, adjacent: &adjacency{rows: map[rowKey][]int{}, reverse: map[rowKey][]int{}}}
	short := 0
	for i := range resp.Dimms {
		d := &resp.Dimms[i]
		where := fmt.Sprintf("Dimms[%d]", i)
		rd, ok := asked[d.DimmId]
		switch {
		case !ok:
			return nil, fmt.Errorf("%s names the DIMM %q, which the request does not hold", where, clip(d.DimmId))
		case res.DIMMs[d.DimmId] != nil:
			return nil, fmt.Errorf("%s is a second analysis of the DIMM %q", where, d.DimmId)
		}
		if l := d.Level2; l != nil {
			if len(l.Lines) > MaxLines || len(l.Notes) > MaxNotes {
				return nil, fmt.Errorf("%s.Level2 has %d lines and %d notes; the limits are %d and %d", where, len(l.Lines), len(l.Notes), MaxLines, MaxNotes)
			}
			for j, line := range l.Lines {
				if err := text(fmt.Sprintf("%s.Level2.Lines[%d].Label", where, j), line.Label, MaxLabel, true); err != nil {
					return nil, err
				}
				if err := text(fmt.Sprintf("%s.Level2.Lines[%d].Value", where, j), line.Value, MaxValue, false); err != nil {
					return nil, err
				}
			}
			for j, n := range l.Notes {
				if err := text(fmt.Sprintf("%s.Level2.Notes[%d]", where, j), n, MaxNote, true); err != nil {
					return nil, err
				}
			}
		}
		if b := d.Baseline; b != nil && (math.IsNaN(b.Percentile) || b.Percentile < 0 || b.Percentile > 100) {
			return nil, fmt.Errorf("%s.Baseline.Percentile is %v; a percentile is a number from 0 to 100", where, b.Percentile)
		}
		if err := res.adjacent.add(d.DimmId, where, d.AdjacentRows); err != nil {
			return nil, err
		}
		res.DIMMs[d.DimmId] = d
		if resp.Analyzer.PreviousLogs > len(rd.PreviousLogs) && rd.EarlierLogs > 0 {
			short++
		}
	}
	if err := res.adjacent.symmetric(); err != nil {
		return nil, err
	}
	if short > 0 {
		res.Notes = append(res.Notes, fmt.Sprintf("the analyzer works with %d previous records per DIMM; %d were sent, and %d DIMMs have more on record (--plugin-logs sends more)",
			resp.Analyzer.PreviousLogs, req.PreviousLogs, short))
	}
	return res, nil
}

// clip makes a text of a response fit for a message: its first 40
// characters, with a question mark for every character that is unsafe.
func clip(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n == 40 {
			b.WriteString("...")
			break
		}
		if unsafe(r) {
			r = '?'
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}
