package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var when = time.Date(2026, 11, 15, 4, 17, 53, 0, time.UTC)

// read unpacks an archive into its members, in order.
func read(t *testing.T, data []byte) []*tar.Header {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if zr.Name != "" || !zr.ModTime.IsZero() {
		t.Errorf("the gzip header names %q, time %v", zr.Name, zr.ModTime)
	}
	tr := tar.NewReader(zr)
	var out []*tar.Header
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		h.Linkname = string(body) // kept here for the comparison
		out = append(out, h)
	}
	return out
}

func TestWriteArchive(t *testing.T) {
	members := []member{{"my", 0o755, []byte("binary")}, {"LICENSE", 0o644, []byte("license")}, {"README.md", 0o644, []byte("readme")}}
	var a, b bytes.Buffer
	if err := writeArchive(&a, "my_v0.1.0_linux_amd64", when, members); err != nil {
		t.Fatal(err)
	}
	// The same input at another time of day, in another zone: the same bytes.
	if err := writeArchive(&b, "my_v0.1.0_linux_amd64", when.In(time.FixedZone("KST", 9*3600)).Add(300*time.Millisecond), members); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("two archives of the same files differ")
	}
	got := read(t, a.Bytes())
	want := []struct {
		name string
		mode int64
		body string
	}{
		{"my_v0.1.0_linux_amd64/", 0o755, ""},
		{"my_v0.1.0_linux_amd64/my", 0o755, "binary"},
		{"my_v0.1.0_linux_amd64/LICENSE", 0o644, "license"},
		{"my_v0.1.0_linux_amd64/README.md", 0o644, "readme"},
	}
	if len(got) != len(want) {
		t.Fatalf("%d members, want %d", len(got), len(want))
	}
	for i, w := range want {
		h := got[i]
		if h.Name != w.name || h.Mode != w.mode || h.Linkname != w.body {
			t.Errorf("member %d: %s mode %o %q, want %s mode %o %q", i, h.Name, h.Mode, h.Linkname, w.name, w.mode, w.body)
		}
		// Nothing of the machine that built it.
		if !h.ModTime.Equal(when) || h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" {
			t.Errorf("member %s: time %v, owner %d:%d %q:%q", h.Name, h.ModTime, h.Uid, h.Gid, h.Uname, h.Gname)
		}
	}
	// Another time gives another archive: the time is part of it.
	var c bytes.Buffer
	if err := writeArchive(&c, "my_v0.1.0_linux_amd64", when.Add(time.Hour), members); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Bytes(), c.Bytes()) {
		t.Error("the commit time is not in the archive")
	}
}

func TestChecksums(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"b.tar.gz": "bbb", "a.tar.gz": "aaa"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := checksums(dir, []string{"b.tar.gz", "a.tar.gz"})
	if err != nil {
		t.Fatal(err)
	}
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	// Sorted by name, two spaces between sum and name: the form sha256sum reads.
	if want := sum("aaa") + "  a.tar.gz\n" + sum("bbb") + "  b.tar.gz\n"; string(got) != want {
		t.Errorf("got\n%swant\n%s", got, want)
	}
	if _, err := checksums(dir, []string{"none.tar.gz"}); err == nil {
		t.Error("a missing archive has a checksum")
	}
}

