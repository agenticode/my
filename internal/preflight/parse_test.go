package preflight

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseCmdline(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   \t\n", nil},
		{"ro quiet", []string{"ro", "quiet"}},
		{"a=1\tb=2\n", []string{"a=1", "b=2"}},
		{`dyndbg="file x.c +p" mce=print_all`, []string{"dyndbg=file x.c +p", "mce=print_all"}},
		{`unterminated="a b`, []string{"unterminated=a b"}},
		{"mce=bootlog mce=print_all mce", []string{"mce=bootlog", "mce=print_all", "mce"}},
		{"\x00\xff=1", []string{"\x00�=1"}},
	} {
		got := parseCmdline(tt.in)
		if len(got) == 0 && len(tt.want) == 0 {
			continue
		}
		if !reflect.DeepEqual([]string(got), tt.want) {
			t.Errorf("parseCmdline(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	c := parseCmdline("mce=bootlog mce=print_all mce console=ttyS0,115200 console=tty0 erst_disable")
	if v := c.values("mce"); !reflect.DeepEqual(v, []string{"bootlog", "print_all", ""}) {
		t.Errorf("values(mce) = %q", v)
	}
	if !c.hasValue("mce", "print_all") || c.hasValue("mce", "off") || !c.has("erst_disable") || c.has("erst") {
		t.Error("has / hasValue")
	}
}

func TestParseCPUInfo(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want cpu
	}{
		{"", cpu{}},
		{"garbage\n:::\n", cpu{}},
		{"vendor_id : GenuineIntel\ncpu family : 6\nmodel : 143\n", cpu{"GenuineIntel", 6, 143, true}},
		{"vendor_id : GenuineIntel\ncpu family : six\nmodel : 143\n", cpu{vendor: "GenuineIntel", model: 143}},
		{"vendor_id : AuthenticAMD\ncpu family : 25\nmodel : 1\nmodel name : AMD EPYC\n\nvendor_id : GenuineIntel\n", cpu{"AuthenticAMD", 25, 1, true}},
		{"vendor_id : X\ncpu family : 99999999999\nmodel : 1\n", cpu{vendor: "X", model: 1}},
		{strings.Repeat("x", 2<<20), cpu{}},
	} {
		if got := parseCPUInfo(tt.in); got != tt.want {
			t.Errorf("parseCPUInfo(%.40q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseINI(t *testing.T) {
	out := map[string]string{}
	parseINI("Storage=volatile\n[Journal\n[Journal]\n# Storage=none\n; x=y\n Storage = persistent \nbroken line\n=nokey\n[PStore]\nUnlink=no\n", out)
	want := map[string]string{".Storage": "volatile", "Journal.Storage": "persistent", "Journal.": "nokey", "PStore.Unlink": "no"}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("parseINI = %q, want %q", out, want)
	}
	for _, s := range []string{"yes", "Y", "true", "1", "on"} {
		if !parseBool(s, false) {
			t.Errorf("parseBool(%q)", s)
		}
	}
	for _, s := range []string{"no", "N", "false", "0", "off"} {
		if parseBool(s, true) {
			t.Errorf("parseBool(%q)", s)
		}
	}
	if !parseBool("maybe", true) || parseBool("", false) {
		t.Error("parseBool of a word it does not know keeps the default")
	}
}

func TestOSRelease(t *testing.T) {
	root := t.TempDir()
	h := &host{root: root}
	if o := h.osRelease(); o != (osRelease{}) {
		t.Errorf("no file: %+v", o)
	}
	os.MkdirAll(filepath.Join(root, "usr/lib"), 0o755)
	os.WriteFile(filepath.Join(root, "usr/lib/os-release"), []byte("ID='Rocky'\nID_LIKE=\"RHEL centos fedora\"\nPRETTY_NAME\n=x\n"), 0o644)
	o := h.osRelease()
	if o.id != "rocky" || !o.is("fedora") || o.is("debian") {
		t.Errorf("os-release = %+v", o)
	}
}

func TestKernelConfig(t *testing.T) {
	root := t.TempDir()
	h := &host{root: root}
	if cfg, file := h.kernelConfig("../../etc"); cfg != nil || file != "" {
		t.Errorf("a release with a slash is read: %v %q", cfg, file)
	}
	os.MkdirAll(filepath.Join(root, "proc"), 0o755)
	os.WriteFile(filepath.Join(root, "proc/config.gz"), []byte("not gzip"), 0o644)
	if cfg, _ := h.kernelConfig("6.8"); cfg != nil {
		t.Errorf("broken config.gz gave %v", cfg)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte("# comment\nCONFIG_PSTORE=y\nCONFIG_X=\"a b\"\nCONFIG_BROKEN\n# CONFIG_X86_MCELOG_LEGACY is not set\n"))
	zw.Close()
	os.WriteFile(filepath.Join(root, "proc/config.gz"), buf.Bytes(), 0o644)
	cfg, file := h.kernelConfig("6.8")
	if file != "proc/config.gz" || cfg["CONFIG_PSTORE"] != "y" || cfg["CONFIG_X"] != "a b" || cfg["CONFIG_X86_MCELOG_LEGACY"] != "" || len(cfg) != 2 {
		t.Errorf("config.gz: %q %v", file, cfg)
	}
	// /boot comes first, and may be larger than a small file.
	os.MkdirAll(filepath.Join(root, "boot"), 0o755)
	big := strings.Repeat("# filler line of a distribution config\n", 10000) + "CONFIG_PSTORE=m\n"
	os.WriteFile(filepath.Join(root, "boot/config-6.8"), []byte(big), 0o644)
	if cfg, file := h.kernelConfig("6.8"); file != "boot/config-6.8" || cfg["CONFIG_PSTORE"] != "m" {
		t.Errorf("boot config: %q %v", file, cfg)
	}
}

func TestHasFlag(t *testing.T) {
	for _, tt := range []struct {
		cmd  string
		want bool
	}{
		{"/usr/sbin/rasdaemon -f -r", true},
		{"/usr/sbin/rasdaemon -fr", true},
		{"/usr/sbin/rasdaemon --foreground --record", true},
		{"/usr/sbin/rasdaemon -f", false},
		{"/usr/sbin/rasdaemon --recordx", false},
		{"/usr/sbin/rasdaemon -f --rr", false},
		{"", false},
	} {
		if got := hasFlag(tt.cmd, "-r", "--record"); got != tt.want {
			t.Errorf("hasFlag(%q) = %v", tt.cmd, got)
		}
	}
}
