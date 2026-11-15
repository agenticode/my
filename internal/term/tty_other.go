//go:build !unix

package term

import "os"

// Size reports whether f is a terminal and how many columns it has. On
// this platform the output is always treated as plain.
func Size(*os.File) (columns int, terminal bool) { return 0, false }
