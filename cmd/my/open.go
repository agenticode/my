package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/cli"
	"github.com/agenticode/my/internal/store"
)

// sqliteMagic starts every SQLite file.
var sqliteMagic = []byte("SQLite format 3\x00")

// openCase prepares a stored case for reading. The file is never changed:
// a ledger of an older schema is copied and the copy is brought up to date.
func openCase(ctx context.Context, path string) (*source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 16)
	n, _ := io.ReadFull(f, head)
	f.Close()
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return nil, worded("err.case_dir", "path", path)
	}
	if bundle.IsBundle(path) {
		c, err := bundle.Open(ctx, path, "")
		if err != nil {
			return nil, err
		}
		return &source{path: path, ledger: c.Ledger, manifest: &c.Manifest, cleanup: func() { c.Close() }}, nil
	}
	if !bytes.Equal(head[:n], sqliteMagic) {
		return nil, worded("err.not_case", "path", path)
	}
	src := &source{path: path, ledger: path, cleanup: func() {}}
	s, err := store.Open(ctx, path, store.ReadOnly)
	switch {
	case err == nil:
		s.Close()
		return src, nil
	case !errors.Is(err, store.ErrNeedsMigration):
		return nil, err
	}
	// An older ledger: work on a private copy.
	tmp, err := os.MkdirTemp("", "my-open-")
	if err != nil {
		return nil, err
	}
	src.cleanup = func() { os.RemoveAll(tmp) }
	src.ledger = filepath.Join(tmp, "ledger.db")
	if err := copyFile(path, src.ledger); err != nil {
		src.cleanup()
		return nil, err
	}
	s, err = store.Open(ctx, src.ledger, store.ReadWrite)
	if err != nil {
		src.cleanup()
		return nil, err
	}
	if err := s.Close(); err != nil {
		src.cleanup()
		return nil, err
	}
	return src, nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// runOpen is "my open FILE [command ...]": the commands that show
// findings, on a stored case instead of on this host.
func runOpen(ctx context.Context, env *cli.Env, args []string) int {
	lang := cli.Lang(env)
	if len(args) == 0 || cli.IsHelp(args[0]) {
		o := newOptions(env, "my open", &source{})
		_, lang, _, _ = o.parse(env, args)
		if len(args) == 0 {
			return o.usageError(env, lang, "%s", say(lang, "err.open_file"))
		}
		return printHelp(env, lang, o.style(env, lang), "open", nil)
	}
	name := "my open"
	if len(args[0]) > 0 && args[0][0] == '-' {
		fmt.Fprintf(env.Stderr, "%s: %s\n", name, say(lang, "err.open_file"))
		return cli.ExitUsage
	}
	src, err := openCase(ctx, args[0])
	if err != nil {
		fmt.Fprintf(env.Stderr, "%s: %s\n", name, errText(lang, err))
		return cli.ExitFailure
	}
	defer src.cleanup()
	rest := args[1:]
	if len(rest) > 0 {
		if fn, ok := viewCommands[rest[0]]; ok {
			return fn(ctx, env, rest[1:], src)
		}
		if len(rest[0]) > 0 && rest[0][0] != '-' && rest[0] != "?" {
			fmt.Fprintf(env.Stderr, "%s: %s\n", name, say(lang, "err.open_command", "cmd", rest[0]))
			return cli.ExitUsage
		}
	}
	return runOverview(ctx, env, rest, src)
}
