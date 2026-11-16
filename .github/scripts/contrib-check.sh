#!/usr/bin/env bash
# Checks the files under contrib/ with the programs they are written for:
#
#   prometheus/    promtool checks the rules, runs the rule tests and loads
#                  the scrape configuration; a Prometheus server scrapes the
#                  exporter and runs every query of the dashboard
#   alertmanager/  amtool checks the configuration and where alerts go
#   systemd/       systemd verifies the units and runs them
#   grafana/       a Grafana server loads the dashboard and runs its queries
#
# It installs the binary and the units on the machine it runs on, so it is
# meant for a CI machine. usage: contrib-check.sh <binary>
set -u

bin=$1
root=$(cd "$(dirname "$0")/../.." && pwd)
prometheus_image=prom/prometheus:v3.16.0
alertmanager_image=prom/alertmanager:v0.35.0
grafana_image=grafana/grafana:13.3.1
tmp=$(mktemp -d)
chmod 755 "$tmp"

demo_pid=
cleanup() {
  [ -n "$demo_pid" ] && kill "$demo_pid" 2>/dev/null
  docker rm -f my-prometheus my-grafana >/dev/null 2>&1
  sudo systemctl stop my-exporter my-fleet-exporter >/dev/null 2>&1
  rm -rf "$tmp"
}
trap cleanup EXIT

fail() {
  echo "FAILED: $*"
  exit 1
}

echo "=== prometheus: rules, rule tests, scrape configuration"
promtool() {
  docker run --rm -v "$root/contrib/prometheus:/rules:ro" -w /rules --entrypoint promtool "$prometheus_image" "$@"
}
promtool check rules my-findings.rules.yml my-exporter.rules.yml || fail "promtool check rules"
promtool test rules my-findings.rules.test.yml my-exporter.rules.test.yml || fail "promtool test rules"
promtool check config scrape.yml || fail "promtool check config"

echo "=== alertmanager: configuration and routes"
amtool() {
  docker run --rm -v "$root/contrib/alertmanager:/am:ro" --entrypoint amtool "$alertmanager_image" "$@"
}
amtool check-config /am/alertmanager.yml || fail "amtool check-config"
route() {
  local want=$1
  shift
  local got
  got=$(amtool config routes test --config.file=/am/alertmanager.yml "$@") || fail "amtool config routes test $*"
  [ "$got" = "$want" ] || fail "alert $* goes to $got, want $want"
  echo "  $* -> $got"
}
route memory-hardware alertname=MemoryFindingWorse finding=replace-now
route memory-hardware alertname=MemoryFindingWorse finding=hold-replacement
route memory-findings alertname=MemoryFindingWorse finding=consider-replacement
route memory-findings alertname=MemoryFindingWorse finding=observe
route memory-hardware alertname=GPUMemoryRepairFailed finding=replace-now
route memory-exporter alertname=MemoryFindingsStale
route default alertname=SomethingElse

echo "=== systemd: the units"
sudo install -m 0755 "$bin" /usr/local/bin/my || fail "install the binary"
sudo install -m 0644 "$root/contrib/systemd/my-exporter.service" "$root/contrib/systemd/my-fleet-exporter.service" /etc/systemd/system/ || fail "install the units"
sudo mkdir -p /etc/my
# A store that is not there: the fleet unit is checked for how it starts.
printf 'RASDAEMON_DB_BACKEND=postgresql\nRAS_PG_HOST=127.0.0.1\nRAS_PG_PORT=5439\nRAS_PG_PASSWORD=not-in-any-output\nRAS_PG_CONNECT_TIMEOUT=2\n' | sudo tee /etc/my/rasdaemon.env >/dev/null
sudo chmod 0600 /etc/my/rasdaemon.env
sudo systemd-analyze verify /etc/systemd/system/my-exporter.service /etc/systemd/system/my-fleet-exporter.service || fail "systemd-analyze verify"
sudo systemctl daemon-reload

