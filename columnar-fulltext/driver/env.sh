#!/usr/bin/env bash
# Sourced by gate.sh. Paths and host ports for the one-engine harness.

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="${REPO_ROOT}/columnar-fulltext/docker-compose.yml"
CORPUS_DIR="${REPO_ROOT}/data/corpus"
CH_HTTP="${CH_HTTP:-http://127.0.0.1:18123}"
ES_HTTP="${ES_HTTP:-http://127.0.0.1:19200}"

if docker compose version >/dev/null 2>&1; then
  compose() { docker compose -f "$COMPOSE_FILE" "$@"; }
elif command -v docker-compose >/dev/null 2>&1; then
  compose() { docker-compose -f "$COMPOSE_FILE" "$@"; }
else
  compose() { echo "need docker compose or docker-compose" >&2; return 127; }
fi
