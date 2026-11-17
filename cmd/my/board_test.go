package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testutil"
)

// my board exits with the grade of the host and writes the view board.
func TestBoardCommand(t *testing.T) {
	root := testutil.Testdata(t, "real", "6.19.10-ghes-multi")
	code, stdout, stderr := runEnv(nil, "board", "--root", root, "--lang", "en", "--width", "80")
	if code != 2 || stderr != "" || !strings.Contains(stdout, "══╡CPU 0╞══") {
		t.Fatalf("exit %d, %s\n%s", code, stderr, stdout)
	}
	code, stdout, _ = runEnv(nil, "board", "--root", root, "--json")
	var env struct {
		View     string `json:"view"`
		Grade    string `json:"grade"`
		ExitCode int    `json:"exit_code"`
		Data     struct {
			Counts struct{ Placed int } `json:"counts"`
			Rows   []json.RawMessage    `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil || code != 2 || env.View != "board" || env.ExitCode != 2 || env.Data.Counts.Placed != 6 || len(env.Data.Rows) != 1 {
		t.Errorf("--json: exit %d, %+v, %v", code, env, err)
	}
	code, stdout, _ = runEnv(nil, "open", testutil.Testdata(t, "real", "6.19.10-ghes-multi", "my", "case.mycase"), "board", "--lang", "ko")
	if code != 2 || !strings.Contains(stdout, "CPU 0") {
		t.Errorf("my open FILE board: exit %d\n%s", code, stdout)
	}
}

// The board of memory on the chipset in --json: one controller in the
// place of the CPUs, no stick on a CPU, and the board placed in full by
// the table of its family.
func TestBoardOfChipsetMemoryJSON(t *testing.T) {
	ledger := t.TempDir() + "/ledger.db"
	in := testutil.Testdata(t, "explorer", "linuxhw-poweredge-2950-121ae0", "dmidecode.txt")
	if code, _, stderr := runEnv(nil, "import", in, "--ledger", ledger); code != 0 {
		t.Fatalf("import: exit %d, %s", code, stderr)
	}
	code, stdout, stderr := runEnv(nil, "open", ledger, "board", "--json")
	var env struct {
		Data struct {
			Hub      bool   `json:"hub"`
			Family   string `json:"family"`
			Complete struct {
				Full bool `json:"full"`
			} `json:"complete"`
			Rows []struct {
				Parts []struct {
					CPU *struct {
						Controller bool `json:"controller"`
						DIMMs      int  `json:"dimms"`
					} `json:"cpu"`
					Channels []struct {
						Name   string            `json:"name"`
						Sticks []json.RawMessage `json:"sticks"`
					} `json:"channels"`
				} `json:"parts"`
			} `json:"rows"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("exit %d, %v, %s\n%s", code, err, stderr, stdout)
	}
	d := env.Data
	controllers, sticks := 0, 0
	for _, r := range d.Rows {
		for _, p := range r.Parts {
			if p.CPU != nil && p.CPU.Controller && p.CPU.DIMMs == 8 {
				controllers++
			}
			for _, c := range p.Channels {
				sticks += len(c.Sticks)
			}
		}
	}
	if !d.Hub || !d.Complete.Full || d.Family != "dell-pe-1950-2950" || controllers != 1 || sticks != 8 {
		t.Errorf("hub %v, full %v, family %q, %d controllers, %d sticks", d.Hub, d.Complete.Full, d.Family, controllers, sticks)
	}
	code, stdout, _ = runEnv(nil, "open", ledger, "board", "--lang", "ko", "--ascii")
	if !strings.Contains(stdout, "==[칩셋]==") {
		t.Errorf("--ascii ko: exit %d\n%s", code, stdout)
	}
}