wait_ready() {
  local want=$1 code=
  for _ in $(seq 300); do
    code=$(curl -s -o "$tmp/ready" -w '%{http_code}' http://127.0.0.1:10163/-/ready)
    [ "$code" = "$want" ] && return 0
    sleep 0.2
  done
  echo "/-/ready answers $code, want $want: $(cat "$tmp/ready" 2>/dev/null)"
  return 1
}

echo "--- my-exporter.service"
sudo systemctl start my-exporter || { sudo journalctl -u my-exporter --no-pager | tail -20; fail "start my-exporter"; }
wait_ready 200 || { sudo journalctl -u my-exporter --no-pager | tail -20; fail "my-exporter is not ready"; }
sudo journalctl -u my-exporter --no-pager | tail -5
curl -s -f http://127.0.0.1:10163/metrics > "$tmp/service.prom" || fail "/metrics of the service"
grep '^my_source_grade' "$tmp/service.prom"

# The service runs as a user that owns nothing, with two capabilities. It
# must read what root reads: the same sources with the same outcome.
user=$(systemctl show my-exporter -p User --value)
pid=$(systemctl show my-exporter -p MainPID --value)
echo "main process $pid runs as uid $(stat -c %u "/proc/$pid")"
[ "$(stat -c %u "/proc/$pid")" != 0 ] || fail "the service runs as root"
sudo /usr/local/bin/my --json > "$tmp/root.json"
[ $? -le 4 ] || fail "my --json as root"
python3 - "$tmp/root.json" "$tmp/service.prom" <<'EOF' || fail "the service reads less than root does"
import json, re, sys
root = {(s["kind"], s["status"]) for s in json.load(open(sys.argv[1]))["data"]["sources"]}
service = set(re.findall(r'^my_source_grade\{host="[^"]*",source="([^"]*)",status="([^"]*)"\}', open(sys.argv[2]).read(), re.M))
print("as root:   ", sorted(root))
print("as service:", sorted(service))
sys.exit(0 if root == service else 1)
EOF

echo "=== systemd: the boot harvest"
# The one command that changes the host: what it writes is what contrib
# holds, systemd takes the units, the service runs and keeps its ledger,
# and uninstall takes back what install did and leaves the ledger.
sudo /usr/local/bin/my harvest install --timer --dry-run || fail "harvest install --dry-run"
[ ! -e /etc/systemd/system/my-harvest.service ] || fail "a dry run wrote the unit"
sudo /usr/local/bin/my harvest install --timer || fail "harvest install"
cmp /etc/systemd/system/my-harvest.service "$root/contrib/systemd/my-harvest.service" || fail "install wrote another service than contrib holds"
cmp /etc/systemd/system/my-harvest.timer "$root/contrib/systemd/my-harvest.timer" || fail "install wrote another timer than contrib holds"
sudo systemd-analyze verify /etc/systemd/system/my-harvest.service /etc/systemd/system/my-harvest.timer || fail "systemd-analyze verify the harvest"
systemctl is-enabled my-harvest.service my-harvest.timer || fail "the harvest units are not enabled"
sudo systemctl start my-harvest.service || { sudo journalctl -u my-harvest --no-pager | tail -20; fail "start my-harvest"; }
sudo systemctl start my-harvest.service || { sudo journalctl -u my-harvest --no-pager | tail -20; fail "start my-harvest again"; }
sudo journalctl -u my-harvest --no-pager | tail -8
sudo journalctl -u my-harvest --no-pager | grep -q "phase A:" || fail "the harvest wrote no line for phase A"
sudo test -s /var/lib/my/ledger.db || fail "the harvest kept no ledger"
sudo /usr/local/bin/my harvest status || fail "harvest status"
sudo /usr/local/bin/my open /var/lib/my/ledger.db --json > "$tmp/kept.json"; [ $? -le 4 ] || fail "my open of the kept ledger"
sudo /usr/local/bin/my harvest uninstall || fail "harvest uninstall"
[ ! -e /etc/systemd/system/my-harvest.service ] && [ ! -e /etc/systemd/system/my-harvest.timer ] || fail "uninstall left a unit"
sudo test -s /var/lib/my/ledger.db || fail "uninstall removed the ledger"

echo "=== prometheus: a server scrapes the exporter and loads the rules"
# This machine has no memory errors. A second exporter serves the demo
# host, which has two DIMMs with a finding, so that the queries of the
# dashboard have something to show.
"$bin" synth -scenario demo -out "$tmp/demo" >/dev/null || fail "my synth"
"$bin" serve --metrics --root "$tmp/demo" --listen 127.0.0.1:10164 --interval 1h 2> "$tmp/demo.log" &
demo_pid=$!
cat > "$tmp/prometheus.yml" <<'EOF'
global:
  scrape_interval: 2s
  evaluation_interval: 2s
rule_files:
  - /rules/my-findings.rules.yml
  - /rules/my-exporter.rules.yml
scrape_configs:
  - job_name: memory
    static_configs:
      - targets: ['127.0.0.1:10163', '127.0.0.1:10164']
EOF
docker run -d --name my-prometheus --network host -v "$root/contrib/prometheus:/rules:ro" -v "$tmp/prometheus.yml:/etc/prometheus/prometheus.yml:ro" \
  "$prometheus_image" --config.file=/etc/prometheus/prometheus.yml --web.listen-address=127.0.0.1:9090 >/dev/null || fail "start prometheus"
scraped=
for _ in $(seq 150); do
  scraped=$(curl -s 'http://127.0.0.1:9090/api/v1/query?query=my_exporter_build_info' | python3 -c 'import json, sys; print(len(json.load(sys.stdin)["data"]["result"]))' 2>/dev/null)
  [ "$scraped" = 2 ] && break
  sleep 0.2
done
[ "$scraped" = 2 ] || { docker logs my-prometheus 2>&1 | tail -20; cat "$tmp/demo.log"; fail "prometheus did not scrape the two exporters"; }
# The finding alert fires for the two DIMMs of the demo host: they show up
# with a finding.
firing=
for _ in $(seq 150); do
  firing=$(curl -s http://127.0.0.1:9090/api/v1/alerts | python3 -c '
import json, sys
alerts = [a for a in json.load(sys.stdin)["data"]["alerts"] if a["labels"]["alertname"] == "MemoryFindingWorse" and a["state"] == "firing"]
print(" ".join(sorted(a["labels"]["slot"] + "=" + a["labels"]["finding"] for a in alerts)))' 2>/dev/null)
  [ "$firing" = "CPU0_C1=replace-now CPU1_D1=observe" ] && break
  sleep 0.2
done
echo "alerts firing: $firing"
[ "$firing" = "CPU0_C1=replace-now CPU1_D1=observe" ] || fail "MemoryFindingWorse does not fire for the DIMMs of the demo host"
curl -s http://127.0.0.1:9090/api/v1/rules | python3 -c '
import json, sys
groups = json.load(sys.stdin)["data"]["groups"]
rules = [(g["name"], r["name"], r["health"]) for g in groups for r in g["rules"]]
print(rules)
sys.exit(0 if len(rules) == 3 and all(h in ("ok", "unknown") for _, _, h in rules) else 1)' || fail "the rules did not load"

echo "=== grafana: a server loads the dashboard and runs its queries"
mkdir -p "$tmp/provisioning/datasources" "$tmp/provisioning/dashboards"
cat > "$tmp/provisioning/datasources/prometheus.yml" <<'EOF'
apiVersion: 1
datasources:
  - name: Prometheus
    uid: prometheus
    type: prometheus
    access: proxy
    url: http://127.0.0.1:9090
    isDefault: true
EOF
cat > "$tmp/provisioning/dashboards/my.yml" <<'EOF'
apiVersion: 1
providers:
  - name: my
    type: file
    options:
      path: /dashboards
EOF
chmod -R a+rX "$tmp/provisioning"
docker run -d --name my-grafana --network host -e GF_SERVER_HTTP_ADDR=127.0.0.1 -e GF_SERVER_HTTP_PORT=3000 \
  -e GF_AUTH_ANONYMOUS_ENABLED=true -e GF_AUTH_ANONYMOUS_ORG_ROLE=Admin -e GF_ANALYTICS_REPORTING_ENABLED=false -e GF_ANALYTICS_CHECK_FOR_UPDATES=false \
  -v "$tmp/provisioning:/etc/grafana/provisioning:ro" -v "$root/contrib/grafana:/dashboards:ro" "$grafana_image" >/dev/null || fail "start grafana"
up=
for _ in $(seq 300); do
  up=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:3000/api/dashboards/uid/my-memory-findings)
  [ "$up" = 200 ] && break
  sleep 0.5
done
[ "$up" = 200 ] || { docker logs my-grafana 2>&1 | grep -i -E 'error|dashboard' | tail -20; fail "grafana did not load the dashboard (HTTP $up)"; }
if docker logs my-grafana 2>&1 | grep -i 'failed to load dashboard'; then
  fail "grafana reports a dashboard it could not load"
fi
curl -s http://127.0.0.1:3000/api/dashboards/uid/my-memory-findings > "$tmp/loaded.json"
python3 - "$root/contrib/grafana/my-memory-findings.json" "$tmp/loaded.json" <<'EOF' || fail "the dashboard did not load as it was written, or a query failed"
import json, sys, urllib.request

written = json.load(open(sys.argv[1]))
loaded = json.load(open(sys.argv[2]))["dashboard"]
titles = lambda d: [p["title"] for p in d["panels"]]
if titles(written) != titles(loaded):
    sys.exit("panels as written %r, as loaded %r" % (titles(written), titles(loaded)))
print("dashboard %r: %d panels, schema version %s as loaded" % (loaded["title"], len(loaded["panels"]), loaded.get("schemaVersion")))

# Every query of every panel, sent the way a panel sends it: through the
# data source of Grafana. The variables take the values a viewer starts with.
failed = 0
for panel in loaded["panels"]:
    queries = []
    for t in panel.get("targets", []):
        expr = t["expr"].replace("$host", ".*").replace("$__range", "1h")
        queries.append({"refId": t["refId"], "datasource": {"type": "prometheus", "uid": "prometheus"}, "expr": expr,
                        "instant": t.get("instant", False), "range": t.get("range", True), "format": t.get("format", "time_series"),
                        "intervalMs": 15000, "maxDataPoints": 500})
    if not queries:
        continue
    body = json.dumps({"queries": queries, "from": "now-1h", "to": "now"}).encode()
    req = urllib.request.Request("http://127.0.0.1:3000/api/ds/query", data=body, headers={"Content-Type": "application/json"})
    try:
        res = json.load(urllib.request.urlopen(req, timeout=30))["results"]
    except Exception as err:
        print("FAILED panel %r: %s" % (panel["title"], err))
        failed += 1
        continue
    for ref, r in sorted(res.items()):
        frames = r.get("frames", [])
        rows = sum(len(f["data"]["values"][0]) if f["data"]["values"] else 0 for f in frames)
        if r.get("error") or r.get("status", 200) != 200:
            print("FAILED panel %r query %s: %s" % (panel["title"], ref, r.get("error")))
            failed += 1
        else:
            print("  %-45s %s: %d frames, %d rows" % (panel["title"], ref, len(frames), rows))
            # The demo host: two DIMMs with a finding, one fault each.
            want = {("DIMMs with a finding", "A"): 2, ("DIMMs with a finding", "B"): 2, ("DIMMs with a finding", "C"): 2,
                    ("DIMMs with a finding", "D"): 2, ("Faults by mode", "A"): 2, ("Finding over time", "A"): 1}.get((panel["title"], ref), 0)
            if rows < want:
                print("FAILED panel %r query %s: %d rows, want at least %d" % (panel["title"], ref, rows, want))
                failed += 1
sys.exit(1 if failed else 0)
EOF

echo "--- stop my-exporter.service"
sudo systemctl stop my-exporter || fail "stop my-exporter"
[ "$(systemctl show my-exporter -p Result --value)" = success ] || { sudo journalctl -u my-exporter --no-pager | tail -20; fail "my-exporter did not end cleanly"; }
sudo journalctl -u my-exporter --no-pager | tail -3 | grep -q ' stopped$' || fail "the log of my-exporter does not say that it stopped"

echo "--- my-fleet-exporter.service"
sudo systemctl start my-fleet-exporter || { sudo journalctl -u my-fleet-exporter --no-pager | tail -20; fail "start my-fleet-exporter"; }
# The store is not there. The service stays up, says so, and keeps trying.
wait_ready 503 || { sudo journalctl -u my-fleet-exporter --no-pager | tail -20; fail "my-fleet-exporter does not answer"; }
for _ in $(seq 100); do
  grep -q 'rasdaemon store postgresql://127.0.0.1:5439/rasdaemon' "$tmp/ready" && break
  sleep 0.2
  curl -s -o "$tmp/ready" http://127.0.0.1:10163/-/ready
done
cat "$tmp/ready"
grep -q 'rasdaemon store postgresql://127.0.0.1:5439/rasdaemon' "$tmp/ready" || { sudo journalctl -u my-fleet-exporter --no-pager | tail -20; fail "the fleet service does not name the store it cannot reach"; }
[ "$(systemctl is-active my-fleet-exporter)" = active ] || fail "my-fleet-exporter is not running"
sudo journalctl -u my-fleet-exporter --no-pager | tail -5
if sudo journalctl -u my-fleet-exporter --no-pager | grep -q 'not-in-any-output' || grep -q 'not-in-any-output' "$tmp/ready"; then
  fail "the password of the store is in the output of the service"
fi
sudo systemctl stop my-fleet-exporter || fail "stop my-fleet-exporter"
[ "$(systemctl show my-fleet-exporter -p Result --value)" = success ] || fail "my-fleet-exporter did not end cleanly"
echo "contrib ok"
