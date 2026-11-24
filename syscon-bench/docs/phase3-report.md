# Phase 3 report

- One engine at a time, for one hour, on the same 1,000,000-row file.
- Each event is one JSON log record, about 561 bytes. Fields: `message`, `uuid`, `instance-id`, `ip-address`, `host-name`, `log-severity`, `status-code`, `component`, `trace-id`, `version`, `service`, `time`, `jira-id`, `group-id`, `external-01`, `external-02`, `external-03`, `binary-field`.
- The `message` body is JSON 60%, access log 10%, plain text 10%, logfmt 10%, stack trace 5%, and audit 5%.
- Steady ingest is 20 events/s. A spikes hour keeps that base and raises two 10-minute windows to 200 events/s.
- Every hour runs 20 queries/s, split evenly: 25% full-text, 25% exact, 25% filter, and 25% aggregation. Full-text skips a token seen in more than 1% of events and picks the token whose document rate is closest to 0.01%. Aggregation sums `message` lengths in the last 60 seconds. The eight hours below were run at 30% full-text, 30% exact, and 40% filter, before aggregation existed.
- VMs, on the LAN, reached by SSH as `VM01`, `VM02`, and `VM03`:
  - VM01, 192.168.100.20, runs the generator, Prometheus, and Grafana.
  - VM02, 192.168.100.21, runs the database. It has 4 CPUs, 16,720,707,584 bytes of RAM, and a root disk `/dev/sda4` of 60,052,172,800 bytes.
  - VM03, 192.168.100.22, has no Docker, so Elasticsearch and VictoriaLogs ran on VM02 as well.
- Each engine uses its default settings. Indexed fields:
  - ClickHouse is ordered by `(service, time, uuid)` and has a token index on `message`.
  - Elasticsearch keeps keyword indexes on the fields and an inverted index on `message`.
  - Loki indexes only the labels `job`, `service`, `severity`, and `component`.
  - VictoriaLogs uses `service`, `component`, and `log-severity` as stream fields.
- Setup: [phase3.md](phase3.md).

Each section is one official hour. The table is the ingest rate across that hour.

CPU Usage is the 99th of the 5-second process CPU samples during that hour. Peak Used Mem is process resident memory, except Elasticsearch, which is JVM heap plus non-heap. Disk Storage is `du -sb` of the engine data directory: ClickHouse `/var/lib/clickhouse`, Elasticsearch `/usr/share/elasticsearch/data`, Loki `/tmp/loki`, VictoriaLogs `/vlogs`. Disk added is that directory after the case minus the same directory before preload. The query lines cover the whole hour. The ingest line is the last 5 minutes. The ingest timeline is one row per minute. A timed-out query is left out of the latency.

## Disk added

The rows below are the older filesystem gauge, kept for the hours already recorded. New sections use `du -sb` of the data directory.

| Case | Before | After | Disk added | Data before | Data after | Data added |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| ClickHouse steady | 32294604800 | 34657603584 | 2362998784 | 21080521 | 487340329 | 466259808 |
| ClickHouse spikes | 32819134464 | 34286297088 | 1467162624 | 90229594 | 644778951 | 554549357 |
| Elasticsearch steady | 32430481408 | 32456400896 | 25919488 | 59251090 | 313939830 | 254688740 |
| Elasticsearch spikes | 32596865024 | 32486047744 | -110817280 | 104287877 | 389557989 | 285270112 |

Elasticsearch spikes disk added is negative. The filesystem gauge moved the other way while the index grew. Data added is the comparable number.

## clickhouse / phase3-clickhouse-steady

- Preload rows: 1000000
- Window: 2026-11-23T15:00:47Z to 2026-11-23T16:00:51Z
- ingest (events/s): 19.322033898305083
- query avg (ms): 10.128
- query p95 (ms): 23.213
- query p99 (ms): 31.733
- query p99 full-text (ms): 24.602
- query p99 exact (ms): 44.435
- query p99 filter (ms): 23.704
- query p99 aggregation (ms): no series
- Peak Used Mem (bytes): 1443676160
- CPU Usage (cores): 0.578
- Disk Storage (bytes): 34665644032
- Disk added (bytes): 2362998784

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-23T15:00:47Z | 12.18 |
| 2026-11-23T15:05:47Z | 20.34 |
| 2026-11-23T15:10:47Z | 20.34 |
| 2026-11-23T15:15:47Z | 20.34 |
| 2026-11-23T15:20:47Z | 20.34 |
| 2026-11-23T15:25:47Z | 20.34 |
| 2026-11-23T15:30:47Z | 20.34 |
| 2026-11-23T15:35:47Z | 20.34 |
| 2026-11-23T15:40:47Z | 20.34 |
| 2026-11-23T15:45:47Z | 20.34 |
| 2026-11-23T15:50:47Z | 20.34 |
| 2026-11-23T15:55:47Z | 20.34 |
| 2026-11-23T16:00:47Z | 18.47 |


