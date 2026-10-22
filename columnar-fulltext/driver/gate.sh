#!/usr/bin/env bash
# Load the 1 GB prefix into every arm, run the count gate, freeze the queries.
#
#   columnar-fulltext/driver/gate.sh
#
# One engine at a time. ClickHouse is :18123, Elasticsearch is :19200.

set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
exec python3 "${REPO_ROOT}/columnar-fulltext/driver/gate.py" "$@"
