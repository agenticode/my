//go:build unix

package outfile

import "golang.org/x/sys/unix"

// writable reports whether this user may create files in dir.
func writable(dir string) bool {
	return unix.Access(dir, unix.W_OK|unix.X_OK) == nil
}
