#!/usr/bin/env python3
"""Runs a "my" binary against the host it is on and records what happened.

Every command it starts only reads the host. Files are written below --out
and nowhere else; the temporary files of the binary go there too.

  host.py --my ./my --out DIR            the commands as this user
  host.py --my ./my --out DIR --sudo     also scan, views and bundle as root (sudo -n)
  host.py --my ./my --out DIR --as USER  run as root: also scan, views and bundle as USER

  --serve    also start the exporter on a free loopback port for a few seconds
  --strace   also trace a scan and a bundle and list what they opened for writing
  --devnull  also write a bundle and a report to /dev/null and check that it stays a device

It writes DIR/results.json (one entry per command: exit code, seconds, peak
memory, problems found, and what the host offers) and, per command, what it
printed. It prints one line per command and exits 1 when a command crashed,
ended with a wrong exit code, left files behind or put a name of this host
into an anonymized bundle.

Needs Python 3.6 and nothing else. docs/validation.md says what it was run on.
"""
import argparse
import glob
import json
import os
import re
import shutil
import signal
import socket
import stat
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request
import zipfile

GRADES = {0, 1, 2, 3, 4}
USAGE, FAILURE = 64, 70


def read(path, limit=1 << 20):
    try:
        with open(path, "rb") as f:
            return f.read(limit).decode("utf-8", "replace").strip()
    except OSError:
        return None


def output(argv, timeout=20):
    """What a short read-only command prints, and its exit code."""
    try:
        p = subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                           stderr=subprocess.PIPE, timeout=timeout)
        return p.returncode, p.stdout.decode("utf-8", "replace").strip()
    except (OSError, subprocess.TimeoutExpired):
        return None, ""


def readable(path):
    return os.path.exists(path) and os.access(path, os.R_OK)


def facts():
    """What the host offers to a memory error investigation, without names."""
    u = os.uname()
    f = {
        "kernel": u.release,
        "arch": u.machine,
        "cpus": os.cpu_count(),
        "user": "root" if os.geteuid() == 0 else "user",
    }
    osr = read("/etc/os-release") or ""
    m = re.search(r'^PRETTY_NAME="?([^"\n]*)', osr, re.M)
    f["os"] = m.group(1) if m else None
    mem = read("/proc/meminfo") or ""
    m = re.search(r"^MemTotal:\s+(\d+) kB", mem, re.M)
    f["mem_total_mb"] = int(m.group(1)) // 1024 if m else None
    m = re.search(r"^HardwareCorrupted:\s+(\d+) kB", mem, re.M)
    f["hardware_corrupted_kb"] = int(m.group(1)) if m else None
    code, virt = output(["systemd-detect-virt"])
    f["virtualization"] = virt if code is not None else None
    f["dmi_product"] = read("/sys/class/dmi/id/product_name")
    f["dmi_vendor"] = read("/sys/class/dmi/id/sys_vendor")

    mcs = sorted(glob.glob("/sys/devices/system/edac/mc/mc[0-9]*"))
    f["edac_controllers"] = len(mcs)
    f["edac_dimms"] = len(glob.glob("/sys/devices/system/edac/mc/mc[0-9]*/dimm[0-9]*")) + \
        len(glob.glob("/sys/devices/system/edac/mc/mc[0-9]*/rank[0-9]*"))
    mods = read("/proc/modules") or ""
    f["edac_modules"] = sorted({l.split()[0] for l in mods.splitlines() if "edac" in l.split()[0]})
    f["machine_check_banks"] = len(glob.glob("/sys/devices/system/machinecheck/machinecheck0/bank*"))
    f["acpi_hest"] = os.path.exists("/sys/firmware/acpi/tables/HEST")
    f["acpi_einj"] = os.path.exists("/sys/firmware/acpi/tables/EINJ")

    db = "/var/lib/rasdaemon/ras-mc_event.db"
    f["rasdaemon_program"] = shutil.which("rasdaemon") is not None
    f["rasdaemon_db"] = "readable" if readable(db) else ("closed" if os.path.exists(db) else "absent")
    if os.path.isdir("/var/lib/rasdaemon") and not os.access("/var/lib/rasdaemon", os.X_OK):
        f["rasdaemon_db"] = "closed"
    code, out = output(["systemctl", "is-active", "rasdaemon"])
    f["rasdaemon_service"] = out or None
    f["mcelog_program"] = shutil.which("mcelog") is not None
    code, out = output(["systemctl", "is-active", "mcelog"])
    f["mcelog_service"] = out or None
    f["nvidia_smi"] = shutil.which("nvidia-smi") is not None

    f["journalctl"] = shutil.which("journalctl") is not None
    if f["journalctl"]:
        code, out = output(["journalctl", "--no-pager", "-k", "-n", "1", "-o", "cat"], 60)
        f["journal_kernel_readable"] = code == 0 and out != ""
        code, out = output(["journalctl", "--no-pager", "--list-boots"], 60)
        f["journal_boots"] = len(out.splitlines()) if code == 0 else None
        code, out = output(["journalctl", "--disk-usage"], 60)
        m = re.search(r"take up ([0-9.]+\s*[KMGT]?)", out)
        f["journal_disk"] = m.group(1).replace(" ", "") + "B" if m else None
    f["dev_kmsg_readable"] = os.access("/dev/kmsg", os.R_OK)
    f["dmesg_restrict"] = read("/proc/sys/kernel/dmesg_restrict")
    f["log_files"] = [p for p in ("/var/log/kern.log", "/var/log/messages", "/var/log/syslog", "/var/log/dmesg")
                      if os.path.exists(p)]
    f["log_files_readable"] = [p for p in f["log_files"] if os.access(p, os.R_OK)]
    f["smbios_tables"] = "readable" if readable("/sys/firmware/dmi/tables/DMI") else (
        "closed" if os.path.exists("/sys/firmware/dmi/tables/DMI") else "absent")
    up = read("/proc/uptime")
    f["uptime_days"] = round(float(up.split()[0]) / 86400, 1) if up else None
    return f


