//go:build !linux && !darwin

package preflight

import "errors"

// freeBytes is not known on this system.
func freeBytes(string) (uint64, error) {
	return 0, errors.New("the free space of a file system is not known on this system")
}
