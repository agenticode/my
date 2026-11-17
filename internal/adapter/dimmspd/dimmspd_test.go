package dimmspd

import (
	"bytes"
	"context"
	"github.com/agenticode/my/internal/adapter/dimmspd/spdtest"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/spd"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

var ctx = context.Background()

var now = time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)

const i801 = "adapter i2c-0 \"SMBus I801 adapter at 0000:00:1f.4\"\npci 0000:00:1f.4 id=8086:a323 driver=i801_smbus"

// reasonProbes are probe texts with the reason each must give: the first
// 29 reach all 18 codes in the order of the table.
var reasonProbes = []struct {
	want, mem, text string
}{
	{ReasonVirtual, "", "virtual VMware"},
	{ReasonNoI2C, "", "i2c absent"},
	{ReasonNoI2C, "", "i2c absent\npci 0000:00:1f.4 id=8086:a323 driver=-\ndriver i801_smbus absent"},
	{ReasonSMBusNotLoaded, "", "i2c present\npci 0000:00:1f.4 id=8086:a323 driver=-\ndriver i801_smbus module"},
	{ReasonSMBusNotBuilt, "", "i2c present\npci 0000:00:14.0 id=1022:790b driver=-\ndriver piix4_smbus absent"},
	{ReasonSMBusRefused, "", "i2c present\npci 0000:00:1f.4 id=8086:a323 driver=-\ndriver i801_smbus registered"},
	{ReasonSMBusUnknown, "", "i2c present\npci 0000:00:1f.4 id=8086:a323 driver=-\ndriver i801_smbus unknown"},
	{ReasonNoSMBusController, "", "i2c present\nadapter i2c-3 \"Synopsys DesignWare I2C adapter\"\nbmc present"},
	{ReasonNoSMBusController, "", "i2c present"},
	{ReasonNoSPDDevice, "DDR4", "i2c present\n" + i801 + "\ndriver ee1004 registered\nbmc present"},
	{ReasonNoSPDDevice, "DDR5", "i2c present\n" + i801 + " spd-wd=0\ndriver spd5118 registered"},
	{ReasonWriteDisabled, "DDR5", "i2c present\n" + i801 + " spd-wd=1\ndriver spd5118 registered"},
	{ReasonNoSPDDevice, "DDR4", "i2c present\n" + i801 + " spd-wd=1\ndriver ee1004 registered"},
	{ReasonSPDDriverNotLoaded, "DDR4", "i2c present\n" + i801 + "\ndriver ee1004 module\nclient 0-0050 name=ee1004 driver=- eeprom=- read=0 state=unbound"},
	{ReasonSPDDriverNotBuilt, "DDR5", "i2c present\n" + i801 + "\ndriver spd5118 absent\nclient 0-0050 name=spd5118 driver=- eeprom=- read=0 state=unbound"},
	{ReasonSPDDriverUnknown, "DDR4", "i2c present\n" + i801 + "\ndriver ee1004 unknown\nclient 0-0050 name=ee1004 driver=- eeprom=- read=0 state=unbound"},
	{ReasonSPDDriverRefused, "DDR5", "i2c present\n" + i801 + "\ndriver spd5118 registered\nclient 0-0050 name=spd5118 driver=- eeprom=- read=0 state=unbound"},
	{ReasonWriteDisabled, "DDR5", "i2c present\n" + i801 + " spd-wd=1\ndriver spd5118 registered\nclient 0-0050 name=spd5118 driver=- eeprom=- read=0 state=unbound"},
	{ReasonSPDDriverRefused, "DDR4", "i2c present\n" + i801 + " spd-wd=1\ndriver ee1004 registered\nclient 0-0050 name=ee1004 driver=- eeprom=- read=0 state=unbound"},
	{ReasonNoEEPROMFile, "DDR5", "i2c present\n" + i801 + "\ndriver spd5118 registered\nclient 0-0050 name=spd5118 driver=spd5118 eeprom=- read=0 state=no-file"},
	{ReasonReadDenied, "DDR4", "i2c present\n" + i801 + "\nclient 0-0050 name=ee1004 driver=ee1004 eeprom=512 read=0 state=denied errno=EACCES"},
	{ReasonReadFailed, "DDR4", "i2c present\n" + i801 + "\nclient 0-0050 name=ee1004 driver=ee1004 eeprom=512 read=0 state=io errno=ENXIO\nclient 0-0051 name=ee1004 driver=ee1004 eeprom=512 read=0 state=denied errno=EACCES"},
	{ReasonReadFailed, "DDR4", "i2c present\n" + i801 + "\nclient 0-0050 name=ee1004 driver=ee1004 eeprom=512 read=0 state=timeout"},
	{ReasonNotDecoded, "DDR3", "i2c present\n" + i801 + "\nclient 0-0050 name=spd driver=at24 eeprom=256 read=256 state=type"},
	{ReasonBlank, "DDR4", "i2c present\n" + i801 + "\nclient 0-0050 name=ee1004 driver=ee1004 eeprom=512 read=512 state=blank"},
	{"", "DDR5", "i2c present\n" + i801 + " spd-wd=1\nclient 0-0050 name=spd5118 driver=spd5118 eeprom=1024 read=128 state=short errno=EIO"},
	{"", "DDR4", "i2c present\n" + i801 + "\nclient 0-0050 name=ee1004 driver=ee1004 eeprom=512 read=512 state=ok\nclient 0-0051 name=ee1004 driver=- eeprom=- read=0 state=unbound"},
	{"", "DDR4", "i2c present\n" + i801 + "\nclient 0-0050 name=ee1004 driver=ee1004 eeprom=512 read=512 state=crc"},
	// A controller without driver beside unbound clients on another bus:
	// the clients decide.
	{ReasonSPDDriverNotLoaded, "DDR4", "i2c present\npci 0000:00:1f.4 id=8086:a323 driver=-\ndriver i801_smbus module\nadapter i2c-1 \"SMBus PIIX4 adapter port 0 at 0b00\"\ndriver ee1004 module\nclient 1-0050 name=ee1004 driver=- eeprom=- read=0 state=unbound"},
}

