package rasdaemon

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/testutil"
)

// StoresEnv names the environment variable that turns the tests against
// real servers on: a comma-separated list of environment files in the form
// rasdaemon takes (RASDAEMON_DB_BACKEND and the RAS_MYSQL_* or RAS_PG_*
// settings), one per server.
//
// The tests create, fill and drop the tables of rasdaemon. They refuse to
// run unless the MySQL database, or the PostgreSQL schema, has a name that
// ends in "_test".
const StoresEnv = "MY_TEST_STORES"

// liveStore is a server the tests may write to.
type liveStore struct {
	name string
	cfg  StoreConfig
	be   *backend
	db   *sql.DB
}

func liveStores(t *testing.T) []*liveStore {
	t.Helper()
	list := os.Getenv(StoresEnv)
	if list == "" {
		t.Skipf("set %s to environment files of test servers to run this against MySQL, MariaDB or PostgreSQL", StoresEnv)
	}
	var out []*liveStore
	for _, file := range strings.Split(list, ",") {
		f, err := os.Open(strings.TrimSpace(file))
		if err != nil {
			t.Fatal(err)
		}
		env, err := ParseEnvFile(f)
		f.Close()
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		cfg, err := StoreConfigFromEnv("", func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		// These tests drop tables. A name that ends in _test says that the
		// place is theirs.
		place := cfg.Database
		if cfg.Backend == BackendPostgreSQL {
			place = cfg.Schema
		}
		if !strings.HasSuffix(place, "_test") {
			t.Fatalf("%s: %q does not end in _test: these tests drop the tables of rasdaemon and only run where the name says they may", file, place)
		}
		conn, _, err := cfg.connector()
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		db := sql.OpenDB(conn)
		t.Cleanup(func() { db.Close() })
		if err := db.PingContext(ctx); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		be, err := newBackend(cfg.Backend, cfg.Schema)
		if err != nil {
			t.Fatal(err)
		}
		var version string
		if err := db.QueryRowContext(ctx, "SELECT version()").Scan(&version); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		t.Logf("%s: %s %s", file, cfg.Backend, version)
		out = append(out, &liveStore{name: strings.TrimSuffix(file[strings.LastIndexByte(file, '/')+1:], ".env"), cfg: cfg, be: be, db: db})
	}
	return out
}

func (l *liveStore) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := l.db.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("%s: %v\n%s", l.name, err, query)
	}
}

// sqlType is the SQL type rasdaemon 1.0.0 gives a column on the backend.
func (l *liveStore) sqlType(typ string) string {
	if l.cfg.Backend == BackendPostgreSQL {
		return map[string]string{"serial": "BIGSERIAL", "int32": "INTEGER", "int64": "BIGINT", "stamp": "TIMESTAMPTZ", "text": "TEXT", "blob": "BYTEA"}[typ]
	}
	return map[string]string{"serial": "BIGINT AUTO_INCREMENT", "int32": "INT", "int64": "BIGINT", "stamp": "DATETIME(6)", "text": "TEXT", "blob": "BLOB"}[typ]
}

// reset drops the tables of rasdaemon.
func (l *liveStore) reset(t *testing.T) {
	t.Helper()
	if l.cfg.Backend == BackendPostgreSQL {
		l.exec(t, "CREATE SCHEMA IF NOT EXISTS "+l.be.ident(l.cfg.Schema))
	}
	for name := range tableUse {
		l.exec(t, "DROP TABLE IF EXISTS "+l.be.table(name))
	}
}

