# Phase 1A harness

Two ways to run the same arm matrix. The SQL is shared ([clickhouse/](clickhouse/), [collect.sql](collect.sql)); the way ClickHouse is started is not.

| | [linux/](linux/driver/replay.sh) | [macos/](macos/driver/replay.sh) |
| --- | --- | --- |
| Where | driver node of the 3-node rig | this Mac, via Docker |
| What it answers | batching and the Kafka question, with CPU per GB | whether the pipeline actually ingests and the count gate holds |
| Page cache | dropped between runs | not dropped |
| CPU-seconds per GB | measured from cgroup v2 | `null` |
| Results | `results/phase1a.ndjson` (`host: linux`) | `results/phase1a-macos.ndjson` (`host: macos`) |

```bash
phase1a/linux/driver/replay.sh a2m 1 throttled
phase1a/macos/driver/replay.sh a2m 1 throttled
```

A macOS record must not be compared with a Linux record and must not go into the handbook sizing table. `host` is on every record so a mixed file is still separable.

On the Mac stack, Grafana is http://localhost:3000 after `replay.sh` (or `docker-compose up -d clickhouse prometheus grafana` in `phase1a/macos`). That dashboard is for watching the smoke test, not for handbook sizing.

## Live generator ingest

[envoy-log-generator](../envoy-log-generator/) writes app, client, and Envoy logs. Vector tails those files into `sysbench.logs_live` (see [clickhouse/ddl-envoy.sql](clickhouse/ddl-envoy.sql)). That table is separate from `logs_hdfs`, and `reset.sql` does not drop it.

```bash
phase1a/macos/run-live.sh
# Linux rig, after the generator is writing into ENVOY_LOG_DIR:
ENVOY_LOG_DIR=/path/to/envoy-log-generator/logs phase1a/linux/driver/run-live.sh
```

Do not run live ingest in the same stack as `replay.sh` (shared Vector port 9598, and the HDFS replay would mix with generator traffic on the dashboards).