func TestParse(t *testing.T) {
	c, err := parse([]string{"-version", "v0.1.0", "-commit", "0123456789ab", "-date", "2026-11-15T13:17:53+09:00", "-out", "x", "-platforms", "linux/amd64, darwin/arm64"})
	if err != nil {
		t.Fatal(err)
	}
	if c.version != "v0.1.0" || c.commit != "0123456789ab" || !c.date.Equal(when) || c.out != "x" || strings.Join(c.platforms, " ") != "linux/amd64 darwin/arm64" {
		t.Errorf("config = %+v", c)
	}
	if c.archiveName("linux/arm64") != "my_v0.1.0_linux_arm64" {
		t.Errorf("archive name %q", c.archiveName("linux/arm64"))
	}
	if c, _ := parse([]string{"-version", "v1", "-commit", "c", "-date", "2026-11-15T04:17:53Z"}); c == nil || strings.Join(c.platforms, ",") != defaultPlatforms {
		t.Errorf("default platforms: %+v", c)
	}
	for _, bad := range [][]string{
		{"-commit", "c", "-date", "2026-11-15T04:17:53Z"},                                     // no version
		{"-version", "../../x", "-commit", "c", "-date", "2026-11-15T04:17:53Z"},              // a version that is a path
		{"-version", "v1 beta", "-commit", "c", "-date", "2026-11-15T04:17:53Z"},              // a version with a space
		{"-version", "v1", "-date", "2026-11-15T04:17:53Z"},                                   // no commit
		{"-version", "v1", "-commit", "c"},                                                    // no date
		{"-version", "v1", "-commit", "c", "-date", "today"},                                  // a date that is none
		{"-version", "v1", "-commit", "c", "-date", "2026-11-15T04:17:53Z", "-platforms", ""}, // nothing to build
		{"-version", "v1", "-commit", "c", "-date", "2026-11-15T04:17:53Z", "extra"},          // a stray argument
		{"-frob"},
	} {
		if _, err := parse(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}

// TestRun runs the whole command with a stand-in for the go command, which
// writes a file that depends on what it was asked to build: the platform,
// the flags and the environment that matter for the result.
func TestRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in for the go command is a shell script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "go")
	script := `#!/bin/sh
out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then out="$a"; fi
  prev="$a"
done
mkdir -p "$(dirname "$out")"
# Everything but the output path: that is a temporary directory.
args=""
for a in "$@"; do
  if [ "$a" != "$out" ]; then args="$args $a"; fi
done
printf 'built for %s/%s cgo=%s amd64=%s arm64=%s experiment=%s:%s\n' "$GOOS" "$GOARCH" "$CGO_ENABLED" "$GOAMD64" "$GOARM64" "$GOEXPERIMENT" "$args" > "$out"
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "src")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"LICENSE": "the license\n", "README.md": "# readme\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	build := func(out string) string {
		c, err := parse([]string{"-version", "v0.1.0", "-commit", "0123456789ab", "-date", "2026-11-15T04:17:53Z", "-out", out, "-root", root, "-go", fake})
		if err != nil {
			t.Fatal(err)
		}
		var log bytes.Buffer
		if err := c.run(&log); err != nil {
			t.Fatal(err)
		}
		sums, err := os.ReadFile(filepath.Join(out, "SHA256SUMS"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(log.String(), string(sums)) {
			t.Errorf("the log does not show the checksums:\n%s", log.String())
		}
		return string(sums)
	}
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b", "nested")
	sums := build(a)
	if again := build(b); again != sums {
		t.Errorf("two runs differ:\n%s\n%s", sums, again)
	}
	lines := strings.Split(strings.TrimSpace(sums), "\n")
	if len(lines) != 3 {
		t.Fatalf("checksums:\n%s", sums)
	}
	for i, name := range []string{"my_v0.1.0_darwin_arm64.tar.gz", "my_v0.1.0_linux_amd64.tar.gz", "my_v0.1.0_linux_arm64.tar.gz"} {
		sum, file, _ := strings.Cut(lines[i], "  ")
		data, err := os.ReadFile(filepath.Join(a, name))
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(data)
		if file != name || sum != hex.EncodeToString(h[:]) {
			t.Errorf("line %d: %q, want the checksum of %s", i, lines[i], name)
		}
		members := read(t, data)
		dirName := strings.TrimSuffix(name, ".tar.gz")
		if len(members) != 4 || members[1].Name != dirName+"/my" || members[1].Mode != 0o755 || members[2].Linkname != "the license\n" || members[3].Linkname != "# readme\n" {
			t.Errorf("%s: %d members", name, len(members))
			continue
		}
		// The binary was built for the platform of the archive, without
		// cgo, for the baseline instruction sets, with the stamps and
		// without anything of the machine.
		bin := members[1].Linkname
		platform := strings.Replace(strings.TrimPrefix(dirName, "my_v0.1.0_"), "_", "/", 1)
		for _, want := range []string{
			"built for " + platform + " cgo=0 amd64=v1 arm64=v8.0 experiment=:",
			" build -trimpath -buildvcs=false -ldflags -s -w -buildid= ",
			"internal/buildinfo.Version=v0.1.0", "internal/buildinfo.Commit=0123456789ab", "internal/buildinfo.Date=2026-11-15T04:17:53Z",
			" -o ./cmd/my",
		} {
			if !strings.Contains(bin, want) {
				t.Errorf("%s: the build lacks %q:\n%s", name, want, bin)
			}
		}
	}
	// Nothing else is written, and no temporary file is left in the output.
	entries, _ := os.ReadDir(a)
	if len(entries) != 4 {
		t.Errorf("%d files in the output directory, want 3 archives and SHA256SUMS", len(entries))
	}

	// A build that fails stops the run and says why.
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'cannot find package' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, _ := parse([]string{"-version", "v0.1.0", "-commit", "c", "-date", "2026-11-15T04:17:53Z", "-out", filepath.Join(dir, "c"), "-root", root, "-go", fake})
	if err := c.run(io.Discard); err == nil || !strings.Contains(err.Error(), "cannot find package") {
		t.Errorf("a failed build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "c", "SHA256SUMS")); err == nil {
		t.Error("checksums were written after a failed build")
	}
	// A platform that is none, and a tree without a license.
	c, _ = parse([]string{"-version", "v0.1.0", "-commit", "c", "-date", "2026-11-15T04:17:53Z", "-out", filepath.Join(dir, "d"), "-root", root, "-go", fake, "-platforms", "plan9"})
	if err := c.run(io.Discard); err == nil || !strings.Contains(err.Error(), "want os/arch") {
		t.Errorf("a platform without an architecture: %v", err)
	}
	c, _ = parse([]string{"-version", "v0.1.0", "-commit", "c", "-date", "2026-11-15T04:17:53Z", "-out", filepath.Join(dir, "e"), "-root", dir, "-go", fake})
	if err := c.run(io.Discard); err == nil {
		t.Error("a tree without a license was packed")
	}
}
