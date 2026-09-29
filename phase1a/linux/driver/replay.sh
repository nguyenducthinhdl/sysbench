#!/usr/bin/env bash
# Phase 1A run harness. One invocation = one run = one line in results/phase1a.ndjson.
#
#   phase1a/linux/driver/replay.sh <arm> <repetition> [throttled|unthrottled]
#
#   arm:        a1 a2s a2m a2l a3w1 a3w0 a4 b1 b2
#   repetition: 1..3 (fairness rule 4: three reps, report median and p95)
#   mode:       throttled (default, 50k events/s) or unthrottled (max throughput)
#
# Runs from the driver node. Requires passwordless ssh to $NODE_SUT and $NODE_INFRA for cold
# start and CPU accounting.
#
# Linux rig only. The macOS smoke test is phase1a/macos/driver/replay.sh, and its numbers
# are not comparable with these. See phase1a-clickhouse-ingest.md.

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

# --- Preflight -----------------------------------------------------------------------------
# Every check here has cost someone a wasted run somewhere.
log "preflight"
MISSING=()
for dep in vector pv clickhouse-client jq ssh awk; do
  command -v "$dep" >/dev/null || MISSING+=("$dep")
done
if ! command -v sha256sum >/dev/null && ! command -v shasum >/dev/null; then
  MISSING+=("sha256sum|shasum")
