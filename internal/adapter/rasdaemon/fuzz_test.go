package rasdaemon

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// A rasdaemon database is a SQLite file, and SQLite columns hold whatever
// was put into them. The fuzz target feeds the row readers values of every
// kind in every column, without a database in between: a row is built from
// the fuzz input and handed to the reader of its table.

// fuzzTimestamp is put into the timestamp column of most rows, so that the
// readers get past their first check.
const fuzzTimestamp = "2026-11-12 03:14:07 +0000"

// Value kinds of the row encoding: one byte for the kind, then the value.
const (
	kindNull = iota
	kindInt
	kindText
	kindBlob
	kindFloat
	kinds
)

// encodeValue writes a column value in the row encoding.
func encodeValue(out []byte, v any) []byte {
	switch x := v.(type) {
	case int64:
		out = append(out, kindInt)
		return binary.BigEndian.AppendUint64(out, uint64(x))
	case float64:
		out = append(out, kindFloat)
		return binary.BigEndian.AppendUint64(out, math.Float64bits(x))
	case string:
		if len(x) > 255 {
			x = x[:255]
		}
		return append(append(out, kindText, byte(len(x))), x...)
	case []byte:
		if len(x) > 255 {
			x = x[:255]
		}
		return append(append(out, kindBlob, byte(len(x))), x...)
	}
	return append(out, kindNull)
}

// decodeValue reads one column value and returns the rest of the input.
// Input that ends early gives NULL.
func decodeValue(data []byte) (any, []byte) {
	if len(data) == 0 {
		return nil, nil
	}
	kind, data := data[0]%kinds, data[1:]
	switch kind {
	case kindInt, kindFloat:
		if len(data) < 8 {
			return nil, nil
		}
		bits := binary.BigEndian.Uint64(data)
		if kind == kindInt {
			return int64(bits), data[8:]
		}
		return math.Float64frombits(bits), data[8:]
	case kindText, kindBlob:
		if len(data) == 0 {
			return nil, nil
		}
		n := int(data[0])
		data = data[1:]
		if n > len(data) {
			n = len(data)
		}
		if kind == kindText {
			return string(data[:n]), data[n:]
		}
		return append([]byte(nil), data[:n]...), data[n:]
	}
	return nil, data
}

// fuzzTables returns the tables that have a reader, with the columns the
// fixtures give them and up to three rows of each as seeds.
func fuzzTables(tb testing.TB) (names []string, columns map[string][]string, seeds map[string][][]any) {
	columns, seeds = map[string][]string{}, map[string][][]any{}
	type rowOf struct {
		cols []string
		vals []any
	}
	raw := map[string][]rowOf{}
	for _, fixture := range []string{"intel-skx-0.6.sql", "amd-upgraded-0.8.sql", "ghes-1.0.sql", "cxl-1.0.sql"} {
		path := testutil.BuildSQLite(tb, testutil.Testdata(tb, "rasdaemon", fixture), "fuzz.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			tb.Fatal(err)
		}
		for table, use := range tableUse {
			if use != "events" && use != "actions" {
				continue
			}
			rows, err := db.Query(`SELECT * FROM "` + table + `" ORDER BY rowid LIMIT 3`)
			if err != nil {
				continue // the fixture has no such table
			}
			cols, _ := rows.Columns()
			for _, c := range cols {
				known := false
				for _, have := range columns[table] {
					known = known || have == c
				}
				if !known {
					columns[table] = append(columns[table], c)
				}
			}
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					tb.Fatal(err)
				}
				raw[table] = append(raw[table], rowOf{cols, vals})
			}
			rows.Close()
		}
		db.Close()
	}
	for table := range columns {
		names = append(names, table)
		// A seed has a value for every column of the table, NULL where the
		// fixture it comes from does not have the column.
		for _, r := range raw[table] {
			vals := make([]any, len(columns[table]))
			for i, c := range columns[table] {
				for j, have := range r.cols {
					if have == c {
						vals[i] = r.vals[j]
					}
				}
			}
			seeds[table] = append(seeds[table], vals)
		}
	}
	sort.Strings(names)
	return names, columns, seeds
}

