package view

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/store"
)

// TestTimelineSaysWhatStoppedTheHost: the uncorrected error that stopped
// the host says so, and the next boot says why it started.
func TestTimelineSaysWhatStoppedTheHost(t *testing.T) {
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "stop.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	ev := func(fp string, at time.Time, sev model.Severity, boot string) *model.Event {
		return &model.Event{Host: "h", Kind: model.KindError, ErrorDT: at, ErrorSeverity: sev, Slot: "A1", Count: 1, BootID: boot,
			Source: "klog/mce", RawRef: fp, Grade: model.GradeC, TimeQuality: model.TimeExact, Fingerprint: fp}
	}
	err = s.Update(ctx, func(w *store.Writer) error {
		if err := w.UpsertHost(&model.Host{Name: "h"}); err != nil {
			return err
		}
		for _, b := range []model.Boot{
			{Host: "h", BootID: "b0", StartedAt: day.Add(-30 * 24 * time.Hour), LastSeen: day.Add(10 * time.Hour), StartQuality: model.TimeExact},
			{Host: "h", BootID: "b1", StartedAt: day.Add(11 * time.Hour), LastSeen: day.Add(12 * time.Hour), StartQuality: model.TimeExact},
			{Host: "h", BootID: "b2", StartedAt: day.Add(13 * time.Hour), LastSeen: day.Add(14 * time.Hour), StartQuality: model.TimeExact},
			{Host: "h", BootID: "b3", StartedAt: day.Add(30 * time.Hour), StartQuality: model.TimeExact},
		} {
			if _, err := w.UpsertBoot(&b); err != nil {
				return err
			}
		}
		mce := ev("mce", day.Add(12*time.Hour), model.SevFatal, "b1")
		stop := ev("stop", day.Add(12*time.Hour+time.Millisecond), model.SevInformational, "b1")
		stop.Source, stop.ErrorType, stop.CauseFingerprint, stop.Slot = "klog/stop", model.ErrorTypeStop, "mce", ""
		other := ev("other", day.Add(13*time.Hour+30*time.Minute), model.SevUE, "b2")
		panic2 := ev("panic", day.Add(14*time.Hour), model.SevInformational, "b2")
		panic2.Source, panic2.ErrorType, panic2.Slot = "klog/panic", model.ErrorTypePanic, ""
		for _, e := range []*model.Event{mce, stop, other, panic2} {
			if _, err := w.InsertEvent(e); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.BootOutcomes(ctx, "h"); err != nil {
		t.Fatal(err)
	}
	marks := func(lang i18n.Lang) (groups map[string]string, each []string) {
		c, err := Open(ctx, s, lang, engine.Options{Now: now})
		if err != nil {
			t.Fatal(err)
		}
		h, err := c.Host("")
		if err != nil {
			t.Fatal(err)
		}
		tl, err := c.Timeline(ctx, h, nil, TimelineOptions{})
		if err != nil {
			t.Fatal(err)
		}
		groups = map[string]string{}
		for _, g := range tl.Markers {
			groups[g.Kind] = g.Text.Text
			for _, m := range g.Marks {
				each = append(each, m.Text.Text)
			}
		}
		return groups, each
	}
	g, each := marks(i18n.EN)
	if g[MarkBoot] != "3 reboots, 1 after a hardware error stopped the host" || g[MarkUE] != "2 UE on 2026-10-01; 1 of them stopped the host" {
		t.Errorf("groups %q", g)
	}
	want := []string{"2026-10-01 reboot", "2026-10-01 reboot after a hardware error stopped the host", "2026-10-02 reboot after a kernel panic",
		"A1: 2026-10-01 UE, it stopped the host", "A1: 2026-10-01 UE"}
	for _, w := range want {
		found := false
		for _, e := range each {
			found = found || e == w
		}
		if !found {
			t.Errorf("no mark %q in %q", w, each)
		}
	}
	g, _ = marks(i18n.KO)
	if g[MarkBoot] != "재부팅 3회, 그중 1회는 하드웨어 오류로 호스트가 중단된 뒤" || g[MarkUE] != "2026-10-01 UE 2건, 그중 1건으로 호스트 중단" {
		t.Errorf("Korean groups %q", g)
	}
}
