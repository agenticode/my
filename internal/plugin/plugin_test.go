package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agenticode/my/internal/engine"
	"github.com/agenticode/my/internal/model"
	"github.com/agenticode/my/internal/testledger"
)

// The test binary plays the analyzer: started with fakeEnv set, it reads a
// request and answers the way the value says. That needs no interpreter
// on the test machine and exercises the real path: a program, its
// standard input and output, its exit status.
const fakeEnv = "MY_TEST_ANALYZER"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeEnv); mode != "" {
		os.Exit(fake(mode))
	}
	testledger.Main(m)
}

var ctx = context.Background()

var now = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)

// fake is the analyzer of the tests.
func fake(mode string) int {
	if mode == "no-read" {
		// Answers without reading its input.
		fmt.Printf(`{"Contract":%q,"Analyzer":{"Name":"deaf","Version":"1"},"Dimms":[]}`, Contract)
		return 0
	}
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 9
	}
	var req Request
	if err := json.Unmarshal(in, &req); err != nil {
		fmt.Fprintln(os.Stderr, "request is not JSON:", err)
		return 9
	}
	resp := Response{Contract: Contract, Analyzer: Analyzer{Name: "test analyzer", Version: "0.1", Vendor: "Example Memory"}}
	first := ""
	if len(req.Dimms) > 0 {
		first = req.Dimms[0].DimmId
	}
	out := func(v any) int {
		if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
			return 9
		}
		return 0
	}
	switch mode {
	case "ok":
		// What the contract carries, for every DIMM of the request: Level 2
		// lines made from the records, the neighbours of each row with
		// errors, and a percentile.
		if os.Getenv("MY_ANALYZER_CONTRACT") != Contract || req.Contract != Contract {
			fmt.Fprintln(os.Stderr, "the contract was not named")
			return 9
		}
		for _, d := range req.Dimms {
			rows := map[int]bool{}
			for _, r := range append(d.PreviousLogs, d.NewLog) {
				if r.Row != nil {
					rows[*r.Row] = true
				}
			}
			rd := ResponseDIMM{DimmId: d.DimmId, Baseline: &Baseline{Percentile: 97.5},
				Level2: &Level2{Lines: []Line{{"records seen", fmt.Sprint(len(d.PreviousLogs) + 1)}, {"rows with errors", fmt.Sprint(len(rows))}},
					Notes: []string{"values of a test analyzer"}}}
			resp.Dimms = append(resp.Dimms, rd)
		}
		return out(resp)
	case "stderr":
		// Talks on its error output and answers well.
		fmt.Fprintln(os.Stderr, "warming up")
		return out(resp)
	case "want-more":
		resp.Analyzer.PreviousLogs = 5000
		for _, d := range req.Dimms {
			resp.Dimms = append(resp.Dimms, ResponseDIMM{DimmId: d.DimmId})
		}
		return out(resp)
	case "extra-fields":
		fmt.Printf(`{"Contract":%q,"Analyzer":{"Name":"a","Version":"1","Build":7},"Signature":"x","Dimms":[{"DimmId":%q,"Level_2":{},"Confidence":3}]}`, Contract, first)
		return 0
	case "slow":
		time.Sleep(30 * time.Second)
		return out(resp)
	case "crash":
		fmt.Fprintln(os.Stderr, "\n  license file not found: /opt/vendor/license.key  \nmore")
		return 3
	case "silent-crash":
		return 4
	case "nothing":
		return 0
	case "garbage":
		fmt.Print("Segmentation fault (core dumped)\n")
		return 0
	case "two-documents":
		out(resp)
		return out(resp)
	case "huge":
		chunk := strings.Repeat("x", 1<<20)
		for range 20 {
			fmt.Print(chunk)
		}
		return 0
	case "not-utf8":
		fmt.Print("{\"Contract\":\"\xff\"}")
		return 0
	}
	// Every other mode is a response in JSON with DIMM replaced by the
	// first DIMM of the request.
	fmt.Print(strings.ReplaceAll(mode, "DIMM", first))
	return 0
}

