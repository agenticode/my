package scan

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/testutil"
)

// TestDetectDirectoryOfFiles: a directory that is none of the known trees
// is read file by file; what no reader recognises is counted in one line.
func TestDetectDirectoryOfFiles(t *testing.T) {
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	copyFile := func(from, to string) {
		t.Helper()
		data, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		to = filepath.Join(dir, filepath.FromSlash(to))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(testutil.Testdata(t, "sel", "ipmitool-elist.txt"), "bmc/sel.txt")
	copyFile(testutil.Testdata(t, "klog", "ghes-6.x.dmesg"), "host/logs/dmesg.txt")
	for name, text := range map[string]string{"notes.txt": "shipped back on Monday\n", "bmc/config.ini": "[net]\nmode=static\n", "empty": ""} {
		copyFile(writeTemp(t, text), name)
	}
	// Ten levels down: below the limit of the walk.
	deep := filepath.Join(dir, "a", "b", "c", "d", "e", "f", "g", "h", "i", "j")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(testutil.Testdata(t, "sel", "ipmitool-elist.txt"), "a/b/c/d/e/f/g/h/i/j/sel.txt")
	// A file above 64 MiB, sparse.
	big, err := os.Create(filepath.Join(dir, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(MaxDirFile + 1); err != nil {
		t.Fatal(err)
	}
	big.Close()

	as, what, err := Detect(ctx, dir, DetectOptions{Now: now})
	if err != nil || what != WhatFiles {
		t.Fatalf("%q, %v", what, err)
	}
	kinds := map[string]int{}
	var note string
	for _, a := range as {
		kinds[a.Kind()]++
		if a.Kind() == adapter.KindUnknown {
			note = a.Collect(ctx, nil).Detail
		}
	}
	if kinds["sel"] != 1 || kinds["klog"] != 1 || kinds[adapter.KindUnknown] != 1 || len(as) != 3 {
		t.Errorf("sources %v", kinds)
	}
	for _, want := range []string{"3 files were not recognised and left out", "1 file above 64 MiB not read", "more than 2000 files or 8 levels"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note %q lacks %q", note, want)
		}
	}

	// Nothing a reader knows: the directory is no input.
	empty := t.TempDir()
	copyFile2 := filepath.Join(empty, "notes.txt")
	if err := os.WriteFile(copyFile2, []byte("nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var ue *UnknownInputError
	if _, _, err := Detect(ctx, empty, DetectOptions{Now: now}); !errors.As(err, &ue) || ue.Code != "dir" {
		t.Errorf("a directory of nothing known: %v", err)
	}
}

// TestDirFilesLimit: the walk stops at MaxDirFiles files.
func TestDirFilesLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxDirFiles+5; i++ {
		if err := os.WriteFile(filepath.Join(dir, strings.Repeat("x", 1+i%7)+"-"+itoa(i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w, err := DirFiles(ctx, dir)
	if err != nil || len(w.Files) != MaxDirFiles || !w.More {
		t.Errorf("%d files, more %v, %v", len(w.Files), w.More, err)
	}
}

func writeTemp(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}
