// Package dcbrain reads the DRAM error dataset published in the dramdata
// directory of github.com/alibaba-edu/dcbrain: corrected memory errors of
// a server fleet with rank, bank, row and column, an inventory of the
// servers, and tickets of server failures attributed to memory.
//
// The dataset has three tables, each a comma-separated file with a header:
//
//	mcelog           sid, memoryid, rankid, bankid, row, col, error_type, error_time
//	inventory        sid, server_manufacturer, DRAM_model, DIMM_number
//	trouble_tickets  sid, failure_type, failed_time
//
// Open finds them in a directory or in the zip archive the dataset is
// distributed as, and reads them as streams. ReadErrors, ReadInventory and
// ReadTickets parse the tables; a line that cannot be used is skipped and
// reported with the reason.
//
// A row of the error table becomes a ledger event as follows.
//
//	sid         host
//	memoryid    slot "DIMM<id>"; socket, channel and position by Topology
//	rankid      Rank
//	bankid      Bank (0 to 15; bank group and bank address are not told apart)
//	row, col    Row, Column
//	error_type  1 read and 3 write: ErrorScrubType Demand; 2 scrub: Patrol
//	error_time  ErrorDT, counted on the dataset calendar from a base date
//
// Every row is stored as a corrected error of observation grade A. The
// dataset has no DRAM device, no failing bits, no physical address, no
// DIMM serial numbers, no uncorrected error records and no repair
// records. What follows from that for the analysis engine:
//
//   - a fault that spans several banks is reported as a device fault with
//     the mode marked as estimated; it cannot be told from a DIMM fault;
//   - a DIMM is identified by server and slot, so a replaced DIMM cannot be
//     told from its predecessor;
//   - the rules that need uncorrected errors, firmware marks, repairs, page
//     offlines or slot moves cannot fire.
//
// Times are written on a calendar of the dataset's own (see Stamp). The
// real dates are not published.
//
// Adapter imports one server into a ledger. The evaluation harness in
// internal/eval reads the whole fleet with the same mapping.
package dcbrain
