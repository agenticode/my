#!/usr/bin/env python3
"""cxl.py: put event records into the event log of an emulated CXL memory device.

    cxl.py --qmp SOCKET [--device ID]

QEMU emulates a CXL type 3 memory device, and its QMP commands
cxl-inject-dram-event, cxl-inject-general-media-event,
cxl-inject-memory-module-event and cxl-inject-poison add records to the
device as its own firmware would. The guest kernel reads them through the
CXL mailbox and traces them; rasdaemon stores the traces.

What is injected, with addresses inside the device (DPA):

  dram-1..8   8 corrected DRAM errors in one row (rank 1, bank group 2, bank 1,
              row 0x2b3c), on six columns, 2 of them found by the scrubber
  dram-ue     1 uncorrected DRAM error in the same row
  media-1..3  3 corrected media errors without a DRAM location (channel 1, rank 0)
  module      1 health record: maintenance needed, hardware replacement needed
  poison      2 ranges of the poison list

One line of JSON is printed per injection, in the form plan.py reads.
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


# Memory event descriptor: bit 0 uncorrectable, bit 1 threshold, bit 2 poison list.
CORRECTED, UNCORRECTED = 0, 1
# DRAM event types and transaction types of the CXL specification.
ECC, SCRUB_ECC = 0, 1
HOST_READ, MEDIA_SCRUB = 1, 5


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--qmp", required=True)
    ap.add_argument("--device", default="cxl-vmem0", help="the QEMU id of the device")
    ap.add_argument("--memdev", default="mem0", help="the name the guest kernel gives the device")
    ap.add_argument("--gap", type=float, default=1.0)
    a = ap.parse_args()
    qmp = QMP(a.qmp)
    path = "/machine/peripheral/" + a.device

    def out(**rec):
        print(json.dumps(rec), flush=True)
        time.sleep(a.gap)

    place = {"rank": 1, "bank-group": 2, "bank": 1, "row": 0x2B3C}
    cells = [(0x010, ECC, HOST_READ), (0x1A8, ECC, HOST_READ), (0x2C0, ECC, HOST_READ), (0x010, ECC, HOST_READ),
             (0x3F8, SCRUB_ECC, MEDIA_SCRUB), (0x1A8, ECC, HOST_READ), (0x0E0, SCRUB_ECC, MEDIA_SCRUB), (0x2C0, ECC, HOST_READ)]
    for i, (column, etype, trans) in enumerate(cells, 1):
        dpa = 0x1000000 + column * 0x40
        t = now()
        qmp.command("cxl-inject-dram-event", path=path, log="informational", flags=1, dpa=dpa, descriptor=CORRECTED,
                    type=etype, **{"transaction-type": trans, "channel": 1, "nibble-mask": 0x4, "column": column}, **place)
        rec = dict(id="dram-%d" % i, time=t, inject="error", severity="CE", count=1, dpa=hex(dpa), device_name=a.memdev,
                   channel=1, rank=1, bank_group=2, bank=1, row=0x2B3C, column=column,
                   note="CXL DRAM event record, %s" % ("scrub media ECC error" if etype == SCRUB_ECC else "media ECC error"))
        if etype == SCRUB_ECC:
            rec["scrub"] = "Patrol"
        out(**rec)

    t = now()
    qmp.command("cxl-inject-dram-event", path=path, log="failure", flags=1, dpa=0x1000000 + 0x2C0 * 0x40 + 0x8, descriptor=UNCORRECTED,
                type=ECC, **{"transaction-type": HOST_READ, "channel": 1, "nibble-mask": 0xC, "column": 0x2C0}, **place)
    out(id="dram-ue", time=t, inject="error", severity="UE", count=1, dpa=hex(0x1000000 + 0x2C0 * 0x40 + 0x8), device_name=a.memdev,
        channel=1, rank=1, bank_group=2, bank=1, row=0x2B3C, column=0x2C0, note="CXL DRAM event record, uncorrectable, media ECC error")

    for i in range(1, 4):
        dpa = 0x2000000 + i * 0x1000
        t = now()
        qmp.command("cxl-inject-general-media-event", path=path, log="informational", flags=1, dpa=dpa, descriptor=CORRECTED,
                    type=0, **{"transaction-type": HOST_READ, "channel": 1, "rank": 0, "device": 0x20})
        out(id="media-%d" % i, time=t, inject="error", severity="CE", count=1, dpa=hex(dpa), device_name=a.memdev, channel=1, rank=0,
            note="CXL general media event record, media ECC error")

    t = now()
    qmp.command("cxl-inject-memory-module-event", path=path, log="warning", flags=1, type=0,
                **{"health-status": 0x5, "media-status": 0, "additional-status": 0, "life-used": 42, "temperature": 51,
                   "dirty-shutdown-count": 3, "corrected-volatile-error-count": 9, "corrected-persistent-error-count": 0})
    out(id="module", time=t, note="memory module event: health status change, maintenance needed and hardware replacement needed, "
        "life used 42 %, 51 degrees, 9 corrected volatile errors")

    for i, start in enumerate((0x3000000, 0x3004000), 1):
        t = now()
        qmp.command("cxl-inject-poison", path=path, start=start, length=128)
        out(id="poison-%d" % i, time=t, note="poison list entry at %s, 128 bytes" % hex(start))


if __name__ == "__main__":
    try:
        main()
    except (OSError, RuntimeError) as err:
        print("cxl.py: %s" % err, file=sys.stderr)
        sys.exit(1)
