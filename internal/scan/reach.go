package scan

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/agenticode/my/internal/adapter/crashlog"
	"github.com/agenticode/my/internal/adapter/edacsysfs"
	"github.com/agenticode/my/internal/adapter/hostinfo"
	"github.com/agenticode/my/internal/adapter/smbios"
	"github.com/agenticode/my/internal/ingest"
	"github.com/agenticode/my/internal/model"
)

// Reads reports whether an entry of the source inventory stands for a
// source that was read and that would have shown an error of the host, had
// there been one.
//
// Host facts and SMBIOS describe the machine and carry no error. The EDAC
// counters count errors only where a memory controller is registered: a
// tree without one was read and says nothing. Every other source that was
// read in full, in part, or read and found without records could have
// shown an error.
func Reads(src model.Source) bool {
	switch src.Kind {
	case hostinfo.Kind, smbios.Kind, ingest.SlotMapKind, ingest.TimeAlignKind:
		return false
	case edacsysfs.Kind, crashlog.KindPstore, crashlog.KindKdump:
		// An empty crash log place says that no boot crashed into it,
		// nothing about the errors of the host.
		return src.Status == model.SourceOK || src.Status == model.SourcePartial
	}
	switch src.Status {
	case model.SourceOK, model.SourcePartial, model.SourceEmpty:
		return true
	}
	return false
}

// Blind reports whether a scan read no source that could have shown an
// error. "No errors on record" means nothing for such a scan: the user may
// not read the kernel log, the directory holds no copy of a host, or the
// system is not Linux.
func Blind(sources []model.Source) bool {
	for _, src := range sources {
		if Reads(src) {
			return false
		}
	}
	return true
}

// HoldsErrorSource reports whether a copy of a host's files holds any
// place a scan reads error records from: a rasdaemon database, a kernel
// log file, a memory controller in the EDAC tree, a saved event log of the
// BMC, saved output of nvidia-smi. A directory without any of them is no
// copy of a host as far as errors go, and a scan of it has nothing to
// read. Files that are there and closed to the user count: the scan
// reports them as not readable.
func HoldsErrorSource(opts *Options) bool {
	there := func(rel string) bool {
		_, err := os.Lstat(opts.path(rel))
		return err == nil || !errors.Is(err, fs.ErrNotExist)
	}
	for _, c := range CrashPlaces {
		if there(c.Dir) {
			return true
		}
	}
	for _, list := range [][]string{RasdaemonPaths, LogFiles, NvidiaSMIFiles} {
		for _, rel := range list {
			if there(rel) {
				return true
			}
		}
	}
	if entries, err := os.ReadDir(opts.path(edacsysfs.Dir)); err == nil {
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), "mc") {
				return true
			}
		}
	}
	if entries, err := os.ReadDir(opts.path(SELDir)); err == nil {
		for _, e := range entries {
			for _, ending := range SELListings {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ending) {
					return true
				}
			}
		}
	}
	return false
}
