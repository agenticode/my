package klog

import (
	"bufio"
	"encoding/gob"
	"os"
	"time"

	"github.com/agenticode/my/internal/model"
)

// maxWaitingInMemory is how many records wait in memory for the time of
// their boot. Records beyond that wait in a temporary file.
const maxWaitingInMemory = 4096

// waiting holds the records of a boot whose start is not settled yet, in
// the order they were read. Lines of dmesg carry seconds since boot only,
// and what dates them (the capture time less the last stamp, or the boot
// time the caller gave for the last boot of the input) is known when the
// boot ends in the input. A log of one boot with a million error lines
// would otherwise sit in memory whole.
type waiting struct {
	// dir is where the temporary file is made; "" means the system's
	// directory for temporary files.
	dir string
	mem []pending
	// The records beyond the first maxWaitingInMemory.
	f     *os.File
	w     *bufio.Writer
	enc   *gob.Encoder
	count int
}

// waitError is a failure of the temporary file: the log itself could be
// read, the place to keep its records could not be used.
type waitError struct{ err error }

func (e waitError) Error() string {
	return "the records of a boot wait for its time in a temporary file, which failed: " + e.err.Error()
}

// stored is a pending record in the form it has in the file.
type stored struct {
	Event   *model.Event
	Action  *model.Action
	GPU     *model.GPUMem
	Mono    time.Duration
	HasMono bool
}

// add appends a record.
func (q *waiting) add(pd pending) error {
	if q.f == nil && len(q.mem) < maxWaitingInMemory {
		q.mem = append(q.mem, pd)
		return nil
	}
	if q.f == nil {
		f, err := os.CreateTemp(q.dir, "my-klog-")
		if err != nil {
			return waitError{err}
		}
		// The file has no name from here on: it is gone when it is
		// closed, however the run ends.
		os.Remove(f.Name())
		q.f, q.w = f, bufio.NewWriterSize(f, 1<<20)
		q.enc = gob.NewEncoder(q.w)
	}
	if err := q.enc.Encode(stored{pd.ev, pd.act, pd.gpu, pd.mono, pd.hasMono}); err != nil {
		return waitError{err}
	}
	q.count++
	return nil
}

// each hands every record to fn, in the order it was added, and empties
// the queue.
func (q *waiting) each(fn func(pending) error) error {
	defer q.reset()
	for _, pd := range q.mem {
		if err := fn(pd); err != nil {
			return err
		}
	}
	if q.f == nil {
		return nil
	}
	if err := q.w.Flush(); err != nil {
		return waitError{err}
	}
	if _, err := q.f.Seek(0, 0); err != nil {
		return waitError{err}
	}
	dec := gob.NewDecoder(bufio.NewReaderSize(q.f, 1<<20))
	for range q.count {
		var s stored
		if err := dec.Decode(&s); err != nil {
			return waitError{err}
		}
		if err := fn(pending{ev: s.Event, act: s.Action, gpu: s.GPU, mono: s.Mono, hasMono: s.HasMono}); err != nil {
			return err
		}
	}
	return nil
}

// reset drops what the queue holds.
func (q *waiting) reset() {
	q.mem = nil
	if q.f != nil {
		q.f.Close()
	}
	q.f, q.w, q.enc, q.count = nil, nil, nil, 0
}
