package adapter

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/agenticode/my/internal/model"
)

func TestTally(t *testing.T) {
	var tl Tally
	if tl.Total() != 0 || tl.Notes() != nil {
		t.Fatalf("empty tally: %d, %v", tl.Total(), tl.Notes())
	}
	tl.Skip("mc_event/7", "timestamp is empty")
	tl.Skip("mc_event/9", "timestamp is empty")
	tl.Skip("mce_record/3", "status register is not valid")
	tl.Skip("mc_event/12", "timestamp is empty")
	if tl.Total() != 4 {
		t.Errorf("total = %d, want 4", tl.Total())
	}
	want := []string{
		"skipped 3 records: timestamp is empty (first: mc_event/7)",
		"skipped mce_record/3: status register is not valid",
	}
	if got := tl.Notes(); !reflect.DeepEqual(got, want) {
		t.Errorf("notes = %q, want %q", got, want)
	}
}

func TestStatusForError(t *testing.T) {
	dir := t.TempDir()
	_, missing := os.Open(filepath.Join(dir, "absent"))
	_, notDir := os.Open(filepath.Join(dir, "absent", "deeper"))
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, underFile := os.Open(filepath.Join(file, "child")) // ENOTDIR

	tests := []struct {
		name string
		err  error
		want model.SourceStatus
	}{
		{"no error", nil, model.SourceOK},
		{"missing file", missing, model.SourceAbsent},
		{"missing directory", notDir, model.SourceAbsent},
		{"path below a file", underFile, model.SourceAbsent},
		{"permission", fmt.Errorf("open /sys/firmware/dmi/tables/DMI: %w", fs.ErrPermission), model.SourceUnreadable},
		{"other", errors.New("database disk image is malformed"), model.SourceError},
	}
	for _, tt := range tests {
		got, detail := StatusForError(tt.err)
		if got != tt.want {
			t.Errorf("%s: status = %s, want %s", tt.name, got, tt.want)
		}
		if tt.want != model.SourceOK && detail == "" {
			t.Errorf("%s: no detail", tt.name)
		}
	}
	if _, detail := StatusForError(fs.ErrPermission); detail != "permission denied; readable as root" {
		t.Errorf("permission detail = %q", detail)
	}
}

func TestCollected(t *testing.T) {
	var c Collected
	var s Sink = &c
	for _, err := range []error{
		s.Host(&model.Host{Name: "h"}), s.Boot(&model.Boot{}), s.Module(&model.Module{}),
		s.Counter(&model.CounterSample{}), s.Event(&model.Event{}), s.Action(&model.Action{}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(c.Hosts) != 1 || len(c.Boots) != 1 || len(c.Modules) != 1 || len(c.Counters) != 1 || len(c.Events) != 1 || len(c.Actions) != 1 {
		t.Errorf("collected = %+v", c)
	}
}

func TestCollectedKeepsEveryRecordKind(t *testing.T) {
	var c Collected
	var sink Sink = &c
	steps := []error{
		sink.Host(&model.Host{}), sink.Boot(&model.Boot{}), sink.Module(&model.Module{}), sink.Counter(&model.CounterSample{}),
		sink.Event(&model.Event{}), sink.Action(&model.Action{}), sink.GPU(&model.GPUMem{}), sink.ECS(&model.ECSSample{}),
	}
	for i, err := range steps {
		if err != nil {
			t.Errorf("step %d: %v", i, err)
		}
	}
	got := []int{len(c.Hosts), len(c.Boots), len(c.Modules), len(c.Counters), len(c.Events), len(c.Actions), len(c.GPUs), len(c.ECSs)}
	if !reflect.DeepEqual(got, []int{1, 1, 1, 1, 1, 1, 1, 1}) {
		t.Errorf("records kept per kind = %v", got)
	}
}
