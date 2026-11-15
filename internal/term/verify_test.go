package term

import (
	"bytes"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/injection"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testcapture"
	"github.com/agenticode/my/internal/view"
)

// verification draws the comparison of a plan with the capture of
// firmware error records.
func verification(t *testing.T, ledger, host, plan string, v variant) string {
	t.Helper()
	s, err := store.Open(ctx, ledger, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := view.Open(ctx, s, v.st.lang(), engine.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host(host)
	if err != nil {
		t.Fatal(err)
	}
	p, err := injection.ReadFile(plan)
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.Verify(ctx, h, p)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := Verification(&b, m, v.st); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestVerificationScreens(t *testing.T) {
	ledger, host := testcapture.Ledger(t, testcapture.GHES)
	bad := testcapture.MismatchedPlan(t)
	for _, v := range []variant{en, ko, ascii, color, w60, w140} {
		check(t, "verify-mismatch", v, verification(t, ledger, host, bad, v))
	}
	for _, v := range all {
		check(t, "verify-ok", v, verification(t, ledger, host, testcapture.PlanPath(t, testcapture.GHES), v))
	}

	plain := verification(t, ledger, host, bad, en)
	for _, want := range []string{
		"host lab-arm  plan ghes",
		"4 of 25 injections were not found as planned",
		"  2  row-2         CE                        1      1  decoded differently",
		" 24  ghost-1       UE                        1      0  not found",
		" 25  stay-1        page offline hard         0      0  as planned",
		" what differs  row-2: row: injected 0x1a30, recorded 0x1a2f in 3 records (rasdaemon/mc_event",
		"               row-2: column: injected 0x119, recorded 0x118 in 3 records (rasdaemon/mc_event",
		"               ghost-1: no record of the host matches: UE at 0x1ffff0000 DIMM 0",
		" records       klog/edac 26, klog/ghes 21, klog/offline 2, rasdaemon/mc_event 26",
		" method        QMP inject-ghes-v2-error of QEMU",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("screen lacks %q:\n%s", want, plain)
		}
	}

	// With room, the list names the place of every injection.
	wide := verification(t, ledger, host, bad, w140)
	if !strings.Contains(wide, "WHERE") || !strings.Contains(wide, "  1  row-1         CE                  0x100010140 DIMM 0 rank0 dev7 bg1") {
		t.Errorf("wide screen:\n%s", wide)
	}

	ok := verification(t, ledger, host, testcapture.PlanPath(t, testcapture.GHES), en)
	if !strings.Contains(ok, "all 23 injections were found as planned") || strings.Contains(ok, "what differs") {
		t.Errorf("screen of a plan that holds:\n%s", ok)
	}
}
