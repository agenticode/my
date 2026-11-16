package klog

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/model"
)

// TestCarriedFromTheLab reads the kernel lines of the lab guest
// (testdata/klog/carried-lab.dmesg, the x86 guest of tools/lab/loop.sh with
// mce=print_all): a fatal machine check exception that panicked the boot,
// and the next boot, which found the record still in bank 7 and printed it
// without " Exception" and with TSC 0, and status-only records QEMU leaves
// in bank 1 of the other processors.
func TestCarriedFromTheLab(t *testing.T) {
	c, rep, _ := collectFile(t, "carried-lab.dmesg", Options{})
	if len(c.Events) != 2 {
		t.Fatalf("%d events, want the exception and the record left in the bank: %+v", len(c.Events), rep)
	}
	exc, left := at(t, c, "L1-L3"), at(t, c, "L11-L14")
	if exc.Carried || exc.ErrorSeverity != model.SevFatal || !exc.ErrorDT.Equal(time.Unix(1794798096, 0)) {
		t.Errorf("the exception: %+v", exc)
	}
	// The TIME of the record left in the bank is when the next boot read
	// it; the ledger moves the record into the boot before.
	if !left.Carried || left.ErrorSeverity != model.SevFatal || !left.ErrorDT.Equal(time.Unix(1794798147, 0)) || left.OsPhysicalAddress != addr(0x12345040) {
		t.Errorf("the record left in the bank: %+v", left)
	}
	if exc.Fingerprint == left.Fingerprint {
		t.Error("one fingerprint for both")
	}
	if !hasNote(rep, "3 machine checks are not memory errors") {
		t.Errorf("notes %q", rep.Notes)
	}
	if len(c.Boots) != 2 {
		t.Errorf("boots %+v", c.Boots)
	}
}

// mceText writes the three lines of __print_mce for a record, at a stamp
// (arch/x86/kernel/cpu/mce/core.c; the forms of the lab capture above).
func mceText(stamp float64, exception bool, status uint64, tsc uint64, unix int64) string {
	exc := ""
	mcg := "0"
	if exception {
		exc, mcg = " Exception", "4"
	}
	return fmt.Sprintf("[%12.6f] mce: [Hardware Error]: CPU 0: Machine Check%s: %s Bank 7: %016x\n", stamp, exc, mcg, status) +
		fmt.Sprintf("[%12.6f] mce: [Hardware Error]: TSC %x ADDR 12345040 MISC 86 \n", stamp+0.001, tsc) +
		fmt.Sprintf("[%12.6f] mce: [Hardware Error]: PROCESSOR 0:50654 TIME %d SOCKET 0 APIC 0 microcode 1\n", stamp+0.002, unix)
}

// later writes lines of a boot that goes on until a stamp.
func later(until float64) string {
	return fmt.Sprintf("[%12.6f] systemd[1]: Reached target multi-user.target - Multi-User System.\n", until)
}

// TestWhatIsNotCarried: records that the running boot logs itself, and the
// exception that ends a boot, stay in their boot.
func TestWhatIsNotCarried(t *testing.T) {
	const (
		fatal   = 0xbe00000000010090 // VAL UC EN MISCV ADDRV PCC
		ucna    = 0xac00000000010090 // VAL UC EN MISCV ADDRV: uncorrected, no action
		srar    = 0xbd80000000100134 // VAL UC EN MISCV ADDRV S AR
		correct = 0x8c00004000010090 // VAL EN MISCV ADDRV, corrected
	)
	boot := "[    0.000000] Linux version 7.3.0-ras\n"
	for _, tt := range []struct {
		name    string
		text    string
		carried bool
	}{
		{"polled corrected record", boot + mceText(4.3, false, correct, 0, 1794798147) + later(60), false},
		{"polled uncorrected record without PCC or S", boot + mceText(4.3, false, ucna, 0, 1794798147) + later(60), false},
		{"polled uncorrected record with PCC", boot + mceText(4.3, false, fatal, 0, 1794798147) + later(60), true},
		{"polled uncorrected record with S", boot + mceText(4.3, false, srar, 0, 1794798147) + later(60), true},
		{"exception that ends the boot", boot + mceText(1539.8, true, fatal, 0x607c3d8fa8d, 1794798096) +
			"[ 1539.831142] Kernel panic - not syncing: Fatal machine check\n", false},
		{"exception with TSC 0 that ends the boot", boot + mceText(1539.8, true, fatal, 0, 1794798096) +
			"[ 1539.831142] Kernel panic - not syncing: Fatal machine check\n", false},
		// Rule 2: MCG_STATUS kept MCIP over the reset, so the record looks
		// like an exception, and the boot goes on after it.
		{"exception with TSC 0 and a boot that goes on", boot + mceText(4.3, true, fatal, 0, 1794798147) + later(60), true},
		{"exception with TSC 0 and PCC, a boot that goes on a few seconds only", boot + mceText(4.3, true, fatal, 0, 1794798147) + later(8), false},
	} {
		c, _ := collectText(t, tt.text, Options{})
		if len(c.Events) != 1 {
			t.Errorf("%s: %d events", tt.name, len(c.Events))
			continue
		}
		if c.Events[0].Carried != tt.carried {
			t.Errorf("%s: carried %v, want %v", tt.name, c.Events[0].Carried, tt.carried)
		}
	}
}

