# Comparing two cases

`my diff` sets two cases of one host side by side: the host before something was done to it, and the same host some time later. It is made for what is done to a host with memory errors before a DIMM is returned: a reseat, a swap of two DIMMs between slots, a firmware update. A replacement reads the same way.

```
my bundle -o before.mycase     # on the host, before the change
                               # power off, swap the DIMMs, power on, let the host run
my bundle -o after.mycase
my diff before.mycase after.mycase
```

Both files are case bundles or ledger files, as `my open` takes them. Each is analysed on its own. Nothing is merged, no file is changed, and the machine the command runs on is not scanned.

## An example

A synthetic host with a row fault in the DIMM of slot CPU0_C1. The first case is a scan of 2026-09-28. At the restart of 2026-10-01 the DIMMs of CPU0_C1 and CPU0_D1 were swapped. The second case is a scan of 2026-11-13.

```
$ my diff before.db after.db
host synth-moved-dimm  before.db 2026-09-28 12:00 UTC  → after.db 2026-11-13 00:00 UTC
45 days apart  host finding: consider replacement
After counts from 2026-10-01 03:00 UTC, the last restart before the later case was taken: 42 days.

 changes  moved    S/N 880018, CPU0_C1 → CPU0_D1
          moved    S/N E7A387, CPU0_D1 → CPU0_C1
          restart  1 after the earlier case: 2026-10-01 03:00 UTC

 DIMM        SLOT               BEFORE  LAST 41D  BETWEEN  AFTER 42D  FINDING
 S/N 880018  CPU0_C1 → CPU0_D1  114 CE    114 CE     5 CE     121 CE  consider replacement
 S/N E7A387  CPU0_D1 → CPU0_C1       0         0        0          0  ok → observe
 S/N 01EAE2  CPU1_A1              1 CE      1 CE        0          0  observe
 S/N D51D2B  CPU1_B1              1 CE      1 CE        0          0  observe
 S/N 332238  CPU1_C1                 0         0        0       1 CE  ok → observe
 S/N A8A64B  CPU1_D1              1 CE      1 CE        0          0  observe
 S/N 3056DB  CPU1_F1              1 CE      1 CE        0          0  observe
 9 DIMMs in the same slot and without errors in both cases are not listed; --all lists them

 reading  S/N 880018: the errors moved with the DIMM. 114 in CPU0_C1 in the 41 days before, 121 in
          CPU0_D1 in the 42 days after; CPU0_C1 has 0 with S/N E7A387 in it.

 notes    5 errors lie after the earlier case and before 2026-10-01 03:00 UTC (CPU0_C1 5), in
          neither window. Until then the DIMMs were where the earlier case has them, so they are
          counted apart for the DIMMs of that case.
          CPU0_C1: the later case counts 114 errors from before the earlier case was taken as errors
          of S/N E7A387, the only DIMM it has seen in that slot. The earlier case has S/N 880018
          there. The finding of the later case for S/N E7A387 (observe) rests on them too.
          The earlier case covers 41 days before it was taken, less than the 42 days after.
```

The DIMM had 114 errors in the 41 days before the first case and has 121 in the 42 days since the restart, in its new slot. The slot it left has none with the other DIMM in it.

The same swap on a host whose errors come from the slot, in Korean:

```
$ my diff before.db after.db --lang ko
호스트 synth-system-slot  before.db 2026-09-28 12:00 UTC  → after.db 2026-11-13 00:00 UTC  간격 45일
호스트 소견: 교체 고려 → 교체 보류
이후 기간은 2026-10-01 03:00 UTC부터: 뒤 케이스 직전의 마지막 재시작. 길이 42일.

 변경  이동    S/N E7A387, CPU0_D1 → CPU0_C1
       이동    S/N 880018, CPU0_C1 → CPU0_D1
       재시작  앞 케이스 이후 1회: 2026-10-01 03:00 UTC

 DIMM        슬롯               이전 전체  직전 42일  사이  이후 42일  소견
 S/N E7A387  CPU0_D1 → CPU0_C1          0          0     0     121 CE  정상 → 교체 보류
 S/N 880018  CPU0_C1 → CPU0_D1     114 CE     114 CE  5 CE          0  교체 고려 → 교체 보류
 S/N D51D2B  CPU1_B1                 1 CE       1 CE     0          0  관찰
 S/N 3056DB  CPU1_F1                 1 CE          0     0          0  관찰
 슬롯이 그대로이고 두 케이스 모두 오류가 없는 DIMM 12개는 생략. --all을 주면 모두 나열

 판독  S/N 880018: 오류가 슬롯 CPU0_C1에 남음. 이전 42일 동안 이 DIMM으로 114건, 이후 42일 동안 S/N
       E7A387 장착 상태로 121건. 이 DIMM은 CPU0_D1에서 0건.

 참고  앞 케이스 뒤부터 2026-10-01 03:00 UTC 전까지의 오류 5건(CPU0_C1 5)은 직전에도 이후에도 넣지
       않고 따로 셈. 그때까지 DIMM은 앞 케이스의 슬롯에 있었으므로 그 케이스의 DIMM으로 셈.
```