// TestReasonOrder: every probe gives its reason, all 18 codes are reached,
// and the text of a parsed probe is the probe again.
func TestReasonOrder(t *testing.T) {
	seen := map[string]bool{}
	for i, c := range reasonProbes {
		p := ParseProbe("spd-probe 1\n" + c.text)
		got := Reason(p, c.mem)
		if got != c.want {
			t.Errorf("probe %d: reason %q, want %q\n%s", i+1, got, c.want, c.text)
		}
		seen[got] = true
		if again := ParseProbe(p.Text()); Reason(again, c.mem) != got {
			t.Errorf("probe %d: the text of the parsed probe gives another reason:\n%s", i+1, p.Text())
		}
	}
	if len(reasonProbes) != 29 {
		t.Errorf("%d probes", len(reasonProbes))
	}
	for _, code := range Reasons {
		if !seen[code] {
			t.Errorf("no probe reaches %s", code)
		}
	}
	if len(Reasons) != 18 {
		t.Errorf("%d codes", len(Reasons))
	}
	// A virtual machine is never a host without SPD driver, also with
	// clients in its probe.
	if got := Reason(ParseProbe("spd-probe 1\nvirtual KVM\nclient 0-0050 name=ee1004 driver=- eeprom=- read=0 state=unbound"), "DDR4"); got != ReasonVirtual {
		t.Errorf("a virtual machine with clients: %s", got)
	}
	// Short texts: each code has one, and the list of every short text
	// holds the module and driver names.
	for _, code := range Reasons {
		if s := Short(&Probe{}, code); s == "" || s == code {
			t.Errorf("%s has no short text", code)
		}
	}
	all := strings.Join(ShortTexts(), "\n")
	for _, want := range []string{"SMBus driver i2c_i801 not loaded", "kernel built without i2c_piix4", "SPD driver ee1004 not loaded", "kernel built without spd5118", "SPD driver at24 not loaded"} {
		if !strings.Contains(all, want) {
			t.Errorf("short texts lack %q", want)
		}
	}
}

