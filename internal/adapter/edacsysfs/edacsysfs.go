// Package edacsysfs reads the EDAC memory controller counters the kernel
// exposes under /sys/devices/system/edac/mc.
//
// The counters say how many corrected and uncorrected errors each DIMM had
// since the EDAC driver was loaded, and nothing about where in the DIMM.
// They restart at zero on every boot. The adapter takes one reading; the
// ledger turns readings into grade C events and subtracts what located
// records already cover.
package edacsysfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/decode/edac"
	"github.com/agenticode/my/internal/model"
)

// Kind is the adapter name in the source inventory.
const Kind = "edac-sysfs"

// Dir is the location of the counters below the file system root.
const Dir = "sys/devices/system/edac/mc"

// Options control a reading.
type Options struct {
	// Now is the time of the reading; zero means the current time.
	Now time.Time
}

// Adapter takes one reading of the EDAC counters.
type Adapter struct {
	fsys   fs.FS
	dir    string
	origin string
	opts   Options
}

// New returns an adapter that reads the directory dir of fsys. origin names
// the source in reports and raw references.
func New(fsys fs.FS, dir, origin string, opts Options) *Adapter {
	return &Adapter{fsys: fsys, dir: dir, origin: origin, opts: opts}
}

// NewRoot returns an adapter for the EDAC counters of the system whose file
// system root is root: "/" on a live host, or the top of a collected tree.
func NewRoot(root string, opts Options) *Adapter {
	return New(os.DirFS(root), Dir, filepath.Join(root, filepath.FromSlash(Dir)), opts)
}

func (a *Adapter) Kind() string   { return Kind }
func (a *Adapter) Origin() string { return a.origin }

var (
	mcDirRE   = regexp.MustCompile(`^mc(\d+)$`)
	dimmDirRE = regexp.MustCompile(`^(dimm|rank)(\d+)$`)
	csrowRE   = regexp.MustCompile(`^csrow\d+$`)
)

// Collect reads the counters into sink.
func (a *Adapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.origin}
	now := a.opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()

	entries, err := fs.ReadDir(a.fsys, a.dir)
	if err != nil {
		rep.Status, rep.Detail = adapter.StatusForError(err)
		if rep.Status == model.SourceAbsent {
			rep.Detail = "no EDAC memory controller directory; the kernel has no EDAC support or sysfs is not mounted"
		}
		return rep
	}
	var mcs []int
	for _, e := range entries {
		if m := mcDirRE.FindStringSubmatch(e.Name()); m != nil && e.IsDir() {
			n, _ := strconv.Atoi(m[1])
			mcs = append(mcs, n)
		}
	}
	sort.Ints(mcs)
	if len(mcs) == 0 {
		rep.Status = model.SourceEmpty
		rep.Detail = "no memory controller is registered; no EDAC driver is loaded for this platform (usual in virtual machines)"
		return rep
	}

	var tally adapter.Tally
	legacyOnly := 0
	for _, mc := range mcs {
		if err := ctx.Err(); err != nil {
			rep.Status, rep.Detail = model.SourceError, err.Error()
			return rep
		}
		mcDir := path.Join(a.dir, fmt.Sprintf("mc%d", mc))
		mcRef := fmt.Sprintf("%s/mc%d", a.origin, mc)
		name, _ := a.text(mcDir, "mc_name")
		var since model.Opt[int64]
		if v, err := a.number(mcDir, "seconds_since_reset"); err == nil {
			since = model.Some(int64(v))
		}

		dimms, err := fs.ReadDir(a.fsys, mcDir)
		if err != nil {
			rep.Records++
			tally.Skip(mcRef, "controller directory is unreadable: "+reason(err))
			continue
		}
		found, legacy := 0, 0
		for _, d := range dimms {
			m := dimmDirRE.FindStringSubmatch(d.Name())
			if m == nil || !d.IsDir() {
				if csrowRE.MatchString(d.Name()) {
					legacy++
				}
				continue
			}
			found++
			rep.Records++
			idx, _ := strconv.Atoi(m[2])
			dir := path.Join(mcDir, d.Name())
			ref := mcRef + "/" + d.Name()
			ce, errCE := a.number(dir, "dimm_ce_count")
			ue, errUE := a.number(dir, "dimm_ue_count")
			if errCE != nil || errUE != nil {
				tally.Skip(ref, "error counters are unreadable: "+reason(errors.Join(errCE, errUE)))
				continue
			}
			s := &model.CounterSample{
				ObservedAt: now, MC: mc, MCName: name, DIMMIndex: idx, CE: ce, UE: ue, SecondsSinceReset: since,
				Source: Kind, RawRef: ref,
			}
			s.Label, _ = a.text(dir, "dimm_label")
			if loc, err := a.text(dir, "dimm_location"); err == nil {
				s.Location = edac.ParseLayers(loc).Key()
			}
			if mb, err := a.number(dir, "size"); err == nil {
				s.SizeMB = model.Some(int(mb))
			}
			s.MemType, _ = a.text(dir, "dimm_mem_type")
			s.DevType, _ = a.text(dir, "dimm_dev_type")
			s.EdacMode, _ = a.text(dir, "dimm_edac_mode")
			if err := sink.Counter(s); err != nil {
				rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
				return rep
			}
		}
		if found == 0 && legacy > 0 {
			legacyOnly++
		}

		// Errors the driver could not tie to a DIMM are counted on the controller.
		ce, errCE := a.number(mcDir, "ce_noinfo_count")
		ue, errUE := a.number(mcDir, "ue_noinfo_count")
		if errCE == nil && errUE == nil {
			rep.Records++
			s := &model.CounterSample{
				ObservedAt: now, MC: mc, MCName: name, DIMMIndex: -1, CE: ce, UE: ue, SecondsSinceReset: since,
				Source: Kind, RawRef: mcRef + "/noinfo",
			}
			if err := sink.Counter(s); err != nil {
				rep.Status, rep.Detail, rep.SinkErr = model.SourceError, "ledger rejected a record: "+err.Error(), err
				return rep
			}
		}
	}

	if legacyOnly > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d memory controllers expose only the csrow layout of kernels before 3.6, which is not read", legacyOnly))
	}
	rep.Skipped = tally.Total()
	rep.Notes = append(rep.Notes, tally.Notes()...)
	switch {
	case rep.Records == 0:
		rep.Status = model.SourceEmpty
		rep.Detail = "memory controllers are registered but expose no DIMM counters"
	case rep.Skipped > 0:
		rep.Status = model.SourcePartial
	default:
		rep.Status = model.SourceOK
	}
	return rep
}

func reason(err error) string {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, fs.ErrNotExist):
		return "file is missing"
	}
	return err.Error()
}

func (a *Adapter) text(dir, name string) (string, error) {
	b, err := fs.ReadFile(a.fsys, path.Join(dir, name))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (a *Adapter) number(dir, name string) (uint64, error) {
	s, err := a.text(dir, name)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s holds %q, not a number", name, s)
	}
	return v, nil
}
