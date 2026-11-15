package rasdaemon

import (
	"bufio"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"

	"github.com/agenticode/my/internal/adapter"
	"github.com/agenticode/my/internal/model"
)

// rasdaemon 1.0 can keep its records on a database server instead of in a
// SQLite file: a MySQL or MariaDB database, or a schema of a PostgreSQL
// database. Every host of a fleet writes to the same tables there. The
// tables are the ones of the SQLite file with one more column, hostname,
// which holds the name of the host a row came from, and with the time of a
// row in a time type of the server instead of text.
//
// A Store reads such a database: it lists the hosts and hands out an
// adapter per host, which reads the rows of that host with the code that
// reads a SQLite file.

// Backends of a store, by the names rasdaemon gives them.
const (
	BackendMySQL      = "mysql"
	BackendPostgreSQL = "postgresql"
)

// StoreConfig says where a store is and how to sign in. The fields are the
// settings rasdaemon itself takes; StoreConfigFromEnv reads them from the
// variables rasdaemon reads them from.
type StoreConfig struct {
	// Backend is BackendMySQL (also for MariaDB) or BackendPostgreSQL.
	Backend string
	// Host is the server. Empty means the server on this machine, through
	// its Unix socket.
	Host string
	// Port is the TCP port; zero means the default of the backend.
	Port int
	// Socket is the path of the Unix socket of a MySQL server. Empty with
	// an empty Host looks in the usual places.
	Socket   string
	User     string
	Password string
	Database string
	// Schema is the PostgreSQL schema that holds the tables.
	Schema string
	// TLS says whether the connection is encrypted. For MySQL: "" or
	// "require". For PostgreSQL an sslmode: "", "disable", "allow",
	// "prefer", "require", "verify-ca", "verify-full"; empty means prefer,
	// as in libpq.
	TLS string
	// ConnectTimeout bounds the connection attempt; zero means 10 seconds.
	ConnectTimeout time.Duration
}

// Defaults of rasdaemon for the settings of a store.
const (
	defaultStoreName      = "rasdaemon"
	defaultMySQLPort      = 3306
	defaultPostgreSQLPort = 5432
	defaultConnectTimeout = 10 * time.Second
)

// identifier is what rasdaemon accepts as a PostgreSQL schema name.
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)

// BackendName returns the backend a name stands for, or "".
func BackendName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "mysql", "mariadb":
		return BackendMySQL
	case "postgresql", "postgres", "pg":
		return BackendPostgreSQL
	}
	return ""
}

