#!/usr/bin/env python3
"""plan.py: write an injection plan from what the injectors printed.

    plan.py --scenario NAME --summary TEXT --method TEXT [--set KEY=VALUE]... < injected.jsonl > plan.yaml

Every line of the input is one JSON object, as hwpoison, mce.sh, cxl.py and
ghes.py print them. The plan lists what was put into the host, where and how
many times, in the form "my verify --expect" compares with what the sources
recorded (see docs/validation-lab.md for the fields).
"""
import argparse
import json
import sys

# Keys of an injection in the order they are written. Anything else an
# injector printed is left out of the plan.
ORDER = [
    "id", "time", "inject", "how", "severity", "scrub", "count", "address", "dpa", "device_name",
    "socket", "channel", "slot", "rank", "device", "bank_group", "bank", "row", "column", "bit",
    "label", "note",
]
HEX = {"address", "dpa", "row", "column"}


def scalar(key, value):
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return hex(value) if key in HEX else str(value)
    text = str(value)
    plain = text and all(c.isalnum() or c in "-_.:/+" for c in text) and not text[0] in "-:" and ": " not in text
    if plain and not text.endswith(":"):
        return text
    return '"' + text.replace("\\", "\\\\").replace('"', '\\"') + '"'


def from_hwpoison(rec):
    """Turns a line of hwpoison into the fields of the plan."""
    kind = rec.get("kind")
    notes = {
        "anon": "anonymous page that was written, madvise(MADV_HWPOISON)",
        "mlocked": "anonymous page that was written and locked, madvise(MADV_HWPOISON)",
        "file": "page of a file that was read, madvise(MADV_HWPOISON)",
        "soft": "anonymous page that was written, madvise(MADV_SOFT_OFFLINE)",
    }
    if kind not in notes:
        return rec
    out = {
        "id": rec["id"], "time": rec["time"], "inject": "page-offline",
        "how": "soft" if kind == "soft" else "hard", "count": 1,
        "address": int(rec["address"], 16), "note": notes[kind],
    }
    if rec.get("signal"):
        out["note"] += "; the process got %s %s" % (rec["signal"], rec.get("si_code", ""))
    if rec.get("error"):
        out["note"] += "; madvise failed: " + rec["error"]
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--scenario", required=True)
    ap.add_argument("--summary", required=True)
    ap.add_argument("--method", required=True)
    ap.add_argument("--set", action="append", default=[], metavar="KEY=VALUE")
    args = ap.parse_args()

    head = [("format", 1), ("scenario", args.scenario), ("summary", args.summary), ("method", args.method)]
    for kv in args.set:
        k, _, v = kv.partition("=")
        head.append((k, v))

    out = ["# Injection plan: what was put into the host, where and how many times."]
    for k, v in head:
        out.append("%s: %s" % (k, scalar(k, v)))
    out.append("injections:")
    n = 0
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        rec = from_hwpoison(json.loads(line))
        if "inject" not in rec:
            continue  # a remark of an injector, not an injection
        n += 1
        first = True
        for key in ORDER:
            if key not in rec or rec[key] is None:
                continue
            value = rec[key]
            if key in HEX and isinstance(value, str):
                value = int(value, 16)
            out.append("%s%s: %s" % ("  - " if first else "    ", key, scalar(key, value)))
            first = False
    if n == 0:
        out[-1] = "injections: []"
    print("\n".join(out))


if __name__ == "__main__":
    main()
