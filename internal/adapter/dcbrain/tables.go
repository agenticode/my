package dcbrain

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/agenticode/my/internal/adapter"
)

// ErrorType is the error_type column of the error table: how the memory
// controller came across the error.
type ErrorType uint8

const (
	TypeRead  ErrorType = 1 // found by a read
	TypeScrub ErrorType = 2 // found by the scrubber
	TypeWrite ErrorType = 3 // found by a write
)

// String returns the wording mcelog uses for the transaction.
func (t ErrorType) String() string {
	switch t {
	case TypeRead:
		return "memory read error"
	case TypeScrub:
		return "memory scrubbing error"
	case TypeWrite:
		return "memory write error"
	}
	return fmt.Sprintf("error_type %d", uint8(t))
}

// FailureType is the failure_type column of the ticket table.
type FailureType int

const (
	// FailureUnspecified is the value 0. It occurs in the table and the
	// dataset description does not define it.
	FailureUnspecified FailureType = 0
	// FailureUE: the server failed because of an uncorrectable error.
	FailureUE FailureType = 1
	// FailureCE: the server failed because of too many correctable errors.
	FailureCE FailureType = 2
	// FailureMisc: another failure attributed to memory.
	FailureMisc FailureType = 3
)

// FailureTypes lists the failure types in the order of their values.
var FailureTypes = []FailureType{FailureUnspecified, FailureUE, FailureCE, FailureMisc}

// String names the failure type.
func (f FailureType) String() string {
	switch f {
	case FailureUnspecified:
		return "unspecified"
	case FailureUE:
		return "uncorrectable error"
	case FailureCE:
		return "correctable errors"
	case FailureMisc:
		return "miscellaneous"
	}
	return fmt.Sprintf("failure_type %d", int(f))
}

// Limits of the columns. The dataset description gives the first four;
// rows and columns get room above the largest values it names (2^17 rows,
// 2^10 columns).
const (
	MaxMemoryID = 23
	MaxRank     = 7
	MaxBank     = 31
	MaxRow      = 1<<24 - 1
	MaxCol      = 1<<16 - 1
)

// Row is one line of the error table: one corrected error.
type Row struct {
	// Line is the line of the file the row was read from, the header
	// being line 1.
	Line int64
	// SID is the server ID.
	SID string
	// MemoryID is the DIMM of the server, 0 to 23.
	MemoryID int
	Rank     int
	Bank     int
	Row      int
	Col      int
	Type     ErrorType
	At       Stamp
}

// Server is one line of the inventory table.
type Server struct {
	Line int64
	SID  string
	// Manufacturer is the server manufacturer as the dataset codes it, M1 to M4.
	Manufacturer string
	// DRAMModel is the DRAM model as the dataset codes it: A1, A2, B1, B2,
	// B3, C1 or C2. The letter stands for the DRAM vendor.
	DRAMModel string
	// DIMMs is the number of DIMMs of the server.
	DIMMs int
}

// Ticket is one line of the ticket table: a server failure attributed to
// memory.
type Ticket struct {
	Line int64
	SID  string
	Type FailureType
	At   Stamp
}

// TableReport says how reading one table went.
type TableReport struct {
	// Name is the file the table was read from.
	Name string
	// Rows is the number of data lines, Skipped the number among them that
	// could not be used.
	Rows    int64
	Skipped int64
	// Notes list the reasons lines were skipped, with counts and one
	// example each.
	Notes []string
}

// Column names. The dataset description and the files spell some of them
// differently; both spellings are accepted.
var (
	errorColumns = [][]string{{"sid"}, {"memoryid"}, {"rankid"}, {"bankid"}, {"row"}, {"col"}, {"error_type"}, {"error_time"}}
	// The order here is the order the fields are used in, not the order in
	// the file: columns are found by name.
	inventoryColumns = [][]string{{"sid"}, {"server_manufacturer", "server_manufacturter"}, {"dram_model"}, {"dimm_number", "dimm_num"}}
	ticketColumns    = [][]string{{"sid"}, {"failure_type"}, {"failed_time", "failure_time"}}
)

// table reads a comma-separated file line by line.
type table struct {
	name   string
	r      *bufio.Reader
	line   int64
	cols   []int // position in the file of each wanted column
	width  int   // number of columns of the header
	fields [][]byte
	tally  adapter.Tally
	rows   int64
}

const maxLine = 1 << 20