// StoreConfigFromEnv reads the settings of a store from the environment
// variables rasdaemon reads: RAS_MYSQL_HOST, RAS_MYSQL_PORT,
// RAS_MYSQL_USER, RAS_MYSQL_PASSWORD, RAS_MYSQL_DATABASE, RAS_MYSQL_SOCKET,
// RAS_MYSQL_USE_SSL and RAS_MYSQL_CONNECT_TIMEOUT for MySQL, and
// RAS_PG_HOST, RAS_PG_PORT, RAS_PG_USER, RAS_PG_PASSWORD, RAS_PG_DATABASE,
// RAS_PG_SCHEMA, RAS_PG_SSL_MODE, RAS_PG_USE_SSL and RAS_PG_CONNECT_TIMEOUT
// for PostgreSQL, with the defaults of rasdaemon. An empty backend takes
// RASDAEMON_DB_BACKEND.
//
// The environment file of a fleet's rasdaemon therefore points this reader
// at the store that rasdaemon writes to.
func StoreConfigFromEnv(backend string, getenv func(string) string) (StoreConfig, error) {
	str := func(name, def string) string {
		if v := getenv(name); v != "" {
			return v
		}
		return def
	}
	// rasdaemon reads numbers with atoi and takes the default for zero.
	num := func(name string, def int) (int, error) {
		v := strings.TrimSpace(getenv(name))
		if v == "" {
			return def, nil
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%s=%q: want a number", name, v)
		}
		if n == 0 {
			return def, nil
		}
		return n, nil
	}
	yes := func(name string) bool {
		switch getenv(name) {
		case "", "0", "false", "no":
			return false
		}
		return true
	}

	name := backend
	if name == "" {
		name = getenv("RASDAEMON_DB_BACKEND")
	}
	cfg := StoreConfig{Backend: BackendName(name)}
	var port, timeout int
	var err error
	switch cfg.Backend {
	case BackendMySQL:
		cfg.Host, cfg.Socket = getenv("RAS_MYSQL_HOST"), getenv("RAS_MYSQL_SOCKET")
		cfg.User, cfg.Password = str("RAS_MYSQL_USER", defaultStoreName), getenv("RAS_MYSQL_PASSWORD")
		cfg.Database = str("RAS_MYSQL_DATABASE", defaultStoreName)
		if yes("RAS_MYSQL_USE_SSL") {
			cfg.TLS = "require"
		}
		if port, err = num("RAS_MYSQL_PORT", defaultMySQLPort); err != nil {
			return cfg, err
		}
		if timeout, err = num("RAS_MYSQL_CONNECT_TIMEOUT", int(defaultConnectTimeout/time.Second)); err != nil {
			return cfg, err
		}
	case BackendPostgreSQL:
		cfg.Host = getenv("RAS_PG_HOST")
		cfg.User, cfg.Password = str("RAS_PG_USER", defaultStoreName), getenv("RAS_PG_PASSWORD")
		cfg.Database, cfg.Schema = str("RAS_PG_DATABASE", defaultStoreName), str("RAS_PG_SCHEMA", defaultStoreName)
		// rasdaemon passes the mode on unless it is empty or "false", and
		// asks for "require" when only RAS_PG_USE_SSL is set.
		if mode := getenv("RAS_PG_SSL_MODE"); mode != "" && !strings.EqualFold(mode, "false") {
			cfg.TLS = mode
		} else if yes("RAS_PG_USE_SSL") {
			cfg.TLS = "require"
		}
		if port, err = num("RAS_PG_PORT", defaultPostgreSQLPort); err != nil {
			return cfg, err
		}
		if timeout, err = num("RAS_PG_CONNECT_TIMEOUT", int(defaultConnectTimeout/time.Second)); err != nil {
			return cfg, err
		}
	default:
		switch {
		case name == "":
			return cfg, errors.New("no backend named: want mysql or postgresql")
		case strings.EqualFold(name, "sqlite3") || strings.EqualFold(name, "sqlite"):
			return cfg, errors.New("the sqlite3 backend keeps a file on each host, which a scan of the host or an import reads; a store is mysql or postgresql")
		}
		return cfg, fmt.Errorf("unknown backend %q: want mysql or postgresql", name)
	}
	cfg.Port, cfg.ConnectTimeout = port, time.Duration(timeout)*time.Second
	return cfg, cfg.check()
}

// check reports settings no server would take.
func (c *StoreConfig) check() error {
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("port %d is out of range", c.Port)
	}
	switch c.Backend {
	case BackendMySQL:
		switch c.TLS {
		case "", "require":
		default:
			return fmt.Errorf("TLS setting %q: want require or nothing", c.TLS)
		}
	case BackendPostgreSQL:
		if c.Schema != "" && !identifier.MatchString(c.Schema) {
			return fmt.Errorf("schema name %q: want letters, digits and underscores", c.Schema)
		}
		switch c.TLS {
		case "", "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		default:
			return fmt.Errorf("sslmode %q: want disable, allow, prefer, require, verify-ca or verify-full", c.TLS)
		}
	default:
		return fmt.Errorf("unknown backend %q: want mysql or postgresql", c.Backend)
	}
	return nil
}

// ParseEnvFile reads settings from a file of NAME=value lines, the form of
// the environment file rasdaemon is started with. Empty lines and lines
// that start with # or ; are skipped, a leading "export " is dropped, and
// one pair of quotes around a value is removed.
func ParseEnvFile(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\xef\xbb\xbf"))
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("line %d: want NAME=value", n)
		}
		value = strings.TrimSpace(value)
		if l := len(value); l >= 2 && (value[0] == '"' || value[0] == '\'') && value[l-1] == value[0] {
			value = value[1 : l-1]
		}
		out[name] = value
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// mysqlSockets are the places a MySQL or MariaDB server puts its socket.
var mysqlSockets = []string{"/var/run/mysqld/mysqld.sock", "/run/mysqld/mysqld.sock", "/var/lib/mysql/mysql.sock", "/tmp/mysql.sock"}

