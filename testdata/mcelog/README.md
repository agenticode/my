# Output of `mcelog --client`

Both files are public samples of what the client prints, kept as they were published.

| File | Origin |
| --- | --- |
| `client-paper.txt` | Listing 1 of "mcelog: memory error handling in user space" (A. Kleen, Linux Kongress 2010), the paper in the mcelog source tree as `lk10-mcelog.pdf`. Two DIMMs with three corrected errors each. This version of the daemon listed its DIMMs in the order of its hash table. |
| `client-x3850.txt` | A public troubleshooting post about an IBM System x3850 X5 (Xeon E7, 2013): a block for socket 3 as a whole and one for channel 0 of that socket, where the daemon could not tell the DIMM. Indented with spaces, as copied from a terminal. |

Neither sample has the `DMI_NAME` line or the page table. The tests of the adapter write those lines in the form the source of the daemon prints them (`memdb.c`, `page.c`).
