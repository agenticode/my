package dcbrain

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Table names one of the three tables of the dataset.
type Table int

const (
	TableErrors Table = iota
	TableInventory
	TableTickets
)

// String returns the name the dataset description uses for the table.
func (t Table) String() string {
	switch t {
	case TableErrors:
		return "mcelog"
	case TableInventory:
		return "inventory"
	case TableTickets:
		return "trouble_tickets"
	}
	return "table"
}

// stems are the file names a table is looked for under, without the
// extension. The dataset description writes "meclog" and "trouble_ticket";
// the files are named mcelog and trouble_tickets.
var stems = map[Table][]string{
	TableErrors:    {"mcelog", "meclog"},
	TableInventory: {"inventory"},
	TableTickets:   {"trouble_tickets", "trouble_ticket"},
}

// A table is stored as plain text or as a gzip-compressed tar archive
// that holds the text.
var extensions = []string{".csv", ".tar.gz", ".tgz"}

// Dataset locates the tables of the dataset. It holds no open files.
type Dataset struct {
	// Path is what was opened: a directory, or the zip archive the
	// dataset is distributed as.
	Path string
	// zipped is set when Path is a zip archive.
	zipped bool
	// files maps a table to its file: a path below the directory, or the
	// name of a member of the zip archive.
	files map[Table]string
}

// Open finds the tables under root. root is a directory that holds the
// three tables (as .csv or as .tar.gz, in the directory or one level
// below), or the zip archive of the dataset. The error table must be
// there; the inventory and the ticket table may be missing, and Has says
// so.
func Open(root string) (*Dataset, error) {
	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("dataset: %w", err)
	}
	d := &Dataset{Path: root, files: map[Table]string{}}
	var names []string
	if st.IsDir() {
		for _, pattern := range []string{"*", "*/*"} {
			m, err := filepath.Glob(filepath.Join(root, pattern))
			if err != nil {
				return nil, fmt.Errorf("dataset: %w", err)
			}
			for _, p := range m {
				if rel, err := filepath.Rel(root, p); err == nil {
					names = append(names, filepath.ToSlash(rel))
				}
			}
		}
	} else {
		zr, err := zip.OpenReader(root)
		if err != nil {
			return nil, fmt.Errorf("dataset: %s is neither a directory nor a zip archive: %w", root, err)
		}
		d.zipped = true
		for _, f := range zr.File {
			if !f.FileInfo().IsDir() {
				names = append(names, f.Name)
			}
		}
		zr.Close()
	}
	for t, ss := range stems {
		// The shortest name wins: a table at the top is preferred to a
		// copy further down, and plain text to an archive of it.
		best := ""
		for _, n := range names {
			base := strings.ToLower(path.Base(n))
			for _, s := range ss {
				for i, ext := range extensions {
					if base != s+ext {
						continue
					}
					if best == "" || rank(n, i) < rank(best, extIndex(best)) {
						best = n
					}
				}
			}
		}
		if best != "" {
			d.files[t] = best
		}
	}
	if !d.Has(TableErrors) {
		return nil, fmt.Errorf("dataset: no error table (mcelog.csv or mcelog.tar.gz) under %s", root)
	}
	return d, nil
}

func extIndex(name string) int {
	low := strings.ToLower(name)
	for i, ext := range extensions {
		if strings.HasSuffix(low, ext) {
			return i
		}
	}
	return len(extensions)
}

// rank orders candidate files: fewer directory levels first, then plain
// text before archives.
func rank(name string, ext int) int {
	return strings.Count(name, "/")*10 + ext
}

// Has reports whether the table was found.
func (d *Dataset) Has(t Table) bool { return d.files[t] != "" }

// Name returns the file a table is read from, for messages and record
// references, or "" when the table was not found.
func (d *Dataset) Name(t Table) string {
	f := d.files[t]
	if f == "" {
		return ""
	}
	if d.zipped {
		return filepath.Base(d.Path) + "/" + f
	}
	return f
}

// stream closes everything that was opened to reach a table.
type stream struct {
	io.Reader
	closers []io.Closer
}

func (s *stream) Close() error {
	var first error
	for i := len(s.closers) - 1; i >= 0; i-- {
		if err := s.closers[i].Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// OpenTable opens a table for reading as text. Archives are read as a
// stream: nothing is unpacked to disk.
func (d *Dataset) OpenTable(t Table) (io.ReadCloser, error) {
	name := d.files[t]
	if name == "" {
		return nil, fmt.Errorf("dataset: no %s table under %s: %w", t, d.Path, fs.ErrNotExist)
	}
	s := &stream{}
	fail := func(err error) (io.ReadCloser, error) {
		s.Close()
		return nil, fmt.Errorf("dataset: %s: %w", d.Name(t), err)
	}
	if d.zipped {
		zr, err := zip.OpenReader(d.Path)
		if err != nil {
			return fail(err)
		}
		s.closers = append(s.closers, zr)
		var member *zip.File
		for _, f := range zr.File {
			if f.Name == name {
				member = f
			}
		}
		if member == nil {
			return fail(errors.New("the archive changed since it was opened"))
		}
		rc, err := member.Open()
		if err != nil {
			return fail(err)
		}
		s.closers = append(s.closers, rc)
		s.Reader = rc
	} else {
		f, err := os.Open(filepath.Join(d.Path, filepath.FromSlash(name)))
		if err != nil {
			return fail(err)
		}
		s.closers = append(s.closers, f)
		s.Reader = f
	}
	if extIndex(name) == 0 {
		return s, nil
	}
	gz, err := gzip.NewReader(s.Reader)
	if err != nil {
		return fail(err)
	}
	s.closers = append(s.closers, gz)
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fail(errors.New("the archive holds no .csv file"))
		}
		if err != nil {
			return fail(err)
		}
		if h.Typeflag == tar.TypeReg && strings.HasSuffix(strings.ToLower(h.Name), ".csv") && !strings.HasPrefix(path.Base(h.Name), "._") {
			s.Reader = tr
			return s, nil
		}
	}
}