def identifiers():
    """Names and addresses of this host: what an anonymized bundle must not hold."""
    ids = set()
    name = socket.gethostname()
    ids.add(name)
    ids.add(name.split(".")[0])
    try:
        ids.add(socket.getfqdn())
    except OSError:
        pass
    code, out = output(["hostname", "-I"])
    for a in out.split():
        ids.add(a)
    for p in glob.glob("/sys/class/net/*/address"):
        a = read(p)
        if a and a != "00:00:00:00:00:00":
            ids.add(a)
    mid = read("/etc/machine-id")
    if mid:
        ids.add(mid)
    # Too short to be told from chance, or local to every host.
    return sorted(i for i in ids if len(i) >= 5 and i not in ("localhost", "127.0.0.1", "::1"))


class Runner:
    def __init__(self, args):
        self.my = os.path.abspath(args.my)
        self.out = os.path.abspath(args.out)
        self.timeout = args.timeout
        self.memlimit = args.memlimit
        self.results = []
        self.ids = identifiers()

    def start(self, who):
        """Directories of one pass. who is "user", "root" (sudo) or a user name."""
        self.who = who
        self.dir = os.path.join(self.out, who)
        self.tmp = os.path.join(self.dir, "tmp")
        os.makedirs(self.tmp, exist_ok=True)
        os.makedirs(os.path.join(self.dir, "figures"), exist_ok=True)
        if who not in ("user", "root"):
            # Another user writes here: give the directories to everyone.
            for d in (self.dir, self.tmp, os.path.join(self.dir, "figures")):
                os.chmod(d, 0o777)

    def prefix(self):
        env = ["env", "TMPDIR=" + self.tmp, "MY_LANG=", "LC_ALL=C.UTF-8", "NO_COLOR=1"]
        if self.memlimit:
            env.append("GOMEMLIMIT=" + self.memlimit)
        if self.who == "root" and os.geteuid() != 0:
            return ["sudo", "-n"] + env
        if self.who not in ("user", "root"):
            return ["runuser", "-u", self.who, "--"] + env
        return env

    def run(self, name, argv, expect=GRADES, stdout=None, wrap=None):
        """One command of the binary. expect is the set of exit codes that are right.
        stdout names a file that takes the standard output in place of the
        usual one; wrap is a command line to put in front (strace)."""
        base = os.path.join(self.dir, "%02d-%s" % (len([r for r in self.results if r["as"] == self.who]) + 1, name))
        full = self.prefix() + (wrap or []) + [self.my] + argv
        rec = {"as": self.who, "name": name, "argv": ["my"] + [a.replace(self.dir + "/", "") for a in argv]}
        with open(stdout or base + ".out", "wb") as so, open(base + ".err", "wb") as se:
            t0 = time.monotonic()
            p = subprocess.Popen(full, stdin=subprocess.DEVNULL, stdout=so, stderr=se, cwd=self.dir,
                                 start_new_session=True)
            timed_out = []

            def stop():
                timed_out.append(True)
                for sig in (signal.SIGTERM, signal.SIGKILL):
                    try:
                        os.kill(p.pid, sig)
                    except OSError:
                        return
                    time.sleep(5)

            timer = threading.Timer(self.timeout, stop)
            timer.start()
            _, status, ru = os.wait4(p.pid, 0)
            timer.cancel()
            p.returncode = 0
            seconds = time.monotonic() - t0
        code = os.WEXITSTATUS(status) if os.WIFEXITED(status) else -os.WTERMSIG(status)
        rec.update(exit=code, seconds=round(seconds, 2), cpu_seconds=round(ru.ru_utime + ru.ru_stime, 2),
                   peak_rss_mb=round(ru.ru_maxrss / 1024, 1),
                   stdout_bytes=os.path.getsize(stdout or base + ".out"), stderr_bytes=os.path.getsize(base + ".err"))
        problems = []
        if timed_out:
            problems.append("timed out after %d s" % self.timeout)
        if code not in expect:
            problems.append("exit code %d, expected one of %s" % (code, sorted(expect)))
        err = read(base + ".err") or ""
        outtext = "" if stdout else (read(base + ".out", 8 << 20) or "")
        # What the Go runtime prints when a program crashes, at the start of
        # a line. Words such as "panic:" inside a text of the tool are none
        # ("/proc/sys/kernel/panic: 0" is evidence of my preflight).
        crash = re.compile(r"^(panic: |fatal error: |goroutine \d+ \[|\[signal SIG|runtime error: )", re.M)
        for text in (err, outtext):
            m = crash.search(text)
            if m:
                problems.append("crash text %r in the output" % m.group(1).strip())
                break
        if code in GRADES and expect == GRADES and rec["stdout_bytes"] == 0:
            problems.append("printed nothing")
        if code in (USAGE, FAILURE) and not err:
            problems.append("failed without a message")
        left = sorted(os.listdir(self.tmp))
        if left:
            problems.append("left in the temporary directory: %s" % ", ".join(left[:5]))
            for n in left:
                shutil.rmtree(os.path.join(self.tmp, n), ignore_errors=True)
        rec["problems"] = problems
        rec["_base"] = base
        self.results.append(rec)
        mark = "ok " if not problems else "BAD"
        print("%s %-5s %-22s exit %-3d %6.2f s %6.1f MB  %s" % (
            mark, self.who, name, code, seconds, rec["peak_rss_mb"], "; ".join(problems)))
        sys.stdout.flush()
        return rec, outtext

    def path(self, name):
        return os.path.join(self.dir, name)

    def check_json(self, rec, text, view):
        """The JSON of a view: parses, names the view, and its exit code is the one of the process."""
        try:
            d = json.loads(text)
        except ValueError as e:
            rec["problems"].append("the JSON does not parse: %s" % e)
            return None
        if d.get("view") != view:
            rec["problems"].append("view is %r, expected %r" % (d.get("view"), view))
        if d.get("exit_code") != rec["exit"]:
            rec["problems"].append("exit_code %r in the JSON, %d from the process" % (d.get("exit_code"), rec["exit"]))
        return d

    def check_bundle(self, rec, path, anonymized):
        """A bundle: a ZIP with the four members; an anonymized one holds no name of this host."""
        if not os.path.exists(path):
            rec["problems"].append("no bundle at %s" % os.path.basename(path))
            return
        rec["bundle_bytes"] = os.path.getsize(path)
        rec["bundle_mode"] = oct(os.stat(path).st_mode & 0o777)
        try:
            with zipfile.ZipFile(path) as z:
                names = sorted(z.namelist())
                if names != ["README.txt", "ledger.db", "manifest.json", "sources.json"]:
                    rec["problems"].append("members: %s" % names)
                bad = z.testzip()
                if bad:
                    rec["problems"].append("damaged member %s" % bad)
                m = json.loads(z.read("manifest.json"))
                rec["bundle_ledger_bytes"] = m.get("ledger", {}).get("bytes")
                if bool(m.get("anonymized")) != anonymized:
                    rec["problems"].append("manifest anonymized=%r" % m.get("anonymized"))
                if anonymized:
                    found = []
                    for n in names:
                        blob = z.read(n)
                        for i in self.ids:
                            if i.encode() in blob:
                                found.append("%s in %s" % (kind_of(i), n))
                    if found:
                        rec["problems"].append("identifiers of this host in the anonymized bundle: %s" %
                                               ", ".join(sorted(set(found))))
        except (OSError, zipfile.BadZipFile, ValueError, KeyError) as e:
            rec["problems"].append("cannot read the bundle: %s" % e)

    # The commands every pass runs: what an operator runs first.
    def views(self):
        rec, text = self.run("scan-json", ["--json"])
        scan = self.check_json(rec, text, "overview")
        slot = None
        if scan:
            data = scan.get("data", {})
            rec["sources"] = [{k: s.get(k) for k in ("kind", "name", "status", "records", "grade", "detail", "notes") if s.get(k) not in (None, "", [])}
                              for s in data.get("sources", [])]
            rec["grade"] = scan.get("grade")
            sm = data.get("slot_map") or {}
            rec["slots"], rec["dimms"] = sm.get("slots"), sm.get("populated")
            host = data.get("host") or {}
            rec["ce"], rec["ue"], rec["boots"] = host.get("ce"), host.get("ue"), host.get("boots")
            rec["missing"] = [m.get("text") for m in data.get("missing") or []]
            rec["virtual"] = host.get("virtual")
            rec["notes"] = [n.get("text") for n in data.get("notes") or []]
            rec["host_finding"] = (data.get("host_finding") or {}).get("grade")
            for row in data.get("ranking", []) or []:
                slot = slot or row.get("slot")
            if not slot:
                slot = find_slot(data)
        rec, _ = self.run("scan", ["--lang", "en"])
        self.run("scan-ko", ["--lang", "ko"])
        self.run("scan-ascii-80", ["--lang", "en", "--ascii", "--width", "80", "--all"])
        self.run("why", ["why", "--lang", "en"])
        self.run("why-oneline", ["why", "--oneline", "--lang", "en"])
        rec, text = self.run("why-json", ["why", "--json"])
        self.check_json(rec, text, "why")
        self.run("timeline", ["timeline", "--lang", "en"])
        # The commands that came after the first validation: the boots, the
        # inventory, the BIOS settings (none without a BMC: exit 0) and the
        # preflight (0, or 1 when a check is lost; no grade).
        self.run("boots", ["boots", "--lang", "ko"])
        rec, text = self.run("boots-json", ["boots", "--json"])
        self.check_json(rec, text, "boots")
        self.run("inventory", ["inventory", "--lang", "en"], {0})
        self.run("bios", ["bios", "--lang", "en"], {0})
        rec, text = self.run("preflight-json", ["preflight", "--json"], {0, 1})
        self.check_json(rec, text, "preflight")
        if slot:
            self.run("dimm", ["dimm", slot, "--lang", "en"])
            self.run("map", ["map", slot, "--lang", "en"])
            self.run("timeline-dimm", ["timeline", slot, "--lang", "ko"])
        rec, _ = self.run("bundle", ["bundle", "-o", self.path("case.mycase"), "--lang", "en"], {0})
        self.check_bundle(rec, self.path("case.mycase"), False)
        rec, _ = self.run("bundle-anonymize", ["bundle", "-o", self.path("case-anon.mycase"), "--anonymize", "--lang", "en"], {0})
        self.check_bundle(rec, self.path("case-anon.mycase"), True)
        # To the standard output: the file belongs to whoever reads the pipe,
        # also when the scan ran as root, and nothing is left on the host.
        rec, _ = self.run("bundle-stdout", ["bundle", "-o", "-", "--lang", "en"], {0}, stdout=self.path("piped.mycase"))
        self.check_bundle(rec, self.path("piped.mycase"), False)
        if os.stat(self.path("piped.mycase")).st_uid != os.geteuid():
            rec["problems"].append("the piped bundle does not belong to the reader of the pipe")
        return slot

    # What only the invoking user runs: the stored case, the report, the
    # imports and the command lines that must fail.
    def rest(self, slot):
        case, anon = self.path("case.mycase"), self.path("case-anon.mycase")
        self.run("version", ["version"], {0})
        rec, text = self.run("version-json", ["version", "--json"], {0})
        try:
            rec["version"] = json.loads(text)
        except ValueError as e:
            rec["problems"].append("the JSON does not parse: %s" % e)
        self.run("bundle-exists", ["bundle", "-o", case], {USAGE})
        self.run("open", ["open", case, "--lang", "en"])
        self.run("open-why", ["open", case, "why", "--lang", "ko"])
        self.run("open-timeline", ["open", anon, "timeline", "--lang", "en"])
        rec, text = self.run("open-json", ["open", case, "--json"])
        self.check_json(rec, text, "overview")
        self.run("report", ["report", "-o", self.path("report.html"), "--lang", "en"], {0})
        self.run("report-case-ko-fa", ["report", case, "-o", self.path("report-ko.html"), "--lang", "ko",
                                       "--audience", "fa", "--all"], {0})
        self.run("report-figures", ["report", anon, "--figures", self.path("figures"), "--lang", "en"], {0})
        rec, text = self.run("report-stdout", ["report", case, "-o", "-"], {0})
        if "<html" not in text[:400]:
            rec["problems"].append("no HTML on stdout")
        self.run("ledger-1", ["--ledger", self.path("host.db"), "--lang", "en"])
        self.run("ledger-2", ["--ledger", self.path("host.db"), "--lang", "en"])
        self.run("open-ledger", ["open", self.path("host.db"), "--lang", "en"])

        # Loose files of this host, the way they are sent by mail.
        code, text = output(["dmesg"], 60)
        if code == 0 and text:
            with open(self.path("dmesg.txt"), "w") as f:
                f.write(text + "\n")
            self.run("import-dmesg", ["import", self.path("dmesg.txt"), "--lang", "en"])
            self.run("import-dmesg-case", ["import", self.path("dmesg.txt"), "-o", self.path("dmesg.mycase")], {0})
        code, text = output(["journalctl", "--no-pager", "-k", "-b", "-o", "json"], 120)
        if code == 0 and text:
            with open(self.path("journal.json"), "w") as f:
                f.write(text + "\n")
            self.run("import-journal", ["import", self.path("journal.json"), "--lang", "en"])
        for p in ("/var/log/messages", "/var/log/kern.log", "/var/log/syslog"):
            if os.path.exists(p):
                ok = GRADES if os.access(p, os.R_OK) else {FAILURE}
                self.run("import-" + os.path.basename(p), ["import", p, "--lang", "en"], ok)
                break

        self.run("help", ["help"], {0})
        self.run("help-reading-ko", ["help", "reading", "--lang", "ko"], {0})
        self.run("help-rules", ["help", "rules"], {0})
        self.run("help-thresholds", ["help", "thresholds"], {0})
        self.run("bad-command", ["frobnicate"], {USAGE})
        self.run("bad-flag", ["--frobnicate"], {USAGE})
        self.run("bad-slot", ["dimm", "NO_SUCH_SLOT_17"], {USAGE, FAILURE})
        self.run("bad-case", ["open", self.path("absent.mycase")], {FAILURE})
        self.run("bad-case-content", ["open", self.path("dmesg.txt")], {FAILURE})
        # A target that cannot be written is a mistake of the command line,
        # found before the scan.
        rec, _ = self.run("bad-out-dir", ["bundle", "-o", self.path("absent/dir/case.mycase")], {USAGE})
        if rec["seconds"] > 1 and rec["seconds"] > 0.5 * max(r["seconds"] for r in self.results if r["name"] == "scan"):
            rec["problems"].append("the target was checked after the scan")
        # A directory that holds nothing of a host is no host without errors.
        os.makedirs(self.path("empty"), exist_ok=True)
        self.run("root-empty", ["--root", self.path("empty"), "--lang", "en"], {FAILURE})
        self.run("root-absent", ["--root", self.path("absent"), "--lang", "en"], {USAGE})
        self.run("command-after-flags", ["--root", self.path("empty"), "why"], {USAGE})
        self.run("gpu", ["gpu", "--lang", "en"])
        self.run("preflight", ["preflight", "--lang", "ko"], {0, 1})
        rec, text = self.run("preflight-script", ["preflight", "--script"], {0, 1})
        if text and not text.startswith("#!"):
            rec["problems"].append("the script does not start with #!")
        self.run("harvest-status", ["harvest", "status", "--ledger", self.path("host.db")], {0})
        self.run("open-boots", ["open", case, "boots", "--lang", "en"])
        # Without EDAC counters there is no position to label: exit 70.
        self.run("export-labels", ["export", "labels", "-o", self.path("labels.txt")], {0, FAILURE})
        rec, _ = self.run("bad-flag-ko", ["--frobnicate", "--lang", "ko"], {USAGE})
        errs = sorted(glob.glob(os.path.join(self.dir, "*-bad-flag-ko.err")))
        err = (read(errs[-1]) if errs else "") or ""
        if not re.search("[\uac00-\ud7a3]", err.split("\n")[0]):
            rec["problems"].append("the error is not in Korean: %r" % err[:80])
        self.run("export-fmfm", ["export", "fmfm", "-o", self.path("fmfm.json"), "--records"], {0})
        self.run("bad-threshold", ["--set", "rule.row.min_errors=many"], {USAGE})


    def devnull(self):
        """A device named as the output is written to and stays a device."""
        before = os.stat("/dev/null")
        self.run("bundle-devnull", ["bundle", "-o", "/dev/null", "--force"], {0})
        rec, _ = self.run("report-devnull", ["report", "-o", "/dev/null", "--force"], {0})
        after = os.stat("/dev/null")
        if not stat.S_ISCHR(after.st_mode) or (before.st_dev, before.st_ino, before.st_rdev) != (after.st_dev, after.st_ino, after.st_rdev):
            rec["problems"].append("/dev/null is no longer the device it was")
        left = [n for n in os.listdir("/dev") if n.endswith(".part")]
        if left:
            rec["problems"].append("files left in /dev: %s" % ", ".join(left))

    def strace(self):
        """What a scan and a bundle open for writing, as strace sees it."""
        if not shutil.which("strace"):
            print("skip  strace is not installed")
            return
        calls = ("openat,open,creat,unlink,unlinkat,rename,renameat,renameat2,mkdir,mkdirat,rmdir,chmod,fchmodat,chown,"
                 "fchownat,truncate,symlink,symlinkat,link,linkat,mknod,mknodat")
        for name, argv in (("strace-scan", ["--lang", "en"]), ("strace-bundle", ["bundle", "-o", self.path("traced.mycase"), "--force"])):
            log = self.path(name + ".trace")
            rec, _ = self.run(name, argv, wrap=["strace", "-f", "-qq", "-o", log, "-e", "trace=" + calls])
            written, relative = set(), set()
            for line in (read(log, 64 << 20) or "").splitlines():
                m = re.search(r'\b(\w+)\((AT_FDCWD, |\d+, |)"([^"]*)"(.*)$', line)
                if not m or "= -1 " in line:
                    continue
                call, at, path, rest = m.groups()
                if call in ("open", "openat") and not re.search(r"O_WRONLY|O_RDWR|O_CREAT|O_TRUNC|O_APPEND", rest):
                    continue
                if os.path.isabs(path):
                    written.add(os.path.normpath(path))
                elif at.strip(", ").isdigit():
                    # A name below a directory that is open, as a removal of
                    # a directory tree gives it.
                    relative.add(path)
                else:
                    written.add(os.path.normpath(os.path.join(self.dir, path)))
            # Such a name is one of the paths above, seen again without its directory.
            known = {os.path.basename(p) for p in written}
            outside = sorted(p for p in written if not (p == self.dir or p.startswith(self.dir + "/")))
            outside += sorted("(below an open directory) " + n for n in relative if n not in known)
            rec["written_paths"] = len(written)
            rec["written_outside"] = outside
            if outside:
                rec["problems"].append("opened for writing outside the output directory: %s" % ", ".join(outside[:8]))
                print("BAD   %s: %s" % (name, rec["problems"][-1]))

    def serve(self):
        """The exporter on a free loopback port: ready, metrics, the API, and a clean stop."""
        with socket.socket() as sk:
            sk.bind(("127.0.0.1", 0))
            port = sk.getsockname()[1]
        base = self.path("serve")
        rec = {"as": self.who, "name": "serve", "argv": ["my", "serve", "--metrics", "--listen", "127.0.0.1:PORT", "--interval", "30s"], "problems": []}
        argv = self.prefix() + [self.my, "serve", "--metrics", "--listen", "127.0.0.1:%d" % port, "--interval", "30s"]
        with open(base + ".out", "wb") as so, open(base + ".err", "wb") as se:
            t0 = time.monotonic()
            p = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=so, stderr=se, cwd=self.dir, start_new_session=True)
            url = "http://127.0.0.1:%d" % port

            def get(path):
                try:
                    with urllib.request.urlopen(url + path, timeout=10) as r:
                        return r.status, r.read()
                except urllib.error.HTTPError as e:
                    return e.code, e.read()
                except (OSError, ValueError):
                    return None, b""
            ready = None
            while time.monotonic() - t0 < self.timeout and p.poll() is None:
                code, _ = get("/-/ready")
                if code == 200:
                    ready = round(time.monotonic() - t0, 2)
                    break
                time.sleep(0.2)
            rec["ready_seconds"] = ready
            if ready is None:
                rec["problems"].append("the exporter did not get ready")
            else:
                code, body = get("/metrics")
                text = body.decode("utf-8", "replace")
                with open(base + ".metrics", "w") as f:
                    f.write(text)
                rec["metrics_status"] = code
                rec["series"] = len([l for l in text.splitlines() if l and not l.startswith("#")])
                rec["families"] = sorted({l.split()[2] for l in text.splitlines() if l.startswith("# TYPE ")})
                if code != 200 or "my_exporter_build_info" not in text:
                    rec["problems"].append("/metrics answered %s without the build information" % code)
                code, body = get("/api/dimm")
                rec["api_status"] = code
                try:
                    rec["api_cards"] = len(json.loads(body).get("data") or [])
                except ValueError:
                    rec["problems"].append("/api/dimm is not JSON")
                rec["healthy_status"], _ = get("/-/healthy")
            try:
                os.kill(p.pid, signal.SIGTERM)
            except OSError:
                pass
            try:
                _, status, ru = os.wait4(p.pid, 0)
            except ChildProcessError:
                status, ru = 0, None
            p.returncode = 0
        code = os.WEXITSTATUS(status) if os.WIFEXITED(status) else -os.WTERMSIG(status)
        rec.update(exit=code, seconds=round(time.monotonic() - t0, 2))
        if ru is not None:
            rec["peak_rss_mb"] = round(ru.ru_maxrss / 1024, 1)
        if code != 0:
            rec["problems"].append("the exporter ended with exit code %d after SIGTERM" % code)
        err = read(base + ".err") or ""
        for mark in ("panic:", "goroutine ", "fatal error:"):
            if mark in err:
                rec["problems"].append("crash text %r in the log" % mark)
        left = sorted(os.listdir(self.tmp))
        if left:
            rec["problems"].append("left in the temporary directory: %s" % ", ".join(left[:5]))
        self.results.append(rec)
        print("%s %-5s %-22s exit %-3d %6.2f s %6.1f MB  %s" % ("ok " if not rec["problems"] else "BAD", self.who, "serve", code,
              rec["seconds"], rec.get("peak_rss_mb", 0), "; ".join(rec["problems"])))


