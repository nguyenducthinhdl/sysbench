# Shared helpers for preload.sh and run.sh. Source this file; do not execute it.

compose_cmd() {
  if docker compose version >/dev/null 2>&1; then
    COMPOSE=(docker compose)
  elif command -v docker-compose >/dev/null 2>&1; then
    COMPOSE=(docker-compose)
  else
    echo "docker compose is required" >&2
    return 1
  fi
}

require_generator_env() {
  : "${STORAGE_ENDPOINT:?set STORAGE_ENDPOINT to http://<tailscale-ip>:8123}"
  : "${CH_USER:?set CH_USER to the scenario auth username}"
  : "${CH_PASSWORD:?set CH_PASSWORD to the scenario auth password}"
  case "$STORAGE_ENDPOINT" in
    http://*|https://*) ;;
    *) echo "STORAGE_ENDPOINT must be an absolute http or https URL" >&2; return 1 ;;
  esac
  case "$CH_PASSWORD" in
    *$' '*|*$'\n'*|*$'\t'*|*':'*)
      echo "CH_PASSWORD cannot contain whitespace or a colon" >&2
      return 1
      ;;
  esac
  if ! command -v curl >/dev/null 2>&1; then
    echo "curl is required" >&2
    return 1
  fi
  if ! command -v python3 >/dev/null 2>&1; then
    echo "python3 is required" >&2
    return 1
  fi
}

ch_host() {
  python3 - <<'PY'
import os
from urllib.parse import urlparse
print(urlparse(os.environ["STORAGE_ENDPOINT"]).hostname or "")
PY
}

ch_query() {
  local sql="$1"
  curl -fsS --max-time 60 --user "${CH_USER}:${CH_PASSWORD}" \
    "${STORAGE_ENDPOINT%/}/" \
    -H 'Content-Type: text/plain; charset=utf-8' \
    --data-binary "$sql" | tr -d '[:space:]'
}

async_sql() {
  printf '%s\n' "SELECT count() FROM system.asynchronous_insert_log WHERE database = 'syscon' AND \`table\` = 'logs' FORMAT TabSeparated"
}

require_async() {
  local ai wf
  ai="$(ch_query "SELECT value FROM system.settings WHERE name = 'async_insert' FORMAT TabSeparated")"
  wf="$(ch_query "SELECT value FROM system.settings WHERE name = 'wait_for_async_insert' FORMAT TabSeparated")"
  if [[ "$ai" != "1" || "$wf" != "1" ]]; then
    echo "async_insert=${ai:-empty} wait_for_async_insert=${wf:-empty}; expected 1 and 1 for ${CH_USER}" >&2
    return 1
  fi
}

write_prometheus() {
  local host
  host="$(ch_host)"
  if [[ -z "$host" ]]; then
    echo "STORAGE_ENDPOINT has no host" >&2
    return 1
  fi
  sed "s/__CH_HOST__/${host}/g" prometheus.yml.tmpl > prometheus.generated.yml
}

read_stamp() {
  "${COMPOSE[@]}" --profile write run --rm --entrypoint /bin/sh -T writer -c 'cat /data/preload.ok'
}

stamp_field() {
  local key="$1"
  printf '%s\n' "$STAMP" | awk -F= -v k="$key" '$1 == k { print $2 }'
}