func dumpOf(t *testing.T, name string) []byte {
	return testutil.ReadTestdata(t, "spd", "illumos", name+".bin")
}

func modTree() *spdtest.Tree { return spdtest.New().Modules(spdtest.Release, spdtest.AllModules, nil) }

func hosts(t *testing.T) []spdtest.Host {
	return spdtest.Hosts(dumpOf(t, "ddr4-rdimm-64g-2rx4"), dumpOf(t, "ddr5-rdimm-64g-2rx4"), testutil.ReadTestdata(t, "spd", "rigred", "ddr3-sodimm.bin"))
}

func run(t *testing.T, h spdtest.Host) (*spdtest.FS, *adapter.Collected, adapter.Report) {
	t.Helper()
	fsys := spdtest.NewFS(t, h.Tree)
	for k, v := range h.Faults {
		fsys.Faults[k] = v
	}
	a := NewLive(fsys, "/", LiveOptions{Now: now, Privileged: h.Privileged, Release: spdtest.Release, Deadline: spdtest.Deadline}, h.Mem)
	var c adapter.Collected
	rep := a.Collect(ctx, &c)
	fsys.CheckAsked(t, h.Privileged)
	return fsys, &c, rep
}

// TestReasons and TestStates: one made host per reason and per state of a
// reading. The probe text of each is a golden file; the reason, the status
// of the source and the states of the readings are those of the table.
func TestReasonsAndStates(t *testing.T) {
	reached := map[string]bool{}
	states := map[string]bool{}
	for _, h := range hosts(t) {
		t.Run(h.Name, func(t *testing.T) {
			_, c, rep := run(t, h)
			if len(c.Readings) != 1 || c.Readings[0].Kind != model.ReadingSPDProbe {
				t.Fatalf("readings %+v", c.Readings)
			}
			text := c.Readings[0].Text
			testutil.Golden(t, "probe/"+h.Name+".txt", []byte(text))
			got := Reason(ParseProbe(text), h.Mem)
			if got != h.Reason {
				t.Errorf("reason %q, want %q\n%s", got, h.Reason, text)
			}
			reached[got] = true
			if rep.Status != h.Status {
				t.Errorf("status %s, want %s (%s)", rep.Status, h.Status, rep.Detail)
			}
			if h.Reason != "" && rep.Detail != Short(ParseProbe(text), h.Reason) {
				t.Errorf("detail %q", rep.Detail)
			}
			if h.Reason == "" && rep.Detail != "" {
				t.Errorf("a host with a reading has the detail %q", rep.Detail)
			}
			got2 := map[string]string{}
			for _, r := range c.SPDs {
				got2[strings.TrimSuffix(strings.TrimPrefix(r.Origin, "/sys/bus/i2c/devices/"), "/eeprom")] = r.State
				states[r.State] = true
				if r.Form != model.SPDFormEEPROM || r.Bus != "i2c-0" || r.Address < 0x50 || r.LastSeen != now {
					t.Errorf("reading %+v", r)
				}
				if (r.State == spd.StateDenied || r.State == spd.StateIO || r.State == spd.StateTimeout) && len(r.Raw) > 0 {
					t.Errorf("a read that failed before the first byte keeps %d bytes", len(r.Raw))
				}
			}
			for id, want := range h.States {
				if got2[id] != want {
					t.Errorf("%s: state %q, want %q (all %v)", id, got2[id], want, got2)
				}
			}
			if len(got2) != len(h.States) {
				t.Errorf("readings %v, want %v", got2, h.States)
			}
		})
	}
	for _, code := range Reasons {
		if code != ReasonVirtual && !reached[code] {
			t.Errorf("no made host gives %s", code)
		}
	}
	for _, s := range spd.States {
		if !states[s] {
			t.Errorf("no made host gives the state %s", s)
		}
	}
}

