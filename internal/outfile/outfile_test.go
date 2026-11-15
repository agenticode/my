package outfile

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func content(text string) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := io.WriteString(w, text)
		return err
	}
}

// names returns the entries of a directory.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestWriteNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "case.mycase")
	n, err := Write(path, false, content("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "hello" {
		t.Errorf("content %q", b)
	}
	if got := names(t, dir); len(got) != 1 {
		t.Errorf("left beside the file: %v", got)
	}
}

func TestWriteFollowsTheUmask(t *testing.T) {
	for _, tc := range []struct {
		umask int
		want  fs.FileMode
	}{{0o022, 0o644}, {0o077, 0o600}, {0o027, 0o640}} {
		old := syscall.Umask(tc.umask)
		path := filepath.Join(t.TempDir(), "out")
		_, err := Write(path, false, content("x"))
		syscall.Umask(old)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != tc.want {
			t.Errorf("umask %03o: mode %03o, want %03o", tc.umask, got, tc.want)
		}
	}
}

func TestExistingFileNeedsForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.html")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Check(path, false); !errors.Is(err, ErrExists) {
		t.Errorf("Check = %v, want ErrExists", err)
	}
	if _, err := Write(path, false, content("new")); !errors.Is(err, ErrExists) {
		t.Errorf("Write = %v, want ErrExists", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "old" {
		t.Errorf("the file was touched: %q", b)
	}
	if err := Check(path, true); err != nil {
		t.Errorf("Check with force = %v", err)
	}
	if _, err := Write(path, true, content("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Errorf("content %q", b)
	}
	if got := names(t, dir); len(got) != 1 {
		t.Errorf("left beside the file: %v", got)
	}
}

func TestFailedWriteLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	_, err := Write(path, true, func(w io.Writer) error {
		io.WriteString(w, "half")
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "old" {
		t.Errorf("the file that was there is %q after a failed write", b)
	}
	if got := names(t, dir); len(got) != 1 {
		t.Errorf("left beside the file: %v", got)
	}
}

func TestMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absent", "case.mycase")
	err := Check(path, false)
	if !errors.Is(err, ErrNoDir) {
		t.Fatalf("Check = %v, want ErrNoDir", err)
	}
	var pe *PathError
	if !errors.As(err, &pe) || pe.Path != filepath.Join(dir, "absent") {
		t.Errorf("the error names %v, want the directory", err)
	}
	if strings.Contains(err.Error(), ".part") {
		t.Errorf("the error names a file of ours: %v", err)
	}
	// A file where the directory should be.
	if err := os.WriteFile(filepath.Join(dir, "plain"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Check(filepath.Join(dir, "plain", "x"), false); !errors.Is(err, ErrNoDir) {
		t.Errorf("below a file: %v, want ErrNoDir", err)
	}
}

func TestDirectoryAsTarget(t *testing.T) {
	dir := t.TempDir()
	if err := Check(dir, true); !errors.Is(err, ErrIsDir) {
		t.Errorf("Check = %v, want ErrIsDir", err)
	}
	if _, err := Write(dir, true, content("x")); !errors.Is(err, ErrIsDir) {
		t.Errorf("Write = %v, want ErrIsDir", err)
	}
}

func TestClosedDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes everywhere")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if err := Check(filepath.Join(dir, "out"), false); !errors.Is(err, ErrDenied) {
		t.Errorf("Check = %v, want ErrDenied", err)
	}
}

func TestSymbolicLinkStaysALink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.html")
	link := filepath.Join(dir, "latest.html")
	if err := os.WriteFile(real, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(link, false, content("new")); !errors.Is(err, ErrExists) {
		t.Errorf("without force: %v, want ErrExists", err)
	}
	if _, err := Write(link, true, content("new")); err != nil {
		t.Fatal(err)
	}
	if li, err := os.Lstat(link); err != nil || li.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the link was replaced by a file (%v)", err)
	}
	if b, _ := os.ReadFile(real); string(b) != "new" {
		t.Errorf("what the link leads to holds %q", b)
	}

	nowhere := filepath.Join(dir, "nowhere")
	if err := os.Symlink(filepath.Join(dir, "gone", "x"), nowhere); err != nil {
		t.Fatal(err)
	}
	if err := Check(nowhere, true); !errors.Is(err, ErrDangling) {
		t.Errorf("a link that leads nowhere: %v, want ErrDangling", err)
	}
}

// A device, a pipe or a socket under the name is written to. It is never
// replaced by a file: "-o /dev/null --force" as root must leave /dev/null
// a device.
func TestPipeIsWrittenThrough(t *testing.T) {
	dir := t.TempDir()
	pipe := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	got := make(chan string, 1)
	go func() {
		b, _ := os.ReadFile(pipe)
		got <- string(b)
	}()
	for _, force := range []bool{false, true} {
		if err := Check(pipe, force); err != nil {
			t.Fatalf("Check(force=%v) = %v", force, err)
		}
	}
	n, err := Write(pipe, true, content("through"))
	if err != nil || n != 7 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if s := <-got; s != "through" {
		t.Errorf("the reader of the pipe got %q", s)
	}
	fi, err := os.Lstat(pipe)
	if err != nil || fi.Mode()&fs.ModeNamedPipe == 0 {
		t.Errorf("the pipe is no longer a pipe: %v %v", fi.Mode(), err)
	}
	if got := names(t, dir); len(got) != 1 {
		t.Errorf("left beside the pipe: %v", got)
	}
}

func TestDeviceStaysADevice(t *testing.T) {
	before, err := os.Stat(os.DevNull)
	if err != nil || before.Mode()&fs.ModeDevice == 0 {
		t.Skip("no null device")
	}
	if _, err := Write(os.DevNull, true, content("gone")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(os.DevNull)
	if err != nil || after.Mode()&fs.ModeDevice == 0 || !os.SameFile(before, after) {
		t.Fatalf("%s is no longer the device it was", os.DevNull)
	}
}

func TestCheckLedger(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "host.db")
	if err := CheckLedger(file); err != nil {
		t.Errorf("a free name: %v", err)
	}
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckLedger(file); err != nil {
		t.Errorf("a file that exists: %v", err)
	}
	if err := CheckLedger(os.DevNull); !errors.Is(err, ErrNotFile) {
		t.Errorf("a device: %v, want ErrNotFile", err)
	}
	if err := CheckLedger(dir); !errors.Is(err, ErrIsDir) {
		t.Errorf("a directory: %v, want ErrIsDir", err)
	}
	if err := CheckLedger(filepath.Join(dir, "absent", "host.db")); !errors.Is(err, ErrNoDir) {
		t.Errorf("a missing directory: %v, want ErrNoDir", err)
	}
}
