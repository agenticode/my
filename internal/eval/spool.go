package eval

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/agenticode/my/internal/adapter/dcbrain"
)

// The error table has tens of millions of rows in no order the harness
// can rely on, and the engine wants the rows of one server together. The
// spool is the table split into bucket files by server, each row in a
// fixed 20 bytes. One bucket fits in memory; the table does not have to.

const (
	spoolVersion = 3
	spoolBuckets = 64
	spoolRecSize = 20
	spoolMeta    = "spool.json"
)

// spoolInfo describes a spool and what reading the error table showed. It
// is stored next to the buckets so that a spool can be used again.
type spoolInfo struct {
	Version int `json:"version"`
	// Source identifies the error table the spool was made from.
	Source     string `json:"source"`
	SourceSize int64  `json:"source_size"`
	SourceTime int64  `json:"source_time"`

	Table dcbrain.TableReport `json:"table"`
	// Rows is the number of rows in the buckets.
	Rows int64 `json:"rows"`
	// ByType counts rows by error_type 1, 2 and 3 (index 0 is unused).
	ByType [4]int64 `json:"by_type"`
	// First and Last are the earliest and the latest time, in seconds on
	// the dataset calendar.
	First int64 `json:"first"`
	Last  int64 `json:"last"`
	// MonthLastDay is, per month of the dataset calendar, the largest day
	// of the month that occurs.
	MonthLastDay [dcbrain.Months]int `json:"month_last_day"`
	// Largest values of the location columns.
	MaxMemoryID int `json:"max_memoryid"`
	MaxRank     int `json:"max_rank"`
	MaxBank     int `json:"max_bank"`
	MaxRow      int `json:"max_row"`
	MaxCol      int `json:"max_col"`
	// Servers lists the server IDs in the order they first occur; a row
	// refers to its server by position. ServerRows counts rows per server,
	// and ServerDIMMs has a bit set for every memoryid a server has rows of.
	Servers     []string `json:"servers"`
	ServerRows  []int64  `json:"server_rows"`
	ServerDIMMs []uint32 `json:"server_dimms"`
}

// spool is an open spool directory.
type spool struct {
	dir  string
	info spoolInfo
	// temp is set when the directory was made for this run and is removed
	// when the run ends.
	temp bool
}

func (s *spool) bucketPath(i int) string {
	return filepath.Join(s.dir, fmt.Sprintf("bucket-%02d.bin", i))
}

// close removes a spool that was made for one run.
func (s *spool) close() error {
	if s.temp {
		return os.RemoveAll(s.dir)
	}
	return nil
}

// sourceIdentity returns what identifies the error table of ds on disk.
func sourceIdentity(ds *dcbrain.Dataset) (name string, size, mtime int64, err error) {
	st, err := os.Stat(ds.Path)
	if err != nil {
		return "", 0, 0, err
	}
	name = ds.Name(dcbrain.TableErrors)
	if st.IsDir() {
		p := filepath.Join(ds.Path, filepath.FromSlash(name))
		if st, err = os.Stat(p); err != nil {
			return "", 0, 0, err
		}
	}
	abs, err := filepath.Abs(ds.Path)
	if err != nil {
		abs = ds.Path
	}
	return abs + "::" + name, st.Size(), st.ModTime().UnixNano(), nil
}

// openSpool returns a spool of the error table of ds. With keep set to a
// directory, a spool found there is used when it was made from the same
// file, and a new one is left there for the next run. Otherwise the spool
// is made under scratch (the system's temporary directory when empty) and
// removed by close.
func openSpool(ctx context.Context, ds *dcbrain.Dataset, keep, scratch string, progress func(string, ...any)) (*spool, error) {
	name, size, mtime, err := sourceIdentity(ds)
	if err != nil {
		return nil, fmt.Errorf("eval: %w", err)
	}
	if keep != "" {
		if b, err := os.ReadFile(filepath.Join(keep, spoolMeta)); err == nil {
			var info spoolInfo
			if json.Unmarshal(b, &info) == nil && info.Version == spoolVersion && info.Source == name &&
				info.SourceSize == size && info.SourceTime == mtime && len(info.Servers) == len(info.ServerRows) &&
				len(info.Servers) == len(info.ServerDIMMs) {
				progress("using the spool in %s (%d rows of %d servers)", keep, info.Rows, len(info.Servers))
				return &spool{dir: keep, info: info}, nil
			}
			progress("the spool in %s was made from another file or version; writing it again", keep)
		}
	}
	s := &spool{}
	if keep != "" {
		if err := os.MkdirAll(keep, 0o755); err != nil {
			return nil, fmt.Errorf("eval: spool: %w", err)
		}
		s.dir = keep
		// A spool that is cut off must not be taken for a whole one.
		os.Remove(filepath.Join(keep, spoolMeta))
	} else {
		dir, err := os.MkdirTemp(scratch, "my-eval-spool-")
		if err != nil {
			return nil, fmt.Errorf("eval: spool: %w", err)
		}
		s.dir, s.temp = dir, true
	}
	progress("splitting %s by server", ds.Name(dcbrain.TableErrors))
	if err := s.write(ctx, ds, progress); err != nil {
		if s.temp {
			os.RemoveAll(s.dir)
		}
		return nil, err
	}
	s.info.Version, s.info.Source, s.info.SourceSize, s.info.SourceTime = spoolVersion, name, size, mtime
	b, err := json.Marshal(&s.info)
	if err == nil {
		err = os.WriteFile(filepath.Join(s.dir, spoolMeta), b, 0o644)
	}
	if err != nil {
		s.close()
		return nil, fmt.Errorf("eval: spool: %w", err)
	}
	return s, nil
}

