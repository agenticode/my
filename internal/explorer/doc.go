// Package explorer holds the readers and the board layout of the Memory
// Explorer: where a memory slot sits on the board, read from its SMBIOS
// locator strings, and the counters of the kernel about memory (meminfo,
// vmstat, slab, NUMA nodes, CPU caches, page statistics, processes).
//
// Every function is pure: text in, data out. The readers decide where a
// slot is drawn and nothing else. The ledger decides what a slot holds and
// the engine decides every finding; nothing here ties a record to a slot.
//
// The readers were first written in JavaScript, and their answers on the
// files under testdata/explorer are kept as want files. Numbers, strings
// and regular expressions follow the rules of that language here
// (jsnum.go, jsstr.go, jsre.go), so that the answers stay the same.
package explorer
