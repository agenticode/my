package scan

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/model"
)

// journal reads the kernel messages of the systemd journal: every boot the
// journal still holds, not only the running one. That is the history a
// host without rasdaemon has.
type journal struct {
	prog    string
	timeout time.Duration
	opts    klog.Options
}

// journalMatch selects kernel messages of all boots. The -k option would
// limit the output to the running boot.
const journalMatch = "_TRANSPORT=kernel"

func (j *journal) Kind() string   { return klog.Kind }
func (j *journal) Origin() string { return "journalctl -o json " + journalMatch }

// deniedHint is what journalctl prints when the user may not read the
// system journal.
const deniedHint = "not seeing messages from other users"

const journalDenied = "the journal shows this user no kernel messages; they are readable as root or as a member of the group systemd-journal"

// limited keeps the first bytes written to it.
type limited struct {
	buf bytes.Buffer
	max int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		l.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// firstLine returns the first line of a program's error output.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

// probe asks the journal for its latest kernel message, to learn whether
// reading it is worth starting.
func (j *journal) probe(ctx context.Context) (model.SourceStatus, string) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, j.prog, "--no-pager", "-o", "json", "-n", "1", journalMatch)
	var stdout, stderr limited
	stdout.max, stderr.max = 1<<20, 16<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	entry := strings.HasPrefix(strings.TrimSpace(stdout.buf.String()), "{")
	switch {
	case entry:
		return model.SourceOK, ""
	case strings.Contains(stderr.buf.String(), deniedHint):
		return model.SourceUnreadable, journalDenied
	case err != nil:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return model.SourceError, "journalctl: " + firstLine(stderr.buf.String())
		}
		return model.SourceAbsent, err.Error()
	}
	return model.SourceAbsent, "the journal holds no kernel messages"
}

// pass runs journalctl once and reads its output into the sink.
func (j *journal) pass(ctx context.Context, sink adapter.Sink, timeout time.Duration, args ...string) (rep adapter.Report, timedOut bool) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, j.prog, append([]string{"--no-pager", "-o", "json"}, args...)...)
	var stderr limited
	stderr.max = 16 << 10
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		rep = adapter.Report{Kind: klog.Kind, Origin: j.Origin()}
		rep.Status, rep.Detail = model.SourceError, "journalctl: "+err.Error()
		return rep, false
	}
	rep = klog.NewReader(bufio.NewReaderSize(stdout, 1<<20), j.Origin(), j.opts).Collect(ctx, sink)
	// Whatever the reader left unread must not block the program.
	_, _ = io.Copy(io.Discard, stdout)
	werr := cmd.Wait()
	timedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	switch {
	case rep.SinkErr != nil, timedOut:
	case werr != nil && rep.Records == 0:
		rep.Status, rep.Detail = model.SourceError, "journalctl: "+firstLine(stderr.buf.String())
		if strings.Contains(stderr.buf.String(), deniedHint) {
			rep.Status, rep.Detail = model.SourceUnreadable, journalDenied
		}
	case werr != nil:
		rep.Status = model.SourcePartial
		rep.Notes = append(rep.Notes, "journalctl ended with an error: "+firstLine(stderr.buf.String()))
	}
	return rep, timedOut
}

// Collect reads the kernel messages of every boot in the journal.
func (j *journal) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	timeout := j.timeout
	if timeout <= 0 {
		timeout = DefaultJournalTimeout
	}
	rep, timedOut := j.pass(ctx, sink, timeout, journalMatch)
	if !timedOut || rep.SinkErr != nil || ctx.Err() != nil {
		return rep
	}
	// The journal is read oldest boot first. When time ran out the running
	// boot was not reached, and it is the one that matters most: it is
	// read by itself. Records of both passes are stored once.
	now, _ := j.pass(ctx, sink, time.Minute, "-b", "0", journalMatch)
	rep.Records += now.Records
	rep.Skipped += now.Skipped
	rep.Status = model.SourcePartial
	rep.Detail = fmt.Sprintf("reading the journal took longer than %s and was stopped; the running boot was read in full, older boots in part", timeout)
	rep.Notes = append(rep.Notes, now.Notes...)
	if now.SinkErr != nil {
		rep.SinkErr, rep.Status, rep.Detail = now.SinkErr, now.Status, now.Detail
	}
	return rep
}
