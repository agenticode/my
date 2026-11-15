package adapter

import (
	"fmt"
	"io"
	"io/fs"
)

// TooLargeError says that a file holds more than a reader takes.
type TooLargeError struct {
	Name  string
	Limit int64
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("%s is larger than %d bytes", e.Name, e.Limit)
}

// ReadFile returns the content of a file of fsys that is expected to be
// small: an attribute of sysfs, a file of /proc, a firmware table. A file
// with more than limit bytes is not read into memory; the error is then a
// *TooLargeError. On a live host such files have the size the kernel gives
// them. In a copy of a host's files they are whatever was put there.
func ReadFile(fsys fs.FS, name string, limit int64) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: name, Err: err}
	}
	if int64(len(b)) > limit {
		return nil, &TooLargeError{Name: name, Limit: limit}
	}
	return b, nil
}