// postgresSockets are the directories a PostgreSQL server puts its socket in.
var postgresSockets = []string{"/var/run/postgresql", "/run/postgresql", "/tmp"}

// connector returns how to connect to the store, and the name the store
// goes by in the source inventory and in references to its rows. The name
// holds no user and no password.
func (c *StoreConfig) connector() (driver.Connector, string, error) {
	if err := c.check(); err != nil {
		return nil, "", err
	}
	timeout := c.ConnectTimeout
	if timeout <= 0 {
		timeout = defaultConnectTimeout
	}
	database := c.Database
	if database == "" {
		database = defaultStoreName
	}
	switch c.Backend {
	case BackendMySQL:
		mc := mysql.NewConfig()
		mc.User, mc.Passwd, mc.DBName = c.User, c.Password, database
		mc.Timeout = timeout
		// The times rasdaemon stores are UTC, in a type without a zone.
		mc.ParseTime, mc.Loc = true, time.UTC
		if c.TLS == "require" {
			// What rasdaemon asks for with RAS_MYSQL_USE_SSL: an encrypted
			// connection, without a check of the server's certificate.
			mc.TLSConfig = "skip-verify"
		}
		var origin string
		if c.Host == "" {
			socket := c.Socket
			if socket == "" {
				for _, p := range mysqlSockets {
					if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
						socket = p
						break
					}
				}
			}
			if socket == "" {
				return nil, "", errors.New("no MySQL server named and no socket of a local server found: set RAS_MYSQL_HOST or RAS_MYSQL_SOCKET")
			}
			mc.Net, mc.Addr = "unix", socket
			origin = "mysql://localhost/" + database
		} else {
			port := c.Port
			if port == 0 {
				port = defaultMySQLPort
			}
			mc.Net, mc.Addr = "tcp", net.JoinHostPort(c.Host, strconv.Itoa(port))
			origin = "mysql://" + mc.Addr + "/" + database
		}
		conn, err := mysql.NewConnector(mc)
		return conn, origin, err

	case BackendPostgreSQL:
		port := c.Port
		if port == 0 {
			port = defaultPostgreSQLPort
		}
		host, shown := c.Host, net.JoinHostPort(c.Host, strconv.Itoa(port))
		if host == "" {
			shown = "localhost"
			host = "localhost"
			for _, dir := range postgresSockets {
				if fi, err := os.Stat(dir + "/.s.PGSQL." + strconv.Itoa(port)); err == nil && fi.Mode()&os.ModeSocket != 0 {
					host = dir
					break
				}
			}
		}
		mode := c.TLS
		if mode == "" {
			// The default of libpq, which rasdaemon connects with.
			mode = "prefer"
		}
		params := [][2]string{{"host", host}, {"port", strconv.Itoa(port)}, {"dbname", database}, {"sslmode", mode},
			{"connect_timeout", strconv.Itoa(int((timeout + time.Second - 1) / time.Second))}, {"application_name", "my"}}
		if c.User != "" {
			params = append(params, [2]string{"user", c.User})
		}
		if c.Password != "" {
			params = append(params, [2]string{"password", c.Password})
		}
		var dsn strings.Builder
		for i, kv := range params {
			if i > 0 {
				dsn.WriteByte(' ')
			}
			dsn.WriteString(kv[0] + "=" + pqQuote(kv[1]))
		}
		pc, err := pq.NewConfig(dsn.String())
		if err != nil {
			// The error of the driver may quote the settings: say only
			// that they were not taken.
			return nil, "", errors.New("the PostgreSQL connection settings are not usable")
		}
		conn, err := pq.NewConnectorConfig(pc)
		schema := c.Schema
		if schema == "" {
			schema = defaultStoreName
		}
		return conn, "postgresql://" + shown + "/" + database + "?schema=" + schema, err
	}
	return nil, "", fmt.Errorf("unknown backend %q", c.Backend)
}

// pqQuote writes a value of a PostgreSQL connection string.
func pqQuote(v string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}

// backend is how one kind of server holds the tables.
type backend struct {
	name string
	// schema qualifies the table names; empty means the database the
	// connection is in.
	schema string
	// quote is the character around an identifier.
	quote string
	// numbered is set when placeholders are $1, $2 and so on.
	numbered bool
}

func (b *backend) ident(name string) string {
	return b.quote + strings.ReplaceAll(name, b.quote, b.quote+b.quote) + b.quote
}