// TestAMDForms: the decode mce_amd prints (drivers/edac/mce_amd.c,
// amd_decode_mce) does not say whether a record was polled or raised, and
// is not carried; the lines of __print_mce of an AMD processor are judged
// like any other.
func TestAMDForms(t *testing.T) {
	const amdDecode = `[    5.100000] [Hardware Error]: Uncorrected, software containable error.
[    5.100100] [Hardware Error]: CPU:0 (19:1:1) MC18_STATUS[-|UE|MiscV|AddrV|PCC|-|SyndV|UECC|-|-|-]: 0xbe20000000020093
[    5.100200] [Hardware Error]: Error Addr: 0x0000000012345040
[    5.100300] [Hardware Error]: IPID: 0x0000009600050f00, Syndrome: 0x000000000a800000
[    5.100400] [Hardware Error]: Unified Memory Controller Ext. Error Code: 0, DRAM ECC error.
[    5.100500] [Hardware Error]: cache level: L3/GEN, tx: GEN, mem-tx: RD
`
	c, _ := collectText(t, "[    0.000000] Linux version 7.3.0\n"+amdDecode+later(60), Options{})
	if len(c.Events) != 1 || c.Events[0].Carried || c.Events[0].Decoder != "mce/amd" {
		t.Errorf("AMD decode: %+v", c.Events)
	}
	amdPrint := strings.ReplaceAll(mceText(5.0, false, 0xbe20000000020093, 0, 1794798147), "PROCESSOR 0:50654", "PROCESSOR 2:a00f11")
	c, _ = collectText(t, "[    0.000000] Linux version 7.3.0\n"+amdPrint+later(60), Options{})
	if len(c.Events) != 1 || !c.Events[0].Carried {
		t.Errorf("__print_mce of AMD: %+v", c.Events)
	}
}

// bertLines is what the kernel prints of a boot error region with one
// fatal memory error, written from the source text of
// drivers/acpi/apei/bert.c (bert_print_all) and drivers/firmware/efi/cper.c
// (cper_estatus_print, cper_estatus_print_section, cper_print_mem,
// cper_mem_err_location, cper_dimm_err_location), with the prefix
// KERN_INFO HW_ERR. No real firmware printed these lines.
const bertLines = `[    7.100000] BERT: Error records from previous boot:
[    7.100100] [Hardware Error]: event severity: fatal
[    7.100200] [Hardware Error]:  Error 0, type: fatal
[    7.100250] [Hardware Error]:  fru_text: CPU0_C1
[    7.100300] [Hardware Error]:   section_type: memory error
[    7.100400] [Hardware Error]:   error_status: Storage error in DRAM memory (0x0000000000000400)
[    7.100500] [Hardware Error]:   physical_address: 0x0000000012345040
[    7.100600] [Hardware Error]:   node:0 card:2 module:1 rank:0 bank_group:1 bank_address:3 device:7 row:6703 column:568
[    7.100700] [Hardware Error]:   error_type: 3, multi-bit ECC
[    7.100800] [Hardware Error]:   DIMM location: not present. DMI handle: 0x1100
[    7.100850] [Hardware Error]: It has been corrected by h/w and requires no further action
[    7.100860] [Hardware Error]: event severity: corrected
[    7.100870] [Hardware Error]:  Error 0, type: corrected
[    7.100880] [Hardware Error]:   section_type: memory error
[    7.100890] [Hardware Error]:   physical_address: 0x0000000077000040
[    7.100895] [Hardware Error]:  Error 1, type: corrected
[    7.100896] [Hardware Error]:   section_type: PCIe error
[    7.100900] BERT: Total records found: 2
`

