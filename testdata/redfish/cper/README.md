# CPER memory records

`memory.cperhex` and `memory2.cperhex` are `examples/memory.cperhex` and `examples/memory2.cperhex` of libcper
(<https://github.com/openbmc/libcper>, commit `b250e92`, 2026-11-09), copied without changes: whole CPER records with
one platform memory error section each, in hex. They are distributed under the Apache License 2.0; the licence is in
[LICENSE.md](LICENSE.md). The tests put them, base64-encoded, into the `DiagnosticData` of a log entry written by hand.
