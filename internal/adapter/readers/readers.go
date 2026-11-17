// Package readers lists every reader of the program with a way to make it
// read one input and a valid input for it. The tests of this package feed
// every reader garbage and check that a fault stays inside the reader; a
// test fails when a reader is added and not listed here.
package readers

import (
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/adapter/bert"
	"github.com/agenticode/my/internal/adapter/crashlog"
	"github.com/agenticode/my/internal/adapter/dcbrain"
	"github.com/agenticode/my/internal/adapter/dimmspd"
	"github.com/agenticode/my/internal/adapter/edacsysfs"
	"github.com/agenticode/my/internal/adapter/fmfm"
	"github.com/agenticode/my/internal/adapter/hostinfo"
	"github.com/agenticode/my/internal/adapter/hostmem"
	"github.com/agenticode/my/internal/adapter/klog"
	"github.com/agenticode/my/internal/adapter/mcelog"
	"github.com/agenticode/my/internal/adapter/nvidia"
	"github.com/agenticode/my/internal/adapter/rasdaemon"
	"github.com/agenticode/my/internal/adapter/redfish"
	"github.com/agenticode/my/internal/adapter/sel"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/adapter/vendorlog"
)

// Reader is one reader of one form of input.
type Reader struct {
	// Name tells the reader and the form apart: "sel/text", "klog/journal".
	Name string
	// Package is the name of the package of the reader under
	// internal/adapter.
	Package string
	// Fixture is a valid input, relative to the root of the repository: a
	// file, or for a reader of a tree the directory.
	Fixture string
	// Main is, for a reader of a tree, the file below Fixture that holds
	// what the reader parses. The garbage tests replace it.
	Main string
	// Open returns the reader for the input at path: a file, or for a
	// reader of a tree a copy of Fixture.
	Open func(path string) adapter.Adapter
	// Bad, when set, is an input the program takes for this reader's form
	// and that holds no record: the head of the form, then garbage.
	Bad []byte
	// BadName is the name the file of Bad needs, when the program knows
	// the form by the name.
	BadName string
	// HostPath, when set, is where a copy of a host's files keeps this
	// input; the garbage tests put garbage there.
	HostPath string
}

// When is the time the readers are run as of, so that their results do
// not depend on the day.
var When = time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)

