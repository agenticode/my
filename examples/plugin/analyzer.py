#!/usr/bin/env python3
"""Example vendor analyzer for MemorY (contract my-analyzer/1).

Reads one request from standard input and writes one response to standard
output. It keeps nothing between two runs.

    my dimm CPU0_C1 --plugin examples/plugin/analyzer.py
    my report case.mycase -o report.html --audience fa --plugin examples/plugin/analyzer.py

This program knows no real device. It shows the three things an analyzer
can answer, with rules that are made up and say so:

  Level2        lines for the failure analysis report, here counted from
                the records: the rows and the data lines with errors
  AdjacentRows  which rows lie next to each other inside the device, here
                "rows 2k and 2k+1 are a pair, and a row has no other
                neighbour"
  Baseline      where the error count of the DIMM stands among DIMMs of
                its part number, here from a table of three steps

docs/plugin-contract.md describes the contract.
"""

import json
import sys

CONTRACT = "my-analyzer/1"
NAME = "example-analyzer"
VERSION = "1.0"
# How many records before the newest one this analyzer looks at.
PREVIOUS_LOGS = 1000


def fail(message):
    """A failure is one line on standard error and a non-zero exit."""
    print(message, file=sys.stderr)
    sys.exit(1)


def place(record):
    """The bank of a record, as far as the platform reports it."""
    return tuple(record.get(k) for k in ("Rank", "DeviceId", "BankGroup", "Bank"))


def analyse(dimm):
    records = list(dimm.get("PreviousLogs") or []) + [dimm["NewLog"]]
    result = {"DimmId": dimm["DimmId"]}

    # Level 2: what a report for failure analysis shows. A real analyzer
    # would name bit lines and the cells the on-die ECC miscorrected; this
    # one counts what the records say.
    rows, vectors, uncorrected = {}, {}, 0
    for r in records:
        n = r.get("count") or 1
        if r.get("Row") is not None:
            key = place(r) + (r["Row"],)
            rows[key] = rows.get(key, 0) + n
        if r.get("ErrorVector"):
            vectors[r["ErrorVector"]] = vectors.get(r["ErrorVector"], 0) + n
        if r.get("ErrorSeverity") in ("Recoverable", "UE", "Fatal"):
            uncorrected += n
    lines = [{"Label": "records analysed", "Value": "%d of %d" % (len(records), len(records) + dimm.get("EarlierLogs", 0))}]
    if rows:
        top = sorted(rows.items(), key=lambda kv: (-kv[1], kv[0][-1]))[:3]
        lines.append({"Label": "rows with most errors",
                      "Value": ", ".join("row 0x%x: %d" % (key[-1], n) for key, n in top)})
        lines.append({"Label": "row pairs involved", "Value": str(len({key[:-1] + (key[-1] >> 1,) for key in rows}))})
    if vectors:
        top = sorted(vectors.items(), key=lambda kv: (-kv[1], kv[0]))[:4]
        lines.append({"Label": "data lines", "Value": ", ".join("%s: %d" % kv for kv in top)})
    if uncorrected:
        lines.append({"Label": "uncorrected errors in these records", "Value": str(uncorrected)})
    result["Level2"] = {
        "Lines": lines,
        "Notes": ["Example values: this analyzer knows no real device and counts what the records say."],
    }

    # Adjacency: every row with errors and its partner, each naming the
    # other, so that the two answers agree.
    adjacent, seen = [], set()
    for key in sorted(rows, key=lambda k: tuple(-1 if v is None else v for v in k)):
        for row in (key[-1], key[-1] ^ 1):
            if key[:-1] + (row,) in seen:
                continue
            seen.add(key[:-1] + (row,))
            entry = {"Row": row, "Neighbors": [row ^ 1]}
            for name, value in zip(("Rank", "DeviceId", "BankGroup", "Bank"), key[:-1]):
                if value is not None:
                    entry[name] = value
            adjacent.append(entry)
    if adjacent:
        result["AdjacentRows"] = adjacent

    # Baseline: a made-up reference for every part number.
    errors = dimm.get("Corrected", 0) + dimm.get("Uncorrected", 0)
    result["Baseline"] = {"Percentile": 50.0 if errors <= 2 else 80.0 if errors <= 100 else 99.0}
    return result


def main():
    try:
        request = json.load(sys.stdin)
    except ValueError as err:
        fail("the request is not JSON: %s" % err)
    if not isinstance(request, dict) or request.get("Contract") != CONTRACT:
        fail("this analyzer speaks %s, the request names %r" % (CONTRACT, request.get("Contract") if isinstance(request, dict) else None))
    response = {
        "Contract": CONTRACT,
        "Analyzer": {"Name": NAME, "Version": VERSION, "Vendor": "Example Memory", "PreviousLogs": PREVIOUS_LOGS},
        "Dimms": [analyse(d) for d in request.get("Dimms") or []],
    }
    json.dump(response, sys.stdout, indent=1)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
