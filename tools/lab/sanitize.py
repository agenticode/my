#!/usr/bin/env python3
"""sanitize.py SRC DEST [--host NAME] [--replace OLD=NEW]...

Copies a capture (see capture.sh) and takes out what names the machine it was
taken on: host name, machine ID, serial numbers and UUIDs of the system and
its file systems, hardware addresses and network addresses. Each value is
replaced by the same stand-in wherever it occurs, so records that belong
together still do.

The values come from SRC/private/ident.txt, which capture.sh writes on the
host. private/ is not copied. A binary file (the rasdaemon database, the
case bundle) is copied as it is and must hold none of the values: if it does,
nothing is written and the command fails.

After the copy, DEST is searched for anything that still looks like a
hardware address, a network address or a UUID. What is found is printed for
a person to judge: kernel messages hold many UUIDs that name no machine.
"""
import argparse
import io
import os
import re
import shutil
import sys
import zipfile

LOOPBACK = {"127.0.0.1", "::1", "127.0.0.53", "0.0.0.0", "00:00:00:00:00:00"}
SKIP = {"private", "capture.log"}

UUID_RE = re.compile(r"\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b")
MAC_RE = re.compile(r"\b(?:[0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}\b")
IPV4_RE = re.compile(r"(?<![\d.])(?:\d{1,3}\.){3}\d{1,3}(?![\d.])")
MACHINE_ID_RE = re.compile(r'("_MACHINE_ID"\s*:\s*")([0-9a-f]{32})(")')


def read_ident(path):
    out = []
    with open(path, encoding="utf-8") as f:
        for line in f:
            key, sep, value = line.rstrip("\n").partition("=")
            value = value.strip()
            if sep and value and value.lower() not in ("none", "not specified", "unknown"):
                out.append((key, value))
    return out


class Mapping:
    """Stand-ins for the values that name the machine."""

    def __init__(self, ident, host):
        self.pairs = []  # (value, stand-in), longest value first when applied
        self.fragments = set()
        self.machine_ids = {}
        counts = {}

        def n(kind):
            counts[kind] = counts.get(kind, 0) + 1
            return counts[kind]

        seen = set()
        for key, value in ident:
            if value in seen or value in LOOPBACK:
                continue
            seen.add(value)
            if key in ("hostname", "fqdn"):
                if host and value not in ("localhost", host):
                    self.add(value, host)
            elif key == "machine_id":
                stand = self.machine_id(value)
                dashed = "-".join((value[0:8], value[8:12], value[12:16], value[16:20], value[20:32]))
                self.add(dashed, "-".join((stand[0:8], stand[8:12], stand[12:16], stand[16:20], stand[20:32])))
            elif key == "mac":
                stand = "00:00:5e:00:53:%02x" % n("mac")
                self.add(value.lower(), stand)
                self.add(value.upper(), stand.upper())
                self.add(value.lower().replace(":", "-"), stand.replace(":", "-"))
            elif key == "ip":
                if ":" in value:
                    k = n("ip6")
                    self.add(value, "fe80::200:5eff:fe00:53%02x" % k if value.lower().startswith("fe80") else "2001:db8::%x" % k)
                else:
                    self.add(value, "192.0.2.%d" % n("ip4"))
            elif key.startswith("dmi_") and not UUID_RE.fullmatch(value):
                self.add(value, "LAB-SERIAL-%d" % n("serial"))
            elif UUID_RE.fullmatch(value):
                stand = "00000000-0000-4000-8000-%012x" % n("uuid")
                self.add(value.lower(), stand)
                self.add(value.upper(), stand.upper())
            elif re.fullmatch(r"[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}", value):
                self.add(value, "0000-%04X" % n("vfat"))
            else:
                self.add(value, "LAB-ID-%d" % n("id"))
        self.pairs.sort(key=lambda p: -len(p[0]))

    def add(self, value, stand):
        if not value or value == stand:
            return
        self.pairs.append((value, stand))
        if "-" in value:
            # systemd writes a dash in a unit name as \x2d
            # (dev-disk-by\x2duuid-...). In JSON the backslash is doubled,
            # and /dev/kmsg writes the backslash itself as \x5c.
            for dash in ("\\x2d", "\\\\x2d", "\\x5cx2d"):
                self.pairs.append((value.replace("-", dash), stand.replace("-", dash)))
        # The start of an ID is enough to find what is left of it in a form
        # nobody thought of.
        if len(value) >= 12 and re.fullmatch(r"[0-9A-Fa-f-]+", value):
            self.fragments.add(value[:8].lower())

    def machine_id(self, value):
        """The stand-in of a machine ID; the journal of one host holds two,
        the one of the initrd and the one of the installed system."""
        if value not in self.machine_ids:
            self.machine_ids[value] = "0123456789abcdef0123456789abcd%02x" % (len(self.machine_ids) + 1)
            self.add(value, self.machine_ids[value])
            self.pairs.sort(key=lambda p: -len(p[0]))
        return self.machine_ids[value]

    def text(self, s):
        for m in set(MACHINE_ID_RE.findall(s)):
            self.machine_id(m[1])
        for value, stand in self.pairs:
            s = s.replace(value, stand)
        return s

    def found_in(self, data):
        """The values a binary file holds."""
        low = data.lower()
        return [value for value, _ in self.pairs if value.lower().encode() in low]


