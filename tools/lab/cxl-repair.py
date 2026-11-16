#!/usr/bin/env python3
"""cxl-repair.py: errors at one row of two emulated CXL memory devices, before
and after the guest repairs that row.

    cxl-repair.py --qmp SOCKET --phase before|after

QEMU 11.1 emulates the post package repair (PPR) and memory sparing
maintenance operations of a CXL type 3 device. A DRAM event record whose
flags ask for maintenance puts its address (DPA) on the device's list of
places to repair; a soft PPR of that address in the guest
(/sys/bus/edac/devices/cxl_memN/mem_repairX) takes it off the list, and the
device then adds a memory sparing event record with the place to its
informational log. QEMU writes that record with the maintenance class of
sparing and the subclass of cacheline sparing (hw/cxl/cxl-mailbox-utils.c,
cxl_perform_ppr).

  before  6 corrected DRAM errors on each device, all in one row, each asking
          for a soft PPR:
            repaired-quiet (serial 0x4c41420011): rank 0, bank group 1, bank 2,
                           row 0x1f40, channel 0, three columns
            repaired-again (serial 0x4c41420012): rank 1, bank group 3, bank 0,
                           row 0x0a7c, channel 1, three columns
  after   3 corrected DRAM errors in the repaired row of the second device;
          the first device stays quiet

One line of JSON is printed per injection, in the form plan.py reads. The
repairs themselves are made inside the guest by cxl-repair.sh.
"""
import argparse
import datetime
import json
import socket
import sys
import time


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
        self.file.write(json.dumps({"execute": name, "arguments": arguments}) + "\n")
        self.file.flush()
        reply = self.read()
        if "error" in reply:
            raise RuntimeError("%s: %s" % (name, reply["error"].get("desc", reply["error"])))
        return reply.get("return")


def now():
    t = datetime.datetime.now(datetime.timezone.utc)
    return t.strftime("%Y-%m-%dT%H:%M:%S.") + "%03dZ" % (t.microsecond // 1000)


# Event record flags of the common header (CXL r3.2 table 8-55): severity
# "warning" in bits 0-1, bit 3 maintenance needed.
WARNING, MAINT_NEEDED = 1, 1 << 3
# The maintenance the device asks for: class 1 post package repair,
# subclass 0 soft PPR.
PPR, SOFT_PPR = 1, 0
CORRECTED = 0  # memory event descriptor without the uncorrectable bit
ECC, HOST_READ = 0, 1  # media ECC error, found by a host read

# The two devices: the QEMU id, the serial number and the row each one fails in.
DEVICES = {
    "quiet": {"qom": "cxl-vmem0", "serial": "0x4c41420011", "channel": 0, "rank": 0, "bank_group": 1, "bank": 2, "row": 0x1F40,
              "columns": [0x080, 0x1C0, 0x080, 0x300, 0x1C0, 0x080], "nibble": 0x2},
    "again": {"qom": "cxl-vmem1", "serial": "0x4c41420012", "channel": 1, "rank": 1, "bank_group": 3, "bank": 0, "row": 0x0A7C,
              "columns": [0x040, 0x200, 0x040, 0x3C0, 0x200, 0x040], "nibble": 0x10},
}
AFTER = {"again": [0x200, 0x2C0, 0x040]}
BASE = 0x1000000  # the DPA of column 0 of the failing row


def dpa(column):
    return BASE + column * 0x40


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--qmp", required=True)
    ap.add_argument("--phase", required=True, choices=["before", "after"])
    ap.add_argument("--gap", type=float, default=1.0)
    a = ap.parse_args()
    qmp = QMP(a.qmp)

    def inject(key, n, column):
        d = DEVICES[key]
        t = now()
        qmp.command("cxl-inject-dram-event", path="/machine/peripheral/" + d["qom"], log="warning", flags=WARNING | MAINT_NEEDED,
                    **{"maint-op-class": PPR, "maint-op-subclass": SOFT_PPR, "dpa": dpa(column), "descriptor": CORRECTED, "type": ECC,
                       "transaction-type": HOST_READ, "channel": d["channel"], "rank": d["rank"], "nibble-mask": d["nibble"],
                       "bank-group": d["bank_group"], "bank": d["bank"], "row": d["row"], "column": column, "sub-type": 0})
        print(json.dumps(dict(id="%s-%s-%d" % (key, a.phase, n), time=t, inject="error", severity="CE", count=1, dpa=hex(dpa(column)),
                              device_name="serial " + d["serial"], channel=d["channel"], rank=d["rank"], bank_group=d["bank_group"],
                              bank=d["bank"], row=d["row"], column=column,
                              note="CXL DRAM event record, media ECC error, maintenance needed: soft PPR")), flush=True)
        time.sleep(a.gap)

    if a.phase == "before":
        cols = {k: d["columns"] for k, d in DEVICES.items()}
    else:
        cols = AFTER
    # The two devices take turns, as two failing modules of one host would.
    longest = max(len(c) for c in cols.values())
    for i in range(longest):
        for key in DEVICES:
            if key in cols and i < len(cols[key]):
                inject(key, i + 1, cols[key][i])


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError) as err:
        print("cxl-repair.py: %s" % err, file=sys.stderr)
        sys.exit(1)
