package preflight

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// host reads the files of the host the checks look at: the running host,
// or a copy of its files below root. It only reads. Paths are relative to
// the root and use slashes.
type host struct {
	root string
	live bool
	// procs returns the command names of the running processes, from
	// /proc/<pid>/comm on a live host.
	procs func() ([]string, error)
	// freeBytes returns the space a user without privileges may still
	// write on the file system that holds path.
	freeBytes func(path string) (uint64, error)
}

// path is where rel is on this machine.
func (h *host) path(rel string) string {
	return filepath.Join(h.root, filepath.FromSlash(rel))
}

// maxSmall bounds the files read whole: sysfs attributes, configuration
// files, /proc/cmdline. The kernel gives an attribute at most a page; a
// copy of a host's files can hold anything.
const maxSmall = 256 << 10

// read returns the contents of a small file, its trailing white space
// removed. A file that is not there returns fs.ErrNotExist; one that is
// closed to this user returns fs.ErrPermission.
func (h *host) read(rel string) (string, error) {
	f, err := os.Open(h.path(rel))
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSmall+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxSmall {
		return "", errTooLarge
	}
	return strings.TrimRight(string(b), " \t\r\n"), nil
}

var errTooLarge = errors.New("file is larger than 256 KiB")

// head returns the first n bytes of a file.
func (h *host) head(rel string, n int64) (string, error) {
	f, err := os.Open(h.path(rel))
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// exists reports whether rel is there, without following a symbolic link
// at its end. An error other than "not there" (a directory closed to this
// user) is returned.
func (h *host) exists(rel string) (bool, error) {
	_, err := os.Lstat(h.path(rel))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return false, err
}

// there is exists without the error: a path that cannot be looked at
// counts as not there.
func (h *host) there(rel string) bool {
	ok, _ := h.exists(rel)
	return ok
}

// isDir reports whether rel is a directory (a link to one counts).
func (h *host) isDir(rel string) bool {
	fi, err := os.Stat(h.path(rel))
	return err == nil && fi.IsDir()
}

// list returns the names in a directory, sorted.
func (h *host) list(rel string) ([]string, error) {
	entries, err := os.ReadDir(h.path(rel))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// readlink returns the target of a symbolic link, or "".
func (h *host) readlink(rel string) string {
	t, err := os.Readlink(h.path(rel))
	if err != nil {
		return ""
	}
	return t
}

// denied reports whether err says that the file is closed to this user.
func denied(err error) bool { return errors.Is(err, fs.ErrPermission) }

// missing reports whether err says that the file is not there.
func missing(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// cmdline is the kernel command line split into its parameters.
type cmdline []string

// parseCmdline splits a kernel command line the way the kernel does
// (lib/cmdline.c, next_arg): at spaces outside double quotes.
func parseCmdline(s string) cmdline {
	var out cmdline
	var b strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case (r == ' ' || r == '\t' || r == '\n') && !quoted:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// values returns the values given to a parameter, in order: "mce=bootlog
// mce=print_all" gives both for "mce". A parameter without "=" gives "".
func (c cmdline) values(key string) []string {
	var out []string
	for _, p := range c {
		k, v, _ := strings.Cut(p, "=")
		if k == key {
			out = append(out, v)
		}
	}
	return out
}

// has reports whether the parameter is given, with or without a value.
func (c cmdline) has(key string) bool { return len(c.values(key)) > 0 }

// hasValue reports whether key=value is given.
func (c cmdline) hasValue(key, value string) bool {
	for _, v := range c.values(key) {
		if v == value {
			return true
		}
	}
	return false
}

// cpu is the processor as /proc/cpuinfo describes its first CPU.
type cpu struct {
	vendor string // "GenuineIntel", "AuthenticAMD", "HygonGenuine", ...
	family int
	model  int
	known  bool
}

// parseCPUInfo reads the first processor block of /proc/cpuinfo
// (arch/x86/kernel/cpu/proc.c, show_cpuinfo: "vendor_id", "cpu family",
// "model").
func parseCPUInfo(s string) cpu {
	var c cpu
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	seen := 0
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if seen > 0 {
				break
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "vendor_id":
			c.vendor, seen = v, seen+1
		case "cpu family":
			if n, ok := atoi(v); ok {
				c.family, seen = n, seen+1
			}
		case "model":
			if n, ok := atoi(v); ok {
				c.model, seen = n, seen+1
			}
		}
	}
	c.known = c.vendor != "" && seen >= 3
	return c
}

func atoi(s string) (int, bool) {
	n := 0
	if s == "" || len(s) > 9 {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// kernelConfig returns the build options of the running kernel from
// /boot/config-<release>, or from /proc/config.gz (kernel/configs.c,
// CONFIG_IKCONFIG_PROC). An option that is not set is missing from the
// map. The second result names the file, or is "" when neither was read.
func (h *host) kernelConfig(release string) (map[string]string, string) {
	if release != "" && !strings.ContainsAny(release, "/\x00") {
		rel := "boot/config-" + release
		if s, err := h.read(rel); err == nil {
			return parseConfig(s), rel
		}
		// The configuration of a distribution kernel can be larger than
		// what read takes: read it line by line.
		if f, err := os.Open(h.path(rel)); err == nil {
			defer f.Close()
			return parseConfigReader(f), rel
		}
	}
	f, err := os.Open(h.path("proc/config.gz"))
	if err != nil {
		return nil, ""
	}
	defer f.Close()
	zr, err := gzip.NewReader(io.LimitReader(f, 4<<20))
	if err != nil {
		return nil, ""
	}
	return parseConfigReader(io.LimitReader(zr, 16<<20)), "proc/config.gz"
}

func parseConfig(s string) map[string]string { return parseConfigReader(strings.NewReader(s)) }

func parseConfigReader(r io.Reader) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.HasPrefix(line, []byte("CONFIG_")) {
			continue
		}
		k, v, ok := bytes.Cut(line, []byte("="))
		if ok {
			out[string(k)] = strings.Trim(string(v), `"`)
		}
	}
	return out
}

// osRelease is what /etc/os-release says about the distribution
// (os-release(5)).
type osRelease struct {
	id, like, pretty string
}

func (h *host) osRelease() osRelease {
	s, err := h.read("etc/os-release")
	if err != nil {
		if s, err = h.read("usr/lib/os-release"); err != nil {
			return osRelease{}
		}
	}
	var o osRelease
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			o.id = strings.ToLower(v)
		case "ID_LIKE":
			o.like = strings.ToLower(v)
		case "PRETTY_NAME":
			o.pretty = v
		}
	}
	return o
}

// is reports whether the distribution is one of names or derived from one.
func (o osRelease) is(names ...string) bool {
	words := append([]string{o.id}, strings.Fields(o.like)...)
	for _, w := range words {
		for _, n := range names {
			if w == n {
				return true
			}
		}
	}
	return false
}

// unitDirs are where systemd looks for unit files of the system, in the
// order it prefers them (systemd.unit(5), "System Unit Search Path").
var unitDirs = []string{
	"etc/systemd/system",
	"run/systemd/system",
	"usr/local/lib/systemd/system",
	"usr/lib/systemd/system",
	"lib/systemd/system",
}

// unit is what the files of a host say about a systemd unit.
type unit struct {
	name    string
	present bool   // a unit file is there
	file    string // the unit file that is used
	masked  bool   // linked to /dev/null in /etc or /run
	enabled string // the .wants link that pulls it in, or ""
}

// unit looks a unit up in the search path and in the .wants directories
// of the targets that may pull it in.
func (h *host) unit(name string, targets ...string) unit {
	u := unit{name: name}
	for _, dir := range unitDirs {
		rel := dir + "/" + name
		if !h.there(rel) {
			continue
		}
		if t := h.readlink(rel); t == "/dev/null" && (dir == "etc/systemd/system" || dir == "run/systemd/system") {
			u.masked = true
			continue
		}
		if !u.present {
			u.present, u.file = true, rel
		}
	}
	for _, target := range targets {
		for _, dir := range unitDirs {
			rel := dir + "/" + target + ".wants/" + name
			if h.there(rel) {
				u.enabled = rel
				return u
			}
		}
	}
	return u
}

// systemdConfig reads a configuration file of systemd with its drop-ins
// the way systemd does (systemd.syntax(7) and the "Configuration
// Directories and Precedence" section of journald.conf(5)): the main file
// is the first of /etc, /run, /usr/local/lib and /usr/lib that exists;
// drop-ins of all four directories are applied after it in the order of
// their file names, and a drop-in of a name that an earlier directory also
// has is replaced by that one. The result maps "Section.Key" to the last
// value. The second result lists the files read.
func (h *host) systemdConfig(name string) (map[string]string, []string) {
	dirs := []string{"etc/systemd", "run/systemd", "usr/local/lib/systemd", "usr/lib/systemd"}
	out := map[string]string{}
	var read []string
	for _, d := range dirs {
		if s, err := h.read(d + "/" + name); err == nil {
			parseINI(s, out)
			read = append(read, d+"/"+name)
			break
		}
	}
	dropins := map[string]string{}
	for _, d := range dirs {
		names, err := h.list(d + "/" + name + ".d")
		if err != nil {
			continue
		}
		for _, n := range names {
			if !strings.HasSuffix(n, ".conf") {
				continue
			}
			if _, taken := dropins[n]; !taken {
				dropins[n] = d + "/" + name + ".d/" + n
			}
		}
	}
	order := make([]string, 0, len(dropins))
	for n := range dropins {
		order = append(order, n)
	}
	sort.Strings(order)
	for _, n := range order {
		if s, err := h.read(dropins[n]); err == nil {
			parseINI(s, out)
			read = append(read, dropins[n])
		}
	}
	return out, read
}

// parseINI reads "[Section]" and "Key=Value" lines into out.
func parseINI(s string, out map[string]string) {
	section := ""
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || line[0] == '#' || line[0] == ';':
		case line[0] == '[' && strings.HasSuffix(line, "]"):
			section = line[1 : len(line)-1]
		default:
			k, v, ok := strings.Cut(line, "=")
			if ok {
				out[section+"."+strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
}

// processNames reads the command names of the running processes from
// /proc/<pid>/comm (fs/proc/base.c, comm_show).
func (h *host) processNames() ([]string, error) {
	entries, err := os.ReadDir(h.path("proc"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if _, ok := atoi(e.Name()); !ok {
			continue
		}
		if s, err := h.read("proc/" + e.Name() + "/comm"); err == nil {
			out = append(out, s)
		}
	}
	return out, nil
}