func newTable(r io.Reader, name string, want [][]string) (*table, error) {
	t := &table{name: name, r: bufio.NewReaderSize(r, maxLine)}
	head, err := t.next()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: the file is empty", name)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	head[0] = bytes.TrimPrefix(head[0], []byte("\xef\xbb\xbf"))
	found := make([]string, len(head))
	for i, h := range head {
		found[i] = strings.ToLower(string(h))
	}
	t.width = len(head)
	for _, names := range want {
		at := -1
		for i, h := range found {
			for _, n := range names {
				if h == n && at < 0 {
					at = i
				}
			}
		}
		if at < 0 {
			return nil, fmt.Errorf("%s: no column %q in the header %q", name, names[0], strings.Join(found, ","))
		}
		t.cols = append(t.cols, at)
	}
	return t, nil
}

// next returns the fields of the next line that is not empty. The slices
// are valid until the following call.
func (t *table) next() ([][]byte, error) {
	for {
		line, err := t.r.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// A line longer than the buffer is not a row of this dataset.
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = t.r.ReadSlice('\n')
			}
			t.line++
			t.rows++
			t.tally.Skip(t.ref(), fmt.Sprintf("line longer than %d bytes", maxLine))
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			continue
		}
		if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
			return nil, err
		}
		t.line++
		line = bytes.TrimRight(line, "\r\n")
		if len(bytes.TrimSpace(line)) == 0 {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}
			continue
		}
		t.fields = t.fields[:0]
		if bytes.IndexByte(line, '"') >= 0 {
			rec, qerr := csv.NewReader(bytes.NewReader(line)).Read()
			if qerr != nil {
				t.rows++
				t.tally.Skip(t.ref(), "quoting that cannot be read")
				continue
			}
			for _, f := range rec {
				t.fields = append(t.fields, []byte(strings.TrimSpace(f)))
			}
			return t.fields, nil
		}
		for {
			i := bytes.IndexByte(line, ',')
			if i < 0 {
				t.fields = append(t.fields, bytes.TrimSpace(line))
				break
			}
			t.fields = append(t.fields, bytes.TrimSpace(line[:i]))
			line = line[i+1:]
		}
		return t.fields, nil
	}
}

func (t *table) ref() string { return t.name + ":" + strconv.FormatInt(t.line, 10) }

// row returns the wanted fields of the next data line, in the order of the
// wanted columns, or io.EOF. A line with another number of fields than the
// header is skipped.
func (t *table) row(out [][]byte) ([][]byte, error) {
	for {
		f, err := t.next()
		if err != nil {
			return nil, err
		}
		t.rows++
		if len(f) != t.width {
			t.tally.Skip(t.ref(), fmt.Sprintf("%d fields where the header has %d", len(f), t.width))
			continue
		}
		out = out[:0]
		for _, c := range t.cols {
			out = append(out, f[c])
		}
		return out, nil
	}
}

func (t *table) report() TableReport {
	return TableReport{Name: t.name, Rows: t.rows, Skipped: int64(t.tally.Total()), Notes: t.tally.Notes()}
}

