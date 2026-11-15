package term

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/view"
)

// comparison draws the two cases of a pair side by side.
func comparison(t *testing.T, pair string, v variant, opts view.CompareOptions) string {
	t.Helper()
	before, after := testledger.DiffPair(t, pair).Paths(t)
	open := func(path string) (*view.Case, *engine.HostResult) {
		s, err := store.Open(ctx, path, store.ReadOnly)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		c, err := view.Open(ctx, s, v.st.lang(), engine.Options{Now: now})
		if err != nil {
			t.Fatal(err)
		}
		h, err := c.Host("")
		if err != nil {
			t.Fatal(err)
		}
		return c, h
	}
	a, ha := open(before)
	b, hb := open(after)
	cmp, err := view.Compare(ctx, a, ha, b, hb, opts)
	if err != nil {
		t.Fatal(err)
	}
	cmp.Before.File, cmp.After.File = "before.mycase", "after.mycase"
	var buf bytes.Buffer
	if err := Comparison(&buf, cmp, v.st); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestComparisonScreens(t *testing.T) {
	// Two scans of a host whose DIMMs were swapped: the errors went with
	// the DIMM, and the later scan counts the old errors of the slot for
	// the DIMM that came.
	for _, v := range []variant{en, ko, ascii, color, w60, w140} {
		check(t, "diff-moved-scan", v, comparison(t, "moved-scan", v, view.CompareOptions{}))
	}
	// The errors stayed with the slot; a replaced DIMM; new firmware; a
	// reseat on record.
	for _, v := range all {
		check(t, "diff-slot", v, comparison(t, "slot", v, view.CompareOptions{}))
		check(t, "diff-replaced", v, comparison(t, "replaced", v, view.CompareOptions{}))
	}
	check(t, "diff-firmware", en, comparison(t, "firmware", en, view.CompareOptions{}))
	check(t, "diff-reseat", ko, comparison(t, "reseat", ko, view.CompareOptions{}))
	check(t, "diff-all", en, comparison(t, "to-empty", en, view.CompareOptions{All: true, At: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)}))

	plain := comparison(t, "moved-scan", en, view.CompareOptions{})
	for _, want := range []string{
		"host synth-moved-dimm  before.mycase 2026-09-28 12:00 UTC  → after.mycase 2026-11-13 00:00 UTC",
		"45 days apart  host finding: consider replacement",
		"After counts from 2026-10-01 03:00 UTC, the last restart before the later case was taken: 42 days.",
		" changes  moved    S/N 880018, CPU0_C1 → CPU0_D1",
		"          moved    S/N E7A387, CPU0_D1 → CPU0_C1",
		"          restart  1 after the earlier case: 2026-10-01 03:00 UTC",
		" DIMM        SLOT               BEFORE  LAST 41D  BETWEEN  AFTER 42D  FINDING",
		" S/N 880018  CPU0_C1 → CPU0_D1  114 CE    114 CE     5 CE     121 CE  consider replacement",
		" S/N E7A387  CPU0_D1 → CPU0_C1       0         0        0          0  ok → observe",
		" 9 DIMMs in the same slot and without errors in both cases are not listed; --all lists them",
		" reading  S/N 880018: the errors moved with the DIMM. 114 in CPU0_C1 in the 41 days before, 121 in",
		" notes    5 errors lie after the earlier case and before 2026-10-01 03:00 UTC (CPU0_C1 5), in",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("screen lacks %q:\n%s", want, plain)
		}
	}

	// A narrow terminal keeps the DIMM, its slots, the errors after and
	// the finding of the later case.
	narrow := comparison(t, "slot", w60, view.CompareOptions{})
	for _, want := range []string{
		" DIMM        SLOT               AFTER 42D  FINDING",
		" S/N E7A387  CPU0_D1 → CPU0_C1     121 CE  hold replacement",
		" S/N 880018  CPU0_C1 → CPU0_D1          0  hold replacement",
	} {
		if !strings.Contains(narrow, want) {
			t.Errorf("narrow screen lacks %q:\n%s", want, narrow)
		}
	}
	// With slot names too long for that, the slot the DIMM is in now.
	long := &view.Comparison{Host: "h", DIMMs: []view.ComparedDIMM{
		{Name: "S/N ..3B9C56", SlotBefore: "CPU1_DIMM_A1", SlotAfter: "CPU1_DIMM_B1", Change: view.ChangeMoved,
			GradeBefore: "consider-replacement", GradeBeforeText: "consider replacement", GradeAfter: "replace-now", GradeAfterText: "replace now"},
		{Name: "S/N ..77A001", SlotBefore: "CPU1_DIMM_C1", Change: view.ChangeRemoved, GradeBefore: "observe", GradeBeforeText: "observe"},
	}}
	var b bytes.Buffer
	if err := Comparison(&b, long, w60.st); err != nil {
		t.Fatal(err)
	}
	inspect(t, "long slot names", w60, b.String())
	for _, want := range []string{
		" DIMM          SLOT              AFTER 0M  FINDING",
		" S/N ..3B9C56  CPU1_DIMM_B1             0  replace now",
		" S/N ..77A001  CPU1_DIMM_C1 → -         -  -",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("screen with long slot names lacks %q:\n%s", want, b.String())
		}
	}

	// A DIMM that left and the one that came have no number for the case
	// they are not in.
	replaced := comparison(t, "replaced", ascii, view.CompareOptions{})
	for _, want := range []string{
		" changes  removed  S/N 01EAE2, was in CPU1_A1",
		"          added    S/N 5A11C3, in CPU1_A1",
		" S/N 5A11C3  - -> CPU1_A1       -         -        -          0  - -> ok",
		" S/N 01EAE2  CPU1_A1 -> -   43 CE     43 CE    18 CE          -  consider replacement -> -",
	} {
		if !strings.Contains(replaced, want) {
			t.Errorf("screen lacks %q:\n%s", want, replaced)
		}
	}

	// Nothing differs between a case and itself.
	_, after := testledger.DiffPair(t, "moved").Paths(t)
	s, err := store.Open(ctx, after, store.ReadOnly)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := view.Open(ctx, s, en.st.lang(), engine.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := c.Host("")
	cmp, err := view.Compare(ctx, c, h, c, h, view.CompareOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Comparison(&buf, cmp, en.st); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), " changes  none on record: the DIMMs are in the same slots") {
		t.Errorf("a case beside itself:\n%s", buf.String())
	}
}
