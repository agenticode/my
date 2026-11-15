// Package storetest stands in for the database server a fleet's rasdaemons
// write to, in tests: a database/sql driver that holds tables in memory.
//
// The server knows the statements the reader of package rasdaemon sends,
// in the dialect of the server it plays, and nothing else: a statement in
// the wrong dialect, or one that is not a SELECT, is an error. It hands
// values over in the Go types the real driver hands them over in.
package storetest

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

	_ "modernc.org/sqlite" // to read the fixtures

	"github.com/agenticode/my/internal/testutil"
)

// Dialects of a server, by the names rasdaemon gives its backends.
const (
	MySQL      = "mysql"
	PostgreSQL = "postgresql"
)

// Server is a stand-in for a MySQL or a PostgreSQL server that holds the
// tables of rasdaemon.
type Server struct {
	// Dialect is MySQL or PostgreSQL.
	Dialect string
	// Schema is the PostgreSQL schema that holds the tables.
	Schema string
	// Text makes a MySQL server hand every value over as text, as the
	// driver does without parseTime and on the text protocol.
	Text bool
	// Loc is the zone a PostgreSQL session reports times in.
	Loc *time.Location
	// Types, when set, gives the type of a column of a fixture: serial,
	// int32, int64, stamp, text or blob. An empty answer leaves the choice
	// to the declaration of the column in the fixture.
	Types func(table, column string) string

	// Fail maps a part of a statement to the error the statement gets.
	Fail map[string]error
	// PingErr and BeginErr fail a ping and the start of a transaction.
	PingErr  error
	BeginErr error

	mu     sync.Mutex
	tables map[string]*table
	// log holds every statement, in order; txs the options of every
	// transaction; ended counts the transactions that were closed.
	log   []string
	txs   []driver.TxOptions
	ended int
}

// Column is a column of a table: its name and its type, which is serial,
// int32, int64, stamp, text, blob or varchar.
type Column struct {
	Name string
	Type string
}

type table struct {
	cols []Column
	rows [][]any
}

// NewServer returns an empty server of a dialect.
func NewServer(dialect string) *Server {
	s := &Server{Dialect: dialect, tables: map[string]*table{}, Fail: map[string]error{}, Loc: time.FixedZone("KST", 9*3600)}
	if dialect == PostgreSQL {
		s.Schema = "rasdaemon"
	}
	return s
}

// DB opens a connection pool to the server. It is closed when the test
// ends.
func (s *Server) DB(tb testing.TB) *sql.DB {
	tb.Helper()
	db := sql.OpenDB(connector{s})
	tb.Cleanup(func() { db.Close() })
	return db
}

// Origin is the name a store on this server goes by.
func (s *Server) Origin() string {
	if s.Dialect == PostgreSQL {
		return "postgresql://db.example:5432/rasdaemon?schema=" + s.Schema
	}
	return "mysql://db.example:3306/rasdaemon"
}

// Statements returns the statements the server got so far.
func (s *Server) Statements() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.log...)
}

// Transactions returns the options of the transactions that were begun,
// and how many of them have ended.
func (s *Server) Transactions() ([]driver.TxOptions, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]driver.TxOptions{}, s.txs...), s.ended
}

// typeName is the type name the driver of the dialect reports for a column.
func (s *Server) typeName(typ string) string {
	if s.Dialect == PostgreSQL {
		return map[string]string{"serial": "INT8", "int32": "INT4", "int64": "INT8", "stamp": "TIMESTAMPTZ", "text": "TEXT", "blob": "BYTEA", "varchar": "TEXT"}[typ]
	}
	return map[string]string{"serial": "BIGINT", "int32": "INT", "int64": "BIGINT", "stamp": "DATETIME", "text": "TEXT", "blob": "BLOB", "varchar": "VARCHAR"}[typ]
}

// hand turns a stored value into what the driver of the dialect hands over.
func (s *Server) hand(v any) driver.Value {
	switch x := v.(type) {
	case nil:
		return nil
	case int64:
		if s.Dialect == MySQL && s.Text {
			return []byte(strconv.FormatInt(x, 10))
		}
		return x
	case string:
		if s.Dialect == MySQL {
			return []byte(x)
		}
		return x
	case []byte:
		return x
	case time.Time:
		switch {
		case s.Dialect == PostgreSQL:
			return x.In(s.Loc)
		case s.Text:
			return []byte(x.UTC().Format("2006-01-02 15:04:05.000000"))
		}
		return x.UTC()
	}
	panic(fmt.Sprintf("storetest: value of type %T", v))
}

