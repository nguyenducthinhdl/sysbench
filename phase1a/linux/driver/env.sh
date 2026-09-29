#!/usr/bin/env bash
# Shared configuration for the Phase 1A driver scripts. Sourced, not executed.
#
# Everything environment-specific lives here so the run scripts contain no hostnames.
# Override any of these in the shell before calling replay.sh.

# --- Topology (benchmark-plan.md section 2) ---
: "${NODE_SUT:=node-1}"          # ClickHouse
: "${NODE_INFRA:=infra}"         # Kafka
: "${CH_PORT:=8123}"
: "${KAFKA_BOOTSTRAP:=${NODE_INFRA}:9092}"
: "${KAFKA_TOPIC:=phase1a-hdfs}"
: "${KAFKA_PARTITIONS:=4}"

# cgroup paths used for CPU accounting. Both must be cgroup v2 directories containing
# cpu.stat. ClickHouse is capped at 16 cores / 64GB here per fairness rule 2.
: "${CH_CGROUP:=/sys/fs/cgroup/system.slice/clickhouse-server.service}"
: "${KAFKA_CGROUP:=/sys/fs/cgroup/system.slice/kafka.service}"

# --- Input (phase1a-clickhouse-ingest.md section 2) ---
# driver/ lives at phase1a/linux/driver, so the repo root is three levels up.
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
: "${CORPUS:=${REPO_ROOT}/data/HDFS.log}"
CORPUS_SHA256="0783096174d7832c618337f9609e06e04abd86ddd7089b3c12b407e63bfebc52"
CORPUS_ROWS=11175629
CORPUS_BYTES=1577982906

# --- Run parameters (phase1a-clickhouse-ingest.md section 4) ---
: "${REPLAY_RATE:=50000}"        # events/s for throttled runs
: "${A1_MAX_EVENTS:=200000}"     # A1 stopping rule
: "${A1_MAX_SECONDS:=600}"
: "${DRAIN_TIMEOUT:=900}"        # how long to wait for the last row to land
: "${CANARY_PORT:=9000}"

# --- Output ---
: "${RESULTS:=${REPO_ROOT}/results/phase1a.ndjson}"
: "${VECTOR_DIR:=${REPO_ROOT}/phase1a/linux/vector}"
: "${CH_SQL_DIR:=${REPO_ROOT}/phase1a/clickhouse}"
: "${KAFKA_BROKER:=infra:9092}"

ch() { clickhouse-client --host "$NODE_SUT" --port 9000 "$@"; }

# sha256sum is canonical on minimal Linux; shasum is what macOS and full distros have.
sha256_file() {
  if command -v sha256sum >/dev/null; then sha256sum "$1" | awk '{print $1}';
  else shasum -a 256 "$1" | awk '{print $1}'; fi
}

# CPU seconds consumed by a cgroup, read locally or over ssh. cgroup v2 reports usage_usec.
#
# printf "%.3f" throughout rather than awk's default OFMT: %.6g switches to scientific
# notation above 1e6, and "1.23457e+06" is not something jq --argjson will accept.
#
# Both functions FAIL rather than returning 0 when they cannot read their source. A wrong
# cgroup path or a missing /proc would otherwise produce a run reporting 0 CPU-seconds per GB,
# and since CPU per GB is the metric the Kafka decision turns on, a silent zero is far worse
# than a stopped run.
cpu_seconds_remote() {
  local host="$1" cg="$2" out
  out=$(ssh "$host" "awk '/^usage_usec/ {printf \"%.3f\", \$2 / 1000000}' '${cg}/cpu.stat'" 2>/dev/null) || {
    echo "cpu accounting failed: cannot read ${cg}/cpu.stat on ${host}" >&2; return 1; }
  [[ -n "$out" ]] || {
    echo "cpu accounting failed: no usage_usec in ${cg}/cpu.stat on ${host} (cgroup v1?)" >&2; return 1; }
  echo "$out"
}

# CPU seconds for a local process, from /proc. utime + stime in clock ticks.
cpu_seconds_pid() {
  local pid="$1" tck
  tck=$(getconf CLK_TCK)
  [[ -r "/proc/${pid}/stat" ]] || {
    echo "cpu accounting failed: /proc/${pid}/stat unreadable (driver must be Linux)" >&2; return 1; }
  awk -v tck="$tck" '{printf "%.3f", ($14 + $15) / tck}' "/proc/${pid}/stat"
}

# Arithmetic that is always valid JSON. bc prints ".5" for one half, and jq --argjson
# rejects it, so every number crossing into the result record goes through this.
num() { awk -v a="$1" -v b="${2:-0}" -v op="${3:--}" 'BEGIN { printf "%.3f", (op == "-" ? a - b : a / b) }'; }

# Offset between the driver clock and the SUT clock, in milliseconds, estimated by
# sandwiching a server-side now64(3) between two local reads. Canary latency is a
# cross-machine millisecond measurement, so this is not optional bookkeeping - an unnoticed
# 200ms skew would silently become 200ms of "visibility latency".
clock_offset_ms() {
  local t0 t1 server mid
  t0=$(date +%s%3N)
  server=$(ch --query "SELECT toUnixTimestamp64Milli(now64(3))")
  t1=$(date +%s%3N)
  mid=$(( (t0 + t1) / 2 ))
  echo $(( server - mid ))
}
