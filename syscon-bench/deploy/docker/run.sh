#!/usr/bin/env bash
# Bring up one engine profile, run a checked-in scenario, and require the
# client and storage series to be present in Prometheus.
#
#   ./run.sh clickhouse
#   ./run.sh elasticsearch --keep
#   ./run.sh loki --scenario 02 --keep
#   ./run.sh victorialogs --corpus
#
# Scenario 01 is the default. Scenario 02 is the same check, left for a longer
# manual run when --keep is set. --corpus mounts data/messages.txt from prepare.
# Without it the bench uses the one-line sample in this directory.
#
# The default tears the stack down after a successful check and deletes volumes
# at the start of the next run, so a previous engine cannot satisfy the series
# check. --keep leaves Grafana on http://localhost:3000. A failed check leaves
# the stack up.

set -euo pipefail
cd "$(dirname "$0")"

usage() {
  sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
}

if docker compose version >/dev/null 2>&1; then
  COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE=(docker-compose)
else
  echo "docker compose is required" >&2
  exit 1
fi

ENGINE=""
KEEP=0
RESET=1
BUILD=(--build)
TIMEOUT=240
SCENARIO=test-scenario-01.yaml
export MESSAGES_FILE="${MESSAGES_FILE:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    clickhouse|elasticsearch|loki|victorialogs)
      ENGINE="$1"
      ;;
    --keep) KEEP=1 ;;
    --no-reset) RESET=0 ;;
    --no-build) BUILD=() ;;
    --timeout)
      TIMEOUT="${2:?--timeout needs seconds}"
      shift
      ;;
    --scenario)
      case "${2:?--scenario needs a scenario file}" in
        01|test-scenario-01|test-scenario-01.yaml) SCENARIO=test-scenario-01.yaml ;;
        02|test-scenario-02|test-scenario-02.yaml) SCENARIO=test-scenario-02.yaml ;;
        phase3-*.yaml)
          if [[ ! -f "../../log-generator/scenarios/$2" ]]; then
            echo "unknown scenario $2" >&2
            exit 2
          fi
          SCENARIO="$2"
          ;;
        phase3-*)
          if [[ ! -f "../../log-generator/scenarios/${2}.yaml" ]]; then
            echo "unknown scenario $2" >&2
            exit 2
          fi
          SCENARIO="${2}.yaml"
          ;;
        *) echo "unknown scenario $2" >&2; exit 2 ;;
      esac
      shift
      ;;
    --corpus)
      if [[ ! -f ../../data/messages.txt ]]; then
        echo "syscon-bench/data/messages.txt is missing; run syscon-bench prepare" >&2
        exit 1
      fi
      MESSAGES_FILE="$(cd ../.. && pwd)/data/messages.txt"
      ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument $1" >&2; usage; exit 2 ;;
  esac
  shift
done

if [[ -z "$ENGINE" ]]; then
  usage
  exit 2
fi
if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to read Prometheus responses" >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi

case "$ENGINE" in
  clickhouse)
    export STORAGE_NAME=clickhouse
    export STORAGE_ENDPOINT=http://clickhouse:8123
    export STORAGE_READY=http://clickhouse:8123/ping
    HOST_READY=http://127.0.0.1:8123/ping
    CPU_QUERY='ClickHouseAsyncMetrics_OSUserTimeNormalized or ClickHouseAsyncMetrics_OSUserTime or rate(container_cpu_usage_seconds_total{name=~".*clickhouse.*"}[2m])'
    RAM_QUERY='ClickHouseAsyncMetrics_MemoryResident or ClickHouseMetrics_MemoryTracking or container_memory_working_set_bytes{name=~".*clickhouse.*"}'
    ;;
  elasticsearch)
    export STORAGE_NAME=elasticsearch
    export STORAGE_ENDPOINT=http://elasticsearch:9200
    export STORAGE_READY=http://elasticsearch:9200/
    HOST_READY=http://127.0.0.1:9200
    CPU_QUERY='elasticsearch_process_cpu_percent or elasticsearch_os_cpu_percent or rate(container_cpu_usage_seconds_total{name=~".*elastic.*"}[2m])'
    RAM_QUERY='elasticsearch_jvm_memory_used_bytes or container_memory_working_set_bytes{name=~".*elastic.*"}'
    ;;
  loki)
    export STORAGE_NAME=loki
    export STORAGE_ENDPOINT=http://loki:3100
    export STORAGE_READY=http://loki:3100/ready
    HOST_READY=http://127.0.0.1:3100/ready
    CPU_QUERY='rate(process_cpu_seconds_total{job="loki"}[2m]) or rate(container_cpu_usage_seconds_total{name=~".*loki.*"}[2m])'
    RAM_QUERY='process_resident_memory_bytes{job="loki"} or container_memory_working_set_bytes{name=~".*loki.*"}'
    ;;
  victorialogs)
    export STORAGE_NAME=victorialogs
    export STORAGE_ENDPOINT=http://victorialogs:9428
    export STORAGE_READY=http://victorialogs:9428/health
    HOST_READY=http://127.0.0.1:9428/health
    CPU_QUERY='rate(process_cpu_seconds_total{job="victorialogs"}[2m]) or rate(container_cpu_usage_seconds_total{name=~".*victoria.*"}[2m])'
    RAM_QUERY='process_resident_memory_bytes{job="victorialogs"} or container_memory_working_set_bytes{name=~".*victoria.*"}'
    ;;
