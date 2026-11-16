# Case bundle format

A case bundle is one file that carries the records of a case from the host they were collected on to whoever analyses them. Its extension is `.mycase`. This page describes the members of a bundle, the manifest, what a reader checks, and what `--anonymize` replaces.

The format is plain so that a sender can check what they send: a bundle is a ZIP archive, and the ledger in it is a SQLite file.

```
$ unzip -l examples/demo.mycase
```

lists the four members.

## Writing and reading

| Command | Result |
| --- | --- |
| `my bundle -o case.mycase` | scans this host and writes what it found |
| `my bundle CPU0_C1 -o case.mycase` | the same, with that DIMM as the subject of the case. The bundle still holds the whole host: a finding about one DIMM rests on its neighbours too |
| `ssh HOST sudo my bundle -o - > case.mycase` | `-o -` writes the bundle to the standard output; what the command says goes to the standard error |
| `my import FILES... -o case.mycase` | builds a case from files: kernel logs, a rasdaemon database, BMC dumps, a copy of a host's files |
| `my collect redfish --bmc ADDRESS -o case.mycase` | builds a case from what a BMC answers |
| `my open case.mycase [command]` | shows the case: the commands that show a host work on a bundle |
| `my open case.mycase bundle -o other.mycase --anonymize` | writes a bundle from a bundle, to anonymize it or to name a subject |
| `my report case.mycase -o report.html` | writes the report of the case |
| `my diff before.mycase after.mycase` | compares two cases of one host ([diff.md](diff.md)) |

A bundle is written beside its target and moved into place, so a file found under the name is whole. An existing file is replaced with `--force` only.

`my open` never changes the bundle. It takes the ledger out into a private directory, removes it at the end, and starts with a line about the case:

```
case bundle of 2026-11-13 09:30 UTC, written by my dev; subject CPU0_C1 S/N 3B9C56
```

## Members

| Member | Content |
| --- | --- |
| `manifest.json` | what the bundle is and what it holds |
| `sources.json` | the source inventory: every source a run looked for, with what it yielded or why it could not be read |
| `README.txt` | the same in words, for a person who receives the file and has not seen the tool |
| `ledger.db` | the ledger: error records with their original text, DIMM identities, slots, placements, boots, actions, counter and GPU readings, and the findings of the sender ([data-model.md](data-model.md)) |

## manifest.json

| Field | Content |
| --- | --- |
| `format` | `mycase` |
| `format_version` | 1. A reader accepts bundles up to its own version and refuses a newer one |
| `created_at` | when the bundle was written, UTC |
| `tool` | the version of the tool that wrote it |
| `ledger.file` | `ledger.db` |
| `ledger.schema_version` | the schema version of the ledger |
| `ledger.bytes`, `ledger.sha256` | size and SHA-256 of the ledger |
| `anonymized` | whether names and addresses were replaced |
| `anonymization` | on an anonymized bundle: how many host names (`hosts`), network addresses (`addresses`) and home directories (`users`) were replaced, and whether the sender gave a key (`keyed`) |
| `subject` | the DIMM the case is about, when the sender named one: `host`, `slot`, `serial`, `dimm_uid` |
| `hosts` | per host: `name`, the number of DIMMs, `ce` and `ue`, the number of records, the first and the last time, the boots on record with those of them a fatal hardware error ended (`boots`, `stops`, `panics`), and the kinds of the sources that gave records (`sources`: `klog`, `rasdaemon`, `pstore`, `kdump`, `bert` and the others) |
| `collected` | per table of the ledger: its name, the number of rows, and what a row is |
| `not_collected` | what a bundle never holds, as text |

## sources.json

`scans` lists the collection runs with the fields of the `scan` table (kind `live`, `import` or `synthetic`, start and end, tool version, whether as root). Each has `sources`: one entry per source that was looked for, with `kind`, `origin`, `status` (`ok`, `partial`, `empty`, `absent`, `unreadable` or `error`), `detail` for a status other than ok, the numbers of records, events, actions and skipped records, the events per observation grade, the first and the last time, and `notes`.

