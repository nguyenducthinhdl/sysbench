#!/usr/bin/env bash
# Phase 3 matrix. Run this on the Mac. The VMs are on the LAN.
#
# VM01 is the log generator, Prometheus, and Grafana.
# VM02 runs ClickHouse or Loki, one at a time.
# VM03 runs Elasticsearch or VictoriaLogs, one at a time.
# Each storage VM has 8 GB of RAM, so the other storage VM is stopped and
# its data is deleted before a case starts.
#
#   ./run-matrix.sh
#   ./run-matrix.sh --engine elasticsearch --smoke
#   ./run-matrix.sh --dry-run
#
# SSH hosts are the names in ~/.ssh/config: VM01, VM02, and VM03.
# VM1 does not resolve. The storage address is tailscale ip -4 when
# that command exists, otherwise the IPv4 used to reach VM01.
# Free disk on the storage VM must be at least 8 GiB (8589934592 bytes).
#
# PRELOAD_ROWS overrides the 1 million row gate. The same JSONL is written
# once, copied to VM01, VM02, and VM03, and loaded before the hour.
# REMOTE_DIR is the repo path on each VM.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MIN_FREE_BYTES=8589934592
ROWS="${PRELOAD_ROWS:-1000000}"
# BENCH_SECONDS replaces the 60-minute hour with one recorded run of this length.
# REPORT_OUT is the markdown file that run appends, relative to the module root.
BENCH_SECONDS="${BENCH_SECONDS:-}"
REPORT_OUT="${REPORT_OUT:-docs/phase3-report.md}"
PRELOAD_FILE="${PRELOAD_FILE:-$ROOT/data/preload-1m.jsonl}"
PRELOAD_HOST_FILE="${PRELOAD_HOST_FILE:-../../data/$(basename "$PRELOAD_FILE")}"
VM1="${VM1:-VM01}"
VM2="${VM2:-VM02}"
VM3="${VM3:-VM03}"
REMOTE_DIR="${REMOTE_DIR:-syscon-bench}"
# A second generator uses another compose project and host ports. The defaults
# are the original board, so an already running generator is left in place.
COMPOSE_PROJECT="${COMPOSE_PROJECT:-syscon-bench}"
DASHBOARD_PORT="${DASHBOARD_PORT:-8001}"
PROMETHEUS_PORT="${PROMETHEUS_PORT:-9090}"
GRAFANA_PORT="${GRAFANA_PORT:-3000}"
PROM_FILE="${PROM_FILE:-prometheus.generated.yml}"
DRY=0
SMOKE_ONLY=0
ONLY_ENGINE=""
ONLY_SCENARIO=""

usage() {
  sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY=1 ;;
    --smoke) SMOKE_ONLY=1 ;;
    --engine)
      ONLY_ENGINE="${2:?--engine needs clickhouse, elasticsearch, loki, or victorialogs}"
      shift
      ;;
    --scenario)
      ONLY_SCENARIO="${2:?--scenario needs steady or spikes}"
      shift
      ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument $1" >&2; usage; exit 2 ;;
  esac
  shift
done

engines=(clickhouse elasticsearch loki victorialogs)
scenarios=(steady spikes)
if [[ -n "$ONLY_ENGINE" ]]; then
  engines=("$ONLY_ENGINE")
fi
if [[ -n "$ONLY_SCENARIO" ]]; then
  scenarios=("$ONLY_SCENARIO")
fi

if [[ "$DRY" -eq 1 ]]; then
  for engine in "${engines[@]}"; do
    for scenario in "${scenarios[@]}"; do
      file="phase3-${engine}-${scenario}.yaml"
      case "$engine" in
        clickhouse|loki) host="$VM2" ;;
        *) host="$VM3" ;;
      esac
      if [[ "$SMOKE_ONLY" -eq 1 ]]; then
        echo "${file} ${host} smoke"
      else
        echo "${file} ${host} smoke"
        echo "${file} ${host} official"
      fi
    done
  done
  exit 0