// create makes a table the way rasdaemon 1.0.0 does on the backend: the
// columns in the order of its table descriptor, the hostname column after
// the first, an index on the host name and on the time, and the row number
// as the primary key.
func (l *liveStore) create(t *testing.T, name string, cols []fakeColumn) {
	t.Helper()
	var defs []string
	for i, c := range cols {
		defs = append(defs, l.be.ident(c.name)+" "+l.sqlType(c.typ))
		if i == 0 {
			if l.cfg.Backend == BackendPostgreSQL {
				defs = append(defs, `"hostname" TEXT`)
			} else {
				defs = append(defs, "`hostname` VARCHAR(255)")
			}
		}
	}
	if l.cfg.Backend == BackendPostgreSQL {
		l.exec(t, "CREATE TABLE IF NOT EXISTS "+l.be.table(name)+" ("+strings.Join(defs, ", ")+`, PRIMARY KEY ("id"))`)
		for _, col := range []string{"hostname", "timestamp"} {
			l.exec(t, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS "%s_%s_idx" ON %s ("%s")`, name, col, l.be.table(name), col))
		}
		return
	}
	l.exec(t, "CREATE TABLE IF NOT EXISTS "+l.be.table(name)+" ("+strings.Join(defs, ", ")+
		fmt.Sprintf(", INDEX `%s_hostname_idx` (`hostname`), INDEX `%s_timestamp_idx` (`timestamp`), PRIMARY KEY (id)) ENGINE=InnoDB", name, name))
}

// load fills the server with the rows of a SQLite fixture as the rows of
// one host. Row numbers are those of the fixture plus offset. The values
// go in the way rasdaemon binds them: the time as a UTC time without zone
// for MySQL, as text with the offset of the host for PostgreSQL.
func (l *liveStore) load(t *testing.T, fixture, host string, offset int64) {
	t.Helper()
	path := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", fixture), "live-"+host+".db")
	src, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	names, err := listTables(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, ok := tableUse[name]; !ok {
			continue
		}
		info, err := src.Query(`PRAGMA table_info("` + name + `")`)
		if err != nil {
			t.Fatal(err)
		}
		var cols []fakeColumn
		for info.Next() {
			var cid, notNull, pk int
			var col, decl string
			var def sql.NullString
			if err := info.Scan(&cid, &col, &decl, &notNull, &def, &pk); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, fakeColumn{col, fixtureType(name, col, decl)})
		}
		info.Close()
		// A fixture of rasdaemon 1.0 has the columns of 1.0, in their order.
		if want := schema10[name]; want != nil && strings.Contains(fixture, "-1.0") {
			var have, declared []string
			for _, c := range cols {
				have = append(have, c.name)
			}
			for _, c := range want {
				declared = append(declared, c.name)
			}
			if !reflect.DeepEqual(have, declared) {
				t.Errorf("%s: table %s has columns %v; rasdaemon 1.0.0 declares %v", fixture, name, have, declared)
			}
		}
		l.create(t, name, cols)

		rows, err := src.Query(`SELECT * FROM "` + name + `" ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			var names, marks []string
			var args []any
			for i, c := range cols {
				v := coerce(vals[i], c.typ)
				switch {
				case c.name == "id":
					v = vals[i].(int64) + offset
				case c.typ == "stamp" && v != nil && l.cfg.Backend == BackendPostgreSQL:
					// rasdaemon hands PostgreSQL the text it would write to
					// a SQLite file, and the server makes an instant of it.
					v = vals[i]
				}
				names = append(names, l.be.ident(c.name))
				marks = append(marks, l.be.arg(len(args)+1))
				args = append(args, v)
			}
			names, marks, args = append(names, l.be.ident("hostname")), append(marks, l.be.arg(len(args)+1)), append(args, host)
			l.exec(t, "INSERT INTO "+l.be.table(name)+" ("+strings.Join(names, ", ")+") VALUES ("+strings.Join(marks, ", ")+")", args...)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
}

// TestStoreOnAServer reads from real servers what the tests with the
// stand-in server read from it: every fixture as the rows of one host,
// compared with the file, and a store that several hosts wrote to.
func TestStoreOnAServer(t *testing.T) {
	for _, l := range liveStores(t) {
		for _, fixture := range fixtures {
			t.Run(l.name+" "+fixture, func(t *testing.T) {
				path := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", fixture), "ras-mc_event.db")
				var file adapter.Collected
				fileRep := New(path, Options{Direct: true}).Collect(ctx, &file)

				l.reset(t)
				l.load(t, fixture, "node-17", 0)
				st, err := OpenStore(ctx, l.cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				var got adapter.Collected
				rep := st.Adapter("node-17", ReadOptions{}).Collect(ctx, &got)
				sameAsFile(t, &got, rep, &file, fileRep, path, st.Origin())

				hosts, err := st.Hosts(ctx, nil)
				if err != nil || len(hosts) != 1 || hosts[0].Name != "node-17" || hosts[0].Rows == 0 {
					t.Errorf("hosts: %+v, %v", hosts, err)
				}
			})
		}

		t.Run(l.name+" several hosts", func(t *testing.T) {
			l.reset(t)
			l.load(t, "ghes-1.0.sql", "db-07", 0)
			l.load(t, "ghes-1.0.sql", "db-08", 1000)
			l.load(t, "cxl-1.0.sql", "cxl-01", 2000)
			// A row without a host name: a rasdaemon that could not learn it.
			l.exec(t, "INSERT INTO "+l.be.table("mc_event")+" ("+l.be.ident("id")+", "+l.be.ident("err_type")+") VALUES (9000, 'Corrected')")
			st, err := OpenStore(ctx, l.cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()

			hosts, err := st.Hosts(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			by := map[string]StoreHost{}
			for _, h := range hosts {
				names = append(names, h.Name)
				by[h.Name] = h
			}
			if !reflect.DeepEqual(names, []string{"", "cxl-01", "db-07", "db-08"}) || by[""].Rows != 1 || by["db-07"].Rows != by["db-08"].Rows || by["db-07"].Rows == 0 {
				t.Fatalf("hosts: %+v", hosts)
			}
			if by["db-08"].Last["mc_event"] != by["db-07"].Last["mc_event"]+1000 {
				t.Errorf("row numbers: db-07 %v, db-08 %v", by["db-07"].Last, by["db-08"].Last)
			}

			var a, b adapter.Collected
			repA := st.Adapter("db-07", ReadOptions{}).Collect(ctx, &a)
			repB := st.Adapter("db-08", ReadOptions{}).Collect(ctx, &b)
			if repA.Status != repB.Status || repA.Records != repB.Records || len(a.Events) == 0 || len(a.Events) != len(b.Events) || len(a.Actions) != len(b.Actions) {
				t.Fatalf("db-07: %+v, %d events; db-08: %+v, %d events", repA, len(a.Events), repB, len(b.Events))
			}
			for i := range a.Events {
				x, y := *a.Events[i], *b.Events[i]
				x.RawRef, x.Raw, x.Fingerprint, x.DupOfFingerprint = "", "", "", ""
				y.RawRef, y.Raw, y.Fingerprint, y.DupOfFingerprint = "", "", "", ""
				if x != y {
					t.Errorf("event %d differs between two hosts with the same rows:\n %+v\n %+v", i, x, y)
				}
			}

			// Hosts with rows above the numbers seen so far.
			seen := map[string]int64{}
			for _, h := range hosts {
				for table, last := range h.Last {
					seen[table] = max(seen[table], last)
				}
			}
			if fresh, err := st.Hosts(ctx, seen); err != nil || len(fresh) != 0 {
				t.Errorf("nothing is new, yet: %+v, %v", fresh, err)
			}
			l.exec(t, "INSERT INTO "+l.be.table("memory_failure_event")+" ("+l.be.ident("pfn")+", "+l.be.ident("hostname")+") VALUES ('0x1234', 'db-08')")
			fresh, err := st.Hosts(ctx, seen)
			if err != nil || len(fresh) != 1 || fresh[0].Name != "db-08" || fresh[0].Rows != 1 {
				t.Errorf("after one new row: %+v, %v", fresh, err)
			}

			// Rows from a time on: the comparison runs on the server.
			since := a.Events[len(a.Events)/2].ErrorDT
			var late adapter.Collected
			st.Adapter("db-07", ReadOptions{Since: since}).Collect(ctx, &late)
			want := 0
			for _, e := range a.Events {
				if !e.ErrorDT.Before(since) {
					want++
				}
			}
			if len(late.Events) != want || want == 0 || want == len(a.Events) {
				t.Errorf("%d events since %s, want %d of %d", len(late.Events), since, want, len(a.Events))
			}

			// Reading wrote nothing: the tables hold what was put in.
			var n int
			if err := l.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+l.be.table("mc_event")).Scan(&n); err != nil || int64(n) != by["db-07"].countOf(t, st, "mc_event")*2+by["cxl-01"].countOf(t, st, "mc_event")+1 {
				t.Errorf("mc_event holds %d rows after the reads (%v)", n, err)
			}
		})
		l.reset(t)
	}
}

// countOf returns the number of rows a host has in one table.
func (h StoreHost) countOf(t *testing.T, st *Store, table string) int64 {
	t.Helper()
	var n int64
	q := "SELECT COUNT(*) FROM " + st.be.table(table) + " WHERE " + st.be.ident("hostname") + " = " + st.be.arg(1)
	if err := st.db.QueryRowContext(ctx, q, h.Name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestStoreOnAServerMistakes: what a real server says when the settings
// are wrong must reach the reader without the password.
func TestStoreOnAServerMistakes(t *testing.T) {
	for _, l := range liveStores(t) {
		t.Run(l.name, func(t *testing.T) {
			l.reset(t)
			// The tables are gone: a database that is not a store.
			if _, err := OpenStore(ctx, l.cfg); err == nil || !strings.Contains(err.Error(), "no rasdaemon tables") {
				t.Errorf("empty database: %v", err)
			}
			wrong := l.cfg
			wrong.Password = "not-the-password-x9"
			_, err := OpenStore(ctx, wrong)
			if err == nil || strings.Contains(err.Error(), wrong.Password) || !strings.Contains(err.Error(), "rasdaemon store ") {
				t.Errorf("wrong password: %v", err)
			}
			t.Logf("wrong password: %v", err)
		})
	}
}
