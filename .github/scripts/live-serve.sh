#!/usr/bin/env bash
# Runs the exporter against the machine it is on: starts "my serve", waits
# for the first scan, reads /metrics, lets promtool check the page, and
# stops the server the way a service manager does.
#
# promtool runs from its container image, or from the program PROMTOOL
# names.
#
# usage: live-serve.sh <binary>
set -u

bin=$1
promtool() {
  if [ -n "${PROMTOOL:-}" ]; then
    "$PROMTOOL" "$@"
  else
    docker run --rm -i --entrypoint promtool prom/prometheus:v3.16.0 "$@"
  fi
}
tmp=$(mktemp -d)
pid=
cleanup() {
  [ -n "$pid" ] && kill "$pid" 2>/dev/null
  rm -rf "$tmp"
}
trap cleanup EXIT

fail() {
  echo "FAILED: $*"
  echo "--- log of the server"
  cat "$tmp/log"
  exit 1
}

echo "user $(id -un), uid $(id -u)"
"$bin" serve --metrics --listen 127.0.0.1:0 --interval 2s 2> "$tmp/log" &
pid=$!

url=
for _ in $(seq 100); do
  url=$(grep -o 'http://127\.0\.0\.1:[0-9]*' "$tmp/log" | head -1)
  [ -n "$url" ] && break
  kill -0 "$pid" 2>/dev/null || fail "the server ended before it listened"
  sleep 0.1
done
[ -n "$url" ] || fail "the server did not listen"
echo "server at $url"

# The first scan: /-/ready answers 503 until it has finished.
ready=
for _ in $(seq 900); do
  ready=$(curl -s -o /dev/null -w '%{http_code}' "$url/-/ready")
  [ "$ready" = 200 ] && break
  sleep 0.2
done
[ "$ready" = 200 ] || fail "/-/ready answers $ready"

curl -s -f "$url/metrics" > "$tmp/metrics" || fail "/metrics"
echo "--- /metrics"
cat "$tmp/metrics"

echo "--- promtool check metrics"
promtool check metrics < "$tmp/metrics" || fail "promtool refuses the page"

host=$("$bin" --json | python3 -c 'import json, sys; print(json.load(sys.stdin).get("host", ""))')
[ -n "$host" ] || fail "the scan names no host"
grep -q "^my_source_grade{host=\"$host\",source=\"hostinfo\",status=\"" "$tmp/metrics" || fail "no source series for host $host"
grep -q '^my_exporter_scan_failures_total 0$' "$tmp/metrics" || fail "a scan failed"
grep -q '^my_exporter_build_info{' "$tmp/metrics" || fail "no build info"

# No series twice, and nothing that looks like an address in a label.
dup=$(grep -v '^#' "$tmp/metrics" | sed 's/ [^ ]*$//' | sort | uniq -d)
[ -z "$dup" ] || fail "series twice: $dup"
if grep -v '^#' "$tmp/metrics" | grep -q -E '(row|column|bank|address)="'; then
  fail "a label carries a location"
fi

# A second scan follows on its own.
first=$(grep '^my_exporter_last_scan_timestamp_seconds ' "$tmp/metrics" | cut -d' ' -f2)
again=
for _ in $(seq 150); do
  again=$(curl -s "$url/metrics" | grep '^my_exporter_last_scan_timestamp_seconds ' | cut -d' ' -f2)
  [ -n "$again" ] && [ "$again" != "$first" ] && break
  sleep 0.2
done
[ "$again" != "$first" ] || fail "no second scan"

# Anything but GET and HEAD is refused.
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$url/metrics")
[ "$code" = 405 ] || fail "POST /metrics answers $code"

kill -TERM "$pid"
wait "$pid"
code=$?
pid=
[ "$code" -eq 0 ] || fail "the server ended with exit code $code after SIGTERM"
echo "--- log of the server"
cat "$tmp/log"
grep -q ' stopped$' "$tmp/log" || fail "the log does not say that the server stopped"
echo "live serve ok"
