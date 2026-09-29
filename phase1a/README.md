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
