// Package store is the MemorY ledger: one SQLite file that holds events,
// DIMM identities, placements, boots, actions, faults, verdicts and GPU
// memory records. The same file is the body of a case bundle.
//
// The driver is pure Go (modernc.org/sqlite), so the binary stays static.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/agenticode/my/internal/buildinfo"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// applicationID marks a SQLite file as a MemorY ledger ("MYLG").
const applicationID = 0x4D594C47

// Mode selects how a ledger is opened.
type Mode int

const (
	// ReadOnly opens an existing ledger and never changes it.
	ReadOnly Mode = iota
	// ReadWrite opens an existing ledger and migrates it to the current
	// schema if it is older.
	ReadWrite
	// Create is ReadWrite, and creates the file if it does not exist.
	Create
)

// Errors returned by Open.
var (
	// ErrNotLedger: the file is not a MemorY ledger.
	ErrNotLedger = errors.New("not a MemorY ledger")
	// ErrNewerSchema: the ledger was written by a newer version of the tool.
	ErrNewerSchema = errors.New("ledger schema is newer than this binary supports")
	// ErrNeedsMigration: the ledger is older and was opened read-only.
	ErrNeedsMigration = errors.New("ledger schema is older than this binary expects")
)

// Store is an open ledger.
type Store struct {
	db   *sql.DB
	path string
	mode Mode
}

// Open opens the ledger at path.
func Open(ctx context.Context, path string, mode Mode) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("ledger %s: %w", path, err)
	}
	fi, statErr := os.Stat(abs)
	switch {
	case statErr == nil && fi.IsDir():
		return nil, fmt.Errorf("ledger %s: is a directory", path)
	case statErr != nil && !errors.Is(statErr, fs.ErrNotExist):
		return nil, fmt.Errorf("ledger %s: %w", path, statErr)
	case statErr != nil && mode != Create:
		return nil, fmt.Errorf("ledger %s: %w", path, fs.ErrNotExist)
	}

	db, err := sql.Open("sqlite", dsn(abs, mode))
	if err != nil {
		return nil, fmt.Errorf("ledger %s: %w", path, err)
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db, path: abs, mode: mode}
	if err := s.init(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ledger %s: %w", path, err)
	}
	return s, nil
}

func dsn(abs string, mode Mode) string {
	// SQLite URI file names need "%", "?" and "#" escaped.
	p := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(abs)
	q := "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	switch mode {
	case ReadOnly:
		q += "&mode=ro"
	case ReadWrite:
		q += "&mode=rw"
	case Create:
		q += "&mode=rwc"
	}
	if mode != ReadOnly {
		// WAL lets readers run while a batch is being written. Close turns
		// the file back into a plain single-file database.
		q += "&_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	}
	return "file:" + p + q
}

func (s *Store) init(ctx context.Context) error {
	var tables int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master`).Scan(&tables); err != nil {
		if isNotDatabase(err) {
			return ErrNotLedger
		}
		return err
	}
	var appID, version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&appID); err != nil {
		return err
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	fresh := tables == 0 && appID == 0 && version == 0
	if !fresh && appID != applicationID {
		return ErrNotLedger
	}

	migs, err := loadMigrations()
	if err != nil {
		return err
	}
	latest := migs[len(migs)-1].version
	switch {
	case version > latest:
		return fmt.Errorf("%w (ledger v%d, binary v%d)", ErrNewerSchema, version, latest)
	case version == latest:
		return nil
	case s.mode == ReadOnly:
		return fmt.Errorf("%w (ledger v%d, binary v%d): open it once with write access to migrate", ErrNeedsMigration, version, latest)
	}
	for _, m := range migs {
		if m.version <= version {
			continue
		}
		if err := s.applyMigration(ctx, m, fresh); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
		}
		fresh = false
	}
	return nil
}

func isNotDatabase(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "not a database") || strings.Contains(msg, "SQLITE_NOTADB")
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var migs []migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		num, rest, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("migration file %q: want NNNN_name.sql", name)
		}
		b, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		migs = append(migs, migration{version: v, name: rest, sql: string(b)})
	}
	if len(migs) == 0 {
		return nil, errors.New("no migrations embedded")
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })
	for i, m := range migs {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration versions must be 1..n without gaps, found %d at position %d", m.version, i+1)
		}
	}
	return migs, nil
}

// SchemaVersion is the schema version this binary reads and writes.
func SchemaVersion() int {
	migs, err := loadMigrations()
	if err != nil {
		return 0
	}
	return migs[len(migs)-1].version
}

func (s *Store) applyMigration(ctx context.Context, m migration, fresh bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	now := time.Now().UTC().Format(timeLayout)
	if fresh {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return err
		}
		for k, v := range map[string]string{
			"ledger_id":  hex.EncodeToString(id),
			"created_at": now,
			"created_by": buildinfo.Version,
		} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)`, k, v); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA application_id = %d`, applicationID)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		fmt.Sprintf("migration.%04d", m.version), now+" "+buildinfo.Version); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, m.version)); err != nil {
		return err
	}
	return tx.Commit()
}

// Close releases the ledger. A ledger opened for writing is checkpointed and
// switched back to a rollback journal, so that the result is a single file
// that opens read-only anywhere.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	if s.mode == ReadOnly || err != nil {
		return err
	}
	// A fresh single connection has the exclusive access the journal mode
	// change needs. If another process holds the file open the change is
	// skipped; the ledger stays valid in WAL mode.
	db, oerr := sql.Open("sqlite", "file:"+strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(s.path)+"?mode=rw&_pragma=busy_timeout(2000)")
	if oerr != nil {
		return nil
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var mode string
	_ = db.QueryRow(`PRAGMA journal_mode = DELETE`).Scan(&mode)
	return nil
}

// Path returns the absolute path of the ledger file.
func (s *Store) Path() string { return s.path }

// Version returns the schema version of the open ledger.
func (s *Store) Version(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v)
	return v, err
}

// Meta returns the value stored under key, or "" when there is none.
func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetMeta stores value under key.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// timeLayout is the fixed-width UTC form all ledger times use. It sorts
// correctly as text.
const timeLayout = "2006-01-02T15:04:05.000000Z"

// ts converts a time to its ledger form; the zero time becomes NULL.
func ts(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(timeLayout)
}

func parseTS(s sql.NullString) time.Time {
	if !s.Valid || s.String == "" {
		return time.Time{}
	}
	t, err := time.Parse(timeLayout, s.String)
	if err != nil {
		// Tolerate other RFC 3339 forms written by hand or by other tools.
		if t2, err2 := time.Parse(time.RFC3339Nano, s.String); err2 == nil {
			return t2.UTC()
		}
		return time.Time{}
	}
	return t
}

// validTime reports whether t can be stored: the fixed-width text form only
// sorts correctly for four-digit years.
func validTime(t time.Time) bool {
	y := t.UTC().Year()
	return !t.IsZero() && y >= 1970 && y <= 9999
}
