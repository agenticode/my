//go:build unix

package scan

import (
	"errors"
	"syscall"
)

func openKmsg(path string) (int, error) {
	// Without O_NONBLOCK a read at the end of the buffer waits for the
	// next kernel message.
	return syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
}

func closeKmsg(fd int) { _ = syscall.Close(fd) }

// readKmsg reads the records in the ring buffer. Each read returns one
// record. lost counts the times the kernel reported that records were
// overwritten before they could be read.
func readKmsg(path string, limit int) (text []byte, lost int, err error) {
	fd, err := openKmsg(path)
	if err != nil {
		return nil, 0, err
	}
	defer closeKmsg(fd)
	buf := make([]byte, 64<<10)
	for len(text) < limit {
		n, err := syscall.Read(fd, buf)
		switch {
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EPIPE):
			lost++ // the reader was overtaken: the next read continues
			continue
		case errors.Is(err, syscall.EAGAIN):
			return text, lost, nil // the end of the buffer
		case err != nil:
			if len(text) > 0 {
				return text, lost, nil
			}
			return nil, lost, err
		case n == 0:
			return text, lost, nil // a plain file: end of file
		}
		text = records(text, buf[:n])
	}
	return text, lost, nil
}
