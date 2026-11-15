# 7.3.0-mce-fatal

One uncorrected memory error with the processor context corrupt. The kernel panics, so nothing reaches rasdaemon or a log file: the console is the only place that says what happened.

| | |
| --- | --- |
| Host | the nested guest of `7.3.0-mce` |
| Kernel | 7.3.0-ras |
| Injected | `tools/lab/mce.sh fatal`: a machine check exception, bank 7, status `be00000000010090`, address `0x12345040` |
| Captured | 2026-11-15 10:44:56 UTC |

`console.log` holds the kernel's lines as the serial console of the guest received them, from the boot banner to the panic. A serial console server or the console log of a BMC keeps the same. The last lines:

```
mce: [Hardware Error]: CPU 0: Machine Check Exception: 4 Bank 7: be00000000010090
mce: [Hardware Error]: TSC ... ADDR 12345040 MISC 86 
mce: [Hardware Error]: PROCESSOR 0:50654 TIME ... SOCKET 0 APIC 0 microcode 1
mce: [Hardware Error]: Run the above through 'mcelog --ascii'
mce: [Hardware Error]: Machine check: Processor context corrupt
Kernel panic - not syncing: Fatal machine check
```

`my import console.log` reads one fatal error at that address, dated by the `TIME` field of the record.

The directory holds no database and no `my/` output: the guest was gone before either could be written. On real hardware the record stays in the bank across a warm reset and the next boot logs it again; the injector keeps its records in memory, so that part is not in this capture.