## clickhouse / phase3-clickhouse-spikes

- Preload rows: 1000000
- Window: 2026-11-23T16:05:53Z to 2026-11-23T17:05:56Z
- ingest (events/s): 19.322033898305083
- query avg (ms): 10.209
- query p95 (ms): 23.176
- query p99 (ms): 32.343
- query p99 full-text (ms): 24.590
- query p99 exact (ms): 44.337
- query p99 filter (ms): 23.768
- query p99 aggregation (ms): no series
- Peak Used Mem (bytes): 1376587776
- CPU Usage (cores): 0.593
- Disk Storage (bytes): 34347249664
- Disk added (bytes): 1467162624

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-23T16:05:53Z | 49.13 |
| 2026-11-23T16:10:53Z | 20.34 |
| 2026-11-23T16:15:53Z | 20.34 |
| 2026-11-23T16:20:53Z | 28.89 |
| 2026-11-23T16:25:53Z | 203.39 |
| 2026-11-23T16:30:53Z | 194.84 |
| 2026-11-23T16:35:53Z | 20.34 |
| 2026-11-23T16:40:53Z | 20.34 |
| 2026-11-23T16:45:53Z | 28.89 |
| 2026-11-23T16:50:53Z | 203.39 |
| 2026-11-23T16:55:53Z | 194.84 |
| 2026-11-23T17:00:53Z | 20.34 |
| 2026-11-23T17:05:53Z | 18.36 |


## elasticsearch / phase3-elasticsearch-steady

- Preload rows: 1000000
- Window: 2026-11-23T17:11:46Z to 2026-11-23T18:11:49Z
- ingest (events/s): 19.322033898305083
- query avg (ms): 1.775
- query p95 (ms): 4.763
- query p99 (ms): 4.964
- query p99 full-text (ms): 4.962
- query p99 exact (ms): 4.961
- query p99 filter (ms): 4.967
- query p99 aggregation (ms): no series
- Peak Used Mem (bytes): 655493472
- CPU Usage (cores): 0.206
- Disk Storage (bytes): 32453980160
- Disk added (bytes): 25919488

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-23T17:11:46Z | 12.36 |
| 2026-11-23T17:16:46Z | 20.34 |
| 2026-11-23T17:21:46Z | 20.34 |
| 2026-11-23T17:26:46Z | 20.34 |
| 2026-11-23T17:31:46Z | 20.34 |
| 2026-11-23T17:36:46Z | 20.34 |
| 2026-11-23T17:41:46Z | 20.34 |
| 2026-11-23T17:46:46Z | 20.34 |
| 2026-11-23T17:51:46Z | 20.34 |
| 2026-11-23T17:56:46Z | 20.34 |
| 2026-11-23T18:01:46Z | 20.34 |
| 2026-11-23T18:06:46Z | 20.34 |
| 2026-11-23T18:11:46Z | 19.36 |


## elasticsearch / phase3-elasticsearch-spikes

- Preload rows: 1000000
- Window: 2026-11-23T18:17:40Z to 2026-11-23T19:17:43Z
- ingest (events/s): 19.322033898305083
- query avg (ms): 1.890
- query p95 (ms): 4.776
- query p99 (ms): 4.977
- query p99 full-text (ms): 4.971
- query p99 exact (ms): 4.973
- query p99 filter (ms): 4.985
- query p99 aggregation (ms): no series
- Peak Used Mem (bytes): 655705528
- CPU Usage (cores): 0.236
- Disk Storage (bytes): 32487010304
- Disk added (bytes): -110817280

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-23T18:17:40Z | 49.86 |
| 2026-11-23T18:22:40Z | 20.34 |
| 2026-11-23T18:27:40Z | 20.34 |
| 2026-11-23T18:32:40Z | 28.88 |
| 2026-11-23T18:37:40Z | 203.39 |
| 2026-11-23T18:42:40Z | 194.84 |
| 2026-11-23T18:47:40Z | 20.34 |
| 2026-11-23T18:52:40Z | 20.34 |
| 2026-11-23T18:57:40Z | 28.88 |
| 2026-11-23T19:02:40Z | 203.39 |
| 2026-11-23T19:07:40Z | 194.84 |
| 2026-11-23T19:12:40Z | 20.34 |
| 2026-11-23T19:17:40Z | 18.17 |


## loki / phase3-loki-steady

