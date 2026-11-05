#!/usr/bin/env bash
# Install ClickHouse on this Linux VM and print the Tailscale HTTP endpoint.
#
# Run it over SSH, from this directory:
#
#   CH_USER=admin CH_PASSWORD=admin ./install-clickhouse.sh
#
# CH_USER and CH_PASSWORD must equal the scenario file's storage auth.
# The user gets durable async_insert. The table syscon.logs is left empty.
# This script does not start the generator.

set -euo pipefail
cd "$(dirname "$0")"

: "${CH_USER:?set CH_USER to the scenario auth username}"
: "${CH_PASSWORD:?set CH_PASSWORD to the scenario auth password}"

if [[ ! "$CH_USER" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
  echo "CH_USER must be letters, digits, and underscores" >&2
  exit 1
fi
case "$CH_PASSWORD" in
  *$' '*|*$'\n'*|*$'\t'*|*'\\'*|*'{'*|*'\$'*|*'}'*|*':'*)
    echo "CH_PASSWORD cannot contain whitespace, backslash, colon, dollar, or braces" >&2
    exit 1
    ;;
esac

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi
if ! command -v tailscale >/dev/null 2>&1; then
  echo "tailscale is required on this VM" >&2
  exit 1
fi
TS_IP="$(tailscale ip -4 | head -n 1)"
if [[ -z "$TS_IP" ]]; then
  echo "tailscale has no IPv4 address" >&2
  exit 1
fi

if docker compose version >/dev/null 2>&1; then
  COMPOSE=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE=(docker-compose)
else
  echo "docker compose is required" >&2
  exit 1
fi

export CH_USER CH_PASSWORD
echo "starting ClickHouse"
"${COMPOSE[@]}" up -d

echo "waiting for ClickHouse"
ready=0
for _ in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:8123/ping >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 2
done
if [[ "$ready" -ne 1 ]]; then
  echo "ClickHouse did not become ready" >&2
  "${COMPOSE[@]}" logs --tail 80 clickhouse >&2 || true
  exit 1
fi

sql="$(mktemp)"
trap 'rm -f "$sql"' EXIT
cat clickhouse/ddl.sql >"$sql"
cat >>"$sql" <<EOF
ALTER USER ${CH_USER} SETTINGS
    async_insert = 1,
    wait_for_async_insert = 1,
    async_insert_max_data_size = 10000000,
    async_insert_busy_timeout_ms = 1000;
GRANT INSERT, SELECT ON syscon.* TO ${CH_USER};
GRANT SELECT ON system.asynchronous_insert_log TO ${CH_USER};
EOF

docker exec -i syscon-clickhouse clickhouse-client \
  --user "$CH_USER" --password "$CH_PASSWORD" --multiquery <"$sql"

async="$(docker exec -i syscon-clickhouse clickhouse-client \
  --user "$CH_USER" --password "$CH_PASSWORD" \
  --query "SELECT value FROM system.settings WHERE name = 'async_insert'")"
waitf="$(docker exec -i syscon-clickhouse clickhouse-client \
  --user "$CH_USER" --password "$CH_PASSWORD" \
  --query "SELECT value FROM system.settings WHERE name = 'wait_for_async_insert'")"
async="$(printf '%s' "$async" | tr -d '[:space:]')"
waitf="$(printf '%s' "$waitf" | tr -d '[:space:]')"
if [[ "$async" != "1" || "$waitf" != "1" ]]; then
  echo "async_insert=${async:-empty} wait_for_async_insert=${waitf:-empty}; expected 1 and 1" >&2
  exit 1
fi

rows="$(docker exec -i syscon-clickhouse clickhouse-client \
  --user "$CH_USER" --password "$CH_PASSWORD" \
  --query "SELECT count() FROM syscon.logs")"
rows="$(printf '%s' "$rows" | tr -d '[:space:]')"
echo "syscon.logs rows=${rows}"
echo "http://${TS_IP}:8123"
