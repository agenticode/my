#!/usr/bin/env bash
# Runs the binary against the machine it is on, the way an operator would:
# scan, the views, JSON, a bundle and opening it again. The machine is
# whatever runs CI: a virtual machine without memory controllers, which is
# the degraded case a first run on a new host often is.
#
# A command that shows findings exits with a grade (0 to 4); that is not a
# failure. Exit codes above 4 are.
#
# usage: live-scan.sh <binary>
set -u

bin=$1
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

run() {
  echo "--- $*"
  "$@"
  local code=$?
  echo "(exit code $code)"
  if [ "$code" -gt 4 ]; then
    echo "FAILED: $*"
    exit 1
  fi
}

echo "user $(id -un), uid $(id -u)"
run "$bin"
run "$bin" --lang ko --ascii --width 80
run "$bin" why --oneline
run "$bin" timeline

echo "--- $bin --json"
"$bin" --json > "$tmp/scan.json"
code=$?
if [ "$code" -gt 4 ]; then
  echo "FAILED: --json (exit code $code)"
  exit 1
fi
python3 - "$tmp/scan.json" <<'EOF'
import json, sys
d = json.load(open(sys.argv[1]))
print("view", d["view"], "host", d.get("host"), "grade", d["grade"], "exit_code", d["exit_code"])
for s in d["data"]["sources"]:
    print(" ", s["kind"], s["status"], s.get("records", 0), "records", "|", s.get("detail", ""))
kinds = {s["kind"] for s in d["data"]["sources"]}
missing = {"hostinfo", "rasdaemon", "klog", "edac-sysfs", "smbios"} - kinds
if missing:
    sys.exit("the source inventory lacks: %s" % ", ".join(sorted(missing)))
EOF
[ $? -eq 0 ] || exit 1

run "$bin" bundle -o "$tmp/runner.mycase" --anonymize
unzip -l "$tmp/runner.mycase" || exit 1
run "$bin" open "$tmp/runner.mycase"
run "$bin" open "$tmp/runner.mycase" timeline

# The name of this machine must not be in an anonymized bundle.
name=$(hostname)
mkdir "$tmp/x" && unzip -q -d "$tmp/x" "$tmp/runner.mycase" || exit 1
if [ ${#name} -ge 4 ] && grep -r -q -F "$name" "$tmp/x"; then
  echo "FAILED: the host name is in the anonymized bundle"
  grep -r -l -F "$name" "$tmp/x"
  exit 1
fi
echo "live scan ok"
