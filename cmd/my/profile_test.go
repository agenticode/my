package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The profiles a run writes when the environment asks for them are files
// "go tool pprof" reads: gzip data. Without the variables nothing is
// written, and a file that cannot be written is reported, not fatal.
func TestProfiles(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{
		envCPUProfile:  filepath.Join(dir, "cpu.pprof"),
		envMemProfile:  filepath.Join(dir, "mem.pprof"),
		envPeakProfile: filepath.Join(dir, "peak.pprof"),
	}
	var stderr bytes.Buffer
	stop := startProfiles(func(k string) string { return env[k] }, &stderr)
	// Work that makes the heap grow, long enough for the heap to be
	// looked at.
	var keep [][]byte
	for range 40 {
		keep = append(keep, make([]byte, 1<<20))
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	_ = keep
	if stderr.Len() != 0 {
		t.Errorf("messages: %s", stderr.String())
	}
	for _, path := range env {
		b, err := os.ReadFile(path)
		if err != nil || len(b) < 2 || b[0] != 0x1f || b[1] != 0x8b {
			t.Errorf("%s: %v, %d bytes", filepath.Base(path), err, len(b))
		}
	}

	// Nothing asked for, nothing written.
	before, _ := os.ReadDir(dir)
	startProfiles(func(string) string { return "" }, &stderr)()
	if after, _ := os.ReadDir(dir); len(after) != len(before) || stderr.Len() != 0 {
		t.Errorf("a run without the variables wrote something: %s", stderr.String())
	}

	// A place that cannot be written to.
	bad := map[string]string{envCPUProfile: filepath.Join(dir, "none", "cpu"), envMemProfile: filepath.Join(dir, "none", "mem")}
	startProfiles(func(k string) string { return bad[k] }, &stderr)()
	if out := stderr.String(); !strings.Contains(out, "my: "+envCPUProfile+": ") || !strings.Contains(out, "my: "+envMemProfile+": ") {
		t.Errorf("messages for files that cannot be written: %q", out)
	}
}
