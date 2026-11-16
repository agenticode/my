package crashlog

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// FuzzParts feeds made pstore directories to the grouping of parts and the
// reading of their headers. The input is a list of files, "name\x00text"
// each, separated by "\x01". Every part of a dump is a file of the input,
// no file is in two dumps, the parts of a dump with headers run from the
// highest part number down, and reading never fails on the content.
func FuzzParts(f *testing.F) {
	f.Add([]byte("dmesg-efi_pstore-179473950301001\x00Panic#1 Part1\n<0>[ 58.2] mce: x\n\x01dmesg-efi_pstore-179473950302001\x00Panic#1 Part2\n<6>[ 1.0] y\n"))
	f.Add([]byte("dmesg-erst-7300000000000000001\x00Oops#1 Part1\n\x01dmesg-erst-7300000000000000002\x00Panic#2 Part1\nz\n\x01dmesg-erst-7300000000000000003\x00Panic#2 Part2\n"))
	f.Add([]byte("dmesg-ramoops-0\x00Panic#1 Part1\n[ 1.000000] a\n\x01console-ramoops-0\x00[ 2.000000] b\n\x01pmsg-ramoops-0\x00c\n"))
	f.Add([]byte("x/001/dmesg.txt\x00Panic#1 Part2\nq\nPanic#1 Part1\nr\n\x01dmesg-efi-1.enc.z\x00\x78\x9c"))
	f.Add([]byte("dmesg-efi_pstore-99999999999999999999\x00Panic#99999 Part999999\n"))
	safe := regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,80}(/[A-Za-z0-9_.\-]{1,40}){0,3}$`)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			return
		}
		dir := t.TempDir()
		names := map[string]bool{}
		for _, file := range bytes.Split(data, []byte{1}) {
			name, text, ok := bytes.Cut(file, []byte{0})
			if !ok || !safe.Match(name) || bytes.Contains(name, []byte("..")) {
				continue
			}
			p := filepath.Join(dir, string(name))
			if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
				continue
			}
			if os.WriteFile(p, text, 0o644) == nil {
				names[p] = true
			}
		}
		a := NewDir(KindPstore, dir, Options{ModTime: true})
		found, err := a.walk()
		if err != nil {
			return
		}
		seen := map[string]bool{}
		for _, d := range found.dumps {
			for i, p := range d.parts {
				if !names[p.path] {
					t.Fatalf("part %s is no file of the input", p.path)
				}
				if seen[p.path] {
					t.Fatalf("part %s is in two dumps", p.path)
				}
				seen[p.path] = true
				if i > 0 && p.header && d.parts[i-1].header && d.parts[i-1].no < p.no {
					t.Fatalf("parts out of order: %d before %d", d.parts[i-1].no, p.no)
				}
			}
			if _, err := d.text(); err != nil && !os.IsNotExist(err) {
				var tl *adapter.TooLargeError
				if !asTooLarge(err, &tl) {
					t.Fatalf("text of %s: %v", d.name, err)
				}
			}
		}
		rep := a.Collect(context.Background(), discard{})
		if rep.Status == model.SourceError {
			t.Fatalf("collect: %+v", rep)
		}
	})
}

func asTooLarge(err error, tl **adapter.TooLargeError) bool {
	t, ok := err.(*adapter.TooLargeError)
	if ok {
		*tl = t
	}
	return ok
}

// discard is a sink that takes every record.
type discard struct{}

func (discard) Event(*model.Event) error             { return nil }
func (discard) Action(*model.Action) error           { return nil }
func (discard) GPU(*model.GPUMem) error              { return nil }
func (discard) ECS(*model.ECSSample) error           { return nil }
func (discard) Boot(*model.Boot) error               { return nil }
func (discard) Host(*model.Host) error               { return nil }
func (discard) Module(*model.Module) error           { return nil }
func (discard) Counter(*model.CounterSample) error   { return nil }
func (discard) BMC(*model.BMC) error                 { return nil }
func (discard) BIOSSetting(*model.BIOSSetting) error { return nil }
func (discard) BMCMemory(*model.BMCMemory) error     { return nil }