fi

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required" >&2
  exit 1
fi

on() {
  local host="$1"
  shift
  ssh "$host" "$@"
}

# The bench image and the scenario files are read on the VM, not on the Mac.
synced_hosts=" "
sync_tree() {
  local host="$1"
  case "$synced_hosts" in
    *" $host "*) return 0 ;;
  esac
  echo "syncing $ROOT to $host:$REMOTE_DIR"
  rsync -a -e ssh \
    --exclude '.git' \
    --exclude 'events.jsonl' \
    --exclude 'docs/phase3-report.md' \
    "$ROOT/" "$host:$REMOTE_DIR/"
  synced_hosts="$synced_hosts$host "
}

ensure_preload_file() {
  local lines=0
  if [[ -f "$PRELOAD_FILE" ]]; then
    lines="$(awk 'END { print NR }' "$PRELOAD_FILE")"
  fi
  if [[ "$lines" == "$ROWS" ]]; then
    echo "preload file $PRELOAD_FILE has $ROWS rows"
    return
  fi
  echo "writing $ROWS rows to $PRELOAD_FILE"
  mkdir -p "$(dirname "$PRELOAD_FILE")"
  (
    cd "$ROOT"
    go run ./cmd/syscon-bench write \
      --rows "$ROWS" \
      --out "$PRELOAD_FILE" \
      --scenario log-generator/scenarios/phase3-clickhouse-steady.yaml \
      --fields data/fields.csv \
      --messages data/messages.txt \
      --seed 1
  )
}

storage_host_for() {
  case "$1" in
    clickhouse|loki) echo "$VM2" ;;
    elasticsearch|victorialogs) echo "$VM3" ;;
  esac
}

# Address the generator and Prometheus use for this storage VM.
# Tailscale wins when it is installed. These hosts are on 192.168.100.0/24
# and do not have the tailscale command, so the fallback is the source
# address of the route toward the generator.
storage_addr() {
  local host="$1" toward="$2"
  on "$host" "toward='$toward' bash -s" <<'EOF' | tr -d '[:space:]'
if command -v tailscale >/dev/null 2>&1; then
  ip=$(tailscale ip -4 2>/dev/null | head -n 1 || true)
  if [ -n "$ip" ]; then
    printf '%s\n' "$ip"
    exit 0
  fi
fi
ip -4 route get "$toward" 2>/dev/null | awk '{
  for (i = 1; i <= NF; i++) if ($i == "src") { print $(i + 1); exit }
}'
EOF
}

free_bytes() {
  on "$1" "df -B1 --output=avail / | tail -1" | tr -d '[:space:]'
}

endpoint_for() {
  local ip="$1" engine="$2"
  case "$engine" in
    clickhouse) echo "http://${ip}:8123" ;;
    elasticsearch) echo "http://${ip}:9200" ;;
    loki) echo "http://${ip}:3100" ;;
    victorialogs) echo "http://${ip}:9428" ;;
  esac
}

ready_url() {
  local ip="$1" engine="$2"
  case "$engine" in
    clickhouse) echo "http://${ip}:8123/ping" ;;
    elasticsearch) echo "http://${ip}:9200/" ;;
    loki) echo "http://${ip}:3100/ready" ;;
    victorialogs) echo "http://${ip}:9428/health" ;;
  esac
}

storage_services() {
  case "$1" in
    clickhouse) echo clickhouse cadvisor ;;
    elasticsearch) echo elasticsearch elasticsearch-exporter cadvisor ;;
    loki) echo loki cadvisor ;;
    victorialogs) echo victorialogs cadvisor ;;
  esac
}

scenario_file() {
  local engine="$1" scenario="$2"
  echo "phase3-${engine}-${scenario}.yaml"
}

