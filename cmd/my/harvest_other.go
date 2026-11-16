//go:build !linux && !darwin

package main

import (
	"errors"
	"io/fs"
)

func lockFile(string) (func(), bool, error) { return func() {}, false, nil }

func freeSpace(string) (uint64, error) {
	return 0, errors.New("the free space of a file system is not known on this system")
}

func fileOwner(fs.FileInfo) (uint32, bool) { return 0, false }
