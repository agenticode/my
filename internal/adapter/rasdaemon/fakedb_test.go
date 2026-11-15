package rasdaemon

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agenticode/my/internal/testutil"
)

// fakeServer stands in for a MySQL or a PostgreSQL server that holds the
// tables of rasdaemon. It knows the statements this package sends, in the
// dialect of the server it plays, and nothing else: a statement in the
// wrong dialect, or one that is not a SELECT, is an error. It hands values
// over in the Go types the real driver hands them over in.
type fakeServer struct {
	dialect string // BackendMySQL or BackendPostgreSQL
	// schema is the PostgreSQL schema that holds the tables.
	schema string
	// text makes a MySQL server hand every value over as text, as the
	// driver does without parseTime and on the text protocol.
	text bool
	// loc is the zone a PostgreSQL session reports times in.
	loc *time.Location

	mu     sync.Mutex
	tables map[string]*fakeTable
	// log holds every statement, in order; txs the options of every
	// transaction; ended counts the transactions that were closed.
	log   []string
	txs   []driver.TxOptions
	ended int
	// fail maps a part of a statement to the error the statement gets.
	fail     map[string]error
	pingErr  error
	beginErr error
}

type fakeColumn struct {
	name string
	// typ is serial, int32, int64, stamp, text, blob or varchar.
	typ string
}

type fakeTable struct {
	cols []fakeColumn
	rows [][]any
}

func newFakeServer(dialect string) *fakeServer {
	s := &fakeServer{dialect: dialect, tables: map[string]*fakeTable{}, fail: map[string]error{}, loc: time.FixedZone("KST", 9*3600)}
	if dialect == BackendPostgreSQL {
		s.schema = "rasdaemon"
	}
	return s
}

// db opens a connection pool to the server.
func (s *fakeServer) db(t *testing.T) *sql.DB {
	t.Helper()
	db := sql.OpenDB(fakeConnector{s})
	t.Cleanup(func() { db.Close() })
	return db
}

// store opens the server as a rasdaemon store.
func (s *fakeServer) store(t *testing.T) *Store {
	t.Helper()
	st, err := NewStore(ctx, s.db(t), s.dialect, s.schema, s.origin())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (s *fakeServer) origin() string {
	if s.dialect == BackendPostgreSQL {
		return "postgresql://db.example:5432/rasdaemon?schema=" + s.schema
	}
	return "mysql://db.example:3306/rasdaemon"
}

// statements returns the statements the server got so far.
func (s *fakeServer) statements() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.log...)
}

// typeName is the type name the driver of the dialect reports for a column.
func (s *fakeServer) typeName(typ string) string {
	if s.dialect == BackendPostgreSQL {
		return map[string]string{"serial": "INT8", "int32": "INT4", "int64": "INT8", "stamp": "TIMESTAMPTZ", "text": "TEXT", "blob": "BYTEA", "varchar": "TEXT"}[typ]
	}
	return map[string]string{"serial": "BIGINT", "int32": "INT", "int64": "BIGINT", "stamp": "DATETIME", "text": "TEXT", "blob": "BLOB", "varchar": "VARCHAR"}[typ]
}

// hand turns a stored value into what the driver of the dialect hands over.
func (s *fakeServer) hand(v any) driver.Value {
	switch x := v.(type) {
	case nil:
		return nil
	case int64:
		if s.dialect == BackendMySQL && s.text {
			return []byte(strconv.FormatInt(x, 10))
		}
		return x
	case string:
		if s.dialect == BackendMySQL {
			return []byte(x)
		}
		return x
	case []byte:
		return x
	case time.Time:
		switch {
		case s.dialect == BackendPostgreSQL:
			return x.In(s.loc)
		case s.text:
			return []byte(x.UTC().Format("2006-01-02 15:04:05.000000"))
		}
		return x.UTC()
	}
	panic(fmt.Sprintf("fake server: value of type %T", v))
}

// table returns the table, creating it with the given columns.
func (s *fakeServer) table(name string, cols []fakeColumn) *fakeTable {
	t := s.tables[name]
	if t == nil {
		t = &fakeTable{cols: cols}
		s.tables[name] = t
	}
	return t
}

