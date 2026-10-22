# Phase 1A on macOS

A Docker smoke test of the arm matrix. Vector and ClickHouse run in containers, so nothing has to be installed with Homebrew. The corpus is still [data/HDFS.log](../../data/HDFS.log), and the table is still the shared [ddl.sql](../clickhouse/ddl.sql).

Requires Docker running (Colima or Docker Desktop), plus `python3`, `jq`, and `shasum`, which macOS already has.

```bash
phase1a/macos/driver/replay.sh a2m 1 throttled
```

`replay.sh` starts ClickHouse, Prometheus, and Grafana. Kafka arms also start the `kafka` profile. The first run pulls `clickhouse/clickhouse-server:26.2` and `timberio/vector:latest-debian`; override with `CH_IMAGE` and `VECTOR_IMAGE` if a tag is missing.

## Metrics dashboard

Grafana is http://localhost:3000 (anonymous admin, no login). The home dashboard is **Phase 1A ClickHouse ingest**: insert rate, delayed/rejected inserts, merges, part count, memory, connections. Prometheus is http://localhost:9090. ClickHouse's own scrape endpoint is http://localhost:9363/metrics.

```bash
docker-compose -f phase1a/macos/docker-compose.yml up -d clickhouse prometheus grafana
```

Vector is only scraped while a replay is running (`localhost:9598`). The Vector target is **down** between runs; that is expected.

## Live generator ingest

This is a second stack on the same Compose project, not an arm of the HDFS matrix. Envoy generator services join ClickHouse; Vector tails `envoy-log-generator/logs` into `sysbench.logs_live`.

```bash
phase1a/macos/run-live.sh
```

`SMOKE=1` shortens the generator ramps to 60s (same as `envoy-log-generator/run.sh`) and leaves the stack up. Grafana dashboards **Envoy log generator** and **Live generator logs (ClickHouse)** appear next to the ingest dashboard. Stop a standalone `envoy-log-generator` compose project first so ports 10000 and 9901 are free. Stop live with:

```bash
docker-compose -f phase1a/macos/docker-compose.live.yml down
```

`replay.sh` and `run-live.sh` both publish Vector on host port 9598. Run one at a time.

## What this run is

Row count, parts, merges, and canary latency. The canary socket is port 9000 inside the Vector container and **9001 on the host**, because 9000 on the host is ClickHouse.

## What this run is not

CPU-seconds per GB is written as `null`. The page cache is not dropped, so `cache_state` is `docker` rather than `cold`. Results go to `results/phase1a-macos.ndjson` and stay out of the handbook.

The Vector configs here are the Linux configs with two substitutions: `node-1:8123` became `clickhouse:8123`, and `infra:9092` became `kafka:9092`. The VRL transform is unchanged. If you edit it, edit [linux/vector](../linux/vector) as well and re-apply those two substitutions.

## Drills

[DRILLS.md](DRILLS.md) is the Docker version of the failure drills. It can show loss and buffering. It cannot show a cgroup CPU cost.
