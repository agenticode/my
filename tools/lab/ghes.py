#!/usr/bin/env python3
"""ghes.py: hand a guest a firmware error record about memory.

    ghes.py --qmp SOCKET --id ID [--count N] [--gap SECONDS] [field options]

QEMU builds the ACPI tables of its guests. With "-machine virt,ras=on" on
aarch64 they hold a HEST with a generic hardware error source, and the QMP
command inject-ghes-v2-error (QEMU 10.2) puts a record into that source and
raises its interrupt. The guest kernel then handles the record as it handles
one of real firmware: ghes prints it, ghes_edac reports it through EDAC, and
memory_failure takes the page when the record asks for it.

The record is a Generic Error Status Block with one Generic Error Data Entry
(revision 3.00) and a memory error section (UEFI 2.10, N.2.5). Only the
fields given on the command line are marked valid in the section.

With --address-base the physical address is made up from the location
fields: BASE + ((row * 2 + rank) * 16 + bank_group * 4 + bank) * 8192 +
column * 8, the layout of a 16 GiB DIMM with two ranks of DDR4 (4 bank
groups of 4 banks, 65536 rows, 1024 columns of 8 bytes) placed at BASE
without interleaving. Real controllers spread a DIMM over the address space;
this layout only keeps every place of a DIMM at an address of its own.

--clock-offset is the guest's clock minus this host's clock, in seconds. The
time in the record and in the plan is the guest's: that is the time the
sources of the guest give the error.

One line of JSON is printed per call, in the form plan.py reads.
"""
import argparse
import base64
import datetime
import json
import socket
import struct
import sys
import time

# Section type: platform memory error.
MEM_GUID = bytes([0x14, 0x11, 0xBC, 0xA5, 0x64, 0x6F, 0xDE, 0x4E, 0xB8, 0x63, 0x3E, 0x83, 0xED, 0x7C, 0x83, 0xB1])

SEVERITY = {"recoverable": 0, "fatal": 1, "corrected": 2, "informational": 3}
LEDGER_SEVERITY = {"recoverable": "Recoverable", "fatal": "Fatal", "corrected": "CE", "informational": "Informational"}
ERROR_TYPES = {
    0: "unknown", 2: "single-bit ECC", 3: "multi-bit ECC", 4: "single-symbol chipkill ECC",
    5: "multi-symbol chipkill ECC", 13: "scrub corrected error", 14: "scrub uncorrected error",
}

# Validation bits of the memory error section.
V_STATUS, V_PA, V_PA_MASK, V_NODE, V_CARD, V_MODULE, V_BANK, V_DEVICE, V_ROW, V_COLUMN, V_BIT = [1 << i for i in range(11)]
V_ERROR_TYPE, V_RANK, V_CARD_HANDLE, V_MODULE_HANDLE = 1 << 14, 1 << 15, 1 << 16, 1 << 17
V_ROW_EXT, V_BANK_GROUP, V_BANK_ADDRESS, V_CHIP_ID = 1 << 18, 1 << 19, 1 << 20, 1 << 21