// Table creates a table with the given columns, unless it is there, and
// puts rows into it: the values of every row in the order of the columns.
func (s *Server) Table(name string, cols []Column, rows ...[]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.table(name, cols)
	t.rows = append(t.rows, rows...)
}

// table returns the table, creating it with the given columns.
func (s *Server) table(name string, cols []Column) *table {
	t := s.tables[name]
	if t == nil {
		t = &table{cols: cols}
		s.tables[name] = t
	}
	return t
}

// Add appends a row to a table: values by column name. host nil stores no
// host name. The row gets the next id of the table unless it brings one.
// Add returns the id.
func (s *Server) Add(table string, host *string, vals map[string]any) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tables[table]
	if t == nil {
		panic("storetest: no table " + table)
	}
	row := make([]any, len(t.cols))
	var id int64
	for i, c := range t.cols {
		switch c.Name {
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
			row[i] = vals[c.Name]
		}
	}
	t.rows = append(t.rows, row)
	return id
}

// Load puts the rows of a SQLite fixture of testdata/rasdaemon into the
// server as the rows of one host: the tables get the hostname column after
// their first column, times become time values, and every value takes the
// type of its column. With renumber the rows get new numbers above those
// in the table, as rows of a table that several hosts write to have them.
func (s *Server) Load(t testing.TB, fixture, host string, renumber bool) {
	t.Helper()
	path := testutil.BuildSQLite(t, testutil.Testdata(t, "rasdaemon", fixture), "fixture-"+host+".db")
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	names, err := sqliteTables(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		info, err := db.Query(`PRAGMA table_info("` + name + `")`)
		if err != nil {
			t.Fatal(err)
		}
		var cols []Column
		for info.Next() {
			var cid, notNull, pk int
			var col, decl string
			var def sql.NullString
			if err := info.Scan(&cid, &col, &decl, &notNull, &def, &pk); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, Column{Name: col, Type: s.columnType(name, col, decl)})
			if len(cols) == 1 {
				cols = append(cols, Column{Name: "hostname", Type: "varchar"})
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
					if c.Name == col {
						typ = c.Type
					}
				}
				byName[col] = Coerce(vals[i], typ)
			}
			if renumber {
				delete(byName, "id")
			}
			s.Add(name, &host, byName)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
}