func (b *backend) table(name string) string {
	if b.schema != "" {
		return b.ident(b.schema) + "." + b.ident(name)
	}
	return b.ident(name)
}

// arg returns the placeholder of the n-th argument, counted from 1.
func (b *backend) arg(n int) string {
	if b.numbered {
		return "$" + strconv.Itoa(n)
	}
	return "?"
}

// inSchema returns the condition that picks the store's own tables in the
// information schema, with its arguments.
func (b *backend) inSchema() (string, []any) {
	if b.schema != "" {
		return "table_schema = " + b.arg(1), []any{b.schema}
	}
	return "table_schema = DATABASE()", nil
}

// stamp returns a time in the form the timestamp column compares with.
func (b *backend) stamp(t time.Time) any {
	if b.numbered {
		return t.UTC()
	}
	// MySQL: the column has no zone and rasdaemon fills it in UTC.
	return t.UTC().Format("2006-01-02 15:04:05")
}

func newBackend(name, schema string) (*backend, error) {
	switch BackendName(name) {
	case BackendMySQL:
		return &backend{name: BackendMySQL, quote: "`"}, nil
	case BackendPostgreSQL:
		if schema == "" {
			schema = defaultStoreName
		}
		if !identifier.MatchString(schema) {
			return nil, fmt.Errorf("schema name %q: want letters, digits and underscores", schema)
		}
		return &backend{name: BackendPostgreSQL, schema: schema, quote: `"`, numbered: true}, nil
	}
	return nil, fmt.Errorf("unknown backend %q: want mysql or postgresql", name)
}

// Store is a rasdaemon database on a MySQL, MariaDB or PostgreSQL server:
// the records of every host that writes there.
type Store struct {
	db     *sql.DB
	be     *backend
	origin string
	// tables are the tables of the store that carry a host name.
	tables []string
	// closeDB is set when the Store opened the connection pool itself.
	closeDB bool
}

// OpenStore connects to a store and checks that it holds the tables of
// rasdaemon. The connection only reads.
func OpenStore(ctx context.Context, cfg StoreConfig) (*Store, error) {
	conn, origin, err := cfg.connector()
	if err != nil {
		return nil, fmt.Errorf("rasdaemon store: %w", err)
	}
	db := sql.OpenDB(conn)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	s, err := NewStore(ctx, db, cfg.Backend, cfg.Schema, origin)
	if err != nil {
		db.Close()
		return nil, err
	}
	s.closeDB = true
	return s, nil
}

// NewStore returns the store behind an open database. backend is
// BackendMySQL or BackendPostgreSQL, schema the PostgreSQL schema of the
// tables, and origin the name the store goes by in the source inventory.
// The caller closes the database.
func NewStore(ctx context.Context, db *sql.DB, backendName, schema, origin string) (*Store, error) {
	be, err := newBackend(backendName, schema)
	if err != nil {
		return nil, fmt.Errorf("rasdaemon store: %w", err)
	}
	s := &Store{db: db, be: be, origin: origin}
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("rasdaemon store %s: %w", origin, err)
	}
	all, err := s.listTables(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("rasdaemon store %s: %w", origin, err)
	}
	withHost, err := s.hostTables(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("rasdaemon store %s: %w", origin, err)
	}
	var known, without []string
	for _, t := range all {
		if _, ok := tableUse[t]; !ok {
			continue
		}
		known = append(known, t)
		if !withHost[t] {
			without = append(without, t)
		}
	}
	switch {
	case len(known) == 0 && s.be.schema != "":
		return nil, fmt.Errorf("rasdaemon store %s: no rasdaemon tables in schema %s", origin, s.be.schema)
	case len(known) == 0:
		return nil, fmt.Errorf("rasdaemon store %s: no rasdaemon tables in the database", origin)
	case len(without) > 0:
		return nil, fmt.Errorf("rasdaemon store %s: table %s has no hostname column; rasdaemon 1.0 adds it to every table it keeps on a server", origin, without[0])
	}
	s.tables = known
	return s, nil
}

// Close closes the connections the store opened.
func (s *Store) Close() error {
	if s.closeDB {
		return s.db.Close()
	}
	return nil
}

// Origin names the store: its kind, server and database, without the user.
func (s *Store) Origin() string { return s.origin }