// number reads a column that holds a whole number from 0 to hi.
func (t *table) number(b []byte, column string, hi int) (int, bool) {
	if len(b) == 0 || len(b) > 18 {
		t.tally.Skip(t.ref(), column+" is not a whole number")
		return 0, false
	}
	n := 0
	for _, c := range b {
		if c < '0' || c > '9' {
			t.tally.Skip(t.ref(), column+" is not a whole number")
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if n > hi {
		t.tally.Skip(t.ref(), fmt.Sprintf("%s above %d", column, hi))
		return 0, false
	}
	return n, true
}

func (t *table) stamp(b []byte, column string) (Stamp, bool) {
	at, err := parseStamp(b)
	if err != nil {
		t.tally.Skip(t.ref(), column+" is not a time of the dataset calendar")
		return 0, false
	}
	return at, true
}

// ReadErrors reads the error table and calls fn for every row it can use.
// The Row passed to fn is reused: fn must copy what it keeps.
//
// A line that cannot be used is skipped and counted in the report with the
// reason. ReadErrors returns an error when the header lacks a column, when
// the stream fails, when ctx is cancelled or when fn returns an error.
func ReadErrors(ctx context.Context, r io.Reader, name string, fn func(*Row) error) (TableReport, error) {
	t, err := newTable(r, name, errorColumns)
	if err != nil {
		return TableReport{Name: name}, err
	}
	sids := map[string]string{}
	var row Row
	buf := make([][]byte, 0, len(errorColumns))
	for {
		if t.rows&0xffff == 0 {
			if err := ctx.Err(); err != nil {
				return t.report(), err
			}
		}
		f, err := t.row(buf)
		if errors.Is(err, io.EOF) {
			return t.report(), nil
		}
		if err != nil {
			return t.report(), fmt.Errorf("%s: after line %d: %w", name, t.line, err)
		}
		if len(f[0]) == 0 {
			t.tally.Skip(t.ref(), "no server ID")
			continue
		}
		var valid bool
		if row.MemoryID, valid = t.number(f[1], "memoryid", MaxMemoryID); !valid {
			continue
		}
		if row.Rank, valid = t.number(f[2], "rankid", MaxRank); !valid {
			continue
		}
		if row.Bank, valid = t.number(f[3], "bankid", MaxBank); !valid {
			continue
		}
		if row.Row, valid = t.number(f[4], "row", MaxRow); !valid {
			continue
		}
		if row.Col, valid = t.number(f[5], "col", MaxCol); !valid {
			continue
		}
		typ, valid := t.number(f[6], "error_type", 255)
		if !valid {
			continue
		}
		if typ < int(TypeRead) || typ > int(TypeWrite) {
			t.tally.Skip(t.ref(), "error_type other than 1, 2 or 3")
			continue
		}
		row.Type = ErrorType(typ)
		if row.At, valid = t.stamp(f[7], "error_time"); !valid {
			continue
		}
		sid, known := sids[string(f[0])]
		if !known {
			sid = string(f[0])
			sids[sid] = sid
		}
		row.SID, row.Line = sid, t.line
		if err := fn(&row); err != nil {
			return t.report(), err
		}
	}
}

// ReadInventory reads the inventory table.
func ReadInventory(r io.Reader, name string) ([]Server, TableReport, error) {
	t, err := newTable(r, name, inventoryColumns)
	if err != nil {
		return nil, TableReport{Name: name}, err
	}
	var out []Server
	buf := make([][]byte, 0, len(inventoryColumns))
	for {
		f, err := t.row(buf)
		if errors.Is(err, io.EOF) {
			return out, t.report(), nil
		}
		if err != nil {
			return out, t.report(), fmt.Errorf("%s: after line %d: %w", name, t.line, err)
		}
		if len(f[0]) == 0 {
			t.tally.Skip(t.ref(), "no server ID")
			continue
		}
		n, ok := t.number(f[3], "DIMM_number", 1024)
		if !ok {
			continue
		}
		out = append(out, Server{Line: t.line, SID: string(f[0]), Manufacturer: string(f[1]), DRAMModel: string(f[2]), DIMMs: n})
	}
}

// ReadTickets reads the ticket table.
func ReadTickets(r io.Reader, name string) ([]Ticket, TableReport, error) {
	t, err := newTable(r, name, ticketColumns)
	if err != nil {
		return nil, TableReport{Name: name}, err
	}
	var out []Ticket
	buf := make([][]byte, 0, len(ticketColumns))
	for {
		f, err := t.row(buf)
		if errors.Is(err, io.EOF) {
			return out, t.report(), nil
		}
		if err != nil {
			return out, t.report(), fmt.Errorf("%s: after line %d: %w", name, t.line, err)
		}
		if len(f[0]) == 0 {
			t.tally.Skip(t.ref(), "no server ID")
			continue
		}
		typ, ok := t.number(f[1], "failure_type", 3)
		if !ok {
			continue
		}
		at, ok := t.stamp(f[2], "failed_time")
		if !ok {
			continue
		}
		out = append(out, Ticket{Line: t.line, SID: string(f[0]), Type: FailureType(typ), At: at})
	}
}

// WriteErrors writes rows as an error table with the header of the dataset.
func WriteErrors(w io.Writer, rows []Row) error {
	bw := bufio.NewWriter(w)
	bw.WriteString("sid,memoryid,rankid,bankid,row,col,error_type,error_time\n")
	for i := range rows {
		r := &rows[i]
		fmt.Fprintf(bw, "%s,%d,%d,%d,%d,%d,%d,%s\n", r.SID, r.MemoryID, r.Rank, r.Bank, r.Row, r.Col, r.Type, r.At)
	}
	return bw.Flush()
}

// WriteInventory writes servers as an inventory table.
func WriteInventory(w io.Writer, servers []Server) error {
	bw := bufio.NewWriter(w)
	bw.WriteString("sid,server_manufacturer,DRAM_model,DIMM_number\n")
	for _, s := range servers {
		fmt.Fprintf(bw, "%s,%s,%s,%d\n", s.SID, s.Manufacturer, s.DRAMModel, s.DIMMs)
	}
	return bw.Flush()
}

// WriteTickets writes tickets as a ticket table.
func WriteTickets(w io.Writer, tickets []Ticket) error {
	bw := bufio.NewWriter(w)
	bw.WriteString("sid,failure_type,failed_time\n")
	for _, t := range tickets {
		fmt.Fprintf(bw, "%s,%d,%s\n", t.SID, int(t.Type), t.At)
	}
	return bw.Flush()
}