// write reads the error table once and distributes its rows over the
// bucket files.
func (s *spool) write(ctx context.Context, ds *dcbrain.Dataset, progress func(string, ...any)) (err error) {
	rc, err := ds.OpenTable(dcbrain.TableErrors)
	if err != nil {
		return fmt.Errorf("eval: %w", err)
	}
	defer rc.Close()

	files := make([]*os.File, spoolBuckets)
	writers := make([]*bufio.Writer, spoolBuckets)
	defer func() {
		for i, f := range files {
			if f == nil {
				continue
			}
			if ferr := writers[i].Flush(); ferr != nil && err == nil {
				err = fmt.Errorf("eval: spool: %w", ferr)
			}
			if cerr := f.Close(); cerr != nil && err == nil {
				err = fmt.Errorf("eval: spool: %w", cerr)
			}
		}
	}()
	for i := range files {
		f, err := os.Create(s.bucketPath(i))
		if err != nil {
			return fmt.Errorf("eval: spool: %w", err)
		}
		files[i], writers[i] = f, bufio.NewWriterSize(f, 256<<10)
	}

	info := &s.info
	info.First = -1
	index := map[string]uint32{}
	var buf [spoolRecSize]byte
	name := ds.Name(dcbrain.TableErrors)
	rep, err := dcbrain.ReadErrors(ctx, rc, name, func(r *dcbrain.Row) error {
		i, ok := index[r.SID]
		if !ok {
			i = uint32(len(info.Servers))
			index[r.SID] = i
			info.Servers = append(info.Servers, r.SID)
			info.ServerRows = append(info.ServerRows, 0)
			info.ServerDIMMs = append(info.ServerDIMMs, 0)
		}
		info.ServerRows[i]++
		info.ServerDIMMs[i] |= 1 << uint(r.MemoryID)
		info.Rows++
		info.ByType[r.Type]++
		at := int64(r.At)
		if info.First < 0 || at < info.First {
			info.First = at
		}
		info.Last = max(info.Last, at)
		if month, day := r.At.Date(); month > 0 {
			info.MonthLastDay[month-1] = max(info.MonthLastDay[month-1], day)
		}
		info.MaxMemoryID = max(info.MaxMemoryID, r.MemoryID)
		info.MaxRank = max(info.MaxRank, r.Rank)
		info.MaxBank = max(info.MaxBank, r.Bank)
		info.MaxRow = max(info.MaxRow, r.Row)
		info.MaxCol = max(info.MaxCol, r.Col)

		rec := r.Rec()
		encodeRec(buf[:], i, &rec)
		if _, err := writers[i%spoolBuckets].Write(buf[:]); err != nil {
			return fmt.Errorf("eval: spool: %w", err)
		}
		if info.Rows%10_000_000 == 0 {
			progress("read %d million rows of %s", info.Rows/1_000_000, name)
		}
		return nil
	})
	info.Table = rep
	if err != nil {
		return fmt.Errorf("eval: %w", err)
	}
	if info.First < 0 {
		info.First = 0
	}
	return nil
}

// encodeRec writes one row into 20 bytes: server index, time, row with the
// error type in its top byte, file line, column, memoryid, rank and bank.
func encodeRec(b []byte, server uint32, r *dcbrain.Rec) {
	binary.LittleEndian.PutUint32(b[0:], server)
	binary.LittleEndian.PutUint32(b[4:], r.At)
	binary.LittleEndian.PutUint32(b[8:], r.Row&0xffffff|uint32(r.Type)<<24)
	binary.LittleEndian.PutUint32(b[12:], r.Line)
	binary.LittleEndian.PutUint16(b[16:], r.Col)
	b[18] = r.MemoryID
	b[19] = r.Rank<<5 | r.Bank&0x1f
}

func decodeRec(b []byte) (server uint32, r dcbrain.Rec) {
	server = binary.LittleEndian.Uint32(b[0:])
	rt := binary.LittleEndian.Uint32(b[8:])
	r = dcbrain.Rec{
		At:       binary.LittleEndian.Uint32(b[4:]),
		Row:      rt & 0xffffff,
		Count:    1,
		Line:     binary.LittleEndian.Uint32(b[12:]),
		Col:      binary.LittleEndian.Uint16(b[16:]),
		MemoryID: b[18],
		Rank:     b[19] >> 5,
		Bank:     b[19] & 0x1f,
		Type:     dcbrain.ErrorType(rt >> 24),
	}
	return server, r
}

