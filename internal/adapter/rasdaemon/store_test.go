package rasdaemon

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testutil"
)

// fixtures are the SQLite fixtures of this package. Each is also served as
// the rows of one host of a store.
var fixtures = []string{"intel-skx-0.6.sql", "amd-upgraded-0.8.sql", "clock-step-0.8.sql", "ghes-1.0.sql", "cxl-1.0.sql"}

// servers are the kinds of server a store is read from, with the ways
// their drivers hand values over.
func servers() map[string]*fakeServer {
	text := newFakeServer(BackendMySQL)
	text.text = true
	other := newFakeServer(BackendPostgreSQL)
	other.schema = "ras_fleet"
	return map[string]*fakeServer{
		"mysql":                  newFakeServer(BackendMySQL),
		"mysql, values as text":  text,
		"postgresql":             newFakeServer(BackendPostgreSQL),
		"postgresql, own schema": other,
	}
}

// TestStoreReadsWhatTheFileHolds serves every SQLite fixture as the rows
// of one host of a store, on each kind of server, and compares what the
// store adapter reads with what the SQLite adapter reads from the file.
func TestStoreReadsWhatTheFileHolds(t *testing.T) {
	for _, fixture := range fixtures {
		path := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", fixture), "ras-mc_event.db")
		var file adapter.Collected
		fileRep := New(path, Options{Direct: true}).Collect(ctx, &file)
		if len(file.Events)+len(file.Actions) == 0 {
			t.Fatalf("%s: the file gives no records", fixture)
		}
		for name, srv := range servers() {
			t.Run(fixture+" on "+name, func(t *testing.T) {
				srv.load(t, fixture, "node-17", false)
				st := srv.store(t)
				a := st.Adapter("node-17", ReadOptions{})
				if a.Kind() != Kind || a.Origin() != srv.origin() || a.(interface{ HostHint() string }).HostHint() != "node-17" {
					t.Errorf("adapter: kind %q, origin %q", a.Kind(), a.Origin())
				}
				var got adapter.Collected
				rep := a.Collect(ctx, &got)

				sameAsFile(t, &got, rep, &file, fileRep, path, srv.origin())

				// The store was only read: every statement is a SELECT, and
				// the rows were read in one read-only transaction that sees
				// the tables as of one moment.
				for _, q := range srv.statements() {
					if !strings.HasPrefix(q, "SELECT ") {
						t.Errorf("statement that is not a SELECT: %s", q)
					}
				}
				if len(srv.txs) != 1 || !srv.txs[0].ReadOnly || sql.IsolationLevel(srv.txs[0].Isolation) != sql.LevelRepeatableRead || srv.ended != 1 {
					t.Errorf("transactions %+v, %d ended; want one, read-only, repeatable read, ended", srv.txs, srv.ended)
				}
			})
		}
	}
}