// TestVirtualMachine: a virtual machine is probed no further than its
// first line; MY_DEBUG=spd-vm lets the probe run there.
func TestVirtualMachine(t *testing.T) {
	tr := modTree().I2C().Adapter("0", "SMBus I801 adapter at 0000:00:1f.4").PCI("0000:00:1f.4", "8086", "a323", "i801_smbus").
		Client("0-0050", "ee1004", "ee1004", dumpOf(t, "ddr4-rdimm-64g-2rx4"))
	fsys := spdtest.NewFS(t, tr)
	var c adapter.Collected
	rep := NewLive(fsys, "/", LiveOptions{Now: now, Virtual: "VMware", Release: spdtest.Release}, "").Collect(ctx, &c)
	if len(fsys.Asked()) != 0 {
		t.Errorf("a virtual machine was probed: %v", fsys.Asked())
	}
	if rep.Status != model.SourceAbsent || rep.Detail != "virtual machine" || len(c.SPDs) != 0 {
		t.Errorf("report %+v", rep)
	}
	if c.Readings[0].Text != "spd-probe 1\nvirtual VMware\n" {
		t.Errorf("probe %q", c.Readings[0].Text)
	}
	t.Setenv("MY_DEBUG", "spd-vm")
	var c2 adapter.Collected
	rep = NewLive(fsys, "/", LiveOptions{Now: now, Virtual: "VMware", AllowVirtual: DebugVirtual(), Release: spdtest.Release}, "").Collect(ctx, &c2)
	if rep.Status != model.SourceOK || len(c2.SPDs) != 1 {
		t.Errorf("MY_DEBUG=spd-vm: %+v", rep)
	}
}

// TestWriteDisableAsRoot: the configuration byte is read as root and not
// read without.
func TestWriteDisableAsRoot(t *testing.T) {
	build := func() *spdtest.Tree {
		return modTree().I2C().Adapter("0", "SMBus I801 adapter at 0000:00:1f.4").PCI("0000:00:1f.4", "8086", "a323", "i801_smbus").
			Config("0000:00:1f.4", 0x10).Registered("i2c", "spd5118")
	}
	for _, root := range []bool{true, false} {
		fsys := spdtest.NewFS(t, build())
		p, _ := ProbeHost(ctx, fsys, LiveOptions{Privileged: root, Release: spdtest.Release})
		read := len(fsys.AskedFor("open sys/bus/pci/devices/0000:00:1f.4/config")) > 0
		if read != root {
			t.Errorf("root %v: config read %v", root, read)
		}
		if got := p.PCI[0].SPDWD; root && got != "1" || !root && got != "" {
			t.Errorf("root %v: spd-wd %q", root, got)
		}
		want := ReasonNoSPDDevice
		if root {
			want = ReasonWriteDisabled
		}
		if got := Reason(p, "DDR5"); got != want {
			t.Errorf("root %v: reason %s", root, got)
		}
	}
}

