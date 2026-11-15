package term

import (
	"bytes"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/view"
)

// gpus draws the GPU ledger: every GPU, or the one named.
func gpus(t *testing.T, v variant, opts view.GPUOptions) string {
	t.Helper()
	c, err := view.Open(ctx, testledger.OpenGPU(t), v.st.lang(), engine.Options{Now: testledger.GPUNow})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := c.GPUs(ctx, h, opts)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := GPUs(&b, m, v.st); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestGPUScreens(t *testing.T) {
	for _, v := range []variant{en, ko, ascii, color, w60, w140} {
		check(t, "gpu", v, gpus(t, v, view.GPUOptions{}))
	}
	for _, v := range all {
		// One GPU: the counts behind its budget and its Xid records.
		check(t, "gpu-one", v, gpus(t, v, view.GPUOptions{Ref: "0"}))
		// A GPU no rule speaks about.
		check(t, "gpu-pending", v, gpus(t, v, view.GPUOptions{Ref: "2"}))
	}

	plain := gpus(t, en, view.GPUOptions{})
	for _, want := range []string{
		"host gpu-01  8 GPUs with memory records  Xid records of the 30 days to 2026-11-14",
		"GPU finding: replace now",
		"   0  ..000412  48x1 63x1 64x1  0 / 2        no       yes     repair failed   replace now R-REP-01",
		"   7  ..000716  -               3 / 1 pages  -        -       repaired        -",
		" GPU 3  |max 637 ",
		"|partial 1 ▒|low 1 ▓|none 1 █|  640 banks",
		" · max   ░ high   ▒ partial   ▓ low   █ none",
		"   next      Reset the GPU, or reboot the host: the pending remapping takes effect then.",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("screen lacks %q:\n%s", want, plain)
		}
	}
	// A GPU without a bank histogram has no bar, and the list of every
	// GPU does not list single Xid records.
	if strings.Contains(plain, " GPU 7  |") || strings.Contains(plain, "   Xid ") {
		t.Errorf("screen:\n%s", plain)
	}

	one := gpus(t, en, view.GPUOptions{Ref: "0"})
	for _, want := range []string{
		"host gpu-01  1 GPU with memory records",
		"GPU-b850f46d-d5ea-c752-ddf3-c4453e44d3f7",
		"   rows      0 remapped after correctable errors, 2 after uncorrectable errors",
		"   ECC       frame buffer memory: 31 corrected and 2 uncorrected errors in the life of the GPU, 0",
		"   Xid       2026-11-12 09:30 UTC 64",
		"             2026-11-12 03:14 UTC 48 An uncorrectable double bit error (DBE) has been detected",
	} {
		if !strings.Contains(one, want) {
			t.Errorf("screen of one GPU lacks %q:\n%s", want, one)
		}
	}

	// No rule fired for the GPU shown: the screen says so and names no grade.
	pending := gpus(t, en, view.GPUOptions{Ref: "2"})
	if !strings.Contains(pending, "no rule fired: only a failed row remapping has one (R-REP-01)") || strings.Contains(pending, "GPU finding") {
		t.Errorf("screen of a GPU without a finding:\n%s", pending)
	}
}

// A host without GPU records says so in one line.
func TestGPUScreenWithoutGPUs(t *testing.T) {
	for _, v := range all {
		s := open(t, "demo", "", v)
		m, err := s.c.GPUs(ctx, s.h, view.GPUOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := GPUs(&b, m, v.st); err != nil {
			t.Fatal(err)
		}
		check(t, "gpu-none", v, b.String())
	}
}
