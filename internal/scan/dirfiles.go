package scan

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/agenticode/my/internal/adapter"
)

// Limits of a directory that is read file by file: an unpacked archive of
// a vendor tool is read as it is, and a directory that holds a whole disk
// is not walked to its end.
const (
	MaxDirFiles = 2000
	MaxDirDepth = 8
	MaxDirFile  = 64 << 20
)

// WhatFiles is a directory read file by file.
const WhatFiles = "directory of files"

// DirFile is a regular file below a directory.
type DirFile struct {
	// Path is the file; Rel its path below the directory, with slashes.
	Path, Rel string
	Size      int64
}

// DirWalk is what DirFiles found.
type DirWalk struct {
	Files []DirFile
	// Big counts the files above MaxDirFile, which are not listed.
	Big int
	// More says that the walk stopped at MaxDirFiles or did not go below
	// MaxDirDepth levels.
	More bool
}

// DirFiles lists the regular files below dir in lexical order, within the
// limits above. Links are not followed.
func DirFiles(ctx context.Context, dir string) (DirWalk, error) {
	var w DirWalk
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			// A part that cannot be read is left out; the rest is walked.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if strings.Count(rel, "/")+1 >= MaxDirDepth {
				w.More = true
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if fi.Size() > MaxDirFile {
			w.Big++
			return nil
		}
		if len(w.Files) == MaxDirFiles {
			w.More = true
			return fs.SkipAll
		}
		w.Files = append(w.Files, DirFile{Path: p, Rel: rel, Size: fi.Size()})
		return nil
	})
	return w, err
}

// detectFiles reads a directory that is none of the known trees file by
// file: each file a reader recognises is a source, and the others are
// counted in one line.
func detectFiles(ctx context.Context, dir string, opts DetectOptions) ([]adapter.Adapter, string, error) {
	w, err := DirFiles(ctx, dir)
	if err != nil {
		return nil, "", err
	}
	var out []adapter.Adapter
	unknown := 0
	for _, f := range w.Files {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		a, ok := detectOne(f.Path, opts)
		if !ok {
			unknown++
			continue
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, "", &UnknownInputError{Path: dir, Code: "dir"}
	}
	if note := dirNote(unknown, w); note != "" {
		out = append(out, adapter.Unrecognised(dir, note))
	}
	return out, WhatFiles, nil
}

// detectOne asks the readers of single files about one file of a
// directory.
func detectOne(path string, opts DetectOptions) (a adapter.Adapter, ok bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
		return nil, false
	}
	head, zipped, err := readHead(path)
	if err != nil {
		return nil, false
	}
	a, _, err = detectFile(path, fi, head, zipped, opts)
	return a, err == nil && a != nil
}

// dirNote says in one line what of a directory was left out.
func dirNote(unknown int, w DirWalk) string {
	var parts []string
	if unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d %s not recognised and left out", unknown, plural(unknown, "file was", "files were")))
	}
	if w.Big > 0 {
		parts = append(parts, fmt.Sprintf("%d %s above %d MiB not read", w.Big, plural(w.Big, "file", "files"), MaxDirFile>>20))
	}
	if w.More {
		parts = append(parts, fmt.Sprintf("the directory holds more than %d files or %d levels; the rest was not looked at", MaxDirFiles, MaxDirDepth))
	}
	return strings.Join(parts, "; ")
}

func plural(n int, one, other string) string {
	if n == 1 {
		return one
	}
	return other
}
