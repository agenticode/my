package adapter

import (
	"bytes"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestReadFile(t *testing.T) {
	fsys := fstest.MapFS{
		"small":   {Data: []byte("7\n")},
		"exact":   {Data: bytes.Repeat([]byte("x"), 16)},
		"large":   {Data: bytes.Repeat([]byte("x"), 17)},
		"dir/one": {Data: []byte("1")},
	}
	if b, err := ReadFile(fsys, "small", 16); err != nil || string(b) != "7\n" {
		t.Errorf("small: %q, %v", b, err)
	}
	if b, err := ReadFile(fsys, "exact", 16); err != nil || len(b) != 16 {
		t.Errorf("a file of exactly the limit: %d bytes, %v", len(b), err)
	}
	// A file beyond the limit is refused, and not kept in memory.
	b, err := ReadFile(fsys, "large", 16)
	var large *TooLargeError
	if b != nil || !errors.As(err, &large) || large.Name != "large" || large.Limit != 16 || err.Error() != "large is larger than 16 bytes" {
		t.Errorf("large: %q, %v", b, err)
	}
	if _, err := ReadFile(fsys, "none", 16); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
	if _, err := ReadFile(fsys, "dir", 16); err == nil {
		t.Error("a directory was read as a file")
	}
}