# measure_disk is the engine's own data directory, in bytes.
# The same command runs before preload and after the hour. Disk added is the difference.
# clickhouse: /var/lib/clickhouse
# elasticsearch: /usr/share/elasticsearch/data
# loki: /tmp/loki
# victorialogs: /vlogs
measure_disk() {
  local host="$1" engine="$2" name path bytes
  case "$engine" in
    clickhouse) name=syscon-bench-clickhouse-1; path=/var/lib/clickhouse ;;
    elasticsearch) name=syscon-bench-elasticsearch-1; path=/usr/share/elasticsearch/data ;;
    loki) name=syscon-bench-loki-1; path=/tmp/loki ;;
    victorialogs) name=syscon-bench-victorialogs-1; path=/vlogs ;;
    *) echo "unknown engine $engine" >&2; return 1 ;;
  esac
  # A missing du must not abort the caller: VictoriaLogs is distroless.
  bytes="$(on "$host" "docker exec -u 0 '$name' du -sb '$path' 2>/dev/null" | awk 'NR==1 && $1 ~ /^[0-9]+$/ { print $1 }')" || true
  if [[ -n "$bytes" ]]; then
    printf '%s\n' "$bytes"
    return 0
  fi
  if [[ "$engine" != "victorialogs" ]]; then
    return 1
  fi
  on "$host" "docker run --rm --entrypoint du -v syscon-bench_vl-data:/data:ro alpine:3.22 -sb /data" | awk 'NR==1 && $1 ~ /^[0-9]+$/ { print $1 }'
}

# Each storage VM has 8 GB of RAM. Stop every engine and delete its data
# before the next case, including a Phase 2 ClickHouse left under deploy/vm.
clear_storage() {
  local host="$1"
  echo "clearing database containers and data on ${host}"
  on "$host" "REMOTE_DIR='$REMOTE_DIR' bash -s" <<'EOF'
if ! command -v docker >/dev/null 2>&1; then
  echo "skip: docker is not installed"
  exit 0
fi
if [ ! -d "$REMOTE_DIR/deploy/docker" ]; then
  echo "skip: $REMOTE_DIR/deploy/docker is absent"
  exit 0
fi
cd "$REMOTE_DIR/deploy/docker" && docker compose --profile clickhouse --profile elasticsearch --profile loki --profile victorialogs down -v --remove-orphans || true
if [ -f "$REMOTE_DIR/deploy/vm/docker-compose.yml" ]; then
  docker compose -f "$REMOTE_DIR/deploy/vm/docker-compose.yml" -p syscon-bench-vm down -v --remove-orphans || true
fi
if [ -f "$REMOTE_DIR/deploy/generator/docker-compose.yml" ]; then
  docker compose -f "$REMOTE_DIR/deploy/generator/docker-compose.yml" -p syscon-bench-generator down -v --remove-orphans || true
fi
ids=$(docker ps -aq --filter name='clickhouse|elastic|loki|victoria|vector' || true)
if [ -n "$ids" ]; then
  docker rm -f $ids || true
fi
docker volume ls -q | grep -E '(ch-data|es-data|vl-data|preload)$' | while read -r vol; do
  docker volume rm "$vol" || true
done
exit 0
EOF
}

# The Phase 2 generator project on VM01 publishes the same host ports the
# Phase 3 board needs: dashboard 8001, Prometheus 9090, Grafana 3000.
free_observability() {
  if [[ "$DASHBOARD_PORT" != "8001" || "$PROMETHEUS_PORT" != "9090" || "$GRAFANA_PORT" != "3000" ]]; then
    echo "leaving the existing generator on ports 8001, 9090, and 3000"
    return 0
  fi
  echo "freeing ports 8001, 9090, and 3000 on $VM1"
  on "$VM1" "REMOTE_DIR='$REMOTE_DIR' bash -s" <<'EOF'
if [ -f "$REMOTE_DIR/deploy/generator/docker-compose.yml" ]; then
  docker compose -f "$REMOTE_DIR/deploy/generator/docker-compose.yml" -p syscon-bench-generator down --remove-orphans || true
fi
ids=$(docker ps --format '{{.ID}} {{.Ports}}' | awk '/0\.0\.0\.0:(8001|9090|3000)->/ { print $1 }')
if [ -n "$ids" ]; then
  docker rm -f $ids || true
fi
exit 0
EOF
}