// add appends a row to a table: values by column name. host nil stores no
// host name. The row gets the next id of the table unless it brings one.
func (s *fakeServer) add(table string, host *string, vals map[string]any) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tables[table]
	if t == nil {
		panic("fake server: no table " + table)
	}
	row := make([]any, len(t.cols))
	var id int64
	for i, c := range t.cols {
		switch c.name {
		case "hostname":
			if host != nil {
				row[i] = *host
			}
		case "id":
			if v, ok := vals["id"]; ok {
				id = v.(int64)
			} else {
				for _, r := range t.rows {
					id = max(id, r[i].(int64))
				}
				id++
			}
			row[i] = id
		default:
			row[i] = vals[c.name]
		}
	}
	t.rows = append(t.rows, row)
	return id
}

// load puts the rows of a SQLite fixture into the server as the rows of
// one host: the tables get the hostname column after their first column,
// times become time values, and every value takes the type of its column.
// With renumber the rows get new numbers above those in the table, as rows
// of a table that several hosts write to have them.
func (s *fakeServer) load(t *testing.T, fixture, host string, renumber bool) {
	t.Helper()
	path := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", fixture), "fixture-"+host+".db")
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	names, err := listTables(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		info, err := db.Query(`PRAGMA table_info("` + name + `")`)
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
			if len(cols) == 1 {
				cols = append(cols, fakeColumn{"hostname", "varchar"})
			}
		}
		info.Close()
		s.mu.Lock()
		tab := s.table(name, cols)
		s.mu.Unlock()

		rows, err := db.Query(`SELECT * FROM "` + name + `" ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		fromCols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(fromCols))
			ptrs := make([]any, len(fromCols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			byName := map[string]any{}
			for i, col := range fromCols {
				typ := "text"
				for _, c := range tab.cols {
					if c.name == col {
						typ = c.typ
					}
				}
				byName[col] = coerce(vals[i], typ)
			}
			if renumber {
				delete(byName, "id")
			}
			s.add(name, &host, byName)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
}

// fixtureType is the type a column of a fixture has on a server: the type
// rasdaemon 1.0.0 declares for it, or the one its SQLite declaration
// stands for.
func fixtureType(table, col, decl string) string {
	for _, c := range schema10[table] {
		if c.name == col {
			return c.typ
		}
	}
	switch {
	case col == "id":
		return "serial"
	case col == "timestamp":
		return "stamp"
	case strings.Contains(strings.ToUpper(decl), "INT"):
		return "int64"
	case strings.Contains(strings.ToUpper(decl), "BLOB"):
		return "blob"
	}
	return "text"
}

// coerce gives a SQLite value the type of a column of a server. SQLite
// takes any value in any column; a server does not.
func coerce(v any, typ string) any {
	if v == nil {
		return nil
	}
	switch typ {
	case "serial", "int32", "int64":
		switch x := v.(type) {
		case int64:
			return x
		case string:
			if n, err := strconv.ParseInt(strings.TrimSpace(x), 0, 64); err == nil {
				return n
			}
		case float64:
			return int64(x)
		}
		return nil
	case "stamp":
		if text, ok := v.(string); ok {
			if at, _, ok := parseTimestamp(text); ok {
				return at
			}
		}
		return nil
	case "blob":
		switch x := v.(type) {
		case []byte:
			return x
		case string:
			return []byte(x)
		}
		return nil
	}
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	}
	return nil
}

type fakeConnector struct{ s *fakeServer }

func (c fakeConnector) Connect(context.Context) (driver.Conn, error) { return &fakeConn{s: c.s}, nil }
func (c fakeConnector) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake server: use the connector")
}

type fakeConn struct{ s *fakeServer }

func (c *fakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("fake server: prepared statements are not supported")
}
func (c *fakeConn) Close() error { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *fakeConn) Ping(context.Context) error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	return c.s.pingErr
}

func (c *fakeConn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if c.s.beginErr != nil {
		return nil, c.s.beginErr
	}
	c.s.txs = append(c.s.txs, opts)
	return fakeTx{c.s}, nil
}

type fakeTx struct{ s *fakeServer }

func (t fakeTx) Commit() error   { return t.Rollback() }
func (t fakeTx) Rollback() error { t.s.mu.Lock(); t.s.ended++; t.s.mu.Unlock(); return nil }

// The statements of each dialect. An identifier is in backticks for MySQL
// and in double quotes, with the schema in front, for PostgreSQL; an
// argument is ? for MySQL and $n for PostgreSQL.
var (
	mysqlTables  = regexp.MustCompile(`^SELECT table_name FROM information_schema\.tables WHERE table_schema = DATABASE\(\) ORDER BY table_name$`)
	mysqlColumns = regexp.MustCompile(`^SELECT table_name FROM information_schema\.columns WHERE table_schema = DATABASE\(\) AND column_name = 'hostname'$`)
	mysqlHosts   = regexp.MustCompile("^SELECT `hostname`, COUNT\\(\\*\\), MAX\\(`id`\\) FROM `(\\w+)`( WHERE `id` > \\?)? GROUP BY `hostname`$")
	mysqlRows    = regexp.MustCompile("^SELECT \\* FROM `(\\w+)` WHERE `hostname` = \\?( AND `timestamp` >= \\?)? ORDER BY `id`$")
	mysqlCount   = regexp.MustCompile("^SELECT COUNT\\(\\*\\) FROM `(\\w+)` WHERE `hostname` = \\?( AND `timestamp` >= \\?)?$")

	pgTables  = regexp.MustCompile(`^SELECT table_name FROM information_schema\.tables WHERE table_schema = \$1 ORDER BY table_name$`)
	pgColumns = regexp.MustCompile(`^SELECT table_name FROM information_schema\.columns WHERE table_schema = \$1 AND column_name = 'hostname'$`)
	pgHosts   = regexp.MustCompile(`^SELECT "hostname", COUNT\(\*\), MAX\("id"\) FROM "(\w+)"\."(\w+)"( WHERE "id" > \$1)? GROUP BY "hostname"$`)
	pgRows    = regexp.MustCompile(`^SELECT \* FROM "(\w+)"\."(\w+)" WHERE "hostname" = \$1( AND "timestamp" >= \$2)? ORDER BY "id"$`)
	pgCount   = regexp.MustCompile(`^SELECT COUNT\(\*\) FROM "(\w+)"\."(\w+)" WHERE "hostname" = \$1( AND "timestamp" >= \$2)?$`)
)

func (c *fakeConn) QueryContext(_ context.Context, query string, named []driver.NamedValue) (driver.Rows, error) {
	s := c.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, query)
	for part, err := range s.fail {
		if strings.Contains(query, part) {
			return nil, err
		}
	}
	args := make([]any, len(named))
	for i, a := range named {
		args[i] = a.Value
	}
	pg := s.dialect == BackendPostgreSQL

	// Which tables there are, and which of them have a hostname column.
	listing, withHost := mysqlTables.MatchString(query), mysqlColumns.MatchString(query)
	if pg {
		listing, withHost = pgTables.MatchString(query), pgColumns.MatchString(query)
	}
	if listing || withHost {
		out := &fakeRows{s: s, cols: []fakeColumn{{"table_name", "varchar"}}}
		if pg && (len(args) != 1 || args[0] != s.schema) {
			// Another schema: it holds nothing.
			return out, nil
		}
		var names []string
		for name, t := range s.tables {
			has := false
			for _, col := range t.cols {
				has = has || col.name == "hostname"
			}
			if listing || has {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			out.rows = append(out.rows, []any{name})
		}
		return out, nil
	}

	// The statements about one table.
	for kind, re := range map[string]*regexp.Regexp{"hosts": mysqlHosts, "rows": mysqlRows, "count": mysqlCount} {
		if pg {
			re = map[string]*regexp.Regexp{"hosts": pgHosts, "rows": pgRows, "count": pgCount}[kind]
		}
		m := re.FindStringSubmatch(query)
		if m == nil {
			continue
		}
		name, bounded := m[1], m[2] != ""
		if pg {
			if m[1] != s.schema {
				return nil, fmt.Errorf(`pq: schema "%s" does not exist`, m[1])
			}
			name, bounded = m[2], m[3] != ""
		}
		t := s.tables[name]
		if t == nil {
			return nil, fmt.Errorf("fake server: table %s does not exist", name)
		}
		col := func(n string) int {
			for i, c := range t.cols {
				if c.name == n {
					return i
				}
			}
			return -1
		}
		idCol, hostCol, timeCol := col("id"), col("hostname"), col("timestamp")
		if idCol < 0 || hostCol < 0 {
			return nil, fmt.Errorf("fake server: unknown column in table %s", name)
		}

		if kind == "hosts" {
			var mark int64
			if bounded {
				var ok bool
				if mark, ok = args[0].(int64); !ok || len(args) != 1 {
					return nil, fmt.Errorf("fake server: arguments %v for %s", args, query)
				}
			} else if len(args) != 0 {
				return nil, fmt.Errorf("fake server: arguments %v for %s", args, query)
			}
			type group struct {
				host    any
				n, last int64
			}
			by := map[any]*group{}
			var order []*group
			for _, r := range t.rows {
				id := r[idCol].(int64)
				if id <= mark {
					continue
				}
				g := by[r[hostCol]]
				if g == nil {
					g = &group{host: r[hostCol]}
					by[r[hostCol]] = g
					order = append(order, g)
				}
				g.n++
				g.last = max(g.last, id)
			}
			out := &fakeRows{s: s, cols: []fakeColumn{{"hostname", "varchar"}, {"count", "int64"}, {"max", "int64"}}}
			for _, g := range order {
				out.rows = append(out.rows, []any{g.host, g.n, g.last})
			}
			return out, nil
		}

		// The rows of one host, perhaps from a time on.
		want := 1
		if bounded {
			want = 2
		}
		host, ok := args[0].(string)
		if !ok || len(args) != want {
			return nil, fmt.Errorf("fake server: arguments %v for %s", args, query)
		}
		var since time.Time
		if bounded {
			if timeCol < 0 {
				return nil, fmt.Errorf("fake server: table %s has no timestamp column", name)
			}
			switch v := args[1].(type) {
			case time.Time:
				if !pg {
					return nil, errors.New("fake server: a time value compared with a column without a zone")
				}
				since = v
			case string:
				if pg {
					return nil, errors.New("fake server: text compared with a timestamptz column")
				}
				var err error
				if since, err = time.Parse("2006-01-02 15:04:05", v); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("fake server: argument %v for the time", v)
			}
		}
		var picked [][]any
		for _, r := range t.rows {
			if r[hostCol] != host {
				continue
			}
			if bounded {
				at, ok := r[timeCol].(time.Time)
				if !ok || at.Before(since) {
					continue
				}
			}
			picked = append(picked, r)
		}
		if kind == "count" {
			return &fakeRows{s: s, cols: []fakeColumn{{"count", "int64"}}, rows: [][]any{{int64(len(picked))}}}, nil
		}
		sort.SliceStable(picked, func(i, j int) bool { return picked[i][idCol].(int64) < picked[j][idCol].(int64) })
		return &fakeRows{s: s, cols: t.cols, rows: picked}, nil
	}
	return nil, fmt.Errorf("fake %s server: syntax error in %q", s.dialect, query)
}

type fakeRows struct {
	s    *fakeServer
	cols []fakeColumn
	rows [][]any
	next int
}

func (r *fakeRows) Columns() []string {
	out := make([]string, len(r.cols))
	for i, c := range r.cols {
		out[i] = c.name
	}
	return out
}

func (r *fakeRows) ColumnTypeDatabaseTypeName(i int) string { return r.s.typeName(r.cols[i].typ) }
func (r *fakeRows) Close() error                            { return nil }

func (r *fakeRows) Next(dest []driver.Value) error {
	if r.next >= len(r.rows) {
		return io.EOF
	}
	for i, v := range r.rows[r.next] {
		dest[i] = r.s.hand(v)
	}
	r.next++
	return nil
}