def kind_of(i):
    if re.fullmatch(r"[0-9a-f]{2}(:[0-9a-f]{2}){5}", i):
        return "a hardware address"
    if re.fullmatch(r"[0-9.]+", i) or ":" in i:
        return "an IP address"
    if re.fullmatch(r"[0-9a-f]{32}", i):
        return "the machine ID"
    return "the host name"


def find_slot(data):
    """The first slot of a scan that holds a module."""
    def walk(v):
        if isinstance(v, dict):
            if v.get("slot") and (v.get("present") or v.get("dimm") or v.get("serial")):
                return v["slot"]
            for x in v.values():
                s = walk(x)
                if s:
                    return s
        elif isinstance(v, list):
            for x in v:
                s = walk(x)
                if s:
                    return s
        return None
    return walk(data)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--my", required=True, help="the binary to run")
    ap.add_argument("--out", required=True, help="directory for everything this writes")
    ap.add_argument("--sudo", action="store_true", help="also run scan, views and bundle through sudo -n")
    ap.add_argument("--as", dest="as_user", help="run as root: also run scan, views and bundle as this user")
    ap.add_argument("--timeout", type=int, default=400, help="seconds one command may take")
    ap.add_argument("--memlimit", default="", help="GOMEMLIMIT for the binary, e.g. 200MiB")
    ap.add_argument("--serve", action="store_true", help="also start the exporter on a free loopback port for a few seconds")
    ap.add_argument("--strace", action="store_true", help="also trace a scan and a bundle and list what they opened for writing")
    ap.add_argument("--devnull", action="store_true", help="also write a bundle and a report to /dev/null")
    args = ap.parse_args()

    r = Runner(args)
    os.makedirs(r.out, exist_ok=True)
    f = facts()
    with open(os.path.join(r.out, "facts.json"), "w") as fh:
        json.dump(f, fh, indent=1)

    r.start("user")
    slot = r.views()
    r.rest(slot)
    if args.devnull:
        r.devnull()
    if args.strace:
        r.strace()
    if args.serve:
        r.serve()
    if args.sudo:
        r.start("root")
        r.views()
    if args.as_user:
        r.start(args.as_user)
        r.views()

    bad = [x for x in r.results if x["problems"]]
    for x in r.results:
        x.pop("_base", None)
    with open(os.path.join(r.out, "results.json"), "w") as fh:
        json.dump({"facts": f, "commands": r.results}, fh, indent=1)
    print("%d commands, %d with problems" % (len(r.results), len(bad)))
    for x in bad:
        print("  %s %s: %s" % (x["as"], x["name"], "; ".join(x["problems"])))
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
