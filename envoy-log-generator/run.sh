#!/usr/bin/env bash
# Start the generator. SMOKE=1 shortens the ramps, checks the logs, then stops.
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p logs

if docker compose version >/dev/null 2>&1; then
  compose() { docker compose "$@"; }
elif command -v docker-compose >/dev/null 2>&1; then
  compose() { docker-compose "$@"; }
else
  echo "need docker compose or docker-compose" >&2
  exit 1
fi

# The 109 token bucket absorbs a short peak. A 60s ramp from 100 to 120
# spends enough time above 109 to empty that burst. Panics are scheduled
# after that, so both clients are still up while the limiter sheds traffic.
if [ "${SMOKE:-0}" = "1" ]; then
  export RPS_WINDOW=60s
  export ERROR_WINDOW=60s
  export PANIC_DELAYS=75s,80s
fi

compose down --remove-orphans
rm -f logs/*.log
compose up -d --build --force-recreate

if [ "${SMOKE:-0}" != "1" ]; then
  echo "running. logs are in $(pwd)/logs"
  echo "stop with: docker compose down    or    docker-compose down"
  exit 0
fi

echo "waiting for envoy"
ready=0
for _ in $(seq 1 90); do
  if curl -sf http://127.0.0.1:9901/ready >/dev/null; then
    ready=1
    break
  fi
  sleep 1
done
if [ "$ready" != 1 ]; then
  echo "envoy did not become ready" >&2
  compose logs --tail 80 envoy >&2 || true
  exit 1
fi

echo "smoke: waiting 85s for the ramp, 429s, 500s, and a panic"
sleep 85

if ! python3 - <<'PY'
import pathlib, re, sys
root = pathlib.Path("logs")

def text(name):
    path = root / name
    if not path.exists() or path.stat().st_size == 0:
        sys.exit(f"missing or empty {path}")
    return path.read_text(errors="replace")

clients = text("client-1.log") + "\n" + text("client-2.log")
vals = [float(v) for v in re.findall(r"per_route=([0-9.]+)", clients)]
if len(vals) < 10:
    sys.exit(f"too few pace samples: {len(vals)}")
if min(vals) > 105 or max(vals) < 112:
    sys.exit(f"ramp not observed min={min(vals):.2f} max={max(vals):.2f}")
print(f"ramp samples={len(vals)} min={min(vals):.2f} max={max(vals):.2f}")

stamp = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}Z ", re.M)
for name in ("client-1.log", "client-2.log", "payments.log", "booking.log"):
    body = text(name)
    if stamp.search(body) is None:
        sys.exit(f"{name} missing timestamp")
    if " INFO " not in body:
        sys.exit(f"{name} missing INFO")

if "POST /payments ->" not in clients or "POST /booking ->" not in clients:
    sys.exit("clients did not record both routes")
if " DEBUG " not in clients:
    sys.exit("clients missing DEBUG")

access = text("envoy-access.log")
if "local_rate_limited" not in access:
    sys.exit("access log has no local_rate_limited 429")
if "/payments" not in access or "/booking" not in access:
    sys.exit("access log missing a route")

backends = text("payments.log") + "\n" + text("booking.log")
if "goroutine" not in backends:
    sys.exit("backend log missing stack trace")
if "scheduled panic" not in clients and "scheduled panic" not in backends:
    sys.exit("no scheduled panic record")

envoy = text("envoy.log")
if "[debug]" not in envoy or "[info]" not in envoy:
    sys.exit("envoy log missing debug or info")
if "goroutine" not in envoy:
    sys.exit("envoy log missing upstream stack trace")
print("log checks ok")
PY
then
  echo "smoke log check failed" >&2
  compose ps >&2 || true
  for f in logs/*.log; do
    echo "----- $f (tail) -----" >&2
    tail -n 15 "$f" >&2 || true
  done
  exit 1
fi

restarted=0
for svc in client-1 client-2 payments booking; do
  cid=""
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    cid="$(compose ps -q "$svc" | head -n 1 || true)"
    if [ -n "$cid" ]; then
      break
    fi
    sleep 1
  done
  if [ -z "$cid" ]; then
    echo "$svc has no container" >&2
    exit 1
  fi
  count="$(docker inspect -f '{{.RestartCount}}' "$cid")"
  echo "$svc restarts=$count"
  if [ "$count" -ge 1 ]; then
    restarted=$((restarted + 1))
  fi
done
if [ "$restarted" -lt 4 ]; then
  echo "expected each client and backend to panic and restart" >&2
  exit 1
fi

compose down --remove-orphans
echo "smoke ok"
