#!/usr/bin/env bash
# Canary emitter. Started by replay.sh, killed by replay.sh.
#
# Emits one line per second into Vector's canary socket, in the real HDFS line format so it
# goes through the same VRL transform and the same sink batch as the bulk replay.
#
# Ingest-to-visible latency is NOT measured by polling. The table's
# IngestedAt DateTime64(3) DEFAULT now64(3) is evaluated server-side during insert
# processing, so the latency of each canary row is simply
#
#     IngestedAt - (the emit timestamp carried in Body)
#
# computed in collect.sql, corrected for the driver/SUT clock offset. That is more precise
# than any polling interval and it costs no queries during the run, which matters because a
# polling loop would itself add load to the thing being measured.
#
# The line carries date 081111 and time 111628 - the corpus's last event - on purpose. Using
# today's date would create a fourth partition and change the merge behaviour being measured.
# Pid 0 and Component sysbench.Canary make these rows excludable by predicate.

set -euo pipefail

PORT="${1:-9000}"
HOST="${2:-127.0.0.1}"

# Requires GNU date for %3N. The rig is Linux; this fails loudly on macOS rather than
# silently emitting a literal "3N".
if [[ "$(date +%3N)" == "3N" ]]; then
  echo "canary.sh: need GNU date with %3N support" >&2
  exit 1
fi

exec 3<>"/dev/tcp/${HOST}/${PORT}"
trap 'exec 3>&-' EXIT

while :; do
  printf '081111 111628 0 INFO sysbench.Canary: zzqcanary %s\n' "$(date +%s%3N)" >&3
  sleep 1
done
