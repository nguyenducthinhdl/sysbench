#!/usr/bin/env bash
# Last step before the formal benchmark.
#
# Writes a 2GiB JSONL file of contract events, then Vector ingests it into
# ClickHouse. The scenario does not start. bench_* counters are not incremented.
#
#   STORAGE_ENDPOINT=http://100.x.y.z:8123 CH_USER=admin CH_PASSWORD=admin ./preload.sh
#
# A failed row-count or async-insert check exits before any benchmark.

set -euo pipefail
cd "$(dirname "$0")"
# shellcheck disable=SC1091
source ./lib.sh
compose_cmd
require_generator_env

BYTES="${BYTES:-2147483648}"
TIMEOUT="${PRELOAD_TIMEOUT:-1800}"
SCENARIO="${SCENARIO:-test-scenario-01.yaml}"
export STORAGE_ENDPOINT CH_USER CH_PASSWORD
export VECTOR_CONFIG="$(cd .. && pwd)/vector/preload.yaml"

require_async
before="$(ch_query "SELECT count() FROM syscon.logs FORMAT TabSeparated")"
async_before="$(ch_query "$(async_sql)")"
echo "syscon.logs rows before preload=${before} async_insert_log=${async_before}"

echo "building the generator image"
"${COMPOSE[@]}" --profile write build writer

echo "writing ${BYTES} bytes to preload.jsonl"
write_out="$("${COMPOSE[@]}" --profile write run --rm writer \
  write --out /data/preload.jsonl --bytes "$BYTES" --scenario "/scenarios/${SCENARIO}")"
printf '%s\n' "$write_out"
events="$(printf '%s\n' "$write_out" | awk -F= '/^events=/{print $2}')"
bytes="$(printf '%s\n' "$write_out" | awk -F= '/^bytes=/{print $2}')"
if [[ -z "${events}" || -z "${bytes}" || "$bytes" -lt "$BYTES" ]]; then
  echo "write did not reach ${BYTES} bytes" >&2
  exit 1
fi

echo "ingesting preload.jsonl through Vector"
"${COMPOSE[@]}" up -d --force-recreate --no-deps vector

rows=""
async=""
deadline=$((SECONDS + TIMEOUT))
while (( SECONDS < deadline )); do
  rows="$(ch_query "SELECT count() FROM syscon.logs FORMAT TabSeparated" || true)"
  async="$(ch_query "$(async_sql)" || true)"
  if [[ -n "$rows" && -n "$async" ]] && (( rows >= before + events && async > async_before )); then
    break
  fi
  sleep 5
done

if [[ -z "$rows" || -z "$async" ]] || ! (( rows >= before + events && async > async_before )); then
  echo "preload check failed: rows=${rows:-empty} want>=$((before + events)) async_insert_log=${async:-empty} want>${async_before}" >&2
  "${COMPOSE[@]}" logs --tail 80 vector >&2 || true
  exit 1
fi

printf 'events=%s\nbytes=%s\nrows=%s\nasync_inserts=%s\n' "$events" "$bytes" "$rows" "$async" | \
  "${COMPOSE[@]}" --profile write run --rm --entrypoint /bin/sh -T writer -c 'cat > /data/preload.ok'
"${COMPOSE[@]}" stop vector >/dev/null
echo "preload ok: events=${events} bytes=${bytes} rows=${rows} async_inserts=${async}"
echo "scenario 01 has not started"
