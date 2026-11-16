# erst-fedora44

Three pstore files of one panic, as a real kernel wrote them into the ACPI error record store (ERST) and the next boot showed them.

| | |
| --- | --- |
| Guest | x86_64, QEMU 10.2.2 without hardware virtualization, `-cpu Skylake-Server`, `-device acpi-erst` with a file behind it (`tools/lab/loop.sh`) |
| System | Fedora 44 cloud image, kernel 6.19.10-300.fc44, systemd 259.5, kernel parameters `mce=print_all console=ttyS0,115200` |
| Injected | QEMU monitor `mce 0 7 0xbe00000000010090 0x4 0x12345040 0x86` (CPU 0 only), 2026-11-16 02:34:53 UTC |
| What the kernel did | "Kernel panic - not syncing: Timeout: Not all CPUs entered broadcast exception handler" 337.8 s after boot, before it printed the record; the dump holds no machine check record. The next boot printed the record it found in bank 7 |
| Where the files were | `/var/lib/systemd/pstore/`, at its top: systemd-pstore 259 moved them out of `/sys/fs/pstore` without a directory per dump and without a `dmesg.txt`, and kept their times (02:35 UTC) |

Changed for the repository: the UUID of the root file system and the serial of the EFI system partition, which belong to the Fedora cloud image, are replaced by `00000000-0000-4000-8000-000000000001` and `0000-0001`. Nothing else was changed. The file times are not kept by git.
