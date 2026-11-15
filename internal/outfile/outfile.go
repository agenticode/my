// Package outfile writes the files the tool hands to its user: a case
// bundle, a report, a figure.
//
// A regular file is written beside its target and moved into place, so
// that a file found under the name is whole. Anything else that exists
// under the name (a device such as /dev/null, a pipe, a socket) is written
// to and never replaced: a file moved onto /dev/null by root would take the
// device away from every program of the host.
package outfile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// What Check and Write report about a target. They come wrapped in a
// *PathError that names the path they are about.
var (
	// ErrExists: a file is there and the caller did not ask to replace it.
	ErrExists = errors.New("the file exists")
	// ErrNoDir: the directory the file would go into is not there.
	ErrNoDir = errors.New("the directory does not exist")
	// ErrIsDir: the name belongs to a directory.
	ErrIsDir = errors.New("is a directory")
	// ErrNotFile: the name belongs to something a ledger cannot live in.
	ErrNotFile = errors.New("not a regular file")
	// ErrDenied: this user may not write into the directory.
	ErrDenied = errors.New("the directory is not writable for this user")
)

// PathError is an error about one path.
type PathError struct {
	Path string
	Err  error
}

func (e *PathError) Error() string { return e.Path + ": " + e.Err.Error() }
func (e *PathError) Unwrap() error { return e.Err }

func pathErr(path string, err error) error { return &PathError{Path: path, Err: err} }

// target is a checked place to write to.
type target struct {
	// path is the file to write: the name the caller gave, or what a
	// symbolic link under that name leads to.
	path string
	// through is set when path is not a regular file and has to be written
	// to as it is.
	through bool
}

// resolve looks at path and says how to write it.
func resolve(path string, force bool) (target, error) {
	t := target{path: path}
	if li, err := os.Lstat(path); err == nil && li.Mode()&fs.ModeSymlink != 0 {
		// A link is followed and stays a link: what it leads to is written.
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			return t, pathErr(path, fmt.Errorf("a symbolic link that leads nowhere: %w", errors.Unwrap(err)))
		}
		t.path = real
	}
	fi, err := os.Stat(t.path)
	switch {
	case err == nil && fi.IsDir():
		return t, pathErr(path, ErrIsDir)
	case err == nil && !fi.Mode().IsRegular():
		t.through = true
		return t, nil
	case err == nil && !force:
		return t, pathErr(path, ErrExists)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return t, err
	}
	dir := filepath.Dir(t.path)
	di, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist), err == nil && !di.IsDir():
		return t, pathErr(dir, ErrNoDir)
	case err != nil:
		return t, err
	case !writable(dir):
		return t, pathErr(dir, ErrDenied)
	}
	return t, nil
}

// Check reports why path cannot be written, before any work is done on
// what would go there: a file that exists and is not to be replaced, a
// directory that is missing or closed to this user.
func Check(path string, force bool) error {
	_, err := resolve(path, force)
	return err
}

// CheckLedger reports why path cannot hold a ledger that is kept between
// runs: a ledger is a database file with files of its own beside it, so it
// has to be a regular file, or a name that is free in a directory that
// exists.
func CheckLedger(path string) error {
	t, err := resolve(path, true)
	if err != nil {
		return err
	}
	if t.through {
		return pathErr(path, ErrNotFile)
	}
	return nil
}

// counter counts what passes through it.
type counter struct {
	w io.Writer
	n int64
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// Write writes what fn produces to path and returns its size. With force
// a file that exists is replaced. The file gets the permissions new files
// of this user get (the umask applies).
func Write(path string, force bool, fn func(io.Writer) error) (int64, error) {
	t, err := resolve(path, force)
	if err != nil {
		return 0, err
	}
	if t.through {
		f, err := os.OpenFile(t.path, os.O_WRONLY, 0)
		if err != nil {
			return 0, err
		}
		c := &counter{w: f}
		err = fn(c)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return c.n, err
	}

	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return 0, err
	}
	partial := filepath.Join(filepath.Dir(t.path), "."+filepath.Base(t.path)+"."+hex.EncodeToString(rnd[:])+".part")
	f, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return 0, err
	}
	c := &counter{w: f}
	err = fn(c)
	if err == nil {
		// On disk before it takes the name: a host that goes down right
		// after must not be left with an empty file under it.
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(partial, t.path)
	}
	if err != nil {
		os.Remove(partial)
		return 0, err
	}
	return c.n, nil
}
