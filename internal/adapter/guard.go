package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/agenticode/my/internal/model"
)

// A fault of a reader stays inside it. A record whose reading fails or
// panics is skipped and the next one is read (Each). A reader that panics
// loses its own source and nothing else (Safely). A sniffer that panics
// does not recognise the input (Sniffs). What happened is counted and
// told in the source inventory.
//
// MY_DEBUG=panic lets a panic through, for a developer who wants the
// whole stack.

// Bounds every reader keeps (design section 9.3 of V1).
const (
	// MaxRecords is how many records of one source are read; the rest is
	// counted and left out, and the source is read in part.
	MaxRecords = 1_000_000
	// MaxRecordBytes is the size of the largest record that is read.
	MaxRecordBytes = 1 << 20
	// MaxArgs is how many arguments of a message are read.
	MaxArgs = 32
)

// DebugPanics reports whether panics are let through.
func DebugPanics() bool {
	for _, w := range strings.Split(os.Getenv("MY_DEBUG"), ",") {
		if strings.TrimSpace(w) == "panic" {
			return true
		}
	}
	return false
}

// modulePrefix is the import path of this program; the frame of a panic
// that is reported is the first one inside it.
const modulePrefix = "github.com/agenticode/my/"

// where returns the first frame inside the program of the stack of a
// panic, as "package.Function (file.go:123)": no full stack, no absolute
// path.
func where() string {
	pcs := make([]uintptr, 64)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f, more := frames.Next()
		fn := f.Function
		if strings.HasPrefix(fn, modulePrefix) && !strings.HasPrefix(fn, modulePrefix+"internal/adapter.") {
			fn = strings.TrimPrefix(fn, modulePrefix)
			fn = fn[strings.LastIndex(fn, "/")+1:]
			return fmt.Sprintf("%s (%s:%d)", fn, path.Base(f.File), f.Line)
		}
		if !more {
			return "an unknown place"
		}
	}
}

// panicText is the value of a panic in at most 200 bytes of one line.
func panicText(v any) string {
	s := strings.Join(strings.Fields(fmt.Sprint(v)), " ")
	if len(s) > 200 {
		end := 197
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
		s = s[:end] + "..."
	}
	return s
}

// Safely runs a.Collect and turns a panic of the reader into a report: the
// source has the status error, Failed is set, and the detail names the
// panic and where it happened. The caller rolls back what the reader gave
// before it failed.
func Safely(ctx context.Context, a Adapter, sink Sink) (rep Report) {
	kind, origin := a.Kind(), a.Origin()
	if DebugPanics() {
		return a.Collect(ctx, sink)
	}
	defer func() {
		if v := recover(); v != nil {
			rep = Report{Kind: kind, Origin: origin, Status: model.SourceError, Failed: true, Faults: 1,
				Detail: fmt.Sprintf("the reader failed: %s at %s; nothing of this source was added", panicText(v), where())}
		}
	}()
	return a.Collect(ctx, sink)
}

// Sniffs runs a sniffer and takes a panic for "does not recognise".
func Sniffs(sniff func([]byte) bool, data []byte) (ok bool) {
	if DebugPanics() {
		return sniff(data)
	}
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	return sniff(data)
}

// Guard runs fn and takes a panic for its zero value.
func Guard[T any](fn func() T) (v T) {
	if DebugPanics() {
		return fn()
	}
	defer func() {
		if recover() != nil {
			var zero T
			v = zero
		}
	}()
	return fn()
}

// unreadable is the error of a record a reader could not use.
type unreadable struct{ reason string }

func (u *unreadable) Error() string { return u.reason }

// Unreadable returns an error that tells Each that the record could not be
// read, with the reason.
func Unreadable(format string, args ...any) error {
	return &unreadable{reason: fmt.Sprintf(format, args...)}
}

// Each runs the work on one record. A panic of fn, or an error made with
// Unreadable, skips the record: it is counted as not understood under the
// reason, and its text is kept when it names memory. A panic also counts
// as a fault. Any other error of fn is returned: it comes from the sink,
// and it stops the source.
func Each(t *Tally, ref, text string, fn func() error) (err error) {
	if DebugPanics() {
		err = fn()
	} else {
		func() {
			defer func() {
				if v := recover(); v != nil {
					t.faults++
					t.Unread(ref, "reading the record failed: "+panicText(v)+" at "+where(), text)
					err = nil
				}
			}()
			err = fn()
		}()
	}
	var u *unreadable
	if errors.As(err, &u) {
		t.Unread(ref, u.reason, text)
		return nil
	}
	return err
}

// memoryWords tells text that names memory, a DIMM or ECC.
var memoryWords = regexp.MustCompile(`(?i)memory|dimm|\becc\b|\bmem\d|\bdram\b|\bddr\d?\b`)

// NamesMemory reports whether text names memory, a DIMM or ECC. Only such
// text of a record that was not understood is kept.
func NamesMemory(text string) bool {
	if len(text) > model.MaxUnreadText*4 {
		text = text[:model.MaxUnreadText*4]
	}
	return memoryWords.MatchString(text)
}

// Unread records that the record at ref was not understood: it is skipped
// under the reason, counted, and its text is kept when it names memory, up
// to model.MaxUnreadKept records, each cut at model.MaxUnreadText bytes.
func (t *Tally) Unread(ref, reason, text string) {
	t.Skip(ref, reason)
	t.unread++
	if len(t.kept) >= model.MaxUnreadKept || !NamesMemory(text) {
		return
	}
	if len(text) > model.MaxUnreadText {
		end := model.MaxUnreadText - 3
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end] + "..."
	}
	t.kept = append(t.kept, model.UnreadRecord{Ref: ref, Reason: reason, Text: strings.ToValidUTF8(text, "�")})
}

// UnreadCount returns the number of records that were not understood, and
// Faults the number of records whose reading panicked.
func (t *Tally) UnreadCount() int { return t.unread }
func (t *Tally) Faults() int      { return t.faults }

// AddTally puts what a tally counted into the report: the skipped records
// with their reasons, the records not understood, the faults, and the kept
// texts.
func (r *Report) AddTally(t *Tally) {
	r.Skipped += t.Total()
	r.Notes = append(r.Notes, t.Notes()...)
	r.Unread += t.unread
	r.Faults += t.faults
	for _, k := range t.kept {
		if len(r.UnreadRecords) >= model.MaxUnreadKept {
			break
		}
		r.UnreadRecords = append(r.UnreadRecords, k)
	}
}
