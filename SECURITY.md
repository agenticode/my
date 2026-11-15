# Security

## Reporting a vulnerability

Please do not open a public issue for a security problem.

Report it privately: through "Report a vulnerability" on the Security tab of the repository where GitHub offers it, or by e-mail to the address on the profile of the repository owner (https://github.com/agenticode). Say which version or commit you used (`my version`), what you did, and what happened. A file that triggers the problem helps; if it comes from a real host, make it with `--anonymize` or cut it down first.

You will get an answer that the report arrived. The fix is published with a note in the release that names the problem, and names you if you want that.

## Supported versions

No version is tagged yet. Fixes go to the `main` branch.

## What counts

MemorY reads files that someone else may have written: logs, databases, BMC dumps, case bundles, the answer of a vendor analyzer. It runs as root on servers, and its reports and bundles are passed on. These are security problems:

- an input file that makes the tool write outside its output files, run a program, or read a file the command line did not name;
- an input that puts markup or a script into an HTML report, or control characters onto a terminal;
- a way to make the tool change system state: it must never take a page offline, start a repair, reset a counter or change a setting of a BMC;
- a network connection the command line did not ask for;
- a password of a BMC that ends up in a file, in a log or on the command line of another process;
- a name or an address that `--anonymize` is documented to replace and that stays in a bundle ([docs/bundle-format.md](docs/bundle-format.md));
- memory or time without bound for an input of ordinary size.

A wrong finding is a bug and is welcome as an issue. It is a security problem only when one of the points above is part of it.
