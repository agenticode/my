package cli

import (
	"errors"
	"io/fs"
	"regexp"
	"strings"
	"syscall"

	"github.com/agenticode/my/internal/i18n"
)

// Messages are the texts of the errors this package words itself: the
// errors of the flag package and of files that cannot be opened. Commands
// show them in the reader's language through ErrorText.
var Messages = i18n.Catalog{
	"cli.unknown_command": {EN: "unknown command \"{cmd}\"", KO: "알 수 없는 명령 \"{cmd}\""},
	"cli.unknown_flag":    {EN: "unknown flag {flag}", KO: "알 수 없는 옵션 {flag}"},
	"cli.needs_value":     {EN: "{flag} needs a value", KO: "{flag}에 값이 필요함"},
	"cli.not_number":      {EN: "{flag} {value}: not a number", KO: "{flag} {value}: 숫자가 아님"},
	"cli.out_of_range":    {EN: "{flag} {value}: the number is too large", KO: "{flag} {value}: 수가 너무 큼"},
	"cli.bad_value":       {EN: "{flag} {value}: {reason}", KO: "{flag} {value}: {reason}"},
	"cli.not_bool":        {EN: "{flag}={value}: want true or false", KO: "{flag}={value}: true 또는 false여야 함"},
	"cli.bad_syntax":      {EN: "{arg}: not a flag", KO: "{arg}: 옵션 형식이 아님"},
	"cli.unknown_lang":    {EN: "unknown language \"{value}\" (supported: en, ko)", KO: "알 수 없는 언어 \"{value}\" (지원: en, ko)"},
	"cli.no_such_file":    {EN: "{path}: no such file or directory", KO: "{path}: 파일이나 디렉터리가 없음"},
	"cli.denied":          {EN: "{path}: permission denied", KO: "{path}: 권한이 없음"},
	"cli.is_dir":          {EN: "{path}: is a directory", KO: "{path}: 디렉터리임"},
	"cli.not_dir":         {EN: "{path}: a part of the path is not a directory", KO: "{path}: 경로의 일부가 디렉터리가 아님"},
	"cli.path_other":      {EN: "{path}: {reason}", KO: "{path}: {reason}"},
}

// The errors of the flag package are plain texts (flag.go, failf). They are
// matched here so that the reader sees them in the words of this tool:
// "--width" where the package says "-width".
var (
	flagUndefined = regexp.MustCompile(`^flag provided but not defined: -+(.+)$`)
	flagNoValue   = regexp.MustCompile(`^flag needs an argument: -+(.+)$`)
	flagBool      = regexp.MustCompile(`^invalid boolean (?:value "(.*)" for|flag) -*([^:]+): `)
	flagValue     = regexp.MustCompile(`^invalid value "(.*)" for flag -+([^:]+): (.*)$`)
	flagSyntax    = regexp.MustCompile(`^bad flag syntax: (.*)$`)
)

// FlagName writes a flag the way the help texts do: "-o" for a name of one
// letter, "--width" for a longer one.
func FlagName(name string) string {
	name = strings.TrimLeft(name, "-")
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// ErrorText says err in the given language. Errors of the flag package, of
// a file that cannot be opened and an unknown language are worded by this
// package; any other error is returned as its own text. A file error inside
// another error keeps the words in front of it.
func ErrorText(lang i18n.Lang, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if m := flagUndefined.FindStringSubmatch(msg); m != nil {
		return Messages.Text(lang, "cli.unknown_flag", map[string]any{"flag": FlagName(m[1])})
	}
	if m := flagNoValue.FindStringSubmatch(msg); m != nil {
		return Messages.Text(lang, "cli.needs_value", map[string]any{"flag": FlagName(m[1])})
	}
	if m := flagBool.FindStringSubmatch(msg); m != nil {
		return Messages.Text(lang, "cli.not_bool", map[string]any{"flag": FlagName(m[2]), "value": m[1]})
	}
	if m := flagValue.FindStringSubmatch(msg); m != nil {
		args := map[string]any{"flag": FlagName(m[2]), "value": m[1], "reason": m[3]}
		switch m[3] {
		case "parse error":
			return Messages.Text(lang, "cli.not_number", args)
		case "value out of range":
			return Messages.Text(lang, "cli.out_of_range", args)
		}
		return Messages.Text(lang, "cli.bad_value", args)
	}
	if m := flagSyntax.FindStringSubmatch(msg); m != nil {
		return Messages.Text(lang, "cli.bad_syntax", map[string]any{"arg": m[1]})
	}
	var le *i18n.LangError
	if errors.As(err, &le) {
		return Messages.Text(lang, "cli.unknown_lang", map[string]any{"value": le.Name})
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		// What stands in front of the file error ("configuration file:")
		// stays; the operation of the system call ("open", "stat") goes.
		prefix := strings.TrimSuffix(msg, pe.Error())
		if prefix == msg {
			prefix = ""
		}
		return prefix + pathText(lang, pe)
	}
	return msg
}

func pathText(lang i18n.Lang, pe *fs.PathError) string {
	args := map[string]any{"path": pe.Path, "reason": pe.Err.Error()}
	switch {
	case errors.Is(pe.Err, fs.ErrNotExist):
		return Messages.Text(lang, "cli.no_such_file", args)
	case errors.Is(pe.Err, fs.ErrPermission):
		return Messages.Text(lang, "cli.denied", args)
	case errors.Is(pe.Err, syscall.EISDIR):
		return Messages.Text(lang, "cli.is_dir", args)
	case errors.Is(pe.Err, syscall.ENOTDIR):
		return Messages.Text(lang, "cli.not_dir", args)
	}
	return Messages.Text(lang, "cli.path_other", args)
}
