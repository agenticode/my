package scan

import (
	"path/filepath"
	"testing"

	"github.com/agenticode/my/internal/adapter/bert"
	"github.com/agenticode/my/internal/decode/cper"
	"github.com/agenticode/my/internal/model"
)

// region is a boot error region made from the layouts of the ACPI
// specification (cper.StatusBlockBytes): one fatal memory error.
func region() string {
	return string(cper.StatusBlockBytes(0, cper.SevFatal, cper.Entry{SectionType: cper.SectionPlatformMemory, Severity: cper.SevFatal,
		Data: cper.MemSection(cper.MemError{PhysicalAddress: model.Some(uint64(0x12345040)), Row: model.Some(0x1a2f), Column: model.Some(0x238), Bank: model.Some(3)})}))
}

// TestBootErrorRegionOfACopy: a copy of a host's files with the region is
// read, and its record is carried into the boot before.
func TestBootErrorRegionOfACopy(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "proc/sys/kernel/hostname"), "lab-guest\n", 0o644)
	write(t, filepath.Join(root, bert.Path), region(), 0o400)
	opts := Options{Root: root}
	if !HoldsErrorSource(&opts) {
		t.Fatal("a copy with a boot error region holds no source of error records")
	}
	s := ledger(t)
	res, err := Run(ctx, s, opts)
	if err != nil {
		t.Fatal(err)
	}
	src := source(t, res.Sources, bert.Kind)
	if src.Status != model.SourceOK || src.Events != 1 || !Reads(src) {
		t.Errorf("bert: %+v", src)
	}
	if Reads(model.Source{Kind: bert.Kind, Status: model.SourceEmpty}) {
		t.Error("an empty region counts as a source of error records")
	}
}

func TestDetectBootErrorRegion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "BERT")
	write(t, p, region(), 0o644)
	as, what, err := Detect(ctx, p, DetectOptions{})
	if err != nil || what != WhatBERT || kinds(as) != bert.Kind {
		t.Errorf("%q %q %v", kinds(as), what, err)
	}
}
