// Package cli is the command dispatcher behind the my binary.
//
// It is a thin layer over the standard flag package: a command is a name, a
// one-line summary and a function. Commands write to the streams in Env so
// that tests can run them without touching the process state.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/agenticode/my/internal/i18n"
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
	// Summary is the one-line description shown in the command list, and
	// SummaryKO the same in Korean.
	Summary   string
	SummaryKO string
	// Hidden commands run normally but are left out of the command list.
	// They exist for tests, demos and development.
	Hidden bool
	// Run executes the command with the arguments after its name and returns
	// the process exit code.
	Run func(ctx context.Context, env *Env, args []string) int
}

// App is a named set of commands.
type App struct {
	Name string
	// Synopsis says in one or two lines what the program is, and
	// SynopsisKO the same in Korean.
	Synopsis   string
	SynopsisKO string
	Commands   []*Command
	// Default runs when no command is named: with no arguments at all, or
	// when the first argument is a flag.
	Default *Command
	// DefaultSummary describes what running the program without a command
	// does, for the usage text.
	DefaultSummary   string
	DefaultSummaryKO string
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

// Lang returns the language the environment asks for: MY_LANG, or the
// locale (LC_ALL, LC_MESSAGES, LANG) when it names a supported language,
// and English otherwise.
func Lang(env *Env) i18n.Lang {
	for _, name := range []string{"MY_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		v := env.Getenv(name)
		if v == "" {
			continue
		}
		if l, err := i18n.ParseLang(v); err == nil {
			return l
		}
		if name != "MY_LANG" {
			break // the locale is set, to a language without a translation
		}
	}
	return i18n.EN
}

// Parse parses the flags in args and returns the other arguments. Unlike
// flag.FlagSet.Parse it lets flags follow positional arguments, as in
// "my map CPU0_C1 --bank 3". Everything after "--" is positional.
func Parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos, rest []string
	for i, a := range args {
		if a == "--" {
			args, rest = args[:i], args[i+1:]
			break
		}
	}
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		pos, args = append(pos, args[0]), args[1:]
	}
	return append(pos, rest...), nil
}

// IsHelp reports whether an argument asks for help. "?" does: it is what
// the screens name as the key for the reading guide.
func IsHelp(arg string) bool {
	switch arg {
	case "-h", "-help", "--help", "?", "-?":
		return true
	}
	return false
}

// Run dispatches args (without the program name) to a command.
func (a *App) Run(ctx context.Context, env *Env, args []string) int {
	if len(args) > 0 {
		switch {
		case args[0] == "help":
			return a.help(ctx, env, args[1:])
		case IsHelp(args[0]):
			a.usage(env.Stdout, Lang(env))
			return ExitOK
		case !strings.HasPrefix(args[0], "-"):
			cmd := a.Lookup(args[0])
			if cmd == nil {
				fmt.Fprintf(env.Stderr, "%s: unknown command %q\n\n", a.Name, args[0])
				a.usage(env.Stderr, Lang(env))
				return ExitUsage
			}
			return cmd.Run(ctx, env, args[1:])
		}
	}
	if a.Default != nil {
		return a.Default.Run(ctx, env, args)
	}
	if len(args) > 0 {
		fmt.Fprintf(env.Stderr, "%s: unknown flag %s\n\n", a.Name, args[0])
	}
	a.usage(env.Stderr, Lang(env))
	return ExitUsage
}

// help prints the command list, or the help of one command. A --lang flag
// among the arguments chooses the language and is handed on to the command.
func (a *App) help(ctx context.Context, env *Env, args []string) int {
	lang := Lang(env)
	var names, langArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		switch {
		case !strings.HasPrefix(arg, "-"):
			names = append(names, arg)
			continue
		case name != "lang":
			continue
		case !hasValue && i+1 < len(args):
			i++
			value = args[i]
		}
		if l, err := i18n.ParseLang(value); err == nil {
			lang = l
		}
		langArgs = []string{"--lang", value}
	}
	if len(names) == 0 {
		a.usage(env.Stdout, lang)
		return ExitOK
	}
	cmd := a.Lookup(names[0])
	if cmd == nil {
		fmt.Fprintf(env.Stderr, "%s help: unknown command %q\n\n", a.Name, names[0])
		a.usage(env.Stderr, lang)
		return ExitUsage
	}
	return cmd.Run(ctx, env, append([]string{"--help"}, langArgs...))
}

func (a *App) usage(w io.Writer, lang i18n.Lang) {
	ko := lang == i18n.KO
	pick := func(en, kor string) string {
		if ko && kor != "" {
			return kor
		}
		return en
	}
	if s := pick(a.Synopsis, a.SynopsisKO); s != "" {
		fmt.Fprintf(w, "%s\n\n", s)
	}
	usage, commands, more := "Usage:", "Commands:", "Run \"%s help <command>\" for the flags of a command and for how to read what it prints.\n"
	if ko {
		usage, commands, more = "사용법:", "명령:", "\"%s help <명령>\"은 그 명령의 옵션과 출력 판독법을 보여줍니다.\n"
	}
	fmt.Fprintf(w, "%s\n", usage)
	if a.Default != nil {
		fmt.Fprintf(w, "  %s [flags]%s%s\n", a.Name, strings.Repeat(" ", 14), pick(a.DefaultSummary, a.DefaultSummaryKO))
	}
	fmt.Fprintf(w, "  %s <command> [arguments]\n\n%s\n", a.Name, commands)
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
	for _, c := range visible {
		fmt.Fprintf(w, "  %-*s  %s\n", width, c.Name, pick(c.Summary, c.SummaryKO))
	}
	fmt.Fprintf(w, "\n"+more, a.Name)
}