// sqliteTables lists the tables of a SQLite database.
func sqliteTables(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// columnType is the type a column of a fixture has on the server.
func (s *Server) columnType(table, col, decl string) string {
	if s.Types != nil {
		if typ := s.Types(table, col); typ != "" {
			return typ
		}
	}
	return DeclaredType(col, decl)
}

// DeclaredType is the type the SQLite declaration of a column stands for.
func DeclaredType(col, decl string) string {
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

// ParseStamp reads the time text rasdaemon writes to a SQLite file.
func ParseStamp(text string) (time.Time, bool) {
	text = strings.TrimSpace(text)
	for _, layout := range []string{"2006-01-02 15:04:05 -0700", "2006-01-02 15:04:05"} {
		if at, err := time.Parse(layout, text); err == nil {
			return at.UTC(), true
		}
	}
	return time.Time{}, false
}

// Coerce gives a SQLite value the type of a column of a server. SQLite
// takes any value in any column; a server does not.
func Coerce(v any, typ string) any {
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
			if at, ok := ParseStamp(text); ok {
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

type connector struct{ s *Server }

func (c connector) Connect(context.Context) (driver.Conn, error) { return &conn{s: c.s}, nil }
func (c connector) Driver() driver.Driver                        { return drv{} }

type drv struct{}

func (drv) Open(string) (driver.Conn, error) {
	return nil, errors.New("storetest: use the connector")
}

type conn struct{ s *Server }

func (c *conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("storetest: prepared statements are not supported")
}
func (c *conn) Close() error { return nil }
func (c *conn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *conn) Ping(context.Context) error {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	return c.s.PingErr
}

func (c *conn) BeginTx(_ context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if c.s.BeginErr != nil {
		return nil, c.s.BeginErr
	}
	c.s.txs = append(c.s.txs, opts)
	return txn{c.s}, nil
}

type txn struct{ s *Server }

func (t txn) Commit() error   { return t.Rollback() }
func (t txn) Rollback() error { t.s.mu.Lock(); t.s.ended++; t.s.mu.Unlock(); return nil }

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

func (c *conn) QueryContext(_ context.Context, query string, named []driver.NamedValue) (driver.Rows, error) {
	s := c.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, query)
	for part, err := range s.Fail {
		if strings.Contains(query, part) {
			return nil, err
		}
	}
	args := make([]any, len(named))
	for i, a := range named {
		args[i] = a.Value
	}
	pg := s.Dialect == PostgreSQL

	// Which tables there are, and which of them have a hostname column.
	listing, withHost := mysqlTables.MatchString(query), mysqlColumns.MatchString(query)
	if pg {
		listing, withHost = pgTables.MatchString(query), pgColumns.MatchString(query)
	}
	if listing || withHost {
		out := &result{s: s, cols: []Column{{Name: "table_name", Type: "varchar"}}}
		if pg && (len(args) != 1 || args[0] != s.Schema) {
			// Another schema: it holds nothing.
			return out, nil
		}
		var names []string
		for name, t := range s.tables {
			has := false
			for _, col := range t.cols {
				has = has || col.Name == "hostname"
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
			if m[1] != s.Schema {
				return nil, fmt.Errorf(`pq: schema "%s" does not exist`, m[1])
			}
			name, bounded = m[2], m[3] != ""
		}
		t := s.tables[name]
		if t == nil {
			return nil, fmt.Errorf("storetest: table %s does not exist", name)
		}
		col := func(n string) int {
			for i, c := range t.cols {
				if c.Name == n {
					return i
				}
			}
			return -1
		}
		idCol, hostCol, timeCol := col("id"), col("hostname"), col("timestamp")
		if idCol < 0 || hostCol < 0 {
			return nil, fmt.Errorf("storetest: unknown column in table %s", name)
		}

		if kind == "hosts" {
			var mark int64
			if bounded {
				var ok bool
				if mark, ok = args[0].(int64); !ok || len(args) != 1 {
					return nil, fmt.Errorf("storetest: arguments %v for %s", args, query)
				}
			} else if len(args) != 0 {
				return nil, fmt.Errorf("storetest: arguments %v for %s", args, query)
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
			out := &result{s: s, cols: []Column{{Name: "hostname", Type: "varchar"}, {Name: "count", Type: "int64"}, {Name: "max", Type: "int64"}}}
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
			return nil, fmt.Errorf("storetest: arguments %v for %s", args, query)
		}
		var since time.Time
		if bounded {
			if timeCol < 0 {
				return nil, fmt.Errorf("storetest: table %s has no timestamp column", name)
			}
			switch v := args[1].(type) {
			case time.Time:
				if !pg {
					return nil, errors.New("storetest: a time value compared with a column without a zone")
				}
				since = v
			case string:
				if pg {
					return nil, errors.New("storetest: text compared with a timestamptz column")
				}
				var err error
				if since, err = time.Parse("2006-01-02 15:04:05", v); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("storetest: argument %v for the time", v)
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
			return &result{s: s, cols: []Column{{Name: "count", Type: "int64"}}, rows: [][]any{{int64(len(picked))}}}, nil
		}
		sort.SliceStable(picked, func(i, j int) bool { return picked[i][idCol].(int64) < picked[j][idCol].(int64) })
		return &result{s: s, cols: t.cols, rows: picked}, nil
	}
	return nil, fmt.Errorf("storetest (%s): syntax error in %q", s.Dialect, query)
}

type result struct {
	s    *Server
	cols []Column
	rows [][]any
	next int
}

func (r *result) Columns() []string {
	out := make([]string, len(r.cols))
	for i, c := range r.cols {
		out[i] = c.Name
	}
	return out
}

func (r *result) ColumnTypeDatabaseTypeName(i int) string { return r.s.typeName(r.cols[i].Type) }
func (r *result) Close() error                            { return nil }

func (r *result) Next(dest []driver.Value) error {
	if r.next >= len(r.rows) {
		return io.EOF
	}
	for i, v := range r.rows[r.next] {
		dest[i] = r.s.hand(v)
	}
	r.next++
	return nil
}
