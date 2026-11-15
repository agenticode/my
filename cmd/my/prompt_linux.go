package main

import "golang.org/x/sys/unix"

// The requests that read and set the settings of a terminal.
const (
	termiosGet = unix.TCGETS
	termiosSet = unix.TCSETS
)
