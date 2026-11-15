package scan

import (
	"bytes"
	"context"
	"fmt"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/model"
)

// kmsg reads the kernel ring buffer of the running boot from /dev/kmsg.
// It is what dmesg prints. The buffer has a fixed size, so messages from
// early in a long boot may be gone.
type kmsg struct {
	path string
	opts klog.Options
}

func (k *kmsg) Kind() string   { return klog.Kind }
func (k *kmsg) Origin() string { return k.path }

// maxKmsg bounds what is read from the ring buffer.
const maxKmsg = 64 << 20

// probe reports whether the ring buffer can be opened.
func (k *kmsg) probe() (model.SourceStatus, string) {
	fd, err := openKmsg(k.path)
	if err != nil {
		status, detail := adapter.StatusForError(err)
		if status == model.SourceUnreadable {
			detail = "permission denied; the kernel ring buffer is readable as root (kernel.dmesg_restrict is set)"
		}
		return status, detail
	}
	closeKmsg(fd)
	return model.SourceOK, ""
}

// Collect reads the records that are in the ring buffer now.
func (k *kmsg) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	text, lost, err := readKmsg(k.path, maxKmsg)
	if err != nil {
		rep := adapter.Report{Kind: klog.Kind, Origin: k.path}
		rep.Status, rep.Detail = adapter.StatusForError(err)
		return rep
	}
	rep := klog.NewReader(bytes.NewReader(text), k.path, k.opts).Collect(ctx, sink)
	if lost > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("the ring buffer was overwritten %d times while it was read; some messages are missing", lost))
		if rep.Status == model.SourceOK {
			rep.Status = model.SourcePartial
		}
	}
	return rep
}

// records turns what read returned for one record into the lines to keep.
// A record is a line "prio,seq,usec,flags;message", followed by lines that
// start with a space and hold key=value pairs about the device; those are
// not messages.
func records(dst, rec []byte) []byte {
	for _, line := range bytes.Split(rec, []byte{'\n'}) {
		if len(line) == 0 || line[0] == ' ' {
			continue
		}
		dst = append(dst, line...)
		dst = append(dst, '\n')
	}
	return dst
}
