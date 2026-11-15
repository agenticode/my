package main

import (
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// TestImportFMFMLogs builds a case from the boot, runtime and crash log of
// a host. The runtime log names its modules by socket, channel and slot
// number; the boot log gives them a slot name and a serial number.
func TestImportFMFMLogs(t *testing.T) {
	runtime := testutil.Testdata(t, "fmfm", "runtime.json")
	boot := testutil.Testdata(t, "fmfm", "boot.json")
	crash := testutil.Testdata(t, "fmfm", "crash.json")
	code, out, errOut := runEnv(fixed, "import", runtime, boot, crash)
	if code != 3 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	wantLines(t, out,
		runtime+": FMFM log",
		"  fmfm: partly read, 14 records, 6 error records (grade A), 5 not usable",
		// The log holds a record of a second host. It is left out, and the
		// import says so.
		"records of other hosts left out: 1 (r12-n035); this run read the records of r12-n034",
		boot+": FMFM log",
		crash+": FMFM log",
		"host r12-n034: 9 error records and 0 actions added",
		"host finding: replace now",
		" 1  CPU0_C1                 80A1C3   2   2  row ",
		// A place the boot log does not list is named by its numbers.
		" 2  socket1 channel0 dimm0  -        0   1  undetermined ",
	)
}

// TestImportOneHostOfALog reads a log that holds the records of two hosts.
// --host picks the host whose records are read. A name the log does not
// use names the case, and the first host of the log is read.
func TestImportOneHostOfALog(t *testing.T) {
	log := testutil.Testdata(t, "fmfm", "runtime.json")
	code, out, errOut := runEnv(fixed, "import", "--host", "r12-n035", log)
	if code != 1 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	wantLines(t, out,
		"  fmfm: ok, 14 records, 1 error record (grade C)",
		"host r12-n035: 1 error record and 0 actions added",
		" 1  socket0 channel0 dimm0  -        1   0  undetermined ",
	)

	code, out, errOut = runEnv(fixed, "import", "--host", "web-12", log)
	if code != 3 || errOut != "" {
		t.Fatalf("exit code %d, stderr %q\n%s", code, errOut, out)
	}
	wantLines(t, out,
		"records of other hosts left out: 1 (r12-n035); this run read the records of r12-n034, the",
		"host web-12: 6 error records and 0 actions added",
	)
}
