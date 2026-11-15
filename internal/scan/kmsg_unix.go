//go:build unix

package scan

import (
	"bytes"
	"errors"
	"syscall"
)

func openKmsg(path string) (int, error) {
	// Without O_NONBLOCK a read at the end of the buffer waits for the
	// next kernel message.
	return syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
}

func closeKmsg(fd int) { _ = syscall.Close(fd) }

// readKmsg reads the records in the ring buffer. The device returns one
// whole record per read. A plain file, which stands in for the device in a
// copy of a host and in tests, returns as much as fits the buffer, so a
// read can end in the middle of a line: what follows the last line end is
// kept for the next read. lost counts the times the kernel reported that
// records were overwritten before they could be read.
func readKmsg(path string, limit int) (text []byte, lost int, err error) {
	fd, err := openKmsg(path)
	if err != nil {
		return nil, 0, err
	}
	defer closeKmsg(fd)
	buf := make([]byte, 64<<10)
	var rest []byte
	for len(text) < limit {
		n, err := syscall.Read(fd, buf)
		switch {
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EPIPE):
			lost++ // the reader was overtaken: the next read continues
			continue
		case errors.Is(err, syscall.EAGAIN):
			return records(text, rest), lost, nil // the end of the buffer
		case err != nil:
			if text = records(text, rest); len(text) > 0 {
				return text, lost, nil
			}
			return nil, lost, err
		case n == 0:
			return records(text, rest), lost, nil // a plain file: end of file
		}
		rest = append(rest, buf[:n]...)
		if end := bytes.LastIndexByte(rest, '\n'); end >= 0 {
			text = records(text, rest[:end+1])
			rest = append(rest[:0], rest[end+1:]...)
		}
	}
	return records(text, rest), lost, nil
}