// sameAsFile compares what a store adapter read with what the SQLite
// adapter read from the file the rows of the store were taken from. The
// records must be the same, apart from what a store does not keep (the UTC
// offset of the host) and from where a record says it came from.
func sameAsFile(t *testing.T, got *adapter.Collected, rep adapter.Report, file *adapter.Collected, fileRep adapter.Report, path, origin string) {
	t.Helper()
	if rep.Status != fileRep.Status || rep.Records != fileRep.Records || rep.Skipped != fileRep.Skipped || rep.Detail != fileRep.Detail {
		t.Errorf("report: %s, %d records, %d skipped, %q; the file gives %s, %d, %d, %q",
			rep.Status, rep.Records, rep.Skipped, rep.Detail, fileRep.Status, fileRep.Records, fileRep.Skipped, fileRep.Detail)
	}
	if rep.HostHint != "node-17" || rep.Origin != origin {
		t.Errorf("report names host %q, origin %q", rep.HostHint, rep.Origin)
	}
	// The notes are those of the file, about the store. A table the
	// reader does not know has no host name and is not a table of
	// rasdaemon: it is not counted.
	var want []string
	for _, n := range fileRep.Notes {
		if !strings.Contains(n, "(unknown table)") {
			want = append(want, strings.ReplaceAll(n, path, origin))
		}
	}
	if !reflect.DeepEqual(rep.Notes, want) {
		t.Errorf("notes:\n %q\nthe file gives:\n %q", rep.Notes, want)
	}

	if len(got.Events) != len(file.Events) || len(got.Actions) != len(file.Actions) || len(got.Modules) != len(file.Modules) {
		t.Fatalf("%d events, %d actions, %d modules; the file gives %d, %d, %d",
			len(got.Events), len(got.Actions), len(got.Modules), len(file.Events), len(file.Actions), len(file.Modules))
	}
	for i, e := range got.Events {
		w := *file.Events[i]
		if !strings.HasPrefix(e.RawRef, origin+"#") || strings.TrimPrefix(e.RawRef, origin) != strings.TrimPrefix(w.RawRef, path) {
			t.Errorf("event %d comes from %q, in the file from %q", i, e.RawRef, w.RawRef)
		}
		if !strings.Contains(e.Raw, `"hostname":"node-17"`) {
			t.Errorf("event %d: the original row lacks the host name: %s", i, e.Raw)
		}
		if e.UTCOffset.Valid {
			t.Errorf("event %d has a UTC offset of %d s; a store does not keep one", i, e.UTCOffset.V)
		}
		g := *e
		g.RawRef, g.Raw, g.UTCOffset = w.RawRef, w.Raw, w.UTCOffset
		if g != w {
			t.Errorf("event %d (%s) differs:\n store %+v\n file  %+v", i, e.RawRef, g, w)
		}
	}
	for i, x := range got.Actions {
		w := *file.Actions[i]
		g := *x
		if strings.TrimPrefix(g.RawRef, origin) != strings.TrimPrefix(w.RawRef, path) {
			t.Errorf("action %d comes from %q, in the file from %q", i, g.RawRef, w.RawRef)
		}
		g.RawRef, g.Raw = w.RawRef, w.Raw
		if g != w {
			t.Errorf("action %d differs:\n store %+v\n file  %+v", i, g, w)
		}
	}
	for i, m := range got.Modules {
		w := *file.Modules[i]
		g := *m
		g.RawRef = strings.Replace(g.RawRef, origin, path, 1)
		if !reflect.DeepEqual(g, w) {
			t.Errorf("module %d differs:\n store %+v\n file  %+v", i, g, w)
		}
	}
}

// fleet is a store with the rows of three hosts in the same tables, rows
// without a host name, and a table that is not rasdaemon's.
func fleet(t *testing.T, srv *fakeServer) *Store {
	t.Helper()
	srv.load(t, "intel-skx-0.6.sql", "web-01", true)
	srv.load(t, "ghes-1.0.sql", "db-07", true)
	srv.load(t, "intel-skx-0.6.sql", "web-02", true)
	empty := ""
	stamp := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	row := map[string]any{"timestamp": stamp, "err_count": int64(1), "err_type": "Corrected", "err_msg": "memory read error", "label": "CPU0_DIMM_A1",
		"mc": int64(0), "top_layer": int64(0), "middle_layer": int64(0), "lower_layer": int64(-1), "address": int64(0x1000)}
	srv.add("mc_event", nil, row)
	srv.add("mc_event", &empty, row)
	srv.mu.Lock()
	srv.table("inventory", []fakeColumn{{"id", "serial"}, {"asset", "text"}}).rows = [][]any{{int64(1), "rack 12"}}
	srv.mu.Unlock()
	return srv.store(t)
}