A reader of a case can tell from it what the findings did not see: a rasdaemon database that needed root, an EDAC driver that was not loaded, a log that starts after the first boot.

## What a reader checks

| Check | Failure |
| --- | --- |
| the file is a ZIP archive with a manifest of format `mycase` | "not a case bundle" |
| `format_version` is at most the version of the reader | "the bundle was written by a newer version of this tool" |
| the ledger is there and is at most 64 GiB | "the bundle is damaged" |
| the SHA-256 of the ledger equals the one in the manifest | "the bundle is damaged" |
| the schema version of the ledger is at most that of the reader | the message of the ledger: its schema is newer |

A ledger of an older schema is brought up to date in the copy the reader works on.

The findings are computed again from the records when a case is opened. When the bundle holds the findings of the sender and they differ, the first lines say so: another engine version, thresholds the sender changed (with the `--set` arguments that repeat them), or another grade.

## What a bundle holds, and what it never holds

| Holds | Never holds |
| --- | --- |
| memory error records with time, severity and place, and the log lines or database rows they were read from | contents of memory, of files or of processes |
| DIMM identities: manufacturer, part number, serial number, size, type | user accounts, credentials or keys |
| the slots of the board and which DIMM was seen where | network configuration |
| boots: time, kernel release, BIOS and BMC version, kernel command line | kernel log lines that are not about memory errors, page offlining, machine checks or the boot banner |
| repairs and isolation steps the host recorded | |
| GPU memory records, counter readings, ECS readings, what a BMC reported | |
| the host name, the system vendor and product | |
| the paths of the files that were read | |

`unzip -p case.mycase manifest.json` and `sources.json` show it for a given file, and `README.txt` says it in words.

## Anonymizing

`--anonymize` replaces what names a machine on a network or a person:

| What | Replaced by |
| --- | --- |
| host names, in every column and inside text, with the short form of a fully qualified name and the domain that follows it | `host-` and 8 hexadecimal digits |
| the address a BMC was asked at | the same as a host name |
| IPv4 and IPv6 addresses | `ip-` or `ip6-` and 8 digits |
| MAC addresses and the longer hardware addresses of other link types | `mac-` and 8 digits |
| UUIDs of file systems, partitions, encrypted volumes and RAID arrays on the kernel command line, and a machine ID there | `uuid-` or `id-` and 8 digits |
| the name of a home directory in a path (`/home/NAME`, `/Users/NAME`) | `user-` and 8 digits |
| identifiers the ledger derives from the host name: record fingerprints, derived boot IDs, placeholder DIMM identities | a keyed hash of the identifier |

A word is made from a keyed hash (HMAC-SHA-256) of the name, the same word wherever the name occurs, so the records of one host still belong together. Without the key a word does not lead back to the name, and a list of guessed names cannot be tested against it.

Without `--key-file` the key is random and thrown away after the run. With `--key-file FILE` (at least 8 characters) every bundle made with that key replaces a name by the same word, so the bundles of one fleet can be compared by the receiver.

The command prints which name became which word. The sender keeps these lines: the bundle does not hold the names, and an answer about the case uses the words.

What stays: times, places, counts, slot names, the system vendor and product, kernel and firmware versions, and the serial numbers of DIMMs and GPUs. A return is about a serial number.

Two things are not replaced, and a sender who needs them gone has to act before the bundle is made:

- A path is kept as it was given, apart from the name of a home directory. A scan of a host records system paths such as `/var/lib/rasdaemon/ras-mc_event.db`; an import records the path of each file on the command line, directory names included.
- Text inside an original record is searched for the host names of the ledger and for addresses. A name of another machine that a log line mentions, and that no record of the ledger knows as a host, stays.
