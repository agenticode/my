//go:build !unix

package scan

import "io/fs"

func openKmsg(string) (int, error) { return 0, fs.ErrNotExist }

func closeKmsg(int) {}

func readKmsg(string, int) ([]byte, int, error) { return nil, 0, fs.ErrNotExist }
