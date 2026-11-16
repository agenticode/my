#!/usr/bin/env python3
"""smbios17.py: SMBIOS memory device structures (type 17) for a QEMU guest.

    smbios17.py OUTDIR LOCATOR,BANK,SERIAL [...] [--size-mib N] [options]

QEMU describes the memory of a guest as one type 17 entry per 16 GiB, and
its "-smbios type=17,..." options set the same strings in every entry: all
DIMMs of the guest get one serial number. A structure loaded with
"-smbios file=PATH" replaces the entries QEMU would make. This program
writes one such structure per DIMM, OUTDIR/dimm0.bin, dimm1.bin, ..., with
the handles QEMU itself gives type 17 entries (0x1100 and up) and the
physical memory array QEMU makes (handle 0x1000). The number of structures
must be the number of entries QEMU would make for the guest's memory.

The layout is that of SMBIOS 3.2 up to the configured voltage (0x28 bytes).
"""
import argparse
import os
import struct
import sys

ARRAY_HANDLE = 0x1000
MEMORY_TYPES = {"DDR4": 0x1A, "DDR5": 0x22}


def structure(handle, locator, bank, serial, a):
    strings = [locator, bank, a.manufacturer, serial, a.asset or "Not Specified", a.part]
    for s in strings:
        if not s or "\0" in s:
            raise ValueError("empty string or NUL in %r" % (strings,))
    if a.size_mib >= 0x7FFF:
        size, extended = 0x7FFF, a.size_mib
    else:
        size, extended = a.size_mib, 0
    width = 72 if a.ecc else 64
    body = struct.pack(
        "<BBHHHHHHBBBBBHHBBBBBIHHHH",
        17, 0x28, handle,
        ARRAY_HANDLE,
        0xFFFE,  # no memory error information structure
        width, 64, size,
        0x09,  # form factor: DIMM
        0,  # not in a set
        1, 2,  # device locator, bank locator
        MEMORY_TYPES[a.type],
        0x2080 if a.registered else 0x0080,  # synchronous, registered (buffered)
        a.speed,
        3, 4, 5, 6,  # manufacturer, serial number, asset tag, part number
        a.ranks & 0x0F,
        extended,
        a.speed,  # configured memory speed
        a.millivolts, a.millivolts, a.millivolts,
    )
    assert len(body) == 0x28, len(body)
    return body + b"".join(s.encode("ascii") + b"\0" for s in strings) + b"\0"


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("outdir")
    ap.add_argument("dimms", nargs="+", metavar="LOCATOR,BANK,SERIAL")
    ap.add_argument("--first-handle", type=lambda s: int(s, 0), default=0x1100)
    ap.add_argument("--size-mib", type=int, default=16384)
    ap.add_argument("--type", choices=sorted(MEMORY_TYPES), default="DDR4")
    ap.add_argument("--registered", action="store_true")
    ap.add_argument("--ecc", action="store_true", help="total width 72 bits for 64 data bits")
    ap.add_argument("--speed", type=int, default=3200, help="MT/s")
    ap.add_argument("--ranks", type=int, default=2)
    ap.add_argument("--millivolts", type=int, default=1200)
    ap.add_argument("--manufacturer", default="Lab Memory")
    ap.add_argument("--part", default="LABDDR4-16G")
    ap.add_argument("--asset")
    a = ap.parse_args()

    os.makedirs(a.outdir, exist_ok=True)
    for i, spec in enumerate(a.dimms):
        parts = spec.split(",")
        if len(parts) != 3:
            ap.error("%r: want LOCATOR,BANK,SERIAL" % spec)
        path = os.path.join(a.outdir, "dimm%d.bin" % i)
        with open(path, "wb") as f:
            f.write(structure(a.first_handle + i, *parts, a))
        print("%s handle 0x%04x %s" % (path, a.first_handle + i, spec))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as err:
        print("smbios17.py: %s" % err, file=sys.stderr)
        sys.exit(1)