// List returns every reader.
func List() []Reader {
	klogOpts := klog.Options{CapturedAt: When}
	return []Reader{
		{Name: "bert", Package: "bert", Fixture: "testdata/real/6.19.10-bert-loop/lab/region.bin",
			Open: func(p string) adapter.Adapter { return bert.New(p, bert.Options{ReadAt: When}) }},
		{Name: "crashlog/pstore", Package: "crashlog",
			Fixture: "internal/adapter/crashlog/testdata/erst-fedora44/dmesg-erst-7708592270475264001",
			Open: func(p string) adapter.Adapter {
				return crashlog.NewFile(crashlog.KindPstore, p, crashlog.Options{TempDir: os.TempDir()})
			},
			BadName: "dmesg-erst-7708592270475264009", Bad: []byte("Panic#1 Part1\n" + garbageText),
			HostPath: "var/lib/systemd/pstore/dmesg-erst-7708592270475264009"},
		{Name: "crashlog/kdump", Package: "crashlog",
			Fixture: "testdata/real/6.19.10-mce-loop-kdump/var/crash/127.0.0.1-2026-11-16-045505/vmcore-dmesg.txt",
			Open: func(p string) adapter.Adapter {
				return crashlog.NewFile(crashlog.KindKdump, p, crashlog.Options{TempDir: os.TempDir()})
			},
			BadName: "vmcore-dmesg.txt", Bad: []byte(garbageText),
			HostPath: "var/crash/127.0.0.1-2026-11-16-045505/vmcore-dmesg.txt"},
		{Name: "dcbrain", Package: "dcbrain", Fixture: "testdata/dcbrain", Main: "mcelog.csv",
			Open: func(p string) adapter.Adapter {
				ds, err := dcbrain.Open(p)
				if err != nil {
					return adapter.Unrecognised(p, err.Error())
				}
				return dcbrain.NewServer(ds, "s0001", dcbrain.Options{})
			}},
		{Name: "edac-sysfs", Package: "edacsysfs", Fixture: "testdata/edac-sysfs/intel-i10nm", Main: "mc/mc0/dimm0/dimm_ce_count",
			Open: func(p string) adapter.Adapter {
				return edacsysfs.New(os.DirFS(p), "mc", p, edacsysfs.Options{Now: When})
			},
			HostPath: "sys/devices/system/edac/mc/mc0/dimm0/dimm_ce_count"},
		{Name: "fmfm", Package: "fmfm", Fixture: "testdata/fmfm/runtime.json",
			Open: func(p string) adapter.Adapter { return fmfm.NewFile(p, fmfm.Options{Now: When}) },
			Bad:  []byte(`{"ErrorDT": "2026-11-12T03:14:07Z", "Records": [` + garbageText)},
		{Name: "host-memory", Package: "hostmem", Fixture: "testdata/explorer/made-two-nodes", Main: "proc/meminfo",
			Open:     func(p string) adapter.Adapter { return hostmem.New(p, hostmem.Options{Now: When}) },
			HostPath: "proc/meminfo"},
		{Name: "hostinfo", Package: "hostinfo", Fixture: "testdata/real/6.19.10-bert-loop", Main: "proc/cpuinfo",
			Open:     func(p string) adapter.Adapter { return hostinfo.New(os.DirFS(p), p, hostinfo.Options{Now: When}) },
			HostPath: "proc/cpuinfo"},
		{Name: "klog/dmesg", Package: "klog", Fixture: "testdata/klog/intel-skx.messages",
			Open: func(p string) adapter.Adapter { return klog.NewFile(p, klogOpts) },
			Bad:  []byte("[    0.000000] Linux version 6.1.0 (builder@example) #1 SMP\n" + garbageText),
			// The kernel log a copy of a host keeps.
			HostPath: "var/log/kern.log"},
		{Name: "klog/journal", Package: "klog", Fixture: "testdata/klog/amd.journal.json",
			Open: func(p string) adapter.Adapter { return klog.NewFile(p, klogOpts) },
			Bad:  []byte(`{"MESSAGE": "x", "_BOOT_ID": "0123456789abcdef0123456789abcdef", "__REALTIME_TIMESTAMP": "` + garbageText)},
		{Name: "mcelog", Package: "mcelog", Fixture: "testdata/mcelog/client-x3850.txt",
			Open: func(p string) adapter.Adapter { return mcelog.NewFile(p, mcelog.Options{Now: When}) },
			Bad:  []byte("SOCKET 0 CHANNEL 0 DIMM 0\n" + garbageText)},
		{Name: "nvidia/q", Package: "nvidia", Fixture: "testdata/nvidia/nvidia-smi-q.txt",
			Open: func(p string) adapter.Adapter { return nvidia.NewFile(p, nvidia.Options{Now: When}) },
			Bad:  []byte("==============NVSMI LOG==============\n\n" + garbageText)},
		{Name: "nvidia/xml", Package: "nvidia", Fixture: "testdata/nvidia/nvidia-smi-q.xml",
			Open: func(p string) adapter.Adapter { return nvidia.NewFile(p, nvidia.Options{Now: When}) },
			Bad:  []byte(`<?xml version="1.0" ?>` + "\n<nvidia_smi_log>\n<gpu " + garbageText)},
		{Name: "nvidia/csv", Package: "nvidia", Fixture: "testdata/nvidia/query-gpu-rtx2080-470.csv",
			Open: func(p string) adapter.Adapter { return nvidia.NewFile(p, nvidia.Options{Now: When}) }},
		{Name: "rasdaemon", Package: "rasdaemon", Fixture: "testdata/rasdaemon/intel-skx-0.6.sql",
			Open: func(p string) adapter.Adapter {
				return rasdaemon.New(p, rasdaemon.Options{Direct: true, TempDir: os.TempDir()})
			},
			Bad:      append([]byte("SQLite format 3\x00"), []byte(garbageText)...),
			HostPath: "var/lib/rasdaemon/ras-mc_event.db"},
		{Name: "redfish/file", Package: "redfish", Fixture: "testdata/redfish/registry-log.json",
			Open: func(p string) adapter.Adapter { return redfish.New(p, redfish.Options{Now: When}) },
			Bad:  []byte(`{"@odata.type": "#LogEntryCollection.LogEntryCollection", "Members": [` + garbageText)},
		{Name: "redfish/tree", Package: "redfish", Fixture: "testdata/redfish/dell-shaped",
			Main: "redfish/v1/Managers/iDRAC.Embedded.1/LogServices/Sel/Entries/index.json",
			Open: func(p string) adapter.Adapter { return redfish.New(p, redfish.Options{Now: When}) }},
		{Name: "sel/text", Package: "sel", Fixture: "testdata/sel/ipmitool-elist.txt",
			Open: func(p string) adapter.Adapter { return sel.NewFile(p, sel.Options{}) },
			Bad:  []byte("   1 | 11/12/2026 | 03:14:07 | Temperature #0x30 | " + garbageText)},
		{Name: "sel/json", Package: "sel", Fixture: "testdata/sel/sel.json",
			Open: func(p string) adapter.Adapter { return sel.NewFile(p, sel.Options{}) },
			Bad:  []byte(`[{"SensorType": "Memory", "Created": ` + garbageText)},
		{Name: "sel/binary", Package: "sel", Fixture: "testdata/sel/sel-writeraw.hex",
			Open: func(p string) adapter.Adapter { return sel.NewFile(p, sel.Options{}) }},
		{Name: "vendorlog/racadm-getsel", Package: "vendorlog", Fixture: "testdata/vendorlog/racadm-getsel.txt",
			Open: func(p string) adapter.Adapter { return vendorlog.NewFile(p, vendorlog.Options{}) },
			Bad:  []byte("Record:      1\nDate/Time:   11/12/2026 03:14:07\nDescription: " + garbageText)},
		{Name: "vendorlog/racadm-lclog", Package: "vendorlog", Fixture: "testdata/vendorlog/racadm-lclog.txt",
			Open: func(p string) adapter.Adapter { return vendorlog.NewFile(p, vendorlog.Options{}) },
			Bad:  []byte("SeqNumber = 1\nMessage ID = MEM0001\nTimestamp = " + garbageText)},
		{Name: "spd/binary", Package: "dimmspd", Fixture: "testdata/spd/illumos/ddr4-rdimm-64g-2rx4.bin",
			Open: func(p string) adapter.Adapter { return dimmspd.NewFile(p, When) },
			// An SPD a collection of my collect host wrote, or a dump a
			// person put beside it.
			HostPath: "spd/0-0050.bin"},
		{Name: "spd/hex", Package: "dimmspd", Fixture: "testdata/spd/illumos/forms/ddr5-rdimm-64g-2rx4.hexdump-C.txt",
			Open: func(p string) adapter.Adapter { return dimmspd.NewFile(p, When) },
			Bad:  []byte("00000000  30 10 12 01 04 20 00 62  00 00 00 00 a0 01 07 00  |0.... .b........|\n" + garbageText)},
		{Name: "spd/decode-dimms", Package: "dimmspd", Fixture: "testdata/spd/illumos/decode-dimms-4.4.txt",
			Open: func(p string) adapter.Adapter { return dimmspd.NewFile(p, When) },
			Bad:  []byte("Decoding EEPROM: /sys/bus/i2c/drivers/ee1004/0-0050\n\n---=== SPD EEPROM Information ===---\n" + garbageText)},
		{Name: "smbios/dmidecode", Package: "smbios", Fixture: "testdata/smbios/dmidecode-t17-ddr4.txt",
			Open: func(p string) adapter.Adapter { return smbios.NewDmidecodeFile(p, smbios.Options{Now: When}) },
			Bad:  []byte("# dmidecode 3.5\n" + garbageText)},
		{Name: "smbios/tables", Package: "smbios", Fixture: "testdata/smbios/tables-ddr5", Main: "DMI",
			Open: func(p string) adapter.Adapter {
				return smbios.NewTables(os.DirFS(p), ".", p, smbios.Options{Now: When})
			},
			HostPath: "sys/firmware/dmi/tables/DMI"},
	}
}

// garbageText is text that is no record of any form: words, numbers and
// punctuation, valid UTF-8 without NUL bytes, so that a reader of text
// takes it.
const garbageText = "zz9 ## }{ ][ ,,, 0xZZ -- qq: ;; ~~ %%s \\\\ \"\" ''' ... \n" +
	"lorem 12 ipsum 34 dolor !! ?? <> || && ** ++ == \n" +
	"]]]] }}}} :::: 9999999999999999999999999 -0.000e+999 \n"

// CopyTree copies the directory from to the new directory to.
func CopyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
}