esac
export SCENARIO
if [[ -z "$MESSAGES_FILE" ]]; then
  unset MESSAGES_FILE
fi

down() {
  "${COMPOSE[@]}" --profile clickhouse --profile elasticsearch --profile loki --profile victorialogs down --remove-orphans "$@"
}

if [[ "$RESET" -eq 1 ]]; then
  down -v
fi

echo "starting $ENGINE with $SCENARIO"
echo "Grafana http://localhost:3000  Prometheus http://localhost:9090"
"${COMPOSE[@]}" --profile "$ENGINE" up -d "${BUILD[@]}"

echo "waiting for $ENGINE at $HOST_READY"
ready=0
for _ in $(seq 1 180); do
  if curl -fsS "$HOST_READY" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 2
done
if [[ "$ready" -ne 1 ]]; then
  echo "$ENGINE did not become ready" >&2
  "${COMPOSE[@]}" logs --tail 80 "$ENGINE" >&2 || true
  exit 1
fi

echo "waiting for Prometheus"
prom_ready=0
for _ in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:9090/-/ready >/dev/null 2>&1; then
    prom_ready=1
    break
  fi
  sleep 2
done
if [[ "$prom_ready" -ne 1 ]]; then
  echo "Prometheus did not become ready" >&2
  "${COMPOSE[@]}" logs --tail 80 prometheus >&2 || true
  exit 1
fi

prom() {
  curl -fsS -G http://127.0.0.1:9090/api/v1/query --data-urlencode "query=$1"
}

# kind is nonempty or positive. nonempty passes when Prometheus returns a series.
# positive passes when a sample is greater than zero.
series_ok() {
  local kind="$1" body="$2"
  KIND="$kind" BODY="$body" python3 - <<'PY'
import json, os, sys
kind = os.environ["KIND"]
try:
    data = json.loads(os.environ["BODY"])
except json.JSONDecodeError:
    sys.exit(1)
result = data.get("data", {}).get("result", [])
if not result:
    sys.exit(1)
if kind == "nonempty":
    sys.exit(0)
for series in result:
    value = series.get("value") or []
    if len(value) > 1 and float(value[1]) > 0:
        sys.exit(0)
sys.exit(1)
PY
}

checks=(
  "positive|ingest events|bench_ingest_events_total"
  "nonempty|ingest RPS|rate(bench_ingest_events_total[2m])"
  "nonempty|error rate|rate(bench_ingest_errors_total[2m]) or bench_ingest_errors_total"
  "positive|query latency|bench_query_duration_seconds_count"
  "nonempty|storage CPU|$CPU_QUERY"
  "nonempty|storage RAM|$RAM_QUERY"
)

echo "checking series for up to ${TIMEOUT}s"
deadline=$((SECONDS + TIMEOUT))
missing=""
while (( SECONDS < deadline )); do
  missing=""
  for item in "${checks[@]}"; do
    kind="${item%%|*}"
    rest="${item#*|}"
    name="${rest%%|*}"
    query="${rest#*|}"
    if ! body="$(prom "$query" 2>/dev/null)" || ! series_ok "$kind" "$body"; then
      missing="${missing}${name}; "
    fi
  done
  if [[ -z "$missing" ]]; then
    break
  fi
  sleep 5
done

if [[ -n "$missing" ]]; then
  echo "series check failed: $missing" >&2
  echo "Prometheus targets:" >&2
  curl -fsS http://127.0.0.1:9090/api/v1/targets 2>/dev/null | python3 -c 'import json,sys
d=json.load(sys.stdin)
for t in d.get("data",{}).get("activeTargets",[]):
    print(t.get("labels",{}).get("job"), t.get("health"), t.get("lastError",""))' >&2 || true
  "${COMPOSE[@]}" logs --tail 80 bench dashboard "$ENGINE" >&2 || true
  echo "stack left up; tear down with: ${COMPOSE[*]} --profile $ENGINE down -v" >&2
  exit 1
fi

echo "series check passed ($ENGINE, $SCENARIO)"
for item in "${checks[@]}"; do
  rest="${item#*|}"
  name="${rest%%|*}"
  query="${rest#*|}"
  body="$(prom "$query")"
  echo "  $name: $(KIND=nonempty BODY="$body" python3 -c 'import json,os; d=json.loads(os.environ["BODY"]); print(d["data"]["result"])')"
done

if [[ "$KEEP" -eq 1 ]]; then
  echo "left running. Grafana http://localhost:3000"
  echo "stop with: ${COMPOSE[*]} --profile $ENGINE down"
  exit 0
fi

down
echo "stopped"