func TestBERTLines(t *testing.T) {
	text := "[    0.000000] Linux version 7.3.0\n" +
		"[    4.000000] rtc_cmos 00:04: setting system clock to 2026-11-16T03:02:33 UTC (1794798153)\n" +
		bertLines + later(60)
	c, rep := collectText(t, text, Options{})
	if len(c.Events) != 2 {
		t.Fatalf("%d events: %+v", len(c.Events), rep)
	}
	e := c.Events[0]
	if !e.Carried || e.Source != "klog/bert" || e.ErrorSeverity != model.SevFatal || e.Grade != model.GradeA || e.Row != some(6703) ||
		e.Column != some(568) || e.Label != "CPU0_C1" || e.OsPhysicalAddress != addr(0x12345040) || e.SMBIOSHandle != some(0x1100) {
		t.Errorf("fatal: %+v", e)
	}
	if ce := c.Events[1]; !ce.Carried || ce.ErrorSeverity != model.SevCE || ce.OsPhysicalAddress != addr(0x77000040) {
		t.Errorf("corrected: %+v", ce)
	}
	if !hasNote(rep, "1 firmware error sections are not memory errors") {
		t.Errorf("notes %q", rep.Notes)
	}
	// A GHES block of the running boot right after is no part of it.
	ghes := "[   12.000000] {1}[Hardware Error]: Hardware error from APEI Generic Hardware Error Source: 1\n" +
		"[   12.000100] {1}[Hardware Error]: event severity: corrected\n" +
		"[   12.000200] {1}[Hardware Error]:  Error 0, type: corrected\n" +
		"[   12.000300] {1}[Hardware Error]:   section_type: memory error\n" +
		"[   12.000400] {1}[Hardware Error]:   physical_address: 0x0000000012345040\n"
	c, _ = collectText(t, text+ghes, Options{})
	if len(c.Events) != 3 || c.Events[2].Carried || c.Events[2].Source != "klog/ghes" {
		t.Errorf("with a GHES block after: %+v", c.Events)
	}
}

// TestTwoBootsEqualStamp: two boots that print the same record left in a
// bank at the same stamp are two records: the boots read the bank at
// different times. Records that are not carried keep their fingerprints.
func TestTwoBootsEqualStamp(t *testing.T) {
	const fatal = 0xbe00000000010090
	boot := "[    0.000000] Linux version 7.3.0\n"
	text := boot + mceText(4.305277, false, fatal, 0, 1794798147) + later(60) +
		boot + mceText(4.305277, false, fatal, 0, 1794798550) + later(60)
	c, _ := collectText(t, text, Options{})
	if len(c.Events) != 2 || c.Events[0].Fingerprint == c.Events[1].Fingerprint {
		t.Fatalf("events %+v", c.Events)
	}
	// The fingerprint of a record that is not carried is what it was
	// before carried records existed: the stamp and the text of its head.
	exc := boot + mceText(1539.815789, true, fatal, 0x607c3d8fa8d, 1794798096) + "[ 1539.831142] Kernel panic - not syncing: Fatal machine check\n"
	c, _ = collectText(t, exc, Options{})
	want := model.Fingerprint(Kind, "mce", "m", "1539815789", "mce: [Hardware Error]: CPU 0: Machine Check Exception: 4 Bank 7: be00000000010090", "")
	if len(c.Events) != 1 || c.Events[0].Fingerprint != want {
		t.Errorf("fingerprint of the exception changed: %+v", c.Events)
	}
}