func TestStoreHosts(t *testing.T) {
	for name, srv := range servers() {
		t.Run(name, func(t *testing.T) {
			st := fleet(t, srv)
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
			if !reflect.DeepEqual(names, []string{"", "db-07", "web-01", "web-02"}) {
				t.Fatalf("hosts %q", names)
			}
			// Rows without a host name, stored as NULL or as an empty
			// name, are counted under the empty name.
			if by[""].Rows != 2 {
				t.Errorf("%d rows without a host name, want 2", by[""].Rows)
			}
			// The two hosts with the same fixture hold the same number of
			// rows, each under its own numbers.
			if by["web-01"].Rows == 0 || by["web-01"].Rows != by["web-02"].Rows || by["web-01"].Last["mc_event"] >= by["web-02"].Last["mc_event"] {
				t.Errorf("web-01 %+v, web-02 %+v", by["web-01"], by["web-02"])
			}
			if by["db-07"].Last["extlog_event"] == 0 || by["web-01"].Last["extlog_event"] != 0 {
				t.Errorf("extlog rows: db-07 %+v, web-01 %+v", by["db-07"].Last, by["web-01"].Last)
			}

			// Each host reads as its own rows.
			var a, b, c adapter.Collected
			repA := st.Adapter("web-01", ReadOptions{}).Collect(ctx, &a)
			repB := st.Adapter("web-02", ReadOptions{}).Collect(ctx, &b)
			repC := st.Adapter("db-07", ReadOptions{}).Collect(ctx, &c)
			if repA.Records != repB.Records || len(a.Events) == 0 || len(a.Events) != len(b.Events) || repC.Records == repA.Records {
				t.Errorf("web-01 %d records, %d events; web-02 %d, %d; db-07 %d", repA.Records, len(a.Events), repB.Records, len(b.Events), repC.Records)
			}
			for i := range a.Events {
				x, y := *a.Events[i], *b.Events[i]
				if x.RawRef == y.RawRef || x.Fingerprint == y.Fingerprint {
					t.Errorf("event %d of two hosts is one row: %s", i, x.RawRef)
				}
				x.RawRef, x.Raw, x.Fingerprint, x.DupOfFingerprint = "", "", "", ""
				y.RawRef, y.Raw, y.Fingerprint, y.DupOfFingerprint = "", "", "", ""
				if x != y {
					t.Errorf("event %d differs between two hosts with the same rows:\n %+v\n %+v", i, x, y)
				}
			}
			for _, e := range c.Events {
				if !strings.Contains(e.Raw, `"hostname":"db-07"`) {
					t.Errorf("a row of another host among those of db-07: %s", e.Raw)
				}
			}
			// A machine check record still points at the EDAC record of
			// the same error, by the number the row has in the shared table.
			pairs := 0
			for _, e := range b.Events {
				if e.DupOfFingerprint == "" {
					continue
				}
				pairs++
				found := false
				for _, o := range b.Events {
					found = found || o.Fingerprint == e.DupOfFingerprint
				}
				if !found {
					t.Errorf("%s points at a record that is not among those of its host", e.RawRef)
				}
			}
			if pairs == 0 {
				t.Error("no machine check record was matched with its EDAC record")
			}

			// A host the store does not know has no rows; that is not an error.
			var none adapter.Collected
			if rep := st.Adapter("web-99", ReadOptions{}).Collect(ctx, &none); rep.Status != model.SourceEmpty || rep.Records != 0 || len(none.Events) != 0 {
				t.Errorf("unknown host: %+v", rep)
			}
			// Rows without a host name are not one host.
			if rep := st.Adapter("", ReadOptions{}).Collect(ctx, &none); rep.Status != model.SourceError || !strings.Contains(rep.Detail, "without a host name") {
				t.Errorf("empty host name: %+v", rep)
			}
		})
	}
}

// TestStoreHostsWithNewRows: a caller that passes the numbers it has seen
// is told which hosts have rows above them.
func TestStoreHostsWithNewRows(t *testing.T) {
	for name, srv := range servers() {
		t.Run(name, func(t *testing.T) {
			st := fleet(t, srv)
			all, err := st.Hosts(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]int64{}
			for _, h := range all {
				for table, last := range h.Last {
					seen[table] = max(seen[table], last)
				}
			}
			fresh, err := st.Hosts(ctx, seen)
			if err != nil || len(fresh) != 0 {
				t.Fatalf("nothing is new, yet: %+v, %v", fresh, err)
			}
			host := "web-01"
			id := srv.add("mc_event", &host, map[string]any{"timestamp": time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), "err_count": int64(1),
				"err_type": "Corrected", "err_msg": "memory read error", "label": "CPU_SrcID#0_MC#0_Chan#0_DIMM#0"})
			fresh, err = st.Hosts(ctx, seen)
			if err != nil || len(fresh) != 1 || fresh[0].Name != "web-01" || fresh[0].Rows != 1 || fresh[0].Last["mc_event"] != id {
				t.Errorf("after one new row: %+v, %v", fresh, err)
			}
			// The statement names the mark as an argument.
			log := srv.statements()
			last := log[len(log)-1]
			if !strings.Contains(last, " > ") || !strings.Contains(last, "GROUP BY") {
				t.Errorf("last statement: %s", last)
			}
		})
	}
}

