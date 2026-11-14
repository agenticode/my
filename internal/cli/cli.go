// Package cli is the command dispatcher behind the my binary.
//
// It is a thin layer over the standard flag package: a command is a name, a
// one-line summary and a function. Commands write to the streams in Env so
// that tests can run them without touching the process state.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Exit codes. Codes 1 to 4 are reserved for verdict grades (observe, consider
// replacement, replace now, hold replacement), so usage and runtime failures
// use the sysexits range instead.
const (
	ExitOK      = 0
	ExitUsage   = 64
	ExitFailure = 70
)

// Env carries the process streams and environment a command may use.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
}

// OSEnv returns an Env bound to the real process streams.
func OSEnv() *Env {
	return &Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv}
}

// Command is one subcommand of the binary.
type Command struct {
	// Name is the word typed after "my".
	Name string
	// Summary is the one-line description shown in the command list.
	Summary string
	// Hidden commands run normally but are left out of the command list.
	// They exist for tests, demos and development.
	Hidden bool
	// Run executes the command with the arguments after its name and returns
	// the process exit code.
	Run func(ctx context.Context, env *Env, args []string) int
}

// App is a named set of commands.
type App struct {
	Name     string
	Synopsis string
	Commands []*Command
}

// Lookup returns the command with the given name, or nil.
func (a *App) Lookup(name string) *Command {
	for _, c := range a.Commands {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Run dispatches args (without the program name) to a command.
func (a *App) Run(ctx context.Context, env *Env, args []string) int {
	if len(args) == 0 {
		a.usage(env.Stderr)
		return ExitUsage
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		a.usage(env.Stdout)
		return ExitOK
	}
	if strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(env.Stderr, "%s: unknown flag %s\n\n", a.Name, args[0])
		a.usage(env.Stderr)
		return ExitUsage
	}
	cmd := a.Lookup(args[0])
	if cmd == nil {
		fmt.Fprintf(env.Stderr, "%s: unknown command %q\n\n", a.Name, args[0])
		a.usage(env.Stderr)
		return ExitUsage
	}
	return cmd.Run(ctx, env, args[1:])
}

func (a *App) usage(w io.Writer) {
	if a.Synopsis != "" {
		fmt.Fprintf(w, "%s\n\n", a.Synopsis)
	}
	fmt.Fprintf(w, "Usage:\n  %s <command> [arguments]\n\nCommands:\n", a.Name)
	var visible []*Command
	width := 0
	for _, c := range a.Commands {
		if c.Hidden {
			continue
		}
		visible = append(visible, c)
		if len(c.Name) > width {
			width = len(c.Name)
		}
	}
	sort.SliceStable(visible, func(i, j int) bool { return visible[i].Name < visible[j].Name })
	for _, c := range visible {
		fmt.Fprintf(w, "  %-*s  %s\n", width, c.Name, c.Summary)
	}
	fmt.Fprintf(w, "\nRun \"%s <command> -h\" for the flags of a command.\n", a.Name)
}
