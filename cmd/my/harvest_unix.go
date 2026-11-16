//go:build linux || darwin

package main

import (
	"errors"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// lockFile takes an exclusive lock (flock(2)) on the file path + ".lock"
// beside the ledger, which it creates when it is not there. held reports
// that another process has the lock. The ledger file itself is not used:
// SQLite locks it with fcntl(2), and on BSD systems flock and fcntl locks
// of one file are the same lock.
func lockFile(path string) (unlock func(), held bool, err error) {
	f, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, false, nil
}

// freeSpace returns what a user without privileges may still write on the
// file system of path (statfs(2), f_bavail).
func freeSpace(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// fileOwner returns the user ID that owns a file.
func fileOwner(fi fs.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}
