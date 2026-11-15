// Command dist builds the release archives of MemorY.
//
// For each platform it builds the my binary and packs it with the license
// and the README into a tar.gz archive; then it writes the SHA-256
// checksums of the archives to SHA256SUMS.
//
// The output is reproducible: the same source at the same commit, built
// with the same Go toolchain, gives the same bytes on any machine. The
// binary is built without paths of the build machine and without a build
// ID, and the archive carries the time of the commit, no user names and a
// fixed order of members. It is written by this program and not by the
// tar of the machine, because GNU tar and BSD tar write different bytes
// for the same files.
//
// Usage:
//
//	go run ./tools/dist -version v0.1.0 -commit 0123456789ab -date 2026-11-15T04:00:00Z -out dist
//
// The Makefile target "dist" fills in the values from git.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// stamps is the import path of the package that holds the build stamps.
const stamps = "github.com/agenticode/my/internal/buildinfo"

// defaultPlatforms are the platforms a release is built for.
const defaultPlatforms = "linux/amd64,linux/arm64,darwin/arm64"

// member is one file of an archive.
type member struct {
	name string
	mode int64
	data []byte
}

// writeArchive writes members as a gzip-compressed tar archive below the
// directory dir. Every header field that could differ between two machines
// is fixed: the time is the one given, owner and group are 0 and unnamed.
func writeArchive(w io.Writer, dir string, when time.Time, members []member) error {
	// The gzip header holds no file name and no time.
	zw, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(zw)
	when = when.UTC().Truncate(time.Second)
	hdr := func(name string, mode int64, size int64, typ byte) *tar.Header {
		return &tar.Header{Name: name, Mode: mode, Size: size, Typeflag: typ, ModTime: when, Format: tar.FormatUSTAR}
	}
	if err := tw.WriteHeader(hdr(dir+"/", 0o755, 0, tar.TypeDir)); err != nil {
		return err
	}
	for _, m := range members {
		if err := tw.WriteHeader(hdr(dir+"/"+m.name, m.mode, int64(len(m.data)), tar.TypeReg)); err != nil {
			return err
		}
		if _, err := tw.Write(m.data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

// checksums returns the content of a SHA256SUMS file for the named files
// of a directory, in the form sha256sum -c and shasum -a 256 -c read.
func checksums(dir string, names []string) ([]byte, error) {
	sort.Strings(names)
	var b bytes.Buffer
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return b.Bytes(), nil
}

// versionRE is what a version may look like: it becomes part of file
// names.
var versionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// config is what a run of the command builds.
type config struct {
	version, commit string
	date            time.Time
	out             string
	platforms       []string
	// root is the root of the source tree.
	root string
	// gocmd is the go command to build with.
	gocmd string
}

// archiveName names the archive of a platform, without its extension.
func (c *config) archiveName(platform string) string {
	return "my_" + c.version + "_" + strings.ReplaceAll(platform, "/", "_")
}

// build builds the binary of one platform and returns its bytes.
func (c *config) build(platform string, tmp string) ([]byte, error) {
	goos, goarch, ok := strings.Cut(platform, "/")
	if !ok || goos == "" || goarch == "" {
		return nil, fmt.Errorf("platform %q: want os/arch", platform)
	}
	bin := filepath.Join(tmp, strings.ReplaceAll(platform, "/", "-"), "my")
	ldflags := fmt.Sprintf("-s -w -buildid= -X %[1]s.Version=%[2]s -X %[1]s.Commit=%[3]s -X %[1]s.Date=%[4]s",
		stamps, c.version, c.commit, c.date.UTC().Format(time.RFC3339))
	cmd := exec.Command(c.gocmd, "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", bin, "./cmd/my")
	cmd.Dir = c.root
	// What the environment could change about the code the compiler
	// writes is set here: no cgo, the baseline instruction sets, no
	// experiments.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch, "GOAMD64=v1", "GOARM64=v8.0", "GOEXPERIMENT=")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build %s: %v\n%s", platform, err, out)
	}
	return os.ReadFile(bin)
}

// run builds every archive and the checksum file.
func (c *config) run(log io.Writer) error {
	if err := os.MkdirAll(c.out, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "my-dist-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	var docs []member
	for _, name := range []string{"LICENSE", "README.md"} {
		data, err := os.ReadFile(filepath.Join(c.root, name))
		if err != nil {
			return err
		}
		docs = append(docs, member{name, 0o644, data})
	}
	var names []string
	for _, p := range c.platforms {
		bin, err := c.build(p, tmp)
		if err != nil {
			return err
		}
		dir := c.archiveName(p)
		var b bytes.Buffer
		if err := writeArchive(&b, dir, c.date, append([]member{{"my", 0o755, bin}}, docs...)); err != nil {
			return err
		}
		name := dir + ".tar.gz"
		if err := os.WriteFile(filepath.Join(c.out, name), b.Bytes(), 0o644); err != nil {
			return err
		}
		names = append(names, name)
		fmt.Fprintf(log, "%s  %d bytes\n", name, b.Len())
	}
	sums, err := checksums(c.out, names)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(c.out, "SHA256SUMS"), sums, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(log, "SHA256SUMS\n%s", sums)
	return nil
}

// parse reads the command line.
func parse(args []string) (*config, error) {
	fs := flag.NewFlagSet("dist", flag.ContinueOnError)
	c := &config{}
	var date, platforms string
	fs.StringVar(&c.version, "version", "", "version of the release, for example v0.1.0")
	fs.StringVar(&c.commit, "commit", "", "commit the release is built from")
	fs.StringVar(&date, "date", "", "time of that commit (RFC 3339); it is stamped into the binary and the archives")
	fs.StringVar(&c.out, "out", "dist", "directory to write the archives to")
	fs.StringVar(&platforms, "platforms", defaultPlatforms, "platforms to build, as os/arch separated by commas")
	fs.StringVar(&c.root, "root", ".", "root of the source tree")
	fs.StringVar(&c.gocmd, "go", "go", "go command to build with")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	switch {
	case fs.NArg() > 0:
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	case !versionRE.MatchString(c.version):
		return nil, fmt.Errorf("-version %q: want a version such as v0.1.0", c.version)
	case c.commit == "":
		return nil, errors.New("-commit is missing")
	}
	t, err := time.Parse(time.RFC3339, date)
	if err != nil {
		return nil, fmt.Errorf("-date %q: want a time such as 2026-11-15T04:00:00Z", date)
	}
	c.date = t
	for _, p := range strings.Split(platforms, ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.platforms = append(c.platforms, p)
		}
	}
	if len(c.platforms) == 0 {
		return nil, errors.New("-platforms is empty")
	}
	return c, nil
}

func main() {
	c, err := parse(os.Args[1:])
	if err == nil {
		err = c.run(os.Stdout)
	}
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "dist:", err)
		}
		os.Exit(1)
	}
}