// request builds the request of the demo host.
func request(t *testing.T, previous int) *Request {
	t.Helper()
	req, err := BuildRequest(ctx, testledger.Open(t, "demo", 1, ""), RequestOptions{Tool: "MemorY", Version: "test", Now: now, PreviousLogs: previous})
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// run sends a request to the fake analyzer in a mode.
func run(t *testing.T, mode string, req *Request, timeout time.Duration) (*Result, error) {
	t.Helper()
	t.Setenv(fakeEnv, mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Run(ctx, Options{Path: exe, Timeout: timeout}, req)
}

func TestRequestOfTheDemoHost(t *testing.T) {
	req := request(t, 0)
	if req.Contract != Contract || req.Core != (Core{Name: "MemorY", Version: "test", EngineVersion: engine.Version}) ||
		req.RequestDT != "2026-11-13T09:30:00.000Z" || req.PreviousLogs != DefaultPreviousLogs || len(req.Dimms) != 2 {
		t.Fatalf("request: %+v, %d DIMMs", req.Core, len(req.Dimms))
	}
	var row *RequestDIMM
	for i := range req.Dimms {
		if req.Dimms[i].Location.Slot == "CPU0_C1" {
			row = &req.Dimms[i]
		}
	}
	if row == nil {
		t.Fatal("no entry for CPU0_C1")
	}
	// 1,232 errors: the newest one, 1,000 before it, 231 that were not sent.
	if row.DimmSerialNumber != "3B9C56" || row.PartNumber != "EXM64G48R2X4-A" || row.MemoryType != "DDR5" || row.SizeBytes != 64<<30 || *row.Ranks != 2 || *row.DeviceWidth != 4 ||
		row.Corrected != 1231 || row.Uncorrected != 1 || len(row.PreviousLogs) != 1000 || row.EarlierLogs != 231 {
		t.Errorf("CPU0_C1: %+v, %d previous, %d earlier", row.Location, len(row.PreviousLogs), row.EarlierLogs)
	}
	if row.Location.HostID != "r12-n034" || *row.Location.SocketId != 0 || *row.Location.ChannelId != 2 || *row.Location.ChannelSlotId != 0 {
		t.Errorf("place: %+v", row.Location)
	}
	// The records are in FMFM field names, the oldest first, the newest
	// apart.
	if row.NewLog.ErrorDT == "" || row.NewLog.Row == nil || row.NewLog.HostID != "r12-n034" || row.NewLog.Grade != "A" {
		t.Errorf("newest record: %+v", row.NewLog)
	}
	for i := 1; i < len(row.PreviousLogs); i++ {
		if row.PreviousLogs[i].ErrorDT < row.PreviousLogs[i-1].ErrorDT {
			t.Fatalf("previous records are not in time order at %d", i)
		}
	}
	if last := row.PreviousLogs[len(row.PreviousLogs)-1]; last.ErrorDT > row.NewLog.ErrorDT {
		t.Errorf("the newest record is %s, a previous one %s", row.NewLog.ErrorDT, last.ErrorDT)
	}
	// The key of a DIMM is the ledger's, and says nothing about the DIMM.
	if row.DimmId == row.DimmSerialNumber || row.DimmId == "" {
		t.Errorf("DimmId %q", row.DimmId)
	}

	// Fewer previous records on request.
	few := request(t, 10)
	for _, d := range few.Dimms {
		if d.Location.Slot == "CPU0_C1" && (len(d.PreviousLogs) != 10 || d.EarlierLogs != 1221 || !reflect.DeepEqual(d.NewLog, row.NewLog)) {
			t.Errorf("with 10 previous records: %d sent, %d earlier", len(d.PreviousLogs), d.EarlierLogs)
		}
		if d.Location.Slot == "CPU1_D1" && (len(d.PreviousLogs) != 10 || d.EarlierLogs != 42) {
			t.Errorf("CPU1_D1 with 10 previous records: %d sent, %d earlier", len(d.PreviousLogs), d.EarlierLogs)
		}
	}
	if few.PreviousLogs != 10 {
		t.Errorf("PreviousLogs = %d", few.PreviousLogs)
	}
}

// A host without errors gives a request without DIMMs.
func TestRequestWithoutErrors(t *testing.T) {
	req, err := BuildRequest(ctx, testledger.OpenGPU(t), RequestOptions{Now: now})
	if err != nil || len(req.Dimms) != 0 || req.Dimms == nil {
		t.Errorf("request: %+v, %v", req, err)
	}
}

func TestRun(t *testing.T) {
	req := request(t, 0)
	res, err := run(t, "ok", req, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Analyzer != (Analyzer{Name: "test analyzer", Version: "0.1", Vendor: "Example Memory"}) || len(res.DIMMs) != 2 || len(res.Notes) != 0 || res.Took <= 0 || res.Path == "" {
		t.Errorf("result: %+v", res)
	}
	for _, d := range req.Dimms {
		got := res.DIMMs[d.DimmId]
		if got == nil || got.Baseline.Percentile != 97.5 || got.Level2.Lines[0] != (Line{"records seen", fmt.Sprint(len(d.PreviousLogs) + 1)}) || got.Level2.Notes[0] != "values of a test analyzer" {
			t.Errorf("DIMM %s: %+v", d.Location.Slot, got)
		}
	}
	// The percentile reaches the engine through the hook it has for it.
	pb := res.PartBaseline()
	if p, ok := pb.Percentile(&model.DIMM{UID: req.Dimms[0].DimmId}, 0, 0); !ok || p != 97.5 {
		t.Errorf("baseline = %v, %v", p, ok)
	}
	if _, ok := pb.Percentile(&model.DIMM{UID: "other"}, 0, 0); ok {
		t.Error("a percentile for a DIMM the analyzer did not answer for")
	}
	if _, ok := pb.Percentile(nil, 0, 0); ok {
		t.Error("a percentile for no DIMM")
	}

	// What an analyzer writes to its error output is its own business.
	if res, err := run(t, "stderr", req, 0); err != nil || len(res.DIMMs) != 0 || res.PartBaseline() != nil {
		t.Errorf("stderr: %+v, %v", res, err)
	}
	// An analyzer that does not read its input is no error by itself.
	if res, err := run(t, "no-read", req, 0); err != nil || res.Analyzer.Name != "deaf" {
		t.Errorf("no-read: %+v, %v", res, err)
	}
	// It asks for more previous records than were sent: said, not failed.
	res, err = run(t, "want-more", req, 0)
	if err != nil || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "works with 5000 previous records per DIMM; 1000 were sent, and 1 DIMMs have more on record") {
		t.Errorf("want-more: %+v, %v", res, err)
	}
	// Fields beyond the contract are passed over and named.
	res, err = run(t, "extra-fields", req, 0)
	if err != nil || len(res.Notes) != 1 || res.Notes[0] != "the response has fields this version does not read: Dimms.Confidence, Dimms.Level_2, Signature" {
		t.Errorf("extra-fields: %+v, %v", res, err)
	}
}

// What makes a run fail, and what the reason says.
func TestRunFailures(t *testing.T) {
	req := request(t, 0)
	head := `{"Contract":"` + Contract + `","Analyzer":{"Name":"a","Version":"1"},"Dimms":[`
	for _, tc := range []struct {
		name, mode string
		timeout    time.Duration
		want       string
	}{
		{"takes too long", "slow", 500 * time.Millisecond, "no answer within 500ms; --plugin-timeout sets the limit"},
		{"fails with a message", "crash", 0, "failed (exit status 3): license file not found: /opt/vendor/license.key"},
		{"fails without a word", "silent-crash", 0, "failed (exit status 4)"},
		{"writes nothing", "nothing", 0, "the response is not readable: the analyzer wrote nothing to its standard output"},
		{"writes something else", "garbage", 0, "the response is not readable: not JSON in the form of the contract"},
		{"writes two documents", "two-documents", 0, "the response is not readable: text after the end of the JSON document"},
		{"writes too much", "huge", 0, "the response is larger than 16 MiB"},
		{"writes bytes that are no text", "not-utf8", 0, "the response is not readable: it is not UTF-8"},
		{"another contract", `{"Contract":"my-analyzer/2","Analyzer":{"Name":"a","Version":"1"},"Dimms":[]}`, 0,
			`it names the contract "my-analyzer/2"; this version speaks "my-analyzer/1"`},
		{"no contract", `{"Analyzer":{"Name":"a","Version":"1"},"Dimms":[]}`, 0, `it names the contract ""`},
		{"no name", `{"Contract":"` + Contract + `","Analyzer":{"Version":"1"},"Dimms":[]}`, 0, "the name of the analyzer is empty"},
		{"a name with an escape sequence", `{"Contract":"` + Contract + `","Analyzer":{"Name":"a\u001b[31m","Version":"1"},"Dimms":[]}`, 0, "the name of the analyzer holds a control character"},
		{"a wrong type", head + `{"DimmId":"DIMM","Baseline":{"Percentile":"high"}}]}`, 0, "not JSON in the form of the contract"},
		{"a DIMM that was not asked about", head + `{"DimmId":"d0000"}]}`, 0, `Dimms[0] names the DIMM "d0000", which the request does not hold`},
		{"a DIMM twice", head + `{"DimmId":"DIMM"},{"DimmId":"DIMM"}]}`, 0, "Dimms[1] is a second analysis of the DIMM"},
		{"a percentile out of range", head + `{"DimmId":"DIMM","Baseline":{"Percentile":140}}]}`, 0, "Dimms[0].Baseline.Percentile is 140; a percentile is a number from 0 to 100"},
		{"a negative percentile", head + `{"DimmId":"DIMM","Baseline":{"Percentile":-1}}]}`, 0, "Dimms[0].Baseline.Percentile is -1"},
		{"a line without a label", head + `{"DimmId":"DIMM","Level2":{"Lines":[{"Label":" ","Value":"x"}]}}]}`, 0, "Dimms[0].Level2.Lines[0].Label is empty"},
		{"a value with a line break", head + `{"DimmId":"DIMM","Level2":{"Lines":[{"Label":"a","Value":"x\ny"}]}}]}`, 0, "Dimms[0].Level2.Lines[0].Value holds a control character"},
		{"a value that is too long", head + `{"DimmId":"DIMM","Level2":{"Lines":[{"Label":"a","Value":"` + strings.Repeat("x", MaxValue+1) + `"}]}}]}`, 0, "Dimms[0].Level2.Lines[0].Value is longer than 400 bytes"},
		{"too many lines", head + `{"DimmId":"DIMM","Level2":{"Lines":[` + strings.TrimSuffix(strings.Repeat(`{"Label":"a","Value":"b"},`, MaxLines+1), ",") + `]}}]}`, 0, "Dimms[0].Level2 has 65 lines and 0 notes; the limits are 64 and 16"},
		{"an empty note", head + `{"DimmId":"DIMM","Level2":{"Notes":[""]}}]}`, 0, "Dimms[0].Level2.Notes[0] is empty"},
		{"a negative row", head + `{"DimmId":"DIMM","AdjacentRows":[{"Row":-1,"Neighbors":[0]}]}]}`, 0, "Dimms[0].AdjacentRows[0].Row is -1"},
		{"a row its own neighbour", head + `{"DimmId":"DIMM","AdjacentRows":[{"Row":7,"Neighbors":[7]}]}]}`, 0, "Dimms[0].AdjacentRows[0] names row 7 its own neighbour"},
		{"a neighbour twice", head + `{"DimmId":"DIMM","AdjacentRows":[{"Row":7,"Neighbors":[8,8]}]}]}`, 0, "Dimms[0].AdjacentRows[0] names the neighbour 8 twice"},
		{"a row twice", head + `{"DimmId":"DIMM","AdjacentRows":[{"Row":7,"Neighbors":[8]},{"Row":7,"Neighbors":[6]}]}]}`, 0, "Dimms[0].AdjacentRows[1] names row 7 a second time"},
		{"too many neighbours", head + `{"DimmId":"DIMM","AdjacentRows":[{"Row":100,"Neighbors":[0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16]}]}]}`, 0, "Dimms[0].AdjacentRows[0] has 17 neighbours; the limit is 16"},
		{"a bank out of range", head + `{"DimmId":"DIMM","AdjacentRows":[{"Bank":-2,"Row":1,"Neighbors":[2]}]}]}`, 0, "Dimms[0].AdjacentRows[0].Bank is -2"},
		{"rows that do not agree", head + `{"DimmId":"DIMM","AdjacentRows":[{"Row":7,"Neighbors":[9]},{"Row":9,"Neighbors":[10]}]}]}`, 0, "row 7 names row 9 a neighbour, and row 9 does not name row 7"},
		{"a negative wish", `{"Contract":"` + Contract + `","Analyzer":{"Name":"a","Version":"1","PreviousLogs":-5},"Dimms":[]}`, 0, "Analyzer.PreviousLogs is negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := run(t, tc.mode, req, tc.timeout)
			var perr *Error
			if res != nil || !errors.As(err, &perr) || !strings.Contains(perr.Reason, tc.want) {
				t.Fatalf("result %+v, error %v\nwant the reason to say %q", res, err, tc.want)
			}
			if !strings.HasPrefix(err.Error(), "vendor analyzer "+perr.Path+": ") || perr.Path == "" {
				t.Errorf("error text %q", err)
			}
		})
	}

	// A program that is not there, one that may not be run, no program.
	dir := t.TempDir()
	for name, path := range map[string]string{"missing": dir + "/none", "a directory": dir, "no program": ""} {
		_, err := Run(ctx, Options{Path: path}, req)
		var perr *Error
		if !errors.As(err, &perr) || (!strings.Contains(perr.Reason, "cannot be run") && !strings.Contains(perr.Reason, "no program named")) {
			t.Errorf("%s: %v", name, err)
		}
	}
	plain := dir + "/notes.txt"
	if err := os.WriteFile(plain, []byte("not a program\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, Options{Path: plain}, req); err == nil || !strings.Contains(err.Error(), "cannot be run") {
		t.Errorf("a file that is no program: %v", err)
	}

	// The caller gave up: that is the caller's error, not the analyzer's.
	gone, cancel := context.WithCancel(ctx)
	cancel()
	t.Setenv(fakeEnv, "ok")
	exe, _ := os.Executable()
	if _, err := Run(gone, Options{Path: exe}, req); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled run: %v", err)
	}
}

// Rows the analyzer names neighbours of each other are neighbours for the
// engine, in both directions, and the rows it did not answer for keep the
// neighbours their numbers give them.
func TestAdjacency(t *testing.T) {
	req := request(t, 0)
	id := req.Dimms[0].DimmId
	mode := `{"Contract":"` + Contract + `","Analyzer":{"Name":"a","Version":"1"},"Dimms":[{"DimmId":"DIMM","AdjacentRows":[` +
		`{"Rank":0,"DeviceId":7,"BankGroup":0,"Bank":3,"Row":100,"Neighbors":[164]},` +
		`{"Rank":0,"DeviceId":7,"BankGroup":0,"Bank":3,"Row":101,"Neighbors":[]},` +
		`{"Rank":0,"DeviceId":7,"BankGroup":0,"Bank":3,"Row":200,"Neighbors":[201,264]},` +
		`{"Rank":0,"DeviceId":7,"BankGroup":0,"Bank":3,"Row":201,"Neighbors":[200]}]}]}`
	res, err := run(t, mode, req, 0)
	if err != nil {
		t.Fatal(err)
	}
	adj := res.Adjacency()
	site := engine.Site{DIMM: &model.DIMM{UID: id}, Rank: model.Some(0), DeviceId: model.Some(7), BankGroup: model.Some(0), Bank: model.Some(3)}
	for row, want := range map[int][]int{
		100: {164},           // as the analyzer said
		164: {163, 165, 100}, // not answered for: its numbers, and the row that names it
		101: nil,             // answered: no neighbours
		99:  {98},            // 100 was answered for and does not name 99
		102: {103},           // 101 was answered for and does not name 102
		200: {201, 264},
		201: {200},
		202: {203}, // 201 does not name 202
		264: {263, 265, 200},
		500: {499, 501}, // far from anything the analyzer spoke about
		0:   {1},
	} {
		if got := adj.NeighborRows(site, row); !(len(got) == 0 && len(want) == 0) && !reflect.DeepEqual(got, want) {
			t.Errorf("neighbours of row %d: %v, want %v", row, got, want)
		}
	}
	// The relation is symmetric over every row near the answers.
	for a := 90; a < 280; a++ {
		for _, b := range adj.NeighborRows(site, a) {
			back := adj.NeighborRows(site, b)
			found := false
			for _, x := range back {
				found = found || x == a
			}
			if !found {
				t.Errorf("row %d names row %d, and row %d names %v", a, b, b, back)
			}
		}
	}
	// Another bank, another DIMM and no DIMM are not what was answered for.
	other := site
	other.Bank = model.Some(4)
	if got := adj.NeighborRows(other, 100); !reflect.DeepEqual(got, []int{99, 101}) {
		t.Errorf("another bank: %v", got)
	}
	if got := adj.NeighborRows(engine.Site{DIMM: &model.DIMM{UID: "x"}, Rank: site.Rank, DeviceId: site.DeviceId, BankGroup: site.BankGroup, Bank: site.Bank}, 100); !reflect.DeepEqual(got, []int{99, 101}) {
		t.Errorf("another DIMM: %v", got)
	}
	if got := adj.NeighborRows(engine.Site{}, 100); !reflect.DeepEqual(got, []int{99, 101}) {
		t.Errorf("no DIMM: %v", got)
	}

	// Without an answer about rows the engine keeps its own adjacency.
	res, err = run(t, "ok", req, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, logical := res.Adjacency().(engine.LogicalAdjacency); !logical {
		t.Errorf("adjacency without an answer: %T", res.Adjacency())
	}
	var none *Result
	if _, logical := none.Adjacency().(engine.LogicalAdjacency); !logical || none.PartBaseline() != nil {
		t.Error("no result must give the engine its own adjacency and no baseline")
	}
}