## What counts as before and after

Before is what the earlier case holds. BEFORE counts every error it has for a DIMM. LAST counts the part that lies in a window at the end of the earlier case, as long as the period after, so that two numbers of the same length of time stand side by side.

After is counted in the later case, from a start that lies after the change for certain:

| Start of the period after | When |
| --- | --- |
| the last restart before the later case was taken | the later case records a restart after the earlier case. A DIMM changes its slot, and a BIOS its version, across a restart only |
| the time given with `--at` | the reader knows when the change was made, for example on a host that was restarted more than once since |
| the time of the earlier case | the later case records no restart after the earlier case |

Errors that lie after the earlier case and before that start are in neither window. With one restart between the cases, or with `--at`, the DIMMs were where the earlier case has them until then: the errors get a column of their own, BETWEEN, on the line of the DIMM that case has in the slot. With several restarts nothing says at which one a DIMM was moved. The errors are then named per slot in a note and stand in no column.

## How the DIMMs of the two cases are paired

1. By the identity the ledger gives a module: manufacturer, part number and serial number.
2. By the serial number, where the two cases read different things about a module.
3. By the slot, where one of the cases has no serial number for what is in it. A note names these slots: another DIMM in such a slot would not show.

Two serial numbers in one slot are two DIMMs: one removed, one added. Host names are not compared. Bundles written with `--anonymize` name the host by another word each, and the serial numbers of the DIMMs stay in them.

## Changes

| Line | Meaning |
| --- | --- |
| moved | a DIMM that is in another slot in the later case |
| removed | a DIMM of the earlier case that is not in the host in the later one |
| added | a DIMM the earlier case does not have |
| BIOS, BMC, kernel | the versions the boot of each case recorded, when both are known and differ |
| restart | the boots of the later case that began after the earlier case |
| recorded | repairs, pages taken offline and firmware marks the later case recorded after the earlier case |

A reseat leaves no record on a host: the same DIMM is in the same slot. The comparison shows the restart, the errors before and the errors after.

## Readings

One line for each DIMM with at least five errors in the earlier case (`rule.system.min_errors`). "Many" means at least that number.

| Reading | Counts behind it |
| --- | --- |
| the errors moved with the DIMM | a DIMM that moved has many errors after, and the slot it left has fewer than five |
| the errors stayed with the slot | the slot has many errors after with the DIMM that came, and the DIMM that left has fewer than five where it is now, or is gone |
| errors in both places | many on both sides |
| the errors go on | a DIMM in the same slot has many errors after |
| no errors after | none after, many in the window before |
| quiet before the change already | fewer than five in the window before, so the time after says little |
| too few to say | some errors after, fewer than five |

A reading states counts. The finding of each case is the grade the engine gives it, in the FINDING column: the grade of the earlier case and, when it differs, the grade of the later one.

## What a later scan gets wrong on its own

A scan knows where the DIMMs are while it runs. Errors a rasdaemon database kept from before a swap carry their slot, and the scan ties them to the DIMM it sees in that slot now. In the first example the later case counts the 114 errors of CPU0_C1 for S/N E7A387, which sat in CPU0_D1 when they were recorded, and `my open after.db` grades that DIMM on them. The comparison names such errors in a note, together with the DIMM the earlier case has in the slot.

A ledger that is kept from scan to scan (`my --ledger FILE`) records where the DIMMs were at every scan. It ties each error to the DIMM of its time, and rules R-SYS-02 and R-SYS-03 then speak about a swap or a reseat in one finding.

## JSON

`--json` prints the envelope of the other commands with `"view": "diff"`. `data` holds `before` and `after` (file, time, grade, kernel, BIOS, BMC), `since` and `since_by` (`restart`, `given` or `case`), `after_days` and `before_days`, `restarts`, `changes`, `dimms` (per line `before`, `recent`, `between`, `after`, the two grades and `inherited`), `between` per slot, and `readings` and `notes`, each with a code, its arguments and the sentence.

## Exit code

The grade of the host in the later case, 0 to 4, as `my open AFTER` gives it. 64 for a wrong command line: fewer or more than two files, the later case named first, an `--at` that does not lie between the cases. 70 when a file cannot be read.

## Known gaps

- The command is tested on pairs of synthetic hosts. It has not been run on two cases of a real host.
- A reseat that no source recorded cannot be told from no change.
- A counter reading (EDAC, mcelog) counts at the time it was read. With `--at`, a reading taken after that time counts as after with every error of its window, also those from before.
- Errors of the later case that carry no date are on neither side. A note counts them.
- GPU memory is not compared.
- The two findings come from two runs of the engine. No finding is computed over both cases.
