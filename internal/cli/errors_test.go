package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/agenticode/my/internal/i18n"
)

// TestErrorText: the errors of the flag package, of files and of the
// language are said in the words of the tool, in both languages; any
// other error keeps its text.
func TestErrorText(t *testing.T) {
	parse := func(args ...string) error {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		fs.Int("width", 0, "")
		fs.Bool("json", false, "")
		fs.String("o", "", "")
		fs.Func("bank", "", func(string) error { return errors.New("want a bank") })
		return fs.Parse(args)
	}
	missing := filepath.Join(t.TempDir(), "absent.conf")
	_, openErr := os.Open(missing)
	for _, tt := range []struct {
		name   string
		err    error
		en, ko string
	}{
		{"unknown flag", parse("--frob"), "unknown flag --frob", "알 수 없는 옵션 --frob"},
		{"short flag", parse("-x"), "unknown flag -x", "알 수 없는 옵션 -x"},
		{"no value", parse("--width"), "--width needs a value", "--width에 값이 필요함"},
		{"no value of -o", parse("-o"), "-o needs a value", "-o에 값이 필요함"},
		{"not a number", parse("--width", "abc"), "--width abc: not a number", "--width abc: 숫자가 아님"},
		{"too large", parse("--width", "99999999999999999999"), "--width 99999999999999999999: the number is too large", "--width 99999999999999999999: 수가 너무 큼"},
		{"boolean", parse("--json=maybe"), "--json=maybe: want true or false", "--json=maybe: true 또는 false여야 함"},
		{"own reason", parse("--bank", "zz"), "--bank zz: want a bank", "--bank zz: want a bank"},
		{"language", func() error { _, err := i18n.ParseLang("xx"); return err }(), `unknown language "xx" (supported: en, ko)`, `알 수 없는 언어 "xx" (지원: en, ko)`},
		{"no file", openErr, missing + ": no such file or directory", missing + ": 파일이나 디렉터리가 없음"},
		{"no file, with words in front", fmt.Errorf("--config %w", openErr), "--config " + missing + ": no such file or directory", "--config " + missing + ": 파일이나 디렉터리가 없음"},
		{"denied", &fs.PathError{Op: "open", Path: "/x", Err: fs.ErrPermission}, "/x: permission denied", "/x: 권한이 없음"},
		{"other file error", &fs.PathError{Op: "read", Path: "/x", Err: errors.New("input/output error")}, "/x: input/output error", "/x: input/output error"},
		{"other error", errors.New("the store is gone"), "the store is gone", "the store is gone"},
	} {
		if tt.err == nil {
			t.Errorf("%s: no error", tt.name)
			continue
		}
		if got := ErrorText(i18n.EN, tt.err); got != tt.en {
			t.Errorf("%s: en %q, want %q", tt.name, got, tt.en)
		}
		if got := ErrorText(i18n.KO, tt.err); got != tt.ko {
			t.Errorf("%s: ko %q, want %q", tt.name, got, tt.ko)
		}
	}
	if ErrorText(i18n.KO, nil) != "" {
		t.Error("no error has a text")
	}
}

func TestMessagesAreComplete(t *testing.T) {
	for _, code := range Messages.Codes() {
		m := Messages[code]
		if m.EN == "" || m.KO == "" {
			t.Errorf("%s: a language is missing", code)
		}
		if fmt.Sprint(i18n.Placeholders(m.EN)) != fmt.Sprint(i18n.Placeholders(m.KO)) {
			t.Errorf("%s: placeholders differ", code)
		}
	}
}

func TestFlagName(t *testing.T) {
	for in, want := range map[string]string{"o": "-o", "-o": "-o", "width": "--width", "-width": "--width", "--width": "--width"} {
		if got := FlagName(in); got != want {
			t.Errorf("FlagName(%q) = %q, want %q", in, got, want)
		}
	}
}
