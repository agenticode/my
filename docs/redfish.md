# Reading a BMC over Redfish

MemorY can ask the BMC of a server for its memory modules, their error counts, the BIOS memory settings and the event logs. It does so only when a BMC is named on the command line. A scan, a bundle or any other command without `--bmc` opens no network connection; `cmd/my/network_test.go` fails when one does.

This page says how to collect, exactly which requests are sent, what is read, how the BIOS settings are checked, how the records of the operating system and of the BMC are set side by side, and what has not been tried yet.

- [Collecting](#collecting)
- [Requests](#requests)
- [TLS and credentials](#tls-and-credentials)
- [The collection on disk](#the-collection-on-disk)
- [BIOS memory RAS settings](#bios-memory-ras-settings)
- [Operating system and BMC side by side](#operating-system-and-bmc-side-by-side)
- [DIMM labels for rasdaemon](#dimm-labels-for-rasdaemon)
- [Limits](#limits)
- [Lab check](#lab-check)

## Collecting

### One BMC

```sh
export MY_REDFISH_PASSWORD='...'
my collect redfish --bmc https://10.0.0.5 --user admin -o r740-07
my import r740-07 -o r740-07.mycase
my open r740-07.mycase inventory
```

`-o` names a new directory; an existing directory that is not empty is refused, so nothing is overwritten. When the name ends in `.mycase`, the command makes a case bundle from the collection directly. Progress goes to standard error; the summary at the end says how many resources came back and which classes did not. `my collect redfish --help` lists every flag.

The exit code is 0 when everything that was asked for came back, 1 when a collection was written and something was denied, timed out or failed, and 70 when no BMC answered.

### The host and its BMC in one run

Every command that reads the host it runs on takes `--bmc` and the same flags for the account and the certificate, among them `my scan`, `my bundle`, `my report`, `my inventory`, `my bios` and `my export`.

```sh
my bundle --bmc https://10.0.0.5 --user admin -o case.mycase
my report --bmc https://10.0.0.5 --user admin -o case.html
```

The BMC is read first, with a deadline of 5 minutes (`--bmc-deadline`), and its answers go into the same ledger as the host's records. A BMC whose DIMM serial numbers are all unknown to the SMBIOS tables of the host describes another machine: its answers are not added, and the command ends with an error. A BMC that gives no system resource ends the command before the host is scanned.

### Many BMCs

```sh
my collect redfish --targets bmcs.txt --user admin -o collection
```

`bmcs.txt` has one BMC per line: the address, then optional `name=NAME`, `user=NAME` and `password-file=FILE`. Lines that start with `#` are comments.

```text
# rack 12
https://10.0.12.5 name=r12-n034
https://10.0.12.6 name=r12-n035 user=readonly password-file=/run/secrets/r12
```

Each BMC gets a directory below `collection`. Four BMCs are asked at the same time (`--parallel`); the requests to one BMC are sent one after the other.

### Through a jump host

When the BMC network is reachable only from a jump host, forward a local port with SSH and give the forwarded address:

```sh
ssh -N -L 8443:10.0.0.5:443 jump.example.net &
my collect redfish --bmc https://127.0.0.1:8443 --user admin --fingerprint 8F:C4:...:3F:06 -o r740-07
```

The certificate of the BMC names the BMC, and the connection goes to 127.0.0.1, so the name check fails. `--fingerprint` accepts exactly the certificate with that SHA-256 fingerprint, whatever name it carries. Without it, the first attempt fails and prints the fingerprint of the certificate it was shown; compare it with the one the BMC's web page or its administrator gives before using it. The client also honours `HTTPS_PROXY` and `NO_PROXY` as Go's `net/http` reads them.

## Requests

Every request is a GET. A `RoundTripper` in `internal/redfish/client` refuses any other method before it reaches the network, and a test checks that. The one exception is `--session`: the client sends one POST to the session service to log in, and one DELETE of the session it got when the run ends, also after an error or an interrupt. Nothing on the BMC is changed: no setting, no log, no action, no `ClearCurrentPeriod`.

Authentication is HTTP Basic unless `--session` is given. A request that got a 401 is not sent again with the same account, because a BMC can lock an account after failed logins. A 401 before any request with the account was answered ends the run at once: the account is wrong. Later, three 401 answers in a row end it. A dropped session is renewed at most twice per run. A redirect to another host is refused.

The resources, in the order they are asked for:

| Class | Resources |
| --- | --- |
| service root | `/redfish/v1/`; `ProtocolFeaturesSupported.ExpandQuery` decides whether `$expand` is used |
| system | the `Systems` collection and each `ComputerSystem` (at most 8) |
| memory modules | each system's `Memory` collection and every `Memory` resource |
| memory metrics | the `MemoryMetrics` resource a module links with `Metrics` |
| BIOS settings | the system's `Bios` resource |
| pending BIOS settings | the resource `@Redfish.Settings.SettingsObject` of the `Bios` resource points to |
| attribute registry | the entry of the `Registries` collection named by `Bios.AttributeRegistry`, then the copy on the BMC its `Location[].Uri` points to (in English when there is a choice). A registry the BMC holds no copy of is not fetched from its `PublicationUri`, which is another host |
| manager | the `Managers` collection and each `Manager` (firmware version, model, clock) |
| log services, log entries | the `LogServices` of each system, manager and chassis, and the entries of each log |
| firmware inventory | `UpdateService` and its `FirmwareInventory` |
| chassis | the `Chassis` collection and each chassis |
| message registries | after the logs: the entries of the `Registries` collection whose prefix a log entry of the run uses, and those that are not a DMTF standard registry; then the copy on the BMC each one's `Location[].Uri` points to (in English when there is a choice). A registry the BMC holds no copy of is named and not fetched from its `PublicationUri`, which is another host. A registry offered only as an archive (`ArchiveUri`) is named and not read |

Logs that hold no hardware events are skipped unless named with `--log`: the BMC's own journal, the host console, POST codes, audit logs, dumps and telemetry. `--no-logs` skips every log.

Limits of a run, with their defaults:

| Limit | Default | Flag |
| --- | --- | --- |
| time for one request | 30 s | `--timeout` |
| new attempts after a timeout, a connection that broke or was refused, or the answer 408, 429, 500, 502, 503 or 504 | 3, the first after 1 s, each pause twice the one before, at most 30 s | `--retries` |
| longest `Retry-After` that is waited for | 60 s | |
| size of one answer | 32 MiB | |
| size of one collection | 512 MiB | |
| time for one BMC | 20 min (5 min with `--bmc` on the other commands) | `--deadline`, `--bmc-deadline` |
| entries read of one log | 5,000 | `--max-log-entries` |
| message registries | 16 per run, and none after 64 MiB of them | |
| pages followed through `Members@odata.nextLink` | 500 per collection | |
| resources in a row without any answer before the BMC counts as gone | 3 | |

A BMC that stops answering in the middle still yields a collection: what came back is kept, and the manifest lists what did not.

## TLS and credentials

The certificate of the BMC is checked against the system's authorities and its name. Most BMCs ship with a certificate they signed themselves, so the check fails on them until it is told what to trust:

- `--fingerprint SHA256` accepts the one certificate with that fingerprint.
- `--ca-file FILE` trusts only the authorities in that PEM file.
- `--insecure` accepts any certificate and allows plain `http://`. It prints a warning, and the manifest records it as `"tls": "insecure"`.

The password is read from `MY_REDFISH_PASSWORD`, from the first line of `--password-file`, or from the terminal without echo. It is never taken from the command line. The account name may be given with `--user` or `MY_REDFISH_USER`. The password, the Basic authorization header and the session token are removed from every error text and every file the command writes. A password shorter than 8 characters is removed from a file only where it is a whole JSON string, and a session token shorter than 8 characters is not removed from files, so that a short word does not take out every place it happens to occur. The tests run a collection and search its whole output tree, standard output and standard error for the password.

## The collection on disk

The directory is laid out as the DMTF mockup tools lay out a service: one `index.json` per URI, below `redfish/v1/`. Next to it, `manifest.json` (format `my-redfish-collection`, version 1) holds:

- the BMC address and URL, the start and end of the run, how the certificate was checked (`tls`) and the authentication (`auth`), whether `$expand` was used;
- the service, the systems with their BIOS version and attribute registry, the managers with their firmware version, the firmware inventory;
- the clock of the BMC against the collector's;
- every URI with its class, status, HTTP code, size, time and attempts. A status is `ok`, `unsupported` (404, 405, 410, 501), `denied` (401, 403), `timeout`, `error` or `skipped`;
- per class, a summary and its state, which also has `not offered` (the service links no such resource) and `not reached` (the run ended before);
- the limits of the run, and why it stopped early when it did.

`my import DIR` reads the directory; the address of the BMC is treated as a host name by `--anonymize`. `my collect support DIR...` prints from the manifests which BMC gave which class of resources, one column per kind of BMC (vendor, model, firmware, outcome); `--json` gives the same as data. The [lab check](#lab-check) below shows one.

What the adapter reads from each resource, property by property, is in [compatibility.md](compatibility.md#redfish).

## BIOS memory RAS settings

`my bios` reads the memory settings of the BIOS the BMC reported and checks them against a profile. A setting is one of these:

| Setting | States |
| --- | --- |
| patrol scrub | on, off |
| ADDDC | on, off |
| memory mode | independent, Advanced ECC, ADDDC, rank sparing, mirroring, fault resilient |
| CE logging: corrected errors are logged to the BMC | on, off |
| CE threshold event: the event of a corrected error threshold is logged | on, off |
| leaky bucket | on, off |
| post package repair | on, off |
| memory frequency | auto, limited |
| refresh rate | 1x, 2x |

The built-in profile expects patrol scrub on and CE logging on. Every other setting is reported without a judgement. For each setting the check says the value in effect, the pending value when it differs ("takes effect after a reboot"), and whether the attribute registry of this BIOS allows the expected value at all. A registry that does not allow it is reported as "expectation does not fit this BIOS version" and is not counted as a mismatch.

```
$ my open dell-shaped.mycase bios
case bundle of 2026-11-13 09:30 UTC, written by my dev
host node-d  Dell Inc. PowerEdge R740  BIOS 2.12.2  BMC 4.40.00.00
12 memory settings of the BIOS on record, read 2026-11-13 09:30 UTC
attribute registry BiosAttributeRegistry.v1_0_0
profile built-in, mapping for Dell (by the system manufacturer)

 SETTING              STATE        EXPECTED  CHECK             BIOS ATTRIBUTE = VALUE
 patrol scrub         on           on        as expected       MemPatrolScrub = Standard
                      the whole memory is scrubbed once in 24 hours
                      pending: Disabled (off); it takes effect after a reboot
                      the pending value is not what the profile expects (on)
 ADDDC                on           -         reported          AdddcSetting = Enabled
                      the device width (x4 or x8) of 4 of 4 DIMMs is not known: ADDDC has an effect
                      on x4 DIMMs only
 memory mode          independent  -         reported          MemOpMode = OptimizerMode
 CE logging           on           on        as expected       CorrEccSmi = Enabled
 CE threshold event   on           -         reported          CECriticalSEL = Enabled
 leaky bucket         -            -         not mapped
 post package repair  -            -         not in this BIOS  PostPackageRepair
                      the settings of this BIOS on record do not list the attribute
 memory frequency     limited      -         reported          MemFrequency = 2666MHz
 refresh rate         1x           -         reported          MemRefreshRate = 1x
[...]
 2 expectations of the profile, all met
 1 pending value will not be as expected after the next reboot
```

`dell-shaped.mycase` is `my import testdata/redfish/dell-shaped -o dell-shaped.mycase`: a hand-built tree in the shape of the Dell documents below, with no real addresses or serial numbers. The exit code of `my bios` is 1 when a setting in effect does not meet an expectation; a pending value that will not does not change it.

### Where the mappings come from

Each attribute name and value of the built-in profile was read in a public document of the server vendor, and the profile names that document next to the mapping. A setting no public document that was read gives an attribute for is left unmapped and reported as such; no attribute name is made up. `my bios --print-profile` prints the profile, and `my bios --profile FILE` checks against another one, for example with other expectations or the attributes of another BIOS.

| Vendor | Attributes | Source |
| --- | --- | --- |
| Dell | `MemPatrolScrub`, `AdddcSetting`, `MemOpMode`, `CorrEccSmi`, `CECriticalSEL`, `PostPackageRepair`, `MemFrequency`, `MemRefreshRate`; leaky bucket unmapped | Integrated Dell Remote Access Controller 9 Attribute Registry, 2020, Rev. A00, <https://dl.dell.com/topicspdf/idrac9-lifecycle-controller-v4x-series_Reference-Guide_en-us.pdf> |
| HPE | `MemPatrolScrubbing`, `AdvancedMemProtection` (memory mode and ADDDC), `MaxMemBusFreqMHz`, `MemRefreshRate`; CE logging, CE threshold, leaky bucket and PPR unmapped | Bios resource definitions of iLO 5 v3.09, <https://servermanagementportal.ext.hpe.com/docs/redfishservices/ilos/ilo5/ilo5_309/ilo5_bios_resourcedefns309> |
| Lenovo | `Memory_MemorySpeed` (the one value the guide shows); everything else unmapped | Lenovo XClarity Controller 2 REST API reference, GET - The pending BIOS settings, <https://pubs.lenovo.com/xcc2-restapi/pending_bios_settings_get> |
| Supermicro | none: the vendor's pages could not be read for this work | |

Dell's registry names attributes as `BIOS.MemSettings.AdddcSetting`; over Redfish the key is the last part, as Dell's own script `GetSetBiosAttributesREDFISH.py` in <https://github.com/dell/iDRAC-Redfish-Scripting> uses it.

### Device width and ADDDC

ADDDC and device correction act on the DRAM devices of a module, and what they can correct depends on the width of a device. The check states the rule with its source:

- ADDDC is a feature of Intel Xeon platforms, works with x4 DIMMs only, and needs two or more ranks per channel. Enabled on x8 DIMMs, it is reported as "enabled, no effect on x8". Source: Dell, "Memory Errors and Dell EMC PowerEdge YX4X Server Memory RAS Features", v1.0, 2020-01-03, <https://dl.dell.com/manuals/common/dellemc_poweredge_yx4x_memoryras_v1_0.pdf>, which covers Intel Xeon Scalable and AMD EPYC.
- On AMD EPYC, the same paper describes Advanced ECC as correcting a whole device on x4 and a nibble on x8. AMD's paper "Advanced Memory Device Correction (AMDC) for Servers", 2020, <https://www.amd.com/content/dam/amd/en/documents/epyc-business-docs/white-papers/advanced-memory-device-correction.pdf>, says that 2nd generation EPYC corrects errors from a single DRAM chip on a x4 ECC DIMM, and makes no statement about x8.

Redfish has no property for the device width, and neither does SMBIOS type 17. The width is known when the EDAC driver of the host reports it (`dev_type`); a case read from the BMC alone has it as unknown, and the check says so. The width is never guessed from a part number.

### Speed

When a module runs below the speed it is rated for, the check lists what the records show that can be the reason: a memory frequency setting of the BIOS, the number of DIMMs per channel, all modules running at the same speed (which leaves the processor and the board). These are listed as candidates, and none of them is claimed.

### What the findings get from it

The engine takes the settings as context for its findings, without changing a grade or a threshold. It reads them through the built-in profile; `--profile` changes what `my bios` and the RAS column of `my inventory` say, and not the findings:

- patrol scrub off: memory that is not read is not checked, so a quiet DIMM says less, and the check of the finding that rests on scrubbing stays open;
- CE logging off: the BMC log holds no corrected errors, and its silence says nothing;
- the CE threshold event on: the BMC log has threshold events, fewer than the errors.

## Operating system and BMC side by side

`my inventory` lists the DIMMs of a host one per line, with what each side recorded. The same table is in section 7 of the HTML report.

The names are joined first. The BMC names a slot by `DeviceLocator`, by its service label and by its `Id`; the EDAC driver by its label or `mc0/dimm3`; SMBIOS by its locator. A module of the BMC whose serial number SMBIOS also lists takes the SMBIOS slot, names equal up to separators are one slot, and the other names stay as aliases of the slot.

```
$ my open dell-shaped.mycase inventory
case bundle of 2026-11-13 09:30 UTC, written by my dev
host node-d  Dell Inc. PowerEdge R740  BIOS 2.12.2  BMC 4.40.00.00
4 DIMMs in 4 slots
slot names and module data from the BMC (read 2026-11-13 09:30 UTC)
memory RAS settings of the BIOS in effect for every DIMM: patrol scrub, ADDDC (width not known)

 SLOT  SERIAL    MAKER           PART NUMBER      SIZE  ORG  SPEED      CE OS  CE BMC
 A1    D0A10001  Example Memory  EXM32G29R2X4-B  32 GB  2R   2666/2933      -       2
       DDR4 RDIMM; BMC: DIMM A1, DIMM.Socket.A1
       corrected errors the BMC counts: 1,250 over the life of the module, 3 since the reset of
       2026-10-02 08:15 UTC
       BMC log 2026-10-28 21:04 UTC: corrected-error limit passed
       BMC log 2026-11-05 02:41 UTC: corrected-error limit passed
[...]
 No source of the operating system was read, so there is nothing to set against the logs of the BMC.
```

With the host's records in the same case, the counts are compared over the period both sides cover. `host-bmc.mycase` is a bundle of the synthetic demo host of the tests, made with `--bmc` against the stand-in BMC the tests run (`cmd/my/docs_redfish_test.go` makes both cases this page quotes):

```
$ my open host-bmc.mycase inventory
case bundle of 2026-11-16 03:00 UTC, written by my dev
host r12-n034  Example Systems R760  BIOS 1.4.2  BMC 2.14.0
16 DIMMs; 16 empty slots not listed (--all lists them)
slot names and module data from SMBIOS, EDAC, the BMC at 127.0.0.1 (read 2026-11-16 03:00 UTC)
the settings profile has no mapping for this BIOS, so the RAS column is empty; my bios says more

 SLOT     SERIAL  MAKER           PART NUMBER      SIZE  ORG   SPEED      CE OS  CE BMC
 CPU0_A1  5C331B  Example Memory  EXM64G48R2X4-A  64 GB  2Rx4  4800/4800      0       0
          DDR5 RDIMM; BMC: BMC.Slot.3, DIMM A3, DIMM3; EDAC: CPU_SrcID#0_MC#0_Chan#0_DIMM#0
[...]
 OS and BMC side by side, 2026-11-02 01:00 UTC to 2026-11-13 00:00 UTC. The records of the operating
 system begin 2026-08-03 13:53 UTC, the logs of the BMC 2026-11-02 01:00 UTC.
 The clock of the BMC was 2 minutes ahead of the machine that read it. The times of its logs are as
 the BMC wrote them.
 The lifetime count of the BMC covers the life of a module, and the EDAC counters of the operating
 system start again at every boot: the two are shown and not compared.
   - CPU0_A1: since the reset of 2026-11-10 00:00 UTC the BMC counts 3 corrected errors, the OS has
     0
   - CPU0_C1: the OS has 604 corrected errors, the logs of the BMC 6 entries; 1 of the entries
     stands at the time of an OS record
   - CPU0_C1: since the reset of 2026-11-10 00:00 UTC the BMC counts 1 corrected error, the OS has
     206
   - CPU1_D1: the OS has 4 corrected errors, the logs of the BMC have none
   - CPU1_D1: since the reset of 2026-11-10 00:00 UTC the BMC counts 2 corrected errors, the OS has
     0

 Errors the OS has and the logs of the BMC lack. Candidates for the reason, none established:
   - a threshold or a leaky bucket in the BIOS can keep corrected errors out of the logs of the BMC;
     no such setting of this BIOS is on record

 Counts since the reset that differ. Candidates for the reason, none established:
   - the BMC counts what the firmware corrected; the OS has what the kernel reported to it and its
     sources kept
   - the current counts of the BMC start again when they are cleared (ClearCurrentPeriod), which can
     be later than the reset
```

The operating system side is rasdaemon, the kernel log, the EDAC counters and mcelog. The BMC side is the entries of its logs about corrected errors and, where the BMC keeps them, its own counts per module (`MemoryMetrics`). An entry of the BMC that the ledger takes for a second report of an OS record counts as matching it ([data-model.md](data-model.md#duplicates) says how duplicates are found).

When the sides differ, the comparison lists the candidate reasons the records support. It establishes none of them:

- The BMC records what the BIOS sends it. On servers where the BIOS (its SMM handler) logs corrected errors to the BMC, it logs them by its own rules: Dell's registry above documents `CorrEccSmi` (corrected errors are logged to the SEL) and `CECriticalSEL` (the event of the correctable error threshold is logged). With a threshold or a leaky bucket, the SEL holds one entry for many errors.
- Errors handled by the firmware first reach the OS only as far as the firmware reports them.
- The EDAC counters of the OS start at zero with every boot. The lifetime count of `MemoryMetrics` covers the life of the module, and its current period starts at the last reset of the system or at the last `ClearCurrentPeriod`, a time the schema does not give.
- A log of the BMC that was full, overwritten or cut at the entry limit, and an OS source that was not read, leave records out on their side.

## DIMM labels for rasdaemon

`my export labels` writes the names of the slots as a label file for rasdaemon. Installed in `/etc/ras/dimm_labels.d/` and loaded with `ras-mc-ctl --register-labels`, it makes the kernel log and rasdaemon name a DIMM as SMBIOS or the BMC do. With `--names bmc` the labels are the BMC's service labels.

```
$ my open host-bmc.mycase export labels --names bmc
# DIMM labels for rasdaemon, written by my dev for host r12-n034.
# Install: copy to /etc/ras/dimm_labels.d/ and run "ras-mc-ctl --register-labels".
# "ras-mc-ctl --mainboard" prints the vendor and model this file has to name; "ras-mc-ctl --print-labels" shows what was matched.
# A label is the service label the BMC has for the slot, and the name of the slot in the ledger where the BMC has none. A position is the memory controller and the location of the DIMM in the EDAC driver.
#
# Other names of the slots:
#   DIMM A3: CPU0_A1, BMC.Slot.3, DIMM3
[...]
Vendor: Example Systems
  Product: R760
    DIMM A3: 0.0.0;
    DIMM A4: 0.1.0;
    DIMM A1: 1.0.0;
[...]
    DIMM A16: 7.1.0;
```

A line gives the memory controller and the EDAC location of a DIMM. The file is written under the mainboard vendor and model when the ledger can tell them, and under the product name; `--vendor`, `--model` and `--product` replace them with what `ras-mc-ctl --mainboard` prints on the host. A position gets no label when its name is longer than the 31 bytes the EDAC driver takes, holds a colon, a semicolon or a control character, starts with `#`, or ends in `vendor`, `model`, `board` or `product` (the Perl parser of ras-mc-ctl reads such a line as the start of a block). The file lists those positions in comments with the reason. The files of the tests were also read with the label parsers of rasdaemon itself: `ras-mc-ctl` of v0.8.2 and `util/ras_dimm.py` of its main branch.

## Limits

- No BMC of a real server has been read yet. The client and the collector are tested against stand-in BMCs (`internal/redfish/bmctest`) and once against the DMTF mockup server (below). The vendor-shaped trees in `testdata/redfish` follow public documents and are not captures.
- A BMC that links no `MemoryMetrics` from its modules has no error counts per module; its side of the comparison is its log entries alone. The DMTF `public-rackmount1` mockup is one such service.
- The BIOS settings are checked only for the vendors and attributes in the table above.
- Of several systems behind one BMC, the BIOS settings of the first are read.
- The device width is unknown in a case without the EDAC records of the host.
- A registry the BMC does not hold is not downloaded from its publication URI. Without it, the allowed values of a setting are unknown and the check reports the values as they are.

## Lab check

On 2026-11-16 `my collect redfish` was run against the DMTF Redfish-Mockup-Server 1.3.0 serving its `public-rackmount1` mockup on 127.0.0.1, each time for less than a minute. The server's two modules for events and uploads were replaced by empty stand-ins; the requests of the collector do not reach them.

| Run | Result |
| --- | --- |
| `http://`, `--insecure` | 26 resources, 20 requests, 29.5 kB, exit 0 |
| `https://` with a self-signed certificate for `bmc.lab`, no trust given | refused before any request, the fingerprint printed, exit 70 |
| the same with `--fingerprint` | 26 resources, exit 0 (the certificate names `bmc.lab`, the connection went to 127.0.0.1) |
| the same with `--session` | one POST to log in, one DELETE of that session, GET otherwise, exit 0; neither the password nor the token in the collection |

```text
A Contoso 3500, BMC firmware 1.45.455b66-rev4, BIOS P79 v1.45 (12/06/2017), Redfish 1.15.0: 1 BMC

                       A
service root           ok
system                 ok
memory modules         ok
memory metrics         not offered
BIOS settings          ok
pending BIOS settings  ok
attribute registry     unsupported
manager                ok
log services           ok
log entries            ok
firmware inventory     ok
chassis                ok
```

The mockup links no `MemoryMetrics`, its `Registries` collection has no entry for the BIOS registry its `Bios` names, and its BIOS attributes are about boot, processors and power. Three changes came out of the runs (pull request 116): the mockup answers a login with 204 and the token, which the client now accepts; `my bios` says when the BMC's BIOS settings hold no memory setting; a BMC clock that is days off (the mockup's is 4,266 days behind) is told in days.