func (s *Store) listTables(ctx context.Context, q querier) ([]string, error) {
	cond, args := s.be.inSchema()
	rows, err := q.QueryContext(ctx, `SELECT table_name FROM information_schema.tables WHERE `+cond+` ORDER BY table_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// hostTables returns the tables that have a hostname column.
func (s *Store) hostTables(ctx context.Context, q querier) (map[string]bool, error) {
	cond, args := s.be.inSchema()
	rows, err := q.QueryContext(ctx, `SELECT table_name FROM information_schema.columns WHERE `+cond+` AND column_name = 'hostname'`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// StoreHost says what a store holds about one host.
type StoreHost struct {
	// Name is the host name the rows carry. It is empty for rows without
	// one: a rasdaemon that could not learn the name of its host. Such
	// rows cannot be told apart by host and are not read.
	Name string
	// Rows is the number of rows of the host in the tables that are read,
	// counted from the mark the listing was asked for.
	Rows int64
	// Last is the highest row number of the host per table.
	Last map[string]int64
}

// readTables are the tables whose rows become events or actions.
func (s *Store) readTables() []string {
	var out []string
	for _, t := range s.tables {
		if use := tableUse[t]; use == "events" || use == "actions" {
			out = append(out, t)
		}
	}
	return out
}

// Hosts lists the hosts that have rows in the store, by name. With after
// set, only rows with a number above after[table] count: a caller that
// keeps the highest numbers it has seen learns which hosts have new rows
// without the server reading the old ones again.
func (s *Store) Hosts(ctx context.Context, after map[string]int64) ([]StoreHost, error) {
	by := map[string]*StoreHost{}
	for _, t := range s.readTables() {
		q := `SELECT ` + s.be.ident("hostname") + `, COUNT(*), MAX(` + s.be.ident("id") + `) FROM ` + s.be.table(t)
		var args []any
		if mark := after[t]; mark > 0 {
			q += ` WHERE ` + s.be.ident("id") + ` > ` + s.be.arg(1)
			args = append(args, mark)
		}
		rows, err := s.db.QueryContext(ctx, q+` GROUP BY `+s.be.ident("hostname"), args...)
		if err != nil {
			return nil, fmt.Errorf("rasdaemon store %s: table %s: %w", s.origin, t, err)
		}
		for rows.Next() {
			var name sql.NullString
			var n, last sql.NullInt64
			if err := rows.Scan(&name, &n, &last); err != nil {
				rows.Close()
				return nil, fmt.Errorf("rasdaemon store %s: table %s: %w", s.origin, t, err)
			}
			h := by[name.String]
			if h == nil {
				h = &StoreHost{Name: name.String, Last: map[string]int64{}}
				by[name.String] = h
			}
			h.Rows += n.Int64
			h.Last[t] = max(h.Last[t], last.Int64)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("rasdaemon store %s: table %s: %w", s.origin, t, err)
		}
	}
	out := make([]StoreHost, 0, len(by))
	for _, h := range by {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ReadOptions narrow what an adapter reads of a host.
type ReadOptions struct {
	// Since leaves out rows older than this time. The zero value reads
	// every row.
	Since time.Time
}

// Adapter returns the adapter that reads the rows of one host. Its records
// are those the SQLite adapter gives for the database file of that host;
// what a store does not keep is the UTC offset of the host's clock.
func (s *Store) Adapter(host string, opts ReadOptions) adapter.Adapter {
	return &storeAdapter{s: s, host: host, opts: opts}
}

type storeAdapter struct {
	s    *Store
	host string
	opts ReadOptions
}

func (a *storeAdapter) Kind() string     { return Kind }
func (a *storeAdapter) Origin() string   { return a.s.origin }
func (a *storeAdapter) HostHint() string { return a.host }

// Collect reads the rows of the host in one read-only transaction, so that
// the tables are seen as they were at one moment: a machine check record
// and the EDAC record of the same error are two rows of two tables.
func (a *storeAdapter) Collect(ctx context.Context, sink adapter.Sink) adapter.Report {
	rep := adapter.Report{Kind: Kind, Origin: a.s.origin, HostHint: a.host}
	fail := func(err error) adapter.Report {
		rep.Status, rep.Detail = model.SourceError, err.Error()
		return rep
	}
	if a.host == "" {
		return fail(errors.New("rows without a host name cannot be read as one host"))
	}
	tx, err := a.s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	r := &reader{ctx: ctx, db: tx, sink: sink, path: a.s.origin, rep: &rep,
		remote: &remote{be: a.s.be, host: a.host, since: a.opts.Since}}
	// Tables without a host name are not rasdaemon's and are left alone.
	return r.run(a.s.tables)
}

// remote says which rows of a store a reader reads.
type remote struct {
	be    *backend
	host  string
	since time.Time
}

// where returns the condition that picks the rows, with its arguments.
func (m *remote) where() (string, []any) {
	cond, args := m.be.ident("hostname")+" = "+m.be.arg(1), []any{m.host}
	if !m.since.IsZero() {
		cond += " AND " + m.be.ident("timestamp") + " >= " + m.be.arg(2)
		args = append(args, m.be.stamp(m.since))
	}
	return cond, args
}

func (m *remote) rows(ctx context.Context, q querier, table string) (*sql.Rows, error) {
	cond, args := m.where()
	return q.QueryContext(ctx, `SELECT * FROM `+m.be.table(table)+` WHERE `+cond+` ORDER BY `+m.be.ident("id"), args...)
}

func (m *remote) count(ctx context.Context, q querier, table string) (int, error) {
	cond, args := m.where()
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+m.be.table(table)+` WHERE `+cond, args...).Scan(&n)
	return n, err
}

// columnKind is what a column of a store holds, as far as the reader cares.
type columnKind uint8

const (
	colOther columnKind = iota
	colInt
	colText
	colBlob
	colTime
)

// columnKinds returns the kind of every column of a result, from the type
// names the drivers report: BIGINT, INT, TEXT, VARCHAR, BLOB and DATETIME
// for MySQL; INT8, INT4, TEXT, BYTEA and TIMESTAMPTZ for PostgreSQL.
func columnKinds(rows *sql.Rows) ([]columnKind, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	out := make([]columnKind, len(types))
	for i, t := range types {
		name := strings.ToUpper(t.DatabaseTypeName())
		switch {
		case strings.Contains(name, "INT") || name == "SERIAL" || name == "BIGSERIAL":
			out[i] = colInt
		case strings.Contains(name, "TIME") || name == "DATE":
			out[i] = colTime
		case strings.Contains(name, "BLOB") || strings.Contains(name, "BINARY") || name == "BYTEA":
			out[i] = colBlob
		case strings.Contains(name, "TEXT") || strings.Contains(name, "CHAR") || name == "NAME":
			out[i] = colText
		}
	}
	return out, nil
}

// stampLayout is the form rasdaemon writes a time in, without the zone.
const stampLayout = "2006-01-02 15:04:05"

// settle turns the values of a row into the forms the SQLite file has
// them in, which is what the table readers expect: whole numbers as int64,
// text as string, and the time of a row as text in UTC. The drivers differ
// in what they hand over: text may come as bytes, a number as text, a time
// as text or as a time value.
//
// The time carries no zone: MySQL stores the UTC time rasdaemon worked
// out, PostgreSQL an instant. The offset of the host's clock is in neither.
func settle(vals []any, kinds []columnKind) {
	for i, v := range vals {
		if v == nil || i >= len(kinds) {
			continue
		}
		switch kinds[i] {
		case colInt:
			switch x := v.(type) {
			case []byte:
				if n, err := strconv.ParseInt(string(x), 10, 64); err == nil {
					vals[i] = n
				} else {
					vals[i] = string(x)
				}
			case string:
				if n, err := strconv.ParseInt(x, 10, 64); err == nil {
					vals[i] = n
				}
			case uint64:
				vals[i] = int64(x)
			}
		case colText:
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		case colBlob:
			if s, ok := v.(string); ok {
				vals[i] = []byte(s)
			}
		case colTime:
			switch x := v.(type) {
			case time.Time:
				vals[i] = x.UTC().Format(stampLayout)
			case []byte:
				vals[i] = trimFraction(string(x))
			case string:
				vals[i] = trimFraction(x)
			}
		default:
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
	}
}

// trimFraction cuts the fraction of a second off a time in text form.
func trimFraction(s string) string {
	if i := strings.IndexByte(s, '.'); i > 0 {
		return s[:i]
	}
	return s
}
