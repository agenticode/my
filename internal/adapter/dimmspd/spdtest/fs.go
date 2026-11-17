package spdtest

import (
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"
)

// Fault is what a file of the recording file system does wrong.
type Fault struct {
	// OpenErr is returned by Open.
	OpenErr error
	// At and Err: a read that starts at offset at returns err.
	At  int
	Err error
	// Size is what Stat reports in place of the length of the data.
	Size int64
	// Block makes a read that starts at offset at wait until the test
	// ends.
	Block bool
}

// FS is a file system over a made tree that records every name it is
// asked for and how, and injects faults. It has no method that writes.
type FS struct {
	m      fstest.MapFS
	Faults map[string]Fault
	mu     sync.Mutex
	asked  []string
	stop   chan struct{}
}

// NewFS returns the file system of a tree. A read that blocks waits
// until the test ends.
func NewFS(t testing.TB, tr *Tree) *FS {
	r := &FS{m: tr.M, Faults: map[string]Fault{}, stop: make(chan struct{})}
	t.Cleanup(func() { close(r.stop) })
	return r
}

func (r *FS) record(op, name string) {
	r.mu.Lock()
	r.asked = append(r.asked, op+" "+name)
	r.mu.Unlock()
}

func (r *FS) Open(name string) (fs.File, error) {
	r.record("open", name)
	f, ok := r.Faults[name]
	if ok && f.OpenErr != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: f.OpenErr}
	}
	file, err := r.m.Open(name)
	if err != nil || !ok {
		return file, err
	}
	return &faultyFile{File: file, f: f, stop: r.stop}, nil
}

func (r *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	r.record("readdir", name)
	return r.m.ReadDir(name)
}

func (r *FS) Stat(name string) (fs.FileInfo, error) {
	r.record("stat", name)
	fi, err := r.m.Stat(name)
	if f, ok := r.Faults[name]; ok && err == nil && f.Size > 0 {
		return sizedInfo{fi, f.Size}, nil
	}
	return fi, err
}

func (r *FS) ReadLink(name string) (string, error) {
	r.record("readlink", name)
	return r.m.ReadLink(name)
}

func (r *FS) Lstat(name string) (fs.FileInfo, error) {
	r.record("lstat", name)
	return r.m.Lstat(name)
}

type sizedInfo struct {
	fs.FileInfo
	size int64
}

func (s sizedInfo) Size() int64 { return s.size }

type faultyFile struct {
	fs.File
	f    Fault
	off  int
	stop chan struct{}
}

func (f *faultyFile) Read(b []byte) (int, error) {
	if f.f.Err != nil && f.off == f.f.At {
		return 0, f.f.Err
	}
	if f.f.Block && f.off == f.f.At {
		<-f.stop
		return 0, io.EOF
	}
	n, err := f.File.Read(b)
	f.off += n
	return n, err
}

// The names the reader may ask for, as docs/compatibility.md lists them.
var allowed = []*regexp.Regexp{
	regexp.MustCompile(`^(stat|readdir) sys/bus/i2c(/devices)?$`),
	regexp.MustCompile(`^open sys/bus/i2c/devices/i2c-\d+/name$`),
	regexp.MustCompile(`^open sys/bus/i2c/devices/\d+-005[0-7]/name$`),
	regexp.MustCompile(`^readlink sys/bus/i2c/devices/\d+-005[0-7]/driver$`),
	regexp.MustCompile(`^(stat|open) sys/bus/i2c/devices/\d+-005[0-7]/eeprom$`),
	regexp.MustCompile(`^(stat|open) sys/bus/nvmem/devices/\d+-005[0-7]/nvmem$`),
	regexp.MustCompile(`^readdir sys/bus/i3c/devices$`),
	regexp.MustCompile(`^readlink sys/bus/i3c/devices/[^/]+/driver$`),
	regexp.MustCompile(`^(stat|open) sys/bus/i3c/devices/[^/]+/eeprom$`),
	regexp.MustCompile(`^stat sys/bus/i2c/drivers/(ee1004|spd5118|at24)$`),
	regexp.MustCompile(`^stat sys/bus/pci/drivers/(i801_smbus|piix4_smbus)$`),
	regexp.MustCompile(`^readdir sys/bus/pci/devices$`),
	regexp.MustCompile(`^open sys/bus/pci/devices/[^/]+/(class|vendor|device)$`),
	regexp.MustCompile(`^readlink sys/bus/pci/devices/[^/]+/driver$`),
	regexp.MustCompile(`^open sys/bus/pci/devices/[^/]+/config$`),
	regexp.MustCompile(`^open lib/modules/[^/]+/modules\.(dep|builtin)$`),
	regexp.MustCompile(`^stat sys/class/ipmi$`),
	regexp.MustCompile(`^readdir sys/firmware/dmi/entries$`),
}

// CheckAsked fails for a name outside the list, and for the configuration
// space of a PCI device asked without root.
func (r *FS) CheckAsked(t testing.TB, privileged bool) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.asked {
		ok := false
		for _, re := range allowed {
			ok = ok || re.MatchString(a)
		}
		if !ok {
			t.Errorf("the reader asked for %q, which is not on its list", a)
		}
		if !privileged && strings.HasSuffix(a, "/config") {
			t.Errorf("the reader read %q without root", a)
		}
		if strings.Contains(a, "hwmon") || strings.HasPrefix(a, "open dev/") {
			t.Errorf("the reader asked for %q", a)
		}
	}
}

// AskedFor returns what was asked for that starts with prefix.
func (r *FS) AskedFor(prefix string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, a := range r.asked {
		if strings.HasPrefix(a, prefix) {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// Errors of the faults.
var (
	EIO    error = syscall.EIO
	ENXIO  error = syscall.ENXIO
	EACCES error = syscall.EACCES
)

// Deadline is a short deadline for the tests of a read that does not
// return.
const Deadline = 200 * time.Millisecond

// Asked returns everything that was asked for, in order.
func (r *FS) Asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.asked...)
}
