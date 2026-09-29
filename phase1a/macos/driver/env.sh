#!/usr/bin/env bash
# macOS smoke-test configuration. Sourced, not executed.
#
# ClickHouse and Vector run in Docker. There is no ssh, no cgroup, and no page-cache drop,
# so this file has none of the Linux rig's CPU accounting.

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
MACOS_DIR="${REPO_ROOT}/phase1a/macos"
COMPOSE_FILE="${MACOS_DIR}/docker-compose.yml"

: "${CORPUS:=${REPO_ROOT}/data/HDFS.log}"
CORPUS_SHA256="0783096174d7832c618337f9609e06e04abd86ddd7089b3c12b407e63bfebc52"
CORPUS_ROWS=11175629
CORPUS_BYTES=1577982906

: "${REPLAY_RATE:=50000}"
: "${A1_MAX_EVENTS:=200000}"
: "${A1_MAX_SECONDS:=600}"
: "${DRAIN_TIMEOUT:=900}"
: "${CANARY_PORT:=9001}"          # host side of vector's 9000; 9000 is ClickHouse native

: "${RESULTS:=${REPO_ROOT}/results/phase1a-macos.ndjson}"
: "${VECTOR_DIR:=${MACOS_DIR}/vector}"
: "${CH_SQL_DIR:=${REPO_ROOT}/phase1a/clickhouse}"
: "${KAFKA_BROKER:=kafka:9092}"
: "${KAFKA_TOPIC:=phase1a-hdfs}"
: "${KAFKA_PARTITIONS:=4}"

# Homebrew on this Mac ships `docker-compose` as its own binary. `docker compose` (the plugin)
# is not installed, and calling it makes the docker CLI reject the rest of the command.
if docker compose version >/dev/null 2>&1; then
  compose() { docker compose -f "$COMPOSE_FILE" "$@"; }
elif command -v docker-compose >/dev/null 2>&1; then
  compose() { docker-compose -f "$COMPOSE_FILE" "$@"; }
else
  compose() { echo "need docker compose or docker-compose" >&2; return 127; }
fi

# The client is inside the container. --queries-file paths must be the mounted /sql tree,
# not paths on the Mac.
ch() { compose exec -T clickhouse clickhouse-client "$@"; }

sha256_file() { shasum -a 256 "$1" | awk '{print $1}'; }

now_ms() { python3 -c 'import time; print(int(time.time() * 1000))'; }

num() { awk -v a="$1" -v b="${2:-0}" -v op="${3:--}" 'BEGIN { printf "%.3f", (op == "-" ? a - b : a / b) }'; }

clock_offset_ms() {
  local t0 t1 server mid
  t0=$(now_ms)
  server=$(ch --query "SELECT toUnixTimestamp64Milli(now64(3))")
  t1=$(now_ms)
  mid=$(( (t0 + t1) / 2 ))
  echo $(( server - mid ))
}

wait_clickhouse() {
  local i
  for i in $(seq 1 60); do
    ch --query "SELECT 1" >/dev/null 2>&1 && return 0
    sleep 1
  done
  echo "clickhouse did not become ready" >&2
  return 1
}
