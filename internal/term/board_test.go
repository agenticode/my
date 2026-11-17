package term

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/bundle"
	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/i18n"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/scan"
	"github.com/agenticode/my/internal/store"
	"github.com/agenticode/my/internal/testledger"
	"github.com/agenticode/my/internal/testutil"
	"github.com/agenticode/my/internal/view"
)

var boardNow = time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)

// boardOfStore builds the board of the only host of a ledger.
func boardOfStore(t *testing.T, s *store.Store, lang i18n.Lang) *view.Board {
	t.Helper()
	c, err := view.Open(context.Background(), s, lang, engine.Options{Now: boardNow})
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.Host("")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Board(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// importBoard imports one input into a ledger.
func importBoard(t *testing.T, path string) *store.Store {
	t.Helper()
	ctx := context.Background()
	as, _, err := scan.Detect(ctx, path, scan.DetectOptions{Now: boardNow, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "ledger.db"), store.Create)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := ingest.Run(ctx, s, as, ingest.Options{Kind: model.ScanImport, Now: boardNow, Host: "board-host"}); err != nil {
		t.Fatal(err)
	}
	return s
}

// twoBy24 is dmidecode text of a board of two sockets of twelve channels
// with two slots each, written after the format of dmidecode.
func twoBy24(t *testing.T) string {
	var b strings.Builder
	b.WriteString("# dmidecode 3.6\n\nHandle 0x0001, DMI type 1, 27 bytes\nSystem Information\n\tManufacturer: Example Systems\n\tProduct Name: E2\n\n")
	b.WriteString("Handle 0x0400, DMI type 4, 48 bytes\nProcessor Information\n\tVersion: Example CPU\n\nHandle 0x0401, DMI type 4, 48 bytes\nProcessor Information\n\tVersion: Example CPU\n\n")
	n := 0
	for s := 0; s < 2; s++ {
		for c := 0; c < 12; c++ {
			for d := 0; d < 2; d++ {
				size := "32 GB"
				if d == 1 {
					size = "No Module Installed"
				}
				fmt.Fprintf(&b, "Handle 0x%04X, DMI type 17, 92 bytes\nMemory Device\n\tSize: %s\n\tLocator: CPU%d_DIMM_%c%d\n\tBank Locator: P%d_Node0_Channel%d_Dimm%d\n\tType: DDR5\n\tSpeed: 4800 MT/s\n\tManufacturer: Example Memory\n\tSerial Number: EX%06d\n\tPart Number: EXM32G32R2X4\n\n",
					0x1100+n, size, s, 'A'+c, d+1, s, c, d, n)
				n++
			}
		}
	}
	p := filepath.Join(t.TempDir(), "dmidecode.txt")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// asideBoard is dmidecode text of a board of two sockets whose slot names
// give neither a socket nor a channel.
func asideBoard(t *testing.T) string {
	var b strings.Builder
	b.WriteString("# dmidecode 3.6\n\nHandle 0x0001, DMI type 1, 27 bytes\nSystem Information\n\tManufacturer: Example Systems\n\tProduct Name: E3\n\n")
	for s := 0; s < 2; s++ {
		fmt.Fprintf(&b, "Handle 0x%04X, DMI type 4, 48 bytes\nProcessor Information\n\tVersion: Example CPU\n\tStatus: Populated, Enabled\n\n", 0x0400+s)
	}
	for n := 0; n < 8; n++ {
		size := "32 GB"
		if n%2 == 1 {
			size = "No Module Installed"
		}
		fmt.Fprintf(&b, "Handle 0x%04X, DMI type 17, 92 bytes\nMemory Device\n\tSize: %s\n\tLocator: DIMM %d\n\tBank Locator: Bank %d\n\tType: DDR4\n\tSpeed: 3200 MT/s\n\tManufacturer: Example Memory\n\tSerial Number: EX%06d\n\tPart Number: EXM32G32R2X4\n\n",
			0x1100+n, size, n+1, n+1, n)
	}
	p := filepath.Join(t.TempDir(), "dmidecode.txt")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBoardScreens(t *testing.T) {
	ctx := context.Background()
	bc, err := bundle.Open(ctx, testutil.Testdata(t, "real", "6.19.10-ghes-multi", "my", "case.mycase"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bc.Close()
	capture, err := store.Open(ctx, bc.Ledger, store.ReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	cases := map[string]*store.Store{
		"capture":    capture,
		"demo":       testledger.Open(t, "demo", 1, ""),
		"2x24":       importBoard(t, twoBy24(t)),
		"family":     importBoard(t, testutil.Testdata(t, "explorer", "linuxhw-thinksystem-sr650-7x06cto1ww-5d8ab0", "dmidecode.txt")),
		"nochannels": importBoard(t, testutil.Testdata(t, "explorer", "netbox-hp-proliant-bl460c-gen10-graphics-exp", "dmidecode.txt")),
		"nodes":      importBoard(t, testutil.Testdata(t, "explorer", "linuxhw-r1g3d32-ec54ac", "dmidecode.txt")),
		"aside":      importBoard(t, asideBoard(t)),
		"chipset":    importBoard(t, testutil.Testdata(t, "explorer", "linuxhw-poweredge-2950-121ae0", "dmidecode.txt")),
		"labels":     importBoard(t, testutil.Testdata(t, "explorer", "netbox-sm-ssg-6028r", "dmidecode.txt")),
		// The demo case of the population check: marks and two findings.
		"population": importBoard(t, testutil.Testdata(t, "explorer", "population-demo", "dmidecode.txt")),
	}
	for name, s := range cases {
		for _, lang := range []i18n.Lang{i18n.EN, i18n.KO} {
			b := boardOfStore(t, s, lang)
			for _, w := range []int{80, 104, 160} {
				for _, style := range []Style{{Lang: lang, Width: w}, {Lang: lang, Width: w, ASCII: true}, {Lang: lang, Width: w, Detail: true}} {
					var out bytes.Buffer
					if err := Board(&out, b, style); err != nil {
						t.Fatal(err)
					}
					for _, line := range strings.Split(out.String(), "\n") {
						if Width(line) > w {
							t.Errorf("%s %s %d: line of %d columns: %q", name, lang, w, Width(line), line)
						}
					}
					tag := ""
					switch {
					case style.ASCII:
						tag = "-ascii"
					case style.Detail:
						tag = "-detail"
					}
					// Every width of the capture, of the demo case of the
					// population check, of a board whose modules stand as
					// its table has them (family) and of one without a
					// rule set (aside); one width of the others.
					if name != "capture" && name != "population" && name != "family" && name != "aside" && w != 104 {
						continue
					}
					testutil.Golden(t, fmt.Sprintf("board/%s-%s-%d%s.txt", name, lang, w, tag), out.Bytes())
				}
			}
		}
	}
}

// The legend names only what the strips draw.
func TestBoardLegend(t *testing.T) {
	s := importBoard(t, twoBy24(t))
	b := boardOfStore(t, s, i18n.EN)
	var out bytes.Buffer
	if err := Board(&out, b, Style{Lang: i18n.EN, Width: 104}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, absent := range []string{"replace now", "consider replacement", "observe", "corrected"} {
		if strings.Contains(text, absent) {
			t.Errorf("the legend names %q, which the board does not draw:\n%s", absent, text)
		}
	}
	for _, present := range []string{"ok", "empty slot"} {
		if !strings.Contains(text, present) {
			t.Errorf("the legend lacks %q", present)
		}
	}
}