- Preload rows: 1000000
- Window: 2026-11-23T19:23:43Z to 2026-11-23T20:24:47Z
- ingest (events/s): 15.254237288135593
- query avg (ms): 19540.688
- query p95 (ms): 29000.000
- query p99 (ms): 29800.000
- query p99 full-text (ms): 29800.000
- query p99 exact (ms): 29800.000
- query p99 filter (ms): 29800.000
- query p99 aggregation (ms): no series
- Peak Used Mem (bytes): 11132841984
- CPU Usage (cores): 3.577
- Disk Storage (bytes): no series

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-23T19:23:43Z | 12.22 |
| 2026-11-23T19:28:43Z | 20.34 |
| 2026-11-23T19:33:43Z | 20.34 |
| 2026-11-23T19:38:43Z | 20.34 |
| 2026-11-23T19:43:43Z | 20.34 |
| 2026-11-23T19:48:43Z | 20.34 |
| 2026-11-23T19:53:43Z | 20.34 |
| 2026-11-23T19:58:43Z | 20.34 |
| 2026-11-23T20:03:43Z | 20.34 |
| 2026-11-23T20:08:43Z | 20.34 |
| 2026-11-23T20:13:43Z | 20.34 |
| 2026-11-23T20:18:43Z | 20.34 |
| 2026-11-23T20:23:43Z | 18.31 |


## loki / phase3-loki-spikes

- Preload rows: 1000000
- Window: 2026-11-23T20:31:10Z to 2026-11-23T21:32:13Z
- ingest (events/s): 16.338983050847457
- query avg (ms): 22407.732
- query p95 (ms): 29000.000
- query p99 (ms): 29800.000
- query p99 full-text (ms): 29800.000
- query p99 exact (ms): 29800.000
- query p99 filter (ms): 29800.000
- query p99 aggregation (ms): no series
- Peak Used Mem (bytes): 12041076736
- CPU Usage (cores): 3.597
- Disk Storage (bytes): no series

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-23T20:31:10Z | 49.32 |
| 2026-11-23T20:36:10Z | 20.34 |
| 2026-11-23T20:41:10Z | 20.34 |
| 2026-11-23T20:46:10Z | 28.88 |
| 2026-11-23T20:51:10Z | 203.39 |
| 2026-11-23T20:56:10Z | 194.84 |
| 2026-11-23T21:01:10Z | 20.34 |
| 2026-11-23T21:06:10Z | 20.34 |
| 2026-11-23T21:11:10Z | 28.89 |
| 2026-11-23T21:16:10Z | 203.39 |
| 2026-11-23T21:21:10Z | 194.84 |
| 2026-11-23T21:26:10Z | 20.34 |
| 2026-11-23T21:31:10Z | 18.31 |


## victorialogs / phase3-victorialogs-steady

- Preload rows: 1000000
- Window: 2026-11-23T21:36:13Z to 2026-11-23T22:36:16Z
- ingest (events/s): 20.406779661016948
- query avg (ms): 385.012
- query p95 (ms): 1782.776
- query p99 (ms): 2378.316
- query p99 full-text (ms): 2433.291
- query p99 exact (ms): 2383.005
- query p99 filter (ms): 2290.447
- query p99 aggregation (ms): no series
- Peak Used Mem (bytes): 242237440
- CPU Usage (cores): 3.452
- Disk Storage (bytes): 32132452352

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-23T21:36:13Z | 13.62 |
| 2026-11-23T21:41:13Z | 20.34 |
| 2026-11-23T21:46:13Z | 20.34 |
| 2026-11-23T21:51:13Z | 20.34 |
| 2026-11-23T21:56:13Z | 20.34 |
| 2026-11-23T22:01:13Z | 20.34 |
| 2026-11-23T22:06:13Z | 20.34 |
| 2026-11-23T22:11:13Z | 20.34 |
| 2026-11-23T22:16:13Z | 20.34 |
| 2026-11-23T22:21:13Z | 20.34 |
| 2026-11-23T22:26:13Z | 20.34 |
| 2026-11-23T22:31:13Z | 20.34 |
| 2026-11-23T22:36:13Z | 19.29 |


## victorialogs / phase3-victorialogs-spikes

- Preload rows: 1000000
- Window: 2026-11-23T22:40:08Z to 2026-11-23T23:40:20Z
- ingest (events/s): 19.322033898305083
- query avg (ms): 5561.025
- query p95 (ms): 27004.345
- query p99 (ms): 29400.869
- query p99 full-text (ms): 29513.997
- query p99 exact (ms): 29448.951
- query p99 filter (ms): 29213.446
- query p99 aggregation (ms): no series
- Peak Used Mem (bytes): 323289088
- CPU Usage (cores): 3.480
- Disk Storage (bytes): 32403435520

### Ingest timeline

| time | events/s |
| --- | ---: |
| 2026-11-23T22:40:08Z | 52.68 |
| 2026-11-23T22:45:08Z | 20.34 |
| 2026-11-23T22:50:08Z | 20.34 |
| 2026-11-23T22:55:08Z | 28.88 |
| 2026-11-23T23:00:08Z | 203.39 |
| 2026-11-23T23:05:08Z | 194.84 |
| 2026-11-23T23:10:08Z | 20.34 |
| 2026-11-23T23:15:08Z | 20.34 |
| 2026-11-23T23:20:08Z | 28.88 |
| 2026-11-23T23:25:08Z | 203.39 |
| 2026-11-23T23:30:08Z | 194.84 |
| 2026-11-23T23:35:08Z | 20.34 |
| 2026-11-23T23:40:08Z | 19.36 |

