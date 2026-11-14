package rasdaemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// snapshot is a private copy of a database that another process may be
// writing to.
type snapshot struct {
	dir string
	db  string
	// journal is set when a rollback journal or write-ahead log was copied
	// along. SQLite then has to recover the copy when it opens it.
	journal bool
	// unstable is set when the source kept changing during every copy
	// attempt. The integrity check decides whether the copy is usable.
	unstable bool
}

func (s *snapshot) remove() {
	if s != nil && s.dir != "" {
		os.RemoveAll(s.dir)
	}
}

// sidecars are the files SQLite keeps next to a database during a write.
var sidecars = []string{"-journal", "-wal"}

// fingerprintFiles describes the size and modification time of the database
// and its sidecar files, to tell whether they changed during a copy.
func fingerprintFiles(src string) (string, error) {
	out := ""
	for i, suffix := range append([]string{""}, sidecars...) {
		fi, err := os.Stat(src + suffix)
		switch {
		case err == nil:
			out += fmt.Sprintf("%s:%d:%d;", suffix, fi.Size(), fi.ModTime().UnixNano())
		case errors.Is(err, fs.ErrNotExist) && i > 0:
			out += suffix + ":absent;"
		default:
			return "", err
		}
	}
	return out, nil
}

func copyFile(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			out.Close()
			return err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			return rerr
		}
	}
	return out.Close()
}

// takeSnapshot copies the database at src, and its journal or write-ahead
// log if one exists, into a new private directory under tmpRoot.
//
// Reading the bytes takes no SQLite lock, so a rasdaemon that is writing is
// never blocked. rasdaemon before 1.0 sets no busy timeout: a reader holding
// a shared lock at the wrong moment makes it drop the record it is storing.
// The price of copying without a lock is that a write may land in the middle
// of the copy. The copy is therefore repeated until the files did not change
// while it ran, and the caller checks the integrity of the result.
func takeSnapshot(ctx context.Context, src, tmpRoot string) (*snapshot, error) {
	dir, err := os.MkdirTemp(tmpRoot, "my-rasdaemon-")
	if err != nil {
		return nil, fmt.Errorf("snapshot directory: %w", err)
	}
	s := &snapshot{dir: dir, db: filepath.Join(dir, "snapshot.db")}
	const attempts = 5
	for attempt := 1; ; attempt++ {
		before, err := fingerprintFiles(src)
		if err != nil {
			s.remove()
			return nil, err
		}
		if err := copyFile(ctx, src, s.db); err != nil {
			s.remove()
			return nil, err
		}
		s.journal = false
		for _, suffix := range sidecars {
			err := copyFile(ctx, src+suffix, s.db+suffix)
			switch {
			case err == nil:
				s.journal = true
			case errors.Is(err, fs.ErrNotExist):
				os.Remove(s.db + suffix)
			default:
				s.remove()
				return nil, err
			}
		}
		after, err := fingerprintFiles(src)
		if err != nil {
			s.remove()
			return nil, err
		}
		if before == after {
			return s, nil
		}
		if attempt == attempts {
			s.unstable = true
			return s, nil
		}
		select {
		case <-ctx.Done():
			s.remove()
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * 50 * time.Millisecond):
		}
	}
}
