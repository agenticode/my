package exporter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testledger/spdhost"
	"github.com/agenticode/my/internal/testutil"
)

func spdMetricsOf(t *testing.T, root string) *Set {
	t.Helper()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "l.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := scan.Run(ctx, s, scan.Options{Root: root, TempDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	r, err := engine.Run(ctx, s, engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	set, err := Host(ctx, s, r.Hosts[0])
	if err != nil {
		t.Fatal(err)
	}
	return set
}

func lines(set *Set, prefix string) []string {
	var b strings.Builder
	set.WriteTo(&b)
	var out []string
	for _, l := range strings.Split(b.String(), "\n") {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

// The demo case: eight DIMMs with their SPD, eight read, nothing unread.
func TestSPDMetricsOfTheDemoCase(t *testing.T) {
	set := spdMetricsOf(t, testutil.Testdata(t, "spd", "demo"))
	info := lines(set, "my_dimm_spd_info{")
	if len(info) != 8 {
		t.Fatalf("%d series of my_dimm_spd_info:\n%s", len(info), strings.Join(info, "\n"))
	}
	if !strings.Contains(strings.Join(info, "\n"), `my_dimm_spd_info{host="r07-n112",slot="CPU0_C1",serial="4B1770AF",date_code="2022-W43",organisation="2Rx4",part_number="MTC40F2046S1RC48BA1"} 1`) {
		t.Errorf("series:\n%s", strings.Join(info, "\n"))
	}
	if v, ok := set.Value(HostSPDRead, "r07-n112"); !ok || v != 8 {
		t.Errorf("my_host_spd_read %v %v", v, ok)
	}
	if got := lines(set, "my_host_spd_unread{"); len(got) != 0 {
		t.Errorf("unread: %v", got)
	}
	var b strings.Builder
	set.WriteTo(&b)
	testutil.MustParseExposition(t, b.String())
}

// A host with a reason: 1 with the code, no readings; a host whose scans
// never looked for the SPD: no series.
func TestSPDMetricsOfAReasonAndOfNone(t *testing.T) {
	root, _ := spdhost.Host(t, 1)
	set := spdMetricsOf(t, root)
	for _, d := range []*Desc{DIMMSPDInfo, HostSPDRead, HostSPDUnread} {
		if got := lines(set, d.Name+"{"); len(got) != 0 {
			t.Errorf("a host that never looked has %s: %v", d.Name, got)
		}
	}
	if err := os.WriteFile(filepath.Join(root, dimmspd.ProbeFile), []byte("spd-probe 1\nvirtual KVM\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set = spdMetricsOf(t, root)
	if v, ok := set.Value(HostSPDUnread, "r12-n034", "virtual-machine"); !ok || v != 1 {
		t.Errorf("my_host_spd_unread{reason=virtual-machine} %v %v", v, ok)
	}
	if v, ok := set.Value(HostSPDRead, "r12-n034"); !ok || v != 0 {
		t.Errorf("my_host_spd_read %v %v", v, ok)
	}
	if got := lines(set, "my_dimm_spd_info{"); len(got) != 0 {
		t.Errorf("info without a reading: %v", got)
	}
}