// mergeBuckets are the widths, in seconds, of the time buckets rows of one
// place and error type are merged in. A server is merged by the second,
// which loses nothing. A server that then still has more records than the
// limit is merged by the minute, the hour or the day, whichever is the
// first that brings it under the limit.
var mergeBuckets = []uint32{1, 60, 3600, 86400}

// serverRecs holds the records of one server after merging, in time order.
type serverRecs struct {
	index int
	// rows is the number of rows of the error table the records stand for.
	rows int64
	recs []dcbrain.Rec
	// bucket is the width in seconds of the time buckets the rows were
	// merged in.
	bucket uint32
	// dropped is set when the server has more records than the limit even
	// when merged by the day. recs is then empty.
	dropped bool
}

type aggKey struct {
	slot               uint32 // time divided by the bucket width
	row                uint32
	col                uint16
	memory, rank, bank uint8
	typ                dcbrain.ErrorType
}

// aggregator merges the rows of a server with many rows as they are read,
// so that the rows never have to be held one by one.
type aggregator struct {
	level int
	limit int
	m     map[aggKey]dcbrain.Rec
	gone  bool
}

func (a *aggregator) key(r *dcbrain.Rec) aggKey {
	return aggKey{slot: r.At / mergeBuckets[a.level], row: r.Row, col: r.Col, memory: r.MemoryID, rank: r.Rank, bank: r.Bank, typ: r.Type}
}

func (a *aggregator) put(r dcbrain.Rec) {
	k := a.key(&r)
	if old, ok := a.m[k]; ok {
		if r.At < old.At || (r.At == old.At && r.Line < old.Line) {
			old.At, old.Line = r.At, r.Line
		}
		if sum := uint64(old.Count) + uint64(r.Count); sum <= 1<<32-1 {
			old.Count = uint32(sum)
			a.m[k] = old
			return
		}
		// A count that would overflow stays a record of its own under a
		// key no row has: memoryid 255.
		k.memory = 255
		for {
			if _, taken := a.m[k]; !taken {
				break
			}
			k.slot++
		}
	}
	a.m[k] = r
}

// add takes one row. When the records exceed the limit, the buckets are
// widened; at the widest bucket the server is given up.
func (a *aggregator) add(r dcbrain.Rec) {
	if a.gone {
		return
	}
	a.put(r)
	for len(a.m) > a.limit {
		if a.level+1 >= len(mergeBuckets) {
			a.gone, a.m = true, nil
			return
		}
		a.level++
		old := a.m
		a.m = make(map[aggKey]dcbrain.Rec, len(old)/2)
		for _, rec := range old {
			a.put(rec)
		}
	}
}

func (a *aggregator) recs() []dcbrain.Rec {
	out := make([]dcbrain.Rec, 0, len(a.m))
	for _, r := range a.m {
		out = append(out, r)
	}
	return dcbrain.Merge(out, mergeBuckets[a.level])
}

// readBucket returns the records of the servers of one bucket, merged and
// in the order of the server index. limit is the largest number of
// records one server may have.
func (s *spool) readBucket(i, limit int) ([]serverRecs, error) {
	f, err := os.Open(s.bucketPath(i))
	if err != nil {
		return nil, fmt.Errorf("eval: spool: %w", err)
	}
	defer f.Close()
	type loading struct {
		serverRecs
		agg *aggregator
	}
	by := map[uint32]*loading{}
	r := bufio.NewReaderSize(f, 1<<20)
	var buf [spoolRecSize]byte
	for {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("eval: spool: %s: %w", s.bucketPath(i), err)
		}
		server, rec := decodeRec(buf[:])
		if int(server) >= len(s.info.Servers) {
			return nil, fmt.Errorf("eval: spool: %s names server %d of %d", s.bucketPath(i), server, len(s.info.Servers))
		}
		l := by[server]
		if l == nil {
			l = &loading{serverRecs: serverRecs{index: int(server)}}
			if n := s.info.ServerRows[server]; n > int64(limit) {
				l.agg = &aggregator{limit: limit, m: map[aggKey]dcbrain.Rec{}}
			} else {
				// The row count is known: one allocation per server.
				l.recs = make([]dcbrain.Rec, 0, n)
			}
			by[server] = l
		}
		l.rows++
		if l.agg != nil {
			l.agg.add(rec)
		} else {
			l.recs = append(l.recs, rec)
		}
	}
	out := make([]serverRecs, 0, len(by))
	for _, l := range by {
		switch {
		case l.agg == nil:
			l.recs, l.bucket = dcbrain.Merge(l.recs, 1), 1
		case l.agg.gone:
			l.dropped, l.bucket = true, mergeBuckets[len(mergeBuckets)-1]
		default:
			l.recs, l.bucket = l.agg.recs(), mergeBuckets[l.agg.level]
		}
		out = append(out, l.serverRecs)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].index < out[b].index })
	return out, nil
}