func TestStoreSince(t *testing.T) {
	for name, srv := range servers() {
		t.Run(name, func(t *testing.T) {
			srv.load(t, "ghes-1.0.sql", "db-07", false)
			st := srv.store(t)
			var all, late adapter.Collected
			st.Adapter("db-07", ReadOptions{}).Collect(ctx, &all)
			// The rows of the fixture run from 2 to 6 October 2026, +0900.
			since := time.Date(2026, 10, 4, 12, 0, 0, 0, time.FixedZone("KST", 9*3600))
			rep := st.Adapter("db-07", ReadOptions{Since: since}).Collect(ctx, &late)
			if len(late.Events) == 0 || len(late.Events) >= len(all.Events) {
				t.Fatalf("%d events since %s, %d in all", len(late.Events), since, len(all.Events))
			}
			for _, e := range late.Events {
				if e.ErrorDT.Before(since) {
					t.Errorf("%s is from %s, before %s", e.RawRef, e.ErrorDT, since)
				}
			}
			n := 0
			for _, e := range all.Events {
				if !e.ErrorDT.Before(since) {
					n++
				}
			}
			if n != len(late.Events) || rep.Records >= len(all.Events)+len(all.Actions) {
				t.Errorf("%d events since, want %d; %d records", len(late.Events), n, rep.Records)
			}
		})
	}
}

