#!/bin/sh
# The checked-in scenario posts client metrics to http://localhost:8001.
# Forward that port to the dashboard container, then wait until the engine
# answers before the generator starts.
set -eu

if [ -n "${DASHBOARD_UPSTREAM:-}" ]; then
  socat TCP-LISTEN:8001,fork,reuseaddr TCP:"${DASHBOARD_UPSTREAM}" &
fi

if [ -n "${STORAGE_READY:-}" ]; then
  i=0
  while [ "$i" -lt 180 ]; do
    if wget -q -O /dev/null "${STORAGE_READY}" 2>/dev/null; then
      break
    fi
    i=$((i + 1))
    sleep 2
  done
  if [ "$i" -ge 180 ]; then
    echo "storage not ready: ${STORAGE_READY}" >&2
    exit 1
  fi
fi

set -- /usr/local/bin/syscon-bench run \
  --scenario "/scenarios/${SCENARIO:-test-scenario-01.yaml}" \
  --fields /data/fields.csv \
  --messages /data/messages.txt \
  --storage-name "${STORAGE_NAME:?STORAGE_NAME is required}" \
  --storage-endpoint "${STORAGE_ENDPOINT:?STORAGE_ENDPOINT is required}"

if [ "${VIA:-}" = "vector" ]; then
  set -- "$@" --via vector --vector-endpoint "${VECTOR_ENDPOINT:-http://127.0.0.1:8686}"
fi

exec "$@"
