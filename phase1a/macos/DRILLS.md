# Phase 1A drills on macOS

The questions are the same as [the Linux runbook](../linux/DRILLS.md). The mechanism is Docker, and the CPU half of the answer does not exist here: there is no cgroup to read. Use these to see whether an arm loses acked rows or buffers across a restart. Use the rig for the cost.

Start a throttled run in one terminal, then act from another.

```bash
phase1a/macos/driver/replay.sh a4 1 throttled
```

The compose project name is the directory name, `macos`, so the ClickHouse container is `macos-clickhouse-1`. `docker-compose ps` in this directory prints it. The service name, which the commands below use, is `clickhouse`.

## D1 — ClickHouse stopped for 5 minutes

```bash
cd phase1a/macos
docker-compose stop clickhouse
sleep 300
docker-compose start clickhouse
```

Vector was started by `replay.sh` as the container `phase1a-vector`, so it does not show up as a compose service. The result that matters is the final row count in the replay terminal: 11,175,629, or the arm dropped data.

A2's memory buffer blocks the feeder, so a clean count here means the Python feeder was willing to block. Say that in the notes. A4's disk buffer is the arm that should absorb the outage without blocking the feeder for long.

## D2 — kill -9

```bash
docker kill --signal=KILL "$(docker-compose ps -q clickhouse)"
sleep 10
docker-compose start clickhouse
```

Acked-but-lost is Vector's sent-event count minus the table count, same definition as the Linux runbook. A3w0 is the arm expected to fail this.

## D3 — Kafka replay

Kafka has to be up (`replay.sh` starts it for `b1`). Kill ClickHouse the same way as D2, start it again, and let the run drain. Then:

```sql
SELECT count() - 11175629 AS delta
FROM sysbench.logs_hdfs
WHERE Component != 'sysbench.Canary';
```

`delta > 0` is duplicates from the Kafka engine's at-least-once delivery. `delta < 0` is loss.
