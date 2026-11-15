//go:build unix

package term

import (
	"os"

	"golang.org/x/sys/unix"
)

// Size reports whether f is a terminal and, if so, how many columns it
// has. A file, a pipe and a closed stream are not terminals.
func Size(f *os.File) (columns int, terminal bool) {
	if f == nil {
		return 0, false
	}
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, false
	}
	return int(ws.Col), true
}