wait_ready() {
  local host="$1" url="$2"
  on "$host" "for i in \$(seq 1 180); do curl -fsS '$url' >/dev/null 2>&1 && exit 0; sleep 2; done; exit 1"
}

prom_local() {
  echo "http://127.0.0.1:${PROM_LOCAL_PORT:-19090}"
}

# The local forward is only for the Mac report after an official hour.
# A smoke run queries Prometheus over SSH on VM01, so it does not bind 19090.
# Reuse a listener left by an earlier run instead of failing the bind.
ensure_prom_tunnel() {
  local port="${PROM_LOCAL_PORT:-19090}"
  if echo >/dev/tcp/127.0.0.1/"$port" 2>/dev/null; then
    return 0
  fi
  ssh -f -N -o ExitOnForwardFailure=yes -L "127.0.0.1:${port}:127.0.0.1:${PROMETHEUS_PORT}" "$VM1"
}

run_bench() {
  local engine="$1" scenario="$2" duration="$3" preload="$4" endpoint="$5" ready="$6"
  local file
  file="$(scenario_file "$engine" "$scenario")"
  on "$VM1" "cd '$REMOTE_DIR/deploy/docker' && \
    STORAGE_NAME='$engine' STORAGE_ENDPOINT='$endpoint' STORAGE_READY='$ready' \
    SCENARIO='$file' DURATION='$duration' PRELOAD_ROWS='$preload' \
    MESSAGES_FILE=../../data/messages.txt \
    PRELOAD_HOST_FILE='$PRELOAD_HOST_FILE' \
    PROM_CONFIG=../phase3/$PROM_FILE \
    DASHBOARD_PORT='$DASHBOARD_PORT' PROMETHEUS_PORT='$PROMETHEUS_PORT' GRAFANA_PORT='$GRAFANA_PORT' \
    docker compose -p '$COMPOSE_PROJECT' --profile '$engine' run --rm --build --no-deps bench"
}

# Load the shared JSONL on the storage VM, over the compose network.
run_preload() {
  local host="$1" engine="$2" scenario="$3"
  local file endpoint ready
  file="$(scenario_file "$engine" "$scenario")"
  endpoint="$(endpoint_for "$engine" "$engine")"
  ready="$(ready_url "$engine" "$engine")"
  on "$host" "cd '$REMOTE_DIR/deploy/docker' && \
    STORAGE_NAME='$engine' STORAGE_ENDPOINT='$endpoint' STORAGE_READY='$ready' \
    SCENARIO='$file' PRELOAD_ROWS='$ROWS' PRELOAD_PATH=/data/preload.jsonl \
    PRELOAD_BATCH=1000 \
    PRELOAD_HOST_FILE='$PRELOAD_HOST_FILE' \
    MESSAGES_FILE=../../data/messages.txt \
    docker compose --profile '$engine' run --rm --build --no-deps bench"
}

smoke_series() {
  on "$VM1" "python3 - <<'PY'
import json, sys, urllib.parse, urllib.request
base = 'http://127.0.0.1:${PROMETHEUS_PORT}'
queries = [
    'sum(rate(bench_ingest_events_total[2m]))',
    'bench_query_duration_seconds_count',
    'sum(rate(container_cpu_usage_seconds_total[2m]))',
    'sum(container_memory_working_set_bytes)',
]
for q in queries:
    url = base + '/api/v1/query?' + urllib.parse.urlencode({'query': q})
    try:
        with urllib.request.urlopen(url, timeout=20) as resp:
            data = json.load(resp)
    except Exception:
        sys.exit(1)
    result = data.get('data', {}).get('result') or []
    if not result:
        print('missing series for', q, file=sys.stderr)
        sys.exit(1)
    if 'bench_' in q:
        seen = False
        for series in result:
            value = series.get('value') or []
            if len(value) > 1 and float(value[1]) > 0:
                seen = True
        if not seen:
            print('empty series for', q, file=sys.stderr)
            sys.exit(1)
sys.exit(0)
PY"
}

