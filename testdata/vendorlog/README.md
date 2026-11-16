# Saved output of vendor tools

These files are written by hand. None is a capture of a real machine; the
makers, part numbers, serial numbers, host names and addresses are made up.

| File | Shape follows |
| --- | --- |
| `racadm-getsel.txt` | the default layout of `racadm getsel` as a public paste of a real server shows it (Wikimedia Phabricator T317662, https://phabricator.wikimedia.org/T317662): `Record:`, `Date/Time:`, `Source:`, `Severity:`, `Description:` and a line of dashes. The sentences of records 3 and 5 are firmware output as the paste shows it, with another module; record 1 is the sentence of the paste as well. The other sentences are those of Dell's message registry (IDRAC 2.15.0) with made-up arguments, and were not seen in a log |
| `racadm-lclog.txt` | the layout of `racadm lclog view` and `racadm getraclog` in the iDRAC9 RACADM CLI Guide (Dell, 2020): `Name = value` lines from `SeqNumber` to `FQDD`, a message that runs over two lines, and record 9305 with its arguments on one line as the guide's own sample prints them. The message IDs and their arguments follow the registry; the values are made up |
