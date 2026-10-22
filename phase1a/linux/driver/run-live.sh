#!/usr/bin/env bash
# Tail envoy-log-generator files into sysbench.logs_live on the Linux rig.
#
# Start the generator first (Docker on the driver is fine) so ENVOY_LOG_DIR has files:
#
#   cd envoy-log-generator && ./run.sh
#   ENVOY_LOG_DIR=$PWD/logs phase1a/linux/driver/run-live.sh
#
# Do not run this at the same time as replay.sh.

set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"

: "${ENVOY_LOG_DIR:=${REPO_ROOT}/envoy-log-generator/logs}"
export ENVOY_LOG_DIR

log() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*" >&2; }

MISSING=()
command -v vector >/dev/null || MISSING+=("vector")
command -v clickhouse-client >/dev/null || MISSING+=("clickhouse-client")
[[ ${#MISSING[@]} -eq 0 ]] || { echo "missing dependencies: ${MISSING[*]}" >&2; exit 1; }
[[ -d "$ENVOY_LOG_DIR" ]] || { echo "log dir missing: $ENVOY_LOG_DIR" >&2; exit 1; }

log "creating sysbench.logs_live on ${NODE_SUT}"
ch --queries-file "${CH_SQL_DIR}/ddl-envoy.sql"

log "vector tails ${ENVOY_LOG_DIR} -> http://${NODE_SUT}:${CH_PORT} sysbench.logs_live"
exec vector --config "${VECTOR_DIR}/live-envoy.yaml"