def is_text(data):
    if b"\0" in data:
        return False
    try:
        data.decode("utf-8")
    except UnicodeDecodeError:
        return False
    return True


def members(name, data):
    """A binary file and, for a zip archive, what it holds."""
    yield name, data
    if data[:4] == b"PK\x03\x04":
        with zipfile.ZipFile(io.BytesIO(data)) as z:
            for info in z.infolist():
                yield name + "!" + info.filename, z.read(info)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("src")
    ap.add_argument("dest")
    ap.add_argument("--host", help="the name the host gets (default: its name is kept)")
    ap.add_argument("--keep", action="append", default=[], metavar="TEXT",
                    help="a UUID or address that names no machine; not reported")
    ap.add_argument("--replace", action="append", default=[], metavar="OLD=NEW",
                    help="a value the capture does not know as its own: the host a kernel was built on is in "
                         "the boot banner of every guest that runs that kernel")
    args = ap.parse_args()

    ident_file = os.path.join(args.src, "private", "ident.txt")
    ident = read_ident(ident_file) if os.path.exists(ident_file) else []
    if not ident and not args.replace:
        print("sanitize.py: %s is missing or empty: nothing is known about this host" % ident_file, file=sys.stderr)
        return 1
    mapping = Mapping(ident, args.host)
    for pair in args.replace:
        old, sep, new = pair.partition("=")
        if not sep or not old:
            print("sanitize.py: --replace takes OLD=NEW, got %r" % pair, file=sys.stderr)
            return 2
        mapping.add(old, new)
    mapping.pairs.sort(key=lambda p: -len(p[0]))

    files = []
    for root, dirs, names in os.walk(args.src):
        rel = os.path.relpath(root, args.src)
        dirs[:] = sorted(d for d in dirs if not (rel == "." and d in SKIP))
        for name in sorted(names):
            if rel == "." and name in SKIP:
                continue
            files.append(os.path.normpath(os.path.join(rel, name)))

    # Text first: the journal names machine IDs that ident.txt does not have.
    out, binaries = {}, {}
    for rel in files:
        with open(os.path.join(args.src, rel), "rb") as f:
            data = f.read()
        if is_text(data):
            out[rel] = mapping.text(data.decode("utf-8")).encode("utf-8")
        else:
            binaries[rel] = data
    bad = []
    for rel, data in binaries.items():
        for name, content in members(rel, data):
            for value in mapping.found_in(content):
                bad.append("%s holds %r" % (name, value))
        out[rel] = data
    if bad:
        print("sanitize.py: binary files hold values that name the machine; nothing was written", file=sys.stderr)
        for line in bad:
            print("  " + line, file=sys.stderr)
        return 1

    if os.path.exists(args.dest):
        shutil.rmtree(args.dest)
    for rel, data in out.items():
        path = os.path.join(args.dest, rel)
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as f:
            f.write(data)

    # What still looks like an address or an ID.
    stand_ins = {stand.lower() for _, stand in mapping.pairs} | {k.lower() for k in args.keep}
    left = {}
    for rel, data in out.items():
        if rel in binaries:
            continue
        text = data.decode("utf-8")
        low_text = text.lower()
        for frag in mapping.fragments:
            if frag in low_text:
                left.setdefault(("part", frag), set()).add(rel)
        for kind, rx in (("uuid", UUID_RE), ("mac", MAC_RE), ("ipv4", IPV4_RE)):
            for m in rx.findall(text):
                low = m.lower()
                if low in stand_ins or low in LOOPBACK:
                    continue
                if kind == "ipv4" and (low.startswith("192.0.2.") or any(int(p) > 255 for p in low.split("."))):
                    continue
                left.setdefault((kind, m), set()).add(rel)
    print("sanitized %d files into %s (%d values replaced)" % (len(out), args.dest, len(mapping.pairs)))
    for (kind, value), where in sorted(left.items()):
        print("  left: %-5s %s  (%s)" % (kind, value, ", ".join(sorted(where)[:3])))
    # A part of a value that names the machine must not be left anywhere.
    return 1 if any(kind == "part" for kind, _ in left) else 0


if __name__ == "__main__":
    sys.exit(main())