GEN_IP="$(ssh -G "$VM1" | awk '$1=="hostname" { print $2; exit }')"
if [[ -z "$GEN_IP" ]]; then
  echo "cannot resolve $VM1" >&2
  exit 1
fi

ensure_preload_file
sync_tree "$VM1"
sync_tree "$VM2"
sync_tree "$VM3"

for engine in "${engines[@]}"; do
  case "$engine" in
    clickhouse|elasticsearch|loki|victorialogs) ;;
    *) echo "unknown engine $engine" >&2; exit 2 ;;
  esac
  storage_host="$(storage_host_for "$engine")"
  for scenario in "${scenarios[@]}"; do
    case "$scenario" in
      steady|spikes) ;;
      *) echo "unknown scenario $scenario" >&2; exit 2 ;;
    esac
    echo "=== $engine $scenario on $storage_host ==="
    clear_storage "$VM2"
    clear_storage "$VM3"
    avail="$(free_bytes "$storage_host")"
    if [[ -z "$avail" || "$avail" -lt "$MIN_FREE_BYTES" ]]; then
      echo "free disk on $storage_host is ${avail:-0} bytes, below ${MIN_FREE_BYTES}" >&2
      exit 1
    fi
    storage_ip="$(storage_addr "$storage_host" "$GEN_IP")"
    if [[ -z "$storage_ip" ]]; then
      echo "$storage_host has no IPv4 address reachable from $VM1 ($GEN_IP)" >&2
      exit 1
    fi
    endpoint="$(endpoint_for "$storage_ip" "$engine")"
    ready="$(ready_url "$storage_ip" "$engine")"
    local_ready="$(ready_url "127.0.0.1" "$engine")"
    sed "s/__STORAGE_HOST__/${storage_ip}/g" "$ROOT/deploy/phase3/prometheus.yml.tmpl" \
      | on "$VM1" "cat > '$REMOTE_DIR/deploy/phase3/$PROM_FILE'"
    # shellcheck disable=SC2086
    on "$storage_host" "cd '$REMOTE_DIR/deploy/docker' && docker compose --profile '$engine' up -d $(storage_services "$engine")"
    if ! wait_ready "$storage_host" "$local_ready"; then
      echo "$engine on $storage_host did not become ready" >&2
      exit 1
    fi
    echo "generator $COMPOSE_PROJECT on $VM1 dashboard=$DASHBOARD_PORT prometheus=$PROMETHEUS_PORT grafana=$GRAFANA_PORT"
    free_observability
    # A stopped Prometheus can come back with no network endpoint, which
    # leaves its host port closed. Recreate this project’s board only.
    on "$VM1" "cd '$REMOTE_DIR/deploy/docker' && \
      DASHBOARD_PORT='$DASHBOARD_PORT' PROMETHEUS_PORT='$PROMETHEUS_PORT' GRAFANA_PORT='$GRAFANA_PORT' \
      PROM_CONFIG=../phase3/$PROM_FILE \
      docker compose -p '$COMPOSE_PROJECT' --profile '$engine' up -d --build --force-recreate dashboard prometheus grafana"
    if ! on "$VM1" "for i in \$(seq 1 60); do curl -fsS http://127.0.0.1:${PROMETHEUS_PORT}/-/ready >/dev/null 2>&1 && exit 0; sleep 2; done; exit 1"; then
      echo "prometheus on $VM1 is not listening on port ${PROMETHEUS_PORT}" >&2
      exit 1
    fi
    disk_before="$(measure_disk "$storage_host" "$engine")"
    if [[ -z "$disk_before" ]]; then
      echo "disk measurement on $storage_host did not report bytes before preload" >&2
      exit 1
    fi
    echo "disk before preload on $engine: $disk_before"
    echo "preloading $PRELOAD_FILE into $engine on $storage_host"
    preload_log="$(mktemp)"
    set +e
    run_preload "$storage_host" "$engine" "$scenario" | tee "$preload_log"
    preload_rc="${PIPESTATUS[0]}"
    set -e
    rows="$(awk -F= '/^rows=/{print $2}' "$preload_log" | tail -1)"
    rm -f "$preload_log"
    if [[ "$preload_rc" -ne 0 || -z "$rows" || "$rows" -lt "$ROWS" ]]; then
      echo "preload stored ${rows:-0} rows, want $ROWS" >&2
      exit 1
    fi
    if [[ -z "$rows" || "$rows" -lt "$ROWS" ]]; then
      echo "preload stored ${rows:-0} rows, want $ROWS" >&2
      exit 1
    fi
    disk_preload="$(measure_disk "$storage_host" "$engine")"
    if [[ -z "$disk_preload" ]]; then
      echo "disk measurement on $storage_host did not report bytes after preload" >&2
      exit 1
    fi
    echo "disk after preload on $engine: $disk_preload"

    if [[ -n "$BENCH_SECONDS" ]]; then
      echo "benchmark ${BENCH_SECONDS}s $engine $scenario"
      start="$(date +%s)"
      run_bench "$engine" "$scenario" "$BENCH_SECONDS" "" "$endpoint" "$ready"
      end="$(date +%s)"
    else
      echo "smoke $engine $scenario"
      set +e
      run_bench "$engine" "$scenario" 180 "" "$endpoint" "$ready" &
      smoke_pid=$!
      set -e
      # The bench image build sits inside this wait. Keep polling until the
      # 180s run ends, then read Prometheus once more after the last push.
      deadline=$((SECONDS + 900))
      ok=0
      while (( SECONDS < deadline )); do
        if smoke_series; then
          ok=1
        fi
        if ! kill -0 "$smoke_pid" 2>/dev/null; then
          break
        fi
        sleep 5
      done
      set +e
      wait "$smoke_pid"
      smoke_rc=$?
      set -e
      if smoke_series; then
        ok=1
      fi
      if [[ "$ok" -ne 1 || "$smoke_rc" -ne 0 ]]; then
        echo "smoke failed for $engine $scenario" >&2
        exit 1
      fi
      if [[ "$SMOKE_ONLY" -eq 1 ]]; then
        echo "smoke passed for $engine $scenario"
        continue
      fi

      echo "official hour $engine $scenario"
      start="$(date +%s)"
      run_bench "$engine" "$scenario" "" "" "$endpoint" "$ready"
      end="$(date +%s)"
    fi
    disk_used="$(measure_disk "$storage_host" "$engine")"
    if [[ -z "$disk_used" ]]; then
      echo "disk measurement on $storage_host did not report bytes after the hour" >&2
      exit 1
    fi
    echo "disk after hour on $engine: $disk_used added $((disk_used - disk_before))"
    ensure_prom_tunnel
    (
      cd "$ROOT"
      go run ./cmd/syscon-bench report \
        --prom "$(prom_local)" \
        --engine "$engine" \
        --scenario "phase3-${engine}-${scenario}" \
        --rows "$rows" \
        --start "$start" \
        --end "$end" \
        --disk-used "$disk_used" \
        --disk-preload "${disk_preload:--1}" \
        --disk-before "${disk_before:--1}" \
        --out "$REPORT_OUT"
    )
    echo "recorded $engine phase3-${scenario}"
  done
  clear_storage "$VM2"
  clear_storage "$VM3"
done

