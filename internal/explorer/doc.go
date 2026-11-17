// Package explorer holds the readers and the board layout of the Memory
// Explorer: where a memory slot sits on the board, read from its SMBIOS
// locator strings, and the counters of the kernel about memory (meminfo,
// vmstat, slab, NUMA nodes, CPU caches, page statistics, processes).
//
// Every function is pure: text in, data out. The functions give what their
// reference gives for every input, defects included: they decide where a
// slot is drawn and nothing else. The ledger decides what a slot holds and
// the engine decides every finding; nothing here ties a record to a slot.
//
// The reference is written in JavaScript, so numbers, strings and regular
// expressions follow its rules here (jsnum.go, jsstr.go, jsre.go).
package explorer

// Carried over from the Memory Explorer of aterm by its author. See NOTICE.
