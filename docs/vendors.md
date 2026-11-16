# Vendors

A BMC or a BIOS says what happened to memory in the words of its vendor: a message ID of a registry, a record of the IPMI system event log, a sentence of a log. MemorY reads each such record as one of 27 common events, the same for every vendor ([data-model.md](data-model.md#firmware-records)), and a finding speaks of a repair, a prediction or an uncorrected error whatever vendor wrote it. A table per vendor or standard says what each message means. This page lists the tables, what each reads and how far it was checked, how a log gets out of a BMC, and how to add a table or send a sample.

## The tables

```
$ my vendors
 table      vendor or standard  messages  basis
 dell@2     Dell                     311  registry: not checked on real hardware
 dmtf@1     DMTF                      19  registry: not checked on real hardware
 ipmi@1     IPMI                      11  registry: not checked on real hardware
 openbmc@1  OpenBMC                   15  registry: not checked on real hardware
 wording@1  any, by wording           15  rules of this tool
 my vendors TABLE: the messages of a table; my vendors --seen: what the BMCs of a case published
```

A table holds facts: message IDs, the role of each argument, the vendor's severity, the common event, and for every line the public source it was read from. It holds no sentence of a vendor; the text an operator sees is the text the BMC wrote into the entry. `my vendors TABLE` lists the messages of a table by common event with its sources:

```
$ my vendors openbmc
openbmc@1  OpenBMC  registry: not checked on real hardware
 applies to: message IDs of the registry OpenBMC, the same names under any registry
 sources:
  registry: OpenBMC message registry OpenBMC.0.5.0 (bmcweb
    redfish-core/include/registries/openbmc.json)
    https://github.com/openbmc/bmcweb/blob/3cdd1fa/redfish-core/include/registries/openbmc.json
  hooks: intel-ipmi-oem src/ipmi_to_redfish_hooks.cpp: the SEL records of the BIOS behind these
    messages https://github.com/openbmc/intel-ipmi-oem/blob/b36a530/src/ipmi_to_redfish_hooks.cpp
 ce               MemoryECCCorrectable
 ue               MemoryECCUncorrectable, MemoryParityCommandAndAddress, MemoryParityNotKnown
 sparing          SparingRedundancyDegraded
 device-sparing   ADDDCCorrectable
 mirror-failover  MirroringRedundancyDegraded
 log-cleared      EventLogCleared
 thermal          MemoryThermTrip
 config-state     MemoryRASConfigurationDisabled, MemoryRASConfigurationEnabled,
                  MemoryRASModeDisabled, MemoryRASModeEnabled
 cleared          MirroringRedundancyFull, SparingRedundancyFull
```

The basis of a table says how far it was checked:

| Basis | Meaning |
| --- | --- |
| `capture` | a record of a real machine with this event is a fixture of the repository, or was read in the lab |
| `sample` | a public paste of a real record was rebuilt as a fixture |
| `registry` | the message was read in a registry a real BMC serves, or in the open source of a BMC; no record of it was seen |
| `documents` | from vendor documents, or second-hand |

No table has reached `capture`: no vendor publishes a log entry of a real memory error that a fixture may copy, and none was captured yet. Every table other than `capture` prints "not checked on real hardware" here, in `my vendors`, in the data quality section of a report, and as an open check of a finding whose deciding record came through it.

## What each table reads

| Table | Reads | Key | Messages | Sources |
| --- | --- | --- | --- | --- |
| `dmtf@1` | Redfish log entries of the standard registries `Platform`, `LogService` and `ResourceEvent` | the name after registry and version in `MessageId` | 10 memory messages of `Platform` 1.5.0, `LogCleared`, 8 lines for 6 messages of `ResourceEvent` that are about memory when the entry's `OriginOfCondition` is a memory resource | [Redfish-Publications](https://github.com/DMTF/Redfish-Publications) `registries/` |
| `openbmc@1` | the OpenBMC registry under any prefix: a company that changes the registry renames the prefix and keeps the names | the name in `MessageId` | 14 memory messages and `EventLogCleared`; `SELEntryAdded` is read as the SEL record in its argument | [bmcweb](https://github.com/openbmc/bmcweb) `openbmc.json`, [intel-ipmi-oem](https://github.com/openbmc/intel-ipmi-oem) |
| `ipmi@1` | the states of the IPMI memory sensor, in a SEL record, in a Redfish entry of type `SEL` (event bytes in `MessageId`, with or without `0x`) or in the text a SEL tool prints | sensor type 0Ch, sensor-specific offset | 11 | [ipmitool](https://github.com/ipmitool/ipmitool) `include/ipmitool/ipmi_sel.h` |
| `dell@2` | iDRAC messages under the registry `IDRAC`, or without a registry (`racadm lclog view`); the sentences of `racadm getsel`, which carry no ID, in a log of a Dell system; every `MEM` ID is about memory, and the prefixes `PDR`, `STOR`, `CTL`, `VDR`, `BAT`, `SSD` and `ENC` are not | the ID after registry and version; for a sentence, a pattern | 285 lines for 282 IDs: 120 of the 122 `MEM` IDs (`MEM8500` and `MEM8501`, the low memory of the operating system, are skipped), 146 `UEFI`, 7 `PST`, 5 `SEL`, 2 `SYS`, 2 `PR`, and the two argument counts of `MEM6102`, `MEM6104` and `MEM7114`; 26 sentence patterns, 3 of them `sample` | the `IDRAC` registries 2.9.0, 2.10.0, 2.14.0 and 2.15.0 four servers serve in Dell's public mockups ([iDRAC-Redfish-Scripting](https://github.com/dell/iDRAC-Redfish-Scripting)); Dell's YX4X memory RAS white paper for self-healing and `MEM9072`; a public `racadm getsel` paste ([Wikimedia T317662](https://phabricator.wikimedia.org/T317662)) |
| `wording@1` | the text of an entry about memory that no table names, and the text a BMC's own registry gives a message | words | 15 rules | the rules of this tool |

The Dell table reads a module the way the iDRAC writes it in its several logs (`DIMM.Socket.A7`, `DIMM A7`, `DIMM_A7`, `DDR5 DIMM(DIMM A7)`, `A7`, or a list of them) as `DIMM A7`, and the slot pass finds the slot an SMBIOS table calls `A7` by that name. `MEM9072` is an uncorrected error that the patrol scrubber found and nothing consumed, as Dell's RAS white paper says (`scrub-ue`, contained). An entry of the Lifecycle log with the agent `SEL` copies an event of the SEL; it is kept with the origin `sel`, and the ledger counts the event once.

The lookup goes in this order, and the first step that answers decides: the state of an IPMI sensor; a line of a user table, then of a shipped table; the text the BMC's own registry gives the message, read by the wording rules; the text of the entry, read by the wording rules; and when the entry is about memory and none of these explains it, `unclassified`. An unclassified record is kept as information with its text, counted per message ID, and never dropped. A text that fits wording rules of two families says two things and stays unclassified.

## A table of one's own

`my vendors --print TABLE` writes a shipped table as a file to start from. `--event-map FILE` on the commands that read sources (`my scan`, `my import`, `my collect redfish -o NAME.mycase`, and every command that scans) asks such a table before the shipped ones. It can be given several times.

```
format: 1
table: dell
version: 1
vendor: Dell
basis: documents
key: message-id
sources:
  notes:
    title: "our service notes for MEM0804"
    url: https://wiki.example.com/mem0804
messages:
  - id: MEM0804
    event: ppr-done
    args: location
    source: notes
```

A line can read a sentence in place of an ID: `text` is an RE2 pattern of at most 256 bytes whose named groups `location`, `socket` and `count` give the place, and `id` names the message the sentence stands for. Such lines are asked for a log without message IDs when the table names the maker of the system under `applies` (`manufacturer: dell`). `location` names how the table reads a module: `plain`, `openbmc` or `dell`.

A table named like a shipped one holds where the shipped one holds; its lines replace the shipped lines of the same key and add the keys the shipped one lacks. A line that cannot be read is left out and named on standard error; the other lines hold. A line needs a source among the `sources` of its table, an ID occurs once per number of arguments (`nargs`), and a pattern on the first argument (`args-like`) is RE2 of at most 256 bytes. The roles an argument can have are `location`, `locations`, `socket`, `channel`, `position`, `rank`, `subrank`, `device`, `bank`, `row`, `column`, `address`, `count`, `serial`, `part`, `board`, `code`, `channel-valid`, `position-valid` and `-`.

`my vendors --seen` on a case (`my open CASE vendors --seen`) lists the message IDs of the case about memory that no table classes, with their counts: the lines a table of one's own would add.

## Saved output of vendor tools

`my import` reads two forms that a Dell tool prints, besides the Redfish collection of the same iDRAC:

| Form | Recognised by | What is read | Time |
| --- | --- | --- | --- |
| `racadm getsel` | a block of `Record:`, `Date/Time:`, `Source:`, `Severity:`, `Description:` and a line of dashes | the sentence, through the sentence patterns of `dell@2` | `MM/DD/YYYY HH:MM:SS` without a zone |
| `racadm lclog view` (and `racadm getraclog`) | a block from `SeqNumber =` with `Message ID =` and `Timestamp =` | the message ID, its arguments (several can stand on one line), the FQDD, the agent | `YYYY-MM-DD HH:MM:SS` without a zone |

The iDRAC writes its own time. `--tz ZONE` names the zone it runs on; without it the times are read as UTC and the source says so. A record that is not about memory (logins, power, storage) is counted and left out, and its text stays out of the case. The samples of both forms in `testdata/vendorlog` are written by hand after a public paste and the RACADM CLI guide; neither was checked against a real iDRAC.

## Formats that wait for a capture

No reader is written without a public description of the format and a sample to check it with. These have neither yet, and a case that holds them can still be built from a Redfish collection of the same BMC:

| Format | What a capture would have to show |
| --- | --- |
| iDRAC Lifecycle log exported as XML (`racadm lclog export`), TSR and SupportAssist archives | the file names inside the archive and one memory event as it is written |
| `omreport system alertlog`, `omreport system esmlog` | the layout of one memory event with its date |
| HPE `hpasmcli -s "show iml"`, IML exported as CSV | one memory entry, its class and code |
| Lenovo XCC event log export, OneCLI output | one memory event with its ID and arguments |
| Huawei and xFusion iBMC event export | one memory event with its code |
| Inspur IDL download | one memory event with its code |
| Cisco tech-support files and UCS faults | the place of the SEL in the archive; one memory fault with its life cycle |

## Sending a sample

A log of a real machine with a memory event is what moves a table from `registry` or `documents` to `capture`. [CONTRIBUTING.md](../CONTRIBUTING.md) says how to send one. Before it is sent, replace host names, addresses and user names; DIMM serial numbers may stay, they tie the records to a module.
