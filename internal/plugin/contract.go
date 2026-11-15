// Package plugin runs a vendor analyzer and checks what it answers.
//
// The open core classifies faults with public knowledge: Level 1 of the
// FMFM framework. What only the maker of a DRAM device knows stays outside
// it: which logical rows are neighbours inside the device, what error rate
// is usual for a part number, what a pattern of failing data lines means.
// A vendor analyzer supplies that as a separate program, in any language,
// under its own license.
//
// The contract has the shape the framework gives its analyzers. The
// program is stateless. It is started once per run, gets one request on
// its standard input and writes one response to its standard output: for
// each DIMM the newest error record and the records before it, in the
// field names of the FMFM runtime error log, in; an analysis per DIMM out.
// The core keeps the history and supplies the previous records; the
// program keeps nothing between two runs.
//
// The core trusts a response no further than it can check it: the
// response is bounded in time and size, must name the contract, and every
// value in it is checked before it is used. A program that fails, takes
// too long or answers something else stops the run with the reason.
// docs/plugin-contract.md is the reference.
package plugin

import (
	"github.com/agenticode/my/internal/fmfmlog"
)

// Contract names the contract and its version. The version changes when a
// field is renamed or removed or changes its meaning; new fields do not
// change it.
const Contract = "my-analyzer/1"

// Request is what an analyzer reads from its standard input.
type Request struct {
	// Contract is the constant Contract.
	Contract string `json:"Contract"`
	// Core names the program that asks.
	Core Core `json:"Core"`
	// RequestDT is the time of the request.
	RequestDT string `json:"RequestDT"`
	// PreviousLogs is the number of records before the newest one that the
	// core sends per DIMM at most.
	PreviousLogs int `json:"PreviousLogs"`
	// Dimms holds one entry per DIMM that has error records.
	Dimms []RequestDIMM `json:"Dimms"`
}

// Core names the program that sends a request.
type Core struct {
	Name          string `json:"Name"`
	Version       string `json:"Version"`
	EngineVersion string `json:"EngineVersion"`
}

// RequestDIMM is one DIMM of a request: what it is, where it sits, and
// its error records.
type RequestDIMM struct {
	// DimmId is the key of the DIMM in this request. The response names a
	// DIMM by it. It says nothing else: it is no serial number.
	DimmId           string `json:"DimmId"`
	DimmSerialNumber string `json:"DimmSerialNumber,omitempty"`
	Manufacturer     string `json:"Manufacturer,omitempty"`
	PartNumber       string `json:"PartNumber,omitempty"`
	MemoryType       string `json:"MemoryType,omitempty"`
	ModuleType       string `json:"ModuleType,omitempty"`
	SizeBytes        int64  `json:"SizeBytes,omitempty"`
	Ranks            *int   `json:"Ranks,omitempty"`
	DeviceWidth      *int   `json:"DeviceWidth,omitempty"`

	Location fmfmlog.Location `json:"Location"`
	// Corrected and Uncorrected are the errors of the DIMM in the ledger,
	// each counted once.
	Corrected   int64 `json:"Corrected"`
	Uncorrected int64 `json:"Uncorrected"`

	// NewLog is the newest error record of the DIMM, and PreviousLogs the
	// records before it, the oldest first. EarlierLogs counts the records
	// older than those that were not sent.
	NewLog       fmfmlog.Runtime   `json:"NewLog"`
	PreviousLogs []fmfmlog.Runtime `json:"PreviousLogs"`
	EarlierLogs  int               `json:"EarlierLogs"`
}

// Response is what an analyzer writes to its standard output.
type Response struct {
	// Contract is the contract the analyzer answers under. It must be the
	// one of the request.
	Contract string `json:"Contract"`
	// Analyzer names the program that answers.
	Analyzer Analyzer `json:"Analyzer"`
	// Dimms holds the analyses, each for one DIMM of the request. A DIMM
	// the analyzer has nothing to say about is left out.
	Dimms []ResponseDIMM `json:"Dimms"`
}

// Analyzer names a vendor analyzer.
type Analyzer struct {
	Name    string `json:"Name"`
	Version string `json:"Version"`
	Vendor  string `json:"Vendor,omitempty"`
	// PreviousLogs is the number of previous records per DIMM the analyzer
	// works with. When it is larger than what the request sent, the run
	// says so, and the caller can send more.
	PreviousLogs int `json:"PreviousLogs,omitempty"`
}

// ResponseDIMM is the analysis of one DIMM.
type ResponseDIMM struct {
	DimmId string `json:"DimmId"`
	// Level2 is what the analyzer says beyond the public classification.
	// It is shown in reports for failure analysis and nowhere else.
	Level2 *Level2 `json:"Level2,omitempty"`
	// AdjacentRows says which rows lie next to a row inside the device.
	// The core uses it when it groups rows into one fault; a row that is
	// not listed is taken to lie next to the rows numbered one below and
	// one above it.
	AdjacentRows []AdjacentRows `json:"AdjacentRows,omitempty"`
	// Baseline places the error count of the DIMM among the DIMMs of its
	// part number that the vendor knows.
	Baseline *Baseline `json:"Baseline,omitempty"`
}

// Level2 is a list of labelled values and notes.
type Level2 struct {
	Lines []Line   `json:"Lines,omitempty"`
	Notes []string `json:"Notes,omitempty"`
}

// Line is one labelled value.
type Line struct {
	Label string `json:"Label"`
	Value string `json:"Value"`
}

// AdjacentRows names the physical neighbours of one row of one bank. The
// place is given the way the records give it: a field the records of the
// platform do not carry is left out.
type AdjacentRows struct {
	Rank      *int  `json:"Rank,omitempty"`
	DeviceId  *int  `json:"DeviceId,omitempty"`
	BankGroup *int  `json:"BankGroup,omitempty"`
	Bank      *int  `json:"Bank,omitempty"`
	Row       int   `json:"Row"`
	Neighbors []int `json:"Neighbors"`
}

// Baseline is a percentile from 0 to 100; 50 means "as usual for the part".
type Baseline struct {
	Percentile float64 `json:"Percentile"`
}

// Limits of a response. A response beyond them is refused: an analyzer
// that needs more is doing something this contract does not cover.
const (
	// MaxResponseBytes bounds the output of an analyzer.
	MaxResponseBytes = 16 << 20
	// MaxLines and MaxNotes bound the Level 2 output for one DIMM, and
	// MaxLabel, MaxValue and MaxNote the length of their texts.
	MaxLines = 64
	MaxNotes = 16
	MaxLabel = 80
	MaxValue = 400
	MaxNote  = 600
	// MaxRows bounds the rows with neighbours for one DIMM, MaxNeighbors
	// the neighbours of one row, and MaxRow the number of a row.
	MaxRows      = 20000
	MaxNeighbors = 16
	MaxRow       = 1<<31 - 1
	// MaxName bounds the name, version and vendor of an analyzer.
	MaxName = 120
)
