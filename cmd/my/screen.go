package main

import (
	"io"
	"os"

	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/term"
)

// screen is the standard output or the standard error of the process when
// it is a terminal. What is written to it goes through term.Safe: the
// commands print text that came from logs, from a BMC or out of a case
// someone sent, and a terminal would act on an escape sequence in it. The
// file is kept for the questions about the terminal itself: whether it is
// one, and how wide it is.
type screen struct {
	io.Writer
	file *os.File
}

// safeStreams puts a screen in the place of each output stream of env that
// is a terminal. Output to a file or a pipe is left as it is: it is not
// for a terminal, and a bundle or a report written to standard output is
// not text.
func safeStreams(env *cli.Env, terminal func(*os.File) bool) {
	for _, w := range []*io.Writer{&env.Stdout, &env.Stderr} {
		if f, ok := (*w).(*os.File); ok && terminal(f) {
			*w = &screen{Writer: term.Safe(f), file: f}
		}
	}
}

// isTerminal reports whether a file is a terminal.
func isTerminal(f *os.File) bool {
	_, terminal := term.Size(f)
	return terminal
}

// fileOf returns the file behind an output stream, or nil when the stream
// is something else: a buffer of a test, for one.
func fileOf(w io.Writer) *os.File {
	switch x := w.(type) {
	case *os.File:
		return x
	case *screen:
		return x.file
	}
	return nil
}
