// Package bert reads the Boot Error Region: the error records firmware
// leaves for the next boot when it handled an error the operating system
// could not be told of, typically the one that reset the machine.
//
// The ACPI table BERT points at the region. Linux shows the region as
// /sys/firmware/acpi/tables/data/BERT (drivers/acpi/sysfs.c,
// acpi_bert_data_init), readable by root only, and prints up to five of its
// records at boot (drivers/acpi/apei/bert.c, bert_print_all), clearing the
// block status of each in the mapped region. The reader therefore does not
// stop at a cleared block status (cper.ParseStatusBlocks).
//
// Each memory error section becomes an error event. A section without a
// time stamp of its own is carried: the error happened in the boot before
// the one that read the region (store.LinkCarried puts it there). A fatal
// block without a memory error marks that boot as stopped through an
// Informational record. Sections of other types are counted in a note.
package bert

import (
	"context"
	"encoding/hex"

	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "bert"

// Path is where a host shows the region, below its root.
const Path = "sys/firmware/acpi/tables/data/BERT"

// maxRegion bounds what is read. The region is as long as the firmware
// declares in the BERT table; a few KiB to some MiB in practice.
const maxRegion = 64 << 20

// Options say what the region itself cannot.
type Options struct {
	// ReadAt is when the region was read: on a live host the time of the
	// scan, for a saved copy the time it was saved. It dates the records
	// to the boot that read them; zero takes the modification time of the
	// file.
	ReadAt time.Time
	// BootID is the kernel boot ID of the boot that read the region, when
	// known (a live host).
	BootID string
}

// Adapter reads one boot error region.
type Adapter struct {
	path string
	opts Options
}

// New returns an adapter for the region at path: the file of a live host
// or a saved copy of it.
func New(path string, opts Options) *Adapter { return &Adapter{path: path, opts: opts} }

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.path }

// Collect reads the region into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.path}
	f, err := os.Open(a.path)
	if err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		if rep.Status == model.SourceUnreadable {
			rep.Detail += "; the region is readable as root only"
		}
		return rep
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		return rep
	}
	region, err := io.ReadAll(io.LimitReader(f, maxRegion+1))
	if err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		return rep
	}
	if len(region) > maxRegion {
		rep.Status, rep.Detail = model.SourceError, (&adapter.TooLargeError{Name: a.path, Limit: maxRegion}).Error()
		return rep
	}
	readAt := a.opts.ReadAt
	if readAt.IsZero() {
		readAt = fi.ModTime()
	}
	readAt = readAt.UTC()

	blocks, stop := cper.ParseStatusBlocks(region)
	var tally adapter.Tally
	if stop != "" {
		tally.Skip(a.path, "the walk of the region stopped: "+stop)
	}
	other, cleared := 0, 0
	for bi, b := range blocks {
		if err := ctx.Err(); err != nil {
			rep.Status, rep.Detail = model.SourceError, err.Error()
			return rep
		}
		rep.Records++
		if b.BlockStatus == 0 {
			cleared++
		}
		mem := 0
		for ei, e := range b.Entries {
			if !e.Memory() {
				other++
				continue
			}
			mem++
			ev, err := a.event(b, bi, e, ei, readAt)
			if err != nil {
				tally.Skip(fmt.Sprintf("%s#block%d/entry%d", a.path, bi+1, ei+1), err.Error())
				continue
			}
			if err := sink.Event(ev); err != nil {
				rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
				return rep
			}
		}
		if mem == 0 && b.Severity == cper.SevFatal {
			// A fatal error the firmware handled that is no memory error:
			// the boot before still ended in it.
			ev := &model.Event{
				Kind: model.KindError, ErrorSeverity: model.SevInformational, ErrorType: model.ErrorTypeFatalBlock,
				ErrorDT: readAt, TimeQuality: model.TimeEstimated, BootID: a.opts.BootID, Carried: true,
				Source: Kind, Decoder: "cper", Count: 1, Grade: model.GradeC,
				RawRef: fmt.Sprintf("%s#block%d", a.path, bi+1), Raw: hexDump(b.Bytes),
				Fingerprint: model.Fingerprint(Kind, "block", hex.EncodeToString(b.Bytes)),
			}
			if err := sink.Event(ev); err != nil {
				rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
				return rep
			}
		}
	}
	rep.Skipped = tally.Total()
	rep.Notes = append(rep.Notes, fmt.Sprintf("%d error status %s in the region", len(blocks), plural(len(blocks), "block", "blocks")))
	if cleared > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("the kernel had printed %d of them at boot and cleared their block status", cleared))
	}
	if other > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d firmware error %s not about memory and left out", other, plural(other, "section is", "sections are")))
	}
	rep.Notes = append(rep.Notes, tally.Notes()...)
	switch {
	case len(blocks) == 0 && rep.Skipped == 0:
		rep.Status, rep.Detail = model.SourceEmpty, "the region holds no error record"
	case rep.Skipped > 0:
		rep.Status = model.SourcePartial
	default:
		rep.Status = model.SourceOK
	}
	return rep
}

// event makes the error event of a memory section.
func (a *Adapter) event(b cper.StatusBlock, bi int, e cper.DataEntry, ei int, readAt time.Time) (*model.Event, error) {
	m, err := cper.ParseSection(e.Data)
	if err != nil {
		return nil, err
	}
	sev, ok := cper.Severity(e.Severity)
	if !ok {
		if sev, ok = cper.Severity(b.Severity); !ok {
			return nil, fmt.Errorf("unknown severity %d", e.Severity)
		}
	}
	ev := &model.Event{
		Kind: model.KindError, ErrorSeverity: sev, Source: Kind, Decoder: "cper", Count: 1,
		RawRef: fmt.Sprintf("%s#block%d/entry%d", a.path, bi+1, ei+1), Raw: hexDump(e.Data),
		// The block status is left out of the bytes: the kernel clears it
		// after printing the block, and the record stays the same.
		Fingerprint: model.Fingerprint(Kind, hex.EncodeToString(b.Bytes), fmt.Sprint(ei)),
	}
	if e.FRUText != "" {
		ev.Label = e.FRUText
	}
	m.Apply(ev)
	if !e.Time.IsZero() {
		// The entry says when the error happened. The zone of the time
		// stamp is not stated by the specification: estimated.
		ev.ErrorDT, ev.TimeQuality = e.Time, model.TimeEstimated
	} else {
		// Kept over the reset: dated by the read, moved to the boot
		// before by store.LinkCarried.
		ev.ErrorDT, ev.TimeQuality, ev.Carried, ev.BootID = readAt, model.TimeEstimated, true, a.opts.BootID
	}
	ev.Grade = ev.ComputeGrade()
	return ev, nil
}

// hexDump writes bytes as lines of 32 hexadecimal digits, at most 4 KiB.
func hexDump(b []byte) string {
	if len(b) > 4096 {
		b = b[:4096]
	}
	var lines []string
	for i := 0; i < len(b); i += 16 {
		lines = append(lines, hex.EncodeToString(b[i:min(i+16, len(b))]))
	}
	return strings.Join(lines, "\n")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Sniff reports whether data looks like a boot error region: one or more
// status blocks whose entries validate.
func Sniff(data []byte) bool {
	blocks, _ := cper.ParseStatusBlocks(data)
	return len(blocks) > 0
}