fi
# Report all of them at once. Discovering four missing tools one run at a time is four cold
# starts wasted.
[[ ${#MISSING[@]} -eq 0 ]] || { echo "missing dependencies: ${MISSING[*]}" >&2; exit 1; }

# The driver must be Linux. Checked here rather than being discovered mid-run: macOS date has
# no %3N, so WALL_0 would become "17906744013N" and the arithmetic would blow up AFTER the cold
# start and most of the replay. See the local-mode note in phase1a-clickhouse-ingest.md.
[[ "$(date +%3N)" != "3N" ]] || {
  echo "need GNU date with %3N (driver node must be Linux, or use gdate from coreutils)" >&2; exit 1; }
[[ -d /proc ]] || { echo "no /proc: the driver node must be Linux" >&2; exit 1; }

# Identical input bytes, every run, every arm (fairness rule 5).
actual_sha=$(sha256_file "$CORPUS")
[[ "$actual_sha" == "$CORPUS_SHA256" ]] || {
  echo "corpus checksum mismatch: $actual_sha != $CORPUS_SHA256" >&2; exit 1; }

vector validate --no-environment --config "$CFG" >/dev/null || {
  echo "vector config invalid: $CFG" >&2; exit 1; }

mkdir -p "$(dirname "$RESULTS")" /var/log/vector

# --- Cold start (fairness rule 3) ----------------------------------------------------------
log "cold start"
ch --queries-file "${CH_SQL_DIR}/reset.sql"
ch --queries-file "${CH_SQL_DIR}/ddl.sql"
[[ "$ASYNC_INSERT" == true ]] && ch --queries-file "${CH_SQL_DIR}/async-insert.sql"

ssh "$NODE_SUT" 'systemctl restart clickhouse-server && sleep 5 && sync && echo 3 > /proc/sys/vm/drop_caches'
# reset.sql/ddl.sql ran before the restart, so re-assert the table survived it.
[[ "$(ch --query 'EXISTS TABLE sysbench.logs_hdfs')" == "1" ]] || {
  echo "table missing after restart" >&2; exit 1; }

if [[ "$KAFKA" == true ]]; then
  log "recreating kafka topic $KAFKA_TOPIC"
  ssh "$NODE_INFRA" "kafka-topics.sh --bootstrap-server localhost:9092 --delete --topic ${KAFKA_TOPIC} 2>/dev/null || true; \
    kafka-topics.sh --bootstrap-server localhost:9092 --create --topic ${KAFKA_TOPIC} \
      --partitions ${KAFKA_PARTITIONS} --replication-factor 1"
  # b1 consumes with ClickHouse's Kafka engine; b2 consumes with a second Vector, started
  # separately. Running both would put two consumer groups on one topic and double-ingest.
  # kafka-engine.sql carries __KAFKA_BROKER__ so the same file serves the rig (infra:9092)
  # and the macOS compose network (kafka:9092).
  [[ "$ARM" == "b1" ]] && sed "s|__KAFKA_BROKER__|${KAFKA_BROKER}|" "${CH_SQL_DIR}/kafka-engine.sql" | ch --multiquery
fi

RUN_START=$(ch --query "SELECT toString(now())")
CLOCK_OFFSET=$(clock_offset_ms)
log "run_start=$RUN_START clock_offset=${CLOCK_OFFSET}ms"

CH_CPU_0=$(cpu_seconds_remote "$NODE_SUT" "$CH_CGROUP")
KAFKA_CPU_0=0
[[ "$KAFKA" == true ]] && KAFKA_CPU_0=$(cpu_seconds_remote "$NODE_INFRA" "$KAFKA_CGROUP")

# --- Run -----------------------------------------------------------------------------------
STREAM=$(mktemp -u /tmp/phase1a-stream.XXXXXX); mkfifo "$STREAM"
CANARY_PID=""; VECTOR_PID=""; FEED_PID=""
cleanup() {
  [[ -n "$CANARY_PID" ]] && kill "$CANARY_PID" 2>/dev/null || true
  [[ -n "$FEED_PID"   ]] && kill "$FEED_PID"   2>/dev/null || true
  [[ -n "$VECTOR_PID" ]] && kill "$VECTOR_PID" 2>/dev/null || true
  rm -f "$STREAM"
}
trap cleanup EXIT

log "starting vector ($ARM, $MODE)"
PHASE1A_CH_USER="$CH_USER" vector --config "$CFG" < "$STREAM" &
VECTOR_PID=$!

# Wait for the canary socket before emitting, or the first canaries are silently refused and
# the latency percentiles are computed over a truncated sample.
for _ in $(seq 1 30); do
  (exec 3<>"/dev/tcp/127.0.0.1/${CANARY_PORT}") 2>/dev/null && break
  sleep 1
done

"$(dirname "${BASH_SOURCE[0]}")/canary.sh" "$CANARY_PORT" & CANARY_PID=$!

WALL_0=$(date +%s%3N)
if [[ "$ARM" == "a1" ]]; then
  # A1 stopping rule: 10 minutes or 200k events, whichever comes first. There is no
  # completion time for this arm and we must not wait for one.
  log "a1: capped at ${A1_MAX_EVENTS} events / ${A1_MAX_SECONDS}s"
  ( timeout "$A1_MAX_SECONDS" head -n "$A1_MAX_EVENTS" "$CORPUS" > "$STREAM" || true ) &
elif [[ "$MODE" == "throttled" ]]; then
  ( pv -l -L "$REPLAY_RATE" "$CORPUS" > "$STREAM" ) &
else
  ( cat "$CORPUS" > "$STREAM" ) &
fi
FEED_PID=$!
wait "$FEED_PID" || true
FEED_PID=""
log "feed complete, draining"

# Drain: wait for the expected row count rather than for a fixed sleep. A fixed sleep either
# truncates a slow arm or pads a fast one, and both show up as wrong throughput.
#
# A1 is exempt: it has no target row count, so waiting for one would burn the full drain
# timeout on every repetition. It gets a short settle instead and reports what landed.
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
WALL_1=$(date +%s%3N)

VECTOR_CPU=$(cpu_seconds_pid "$VECTOR_PID")
kill "$CANARY_PID" 2>/dev/null || true; CANARY_PID=""
kill "$VECTOR_PID" 2>/dev/null || true; VECTOR_PID=""

# Let merges settle before measuring on-disk size, or storage efficiency is measured mid-merge
# and A1 in particular looks far worse than it is.
log "waiting for merges to settle"
for _ in $(seq 1 60); do
  active=$(ch --query "SELECT count() FROM system.merges WHERE database='sysbench'")
  [[ "$active" == "0" ]] && break
  sleep 5
done

CH_CPU=$(num "$(cpu_seconds_remote "$NODE_SUT" "$CH_CGROUP")" "$CH_CPU_0")
KAFKA_CPU=0
[[ "$KAFKA" == true ]] && KAFKA_CPU=$(num "$(cpu_seconds_remote "$NODE_INFRA" "$KAFKA_CGROUP")" "$KAFKA_CPU_0")

# --- Collect -------------------------------------------------------------------------------
log "collecting"
METRICS=$(ch --param_run_start="$RUN_START" --param_clock_offset_ms="$CLOCK_OFFSET" \
             --queries-file "${REPO_ROOT}/phase1a/collect.sql")

PARSE_FAILURES=$(wc -l < /var/log/vector/phase1a-parse-failures.log 2>/dev/null || echo 0)
WALL_SEC=$(num "$(( WALL_1 - WALL_0 ))" 1000 /)
ROWS=$(jq -r .rows_actual <<<"$METRICS")

# GB is scaled by rows actually ingested, not by the corpus size. Only A1 ever differs, but
# without this A1's cpu_sec_per_gb would be divided by 1.58 GB after sending ~28 MB and would
# look like the most efficient arm in the matrix.
GB=$(awk -v b="$CORPUS_BYTES" -v r="$ROWS" -v t="$CORPUS_ROWS" \
     'BEGIN { printf "%.6f", (b * (r / t)) / 1000000000 }')

jq -c -n \
  --argjson m "$METRICS" \
  --arg arm "$ARM" --arg pipeline "$PIPELINE" --arg mode "$MODE" \
  --arg cfg "$(cat "$CFG")" --arg ch_user "$CH_USER" \
  --argjson rep "$REP" --argjson wall "$WALL_SEC" \
  --argjson vcpu "${VECTOR_CPU:-0}" --argjson kcpu "${KAFKA_CPU:-0}" --argjson ccpu "${CH_CPU:-0}" \
  --argjson gb "$GB" --argjson rows "$ROWS" --argjson expected "$CORPUS_ROWS" \
  --argjson async "$ASYNC_INSERT" --argjson waitasync "$WAIT_ASYNC" --argjson kafka "$KAFKA" \
  --argjson rate "$([[ "$MODE" == throttled ]] && echo "$REPLAY_RATE" || echo null)" \
  --argjson pf "$PARSE_FAILURES" \
  --arg sha "$CORPUS_SHA256" --arg start "$RUN_START" --argjson offset "$CLOCK_OFFSET" \
  '{
    phase: "1a", engine: "clickhouse", tier: "T1", host: "linux",
    arm: $arm, pipeline: $pipeline, repetition: $rep, cache_state: "cold",
    mode: $mode, replay_rate_events_per_sec: $rate,
    async_insert: $async, wait_for_async_insert: $waitasync, kafka: $kafka,
    rows_expected: $expected, rows_actual: $rows, row_delta: ($rows - $expected),
    parse_failures: $pf,
    wall_time_sec: $wall,
    events_per_sec: (if $wall > 0 then ($rows / $wall | floor) else null end),
    cpu_sec_per_gb: {
      vector: ($vcpu / $gb), kafka: ($kcpu / $gb), clickhouse: ($ccpu / $gb),
      total: (($vcpu + $kcpu + $ccpu) / $gb)
    },
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
    non_default_config: { vector_config: $cfg, clickhouse_user: $ch_user }
  }' >> "$RESULTS"

log "done: rows=$ROWS/$CORPUS_ROWS parse_failures=$PARSE_FAILURES -> $RESULTS"

# acked_but_lost and duplicates stay null here: they are only meaningful under the failure
# drills, which write their own records. See phase1a/linux/DRILLS.md.
if [[ "$ROWS" != "$CORPUS_ROWS" && "$ARM" != "a1" ]]; then
  log "WARNING: row count gate FAILED. This run is a loss/duplication finding, not a timing result."
fi