func FuzzRows(f *testing.F) {
	names, columns, seeds := fuzzTables(f)
	if len(names) == 0 {
		f.Fatal("no table with a reader in the fixtures")
	}
	for i, table := range names {
		for _, vals := range seeds[table] {
			var data []byte
			for _, v := range vals {
				data = encodeValue(data, v)
			}
			f.Add(uint8(i), true, data)
		}
		f.Add(uint8(i), true, []byte{})
		f.Add(uint8(i), false, []byte{kindText, 3, 'a', 'b', 'c', kindInt, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	}

	f.Fuzz(func(t *testing.T, sel uint8, dated bool, data []byte) {
		table := names[int(sel)%len(names)]
		cols := columns[table]
		var c adapter.Collected
		rep := adapter.Report{Kind: Kind, Origin: "fuzz.db"}
		r := &reader{ctx: ctx, sink: &c, path: "fuzz.db", rep: &rep}
		readers := map[string]func(*row) error{
			"mc_event": r.mcEvent, "mce_record": r.mceRecord, "extlog_event": r.extlogEvent, "memory_failure_event": r.memoryFailure,
			"cxl_dram_event": r.cxlDRAM, "cxl_general_media_event": r.cxlGeneralMedia, "cxl_memory_module_event": r.cxlModule,
			"cxl_poison_event": r.cxlPoison, "cxl_memory_sparing_event": r.cxlSparing, "cxl_overflow_event": r.cxlOverflow,
		}
		read := readers[table]
		if read == nil {
			t.Fatalf("table %s has no reader in this test", table)
		}
		index := make(map[string]int, len(cols))
		for i, name := range cols {
			index[name] = i
		}
		// The input holds one row after the other.
		for n := 0; n < 8; n++ {
			rw := &row{table: table, names: cols, index: index, vals: make([]any, len(cols))}
			for i := range cols {
				rw.vals[i], data = decodeValue(data)
			}
			if i, ok := index["timestamp"]; ok && dated {
				rw.vals[i] = fuzzTimestamp
			}
			if err := read(rw); err != nil {
				t.Fatalf("%s: the collecting sink takes every record, yet the reader failed: %v", table, err)
			}
			_ = rw.raw()
			if len(data) == 0 {
				break
			}
		}
		if err := r.cxlDevices(); err != nil {
			t.Fatalf("devices: %v", err)
		}
		for _, e := range c.Events {
			if e.Fingerprint == "" || e.Source == "" || e.RawRef == "" || e.ErrorDT.IsZero() || e.Grade == "" {
				t.Fatalf("%s: incomplete event %+v", table, e)
			}
		}
		for _, a := range c.Actions {
			if a.Fingerprint == "" || a.Source == "" || a.Kind == "" || a.At.IsZero() {
				t.Fatalf("%s: incomplete action %+v", table, a)
			}
		}
	})
}

// FuzzStoreSettings feeds an environment file to the reader of the store
// settings. Whatever the file holds, reading it and building the
// connection from it must end in settings or in an error.
func FuzzStoreSettings(f *testing.F) {
	for _, seed := range []string{
		"RASDAEMON_DB_BACKEND=mysql\nRAS_MYSQL_HOST=db.example\nRAS_MYSQL_PORT=3307\nRAS_MYSQL_PASSWORD=\"s3 cret\"\n",
		"RASDAEMON_DB_BACKEND=postgresql\nRAS_PG_HOST=pg.example\nRAS_PG_SCHEMA=ras\nRAS_PG_SSL_MODE=verify-full\nRAS_PG_PASSWORD='a\\'b'\n",
		"# comment\n; comment\nexport RASDAEMON_DB_BACKEND=mariadb\nRAS_MYSQL_SOCKET=/run/mysqld/mysqld.sock\nRAS_MYSQL_USE_SSL=yes\n",
		"RASDAEMON_DB_BACKEND=sqlite3\n",
		"RAS_PG_PORT=99999\nRASDAEMON_DB_BACKEND=pg\nRAS_PG_CONNECT_TIMEOUT=0\n",
		"=\n",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		env, err := ParseEnvFile(strings.NewReader(text))
		if err != nil {
			return
		}
		cfg, err := StoreConfigFromEnv("", func(k string) string { return env[k] })
		if err != nil {
			return
		}
		if cfg.Backend != BackendMySQL && cfg.Backend != BackendPostgreSQL {
			t.Fatalf("settings without an error and with backend %q", cfg.Backend)
		}
		// A host makes the connection independent of the sockets on the
		// machine the fuzzer runs on.
		if cfg.Host == "" {
			cfg.Host = "db.example"
		}
		conn, origin, err := cfg.connector()
		if err != nil {
			return
		}
		if conn == nil || origin == "" || (cfg.Password != "" && len(cfg.Password) > 3 && strings.Contains(origin, cfg.Password) && !strings.Contains(cfg.Host+cfg.Database+cfg.Schema, cfg.Password)) {
			t.Fatalf("connector: %v, origin %q", conn, origin)
		}
	})
}

// FuzzDatabase hands the adapter a file of arbitrary bytes as a database:
// a copy that was cut off, a file that was being written to when it was
// copied, a file that only starts like a database. The SQLite library
// reads what it can of such a file. Whatever it yields, the adapter
// returns a report, hands on only complete records, and leaves the file
// and the working directory as they were.
func FuzzDatabase(f *testing.F) {
	for _, fixture := range []string{"intel-skx-0.6.sql", "amd-upgraded-0.8.sql", "ghes-1.0.sql", "cxl-1.0.sql", "clock-step-0.8.sql"} {
		db, err := os.ReadFile(testutil.BuildSQLite(f, testutil.Testdata(f, "rasdaemon", fixture), "seed.db"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(db, true)
		f.Add(db[:len(db)/2], false)
	}
	f.Add([]byte("SQLite format 3\x00"), true)
	f.Add([]byte{}, false)

	dir := f.TempDir()
	path := filepath.Join(dir, "ras-mc_event.db")
	work := filepath.Join(dir, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte, direct bool) {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		var c adapter.Collected
		rep := New(path, Options{Direct: direct, TempDir: work}).Collect(ctx, &c)
		if rep.Status == "" || rep.Kind != Kind || rep.SinkErr != nil {
			t.Fatalf("report %+v", rep)
		}
		if rep.Skipped > rep.Records {
			t.Errorf("%d of %d records skipped", rep.Skipped, rep.Records)
		}
		if n := len(c.Events) + len(c.Actions); n > 0 && rep.Status != model.SourceOK && rep.Status != model.SourcePartial {
			t.Errorf("%d records from a source with status %s", n, rep.Status)
		}
		for _, e := range c.Events {
			if y := e.ErrorDT.UTC().Year(); e.Fingerprint == "" || e.Source == "" || e.RawRef == "" || !e.ErrorSeverity.Valid() ||
				!e.Grade.Valid() || e.Count < 1 || y < 1970 || y > 9999 {
				t.Errorf("event the ledger would refuse: %+v", e)
			}
		}
		for _, a := range c.Actions {
			if y := a.At.UTC().Year(); a.Fingerprint == "" || a.Source == "" || !a.Kind.Valid() || y < 1970 || y > 9999 {
				t.Errorf("action the ledger would refuse: %+v", a)
			}
		}
		// The source is read, never written; the copy is gone.
		if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, data) {
			t.Errorf("the database was changed by reading it (%v)", err)
		}
		if entries, _ := os.ReadDir(work); len(entries) != 0 {
			t.Errorf("%d entries left in the working directory", len(entries))
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 2 {
			t.Errorf("%d entries beside the database", len(entries)-2)
		}
	})
}