func TestStoreMistakes(t *testing.T) {
	t.Run("the server does not answer", func(t *testing.T) {
		srv := newFakeServer(BackendMySQL)
		srv.pingErr = errors.New("dial tcp 10.0.0.9:3306: connect: connection refused")
		_, err := NewStore(ctx, srv.db(t), BackendMySQL, "", srv.origin())
		if err == nil || !strings.Contains(err.Error(), "rasdaemon store mysql://db.example:3306/rasdaemon: dial tcp 10.0.0.9:3306: connect: connection refused") {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("a database without the tables", func(t *testing.T) {
		srv := newFakeServer(BackendMySQL)
		srv.table("inventory", []fakeColumn{{"id", "serial"}})
		_, err := NewStore(ctx, srv.db(t), BackendMySQL, "", srv.origin())
		if err == nil || !strings.Contains(err.Error(), "no rasdaemon tables in the database") {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("another schema", func(t *testing.T) {
		srv := newFakeServer(BackendPostgreSQL)
		srv.load(t, "ghes-1.0.sql", "db-07", false)
		_, err := NewStore(ctx, srv.db(t), BackendPostgreSQL, "public", srv.origin())
		if err == nil || !strings.Contains(err.Error(), "no rasdaemon tables in schema public") {
			t.Errorf("error = %v", err)
		}
		if _, err := NewStore(ctx, srv.db(t), BackendPostgreSQL, `ras"; DROP TABLE mc_event; --`, srv.origin()); err == nil || !strings.Contains(err.Error(), "schema name") {
			t.Errorf("a schema name that is no identifier: %v", err)
		}
	})
	t.Run("tables of a SQLite layout", func(t *testing.T) {
		// Tables without the hostname column: the layout of the SQLite
		// file, copied to a server by hand. Rows of several hosts could
		// not be told apart there.
		srv := newFakeServer(BackendPostgreSQL)
		srv.table("mc_event", []fakeColumn{{"id", "serial"}, {"timestamp", "stamp"}, {"err_type", "text"}})
		_, err := NewStore(ctx, srv.db(t), BackendPostgreSQL, "", srv.origin())
		if err == nil || !strings.Contains(err.Error(), "table mc_event has no hostname column") {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("unknown backend", func(t *testing.T) {
		if _, err := NewStore(ctx, newFakeServer(BackendMySQL).db(t), "oracle", "", "x"); err == nil || !strings.Contains(err.Error(), `unknown backend "oracle"`) {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("the listing fails", func(t *testing.T) {
		for _, part := range []string{"information_schema.tables", "information_schema.columns"} {
			srv := newFakeServer(BackendMySQL)
			srv.load(t, "ghes-1.0.sql", "db-07", false)
			srv.fail[part] = errors.New("Error 1142 (42000): SELECT command denied to user")
			if _, err := NewStore(ctx, srv.db(t), BackendMySQL, "", srv.origin()); err == nil || !strings.Contains(err.Error(), "SELECT command denied") {
				t.Errorf("%s: error = %v", part, err)
			}
		}
	})
	t.Run("a table cannot be read", func(t *testing.T) {
		srv := newFakeServer(BackendMySQL)
		srv.load(t, "ghes-1.0.sql", "db-07", false)
		st := srv.store(t)
		srv.fail["FROM `extlog_event` WHERE"] = errors.New("Error 1142 (42000): SELECT command denied to user 'reader'@'10.0.0.5' for table 'extlog_event'")
		var got adapter.Collected
		rep := st.Adapter("db-07", ReadOptions{}).Collect(ctx, &got)
		if rep.Status != model.SourceError || !strings.Contains(rep.Detail, "table extlog_event: Error 1142") {
			t.Errorf("report = %+v", rep)
		}
		if srv.ended != 1 {
			t.Errorf("the transaction of a read that failed was left open")
		}
		srv.fail = map[string]error{"GROUP BY": errors.New("Error 2013 (HY000): Lost connection to MySQL server during query")}
		if _, err := st.Hosts(ctx, nil); err == nil || !strings.Contains(err.Error(), "mysql://db.example:3306/rasdaemon: table ") || !strings.Contains(err.Error(), "Lost connection") {
			t.Errorf("Hosts: error = %v", err)
		}
	})
	t.Run("no transaction", func(t *testing.T) {
		srv := newFakeServer(BackendPostgreSQL)
		srv.load(t, "ghes-1.0.sql", "db-07", false)
		st := srv.store(t)
		srv.beginErr = errors.New("pq: sorry, too many clients already")
		var got adapter.Collected
		rep := st.Adapter("db-07", ReadOptions{}).Collect(ctx, &got)
		if rep.Status != model.SourceError || rep.Detail != "pq: sorry, too many clients already" || len(got.Events) != 0 {
			t.Errorf("report = %+v", rep)
		}
	})
	t.Run("the ledger refuses a record", func(t *testing.T) {
		srv := newFakeServer(BackendMySQL)
		srv.load(t, "ghes-1.0.sql", "db-07", false)
		rep := srv.store(t).Adapter("db-07", ReadOptions{}).Collect(ctx, &refusing{})
		if rep.Status != model.SourceError || rep.SinkErr == nil || !strings.Contains(rep.Detail, "ledger rejected a record") {
			t.Errorf("report = %+v", rep)
		}
	})
	t.Run("a store that the caller opened stays open", func(t *testing.T) {
		srv := newFakeServer(BackendMySQL)
		srv.load(t, "ghes-1.0.sql", "db-07", false)
		db := srv.db(t)
		st, err := NewStore(ctx, db, BackendMySQL, "", srv.origin())
		if err != nil {
			t.Fatal(err)
		}
		if st.Origin() != srv.origin() || st.Close() != nil || db.PingContext(ctx) != nil {
			t.Error("Close closed a database the store did not open")
		}
	})
}

// refusing is a sink that takes nothing.
type refusing struct{ adapter.Collected }

func (*refusing) Event(*model.Event) error   { return errors.New("disk full") }
func (*refusing) Action(*model.Action) error { return errors.New("disk full") }

// TestSettle checks every form a driver hands a value over in.
func TestSettle(t *testing.T) {
	kst := time.FixedZone("KST", 9*3600)
	at := time.Date(2026, 10, 2, 9, 15, 30, 0, kst)
	kinds := []columnKind{colInt, colInt, colInt, colInt, colText, colText, colBlob, colBlob, colTime, colTime, colTime, colTime, colOther, colOther, colInt}
	vals := []any{int64(7), []byte("-12"), "0x1f", uint64(9), []byte("label"), "text", []byte{0, 1}, "blob", at, []byte("2026-10-02 00:15:30.000000"),
		"2026-10-02 00:15:30", nil, []byte("misc"), 3.5, []byte("not a number")}
	settle(vals, kinds)
	want := []any{int64(7), int64(-12), "0x1f", int64(9), "label", "text", []byte{0, 1}, []byte("blob"), "2026-10-02 00:15:30", "2026-10-02 00:15:30",
		"2026-10-02 00:15:30", nil, "misc", 3.5, "not a number"}
	if !reflect.DeepEqual(vals, want) {
		t.Errorf("settle:\n got  %#v\n want %#v", vals, want)
	}
	// More values than kinds: the rest stays as it is.
	rest := []any{[]byte("1"), []byte("2")}
	settle(rest, []columnKind{colInt})
	if !reflect.DeepEqual(rest, []any{int64(1), []byte("2")}) {
		t.Errorf("settle with fewer kinds: %#v", rest)
	}
	// The text a time becomes is a time the reader takes as UTC, without
	// an offset of the host.
	got, off, ok := parseTimestamp(vals[8].(string))
	if !ok || !got.Equal(at) || off.Valid {
		t.Errorf("parseTimestamp(%q) = %v, %v, %v", vals[8], got, off, ok)
	}
}

func TestColumnKinds(t *testing.T) {
	srv := newFakeServer(BackendMySQL)
	srv.table("mc_event", []fakeColumn{{"id", "serial"}, {"hostname", "varchar"}, {"timestamp", "stamp"}, {"mc", "int32"}, {"address", "int64"}, {"label", "text"}, {"cper", "blob"}})
	for _, dialect := range []string{BackendMySQL, BackendPostgreSQL} {
		srv.dialect, srv.schema = dialect, ""
		q := "SELECT * FROM `mc_event` WHERE `hostname` = ? ORDER BY `id`"
		if dialect == BackendPostgreSQL {
			srv.schema = "rasdaemon"
			q = `SELECT * FROM "rasdaemon"."mc_event" WHERE "hostname" = $1 ORDER BY "id"`
		}
		rows, err := srv.db(t).QueryContext(ctx, q, "h")
		if err != nil {
			t.Fatal(err)
		}
		kinds, err := columnKinds(rows)
		rows.Close()
		if err != nil || !reflect.DeepEqual(kinds, []columnKind{colInt, colText, colTime, colInt, colInt, colText, colBlob}) {
			t.Errorf("%s: kinds %v, %v", dialect, kinds, err)
		}
	}
}

func TestStoreConfigFromEnv(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	for _, tt := range []struct {
		name    string
		backend string
		env     func(string) string
		want    StoreConfig
	}{
		{"mysql defaults", "mysql", env(),
			StoreConfig{Backend: BackendMySQL, Port: 3306, User: "rasdaemon", Database: "rasdaemon", ConnectTimeout: 10 * time.Second}},
		{"mysql settings", "", env("RASDAEMON_DB_BACKEND", "mysql", "RAS_MYSQL_HOST", "db.example", "RAS_MYSQL_PORT", "3307", "RAS_MYSQL_USER", "reader",
			"RAS_MYSQL_PASSWORD", "s3 cret", "RAS_MYSQL_DATABASE", "ras", "RAS_MYSQL_USE_SSL", "true", "RAS_MYSQL_CONNECT_TIMEOUT", "3", "RAS_PG_HOST", "other"),
			StoreConfig{Backend: BackendMySQL, Host: "db.example", Port: 3307, User: "reader", Password: "s3 cret", Database: "ras", TLS: "require", ConnectTimeout: 3 * time.Second}},
		{"mariadb is mysql", "MariaDB", env("RAS_MYSQL_SOCKET", "/run/mysqld/mysqld.sock", "RAS_MYSQL_USE_SSL", "no", "RAS_MYSQL_PORT", "0"),
			StoreConfig{Backend: BackendMySQL, Port: 3306, Socket: "/run/mysqld/mysqld.sock", User: "rasdaemon", Database: "rasdaemon", ConnectTimeout: 10 * time.Second}},
		{"postgresql defaults", "postgresql", env(),
			StoreConfig{Backend: BackendPostgreSQL, Port: 5432, User: "rasdaemon", Database: "rasdaemon", Schema: "rasdaemon", ConnectTimeout: 10 * time.Second}},
		{"postgresql settings", "pg", env("RAS_PG_HOST", "pg.example", "RAS_PG_PORT", "6432", "RAS_PG_USER", "reader", "RAS_PG_PASSWORD", "pw", "RAS_PG_DATABASE", "fleet",
			"RAS_PG_SCHEMA", "ras", "RAS_PG_SSL_MODE", "verify-full", "RAS_PG_USE_SSL", "false", "RAS_PG_CONNECT_TIMEOUT", "30"),
			StoreConfig{Backend: BackendPostgreSQL, Host: "pg.example", Port: 6432, User: "reader", Password: "pw", Database: "fleet", Schema: "ras", TLS: "verify-full", ConnectTimeout: 30 * time.Second}},
		// rasdaemon takes the mode "false" as no mode, and asks for
		// "require" when only the switch is on.
		{"postgresql TLS switch", "postgres", env("RASDAEMON_DB_BACKEND", "mysql", "RAS_PG_SSL_MODE", "false", "RAS_PG_USE_SSL", "1"),
			StoreConfig{Backend: BackendPostgreSQL, Port: 5432, User: "rasdaemon", Database: "rasdaemon", Schema: "rasdaemon", TLS: "require", ConnectTimeout: 10 * time.Second}},
	} {
		got, err := StoreConfigFromEnv(tt.backend, tt.env)
		if err != nil || got != tt.want {
			t.Errorf("%s:\n got  %+v, %v\n want %+v", tt.name, got, err, tt.want)
		}
	}
	for _, tt := range []struct {
		name    string
		backend string
		env     func(string) string
		want    string
	}{
		{"no backend", "", env(), "no backend named: want mysql or postgresql"},
		{"sqlite3", "", env("RASDAEMON_DB_BACKEND", "sqlite3"), "the sqlite3 backend keeps a file on each host"},
		{"unknown", "oracle", env(), `unknown backend "oracle"`},
		{"port", "mysql", env("RAS_MYSQL_PORT", "mysql"), `RAS_MYSQL_PORT="mysql": want a number`},
		{"port out of range", "postgresql", env("RAS_PG_PORT", "70000"), "port 70000 is out of range"},
		{"timeout", "postgresql", env("RAS_PG_CONNECT_TIMEOUT", "-1"), `RAS_PG_CONNECT_TIMEOUT="-1": want a number`},
		{"mysql timeout", "mysql", env("RAS_MYSQL_CONNECT_TIMEOUT", "soon"), `RAS_MYSQL_CONNECT_TIMEOUT="soon"`},
		{"pg port", "pg", env("RAS_PG_PORT", "x"), `RAS_PG_PORT="x"`},
		{"schema", "postgresql", env("RAS_PG_SCHEMA", "ras fleet"), `schema name "ras fleet"`},
		{"sslmode", "postgresql", env("RAS_PG_SSL_MODE", "always"), `sslmode "always"`},
	} {
		if _, err := StoreConfigFromEnv(tt.backend, tt.env); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: error = %v, want %q", tt.name, err, tt.want)
		}
	}
	for in, want := range map[string]string{"mysql": "mysql", " MariaDB ": "mysql", "postgresql": "postgresql", "Postgres": "postgresql", "pg": "postgresql", "sqlite3": "", "": ""} {
		if got := BackendName(in); got != want {
			t.Errorf("BackendName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEnvFile(t *testing.T) {
	got, err := ParseEnvFile(strings.NewReader("\xef\xbb\xbf# rasdaemon settings\n\n; a comment\nRASDAEMON_DB_BACKEND=\"postgresql\"\n" +
		"RAS_PG_HOST = pg.example \nexport RAS_PG_PASSWORD='p\"w=1 #x'\nRAS_PG_SCHEMA=\nRAS_PG_USER=\"\n"))
	want := map[string]string{"RASDAEMON_DB_BACKEND": "postgresql", "RAS_PG_HOST": "pg.example", "RAS_PG_PASSWORD": `p"w=1 #x`, "RAS_PG_SCHEMA": "", "RAS_PG_USER": `"`}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ParseEnvFile = %q, %v\nwant %q", got, err, want)
	}
	for _, bad := range []string{"RAS_PG_HOST pg.example\n", "A=1\n=2\n", "MY NAME=x\n"} {
		if _, err := ParseEnvFile(strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "want NAME=value") {
			t.Errorf("ParseEnvFile(%q) = %v", bad, err)
		}
	}
	if _, err := ParseEnvFile(strings.NewReader("A=1\n=2\n")); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("the error does not name the line: %v", err)
	}
	// The settings of a file reach the configuration.
	cfg, err := StoreConfigFromEnv("", func(k string) string { return got[k] })
	if err != nil || cfg.Backend != BackendPostgreSQL || cfg.Host != "pg.example" || cfg.Password != `p"w=1 #x` || cfg.Schema != "rasdaemon" {
		t.Errorf("configuration from the file: %+v, %v", cfg, err)
	}
}

// TestConnector checks where a configuration connects to and what the
// store is called. The name must not hold the user or the password.
func TestConnector(t *testing.T) {
	for _, tt := range []struct {
		cfg    StoreConfig
		origin string
	}{
		{StoreConfig{Backend: BackendMySQL, Host: "db.example", User: "reader", Password: "s3cret", Database: "ras"}, "mysql://db.example:3306/ras"},
		{StoreConfig{Backend: BackendMySQL, Host: "2001:db8::7", Port: 3307, User: "reader", Password: "s3cret", TLS: "require"}, "mysql://[2001:db8::7]:3307/rasdaemon"},
		{StoreConfig{Backend: BackendMySQL, Socket: "/tmp/test-mysql.sock", User: "reader", Password: "s3cret"}, "mysql://localhost/rasdaemon"},
		{StoreConfig{Backend: BackendPostgreSQL, Host: "pg.example", User: "reader", Password: "s3 'cret\\", Database: "fleet", Schema: "ras"}, "postgresql://pg.example:5432/fleet?schema=ras"},
		{StoreConfig{Backend: BackendPostgreSQL, Host: "10.0.0.5", Port: 6432, TLS: "verify-full", ConnectTimeout: 1500 * time.Millisecond}, "postgresql://10.0.0.5:6432/rasdaemon?schema=rasdaemon"},
		{StoreConfig{Backend: BackendPostgreSQL, User: "reader"}, "postgresql://localhost/rasdaemon?schema=rasdaemon"},
	} {
		conn, origin, err := tt.cfg.connector()
		if err != nil || conn == nil || origin != tt.origin {
			t.Errorf("%+v: origin %q, %v; want %q", tt.cfg, origin, err, tt.origin)
		}
		if strings.Contains(origin, "reader") || strings.Contains(origin, "cret") {
			t.Errorf("the name of the store holds the user or the password: %s", origin)
		}
	}
	for _, tt := range []struct {
		cfg  StoreConfig
		want string
	}{
		{StoreConfig{Backend: "oracle"}, `unknown backend "oracle"`},
		{StoreConfig{Backend: BackendMySQL, Host: "db", Port: 99999}, "port 99999 is out of range"},
		{StoreConfig{Backend: BackendMySQL, Host: "db", TLS: "verify-full"}, `TLS setting "verify-full"`},
		{StoreConfig{Backend: BackendPostgreSQL, Host: "db", Schema: "a-b"}, `schema name "a-b"`},
	} {
		if _, _, err := tt.cfg.connector(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%+v: error = %v, want %q", tt.cfg, err, tt.want)
		}
	}
	// No server named and no socket where a local server would put one.
	saved := mysqlSockets
	mysqlSockets = []string{"/nonexistent/mysqld.sock"}
	defer func() { mysqlSockets = saved }()
	if _, _, err := (&StoreConfig{Backend: BackendMySQL}).connector(); err == nil || !strings.Contains(err.Error(), "set RAS_MYSQL_HOST or RAS_MYSQL_SOCKET") {
		t.Errorf("no local server: error = %v", err)
	}
	// OpenStore says which store it could not reach, without the password.
	_, err := OpenStore(ctx, StoreConfig{Backend: BackendMySQL, Host: "127.0.0.1", Port: 1, User: "reader", Password: "s3cret", ConnectTimeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "rasdaemon store mysql://127.0.0.1:1/rasdaemon: ") || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("OpenStore on a closed port: %v", err)
	}
	if _, err := OpenStore(ctx, StoreConfig{Backend: "oracle"}); err == nil || !strings.Contains(err.Error(), "rasdaemon store: unknown backend") {
		t.Errorf("OpenStore with an unknown backend: %v", err)
	}
}

func TestBackendSQL(t *testing.T) {
	my, _ := newBackend("mariadb", "ignored")
	pg, _ := newBackend("postgres", "")
	if my.table("mc_event") != "`mc_event`" || pg.table("mc_event") != `"rasdaemon"."mc_event"` {
		t.Errorf("tables: %s, %s", my.table("mc_event"), pg.table("mc_event"))
	}
	if my.ident("we`ird") != "`we``ird`" || pg.ident(`we"ird`) != `"we""ird"` {
		t.Errorf("identifiers: %s, %s", my.ident("we`ird"), pg.ident(`we"ird`))
	}
	if my.arg(2) != "?" || pg.arg(2) != "$2" {
		t.Errorf("arguments: %s, %s", my.arg(2), pg.arg(2))
	}
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.FixedZone("KST", 9*3600))
	if my.stamp(at) != "2026-10-04 03:00:00" {
		t.Errorf("mysql stamp: %v", my.stamp(at))
	}
	if v, ok := pg.stamp(at).(time.Time); !ok || !v.Equal(at) || v.Location() != time.UTC {
		t.Errorf("postgresql stamp: %v", pg.stamp(at))
	}
	m := &remote{be: pg, host: "h", since: at}
	cond, args := m.where()
	if cond != `"hostname" = $1 AND "timestamp" >= $2` || len(args) != 2 {
		t.Errorf("where: %s %v", cond, args)
	}
	var _ driver.Connector = fakeConnector{}
	_ = fmt.Sprint
}
