//go:build linux || darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/agenticode/my/internal/cli"
)

// promptPassword asks for a password on the terminal and reads it without
// showing it. It fails when the standard input is no terminal: a command
// in a script takes the password from the environment or from a file.
func promptPassword(ctx context.Context, env *cli.Env, prompt string) (string, error) {
	in, ok := env.Stdin.(*os.File)
	if !ok || in == nil {
		return "", errors.New("the standard input is no terminal")
	}
	fd := int(in.Fd())
	saved, err := unix.IoctlGetTermios(fd, termiosGet)
	if err != nil {
		return "", errors.New("the standard input is no terminal")
	}
	quiet := *saved
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	if err := unix.IoctlSetTermios(fd, termiosSet, &quiet); err != nil {
		return "", err
	}
	restore := func() { unix.IoctlSetTermios(fd, termiosSet, saved) }
	fmt.Fprint(env.Stderr, prompt)

	type answer struct {
		text string
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		var b strings.Builder
		buf := make([]byte, 1)
		for {
			n, err := in.Read(buf)
			if n == 1 && buf[0] == '\n' {
				done <- answer{text: strings.TrimRight(b.String(), "\r")}
				return
			}
			if n == 1 {
				b.WriteByte(buf[0])
				if b.Len() > 1024 {
					done <- answer{err: errors.New("longer than a password")}
					return
				}
			}
			if err != nil {
				done <- answer{err: errors.New("no password was entered")}
				return
			}
		}
	}()
	select {
	case a := <-done:
		restore()
		fmt.Fprintln(env.Stderr)
		return a.text, a.err
	case <-ctx.Done():
		// The terminal gets its echo back also when the command is
		// interrupted at the prompt.
		restore()
		fmt.Fprintln(env.Stderr)
		return "", ctx.Err()
	}
}