// TestReadInSteps: one read per step of 128 bytes; EIO at offset 128 keeps
// the first 128 bytes; a file that reports 1,024 bytes and gives 512; a
// read past the deadline leaves the reads after it undone.
func TestReadInSteps(t *testing.T) {
	d5 := dumpOf(t, "ddr5-rdimm-64g-2rx4")
	intel := func() *spdtest.Tree {
		return modTree().I2C().Adapter("0", "SMBus I801 adapter at 0000:00:1f.4").PCI("0000:00:1f.4", "8086", "a323", "i801_smbus")
	}
	t.Run("EIO at 128", func(t *testing.T) {
		fsys := spdtest.NewFS(t, intel().Client("0-0050", "spd5118", "spd5118", d5))
		fsys.Faults["sys/bus/i2c/devices/0-0050/eeprom"] = spdtest.Fault{At: 128, Err: spdtest.EIO}
		p, devs := ProbeHost(ctx, fsys, LiveOptions{Release: spdtest.Release})
		if len(devs) != 1 || !bytes.Equal(devs[0].Raw, d5[:128]) {
			t.Fatalf("%d bytes kept", len(devs[0].Raw))
		}
		c := p.Clients[0]
		if c.State != spd.StateShort || c.Errno != "EIO" || c.At != 128 || c.Read != 128 || c.EEPROM != 1024 {
			t.Errorf("client %+v", c)
		}
		d := spd.Decode(devs[0].Raw)
		if !d.DeviceWidth.Valid || d.DeviceWidth.V != 4 || d.HasSerial() {
			t.Errorf("the first page gives the width and no serial: %+v", d)
		}
	})
	t.Run("size 1024, 512 given", func(t *testing.T) {
		fsys := spdtest.NewFS(t, intel().Client("0-0050", "spd5118", "spd5118", d5[:512]))
		fsys.Faults["sys/bus/i2c/devices/0-0050/eeprom"] = spdtest.Fault{Size: 1024}
		p, devs := ProbeHost(ctx, fsys, LiveOptions{Release: spdtest.Release})
		if c := p.Clients[0]; c.Read != 512 || c.EEPROM != 1024 || c.State != spd.StateShort || len(devs[0].Raw) != 512 {
			t.Errorf("client %+v", c)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		d4 := dumpOf(t, "ddr4-rdimm-64g-2rx4")
		fsys := spdtest.NewFS(t, intel().Client("0-0050", "ee1004", "ee1004", d4).Client("0-0051", "ee1004", "ee1004", d4).Client("0-0052", "ee1004", "ee1004", d4))
		fsys.Faults["sys/bus/i2c/devices/0-0051/eeprom"] = spdtest.Fault{At: 128, Block: true}
		start := time.Now()
		p, _ := ProbeHost(ctx, fsys, LiveOptions{Release: spdtest.Release, Deadline: spdtest.Deadline})
		if took := time.Since(start); took > 5*spdtest.Deadline {
			t.Errorf("the probe took %v with a deadline of %v", took, spdtest.Deadline)
		}
		want := []string{spd.StateOK, spd.StateShort, spd.StateTimeout}
		for i, c := range p.Clients {
			if c.State != want[i] {
				t.Errorf("%s: %s, want %s", c.ID, c.State, want[i])
			}
		}
		if c := p.Clients[1]; c.Errno != "deadline" || c.At != 128 {
			t.Errorf("the read left behind: %+v", c)
		}
		if opened := fsys.AskedFor("open sys/bus/i2c/devices/0-0052/eeprom"); len(opened) != 0 {
			t.Errorf("a read after the deadline: %v", opened)
		}
	})
}

// TestOneReadPerStep: the reader asks for at most 128 bytes per read.
func TestOneReadPerStep(t *testing.T) {
	d5 := dumpOf(t, "ddr5-rdimm-64g-2rx4")
	tr := modTree().I2C().Client("0-0050", "spd5118", "spd5118", d5)
	fsys := &stepFS{FS: spdtest.NewFS(t, tr)}
	_, devs := ProbeHost(ctx, fsys, LiveOptions{Release: spdtest.Release})
	if !bytes.Equal(devs[0].Raw, d5) {
		t.Fatalf("%d bytes", len(devs[0].Raw))
	}
	if len(fsys.sizes) != 8 {
		t.Errorf("%d reads for 1,024 bytes: %v", len(fsys.sizes), fsys.sizes)
	}
	for _, n := range fsys.sizes {
		if n > Step {
			t.Errorf("a read of %d bytes", n)
		}
	}
}

// stepFS records the size of every read of an eeprom file.
type stepFS struct {
	*spdtest.FS
	mu    sync.Mutex
	sizes []int
}

func (s *stepFS) Open(name string) (fs.File, error) {
	f, err := s.FS.Open(name)
	if err != nil || !strings.HasSuffix(name, "/eeprom") {
		return f, err
	}
	return &sizeFile{File: f, s: s}, nil
}

type sizeFile struct {
	fs.File
	s *stepFS
}

func (f *sizeFile) Read(b []byte) (int, error) {
	f.s.mu.Lock()
	f.s.sizes = append(f.s.sizes, len(b))
	f.s.mu.Unlock()
	return f.File.Read(b)
}