def bcd(v):
    return (v // 10) << 4 | v % 10


def record(a, now):
    valid = 0
    bank = 0
    extended = 0

    def have(value, bit):
        nonlocal valid
        if value is not None:
            valid |= bit
        return value or 0

    status = have(a.status, V_STATUS)
    pa = have(a.address, V_PA)
    mask = have(a.mask, V_PA_MASK)
    node, card, module = have(a.node, V_NODE), have(a.card, V_CARD), have(a.module, V_MODULE)
    device = have(a.device, V_DEVICE)
    column, bit = have(a.column, V_COLUMN), have(a.bit, V_BIT)
    etype, rank = have(a.type, V_ERROR_TYPE), have(a.rank, V_RANK)
    handle = have(a.handle, V_MODULE_HANDLE)
    row = have(a.row, V_ROW)
    if a.row is not None and a.row > 0xFFFF:
        valid |= V_ROW_EXT
        extended |= (a.row >> 16) & 0x3
    if a.bank_group is not None or a.bank_address is not None:
        # DDR4 and later: bank group in the high byte, bank address in the low byte.
        if a.bank_group is not None:
            valid |= V_BANK_GROUP
        if a.bank_address is not None:
            valid |= V_BANK_ADDRESS
        bank = (a.bank_group or 0) << 8 | (a.bank_address or 0)
    elif a.bank is not None:
        valid |= V_BANK
        bank = a.bank
    if a.chip is not None:
        valid |= V_CHIP_ID
        extended |= (a.chip & 0x7) << 5

    section = struct.pack("<QQQQ8HQQQBBHHH", valid, status, pa, mask, node, card, module, bank, device,
                          row & 0xFFFF, column, bit, 0, 0, 0, etype, extended, rank, 0, handle)
    assert len(section) == 80

    sev = SEVERITY[a.severity]
    flags = 0x01  # primary
    if a.threshold:
        flags |= 0x08  # error threshold exceeded: the firmware asks for the page to be retired
    fru_text = (a.fru_text or "").encode("ascii")[:19].ljust(20, b"\0")
    stamp = bytes([bcd(now.second), bcd(now.minute), bcd(now.hour), 0x01, bcd(now.day), bcd(now.month),
                   bcd(now.year % 100), bcd(now.year // 100)])
    entry_valid = 0x04 | (0x02 if a.fru_text else 0)
    entry = MEM_GUID + struct.pack("<IHBBI", sev, 0x300, entry_valid, flags, len(section)) + bytes(16) + fru_text + stamp
    assert len(entry) == 72

    # Block status: bit 0 uncorrectable, bit 1 correctable, bits 4..13 the number of entries.
    block_status = (0x2 if a.severity in ("corrected", "informational") else 0x1) | 1 << 4
    block = struct.pack("<IIIII", block_status, 0, 0, len(entry) + len(section), sev)
    return block + entry + section


class QMP:
    def __init__(self, path):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.connect(path)
        self.file = self.sock.makefile("rw")
        self.read()
        self.command("qmp_capabilities")

    def read(self):
        while True:
            line = self.file.readline()
            if not line:
                raise RuntimeError("QMP connection closed")
            msg = json.loads(line)
            if "event" not in msg:
                return msg

    def command(self, name, **arguments):
        cmd = {"execute": name}
        if arguments:
            cmd["arguments"] = arguments
        self.file.write(json.dumps(cmd) + "\n")
        self.file.flush()
        reply = self.read()
        if "error" in reply:
            raise RuntimeError("%s: %s" % (name, reply["error"].get("desc", reply["error"])))
        return reply.get("return")


def number(text):
    return int(text, 0)


def linear(a):
    """The address of the location of a at a.address_base (see the module text)."""
    need = {"rank": a.rank, "bank-group": a.bank_group, "bank-address": a.bank_address, "row": a.row, "column": a.column}
    missing = [k for k, v in need.items() if v is None]
    if missing:
        raise ValueError("--address-base needs --" + ", --".join(missing))
    if not (0 <= a.rank < 2 and 0 <= a.bank_group < 4 and 0 <= a.bank_address < 4 and 0 <= a.row < 1 << 16 and 0 <= a.column < 1 << 10):
        raise ValueError("location outside a 16 GiB DIMM of two ranks")
    return a.address_base + ((a.row * 2 + a.rank) * 16 + a.bank_group * 4 + a.bank_address) * 8192 + a.column * 8


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--qmp", required=True, help="QMP socket of the guest's QEMU")
    ap.add_argument("--id", required=True)
    ap.add_argument("--count", type=int, default=1)
    ap.add_argument("--gap", type=float, default=0.0, help="seconds between records")
    ap.add_argument("--severity", choices=sorted(SEVERITY), default="corrected")
    ap.add_argument("--threshold", action="store_true", help="set the flag \"error threshold exceeded\"")
    ap.add_argument("--type", type=number, help="memory error type: 2 single-bit ECC, 3 multi-bit ECC, 13 scrub corrected, 14 scrub uncorrected")
    ap.add_argument("--status", type=number)
    ap.add_argument("--address", type=number)
    ap.add_argument("--address-base", type=number, help="make the address up from the location, from this base")
    ap.add_argument("--clock-offset", type=float, default=0.0, help="seconds the guest's clock is ahead of this host's")
    ap.add_argument("--mask", type=number)
    for name in ("node", "card", "module", "rank", "bank", "bank-group", "bank-address", "device", "chip", "row", "column", "bit", "handle"):
        ap.add_argument("--" + name, type=number)
    ap.add_argument("--fru-text")
    ap.add_argument("--label", help="the DIMM the record should end up on, for the plan")
    ap.add_argument("--note", default="")
    ap.add_argument("--dump", action="store_true", help="print the record in hexadecimal and send nothing")
    a = ap.parse_args()
    if a.address_base is not None:
        if a.address is not None:
            ap.error("--address and --address-base exclude each other")
        a.address = linear(a)

    now = datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(seconds=a.clock_offset)
    data = record(a, now)
    if a.dump:
        print(data.hex())
        return
    qmp = QMP(a.qmp)
    cper = base64.b64encode(data).decode("ascii")
    for i in range(a.count):
        if i:
            time.sleep(a.gap)
        qmp.command("inject-ghes-v2-error", cper=cper)

    out = {"id": a.id, "time": now.strftime("%Y-%m-%dT%H:%M:%S.") + "%03dZ" % (now.microsecond // 1000),
           "inject": "error", "severity": LEDGER_SEVERITY[a.severity], "count": a.count}
    if a.type in (13, 14):
        out["scrub"] = "Patrol"
    if a.address is not None:
        out["address"] = hex(a.address)
    for key, value in (("socket", a.node), ("channel", a.card), ("slot", a.module), ("rank", a.rank),
                       ("device", a.device), ("bank_group", a.bank_group),
                       ("bank", a.bank_address if a.bank_address is not None else a.bank),
                       ("row", a.row), ("column", a.column), ("bit", a.bit)):
        if value is not None:
            out[key] = value
    if a.label:
        out["label"] = a.label
    note = "firmware error record (CPER), memory error section"
    if a.type is not None:
        note += ", type %d (%s)" % (a.type, ERROR_TYPES.get(a.type, "other"))
    if a.threshold:
        note += ", flag \"error threshold exceeded\""
    if a.note:
        note += "; " + a.note
    out["note"] = note
    print(json.dumps(out))


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError, ValueError) as err:
        print("ghes.py: %s" % err, file=sys.stderr)
        sys.exit(1)
