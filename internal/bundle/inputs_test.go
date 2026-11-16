package bundle

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/testledger"
)

// TestInputs: the manifest lists the files that were read with their
// size and checksum; with WithInputs they travel under inputs/, whole; an
// anonymized bundle refuses them.
func TestInputs(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a", "sel.txt")
	b := filepath.Join(dir, "b", "sel.txt")
	c := filepath.Join(dir, "dump")
	for path, text := range map[string]string{a: "SEL of one\n", b: "SEL of another, login admin\n", filepath.Join(c, "redfish", "v1", "Systems.json"): "{}\n"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inputs := []InputFile{{Name: "sel.txt", Path: a}, {Name: "sel.txt", Path: b}, {Name: "../dump//redfish/v1/Systems.json", Path: filepath.Join(c, "redfish", "v1", "Systems.json")}}
	sum := func(path string) string {
		data, _ := os.ReadFile(path)
		h := sha256.Sum256(data)
		return hex.EncodeToString(h[:])
	}
	src := testledger.Open(t, "demo", 1, "")

	// Listed, and nothing travels.
	w := write(t, src, Options{Inputs: inputs})
	m := w.Manifest
	if len(m.Inputs) != 3 || m.Inputs[0].Name != "sel.txt" || m.Inputs[1].Name != "sel.txt-2" || m.Inputs[2].Name != "dump/redfish/v1/Systems.json" {
		t.Fatalf("inputs %+v", m.Inputs)
	}
	for i, in := range m.Inputs {
		if in.SHA256 != sum(inputs[i].Path) || in.Member != "" {
			t.Errorf("input %d: %+v", i, in)
		}
	}
	if members(t, w.Path)["inputs/sel.txt"] != "" || strings.Join(m.NotCollected, "|") != strings.Join(NotCollected, "|") {
		t.Error("a file travels without WithInputs")
	}
	if text := readmeOf(t, w.Path); !strings.Contains(text, "Files read\n  sel.txt  11 bytes  sha256 "+sum(a)) || strings.Contains(text, "inputs/") {
		t.Errorf("README:\n%s", text)
	}

	// They travel.
	w = write(t, src, Options{Inputs: inputs, WithInputs: true})
	got := members(t, w.Path)
	for i, in := range w.Manifest.Inputs {
		data, _ := os.ReadFile(inputs[i].Path)
		if in.Member != "inputs/"+in.Name || got[in.Member] != string(data) {
			t.Errorf("member %q: %q", in.Member, got[in.Member])
		}
	}
	if strings.Join(w.Manifest.NotCollected, "|") != strings.Join(NotCollectedWithInputs, "|") {
		t.Errorf("not collected %v", w.Manifest.NotCollected)
	}
	text := readmeOf(t, w.Path)
	for _, want := range []string{"inputs/        the 3 files the records were read from, as they were given", "can hold logins, user"} {
		if !strings.Contains(text, want) {
			t.Errorf("README lacks %q:\n%s", want, text)
		}
	}
	// The bundle opens as any other.
	cs, err := Open(ctx, w.Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cs.Close()

	// Not with --anonymize.
	if _, err := Write(ctx, src, filepath.Join(t.TempDir(), "x"+Extension), Options{Inputs: inputs, WithInputs: true, Anonymize: true}); !errors.Is(err, ErrInputsAnonymized) {
		t.Errorf("anonymized with inputs: %v", err)
	}
	// A file that cannot be read stops the bundle.
	if _, err := Write(ctx, src, filepath.Join(t.TempDir(), "y"+Extension), Options{Inputs: []InputFile{{Name: "gone", Path: filepath.Join(dir, "gone")}}}); err == nil {
		t.Error("a missing input gave a bundle")
	}
}

func TestInputName(t *testing.T) {
	for in, want := range map[string]string{"sel.txt": "sel.txt", "/abs/x": "abs/x", "../../etc/passwd": "etc/passwd", "a/./b//c": "a/b/c", "": "input", "..": "input"} {
		if got := inputName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// members reads the members of a bundle.
func members(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		out[f.Name] = string(data)
	}
	return out
}

func readmeOf(t *testing.T, path string) string {
	t.Helper()
	return members(t, path)[readmeName]
}
