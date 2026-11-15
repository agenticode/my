// Package labcase holds the regression tests over the captures of real
// hosts under testdata/real.
//
// A capture is what a lab host recorded after a known set of errors was
// injected into it: the kernel log, the rasdaemon database, the EDAC
// counters, the memory devices of SMBIOS, and what "my" itself said on the
// host. Next to it lies the injection plan. The tests read each capture the
// ways a reader would (as a copy of the host's files, as loose files, as
// the case bundle written on the host) and compare the ledger with the
// plan.
//
// docs/validation-lab.md says how the captures were made.
package labcase
