#!/usr/bin/env bash
# Start envoy-log-generator and ingest its files into ClickHouse via Vector.
#
#   phase1a/macos/run-live.sh
#   SMOKE=1 phase1a/macos/run-live.sh    # 60s ramps, same as the generator smoke
#
# Grafana: http://localhost:3000  (Envoy log generator, Live generator logs)
# Envoy:   http://localhost:10000
# SQL:     SELECT * FROM sysbench.logs_live
#
# Do not run this at the same time as replay.sh: both want host port 9598.
# Stop a standalone envoy-log-generator stack first (ports 10000 and 9901).

set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/driver/env.sh"

LIVE_FILE="${MACOS_DIR}/docker-compose.live.yml"
if docker compose version >/dev/null 2>&1; then
  live() { docker compose -f "$LIVE_FILE" "$@"; }
elif command -v docker-compose >/dev/null 2>&1; then
  live() { docker-compose -f "$LIVE_FILE" "$@"; }
else
  echo "need docker compose or docker-compose" >&2
  exit 1
fi

if [ "${SMOKE:-0}" = "1" ]; then
  export RPS_WINDOW=60s
  export ERROR_WINDOW=60s
  export PANIC_DELAYS=75s,80s
fi

log() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*" >&2; }

log "preflight"
MISSING=()
for dep in docker python3; do
  command -v "$dep" >/dev/null || MISSING+=("$dep")
done
[[ ${#MISSING[@]} -eq 0 ]] || { echo "missing dependencies: ${MISSING[*]}" >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo "docker is not running (start Colima or Docker Desktop)" >&2; exit 1; }

mkdir -p "${REPO_ROOT}/envoy-log-generator/logs" "${MACOS_DIR}/logs"

log "starting clickhouse + prometheus + grafana"
live up -d clickhouse prometheus grafana
wait_clickhouse

log "creating sysbench.logs_live"
ch --queries-file /sql/ddl-envoy.sql

log "starting generator + vector-live"
live up -d --build \
  payments booking users orders foods envoy client-1 client-2 vector-live

log "waiting for vector-live"
for _ in $(seq 1 30); do
  if live exec -T vector-live /usr/bin/vector --version >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if ! live ps --status running --services | grep -qx vector-live; then
  echo "vector-live is not running" >&2
  live logs --tail 80 vector-live >&2 || true
  exit 1
fi

log "waiting for envoy"
ready=0
for _ in $(seq 1 90); do
  if curl -sf --max-time 2 http://127.0.0.1:9901/ready >/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
[[ "$ready" == 1 ]] || { echo "envoy did not become ready" >&2; live logs --tail 80 envoy >&2 || true; exit 1; }

log "waiting for rows in sysbench.logs_live"
rows=0
for _ in $(seq 1 30); do
  rows="$(ch --query 'SELECT count() FROM sysbench.logs_live' 2>/dev/null | tr -d '[:space:]')"
  rows="${rows:-0}"
  if [[ "$rows" -gt 0 ]]; then
    break
  fi
  sleep 1
done

echo
echo "generator logs -> Vector -> ClickHouse  (${rows} rows so far)"
echo "  Grafana     http://localhost:3000"
echo "  Envoy       http://localhost:10000   admin http://localhost:9901"
echo "  ClickHouse  http://localhost:8123"
echo "  SQL         SELECT count(), Source FROM sysbench.logs_live GROUP BY Source"
echo "stop with:  docker-compose -f phase1a/macos/docker-compose.live.yml down"
echo "            (or docker compose, same -f flag)"
