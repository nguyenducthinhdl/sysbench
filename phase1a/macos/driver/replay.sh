#!/usr/bin/env bash
# Phase 1A on a Mac, via Docker. One invocation = one line in results/phase1a-macos.ndjson.
#
#   phase1a/macos/driver/replay.sh <arm> <repetition> [throttled|unthrottled]
#
# Smoke test only. Row count, parts, and canary latency are real. CPU-seconds per GB is
# null, and the page cache is not dropped, so these records must not be compared with
# phase1a/linux or copied into the handbook sizing table.

set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"

ARM="${1:?arm required}"
REP="${2:?repetition required}"
MODE="${3:-throttled}"

CFG=""; CH_USER="default"; KAFKA=false; PIPELINE="vector-direct"
ASYNC_INSERT=false; WAIT_ASYNC="null"
case "$ARM" in
  a1)   CFG=a1-nobatch.yaml ;;
  a2s)  CFG=a2s-batch-1mb.yaml ;;
  a2m)  CFG=a2m-batch-10mb.yaml ;;
  a2l)  CFG=a2l-batch-100mb.yaml ;;
  a3w1) CFG=a3-async.yaml; CH_USER=phase1a_async_w1; ASYNC_INSERT=true; WAIT_ASYNC=1 ;;
  a3w0) CFG=a3-async.yaml; CH_USER=phase1a_async_w0; ASYNC_INSERT=true; WAIT_ASYNC=0 ;;
  a4)   CFG=a4-diskbuffer.yaml ;;
  b1)   CFG=b1-kafka-producer.yaml; KAFKA=true; PIPELINE="vector-kafka-chengine" ;;
  b2)   CFG=b1-kafka-producer.yaml; KAFKA=true; PIPELINE="vector-kafka-vectorconsumer" ;;
  *)    echo "unknown arm: $ARM" >&2; exit 2 ;;
esac
CFG="${VECTOR_DIR}/${CFG}"

log() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*" >&2; }

log "preflight"
MISSING=()
for dep in docker python3 jq shasum awk; do
  command -v "$dep" >/dev/null || MISSING+=("$dep")
done
if ! docker compose version >/dev/null 2>&1 && ! command -v docker-compose >/dev/null 2>&1; then
  MISSING+=("docker-compose")
