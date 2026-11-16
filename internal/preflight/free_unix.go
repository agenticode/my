//go:build linux || darwin

package preflight

import "golang.org/x/sys/unix"

// freeBytes returns the space a user without privileges may still write
// on the file system that holds path (statfs(2), f_bavail).
func freeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
