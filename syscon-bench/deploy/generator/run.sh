#!/usr/bin/env bash
# Formal benchmark. A missing 2GiB preload is a warning, not a stop.
#
#   STORAGE_ENDPOINT=http://100.x.y.z:8123 CH_USER=admin CH_PASSWORD=admin ./run.sh
#
# Scenario 01 is not shortened. Client series are pushed to the dashboard
# receiver. This script appends the measured block to docs/phase2.md.

set -euo pipefail
cd "$(dirname "$0")"
# shellcheck disable=SC1091
source ./lib.sh
compose_cmd
require_generator_env

export STORAGE_ENDPOINT CH_USER CH_PASSWORD
export SCENARIO="${SCENARIO:-test-scenario-01.yaml}"
export VECTOR_CONFIG="$(cd .. && pwd)/vector/bench.yaml"
DOCS="$(cd ../.. && pwd)/docs/phase2.md"

preload_note="Preload stamp is missing. The 2GiB file was not loaded."
if STAMP="$(read_stamp)"; then
  stamp_rows="$(stamp_field rows)"
  stamp_events="$(stamp_field events)"
  stamp_bytes="$(stamp_field bytes)"
  stamp_async="$(stamp_field async_inserts)"
  if [[ -z "$stamp_rows" || -z "$stamp_bytes" || -z "$stamp_async" ]]; then
    echo "warning: preload stamp is incomplete. Continuing without the 2GiB preload." >&2
  else
    rows="$(ch_query "SELECT count() FROM syscon.logs FORMAT TabSeparated" || true)"
    async="$(ch_query "$(async_sql)" || true)"
    if [[ -z "$rows" || -z "$async" ]] || (( rows < stamp_rows || async < 1 )); then
      echo "warning: ClickHouse does not hold the preload (rows=${rows:-empty} stamp=${stamp_rows} async_insert_log=${async:-empty}). Continuing." >&2
      preload_note="Preload stamp says ${stamp_bytes} bytes and ${stamp_rows} rows, but ClickHouse reported ${rows:-empty} rows."
    else
      echo "preload still present: rows=${rows} async_insert_log=${async}"
      preload_note="Preload file: ${stamp_bytes} bytes, ${stamp_events} events. ClickHouse reported ${stamp_rows} rows and ${stamp_async} async-insert log rows before scenario 01."
    fi
  fi
else
  echo "warning: preload stamp is missing. The 2GiB preload was not loaded. Continuing." >&2
fi

write_prometheus
echo "building the generator image"
"${COMPOSE[@]}" --profile bench build bench
echo "starting Vector, the dashboard receiver, Prometheus, and Grafana"
"${COMPOSE[@]}" up -d --force-recreate --no-deps vector
"${COMPOSE[@]}" up -d dashboard prometheus grafana

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
  "${COMPOSE[@]}" logs --tail 40 prometheus >&2 || true
  exit 1
fi

echo "waiting for the dashboard receiver"
dash_ready=0
for _ in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:8001/health >/dev/null 2>&1; then
    dash_ready=1
    break
  fi
  sleep 1
done
if [[ "$dash_ready" -ne 1 ]]; then
  echo "dashboard receiver did not become ready" >&2
  "${COMPOSE[@]}" logs --tail 40 dashboard >&2 || true
  exit 1
fi

echo "running ${SCENARIO} through Vector"
set +e
"${COMPOSE[@]}" --profile bench run --rm bench
rc=$?
set -e

sleep 15
prom() {
  curl -fsS -G http://127.0.0.1:9090/api/v1/query --data-urlencode "query=$1" || true
}
sample() {
  local body="$1"
  BODY="$body" python3 - <<'PY' || true
import json, os
raw = os.environ.get("BODY", "")
try:
    data = json.loads(raw)
except json.JSONDecodeError:
    print("missing")
else:
    result = data.get("data", {}).get("result", [])
    if not result:
        print("missing")
    else:
        value = result[0].get("value") or []
        print(value[1] if len(value) > 1 else "missing")
PY
}

ingest="$(sample "$(prom 'sum(bench_ingest_events_total)')")"
queries="$(sample "$(prom 'sum(bench_query_duration_seconds_count)')")"
sent="$(sample "$(prom 'sum(vector_component_sent_events_total{component_kind="sink"})')")"
inserted="$(sample "$(prom 'max(ClickHouseProfileEvents_InsertedRows)')")"
memory="$(sample "$(prom 'max(ClickHouseAsyncMetrics_MemoryResident)')")"

missing=""
[[ "$ingest" == "missing" ]] && missing="${missing}bench_ingest_events_total "
[[ "$queries" == "missing" ]] && missing="${missing}bench_query_duration_seconds_count "
[[ "$sent" == "missing" ]] && missing="${missing}vector_component_sent_events_total "
[[ "$memory" == "missing" && "$inserted" == "missing" ]] && missing="${missing}clickhouse "

{
  echo
  echo "### $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo
  echo "$preload_note"
  echo
  echo "Scenario process exit ${rc}. Live series after the run:"
  echo
  echo "- bench_ingest_events_total=${ingest} (events Vector accepted; the contract integral for scenario 01 is 360000 when every push succeeds, and 60 query ticks)"
  echo "- bench_query_duration_seconds_count=${queries}"
  echo "- vector_component_sent_events_total=${sent} (this Vector process only; the preload process was recreated)"
  echo "- ClickHouseProfileEvents_InsertedRows=${inserted} (server counter; it includes the preload as well as the live run)"
  echo "- ClickHouseAsyncMetrics_MemoryResident=${memory}"
  echo
  echo "bench_ingest_events_total counts batches the sidecar accepted. That 2xx can arrive before async_insert flushes. ClickHouse inserted rows are the stored count, and the difference is the agent path: batching, retry, and lag. Filter queries use the live half-open window of 60 seconds, so the preload file is the store under test for full-text and exact match."
  if [[ -n "$missing" ]]; then
    echo
    echo "Series check failed, missing: ${missing}"
  fi
} >>"$DOCS"

if [[ "$rc" -ne 0 ]]; then
  echo "scenario exited ${rc}. The note was appended to ${DOCS}" >&2
  exit "$rc"
fi
if [[ -n "$missing" ]]; then
  echo "series check failed, missing: ${missing}. The note was appended to ${DOCS}" >&2
  exit 1
fi
echo "formal run finished. Grafana http://localhost:3000"
echo "note appended to ${DOCS}"