fi
[[ ${#MISSING[@]} -eq 0 ]] || { echo "missing dependencies: ${MISSING[*]}" >&2; exit 1; }
docker info >/dev/null 2>&1 || { echo "docker is not running (start Colima or Docker Desktop)" >&2; exit 1; }

actual_sha=$(sha256_file "$CORPUS")
[[ "$actual_sha" == "$CORPUS_SHA256" ]] || {
  echo "corpus checksum mismatch: $actual_sha != $CORPUS_SHA256" >&2; exit 1; }
[[ -f "$CFG" ]] || { echo "missing vector config: $CFG" >&2; exit 1; }

mkdir -p "$(dirname "$RESULTS")" "${MACOS_DIR}/logs"
: > "${MACOS_DIR}/logs/phase1a-parse-failures.log" || true

log "starting clickhouse"
compose up -d clickhouse
wait_clickhouse

log "resetting table"
ch --queries-file /sql/reset.sql
ch --queries-file /sql/ddl.sql
[[ "$ASYNC_INSERT" == true ]] && ch --queries-file /sql/async-insert.sql
# Restart the process so the run does not inherit the previous arm's caches in the server.
# This is not a page-cache drop; the host still has HDFS.log cached. Recorded as cache_state
# "docker" for that reason.
compose restart clickhouse
wait_clickhouse
[[ "$(ch --query 'EXISTS TABLE sysbench.logs_hdfs')" == "1" ]] || {
  echo "table missing after restart" >&2; exit 1; }

if [[ "$KAFKA" == true ]]; then
  log "starting kafka"
  compose --profile kafka up -d kafka
  # apache/kafka and bitnami put the script in different places; try both.
  for _ in $(seq 1 30); do
    compose exec -T kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --list >/dev/null 2>&1 && break
    compose exec -T kafka kafka-topics.sh --bootstrap-server localhost:9092 --list >/dev/null 2>&1 && break
    sleep 2
  done
  compose exec -T kafka sh -c '
    TOPICS=/opt/kafka/bin/kafka-topics.sh
    [ -x "$TOPICS" ] || TOPICS=kafka-topics.sh
    $TOPICS --bootstrap-server localhost:9092 --delete --topic '"$KAFKA_TOPIC"' >/dev/null 2>&1 || true
    $TOPICS --bootstrap-server localhost:9092 --create --topic '"$KAFKA_TOPIC"' \
      --partitions '"$KAFKA_PARTITIONS"' --replication-factor 1
  '
  if [[ "$ARM" == "b1" ]]; then
    sed "s|__KAFKA_BROKER__|${KAFKA_BROKER}|" "${CH_SQL_DIR}/kafka-engine.sql" | ch --multiquery
  else
    # b2: a second Vector consumes. Detached, and without --service-ports so it does not
    # take the canary port from the producer.
    docker rm -f phase1a-consumer >/dev/null 2>&1 || true
    compose run --rm --no-deps -d --name phase1a-consumer \
      vector --config /etc/vector/b2-kafka-consumer.yaml
  fi
fi

RUN_START=$(ch --query "SELECT toString(now())")
CLOCK_OFFSET=$(clock_offset_ms)
log "run_start=$RUN_START clock_offset=${CLOCK_OFFSET}ms"

STREAM=$(mktemp -u /tmp/phase1a-stream.XXXXXX); mkfifo "$STREAM"
CANARY_PID=""; VECTOR_PID=""; FEED_PID=""
cleanup() {
  [[ -n "$CANARY_PID" ]] && kill "$CANARY_PID" 2>/dev/null || true
  [[ -n "$FEED_PID"   ]] && kill "$FEED_PID"   2>/dev/null || true
  docker rm -f phase1a-vector >/dev/null 2>&1 || true
  [[ "$ARM" == "b2" ]] && docker rm -f phase1a-consumer >/dev/null 2>&1 || true
  rm -f "$STREAM"
}
trap cleanup EXIT

docker rm -f phase1a-vector >/dev/null 2>&1 || true
log "starting vector ($ARM, $MODE)"
# The fifo open blocks until the writer connects, so start the reader in the background
# and only then open the writer.
PHASE1A_CH_USER="$CH_USER" compose run --rm --service-ports --no-deps -T \
  --name phase1a-vector \
  -e PHASE1A_CH_USER="$CH_USER" \
  vector --config "/etc/vector/$(basename "$CFG")" < "$STREAM" &
VECTOR_PID=$!

FEED_ARGS=("$CORPUS")
if [[ "$ARM" == "a1" ]]; then
  FEED_ARGS+=(--max-events "$A1_MAX_EVENTS" --max-seconds "$A1_MAX_SECONDS")
  log "a1: capped at ${A1_MAX_EVENTS} events / ${A1_MAX_SECONDS}s"
elif [[ "$MODE" == "throttled" ]]; then
  FEED_ARGS+=(--rate "$REPLAY_RATE")
fi
python3 "${MACOS_DIR}/driver/feed.py" "${FEED_ARGS[@]}" > "$STREAM" &
FEED_PID=$!

ready=0
for _ in $(seq 1 60); do
  if python3 -c "import socket; socket.create_connection(('127.0.0.1', ${CANARY_PORT})).close()" 2>/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
[[ "$ready" == 1 ]] || { echo "vector did not open the canary port ${CANARY_PORT}" >&2; exit 1; }
python3 "${MACOS_DIR}/driver/canary.py" 127.0.0.1 "$CANARY_PORT" &
CANARY_PID=$!

WALL_0=$(now_ms)
wait "$FEED_PID" || true
FEED_PID=""
log "feed complete, draining"

if [[ "$ARM" == "a1" ]]; then
  sleep 30
else
  deadline=$(( $(date +%s) + DRAIN_TIMEOUT ))
  while :; do
    got=$(ch --query "SELECT count() FROM sysbench.logs_hdfs WHERE Component != 'sysbench.Canary'")
    [[ "$got" -ge "$CORPUS_ROWS" ]] && break
    [[ $(date +%s) -ge $deadline ]] && { log "drain timeout at ${got} rows"; break; }
    sleep 2
  done
fi
WALL_1=$(now_ms)

kill "$CANARY_PID" 2>/dev/null || true; CANARY_PID=""
docker rm -f phase1a-vector >/dev/null 2>&1 || true
VECTOR_PID=""

log "waiting for merges to settle"
for _ in $(seq 1 60); do
  active=$(ch --query "SELECT count() FROM system.merges WHERE database='sysbench'")
  [[ "$active" == "0" ]] && break
  sleep 5
done

log "collecting"
METRICS=$(ch --param_run_start="$RUN_START" --param_clock_offset_ms="$CLOCK_OFFSET" \
             --queries-file /collect.sql)

PARSE_FAILURES=$(wc -l < "${MACOS_DIR}/logs/phase1a-parse-failures.log" | tr -d ' ')
WALL_SEC=$(num "$(( WALL_1 - WALL_0 ))" 1000 /)
ROWS=$(jq -r .rows_actual <<<"$METRICS")

jq -c -n \
  --argjson m "$METRICS" \
  --arg arm "$ARM" --arg pipeline "$PIPELINE" --arg mode "$MODE" \
  --arg cfg "$(cat "$CFG")" --arg ch_user "$CH_USER" \
  --argjson rep "$REP" --argjson wall "$WALL_SEC" \
  --argjson rows "$ROWS" --argjson expected "$CORPUS_ROWS" \
  --argjson async "$ASYNC_INSERT" --argjson waitasync "$WAIT_ASYNC" --argjson kafka "$KAFKA" \
  --argjson rate "$([[ "$MODE" == throttled ]] && echo "$REPLAY_RATE" || echo null)" \
  --argjson pf "$PARSE_FAILURES" \
  --arg sha "$CORPUS_SHA256" --arg start "$RUN_START" --argjson offset "$CLOCK_OFFSET" \
  '{
    phase: "1a", engine: "clickhouse", tier: "T1", host: "macos",
    arm: $arm, pipeline: $pipeline, repetition: $rep, cache_state: "docker",
    mode: $mode, replay_rate_events_per_sec: $rate,
    async_insert: $async, wait_for_async_insert: $waitasync, kafka: $kafka,
    rows_expected: $expected, rows_actual: $rows, row_delta: ($rows - $expected),
    parse_failures: $pf,
    wall_time_sec: $wall,
    events_per_sec: (if $wall > 0 then ($rows / $wall | floor) else null end),
    cpu_sec_per_gb: null,
    visible_latency_ms: { p50: $m.visible_latency_p50_ms, p99: $m.visible_latency_p99_ms },
    canary_rows: $m.canary_rows,
    parts_created: $m.parts_created, merges: $m.merges, merge_seconds: $m.merge_seconds,
    active_parts: $m.active_parts, partitions: $m.partitions,
    on_disk_bytes: $m.on_disk_bytes,
    too_many_parts_errors: $m.too_many_parts_errors,
    async_insert_flushes: $m.async_insert_flushes,
    kafka_messages_read: $m.kafka_messages_read,
    kafka_last_exception: $m.kafka_last_exception,
    acked_but_lost: null, duplicates: null,
    run_start: $start, clock_offset_ms: $offset,
    corpus_sha256: $sha,
    non_default_config: { vector_config: $cfg, clickhouse_user: $ch_user },
    notes: "macos docker smoke test; cpu_sec_per_gb omitted; page cache not dropped"
  }' >> "$RESULTS"

log "done: rows=$ROWS/$CORPUS_ROWS parse_failures=$PARSE_FAILURES -> $RESULTS"
if [[ "$ROWS" != "$CORPUS_ROWS" && "$ARM" != "a1" ]]; then
  log "WARNING: row count gate FAILED. This run is a loss/duplication finding, not a timing result."
fi
