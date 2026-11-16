package preflight_test

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/preflight"
	pt "github.com/agenticode/my/internal/preflight/preflighttest"
)

// FuzzHost: a copy of a host's files whose files hold anything gives a
// report with every check in a known state, a script, and sentences in
// both languages, and changes nothing in the copy. One file of the copy
// of a kept host is replaced by the input at a time.
func FuzzHost(f *testing.F) {
	files := pt.Kept()
	var texts []string
	for name, v := range files {
		if v != pt.Dir && v != pt.Denied && v != pt.DeniedDir && !strings.HasPrefix(v, pt.LinkPrefix) {
			texts = append(texts, name)
		}
	}
	sort.Strings(texts)
	root := pt.Tree(f, files)
	index := func(name string) uint16 {
		return uint16(sort.SearchStrings(texts, name))
	}
	f.Add(index("proc/cmdline"), []byte("crashkernel=0M-2G:128M,2G-:256M mce=print_all panic=-1 console=ttyS0,\n"))
	f.Add(index("proc/cpuinfo"), []byte("vendor_id\t: AuthenticAMD\ncpu family\t: 25\nmodel\t: \n"))
	f.Add(index("etc/systemd/journald.conf"), []byte("[Journal]\nStorage=volatile\nSyncIntervalSec=\x00\n[Journal\n"))
	f.Add(index("usr/lib/systemd/pstore.conf"), []byte("[PStore]\nStorage=journal\nUnlink=maybe\n"))
	f.Add(index("etc/os-release"), []byte("ID=\"debian\nID_LIKE=ubuntu debian\n"))
	f.Add(index("proc/consoles"), []byte("ttyS0 -W- (E p a) 4:64\nnetcon0 -W- (E) 0:0\n\x1b[31m"))
	f.Add(index("sys/kernel/kexec_crash_size"), []byte("99999999999999999999999\n"))
	f.Add(index("proc/stat"), []byte("btime -1\n"))
	f.Add(index("etc/systemd/system/my-harvest.service"), []byte(preflight.UnitMarker+"\nExecStart=/usr/local/bin/my harvest --ledger /x y\n"))
	f.Fuzz(func(t *testing.T, which uint16, data []byte) {
		name := texts[int(which)%len(texts)]
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.WriteFile(path, []byte(files[name]), 0o644)
		rep := preflight.Run(preflight.Options{Root: root})
		if len(rep.Checks) != len(preflight.Names) {
			t.Fatalf("%s: %d checks", name, len(rep.Checks))
		}
		lost := 0
		for i, c := range rep.Checks {
			if c.Name != preflight.Names[i] {
				t.Fatalf("%s: check %d is %q", name, i, c.Name)
			}
			switch c.State {
			case preflight.Kept, preflight.None, preflight.Unknown, preflight.Info:
			case preflight.Lost:
				lost++
			default:
				t.Fatalf("%s: %s has the state %q", name, c.Name, c.State)
			}
			for _, l := range i18n.Langs {
				if s := preflight.Text(l, c.Detail); s == "" || s == c.Detail.Code {
					t.Fatalf("%s: %s: no sentence for %q in %s", name, c.Name, c.Detail.Code, l)
				}
			}
		}
		if lost != rep.Lost {
			t.Fatalf("%s: Lost = %d, %d checks lost", name, rep.Lost, lost)
		}
		if err := preflight.Script(io.Discard, rep, i18n.KO, time.Unix(0, 0)); err != nil {
			t.Fatalf("%s: script: %v", name, err)
		}
		if b, err := os.ReadFile(path); err != nil || string(b) != string(data) {
			t.Fatalf("%s: the copy changed", name)
		}
	})
}
